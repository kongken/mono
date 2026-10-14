import type { Page } from "@playwright/test";
import {
  create,
  toBinary,
  toJsonString,
  fromBinary,
  fromJson,
  type DescMessage,
} from "@bufbuild/protobuf";
import * as auth from "../src/gen/tripmap/v1/auth_pb";
import * as collaborationPb from "../src/gen/tripmap/v1/collaboration_pb";
import * as footprint from "../src/gen/tripmap/v1/footprint_pb";
import * as reportPb from "../src/gen/tripmap/v1/report_pb";
import * as searchPb from "../src/gen/tripmap/v1/search_pb";
import * as snapshotPb from "../src/gen/tripmap/v1/snapshot_pb";
import * as system from "../src/gen/tripmap/v1/system_pb";
import { buildReport } from "../src/lib/report";
import { searchNotes } from "../src/lib/search";

export const green = "#4b937d";
export const gold = "#e2af5c";
export const user = { id: "ci-migrate", username: "ci_user", displayName: "CI 旅行者" };

/** Old-shape input: a city with its single trip's date and note. */
export type V = { code: string; name: string; province: string; color: string; date: string; note: string };
export const dongcheng = (extra: Partial<V> = {}): V => ({
  code: "110101", name: "东城区", province: "110000", color: green, date: "2026-05-01", note: "", ...extra,
});
export const xicheng = (extra: Partial<V> = {}): V => ({
  code: "110102", name: "西城区", province: "110000", color: green, date: "", note: "", ...extra,
});
export const tianjin = (extra: Partial<V> = {}): V => ({
  code: "120101", name: "和平区", province: "120000", color: gold, date: "", note: "", ...extra,
});

export type City = { code: string; name: string; province: string; color: string };
export type StoredTrip = { id: string; code: string; date: string; note: string };
export const legacyId = (code: string) => `legacy-${code}`;
const cityOf = (v: V): City => ({ code: v.code, name: v.name, province: v.province, color: v.color });

type Snap = {
  id: string;
  reason: string;
  createdAt: number;
  revision: number;
  visits: City[];
  trips: StoredTrip[];
  marks: Record<string, string>;
};

const failure = (code: string, message: string, status: number) => ({
  status,
  contentType: "application/json",
  body: JSON.stringify({ code, message }),
});

