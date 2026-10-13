import type { Saved, Visit } from "./travel.ts";

export type Op =
  | { type: "upsertVisit"; visit: Visit }
  | { type: "deleteVisit"; code: string }
  | { type: "upsertMark"; province: string; color: string }
  | { type: "deleteMark"; province: string };

/** One SyncChanges batch is atomic; the server accepts up to this many operations. */
export const MAX_BATCH_OPS = 600;

export const sameVisit = (a: Visit, b: Visit) =>
  a.name === b.name &&
  a.province === b.province &&
  a.color === b.color &&
  a.date === b.date &&
  a.note === b.note;

export function diffSaved(base: Saved, next: Saved): Op[] {
  const ops: Op[] = [];
  const before = new Map(base.visits.map((v) => [v.code, v]));
  const after = new Map(next.visits.map((v) => [v.code, v]));
  for (const code of before.keys())
    if (!after.has(code)) ops.push({ type: "deleteVisit", code });
  for (const [code, visit] of after) {
    const old = before.get(code);
    if (!old || !sameVisit(old, visit))
      ops.push({ type: "upsertVisit", visit });
  }
  for (const province of Object.keys(base.provinces))
    if (!(province in next.provinces))
      ops.push({ type: "deleteMark", province });
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
        visits: [
          ...saved.visits.filter((v) => v.code !== op.visit.code),
          op.visit,
        ],
      };
    case "deleteVisit":
      return {
        ...saved,
        visits: saved.visits.filter((v) => v.code !== op.code),
      };
    case "upsertMark":
      return {
        ...saved,
        provinces: { ...saved.provinces, [op.province]: op.color },
      };
    case "deleteMark": {
      const provinces = { ...saved.provinces };
      delete provinces[op.province];
      return { ...saved, provinces };
    }
  }
}

function record(saved: Saved, op: Op) {
  return op.type === "upsertVisit" || op.type === "deleteVisit"
    ? saved.visits.find(
        (v) => v.code === (op.type === "upsertVisit" ? op.visit.code : op.code),
      )
    : saved.provinces[op.province];
}
function equalRecord(
  a: ReturnType<typeof record>,
  b: ReturnType<typeof record>,
) {
  if (typeof a === "object" && typeof b === "object") return sameVisit(a, b);
  return a === b;
}

/** Merge unrelated remote edits; a same-record edit/delete needs a user's choice. */
export function rebaseSaved(base: Saved, desired: Saved, remote: Saved) {
  const conflicts: Op[] = [];
  let merged = remote;
  for (const op of diffSaved(base, desired)) {
    if (
      !equalRecord(record(base, op), record(remote, op)) &&
      !equalRecord(record(desired, op), record(remote, op))
    )
      conflicts.push(op);
    merged = applyOp(merged, op);
  }
  return { saved: merged, conflicts };
}

export type CloudSnapshot = { saved: Saved; revision: number };
/** The visible desired data and its server-confirmed ancestor are persisted together. */
export type PendingBatch = { id: string; revision: number; ops: Op[] };
export type SyncCache = CloudSnapshot & {
  confirmed: Saved;
  pendingBatch?: PendingBatch;
};
export class RevisionChanged extends Error {}
export interface FootprintApi {
  get(): Promise<CloudSnapshot>;
  apply(op: Op, revision: number): Promise<number>;
  applyBatch?(ops: Op[], revision: number, id: string): Promise<number>;
}
export type SyncStatus = "idle" | "syncing" | "error" | "conflict";
export type SyncListener = {
  onStatus?(status: SyncStatus): void;
  onConfirmed?(snapshot: CloudSnapshot): void;
  onChange?(cache: SyncCache): void;
  onError?(error: unknown): void;
  onBeforeSend?(cache: SyncCache): void;
};

export class SyncEngine {
  private base: Saved;
  private revision: number;
  private latest: Saved;
  private running: Promise<void> | null = null;
  private currentStatus: SyncStatus = "idle";
  private conflicting: Op[] = [];
  private remote: CloudSnapshot | null = null;
  private stopped = false;
  private pendingBatch: PendingBatch | undefined;

  private readonly api: FootprintApi;
  private readonly listener: SyncListener;

  constructor(
    api: FootprintApi,
    snapshot: CloudSnapshot,
    listener: SyncListener = {},
    desired = snapshot.saved,
    pendingBatch?: PendingBatch,
  ) {
    this.api = api;
    this.listener = listener;
    this.base = snapshot.saved;
    this.latest = desired;
    this.revision = snapshot.revision;
    this.pendingBatch = pendingBatch;
  }
  get status() {
    return this.currentStatus;
  }
  get confirmed(): CloudSnapshot {
    return { saved: this.base, revision: this.revision };
  }
  get cache(): SyncCache {
    return {
      saved: this.latest,
      confirmed: this.base,
      revision: this.revision,
      ...(this.pendingBatch ? { pendingBatch: this.pendingBatch } : {}),
    };
  }
  get pending() {
    return !!this.pendingBatch || diffSaved(this.base, this.latest).length > 0;
  }
  get conflicts() {
    return this.conflicting;
  }

