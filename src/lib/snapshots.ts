import { diffSaved } from "./sync.ts";
import type { Saved, Visit } from "./travel.ts";

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

type DataLike = {
  visits: Visit[];
  provinceMarks: { province: string; color: string }[];
};

/** The footprints a snapshot holds, in the app's own shape. */
export function snapshotToSaved(data: DataLike): Saved {
  return {
    version: 1,
    visits: data.visits.map((v) => ({
      code: v.code,
      name: v.name,
      province: v.province,
      color: v.color,
      date: v.date,
      note: v.note,
    })),
    provinces: Object.fromEntries(
      data.provinceMarks.map((m) => [m.province, m.color]),
    ),
  };
}

export type RestorePlan = {
  /** City records the restore brings back. */
  addedVisits: number;
  /** City records whose content is rewritten to the snapshot's. */
  changedVisits: number;
  /** City records that exist now but not in the snapshot; they are removed. */
  removedVisits: number;
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
    addedMarks: 0,
    changedMarks: 0,
    removedMarks: 0,
    unchanged: 0,
  };
  const ops = diffSaved(current, target);
  const hadVisit = new Set(current.visits.map((v) => v.code));
  for (const op of ops) {
    switch (op.type) {
      case "upsertVisit":
        if (hadVisit.has(op.visit.code)) plan.changedVisits++;
        else plan.addedVisits++;
        break;
      case "deleteVisit":
        plan.removedVisits++;
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
  const total =
    new Set([...current.visits, ...target.visits].map((v) => v.code)).size +
    new Set([
      ...Object.keys(current.provinces),
      ...Object.keys(target.provinces),
    ]).size;
  plan.unchanged = total - ops.length;
  return plan;
}

export const planChanges = (plan: RestorePlan) =>
  plan.addedVisits +
  plan.changedVisits +
  plan.removedVisits +
  plan.addedMarks +
  plan.changedMarks +
  plan.removedMarks;

/** True when the account already holds any footprints worth protecting. */
export const hasFootprints = (saved: Saved) =>
  saved.visits.length > 0 || Object.keys(saved.provinces).length > 0;