// A tiny in-memory FootprintService (schema 2): enough of the real contract
// (revision check, atomic batch, idempotent replay, trips, cascade deletion,
// the client-schema gate) and SnapshotService (one snapshot per revision,
// conditional atomic restore) to drive the UI.
export function fakeBackend(
  page: Page,
  initial: { visits: V[]; marks?: Record<string, string>; trips?: StoredTrip[] },
  options: { footprintSchema?: number; shareEnabled?: boolean } = {},
) {
  const seededTrips: StoredTrip[] = [
    ...initial.visits.map((v) => ({ id: legacyId(v.code), code: v.code, date: v.date, note: v.note })),
    ...(initial.trips ?? []),
  ];
  const state = {
    revision: initial.visits.length + seededTrips.length + Object.keys(initial.marks ?? {}).length,
    visits: new Map<string, City>(initial.visits.map((v) => [v.code, cityOf(v)])),
    trips: new Map<string, StoredTrip>(seededTrips.map((t) => [t.id, t])),
    marks: new Map(Object.entries(initial.marks ?? {})),
    syncCalls: [] as number[],
    /** Operation kinds of each accepted batch, in order. */
    batches: [] as string[][],
    seedRevision: 0,
    log: [] as { revision: number; mutation: footprint.FootprintMutation }[],
    failSync: 0,
    /** Number of reads (GetFootprints/GetChanges) that fail with a 503 before succeeding again. */
    failGet: 0,
    failSearch: 0,
    searchCalls: [] as string[],
    receipts: new Map<string, number>(),
    snapshots: [] as Snap[],
    snapshotCalls: [] as string[],
    failCreateSnapshot: 0,
    failRestore: 0,
    restoreReceipts: new Map<string, { revision: number; changed: number; undoId: string }>(),
    restoreCalls: 0,
    restoreIds: [] as string[],
    /** Schema header values seen on footprint and snapshot requests. */
    schemaHeaders: new Set<string>(),
    /** Lose the reply of the next accepted batch after committing it. */
    dropNextSyncReply: 0,
    footprintSchema: options.footprintSchema ?? 2,
  };
  state.seedRevision = state.revision;
  const ts = () => ({ seconds: BigInt(Math.floor(Date.now() / 1000)) });
  const tripsOf = (code: string) => [...state.trips.values()].filter((t) => t.code === code);
  const wireTrip = (t: StoredTrip) => ({
    id: t.id, regionCode: t.code, date: t.date, note: t.note, createdAt: ts(), updatedAt: ts(),
  });
  const snapInfo = (snap: Snap) => ({
    id: snap.id,
    reason: snap.reason,
    createdAt: { seconds: BigInt(Math.floor(snap.createdAt / 1000)) },
    visitCount: snap.visits.length,
    tripCount: snap.trips.length,
    provinceMarkCount: Object.keys(snap.marks).length,
    revision: BigInt(snap.revision),
    schemaVersion: 2,
  });
  // Same contract as the server: one snapshot per revision, reused if present.
  const capture = (reason: string) => {
    const existing = state.snapshots.find((s) => s.revision === state.revision);
    if (existing) return { snap: existing, created: false };
    const snap: Snap = {
      id: `snap-${state.snapshots.length + 1}`,
      reason,
      createdAt: Date.now(),
      revision: state.revision,
      visits: [...state.visits.values()].map((v) => ({ ...v })),
      trips: [...state.trips.values()].map((t) => ({ ...t })),
      marks: Object.fromEntries(state.marks),
    };
    state.snapshots.push(snap);
    return { snap, created: true };
  };
  const logChange = (mutation: footprint.FootprintMutation) => {
    state.revision++;
    state.log.push({ revision: state.revision, mutation });
  };
  const mutation = (operation: { case: string; value: unknown }) =>
    create(footprint.FootprintMutationSchema, { operation } as never);
  const upsertCityMutation = (c: City) => mutation({ case: "upsertCity", value: c });
  const upsertTripMutation = (t: StoredTrip) =>
    mutation({ case: "upsertTrip", value: { id: t.id, regionCode: t.code, date: t.date, note: t.note } });
  /** Applies one mutation as the server does; returns false when it is rejected. */
  const apply = (m: footprint.FootprintMutation): string => {
    const op = m.operation;
    switch (op.case) {
      case "upsertVisit": {
        const v = op.value as unknown as V;
        state.visits.set(v.code, cityOf(v));
        state.trips.set(legacyId(v.code), { id: legacyId(v.code), code: v.code, date: v.date, note: v.note });
        return "upsertVisit";
      }
      case "upsertCity":
        state.visits.set(op.value.code, cityOf(op.value as unknown as V));
        return "upsertCity";
      case "upsertTrip": {
        const t = op.value;
        if (!state.visits.has(t.regionCode)) throw new Error("trip city does not exist");
        state.trips.set(t.id, { id: t.id, code: t.regionCode, date: t.date, note: t.note });
        return "upsertTrip";
      }
      case "deleteTrip":
        state.trips.delete(op.value.id);
        return "deleteTrip";
      case "deleteVisit":
        state.visits.delete(op.value);
        for (const t of tripsOf(op.value)) state.trips.delete(t.id);
        return "deleteVisit";
      case "upsertMark":
        state.marks.set(op.value.province, op.value.color);
        return "upsertMark";
      case "deleteMark":
        state.marks.delete(op.value);
        return "deleteMark";
      default:
        return "unknown";
    }
  };
  return {
    state,
    tripsOf,
    noteOf: (code: string) => state.trips.get(legacyId(code))?.note,
    /** A visit written by another device with the old single-trip shape. */
    otherDeviceUpsert: (visit: V) => {
      apply(create(footprint.FootprintMutationSchema, { operation: { case: "upsertVisit", value: visit } }));
      logChange(create(footprint.FootprintMutationSchema, { operation: { case: "upsertVisit", value: visit } }));
    },
    otherDeviceCity: (city: City) => {
      state.visits.set(city.code, city);
      logChange(upsertCityMutation(city));
    },
    otherDeviceTrip: (trip: StoredTrip) => {
      state.trips.set(trip.id, trip);
      logChange(upsertTripMutation(trip));
    },
    otherDeviceDeleteTrip: (id: string) => {
      const t = state.trips.get(id);
      if (!t) return;
      state.trips.delete(id);
      logChange(mutation({ case: "deleteTrip", value: { regionCode: t.code, id } }));
    },
    otherDeviceDeleteVisit: (code: string) => {
      apply(mutation({ case: "deleteVisit", value: code }) as footprint.FootprintMutation);
      logChange(mutation({ case: "deleteVisit", value: code }));
    },
    /** Saves a snapshot of the current server state, as the server's scheduler would. */
    seedSnapshot: (reason = "scheduled") => capture(reason).snap,
    install: () =>
      page.route("**/api/**", async (route) => {
        const req = route.request();
        const method = req.url().split("/").pop()!;
        const binary = !!req.headers()["content-type"]?.includes("proto");
        const read = (schema: DescMessage) => {
          const raw = req.postDataBuffer()!;
          return binary ? fromBinary(schema, raw) : fromJson(schema, JSON.parse(raw.toString()));
        };
        const send = async (schema: DescMessage, data: object) => {
          const message = create(schema, data as never);
          await route.fulfill({
            status: 200,
            contentType: binary ? "application/proto" : "application/json",
            body: binary ? Buffer.from(toBinary(schema, message)) : toJsonString(schema, message),
          });
        };
        const gated = ["GetFootprints", "GetChanges", "SyncChanges", "CreateSnapshot", "ListSnapshots", "GetSnapshot", "RestoreSnapshot"];
        if (gated.includes(method)) {
          const header = req.headers()["x-tripmap-schema"] ?? "";
          state.schemaHeaders.add(header);
          // The server refuses a request that cannot see trips once trips exist.
          if (header !== "2") return route.fulfill(failure("failed_precondition", "client upgrade required: reload the page", 412));
        }
        if ((method === "GetFootprints" || method === "GetChanges") && state.failGet > 0) {
          state.failGet--;
          return route.fulfill(failure("unavailable", "temporarily unavailable", 503));
        }
        switch (method) {
          case "GetStatus":
            return send(system.GetStatusResponseSchema, { footprintSchema: state.footprintSchema, shareEnabled: options.shareEnabled ?? false });
          case "Login":
            return send(auth.LoginResponseSchema, {
              token: "ci-token",
              user,
              expiresAt: { seconds: BigInt(Math.floor(Date.now() / 1000) + 3600) },
            });
          case "ListOAuthProviders":
            return send(auth.ListOAuthProvidersResponseSchema, {});
          case "Me":
            return send(auth.MeResponseSchema, { user });
          case "ListSessions":
            return send(auth.ListSessionsResponseSchema, {});
          case "GetFootprints":
            return send(footprint.GetFootprintsResponseSchema, {
              revision: BigInt(state.revision),
              visits: [...state.visits.values()].map((v) => ({ ...v, date: "", note: "", updatedAt: ts() })),
              trips: [...state.trips.values()].map(wireTrip),
              provinceMarks: [...state.marks].map(([province, color]) => ({ province, color, updatedAt: ts() })),
              schemaVersion: state.footprintSchema,
            });
          case "GetChanges": {
            const since = Number(
              (read(footprint.GetChangesRequestSchema) as footprint.GetChangesRequest).sinceRevision,
            );
            const changes = state.log.filter((c) => c.revision > since);
            return send(footprint.GetChangesResponseSchema, {
              changes: changes.map((c) => ({ revision: BigInt(c.revision), mutation: c.mutation })),
              nextRevision: BigInt(state.revision),
              currentRevision: BigInt(state.revision),
              // Initial seed rows have no log entries: ask for a full snapshot.
              resyncRequired: since < state.seedRevision,
              schemaVersion: state.footprintSchema,
            });
          }
          case "SyncChanges": {
            const body = read(footprint.SyncChangesRequestSchema) as footprint.SyncChangesRequest;
            if (state.failSync > 0) {
              state.failSync--;
              return route.fulfill(failure("unavailable", "offline", 503));
            }
            const replayed = state.receipts.get(body.mutationId);
            if (replayed !== undefined) {
              return send(footprint.SyncChangesResponseSchema, {
                revision: BigInt(replayed), changedCount: body.mutations.length, replayed: true,
              });
            }
            if (Number(body.baseRevision) !== state.revision)
              return route.fulfill(failure("aborted", "footprints changed since base_revision", 409));
            // All or nothing: apply on a copy first.
            const before = {
              visits: new Map(state.visits),
              trips: new Map(state.trips),
              marks: new Map(state.marks),
              revision: state.revision,
              log: state.log.length,
            };
            try {
              const kinds: string[] = [];
              for (const m of body.mutations) {
                kinds.push(apply(m));
                state.revision++;
                state.log.push({ revision: state.revision, mutation: m });
              }
              state.batches.push(kinds);
            } catch (e) {
              state.visits = before.visits;
              state.trips = before.trips;
              state.marks = before.marks;
              state.revision = before.revision;
              state.log.length = before.log;
              return route.fulfill(failure("failed_precondition", (e as Error).message, 412));
            }
            state.syncCalls.push(body.mutations.length);
            state.receipts.set(body.mutationId, state.revision);
            if (state.dropNextSyncReply > 0) {
              state.dropNextSyncReply--;
              return route.fulfill(failure("unavailable", "reply lost", 503));
            }
            return send(footprint.SyncChangesResponseSchema, {
              revision: BigInt(state.revision), changedCount: body.mutations.length,
            });
          }
          case "CreateSnapshot": {
            const body = read(snapshotPb.CreateSnapshotRequestSchema) as snapshotPb.CreateSnapshotRequest;
            state.snapshotCalls.push(body.reason);
            if (state.failCreateSnapshot > 0) {
              state.failCreateSnapshot--;
              return route.fulfill(failure("unavailable", "offline", 503));
            }
            const { snap, created } = capture(body.reason);
            return send(snapshotPb.CreateSnapshotResponseSchema, { snapshot: snapInfo(snap), created });
          }
          case "ListSnapshots":
            return send(snapshotPb.ListSnapshotsResponseSchema, {
              snapshots: [...state.snapshots].reverse().map(snapInfo),
            });
          case "GetSnapshot": {
            const body = read(snapshotPb.GetSnapshotRequestSchema) as snapshotPb.GetSnapshotRequest;
            const snap = state.snapshots.find((s) => s.id === body.id);
            if (!snap) return route.fulfill(failure("not_found", "snapshot not found", 404));
            return send(snapshotPb.GetSnapshotResponseSchema, {
              snapshot: snapInfo(snap),
              data: {
                visits: snap.visits.map((v) => ({ ...v, date: "", note: "", updatedAt: ts() })),
                trips: snap.trips.map(wireTrip),
                provinceMarks: Object.entries(snap.marks).map(([province, color]) => ({ province, color, updatedAt: ts() })),
                schemaVersion: 2,
              },
            });
          }
          case "RestoreSnapshot": {
            const body = read(snapshotPb.RestoreSnapshotRequestSchema) as snapshotPb.RestoreSnapshotRequest;
            state.restoreCalls++;
            state.restoreIds.push(body.mutationId);
            if (state.failRestore > 0) {
              state.failRestore--;
              return route.fulfill(failure("unavailable", "offline", 503));
            }
            const replay = state.restoreReceipts.get(body.mutationId);
            if (replay) {
              const undo = state.snapshots.find((s) => s.id === replay.undoId);
              return send(snapshotPb.RestoreSnapshotResponseSchema, {
                revision: BigInt(replay.revision),
                changedCount: replay.changed,
                undoSnapshot: undo ? snapInfo(undo) : undefined,
                replayed: true,
              });
            }
            if (Number(body.baseRevision) !== state.revision)
              return route.fulfill(failure("aborted", "footprints changed since base_revision", 409));
            const target = state.snapshots.find((s) => s.id === body.snapshotId);
            if (!target) return route.fulfill(failure("not_found", "snapshot not found", 404));
            const { snap: undo } = capture("before_restore");
            let changed = 0;
            const wantCities = new Map(target.visits.map((v) => [v.code, v]));
            const wantTrips = new Map(target.trips.map((t) => [t.id, t]));
            for (const t of [...state.trips.values()]) {
              if (wantTrips.has(t.id) && wantCities.has(t.code) && wantTrips.get(t.id)!.code === t.code) continue;
              if (!state.trips.delete(t.id)) continue;
              // A trip of a city that goes away is removed by that city's own deletion.
              if (!wantCities.has(t.code)) continue;
              logChange(mutation({ case: "deleteTrip", value: { regionCode: t.code, id: t.id } }));
              changed++;
            }
            for (const code of [...state.visits.keys()]) {
              if (wantCities.has(code)) continue;
              state.visits.delete(code);
              logChange(mutation({ case: "deleteVisit", value: code }));
              changed++;
            }
            for (const [code, v] of wantCities) {
              const have = state.visits.get(code);
              if (have && JSON.stringify(have) === JSON.stringify(v)) continue;
              state.visits.set(code, { ...v });
              logChange(upsertCityMutation(v));
              changed++;
            }
            for (const [id, t] of wantTrips) {
              const have = state.trips.get(id);
              if (have && JSON.stringify(have) === JSON.stringify(t)) continue;
              state.trips.set(id, { ...t });
              logChange(upsertTripMutation(t));
              changed++;
            }
            for (const province of [...state.marks.keys()]) {
              if (province in target.marks) continue;
              state.marks.delete(province);
              logChange(mutation({ case: "deleteMark", value: province }));
              changed++;
            }
            for (const [province, color] of Object.entries(target.marks)) {
              if (state.marks.get(province) === color) continue;
              state.marks.set(province, color);
              logChange(mutation({ case: "upsertMark", value: { province, color } }));
              changed++;
            }
            state.restoreReceipts.set(body.mutationId, { revision: state.revision, changed, undoId: undo.id });
            return send(snapshotPb.RestoreSnapshotResponseSchema, {
              revision: BigInt(state.revision),
              changedCount: changed,
              undoSnapshot: snapInfo(undo),
            });
          }
          case "GetCollaborationOverview":
            return send(collaborationPb.GetCollaborationOverviewResponseSchema, {
              myFriendCode: "fc_ci_user_1234",
              friendSettings: { showCities: true, showDates: false, showNotes: false },
              friends: [],
              sharedTrips: [],
            });
          case "SearchNotes": {
            const body = read(searchPb.SearchNotesRequestSchema) as searchPb.SearchNotesRequest;
            state.searchCalls.push(body.query);
            if (state.failSearch > 0) {
              state.failSearch--;
              return route.fulfill(failure("unavailable", "offline", 503));
            }
            if (body.expectedRevision !== undefined && body.expectedRevision !== BigInt(state.revision)) return route.fulfill(failure("aborted", "restart search", 409));
            const visits = [...state.visits.values()].map(v => ({ ...v, trips: [...state.trips.values()].filter(t => t.code === v.code).map(t => ({ id: t.id, date: t.date, note: t.note })) }));
            const matches = searchNotes({ version: 2, visits, provinces: Object.fromEntries(state.marks) }, body.query, body.provinceCode);
            const limit = body.limit || 20;
            return send(searchPb.SearchNotesResponseSchema, { matches: matches.slice(body.offset, body.offset + limit), revision: BigInt(state.revision), nextOffset: Math.min(body.offset + limit, matches.length), hasMore: matches.length > body.offset + limit });
          }
          case "GetReport": {
            const body = read(reportPb.GetReportRequestSchema) as reportPb.GetReportRequest;
            const saved = {
              version: 2 as const,
              provinces: Object.fromEntries(state.marks),
              visits: [...state.visits.values()].map(v => ({
                ...v,
                trips: [...state.trips.values()]
                  .filter(t => t.code === v.code)
                  .map(t => ({ id: t.id, date: t.date, note: t.note })),
              })),
            };
            const computed = buildReport(saved, {
              timezone: body.timezone || undefined,
              year: body.year || undefined,
              revision: state.revision,
            });
            return send(reportPb.GetReportResponseSchema, {
              ...computed,
              revision: BigInt(computed.revision),
              fromCache: true,
            });
          }
          default:
            return route.fulfill({ status: 404, contentType: "application/json", body: '{"code":"unimplemented"}' });
        }
      }),
  };
}

/** Writes version 1 data, as an older release saved it. */
export async function seedLocal(page: Page, visits: V[], provinces: Record<string, string> = {}) {
  await page.addInitScript(
    (data) => {
      if (!localStorage.getItem("shanhai-travel-v1")) {
        localStorage.setItem("shanhai-travel-v1", JSON.stringify(data));
      }
    },
    { version: 1, visits, provinces },
  );
}

export async function login(page: Page) {
  await page.goto("/");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill("ci_user");
  await page.getByLabel("密码", { exact: true }).fill("correct horse battery");
  await page.getByRole("button", { name: "登录", exact: true }).click();
}


/** Writes exactly this text to the local footprint key before the page loads. */
export async function seedLocalRaw(page: Page, raw: string) {
  await page.addInitScript((text) => {
    if (!localStorage.getItem("shanhai-travel-v1")) localStorage.setItem("shanhai-travel-v1", text);
  }, raw);
}
