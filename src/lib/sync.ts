import type { City, Saved, Trip, Visit } from "./travel.ts";
import { byTripId, canonicalTrips, legacyTripId } from "./trips.ts";

/** A schema 1 record: a city carrying its single date and note. */
export type LegacyVisit = City & { date: string; note: string };
export type Op =
  | { type: "upsertVisit"; visit: City }
  | { type: "deleteVisit"; code: string }
  | { type: "upsertTrip"; code: string; trip: Trip }
  | { type: "deleteTrip"; code: string; id: string }
  | { type: "upsertMark"; province: string; color: string }
  | { type: "deleteMark"; province: string }
  /**
   * A city plus its `legacy-<code>` trip, as written by versions before several
   * trips per city. It only appears in batches saved by an older version that are
   * replayed unchanged, and in the backend's change log; diffSaved never makes one.
   */
  | { type: "legacyUpsertVisit"; visit: LegacyVisit };

/** One SyncChanges batch is atomic; the server accepts up to this many operations. */
export const MAX_BATCH_OPS = 2000;

export const cityOf = (v: City): City => ({
  code: v.code,
  name: v.name,
  province: v.province,
  color: v.color,
});
export const sameCity = (a: City, b: City) =>
  a.name === b.name && a.province === b.province && a.color === b.color;
export const sameTrip = (a: Trip, b: Trip) => a.date === b.date && a.note === b.note;
/** Deep comparison of a city and all of its trips. */
export const sameVisit = (a: Visit, b: Visit) =>
  sameCity(a, b) &&
  a.trips.length === b.trips.length &&
  canonicalTrips(a.trips).every((t, i) => {
    const o = canonicalTrips(b.trips)[i];
    return t.id === o.id && sameTrip(t, o);
  });

export function diffSaved(base: Saved, next: Saved): Op[] {
  const before = new Map(base.visits.map((v) => [v.code, v]));
  const after = new Map(next.visits.map((v) => [v.code, v]));
  const deleteTrips: Op[] = [];
  const deleteVisits: Op[] = [];
  const deleteMarks: Op[] = [];
  const upsertVisits: Op[] = [];
  const upsertTrips: Op[] = [];
  const upsertMarks: Op[] = [];
  for (const [code, old] of before) {
    const now = after.get(code);
    if (!now) {
      deleteVisits.push({ type: "deleteVisit", code });
      continue;
    }
    const kept = new Set(now.trips.map((t) => t.id));
    for (const trip of old.trips)
      if (!kept.has(trip.id)) deleteTrips.push({ type: "deleteTrip", code, id: trip.id });
  }
  for (const province of Object.keys(base.provinces))
    if (!(province in next.provinces)) deleteMarks.push({ type: "deleteMark", province });
  for (const [code, visit] of after) {
    const old = before.get(code);
    if (!old || !sameCity(old, visit))
      upsertVisits.push({ type: "upsertVisit", visit: cityOf(visit) });
    const had = new Map((old?.trips ?? []).map((t) => [t.id, t]));
    for (const trip of canonicalTrips(visit.trips)) {
      const prior = had.get(trip.id);
      if (!prior || !sameTrip(prior, trip)) upsertTrips.push({ type: "upsertTrip", code, trip });
    }
  }
  for (const [province, color] of Object.entries(next.provinces))
    if (base.provinces[province] !== color)
      upsertMarks.push({ type: "upsertMark", province, color });
  // A city exists before its trips are added, and trips are removed before their
  // city is, so any prefix of this list is itself a consistent set of changes.
  return [...deleteTrips, ...deleteVisits, ...deleteMarks, ...upsertVisits, ...upsertTrips, ...upsertMarks];
}

function withTrip(trips: Trip[], trip: Trip): Trip[] {
  return [...trips.filter((t) => t.id !== trip.id), trip].sort(byTripId);
}

