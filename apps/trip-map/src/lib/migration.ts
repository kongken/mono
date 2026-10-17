import { MAX_BATCH_OPS, diffSaved, sameCity, sameTrip } from "./sync.ts";
import type { Saved, Trip, Visit } from "./travel.ts";
import { canonicalTrips, legacyTripId } from "./trips.ts";

/** A record that exists on both sides with different content. */
export type MigrationConflict =
  | { kind: "visit"; key: string; local: Visit; cloud: Visit }
  | { kind: "trip"; key: string; code: string; city: string; local: Trip; cloud: Trip }
  | { kind: "mark"; key: string; province: string; local: string; cloud: string };

export type NewTrip = { code: string; city: string; trip: Trip };

export type MigrationPlan = {
  /** Cities only on this device, with all of their trips; they are uploaded. */
  newVisits: Visit[];
  /** Trips only on this device, in cities the cloud has too; they are added. */
  newTrips: NewTrip[];
  /** Province marks only on this device; they are uploaded. */
  newMarks: { province: string; color: string }[];
  conflicts: MigrationConflict[];
  /** Records identical on both sides. */
  same: number;
  /** Cloud records this device does not have; they are always kept. */
  cloudOnly: number;
  /**
   * Trips of this device whose ID already belongs to a trip of another city in
   * the cloud. Trip IDs are unique per account, so they are uploaded under a new
   * ID (listed here); newVisits / newTrips / conflicts already carry the new ID.
   */
  remapped: { code: string; city: string; from: string; to: string }[];
};

/** Per-conflict choice; a missing entry means the cloud copy is kept. */
export type MigrationChoices = Record<string, "local" | "cloud">;

export const visitKey = (code: string) => `v:${code}`;
export const tripKey = (id: string) => `t:${id}`;
export const markKey = (province: string) => `m:${province}`;

/**
 * Compares city by city, then trip by trip. A trip is the same record when its
 * ID matches; a local trip whose ID is unknown to the cloud but whose date and
 * note equal one of that city's cloud trips is the same trip recorded twice,
 * not a new one. Everything else on this device is added; nothing in the cloud
 * is touched unless the user picks the local version of a conflict.
 */
/**
 * The ID a colliding trip is uploaded under: derived from the old ID and the
 * city, so recomputing the plan always gives the same result, and extended
 * until it is free among every ID of either side.
 */
