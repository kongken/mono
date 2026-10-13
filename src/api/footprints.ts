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
export const footprintApi: FootprintApi = {
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
