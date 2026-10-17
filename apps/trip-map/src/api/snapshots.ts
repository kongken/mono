import { createClient } from "@connectrpc/connect";
import { timestampMs } from "@bufbuild/protobuf/wkt";
import {
  SnapshotService,
  type SnapshotInfo as WireInfo,
} from "@kongken/api-client/bff/tripmap/v1/snapshot_pb";
import {
  snapshotToSaved,
  type SnapshotInfo,
  type SnapshotReason as Reason,
} from "@/lib/snapshots";
import type { Saved } from "@/lib/travel";
import { transport } from "./transport";

const raw = transport ? createClient(SnapshotService, transport) : null;
// Only reachable once a session exists, which requires a configured backend.
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

const reasons: Record<string, Reason> = {
  manual: "manual",
  scheduled: "scheduled",
  before_migration: "beforeMigration",
  before_restore: "beforeRestore",
};

function info(wire: WireInfo): SnapshotInfo {
  return {
    id: wire.id,
    // An unknown future reason is shown as a manual snapshot rather than hidden.
    reason: reasons[wire.reason] ?? "manual",
    createdAt: wire.createdAt ? timestampMs(wire.createdAt) : 0,
    visitCount: wire.visitCount,
    // Snapshots from before several trips per city hold one trip per city.
    tripCount: wire.schemaVersion >= 2 ? wire.tripCount : wire.visitCount,
    markCount: wire.provinceMarkCount,
    revision: Number(wire.revision),
  };
}

export const snapshotApi = {
  list: async (): Promise<SnapshotInfo[]> =>
    (await client().listSnapshots({})).snapshots.map(info),
  create: async (
    reason: "manual" | "beforeMigration",
  ): Promise<{ snapshot: SnapshotInfo; created: boolean }> => {
    const r = await client().createSnapshot({
      reason: reason === "manual" ? "manual" : "before_migration",
    });
    if (!r.snapshot) throw new Error("incomplete snapshot reply");
    return { snapshot: info(r.snapshot), created: r.created };
  },
  get: async (id: string): Promise<{ snapshot: SnapshotInfo; saved: Saved }> => {
    const r = await client().getSnapshot({ id });
    if (!r.snapshot || !r.data) throw new Error("incomplete snapshot reply");
    const data = r.data;
    return {
      snapshot: info(r.snapshot),
      saved: snapshotToSaved({
        visits: data.visits,
        provinceMarks: data.provinceMarks,
        schemaVersion: data.schemaVersion,
        regionVersion: data.regionVersion,
        trips: data.trips.map((t) => ({
          id: t.id,
          regionCode: t.city,
          date: t.date,
          note: t.note,
        })),
      }),
    };
  },
  /**
   * Make the account equal the snapshot. `baseRevision` is the revision the
   * caller previewed against, and `mutationId` is reused verbatim on a retry.
   */
  restore: async (
    snapshotId: string,
    baseRevision: number,
    mutationId: string,
  ) => {
    const r = await client().restoreSnapshot({
      snapshotId,
      baseRevision: BigInt(baseRevision),
      mutationId,
    });
    return {
      revision: Number(r.revision),
      changedCount: r.changedCount,
      undo: r.undoSnapshot ? info(r.undoSnapshot) : null,
      replayed: r.replayed,
    };
  },
};