export function remappedTripId(id: string, code: string, taken: ReadonlySet<string>): string {
  const base = `${id.slice(0, 40)}-m-${code}`;
  for (let n = 1; ; n++) {
    const candidate = n === 1 ? base : `${base}-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
}

export function planMigration(local: Saved, cloud: Saved): MigrationPlan {
  const cloudVisits = new Map(cloud.visits.map((v) => [v.code, v]));
  const plan: MigrationPlan = {
    newVisits: [],
    newTrips: [],
    newMarks: [],
    conflicts: [],
    same: 0,
    cloudOnly: 0,
    remapped: [],
  };
  // An ID may exist once per account, whichever city it belongs to.
  const cloudOwner = new Map(cloud.visits.flatMap((v) => v.trips.map((t) => [t.id, v.code] as const)));
  const taken = new Set([...cloudOwner.keys(), ...local.visits.flatMap((v) => v.trips.map((t) => t.id))]);
  const rehome = (visit: Visit, trip: Trip): Trip => {
    const owner = cloudOwner.get(trip.id);
    if (owner === undefined || owner === visit.code) return trip;
    const id = remappedTripId(trip.id, visit.code, taken);
    taken.add(id);
    plan.remapped.push({ code: visit.code, city: visit.name, from: trip.id, to: id });
    return { ...trip, id };
  };
  const localCodes = new Set<string>();
  for (const visit of local.visits) {
    localCodes.add(visit.code);
    const remote = cloudVisits.get(visit.code);
    if (!remote) {
      plan.newVisits.push({ ...visit, trips: canonicalTrips(canonicalTrips(visit.trips).map((t) => rehome(visit, t))) });
      continue;
    }
    if (sameCity(visit, remote)) plan.same++;
    else plan.conflicts.push({ kind: "visit", key: visitKey(visit.code), local: visit, cloud: remote });
    const remoteTrips = new Map(remote.trips.map((t) => [t.id, t]));
    const matched = new Set<string>();
    for (const trip of canonicalTrips(visit.trips)) {
      const same = remoteTrips.get(trip.id);
      if (same) {
        matched.add(same.id);
        if (sameTrip(trip, same)) plan.same++;
        else
          plan.conflicts.push({
            kind: "trip",
            key: tripKey(trip.id),
            code: visit.code,
            city: visit.name,
            local: trip,
            cloud: same,
          });
        continue;
      }
      const twin = canonicalTrips(remote.trips).find((t) => !matched.has(t.id) && !visit.trips.some((o) => o.id === t.id) && sameTrip(t, trip));
      if (twin) {
        matched.add(twin.id);
        plan.same++;
      } else plan.newTrips.push({ code: visit.code, city: visit.name, trip: rehome(visit, trip) });
    }
    plan.cloudOnly += remote.trips.filter((t) => !matched.has(t.id) && !visit.trips.some((o) => o.id === t.id)).length;
  }
  for (const [province, color] of Object.entries(local.provinces)) {
    const remote = cloud.provinces[province];
    if (remote === undefined) plan.newMarks.push({ province, color });
    else if (remote === color) plan.same++;
    else
      plan.conflicts.push({
        kind: "mark",
        key: markKey(province),
        province,
        local: color,
        cloud: remote,
      });
  }
  plan.cloudOnly +=
    cloud.visits.filter((v) => !localCodes.has(v.code)).length +
    Object.keys(cloud.provinces).filter((p) => !(p in local.provinces)).length;
  return plan;
}

/** True when migrating would change nothing in the cloud. */
export const isNoop = (plan: MigrationPlan) =>
  !plan.newVisits.length && !plan.newTrips.length && !plan.newMarks.length && !plan.conflicts.length;

/**
 * The exact number of operations the migration is sent as. One migration is one
 * atomic batch, so it must not exceed what the server accepts in a batch.
 */
export function migrationOps(cloud: Saved, plan: MigrationPlan, choices: MigrationChoices): number {
  return diffSaved(cloud, migratedSaved(cloud, plan, choices)).length;
}
export const migrationFits = (count: number) => count <= MAX_BATCH_OPS;

/** True when two states hold the same cities, trips and marks. */
export const sameSaved = (a: Saved, b: Saved) => diffSaved(a, b).length === 0;

/** What a confirmation would do, for the preview line. */
export function summarize(plan: MigrationPlan, choices: MigrationChoices) {
  const overwrite = plan.conflicts.filter((c) => choices[c.key] === "local").length;
  return {
    added: plan.newVisits.length + plan.newTrips.length + plan.newMarks.length,
    addedTrips:
      plan.newTrips.length + plan.newVisits.reduce((total, v) => total + v.trips.length, 0),
    overwritten: overwrite,
    keptCloud: plan.conflicts.length - overwrite,
    remapped: plan.remapped.length,
    same: plan.same,
    cloudOnly: plan.cloudOnly,
  };
}

/**
 * The cloud state after migration: all cloud records stay, local-only records
 * are added, and each conflict follows its choice. Nothing is ever deleted.
 */
export function migratedSaved(
  cloud: Saved,
  plan: MigrationPlan,
  choices: MigrationChoices,
): Saved {
  const provinces = { ...cloud.provinces };
  const cityOverride = new Map<string, Visit>();
  const tripOverride = new Map<string, Trip>();
  for (const c of plan.conflicts) {
    if (choices[c.key] !== "local") continue;
    if (c.kind === "visit") cityOverride.set(c.local.code, c.local);
    else if (c.kind === "trip") tripOverride.set(c.local.id, c.local);
    else provinces[c.province] = c.local;
  }
  for (const { province, color } of plan.newMarks) provinces[province] = color;
  const added = new Map<string, Trip[]>();
  for (const { code, trip } of plan.newTrips) added.set(code, [...(added.get(code) ?? []), trip]);
  return {
    version: 2,
    visits: [
      ...cloud.visits.map((v) => {
        const local = cityOverride.get(v.code);
        const trips = [...v.trips.map((t) => tripOverride.get(t.id) ?? t), ...(added.get(v.code) ?? [])];
        return {
          code: v.code,
          name: local?.name ?? v.name,
          province: local?.province ?? v.province,
          color: local?.color ?? v.color,
          trips: canonicalTrips(trips),
        };
      }),
      ...plan.newVisits,
    ],
    provinces,
  };
}

/** Compact fingerprint of local data, so a changed local copy is offered again. */
export function fingerprint(saved: Saved): string {
  const text = JSON.stringify([
    [...saved.visits]
      .sort((a, b) => a.code.localeCompare(b.code))
      .map((v) => [v.code, v.name, v.province, v.color, canonicalTrips(v.trips).map((t) => [t.id, t.date, t.note])]),
    Object.entries(saved.provinces).sort(([a], [b]) => a.localeCompare(b)),
  ]);
  return `v2:${saved.visits.length}:${Object.keys(saved.provinces).length}:${hash(text)}`;
}

function hash(text: string) {
  let h = 5381;
  for (let i = 0; i < text.length; i++) h = ((h << 5) + h + text.charCodeAt(i)) | 0;
  return (h >>> 0).toString(36);
}

/**
 * The fingerprint earlier versions stored for the same data, so a migration
 * that was already done or declined is not offered again after the upgrade.
 * Only data that earlier versions could hold has one: every city with exactly
 * its single `legacy-<code>` trip. Otherwise null.
 */
export function legacyFingerprint(saved: Saved): string | null {
  if (!saved.visits.every((v) => v.trips.length === 1 && v.trips[0].id === legacyTripId(v.code)))
    return null;
  const text = JSON.stringify([
    [...saved.visits]
      .sort((a, b) => a.code.localeCompare(b.code))
      .map((v) => ({
        code: v.code,
        name: v.name,
        province: v.province,
        color: v.color,
        date: v.trips[0].date,
        note: v.trips[0].note,
      })),
    Object.entries(saved.provinces).sort(([a], [b]) => a.localeCompare(b)),
  ]);
  return `${saved.visits.length}:${Object.keys(saved.provinces).length}:${hash(text)}`;
}
