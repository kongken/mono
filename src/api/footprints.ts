import { create } from "@bufbuild/protobuf";
import { createIncrementalReader } from "@/lib/delta";
import {
  FootprintMutationSchema,
  type FootprintMutation,
} from "@/gen/tripmap/v1/footprint_pb";
import type { CloudSnapshot } from "@/lib/sync";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { FootprintService } from "@/gen/tripmap/v1/footprint_pb";
import { RevisionChanged, type FootprintApi, type Op } from "@/lib/sync";
import { transport } from "./transport";

const raw = transport ? createClient(FootprintService, transport) : null;
// Only reachable once a session exists, which requires a configured backend.
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

// Exact revisions guard every operation, including the initially empty account.
// The backend must support require_revision before deploying this frontend.
const legacyApi: FootprintApi = {
  async get() {
    const r = await client().getFootprints({});
    return {
      revision: Number(r.revision),
      saved: {
        version: 1,
        visits: r.visits.map((v) => ({
          code: v.code,
          name: v.name,
          province: v.province,
          color: v.color,
          date: v.date,
          note: v.note,
        })),
        provinces: Object.fromEntries(
          r.provinceMarks.map((m) => [m.province, m.color]),
        ),
      },
    };
  },
  async apply(op: Op, revision: number) {
    const guard = { baseRevision: BigInt(revision), requireRevision: true };
    try {
      switch (op.type) {
        case "upsertVisit":
          return Number(
            (await client().upsertVisit({ visit: op.visit, ...guard }))
              .revision,
          );
        case "deleteVisit":
          return Number(
            (await client().deleteVisit({ code: op.code, ...guard })).revision,
          );
        case "upsertMark":
          return Number(
            (
              await client().upsertProvinceMark({
                mark: { province: op.province, color: op.color },
                ...guard,
              })
            ).revision,
          );
        case "deleteMark":
          return Number(
            (
              await client().deleteProvinceMark({
                province: op.province,
                ...guard,
              })
            ).revision,
          );
      }
    } catch (error) {
      if (error instanceof ConnectError && error.code === Code.Aborted)
        throw new RevisionChanged("云端版本已变化");
      throw error;
    }
  },
};

function mutation(op: Op): FootprintMutation {
  switch (op.type) {
    case "upsertVisit":
      return create(FootprintMutationSchema, {
        operation: { case: "upsertVisit", value: op.visit },
      });
    case "deleteVisit":
      return create(FootprintMutationSchema, {
        operation: { case: "deleteVisit", value: op.code },
      });
    case "upsertMark":
      return create(FootprintMutationSchema, {
        operation: {
          case: "upsertMark",
          value: { province: op.province, color: op.color },
        },
      });
    case "deleteMark":
      return create(FootprintMutationSchema, {
        operation: { case: "deleteMark", value: op.province },
      });
  }
}
function operation(m?: FootprintMutation): Op {
  const o = m?.operation;
  switch (o?.case) {
    case "upsertVisit": {
      const v = o.value;
      return {
        type: "upsertVisit",
        visit: {
          code: v.code,
          name: v.name,
          province: v.province,
          color: v.color,
          date: v.date,
          note: v.note,
        },
      };
    }
    case "deleteVisit":
      return { type: "deleteVisit", code: o.value };
    case "upsertMark":
      return {
        type: "upsertMark",
        province: o.value.province,
        color: o.value.color,
      };
    case "deleteMark":
      return { type: "deleteMark", province: o.value };
    default:
      throw new Error("云端增量操作无法识别");
  }
}
/** A private cursor per account/engine avoids cross-account delta contamination. */
export function createFootprintApi(seed?: CloudSnapshot): FootprintApi {
  const reader = createIncrementalReader(
    {
      full: legacyApi.get,
      delta: async (revision) => {
        try {
          const page = await client().getChanges({
            sinceRevision: BigInt(revision),
            limit: 500,
          });
          return {
            changes: page.changes.map((c) => ({
              revision: Number(c.revision),
              op: operation(c.mutation),
            })),
            nextRevision: Number(page.nextRevision),
            currentRevision: Number(page.currentRevision),
            hasMore: page.hasMore,
            resyncRequired: page.resyncRequired,
          };
        } catch (error) {
          if (
            error instanceof ConnectError &&
            error.code === Code.Unimplemented
          )
            return {
              changes: [],
              nextRevision: revision,
              currentRevision: revision,
              hasMore: false,
              resyncRequired: true,
            };
          throw error;
        }
      },
    },
    seed,
  );
  return {
    get: reader.get,
    apply: legacyApi.apply,
    async applyBatch(ops, revision, id) {
      try {
        return Number(
          (
            await client().syncChanges({
              mutationId: id,
              baseRevision: BigInt(revision),
              mutations: ops.map(mutation),
            })
          ).revision,
        );
      } catch (error) {
        if (error instanceof ConnectError && error.code === Code.Aborted)
          throw new RevisionChanged("云端版本已变化");
        throw error;
      }
    },
  };
}
// Bootstrap always fetches a fresh full snapshot; incremental cursors live in engines.
export const footprintApi = legacyApi;
