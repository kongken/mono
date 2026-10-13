import { test, expect, type Page } from "@playwright/test";
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

const green = "#4b937d";
const gold = "#e2af5c";
const user = { id: "ci-migrate", username: "ci_user", displayName: "CI 旅行者" };

type V = { code: string; name: string; province: string; color: string; date: string; note: string };
const dongcheng = (extra: Partial<V> = {}): V => ({
  code: "110101", name: "东城区", province: "110000", color: green, date: "2026-05-01", note: "", ...extra,
});
const xicheng = (extra: Partial<V> = {}): V => ({
  code: "110102", name: "西城区", province: "110000", color: green, date: "", note: "", ...extra,
});
const tianjin = (extra: Partial<V> = {}): V => ({
  code: "120101", name: "和平区", province: "120000", color: gold, date: "", note: "", ...extra,
});

// A tiny in-memory FootprintService: enough of the real contract (revision
// check, atomic batch, idempotent replay) to drive the migration UI.
function fakeBackend(page: Page, initial: { visits: V[]; marks?: Record<string, string> }) {
  const state = {
    revision: initial.visits.length + Object.keys(initial.marks ?? {}).length,
    visits: new Map(initial.visits.map((v) => [v.code, v])),
    marks: new Map(Object.entries(initial.marks ?? {})),
    syncCalls: [] as number[],
    seedRevision: 0,
    log: [] as { revision: number; mutation: footprint.FootprintMutation }[],
    failSync: 0,
    receipts: new Map<string, number>(),
  };
  state.seedRevision = state.revision;
  const ts = () => ({ seconds: BigInt(Math.floor(Date.now() / 1000)) });
  return {
    state,
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
          default:
            return route.fulfill({ status: 404, contentType: "application/json", body: '{"code":"unimplemented"}' });
        }
      }),
  };
}

async function seedLocal(page: Page, visits: V[], provinces: Record<string, string> = {}) {
  await page.addInitScript(
    (data) => {
      if (!localStorage.getItem("shanhai-travel-v1")) {
        localStorage.setItem("shanhai-travel-v1", JSON.stringify(data));
      }
    },
    { version: 1, visits, provinces },
  );
}