export function applyOp(saved: Saved, op: Op): Saved {
  switch (op.type) {
    case "upsertVisit": {
      const found = saved.visits.some((v) => v.code === op.visit.code);
      return {
        ...saved,
        visits: found
          ? saved.visits.map((v) => (v.code === op.visit.code ? { ...cityOf(op.visit), trips: v.trips } : v))
          : [...saved.visits, { ...cityOf(op.visit), trips: [] }],
      };
    }
    case "deleteVisit":
      return { ...saved, visits: saved.visits.filter((v) => v.code !== op.code) };
    case "upsertTrip":
      // A trip needs its city; without it the change has nothing to attach to.
      return {
        ...saved,
        visits: saved.visits.map((v) =>
          v.code === op.code ? { ...v, trips: withTrip(v.trips, { id: op.trip.id, date: op.trip.date, note: op.trip.note }) } : v,
        ),
      };
    case "deleteTrip":
      return {
        ...saved,
        visits: saved.visits.map((v) =>
          v.code === op.code ? { ...v, trips: v.trips.filter((t) => t.id !== op.id) } : v,
        ),
      };
    case "legacyUpsertVisit": {
      const { date, note, ...city } = op.visit;
      return applyOp(applyOp(saved, { type: "upsertVisit", visit: city }), {
        type: "upsertTrip",
        code: city.code,
        trip: { id: legacyTripId(city.code), date, note },
      });
    }
    case "upsertMark":
      return { ...saved, provinces: { ...saved.provinces, [op.province]: op.color } };
    case "deleteMark": {
      const provinces = { ...saved.provinces };
      delete provinces[op.province];
      return { ...saved, provinces };
    }
  }
}

// A conflict is about one record. Comparing records, not whole states, lets
// edits to different records merge without asking.
const city = (saved: Saved, code: string) => saved.visits.find((v) => v.code === code);
const cityRecord = (saved: Saved, code: string) => {
  const v = city(saved, code);
  return v ? JSON.stringify([v.name, v.province, v.color]) : undefined;
};
const tripRecord = (saved: Saved, code: string, id: string) => {
  const t = city(saved, code)?.trips.find((t) => t.id === id);
  return t ? JSON.stringify([t.date, t.note]) : undefined;
};
/** The whole city with its trips: what a local delete of the city discards. */
const subtreeRecord = (saved: Saved, code: string) => {
  const v = city(saved, code);
  return v
    ? JSON.stringify([v.name, v.province, v.color, canonicalTrips(v.trips).map((t) => [t.id, t.date, t.note])])
    : undefined;
};
function record(saved: Saved, op: Op): string | undefined {
  switch (op.type) {
    case "upsertVisit":
    case "legacyUpsertVisit":
      return cityRecord(saved, op.visit.code);
    case "deleteVisit":
      return subtreeRecord(saved, op.code);
    case "upsertTrip":
      return tripRecord(saved, op.code, op.trip.id);
    case "deleteTrip":
      return tripRecord(saved, op.code, op.id);
    case "upsertMark":
    case "deleteMark":
      return saved.provinces[op.province];
  }
}

const opCode = (op: Op): string | undefined =>
  op.type === "upsertTrip" || op.type === "deleteTrip" || op.type === "deleteVisit"
    ? op.code
    : op.type === "upsertVisit" || op.type === "legacyUpsertVisit"
      ? op.visit.code
      : undefined;

/** The whole city with its trips from `from`, ready to put back after it was removed elsewhere. */
function resurrect(merged: Saved, from: Saved, code: string): Saved {
  const visit = city(from, code);
  return visit ? { ...merged, visits: [...merged.visits, visit] } : merged;
}

/**
 * Merge unrelated remote edits; a same-record edit/delete needs a user's choice.
 * A trip added or edited under a city that was removed remotely is also a
 * conflict, because the city has to come back for the trip to exist.
 */
export function rebaseSaved(base: Saved, desired: Saved, remote: Saved) {
  const conflicts: Op[] = [];
  let merged = remote;
  for (const op of diffSaved(base, desired)) {
    const code = opCode(op);
    const parentRemoved =
      op.type === "upsertTrip" && !!city(base, op.code) && !city(remote, op.code) && !!city(desired, op.code);
    if (
      parentRemoved ||
      (record(base, op) !== record(remote, op) && record(desired, op) !== record(remote, op))
    )
      conflicts.push(op);
    // Keeping a local change under a city that was removed remotely brings the
    // city back with everything this device had in it.
    if (
      code !== undefined &&
      (op.type === "upsertTrip" || op.type === "upsertVisit") &&
      !city(merged, code) &&
      !!city(base, code) &&
      !!city(desired, code)
    )
      merged = resurrect(merged, desired, code);
    merged = applyOp(merged, op);
  }
  return { saved: merged, conflicts };
}

