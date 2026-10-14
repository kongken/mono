import { diffSaved, sameCity, sameTrip } from "./sync.ts";
import type { Saved, Trip } from "./travel.ts";
import { canonicalTrips, legacyTripId } from "./trips.ts";
import { regionCatalog, RegionCompatibilityError } from "./region-catalog.ts";

export type SnapshotReason =
  | "manual"
  | "scheduled"
  | "beforeMigration"
  | "beforeRestore";

export type SnapshotInfo = {
  id: string;
  reason: SnapshotReason;
  /** Milliseconds since the epoch. */
  createdAt: number;
  visitCount: number;
  /** Trips in the snapshot; one per city for snapshots saved before several trips per city. */
  tripCount: number;
  markCount: number;
  /** The account revision the snapshot was taken at. */
  revision: number;
};

export const reasonLabel: Record<SnapshotReason, string> = {
  manual: "手动保存",
  scheduled: "定时保存",
  beforeMigration: "迁移前自动保存",
  beforeRestore: "恢复前自动保存",
};

type CityLike = { code: string; name: string; province: string; color: string; date?: string; note?: string };
type DataLike = {
  visits: CityLike[];
  trips?: { id: string; regionCode: string; date: string; note: string }[];
  /** 2 when the data carries trips; otherwise each city carries one date and note. */
  schemaVersion?: number;
  regionVersion?: string;
  provinceMarks: { province: string; color: string }[];
};

/** The footprints a snapshot holds, in the app's own shape. */
export function snapshotToSaved(data: DataLike, catalog = regionCatalog): Saved {
  catalog.assertVersion(data.regionVersion);
  const seen = new Set<string>();
  const tripped = (data.schemaVersion ?? 0) >= 2;
  const byCity = new Map<string, Trip[]>();
  for (const t of data.trips ?? [])
    byCity.set(t.regionCode, [...(byCity.get(t.regionCode) ?? []), { id: t.id, date: t.date, note: t.note }]);
  return {
    version: 2,
    visits: data.visits.map((v) => {
      const city = catalog.city(v.code, v.name, v.province);
      if (!city || seen.has(city.code)) throw new RegionCompatibilityError("区域迁移存在未映射记录或 ID 碰撞，原数据已保留");
      seen.add(city.code);
      return {
      ...city,
      color: v.color,
      trips: tripped
        ? canonicalTrips(byCity.get(v.code) ?? [])
        : [{ id: legacyTripId(v.code), date: v.date ?? "", note: v.note ?? "" }],
    }; }),
    provinces: Object.fromEntries(
      data.provinceMarks.map((m) => [m.province, m.color]),
    ),
  };
}

export type RestorePlan = {
  /** City records the restore brings back. */
  addedVisits: number;
  /** City records whose color is rewritten to the snapshot's. */
  changedVisits: number;
  /** City records that exist now but not in the snapshot; they are removed. */
  removedVisits: number;
  /** Trips the restore brings back, including those of cities it brings back. */
  addedTrips: number;
  /** Trips whose date or note is rewritten to the snapshot's. */
  changedTrips: number;
  /** Trips that exist now but not in the snapshot, including those of removed cities. */
  removedTrips: number;
  addedMarks: number;
  changedMarks: number;
  removedMarks: number;
  /** Records that are already identical. */
  unchanged: number;
};

/** What restoring `target` would do to `current`, for the confirmation preview. */
export function planRestore(current: Saved, target: Saved): RestorePlan {
  const plan: RestorePlan = {
    addedVisits: 0,
    changedVisits: 0,
    removedVisits: 0,
    addedTrips: 0,
    changedTrips: 0,
    removedTrips: 0,
    addedMarks: 0,
    changedMarks: 0,
    removedMarks: 0,
    unchanged: 0,
  };
  const ops = diffSaved(current, target);
  const have = new Map(current.visits.map((v) => [v.code, v]));
  const haveTrip = new Set(current.visits.flatMap((v) => v.trips.map((t) => t.id)));
  for (const op of ops) {
    switch (op.type) {
      case "upsertVisit":
        if (have.has(op.visit.code)) plan.changedVisits++;
        else plan.addedVisits++;
        break;
      case "deleteVisit":
        plan.removedVisits++;
        plan.removedTrips += have.get(op.code)?.trips.length ?? 0;
        break;
      case "upsertTrip":
        if (haveTrip.has(op.trip.id)) plan.changedTrips++;
        else plan.addedTrips++;
        break;
      case "deleteTrip":
        plan.removedTrips++;
        break;
      case "upsertMark":
        if (op.province in current.provinces) plan.changedMarks++;
        else plan.addedMarks++;
        break;
      case "deleteMark":
        plan.removedMarks++;
        break;
    }
  }
  for (const v of current.visits) {
    const t = target.visits.find((o) => o.code === v.code);
    if (!t) continue;
    if (sameCity(v, t)) plan.unchanged++;
    for (const trip of v.trips) {
      const other = t.trips.find((o) => o.id === trip.id);
      if (other && sameTrip(trip, other)) plan.unchanged++;
    }
  }
  for (const [province, color] of Object.entries(current.provinces))
    if (target.provinces[province] === color) plan.unchanged++;
  return plan;
}

export const planChanges = (plan: RestorePlan) =>
  plan.addedVisits +
  plan.changedVisits +
  plan.removedVisits +
  plan.addedTrips +
  plan.changedTrips +
  plan.removedTrips +
  plan.addedMarks +
  plan.changedMarks +
  plan.removedMarks;

/** True when the account already holds any footprints worth protecting. */
export const hasFootprints = (saved: Saved) =>
  saved.visits.length > 0 || Object.keys(saved.provinces).length > 0;
