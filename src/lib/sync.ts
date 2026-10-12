import type { Saved, Visit } from "./travel.ts";

/** One change to the cloud copy; diffs between two Saved states are lists of these. */
export type Op =
  | { type: "upsertVisit"; visit: Visit }
  | { type: "deleteVisit"; code: string }
  | { type: "upsertMark"; province: string; color: string }
  | { type: "deleteMark"; province: string };

const sameVisit = (a: Visit, b: Visit) =>
  a.name === b.name &&
  a.province === b.province &&
  a.color === b.color &&
  a.date === b.date &&
  a.note === b.note;

/**
 * Operations that turn `base` into `next`. Records present in both and equal
 * produce nothing, so a stale or partial view can never touch other records.
 */
export function diffSaved(base: Saved, next: Saved): Op[] {
  const ops: Op[] = [];
  const before = new Map(base.visits.map((v) => [v.code, v]));
  const after = new Map(next.visits.map((v) => [v.code, v]));
  for (const code of before.keys())
    if (!after.has(code)) ops.push({ type: "deleteVisit", code });
  for (const [code, visit] of after) {
    const old = before.get(code);
    if (!old || !sameVisit(old, visit)) ops.push({ type: "upsertVisit", visit });
  }
  for (const province of Object.keys(base.provinces))
    if (!(province in next.provinces)) ops.push({ type: "deleteMark", province });
  for (const [province, color] of Object.entries(next.provinces))
    if (base.provinces[province] !== color)
      ops.push({ type: "upsertMark", province, color });
  return ops;
}

export function applyOp(saved: Saved, op: Op): Saved {
  switch (op.type) {
    case "upsertVisit":
      return {
        ...saved,
        visits: [...saved.visits.filter((v) => v.code !== op.visit.code), op.visit],
      };
    case "deleteVisit":
      return { ...saved, visits: saved.visits.filter((v) => v.code !== op.code) };
    case "upsertMark":
      return { ...saved, provinces: { ...saved.provinces, [op.province]: op.color } };
    case "deleteMark": {
      const provinces = { ...saved.provinces };
      delete provinces[op.province];
      return { ...saved, provinces };
    }
  }
}

export type CloudSnapshot = { saved: Saved; revision: number };

/** The footprint API the engine talks to; apply resolves to the new revision. */
export interface FootprintApi {
  get(): Promise<CloudSnapshot>;
  apply(op: Op): Promise<number>;
}

export type SyncStatus = "idle" | "syncing" | "error";

export type SyncListener = {
  onStatus?(status: SyncStatus): void;
  /** Called after each confirmed operation with the server-confirmed state. */
  onConfirmed?(snapshot: CloudSnapshot): void;
  onError?(error: unknown): void;
};

/**
 * Sends the difference between the server-confirmed state and the latest
 * desired state, one operation at a time. The base only ever advances by
 * operations the server acknowledged, and the engine is created from a fetched
 * snapshot, so it cannot overwrite the cloud with data it never loaded.
 */
export class SyncEngine {
  private base: Saved;
  private revision: number;
  private latest: Saved;
  private running: Promise<void> | null = null;
  private currentStatus: SyncStatus = "idle";
  private readonly api: FootprintApi;
  private readonly listener: SyncListener;

  constructor(api: FootprintApi, snapshot: CloudSnapshot, listener: SyncListener = {}) {
    this.api = api;
    this.listener = listener;
    this.base = snapshot.saved;
    this.latest = snapshot.saved;
    this.revision = snapshot.revision;
  }

  get status() {
    return this.currentStatus;
  }
  get confirmed(): CloudSnapshot {
    return { saved: this.base, revision: this.revision };
  }
  get pending() {
    return diffSaved(this.base, this.latest).length > 0;
  }

  /** Replace the confirmed state, for example after a refetch. */
  reset(snapshot: CloudSnapshot) {
    this.base = snapshot.saved;
    this.latest = snapshot.saved;
    this.revision = snapshot.revision;
    this.setStatus("idle");
  }

  push(next: Saved) {
    this.latest = next;
    if (!this.running) this.running = this.run();
  }

  /** Resolves once everything pushed so far has been sent, or sending failed. */
  async drain() {
    while (this.running) await this.running;
  }

  private setStatus(status: SyncStatus) {
    if (status === this.currentStatus) return;
    this.currentStatus = status;
    this.listener.onStatus?.(status);
  }

  private async run() {
    this.setStatus("syncing");
    try {
      for (;;) {
        const [op] = diffSaved(this.base, this.latest);
        if (!op) break;
        this.revision = await this.api.apply(op);
        this.base = applyOp(this.base, op);
        this.listener.onConfirmed?.(this.confirmed);
      }
      this.setStatus("idle");
    } catch (error) {
      this.setStatus("error");
      this.listener.onError?.(error);
    } finally {
      this.running = null;
    }
  }
}
