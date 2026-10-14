import { create } from "@bufbuild/protobuf";
import { createIncrementalReader } from "@/lib/delta";
import {
  FootprintMutationSchema,
  type FootprintMutation,
  type GetFootprintsResponse,
} from "@/gen/tripmap/v1/footprint_pb";
import type { CloudSnapshot } from "@/lib/sync";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { FootprintService } from "@/gen/tripmap/v1/footprint_pb";
import { RevisionChanged, type FootprintApi, type Op } from "@/lib/sync";
import { BackendNotUpgraded } from "@/lib/errors";
import type { Trip } from "@/lib/travel";
import { transport } from "./transport";
import { getBackendStatus } from "./system";

const raw = transport ? createClient(FootprintService, transport) : null;
// Only reachable once a session exists, which requires a configured backend.
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

let capability: Promise<void> | null = null;
/**
 * Resolves once the backend reports it stores several trips per city. A failed
 * check (offline, server error) is not remembered, so the next attempt asks again.
 */
export function ensureBackendUpgraded(): Promise<void> {
  if (!capability) {
    const status = getBackendStatus();
    if (!status) return Promise.reject(new Error("backend not configured"));
    capability = status
      .then((s) => {
        if (s.footprintSchema < 2) throw new BackendNotUpgraded();
      })
      .catch((error) => {
        if (!(error instanceof BackendNotUpgraded)) capability = null;
        throw error;
      });
  }
  return capability;
}

function toSnapshot(r: GetFootprintsResponse): CloudSnapshot {
  // A backend that predates trips sends no schema version, and its visits carry
  // the old single date and note: reading them as cities would drop the trips.
  if (r.schemaVersion < 2) throw new BackendNotUpgraded();
  const trips = new Map<string, Trip[]>();
  for (const t of r.trips)
    trips.set(t.regionCode, [...(trips.get(t.regionCode) ?? []), { id: t.id, date: t.date, note: t.note }]);
  return {
    revision: Number(r.revision),
    saved: {
      version: 2,
      visits: r.visits.map((v) => ({
        code: v.code,
        name: v.name,
        province: v.province,
        color: v.color,
        trips: trips.get(v.code) ?? [],
      })),
      provinces: Object.fromEntries(r.provinceMarks.map((m) => [m.province, m.color])),
    },
  };
}

function mutation(op: Op): FootprintMutation {
  switch (op.type) {
    case "upsertVisit":
      return create(FootprintMutationSchema, {
        operation: {
          case: "upsertCity",
          value: {
            code: op.visit.code,
            name: op.visit.name,
            province: op.visit.province,
            color: op.visit.color,
          },
        },
      });
    case "legacyUpsertVisit":
      return create(FootprintMutationSchema, {
        operation: { case: "upsertVisit", value: op.visit },
      });
    case "upsertTrip":
      return create(FootprintMutationSchema, {
        operation: {
          case: "upsertTrip",
          value: { id: op.trip.id, regionCode: op.code, date: op.trip.date, note: op.trip.note },
        },
      });
    case "deleteTrip":
      return create(FootprintMutationSchema, {
        operation: { case: "deleteTrip", value: { regionCode: op.code, id: op.id } },
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
      // Written by an older client or batch: a city plus its one date and note.
      const v = o.value;
      return {
        type: "legacyUpsertVisit",
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
    case "upsertCity": {
      const v = o.value;
      return {
        type: "upsertVisit",
        visit: { code: v.code, name: v.name, province: v.province, color: v.color },
      };
    }
    case "upsertTrip":
      return {
        type: "upsertTrip",
        code: o.value.regionCode,
        trip: { id: o.value.id, date: o.value.date, note: o.value.note },
      };
    case "deleteTrip":
      return { type: "deleteTrip", code: o.value.regionCode, id: o.value.id };
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

async function send(ops: Op[], revision: number, id: string): Promise<number> {
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
}

// Exact revisions guard every operation, including the initially empty account.
// Single operations travel as one-element batches: the single-record RPCs
// predate trips and would write the old one-date-per-city form.
const fullApi: FootprintApi = {
  ready: ensureBackendUpgraded,
  async get() {
    return toSnapshot(await client().getFootprints({}));
  },
  apply: (op, revision) => send([op], revision, crypto.randomUUID()),
};

/** A private cursor per account/engine avoids cross-account delta contamination. */
export function createFootprintApi(seed?: CloudSnapshot): FootprintApi {
  const reader = createIncrementalReader(
    {
      full: fullApi.get,
      delta: async (revision) => {
        try {
          const page = await client().getChanges({
            sinceRevision: BigInt(revision),
            limit: 500,
          });
          // No schema version means a backend that predates trips: its change
          // log cannot be interpreted, so fall back to a full read, which refuses it.
          if (page.schemaVersion < 2)
            return {
              changes: [],
              nextRevision: revision,
              currentRevision: revision,
              hasMore: false,
              resyncRequired: true,
            };
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
    ready: ensureBackendUpgraded,
    get: reader.get,
    apply: fullApi.apply,
    applyBatch: send,
  };
}
// Bootstrap always fetches a fresh full snapshot; incremental cursors live in engines.
export const footprintApi = fullApi;