async function login(page: Page) {
  await page.goto("/");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill("ci_user");
  await page.getByLabel("密码", { exact: true }).fill("correct horse battery");
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

const dialog = (page: Page) => page.getByRole("dialog", { name: "把本机足迹迁移到云端" });
const noOverflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

test("empty cloud: auto prompt uploads everything in one batch and keeps local data", async ({ page }) => {
  const local = [dongcheng(), xicheng(), tianjin()];
  await seedLocal(page, local, { "310000": green });
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  await login(page);

  await expect(dialog(page)).toBeVisible();
  const summary = dialog(page).getByLabel("迁移预览");
  await expect(summary).toContainText("4");
  expect(await noOverflow(page)).toBe(true);
  await dialog(page).getByRole("button", { name: "确认迁移" }).click();
  await expect(dialog(page).getByText(/迁移完成/)).toBeVisible();
  expect(backend.state.syncCalls).toEqual([4]);
  expect([...backend.state.visits.keys()].sort()).toEqual(["110101", "110102", "120101"]);
  expect(backend.state.marks.get("310000")).toBe(green);
  await dialog(page).getByRole("button", { name: "完成" }).click();

  const stored = await page.evaluate(() => JSON.parse(localStorage.getItem("shanhai-travel-v1")!));
  expect(stored.visits).toHaveLength(3);
  expect(await page.evaluate(() => localStorage.getItem("shanhai-migrated-v1:ci-migrate"))).not.toBeNull();

  // Same local data is not offered again.
  await page.reload();
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await expect(dialog(page)).toHaveCount(0);
});

test("conflicts: cloud wins by default, per-item and bulk choices apply, cloud-only is kept", async ({ page }) => {
  await seedLocal(page, [dongcheng({ color: gold, note: "本机" }), xicheng({ color: gold }), tianjin()]);
  const backend = fakeBackend(page, {
    visits: [dongcheng({ note: "云端" }), xicheng(), tianjin({ code: "120102", name: "河东区" })],
  });
  await backend.install();
  await login(page);

  await expect(dialog(page)).toBeVisible();
  await expect(dialog(page).getByText("2 条记录两边内容不同")).toBeVisible();
  // Default keeps the cloud copy for both conflicts; choose local for one.
  const rows = dialog(page).locator(".migration-conflict");
  await expect(rows).toHaveCount(2);
  await rows.filter({ hasText: "东城区" }).getByRole("button", { name: /用本机/ }).click();
  await dialog(page).getByRole("button", { name: "确认迁移" }).click();
  await expect(dialog(page).getByText(/迁移完成/)).toBeVisible();

  expect(backend.state.visits.get("110101")?.note).toBe("本机");
  expect(backend.state.visits.get("110102")?.color).toBe(green); // kept cloud
  expect(backend.state.visits.has("120102")).toBe(true); // cloud-only survives
  expect(backend.state.visits.has("120101")).toBe(true); // local-only uploaded
  expect(backend.state.visits.size).toBe(4);
});

test("bulk choice 全部用本机 overwrites every conflict", async ({ page }) => {
  await seedLocal(page, [dongcheng({ color: gold }), xicheng({ color: gold })]);
  const backend = fakeBackend(page, { visits: [dongcheng(), xicheng()] });
  await backend.install();
  await login(page);
  await expect(dialog(page)).toBeVisible();
  await dialog(page).getByRole("button", { name: "全部用本机" }).click();
  await dialog(page).getByRole("button", { name: "确认迁移" }).click();
  await expect(dialog(page).getByText(/迁移完成/)).toBeVisible();
  expect([...backend.state.visits.values()].map((v) => v.color)).toEqual([gold, gold]);
});

test("later changes nothing and the account dialog can import on demand", async ({ page }) => {
  await seedLocal(page, [dongcheng()]);
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  await login(page);
  await expect(dialog(page)).toBeVisible();
  await dialog(page).getByRole("button", { name: "稍后再说" }).click();
  await expect(dialog(page)).toHaveCount(0);
  expect(backend.state.syncCalls).toEqual([]);
  expect(backend.state.visits.size).toBe(0);

  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "导入本机足迹" }).click();
  await expect(dialog(page)).toBeVisible();
  await dialog(page).getByRole("button", { name: "确认迁移" }).click();
  await expect(dialog(page).getByText(/迁移完成/)).toBeVisible();
  expect(backend.state.visits.size).toBe(1);
});

test("a failed write keeps the migration on this device and retry completes it exactly once", async ({ page }) => {
  await seedLocal(page, [dongcheng(), xicheng()]);
  const backend = fakeBackend(page, { visits: [] });
  backend.state.failSync = 1;
  await backend.install();
  await login(page);
  await expect(dialog(page)).toBeVisible();
  await dialog(page).getByRole("button", { name: "确认迁移" }).click();
  await expect(dialog(page).getByText(/同步未完成/)).toBeVisible();
  expect(backend.state.visits.size).toBe(0);
  await dialog(page).getByRole("button", { name: "重试" }).click();
  await expect(dialog(page).getByText(/迁移完成/)).toBeVisible();
  expect(backend.state.visits.size).toBe(2);
  expect(backend.state.syncCalls).toEqual([2, 2]);
});

test("identical data is not prompted and is recorded as handled", async ({ page }) => {
  await seedLocal(page, [dongcheng()]);
  const backend = fakeBackend(page, { visits: [dongcheng()] });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await expect(dialog(page)).toHaveCount(0);
  expect(backend.state.syncCalls).toEqual([]);
  await expect.poll(() => page.evaluate(() => localStorage.getItem("shanhai-migrated-v1:ci-migrate"))).not.toBeNull();
  const stored = await page.evaluate(() => JSON.parse(localStorage.getItem("shanhai-travel-v1")!));
  expect(stored.visits).toHaveLength(1);
});
