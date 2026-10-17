import { applyOp, type CloudSnapshot, type Op } from "./sync.ts";
export type DeltaPage = {
  changes: { revision: number; op: Op }[];
  nextRevision: number;
  currentRevision: number;
  hasMore: boolean;
  resyncRequired: boolean;
};
export function applyDelta(
  snapshot: CloudSnapshot,
  page: DeltaPage,
  project: (ops: Op[]) => Op[] = ops => ops,
): CloudSnapshot {
  if (
    ![snapshot.revision, page.nextRevision, page.currentRevision].every(
      (n) => Number.isSafeInteger(n) && n >= 0,
    ) ||
    page.resyncRequired
  )
    throw new Error("invalid delta cursor");
  let saved = snapshot.saved,
    revision = snapshot.revision;
  for (const change of page.changes) {
    if (change.revision !== revision + 1)
      throw new Error("non-contiguous delta");
    for (const op of project([change.op])) saved = applyOp(saved, op);
    revision = change.revision;
  }
  if (
    revision !== page.nextRevision ||
    revision > page.currentRevision ||
    (page.hasMore &&
      (!page.changes.length || revision >= page.currentRevision)) ||
    (!page.hasMore && revision !== page.currentRevision)
  )
    throw new Error("invalid delta pagination");
  return { saved, revision };
}
/** The cursor advances only after every page succeeds; failed reads keep the ancestor. */
export function createIncrementalReader(
  source: {
    full(): Promise<CloudSnapshot>;
    /** Regional projections may expand one legacy mutation without changing its revision. */
    project?(ops: Op[]): Op[];
    delta(revision: number): Promise<DeltaPage>;
  },
  seed?: CloudSnapshot,
) {
  let cursor = seed;
  return {
    async get(): Promise<CloudSnapshot> {
      if (!cursor) {
        cursor = await source.full();
        return cursor;
      }
      let candidate = cursor;
      for (let pages = 0; pages < 20; pages++) {
        const page = await source.delta(candidate.revision);
        if (page.resyncRequired) {
          cursor = await source.full();
          return cursor;
        }
        candidate = applyDelta(candidate, page, source.project);
        if (!page.hasMore) {
          cursor = candidate;
          return cursor;
        }
      }
      cursor = await source.full();
      return cursor;
    },
  };
}
