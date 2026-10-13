import { sameVisit } from "./sync.ts";
import type { Saved, Visit } from "./travel.ts";

/** A record that exists on both sides with different content. */
export type MigrationConflict =
  | { kind: "visit"; key: string; local: Visit; cloud: Visit }
  | { kind: "mark"; key: string; province: string; local: string; cloud: string };

export type MigrationPlan = {
  /** Cities only on this device; they are uploaded. */
  newVisits: Visit[];
  /** Province marks only on this device; they are uploaded. */
  newMarks: { province: string; color: string }[];
  conflicts: MigrationConflict[];
  /** Records identical on both sides. */
  same: number;
  /** Cloud records this device does not have; they are always kept. */
  cloudOnly: number;
};

/** Per-conflict choice; a missing entry means the cloud copy is kept. */
export type MigrationChoices = Record<string, "local" | "cloud">;

export const visitKey = (code: string) => `v:${code}`;
export const markKey = (province: string) => `m:${province}`;

export function planMigration(local: Saved, cloud: Saved): MigrationPlan {
  const cloudVisits = new Map(cloud.visits.map((v) => [v.code, v]));
  const plan: MigrationPlan = {
    newVisits: [],
    newMarks: [],
    conflicts: [],
    same: 0,
    cloudOnly: 0,
  };
  const localCodes = new Set<string>();
  for (const visit of local.visits) {
    localCodes.add(visit.code);
    const remote = cloudVisits.get(visit.code);
    if (!remote) plan.newVisits.push(visit);
    else if (sameVisit(visit, remote)) plan.same++;
    else
      plan.conflicts.push({
        kind: "visit",
        key: visitKey(visit.code),
        local: visit,
        cloud: remote,
      });
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
  plan.cloudOnly =
    cloud.visits.filter((v) => !localCodes.has(v.code)).length +
    Object.keys(cloud.provinces).filter((p) => !(p in local.provinces)).length;
  return plan;
}

/** True when migrating would change nothing in the cloud. */
export const isNoop = (plan: MigrationPlan) =>
  !plan.newVisits.length && !plan.newMarks.length && !plan.conflicts.length;

/** What a confirmation would do, for the preview line. */
export function summarize(plan: MigrationPlan, choices: MigrationChoices) {
  const overwrite = plan.conflicts.filter((c) => choices[c.key] === "local").length;
  return {
    added: plan.newVisits.length + plan.newMarks.length,
    overwritten: overwrite,
    keptCloud: plan.conflicts.length - overwrite,
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
  const replace = new Map<string, Visit>();
  const provinces = { ...cloud.provinces };
  for (const c of plan.conflicts) {
    if (choices[c.key] !== "local") continue;
    if (c.kind === "visit") replace.set(c.local.code, c.local);
    else provinces[c.province] = c.local;
  }
  for (const { province, color } of plan.newMarks) provinces[province] = color;
  return {
    version: 1,
    visits: [
      ...cloud.visits.map((v) => replace.get(v.code) ?? v),
      ...plan.newVisits,
    ],
    provinces,
  };
}

/** Compact fingerprint of local data, so a changed local copy is offered again. */
export function fingerprint(saved: Saved): string {
  const text = JSON.stringify([
    [...saved.visits].sort((a, b) => a.code.localeCompare(b.code)),
    Object.entries(saved.provinces).sort(([a], [b]) => a.localeCompare(b)),
  ]);
  let h = 5381;
  for (let i = 0; i < text.length; i++) h = ((h << 5) + h + text.charCodeAt(i)) | 0;
  return `${saved.visits.length}:${Object.keys(saved.provinces).length}:${(h >>> 0).toString(36)}`;
}
