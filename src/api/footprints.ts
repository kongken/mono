import { createClient } from "@connectrpc/connect";
import { FootprintService } from "@/gen/tripmap/v1/footprint_pb";
import type { FootprintApi, Op } from "@/lib/sync";
import { transport } from "./transport";

const raw = transport ? createClient(FootprintService, transport) : null;
// Only reachable once a session exists, which requires a configured backend.
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

// Writes are idempotent on the server, so a retried request is safe. No
// base_revision is sent: the engine sends per-record changes, and the server
// merges them, rather than overwriting a whole snapshot.
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
        provinces: Object.fromEntries(r.provinceMarks.map((m) => [m.province, m.color])),
      },
    };
  },
  async apply(op: Op) {
    switch (op.type) {
      case "upsertVisit":
        return Number((await client().upsertVisit({ visit: op.visit })).revision);
      case "deleteVisit":
        return Number((await client().deleteVisit({ code: op.code })).revision);
      case "upsertMark":
        return Number(
          (await client().upsertProvinceMark({ mark: { province: op.province, color: op.color } })).revision,
        );
      case "deleteMark":
        return Number((await client().deleteProvinceMark({ province: op.province })).revision);
    }
  },
};
