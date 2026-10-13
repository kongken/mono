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
import * as footprint from "../src/gen/tripmap/v1/footprint_pb";
import * as snapshotPb from "../src/gen/tripmap/v1/snapshot_pb";

export const green = "#4b937d";
export const gold = "#e2af5c";
export const user = { id: "ci-migrate", username: "ci_user", displayName: "CI 旅行者" };

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

type Snap = {
  id: string;
  reason: string;
  createdAt: number;
  revision: number;
  visits: V[];
  marks: Record<string, string>;
};

// A tiny in-memory FootprintService: enough of the real contract (revision
// check, atomic batch, idempotent replay) and SnapshotService (one snapshot
// per revision, conditional atomic restore) to drive the migration and
// snapshot UI.
export function fakeBackend(page: Page, initial: { visits: V[]; marks?: Record<string, string> }) {
  const state = {
    revision: initial.visits.length + Object.keys(initial.marks ?? {}).length,
    visits: new Map(initial.visits.map((v) => [v.code, v])),
    marks: new Map(Object.entries(initial.marks ?? {})),
    syncCalls: [] as number[],
    seedRevision: 0,
    log: [] as { revision: number; mutation: footprint.FootprintMutation }[],
    failSync: 0,
    receipts: new Map<string, number>(),
    snapshots: [] as Snap[],
    snapshotCalls: [] as string[],
    failCreateSnapshot: 0,
    failRestore: 0,
    restoreReceipts: new Map<string, { revision: number; changed: number; undoId: string }>(),
    restoreCalls: 0,
    restoreIds: [] as string[],
  };
  state.seedRevision = state.revision;
  const ts = () => ({ seconds: BigInt(Math.floor(Date.now() / 1000)) });
  const snapInfo = (snap: Snap) => ({
    id: snap.id,
    reason: snap.reason,
    createdAt: { seconds: BigInt(Math.floor(snap.createdAt / 1000)) },
    visitCount: snap.visits.length,
    provinceMarkCount: Object.keys(snap.marks).length,
    revision: BigInt(snap.revision),
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
      marks: Object.fromEntries(state.marks),
    };
    state.snapshots.push(snap);
    return { snap, created: true };
  };
  const logChange = (mutation: footprint.FootprintMutation) => {
    state.revision++;
    state.log.push({ revision: state.revision, mutation });
  };
  const upsertVisitMutation = (visit: V) =>
    create(footprint.FootprintMutationSchema, { operation: { case: "upsertVisit", value: visit } });
  return {
    state,
    /** A write made by another device: it lands in the change feed. */
    otherDeviceUpsert: (visit: V) => {
      state.visits.set(visit.code, visit);
      logChange(upsertVisitMutation(visit));
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
        switch (method) {
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
              visits: [...state.visits.values()].map((v) => ({ ...v, updatedAt: ts() })),
              provinceMarks: [...state.marks].map(([province, color]) => ({ province, color, updatedAt: ts() })),
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
            });
          }
          case "SyncChanges": {
            let body: footprint.SyncChangesRequest;
            body = read(footprint.SyncChangesRequestSchema) as footprint.SyncChangesRequest;
            state.syncCalls.push(body.mutations.length);
            if (state.failSync > 0) {
              state.failSync--;
              return route.fulfill({
                status: 503,
                contentType: "application/json",
                body: JSON.stringify({ code: "unavailable", message: "offline" }),
              });
            }
            const replayed = state.receipts.get(body.mutationId);
            if (replayed !== undefined) {
              return send(footprint.SyncChangesResponseSchema, {
                revision: BigInt(replayed), changedCount: body.mutations.length, replayed: true,
              });
            }
            for (const m of body.mutations) {
              const op = m.operation;
              if (op.case === "upsertVisit") state.visits.set(op.value.code, op.value as unknown as V);
              else if (op.case === "deleteVisit") state.visits.delete(op.value);
              else if (op.case === "upsertMark") state.marks.set(op.value.province, op.value.color);
              else if (op.case === "deleteMark") state.marks.delete(op.value);
              state.revision++;
              state.log.push({ revision: state.revision, mutation: m });
            }
            state.receipts.set(body.mutationId, state.revision);
            return send(footprint.SyncChangesResponseSchema, {
              revision: BigInt(state.revision), changedCount: body.mutations.length,
            });
          }
          case "CreateSnapshot": {
            const body = read(snapshotPb.CreateSnapshotRequestSchema) as snapshotPb.CreateSnapshotRequest;
            state.snapshotCalls.push(body.reason);
            if (state.failCreateSnapshot > 0) {
              state.failCreateSnapshot--;
              return route.fulfill({
                status: 503,
                contentType: "application/json",
                body: JSON.stringify({ code: "unavailable", message: "offline" }),
              });
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
            if (!snap)
              return route.fulfill({
                status: 404,
                contentType: "application/json",
                body: JSON.stringify({ code: "not_found", message: "snapshot not found" }),
              });
            return send(snapshotPb.GetSnapshotResponseSchema, {
              snapshot: snapInfo(snap),
              data: {
                visits: snap.visits.map((v) => ({ ...v, updatedAt: ts() })),
                provinceMarks: Object.entries(snap.marks).map(([province, color]) => ({ province, color, updatedAt: ts() })),
              },
            });
          }
          case "RestoreSnapshot": {
            const body = read(snapshotPb.RestoreSnapshotRequestSchema) as snapshotPb.RestoreSnapshotRequest;
            state.restoreCalls++;
            state.restoreIds.push(body.mutationId);
            if (state.failRestore > 0) {
              state.failRestore--;
              return route.fulfill({
                status: 503,
                contentType: "application/json",
                body: JSON.stringify({ code: "unavailable", message: "offline" }),
              });
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
              return route.fulfill({
                status: 409,
                contentType: "application/json",
                body: JSON.stringify({ code: "aborted", message: "footprints changed since base_revision" }),
              });
            const target = state.snapshots.find((s) => s.id === body.snapshotId);
            if (!target)
              return route.fulfill({
                status: 404,
                contentType: "application/json",
                body: JSON.stringify({ code: "not_found", message: "snapshot not found" }),
              });
            const { snap: undo } = capture("before_restore");
            let changed = 0;
            const want = new Map(target.visits.map((v) => [v.code, v]));
            for (const code of [...state.visits.keys()]) {
              if (want.has(code)) continue;
              state.visits.delete(code);
              logChange(create(footprint.FootprintMutationSchema, { operation: { case: "deleteVisit", value: code } }));
              changed++;
            }
            for (const [code, v] of want) {
              const have = state.visits.get(code);
              if (have && JSON.stringify(have) === JSON.stringify(v)) continue;
              state.visits.set(code, { ...v });
              logChange(upsertVisitMutation(v));
              changed++;
            }
            for (const province of [...state.marks.keys()]) {
              if (province in target.marks) continue;
              state.marks.delete(province);
              logChange(create(footprint.FootprintMutationSchema, { operation: { case: "deleteMark", value: province } }));
              changed++;
            }
            for (const [province, color] of Object.entries(target.marks)) {
              if (state.marks.get(province) === color) continue;
              state.marks.set(province, color);
              logChange(
                create(footprint.FootprintMutationSchema, { operation: { case: "upsertMark", value: { province, color } } }),
              );
              changed++;
            }
            state.restoreReceipts.set(body.mutationId, { revision: state.revision, changed, undoId: undo.id });
            return send(snapshotPb.RestoreSnapshotResponseSchema, {
              revision: BigInt(state.revision),
              changedCount: changed,
              undoSnapshot: snapInfo(undo),
            });
          }
          default:
            return route.fulfill({ status: 404, contentType: "application/json", body: '{"code":"unimplemented"}' });
        }
      }),
  };
}

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