/** The operations that restore the cloud's version of the record a conflicting op touched. */
export function restoreOps(op: Op, remote: Saved): Op[] {
  switch (op.type) {
    case "upsertVisit":
    case "legacyUpsertVisit": {
      const v = city(remote, op.visit.code);
      return [v ? { type: "upsertVisit", visit: cityOf(v) } : { type: "deleteVisit", code: op.visit.code }];
    }
    case "deleteVisit": {
      const v = city(remote, op.code);
      return v
        ? [
            { type: "upsertVisit", visit: cityOf(v) },
            ...v.trips.map((trip): Op => ({ type: "upsertTrip", code: v.code, trip })),
          ]
        : [];
    }
    case "upsertTrip":
    case "deleteTrip": {
      const code = op.code;
      const id = op.type === "upsertTrip" ? op.trip.id : op.id;
      const v = city(remote, code);
      // When the whole city is gone remotely, keeping the cloud's version removes it here too.
      if (!v) return [{ type: "deleteVisit", code }];
      const trip = v.trips.find((t) => t.id === id);
      return [trip ? { type: "upsertTrip", code, trip } : { type: "deleteTrip", code, id }];
    }
    case "upsertMark":
    case "deleteMark": {
      const value = remote.provinces[op.province];
      return [
        value
          ? { type: "upsertMark", province: op.province, color: value }
          : { type: "deleteMark", province: op.province },
      ];
    }
  }
}

export type CloudSnapshot = { saved: Saved; revision: number };
/** The visible desired data and its server-confirmed ancestor are persisted together. */
export type PendingBatch = { id: string; revision: number; ops: Op[] };
export type SyncCache = CloudSnapshot & {
  confirmed: Saved;
  pendingBatch?: PendingBatch;
  /**
   * The unsent change is all-or-nothing (the F2 migration). Stored with the cache so
   * a reload before the batch exists, or after a failed or conflicting attempt, still
   * refuses to split it; cleared once that batch is confirmed.
   */
  atomic?: true;
};
export class RevisionChanged extends Error {}
/**
 * An all-or-nothing change (the F2 migration) turned out to need more operations
 * than one batch holds. Nothing was sent; the change stays unsent in the cache, with
 * everything the user did since, until it is reduced to fit.
 */
export class AtomicBatchTooLarge extends Error {
  readonly count: number;
  constructor(count: number) {
    super(`atomic change needs ${count} operations, more than one batch of ${MAX_BATCH_OPS}`);
    this.count = count;
  }
}
/** An all-or-nothing change cannot be sent because the connection has no batch call. Nothing was sent or discarded. */
export class AtomicBatchUnavailable extends Error {
  constructor() {
    super("atomic change needs batch requests, which are unavailable");
  }
}
export interface FootprintApi {
  /**
   * Rejects when the backend cannot store several trips per city. The engine
   * waits for it before any read or write, so an older backend is never sent
   * data it would flatten or drop.
   */
  ready?(): Promise<void>;
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
  /** Set while an all-or-nothing change is on its way; it must go out as one batch. Persisted in the cache. */
  private atomic: boolean;

  private readonly api: FootprintApi;
  private readonly listener: SyncListener;

  constructor(
    api: FootprintApi,
    snapshot: CloudSnapshot,
    listener: SyncListener = {},
    desired = snapshot.saved,
    pendingBatch?: PendingBatch,
    atomic = false,
  ) {
    this.api = api;
    this.atomic = atomic;
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
      ...(this.atomic ? { atomic: true as const } : {}),
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
    this.atomic = false;
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
  /**
   * Like push, for a change that must reach the server as exactly one atomic
   * batch (the migration). Refused while other changes are waiting, so there is
   * nothing to mix with. If the batch would exceed the limit by the time it is
   * built, sync pauses with an error instead of splitting it; the unsent data and
   * the intent are kept.
   */
  pushAtomic(next: Saved): boolean {
    if (this.pending || this.currentStatus === "conflict") return false;
    this.atomic = true;
    this.push(next);
    return true;
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
      for (const op of result.conflicts)
        for (const restore of restoreOps(op, this.remote.saved))
          desired = applyOp(desired, restore);
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
      await this.api.ready?.();
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
            // An atomic change is built only when it fits one batch, so this was all of it.
            this.atomic = false;
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
        if (!op) {
          // Nothing is left to send: the all-or-nothing change is complete (or empty).
          if (this.atomic) {
            this.atomic = false;
            this.changed();
          }
          break;
        }
        // An all-or-nothing change that cannot go out as one batch is never split and
        // never rolled back: the accepted data stays in the cache with its intent, sync
        // pauses, and the user shrinks it, exports it or decides what to do.
        if (this.atomic && operations.length > MAX_BATCH_OPS)
          throw new AtomicBatchTooLarge(operations.length);
        if (this.atomic && !this.api.applyBatch)
          throw new AtomicBatchUnavailable();
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