  reset(snapshot: CloudSnapshot) {
    this.base = snapshot.saved;
    this.latest = snapshot.saved;
    this.revision = snapshot.revision;
    this.conflicting = [];
    this.remote = null;
    this.pendingBatch = undefined;
    this.setStatus("idle");
    this.changed();
  }
  /** Detached engines must never issue further requests using another account's token. */
  stop() {
    this.stopped = true;
  }
  resume() {
    this.stopped = false;
    this.retry();
  }
  push(next: Saved) {
    this.latest = next;
    this.changed();
    this.retry();
  }
  retry() {
    if (this.running || this.stopped || this.currentStatus === "conflict")
      return;
    // Defer execution so even a synchronous failure cannot leave a settled promise installed.
    this.running = Promise.resolve().then(() => this.run());
  }
  async drain() {
    while (this.running) await this.running;
  }

  resolve(choice: "local" | "cloud") {
    if (!this.remote || this.running) return;
    const result = rebaseSaved(this.base, this.latest, this.remote.saved);
    let desired = result.saved;
    if (choice === "cloud") {
      for (const op of result.conflicts) {
        const value = record(this.remote.saved, op);
        const restore: Op =
          op.type === "upsertVisit" || op.type === "deleteVisit"
            ? value
              ? { type: "upsertVisit", visit: value as Visit }
              : {
                  type: "deleteVisit",
                  code: op.type === "upsertVisit" ? op.visit.code : op.code,
                }
            : value
              ? {
                  type: "upsertMark",
                  province: op.province,
                  color: value as string,
                }
              : { type: "deleteMark", province: op.province };
        desired = applyOp(desired, restore);
      }
    }
    this.base = this.remote.saved;
    this.revision = this.remote.revision;
    this.latest = desired;
    this.remote = null;
    this.conflicting = [];
    this.setStatus("idle");
    this.changed();
    this.retry();
  }
  private changed() {
    this.listener.onChange?.(this.cache);
  }
  private setStatus(status: SyncStatus) {
    if (status === this.currentStatus) return;
    this.currentStatus = status;
    this.listener.onStatus?.(status);
  }
  private async run() {
    this.setStatus("syncing");
    try {
      let retries = 0;
      for (;;) {
        if (this.stopped) return;
        if (this.pendingBatch && this.api.applyBatch) {
          const batch = this.pendingBatch;
          try {
            this.listener.onBeforeSend?.(this.cache);
            const revision = await this.api.applyBatch(
              batch.ops,
              batch.revision,
              batch.id,
            );
            if (this.stopped) return;
            for (const op of batch.ops) this.base = applyOp(this.base, op);
            this.revision = revision;
            this.pendingBatch = undefined;
            this.listener.onConfirmed?.(this.confirmed);
            this.changed();
          } catch (error) {
            if (!(error instanceof RevisionChanged)) throw error;
            this.pendingBatch = undefined;
            this.changed();
            if (++retries > 3) throw error;
          }
        }
        const remote = await this.api.get();
        if (this.stopped) return;
        const result = rebaseSaved(this.base, this.latest, remote.saved);
        if (result.conflicts.length) {
          this.remote = remote;
          this.conflicting = result.conflicts;
          this.setStatus("conflict");
          return;
        }
        this.base = remote.saved;
        this.revision = remote.revision;
        this.latest = result.saved;
        this.changed();
        const operations = diffSaved(this.base, this.latest);
        const [op] = operations;
        if (!op) break;
        if (this.api.applyBatch) {
          this.pendingBatch = {
            id: crypto.randomUUID(),
            revision: this.revision,
            ops: operations.slice(0, MAX_BATCH_OPS),
          };
          // Persist the exact request before sending; a reload can replay its receipt.
          this.changed();
          continue;
        }
        try {
          const revision = await this.api.apply(op, this.revision);
          if (this.stopped) return;
          this.revision = revision;
          this.base = applyOp(this.base, op);
          this.listener.onConfirmed?.(this.confirmed);
          this.changed();
          retries = 0;
        } catch (error) {
          if (error instanceof RevisionChanged && ++retries <= 3) continue;
          throw error;
        }
      }
      this.setStatus("idle");
    } catch (error) {
      if (this.stopped) return;
      this.setStatus("error");
      this.listener.onError?.(error);
    } finally {
      this.running = null;
    }
  }
}
