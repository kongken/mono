import { test, expect, type Page } from "@playwright/test";
import { fakeBackend, login, green, dongcheng } from "./fake-backend";
import { REGION_VERSION } from "../src/lib/region-catalog";

const island = { code: "tw-09020", name: "金门县", province: "710000", color: green, date: "2026-10-14", note: "旧旅行记忆 🏔️" };
const dialog = (page: Page) => page.getByRole("dialog", { name: "区域数据与兼容" });
async function openRegions(page: Page) {
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "区域数据与兼容", exact: true }).click();
  await expect(dialog(page)).toBeVisible();
}
const cached = (page: Page) => page.evaluate(() => JSON.parse(localStorage.getItem("shanhai-cloud-v2:ci-migrate") ?? "null"));

test("regional preview is read-only; confirmation keeps journeys, marks and the original snapshot", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [island], marks: { "710000": green } });
  await backend.install();
  await login(page);
  await openRegions(page);
  await expect(dialog(page).getByRole("list", { name: "区域迁移预览" })).toContainText("金门县");
  await expect(dialog(page)).toContainText("台湾");
  await expect(dialog(page)).toContainText("福建");
  expect(backend.state.snapshots).toHaveLength(0);
  expect(backend.state.visits.get(island.code)?.province).toBe("710000");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const button = dialog(page).getByRole("button", { name: "确认迁移区域", exact: true });
  await expect(button).toBeEnabled();
  await button.click();
  await expect(dialog(page)).toContainText("已迁移 1 条区域记录");
  await expect.poll(async () => (await cached(page))?.revision).toBe(backend.state.revision);
  const cache = await cached(page);
  expect(cache.regionVersion).toBe(REGION_VERSION);
  expect(cache.saved.visits[0].province).toBe("350000");
  expect(cache.saved.visits[0].trips[0]).toEqual({ id: "legacy-tw-09020", date: island.date, note: island.note });
  expect(cache.saved.provinces["710000"]).toBe(green);
  expect(backend.state.snapshots).toHaveLength(1);
  expect(backend.state.snapshots[0].visits[0].province).toBe("710000");
  expect(backend.state.regionHeaders).toEqual(new Set([REGION_VERSION]));
});

test("a lost region-upgrade reply retries the same mutation instead of migrating twice", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [island] });
  backend.state.dropNextRegionReply = 1;
  await backend.install();
  await login(page);
  await openRegions(page);
  await dialog(page).getByRole("button", { name: "确认迁移区域", exact: true }).click();
  await expect(dialog(page).getByRole("alert")).toContainText("无法连接服务器");
  await dialog(page).getByRole("button", { name: "重试确认迁移", exact: true }).click();
  await expect(dialog(page)).toContainText("已迁移 1 条区域记录");
  expect(backend.state.regionApplyIds).toHaveLength(2);
  expect(new Set(backend.state.regionApplyIds).size).toBe(1);
  expect(backend.state.snapshots).toHaveLength(1);
});

test("changed cloud revision refuses the entire upgrade and allows a fresh preview", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [island] });
  await backend.install();
  await login(page);
  await openRegions(page);
  await expect(dialog(page)).toContainText("将迁移 1 条区域记录");
  backend.otherDeviceUpsert(dongcheng({ note: "别的设备" }));
  await dialog(page).getByRole("button", { name: "确认迁移区域", exact: true }).click();
  await expect(dialog(page).getByRole("alert")).toContainText("重新预览");
  expect(backend.state.snapshots).toHaveLength(0);
  expect(backend.state.visits.get(island.code)?.province).toBe("710000");
  await dialog(page).getByRole("button", { name: "重新预览", exact: true }).click();
  await expect(dialog(page).getByRole("alert")).toHaveCount(0);
  await expect(dialog(page)).toContainText(`云端版本 ${backend.state.revision}`);
});

test("unmapped/colliding cloud records are shown but never partially migrated", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [island] });
  backend.state.regionBlockers = ["region collision: old / new"];
  await backend.install();
  await login(page);
  await openRegions(page);
  await expect(dialog(page).getByRole("alert")).toContainText("没有修改任何内容");
  await expect(dialog(page).getByRole("button", { name: "确认迁移区域", exact: true })).toBeDisabled();
  expect(backend.state.regionApplyIds).toHaveLength(0);
  expect(backend.state.snapshots).toHaveLength(0);
});

test("front/backend region mismatch preserves the cached target and never sends a batch", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [island] }, { regionVersion: "sha256:newer-server" });
  const source = { schema: 2, saved: { version: 2, visits: [], provinces: { "330000": green } }, confirmed: { version: 2, visits: [], provinces: {} }, revision: 0 };
  await page.addInitScript(data => localStorage.setItem("shanhai-cloud-v2:ci-migrate", JSON.stringify(data)), source);
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await openRegions(page);
  await expect(dialog(page).getByRole("alert")).toContainText("区域索引版本不一致");
  await expect(dialog(page).getByRole("button", { name: "确认迁移区域", exact: true })).toBeDisabled();
  expect((await cached(page)).saved.provinces["330000"]).toBe(green);
  expect(backend.state.syncCalls).toHaveLength(0);
  expect(backend.state.regionApplyIds).toHaveLength(0);
});

test("current cloud data needs no migration or additional snapshot", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [dongcheng()] });
  await backend.install();
  await login(page);
  await openRegions(page);
  await expect(dialog(page)).toContainText("无需迁移");
  await expect(dialog(page).getByRole("button", { name: "确认迁移区域", exact: true })).toBeDisabled();
  expect(backend.state.snapshots).toHaveLength(0);
});

test("unknown local region version retains its exact bytes when merely opened", async ({ page }) => {
  const raw = JSON.stringify({ version: 2, regionVersion: "sha256:future", visits: [], provinces: { "710000": green } });
  await page.addInitScript(text => localStorage.setItem("shanhai-travel-v2", text), raw);
  await page.goto("/");
  await expect(page.getByText("数据使用未知或更新的区域索引版本", { exact: false })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("shanhai-travel-v2"))).toBe(raw);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
