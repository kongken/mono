import { test, expect, type Page } from "@playwright/test";
import {
  dongcheng, fakeBackend, gold, green, login, seedLocal, tianjin, xicheng,
} from "./fake-backend";

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
  await expect(summary).toContainText("4新增到云端");
  await expect(summary).toContainText("3新增旅行记录");
  expect(await noOverflow(page)).toBe(true);
  await dialog(page).getByRole("button", { name: "确认迁移" }).click();
  await expect(dialog(page).getByText(/迁移完成/)).toBeVisible();
  // 3 cities and their 3 trips (city first, so every prefix is consistent), plus 1 province mark.
  expect(backend.state.syncCalls).toEqual([7]);
  expect(backend.state.batches[0].slice(0, 3)).toEqual(["upsertCity", "upsertCity", "upsertCity"]);
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
  await expect(dialog(page).getByText("3 条记录两边内容不同")).toBeVisible();
  // Default keeps the cloud copy for every conflict. 东城区 differs in its color and in its
  // trip, which are two separate records; choose local for both and keep the cloud 西城区.
  const rows = dialog(page).locator(".migration-conflict");
  await expect(rows).toHaveCount(3);
  const dongchengRows = rows.filter({ hasText: "东城区" });
  await expect(dongchengRows).toHaveCount(2);
  for (const row of await dongchengRows.all()) await row.getByRole("button", { name: /用本机/ }).click();
  await dialog(page).getByRole("button", { name: "确认迁移" }).click();
  await expect(dialog(page).getByText(/迁移完成/)).toBeVisible();

  expect(backend.noteOf("110101")).toBe("本机");
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
  expect(backend.state.syncCalls).toEqual([4]);
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
