import type { Page } from "@playwright/test";
import {
  create,
  toBinary,
  toJsonString,
  fromBinary,
  fromJson,
  type DescMessage,
} from "@bufbuild/protobuf";
import * as bffAuth from "@trip-map/api-client/bff/v1/auth_pb";
import * as bffItinerary from "@trip-map/api-client/bff/v1/itinerary_pb";
import * as auth from "@trip-map/api-client/tripmap/v1/auth_pb";
import * as collaborationPb from "@trip-map/api-client/tripmap/v1/collaboration_pb";
import * as footprint from "@trip-map/api-client/bff/v1/footprint_pb";
import { DEFAULT_ESTIMATION_POLICY, buildItineraryInfo, validateItineraryInput, type ItineraryRecord } from "../src/lib/itinerary";
import * as notificationPb from "@trip-map/api-client/bff/v1/notification_pb";
import * as reportPb from "@trip-map/api-client/bff/v1/report_pb";
import * as regionsPb from "@trip-map/api-client/bff/v1/region_pb";
import catalog from "@trip-map/geo-data/region-catalog.json" with { type: "json" };
import areas from "@trip-map/geo-data/regions.json" with { type: "json" };
import { regionCatalog, REGION_VERSION } from "../src/lib/region-catalog";
import * as searchPb from "@trip-map/api-client/bff/v1/search_pb";
import * as snapshotPb from "@trip-map/api-client/bff/v1/snapshot_pb";
import * as statusPb from "@trip-map/api-client/bff/v1/status_pb";
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
  options: { footprintSchema?: number; shareEnabled?: boolean; regionVersion?: string; email?: boolean; push?: boolean } = {},
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
    regionVersion: options.regionVersion ?? REGION_VERSION,
    regionHeaders: new Set<string>(),
    regionCalls: [] as string[],
    regionApplyIds: [] as string[],
    regionBlockers: [] as string[],
    dropNextRegionReply: 0,
    /** F11 itineraries of the signed-in account, by id. */
    itineraries: new Map<string, ItineraryRecord>(),
    itineraryCalls: [] as string[],
    itineraryMutations: new Map<string, string>(),
    failItinerarySave: 0,
    /** Commit the next itinerary save but lose its reply. */
    dropNextItineraryReply: 0,
    /** F12 reminder settings of the signed-in account (all off by default). */
    notify: {
      prefs: new Map<string, boolean>(),
      timezone: "Asia/Shanghai",
      sendHour: 9,
      showPlaces: false,
      email: "",
      pendingEmail: "",
      code: "123456",
      endpoints: [] as string[],
      calls: [] as string[],
      updates: [] as { timezone: string; sendHour: number; showPlaces: boolean; on: string[] }[],
      tests: [] as string[],
      emailAvailable: options.email ?? true,
      pushAvailable: options.push ?? true,
      failTest: false,
    },
    regionReceipts: new Map<string, { request: string; revision: number; changed: number; snapshotId: string }>(),
  };
  state.seedRevision = state.revision;
  const ts = () => ({ seconds: BigInt(Math.floor(Date.now() / 1000)) });
  const tripsOf = (code: string) => [...state.trips.values()].filter((t) => t.code === code);
  const wireTrip = (t: StoredTrip) => ({
    id: t.id, city: t.code, date: t.date, note: t.note, updatedAt: ts(),
  });
  const wireBffTrip = (t: StoredTrip) => ({
    id: t.id, city: t.code, date: t.date, note: t.note, updatedAt: ts(),
  });
  const bffUser = () => ({
    id: user.id,
    username: user.username,
    displayName: user.displayName,
    avatarUrl: "",
    createdAt: ts(),
    deletionPending: false,
  });
  const csrfToken = "ci-csrf-test-token";
  const wireItinerary = (r: ItineraryRecord) => {
    const info = buildItineraryInfo(r);
    return { ...info, version: BigInt(info.version), createdAt: ts(), updatedAt: ts() };
  };
  const mask = (a: string) => (a ? `${a[0]}***${a.slice(a.lastIndexOf("@"))}` : "");
  const wireNotify = () => ({
    preferences: ["memory", "annual_report"].flatMap((kind) =>
      ["email", "push"].map((channel) => ({ kind, channel, enabled: state.notify.prefs.get(`${kind}/${channel}`) ?? false })),
    ),
    timezone: state.notify.timezone,
    sendHour: state.notify.sendHour,
    showPlaces: state.notify.showPlaces,
    emailMasked: mask(state.notify.email),
    emailVerified: !!state.notify.email,
    pendingEmailMasked: mask(state.notify.pendingEmail),
    pushSubscriptionCount: state.notify.endpoints.length,
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
    mutation({ case: "upsertTrip", value: { id: t.id, city: t.code, date: t.date, note: t.note } });
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
        if (!state.visits.has(t.city)) throw new Error("trip city does not exist");
        state.trips.set(t.id, { id: t.id, code: t.city, date: t.date, note: t.note });
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
      logChange(mutation({ case: "deleteTrip", value: { city: t.code, id } }));
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
        // The anonymous status query (bff.v1) carries no client capability headers.
        if (method !== "GetStatus") state.regionHeaders.add(req.headers()["x-tripmap-regions"] ?? "");
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
            return send(statusPb.GetStatusResponseSchema, { footprintSchema: state.footprintSchema, shareEnabled: options.shareEnabled ?? false, regionVersion: state.regionVersion });
          case "IssueCSRFToken":
            return send(bffAuth.IssueCSRFTokenResponseSchema, { token: csrfToken });
          case "Register":
            return send(bffAuth.RegisterResponseSchema, {
              user: bffUser(),
              expiresAt: { seconds: BigInt(Math.floor(Date.now() / 1000) + 3600) },
            });
          case "Login":
            return send(bffAuth.LoginResponseSchema, {
              user: bffUser(),
              expiresAt: { seconds: BigInt(Math.floor(Date.now() / 1000) + 3600) },
            });
          case "Logout":
            return send(bffAuth.LogoutResponseSchema, {});
          case "ListOAuthProviders":
            return send(bffAuth.ListOAuthProvidersResponseSchema, {});
          case "BeginOAuthFlow":
            return send(bffAuth.BeginOAuthFlowResponseSchema, {
              authorizeUrl: "https://example.test/oauth?state=ci-state",
              state: "ci-state",
            });
          case "CompleteOAuthFlow":
            return send(bffAuth.CompleteOAuthFlowResponseSchema, {
              user,
              expiresAt: { seconds: BigInt(Math.floor(Date.now() / 1000) + 3600) },
            });
          case "Me":
            return send(bffAuth.MeResponseSchema, { user: bffUser() });
          case "ListSessions":
            return send(
              req.url().includes("bff.v1") ? bffAuth.ListSessionsResponseSchema : auth.ListSessionsResponseSchema,
              {},
            );
          case "RevokeSession":
            return send(
              req.url().includes("bff.v1") ? bffAuth.RevokeSessionResponseSchema : auth.RevokeSessionResponseSchema,
              {},
            );
          case "GetRegionIndex": {
            state.regionCalls.push(method);
            const body = read(regionsPb.GetRegionIndexRequestSchema) as regionsPb.GetRegionIndexRequest;
            const notModified = body.knownVersion === state.regionVersion;
            return send(regionsPb.GetRegionIndexResponseSchema, { version: state.regionVersion, notModified, indexJson: notModified ? "" : JSON.stringify(areas), supportedVersions: catalog.supportedVersions, mappings: notModified ? [] : catalog.mappings });
          }
          case "PreviewRegionUpgrade": {
            state.regionCalls.push(method);
            const changes = [...state.visits.values()].flatMap(v => {
              const ref = regionCatalog.city(v.code, v.name, v.province);
              return ref && (ref.code !== v.code || ref.name !== v.name || ref.province !== v.province) ? [{ fromCode: v.code, fromName: v.name, fromProvince: v.province, toCode: ref.code, toName: ref.name, toProvince: ref.province }] : [];
            });
            return send(regionsPb.PreviewRegionUpgradeResponseSchema, { version: state.regionVersion, revision: BigInt(state.revision), changes, blockers: state.regionBlockers });
          }
          case "ApplyRegionUpgrade": {
            state.regionCalls.push(method);
            const body = read(regionsPb.ApplyRegionUpgradeRequestSchema) as regionsPb.ApplyRegionUpgradeRequest;
            state.regionApplyIds.push(body.mutationId);
            const request = toJsonString(regionsPb.ApplyRegionUpgradeRequestSchema, body);
            const receipt = state.regionReceipts.get(body.mutationId);
            if (receipt) {
              if (receipt.request !== request) return route.fulfill(failure("already_exists", "mutation_id was already used", 409));
              return send(regionsPb.ApplyRegionUpgradeResponseSchema, { version: body.version, revision: BigInt(receipt.revision), changedCount: receipt.changed, snapshotId: receipt.snapshotId, replayed: true });
            }
            if (body.version !== state.regionVersion) return route.fulfill(failure("failed_precondition", "region index version mismatch", 412));
            if (Number(body.baseRevision) !== state.revision) return route.fulfill(failure("aborted", "footprints changed since region preview", 409));
            if (state.regionBlockers.length) return route.fulfill(failure("failed_precondition", "region upgrade blocked", 412));
            const changes = [...state.visits.values()].flatMap(v => {
              const ref = regionCatalog.city(v.code, v.name, v.province);
              return ref && (ref.code !== v.code || ref.name !== v.name || ref.province !== v.province) ? [{ ...v, ...ref }] : [];
            });
            const snap = changes.length ? capture("before_migration").snap : null;
            for (const v of changes) {
              state.visits.set(v.code, v);
              logChange(create(footprint.FootprintMutationSchema, { operation: { case: "upsertCity", value: v } }));
            }
            const saved = { request, revision: state.revision, changed: changes.length, snapshotId: snap?.id ?? "" };
            state.regionReceipts.set(body.mutationId, saved);
            if (state.dropNextRegionReply > 0) { state.dropNextRegionReply--; return route.fulfill(failure("unavailable", "lost reply", 503)); }
            return send(regionsPb.ApplyRegionUpgradeResponseSchema, { version: body.version, revision: BigInt(saved.revision), changedCount: saved.changed, snapshotId: saved.snapshotId });
          }
          case "GetFootprints":
            return send(footprint.GetFootprintsResponseSchema, {
              revision: BigInt(state.revision),
              visits: [...state.visits.values()].map((v) => ({ ...v, date: "", note: "", updatedAt: ts() })),
              trips: [...state.trips.values()].map(wireBffTrip),
              provinceMarks: [...state.marks].map(([province, color]) => ({ province, color, updatedAt: ts() })),
              schemaVersion: state.footprintSchema,
              regionVersion: state.regionVersion,
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
              regionVersion: state.regionVersion,
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
              logChange(mutation({ case: "deleteTrip", value: { city: t.code, id: t.id } }));
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
          case "ListItineraries":
            state.itineraryCalls.push(method);
            return send(bffItinerary.ListItinerariesResponseSchema, {
              itineraries: [...state.itineraries.values()].map(wireItinerary),
              policy: DEFAULT_ESTIMATION_POLICY,
            });
          case "SaveItinerary": {
            const body = read(bffItinerary.SaveItineraryRequestSchema) as bffItinerary.SaveItineraryRequest;
            state.itineraryCalls.push(method);
            if (state.failItinerarySave > 0) {
              state.failItinerarySave--;
              return route.fulfill(failure("unavailable", "offline", 503));
            }
            const prior = state.itineraries.get(body.id);
            const request = JSON.stringify([body.id, body.title, body.note, body.color, body.stops.map((x) => [x.id, x.regionCode, x.date, x.note, x.transport])]);
            if (body.mutationId && state.itineraryMutations.has(body.mutationId)) {
              if (state.itineraryMutations.get(body.mutationId) !== request) return route.fulfill(failure("already_exists", "mutation_id was already used", 409));
              return send(bffItinerary.SaveItineraryResponseSchema, { itinerary: wireItinerary(prior!), policy: DEFAULT_ESTIMATION_POLICY, replayed: true });
            }
            if (body.expectedVersion !== 0n && BigInt(prior?.version ?? 0) !== body.expectedVersion) return route.fulfill(failure("aborted", "itinerary changed, reload", 409));
            let record: ItineraryRecord;
            try {
              record = validateItineraryInput({
                id: body.id, title: body.title, note: body.note, color: body.color,
                version: (prior?.version ?? 0) + 1,
                stops: body.stops.map((x) => ({ id: x.id, regionCode: x.regionCode, date: x.date, note: x.note, transport: x.transport, tripId: x.tripId })),
              });
            } catch (error) {
              return route.fulfill(failure("invalid_argument", String(error), 400));
            }
            state.itineraries.set(record.id, record);
            if (body.mutationId) state.itineraryMutations.set(body.mutationId, request);
            if (state.dropNextItineraryReply > 0) {
              state.dropNextItineraryReply--;
              return route.fulfill(failure("unavailable", "lost reply", 503));
            }
            return send(bffItinerary.SaveItineraryResponseSchema, { itinerary: wireItinerary(record), policy: DEFAULT_ESTIMATION_POLICY, replayed: false });
          }
          case "DeleteItinerary": {
            const body = read(bffItinerary.DeleteItineraryRequestSchema) as bffItinerary.DeleteItineraryRequest;
            state.itineraryCalls.push(method);
            const existed = state.itineraries.delete(body.id);
            return send(bffItinerary.DeleteItineraryResponseSchema, { deleted: existed });
          }
          case "GetNotificationSettings":
            state.notify.calls.push(method);
            return send(notificationPb.GetNotificationSettingsResponseSchema, {
              settings: wireNotify(),
              emailAvailable: state.notify.emailAvailable,
              pushAvailable: state.notify.pushAvailable,
              vapidPublicKey: state.notify.pushAvailable ? "BEl62iUYgUivxIkv69yViEuiBIa-Ib9-SkvMeAtA3LFgDzkrxZJjSgSnfckjBJuBkr3qBUYIHBQFLXYp5Nksh8U" : "",
            });
          case "UpdateNotificationSettings": {
            const body = read(notificationPb.UpdateNotificationSettingsRequestSchema) as notificationPb.UpdateNotificationSettingsRequest;
            state.notify.calls.push(method);
            for (const p of body.preferences) {
              if (p.enabled && p.channel === "email" && !state.notify.email) return route.fulfill(failure("failed_precondition", "verify an email address first", 400));
              state.notify.prefs.set(`${p.kind}/${p.channel}`, p.enabled);
            }
            state.notify.timezone = body.timezone;
            state.notify.sendHour = body.sendHour;
            state.notify.showPlaces = body.showPlaces;
            state.notify.updates.push({ timezone: body.timezone, sendHour: body.sendHour, showPlaces: body.showPlaces, on: [...state.notify.prefs].filter(([, v]) => v).map(([k]) => k).sort() });
            return send(notificationPb.UpdateNotificationSettingsResponseSchema, { settings: wireNotify() });
          }
          case "RequestEmailVerification": {
            const body = read(notificationPb.RequestEmailVerificationRequestSchema) as notificationPb.RequestEmailVerificationRequest;
            state.notify.calls.push(method);
            if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(body.email)) return route.fulfill(failure("invalid_argument", "enter a valid email address", 400));
            state.notify.pendingEmail = body.email.toLowerCase();
            return send(notificationPb.RequestEmailVerificationResponseSchema, { pendingEmailMasked: mask(state.notify.pendingEmail), expiresAt: ts() });
          }
          case "ConfirmEmail": {
            const body = read(notificationPb.ConfirmEmailRequestSchema) as notificationPb.ConfirmEmailRequest;
            state.notify.calls.push(method);
            if (!state.notify.pendingEmail) return route.fulfill(failure("failed_precondition", "the code expired or was used up", 400));
            if (body.code !== state.notify.code) return route.fulfill(failure("invalid_argument", "the code is not correct", 400));
            state.notify.email = state.notify.pendingEmail;
            state.notify.pendingEmail = "";
            return send(notificationPb.ConfirmEmailResponseSchema, { settings: wireNotify() });
          }
          case "RemoveEmail":
            state.notify.calls.push(method);
            state.notify.email = "";
            state.notify.pendingEmail = "";
            for (const kind of ["memory", "annual_report"]) state.notify.prefs.set(`${kind}/email`, false);
            return send(notificationPb.RemoveEmailResponseSchema, { settings: wireNotify() });
          case "RegisterPushSubscription": {
            const body = read(notificationPb.RegisterPushSubscriptionRequestSchema) as notificationPb.RegisterPushSubscriptionRequest;
            state.notify.calls.push(method);
            if (!state.notify.endpoints.includes(body.endpoint)) state.notify.endpoints.push(body.endpoint);
            return send(notificationPb.RegisterPushSubscriptionResponseSchema, { pushSubscriptionCount: state.notify.endpoints.length });
          }
          case "RemovePushSubscription": {
            const body = read(notificationPb.RemovePushSubscriptionRequestSchema) as notificationPb.RemovePushSubscriptionRequest;
            state.notify.calls.push(method);
            state.notify.endpoints = state.notify.endpoints.filter((e) => e !== body.endpoint);
            return send(notificationPb.RemovePushSubscriptionResponseSchema, { pushSubscriptionCount: state.notify.endpoints.length });
          }
          case "SendTestNotification": {
            const body = read(notificationPb.SendTestNotificationRequestSchema) as notificationPb.SendTestNotificationRequest;
            state.notify.calls.push(method);
            if (state.notify.failTest) return route.fulfill(failure("unavailable", "could not send the test email", 503));
            state.notify.tests.push(body.channel);
            return send(notificationPb.SendTestNotificationResponseSchema, { delivered: 1 });
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
