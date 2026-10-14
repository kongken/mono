import { test, expect, type Page } from "@playwright/test";
import { fakeBackend, green, gold, login, seedLocalRaw, user, type StoredTrip, type V } from "./fake-backend";

// Data-safety scenarios of F5: nothing the user typed or another device wrote may
// be dropped silently, and the canonical v2 keys are never clobbered by old pages.

const KEY = "shanhai-travel-v2";
const LEGACY = "shanhai-travel-v1";
const CACHE = `shanhai-cloud-v2:${user.id}`;
const LEGACY_CACHE = `shanhai-cloud-v1:${user.id}`;

const hangzhou = (extra: Partial<V> = {}): V => ({
  code: "330100", name: "杭州市", province: "330000", color: green, date: "2026-10-01", note: "西湖", ...extra,
});
const spring: StoredTrip = { id: "t-spring", code: "330100", date: "2026-03-01", note: "春" };
const v1File = (visits: object[], provinces: object = {}) => JSON.stringify({ version: 1, visits, provinces });
const v1Visit = (code: string, name: string, province: string, note = "") => ({
  code, name, province, color: green, date: "2026-05-01", note,
});

const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const raw = (page: Page, key: string) => page.evaluate((k) => localStorage.getItem(k), key);
const stored = (page: Page) => page.evaluate((k) => JSON.parse(localStorage.getItem(k) ?? "null"), KEY);
const editor = (page: Page) => page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "保存足迹" }) });
const stat = (page: Page, label: string) => page.locator(".stats > div").filter({ hasText: label }).locator("strong");
const card = (page: Page, date: string) =>
  editor(page).locator(".trip-card").filter({ has: page.locator(`input[value="${date}"]`) });
const signedIn = (page: Page) => expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();

async function openCity(page: Page) {
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await page.locator(".visit-card").first().click();
  await expect(editor(page)).toBeVisible();
}
const pull = (page: Page) => page.evaluate(() => window.dispatchEvent(new Event("online")));

async function emptyMedia(page: Page) {
  await page.route("**/api/tripmap.v1.MediaService/**", (route) => route.fulfill({ status: 200, json: { media: [] } }));
}

// ---------- editor draft versus live data (items 13, 18) ----------

test("an update arriving while the editor is open is kept when the editor saves", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [spring] });
  await backend.install();
  await login(page);
  await signedIn(page);
  await openCity(page);
  await card(page, "2026-10-01").locator("textarea").fill("本机改的");

  backend.otherDeviceTrip({ id: "t-remote", code: "330100", date: "2026-07-01", note: "别处新增" });
  backend.otherDeviceTrip({ ...spring, note: "别处改的春" });
  await pull(page);
  await expect(stat(page, "旅行次数")).toHaveText("3");

  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  await expect(page.getByRole("dialog", { name: "编辑期间这条记录有了新的变化" })).toHaveCount(0);
  await expect.poll(() => backend.state.trips.get("legacy-330100")?.note).toBe("本机改的");
  // The trips I did not touch keep the latest data, and the new one is not removed.
  expect(backend.state.trips.get("t-remote")?.note).toBe("别处新增");
  expect(backend.state.trips.get("t-spring")?.note).toBe("别处改的春");
  expect(backend.state.trips.size).toBe(3);
});

test("the same trip changed in both places asks, per trip, which version to keep", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [spring] });
  await backend.install();
  await login(page);
  await signedIn(page);
  await openCity(page);
  await card(page, "2026-10-01").locator("textarea").fill("本机改的");

  backend.otherDeviceTrip({ id: "t-remote", code: "330100", date: "2026-07-01", note: "别处新增" });
  backend.otherDeviceTrip({ id: "legacy-330100", code: "330100", date: "2026-10-01", note: "别处改的西湖" });
  await pull(page);
  await expect(stat(page, "旅行次数")).toHaveText("3");

  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  const dialog = page.getByRole("dialog", { name: "编辑期间这条记录有了新的变化" });
  await expect(dialog).toBeVisible();
  expect(await noOverflow(page)).toBe(true);
  const confirm = dialog.getByRole("button", { name: "确认并保存" });
  await expect(confirm).toBeDisabled();
  await dialog.getByRole("button", { name: /用我的/ }).click();
  await expect(confirm).toBeEnabled();
  await confirm.click();
  await expect(dialog).toHaveCount(0);
  await expect.poll(() => backend.state.trips.get("legacy-330100")?.note).toBe("本机改的");
  expect(backend.state.trips.has("t-remote")).toBe(true);
  expect(backend.state.trips.size).toBe(3);
});

test("going back from the conflict question keeps the editor and its draft", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [spring] });
  await backend.install();
  await login(page);
  await signedIn(page);
  await openCity(page);
  await card(page, "2026-10-01").locator("textarea").fill("本机改的");
  backend.otherDeviceTrip({ id: "legacy-330100", code: "330100", date: "2026-10-01", note: "别处改的西湖" });
  backend.otherDeviceTrip({ id: "t-remote", code: "330100", date: "2026-07-01", note: "别处新增" });
  await pull(page);
  await expect(stat(page, "旅行次数")).toHaveText("3");
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  const dialog = page.getByRole("dialog", { name: "编辑期间这条记录有了新的变化" });
  await dialog.getByRole("button", { name: "返回编辑" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(card(page, "2026-10-01").locator("textarea")).toHaveValue("本机改的");
  expect(backend.state.trips.get("legacy-330100")?.note).toBe("别处改的西湖");
});

test("opening photos from the editor keeps the unsaved draft and returns to it", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [spring] });
  await backend.install();
  await emptyMedia(page);
  await login(page);
  await signedIn(page);
  await openCity(page);
  await card(page, "2026-10-01").locator("textarea").fill("还没保存的备注");

  const photos = card(page, "2026-10-01").getByRole("button", { name: /这次旅行的照片与附件/ });
  // The accessible name starts with the visible text.
  await expect(photos).toHaveAccessibleName(/^这次旅行的照片与附件/);
  await photos.click();
  const media = page.getByRole("dialog", { name: "照片与附件" });
  await expect(media).toBeVisible();
  expect(await noOverflow(page)).toBe(true);
  await page.keyboard.press("Escape");
  await expect(media).toHaveCount(0);
  await expect(editor(page)).toBeVisible();
  await expect(card(page, "2026-10-01").locator("textarea")).toHaveValue("还没保存的备注");
  // Focus goes back to the button that opened the photos, not to the page.
  await expect(photos).toBeFocused();
  expect(backend.state.trips.get("legacy-330100")?.note).toBe("西湖");

  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  await expect.poll(() => backend.state.trips.get("legacy-330100")?.note).toBe("还没保存的备注");
});

test("trips on the same day or without a date have distinguishable photo labels", async ({ page }) => {
  const backend = fakeBackend(page, {
    visits: [hangzhou({ date: "2026-03-01", note: "第一天" })],
    trips: [
      { id: "t-a", code: "330100", date: "2026-03-01", note: "同日另一次" },
      { id: "t-b", code: "330100", date: "", note: "" },
      { id: "t-c", code: "330100", date: "", note: "无日期的游记" },
    ],
  });
  await backend.install();
  await emptyMedia(page);
  await login(page);
  await signedIn(page);
  await openCity(page);
  await editor(page).getByRole("button", { name: "照片与附件", exact: true }).click();
  const media = page.getByRole("dialog", { name: "照片与附件" });
  const labels = await media.getByLabel("关联旅行").locator("option").allTextContents();
  const trips = labels.filter((l) => l.includes("第"));
  expect(trips).toHaveLength(4);
  expect(new Set(trips).size).toBe(4);
  expect(trips.some((l) => l.includes("日期未记录"))).toBe(true);
  expect(trips.some((l) => l.includes("同日另一次"))).toBe(true);
});

// ---------- cancelling and undo use the live city (item 14) ----------

test("a trip that arrives while the cancel question is open is removed and restored with the city", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [spring] });
  await backend.install();
  await login(page);
  await signedIn(page);
  await openCity(page);
  await editor(page).getByRole("button", { name: "取消城市打卡" }).click();
  const confirm = page.getByRole("dialog", { name: /取消 杭州市 的打卡/ });
  await expect(confirm).toContainText("2 次旅行记录");

  backend.otherDeviceTrip({ id: "t-late", code: "330100", date: "2026-08-08", note: "刚到的" });
  await pull(page);
  await expect(stat(page, "旅行次数")).toHaveText("3");
  await expect(confirm).toContainText("3 次旅行记录");
  await confirm.getByRole("button", { name: "确认取消打卡" }).click();
  await expect.poll(() => backend.state.visits.size).toBe(0);
  expect(backend.state.trips.size).toBe(0);

  await page.getByRole("button", { name: "撤销" }).click();
  await expect.poll(() => backend.state.trips.size).toBe(3);
  expect(backend.state.trips.get("t-late")?.note).toBe("刚到的");
  expect(backend.state.visits.has("330100")).toBe(true);
});

// ---------- old pages sharing the legacy keys (item 15) ----------

test("data an old page writes under the legacy key is never lost nor overwritten, and can be merged", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "添加足迹" }).first().click();
  const selects = page.getByRole("dialog").locator("select");
  await selects.nth(0).selectOption("330000");
  await selects.nth(1).selectOption("330100");
  await editor(page).locator("textarea").fill("v2 的记录");
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  const mine = await raw(page, KEY);
  expect(mine).toContain("v2 的记录");
  expect(await raw(page, LEGACY)).toBeNull();

  // An old page that was still open writes to its own key, while this page is closed.
  await page.goto("about:blank");
  const old = v1File([v1Visit("330200", "宁波市", "330000", "旧页面的记录")]);
  await page.goto("/");
  await page.evaluate(([k, v]) => localStorage.setItem(k, v), [LEGACY, old]);
  await page.reload();

  await expect(stat(page, "旅行次数")).toHaveText("1");
  const dialog = page.getByRole("dialog", { name: "旧版页面修改了本机足迹" });
  await expect(dialog).toBeVisible();
  expect(await noOverflow(page)).toBe(true);
  expect(await raw(page, KEY)).toBe(mine);
  expect(await raw(page, LEGACY)).toBe(old);

  await dialog.getByRole("button", { name: "合并新增内容" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(stat(page, "旅行次数")).toHaveText("2");
  const merged = await stored(page);
  expect(merged.visits.map((v: { code: string }) => v.code).sort()).toEqual(["330100", "330200"]);
  expect(await raw(page, LEGACY)).toBe(old);
  // Handled once: it is not offered again after a reload.
  await page.reload();
  await expect(page.getByRole("dialog", { name: "旧版页面修改了本机足迹" })).toHaveCount(0);
  await expect(stat(page, "旅行次数")).toHaveText("2");
});

test("a legacy write while the page is open is reported without stopping editing, and keeping it aside is remembered", async ({ page, context }) => {
  await seedLocalRaw(page, v1File([v1Visit("330100", "杭州市", "330000", "旧")]));
  await page.goto("/");
  await expect(stat(page, "旅行次数")).toHaveText("1");
  // The first edit moves the data to the version 2 key; from then on the legacy key is only watched.
  await openCity(page);
  await editor(page).locator("textarea").fill("v2 已保存");
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  expect(await raw(page, KEY)).toContain("v2 已保存");
  const other = await context.newPage();
  await other.goto("/");
  const old = v1File([v1Visit("330200", "宁波市", "330000", "旧页面新增")]);
  await other.evaluate(([k, v]) => localStorage.setItem(k, v), [LEGACY, old]);
  const dialog = page.getByRole("dialog", { name: "旧版页面修改了本机足迹" });
  await expect(dialog).toBeVisible();
  // Editing is not blocked while the notice is shown.
  expect(await raw(page, KEY)).toContain("v2 已保存");
  await dialog.getByRole("button", { name: "不合并，保留在旧位置" }).click();
  await expect(dialog).toHaveCount(0);
  expect(await raw(page, LEGACY)).toBe(old);
  expect(await raw(page, KEY)).toContain("v2 已保存");
  await page.reload();
  await expect(page.getByRole("dialog", { name: "旧版页面修改了本机足迹" })).toHaveCount(0);
  expect(await raw(page, LEGACY)).toBe(old);
});

test("unsynced edits in the account cache survive an old page writing the legacy cache key", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  await login(page);
  await signedIn(page);
  backend.state.failSync = 1;
  await page.getByRole("button", { name: "添加足迹" }).first().click();
  const selects = page.getByRole("dialog").locator("select");
  await selects.nth(0).selectOption("330000");
  await selects.nth(1).selectOption("330100");
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  await expect(page.locator(".local-status")).toContainText("本机已保存，待同步");
  expect(await raw(page, CACHE)).toContain("pendingBatch");

  // The old page's cache: its own, different, view of the account.
  const oldCache = JSON.stringify({
    saved: { version: 1, visits: [], provinces: {} }, revision: 0,
  });
  await page.evaluate(([k, v]) => localStorage.setItem(k, v), [LEGACY_CACHE, oldCache]);
  await page.reload();
  await expect(page.locator(".local-status")).toContainText("已同步到云端");
  expect(backend.state.trips.size).toBe(1);
  expect(backend.state.visits.has("330100")).toBe(true);
  expect(await raw(page, LEGACY_CACHE)).toBe(oldCache);
});

// ---------- overwriting the other tab (item 16) ----------

test("overwriting the other page keeps its raw data; when the browser cannot keep it, it must be exported first", async ({ page, context }) => {
  // Storage refuses any "-conflict-" copy while the flag is set.
  await page.addInitScript(() => {
    const set = Storage.prototype.setItem;
    Storage.prototype.setItem = function (k: string, v: string) {
      if ((window as unknown as { __full?: boolean }).__full && k.includes("-conflict-")) throw new DOMException("full", "QuotaExceededError");
      return set.call(this, k, v);
    };
  });
  await page.goto("/");
  const other = await context.newPage();
  await other.goto("/");
  await other.getByRole("button", { name: "添加足迹" }).first().click();
  const selects = other.getByRole("dialog").locator("select");
  await selects.nth(0).selectOption("330000");
  await selects.nth(1).selectOption("330100");
  await editor(other).locator("textarea").fill("另一个页面的记录");
  await editor(other).getByRole("button", { name: "保存足迹" }).click();
  await expect(stat(other, "旅行次数")).toHaveText("1");

  const dialog = page.getByRole("dialog", { name: "另一个页面修改了本机足迹" });
  await expect(dialog).toBeVisible();
  const otherText = await raw(page, KEY);
  expect(otherText).toContain("另一个页面的记录");
  await page.evaluate(() => ((window as unknown as { __full?: boolean }).__full = true));
  await dialog.getByRole("button", { name: /用本页数据覆盖/ }).click();
  // Nothing was overwritten: the other page's data has no copy yet.
  await expect(dialog.getByRole("alert")).toContainText("导出另一个页面的数据");
  expect(await raw(page, KEY)).toBe(otherText);
  expect(await noOverflow(page)).toBe(true);

  const download = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "导出另一个页面的数据" }).click();
  const file = await download;
  const path = await file.path();
  const exported = (await import("node:fs")).readFileSync(path, "utf8");
  expect(exported).toBe(otherText);

  // The other page writes again: the export of version A is no licence to replace version B.
  await other.getByRole("button", { name: "添加足迹" }).first().click();
  const again = other.getByRole("dialog").locator("select");
  await again.nth(0).selectOption("330000");
  await again.nth(1).selectOption("330200");
  await editor(other).getByRole("button", { name: "保存足迹" }).click();
  await expect(stat(other, "旅行次数")).toHaveText("2");
  await expect.poll(() => raw(page, KEY)).not.toBe(otherText);
  const newer = await raw(page, KEY);
  expect(newer).toContain("330200");
  await dialog.getByRole("button", { name: /用本页数据覆盖/ }).click();
  await expect(dialog.getByRole("alert")).toContainText("导出另一个页面的数据");
  await expect(dialog).toBeVisible();
  expect(await raw(page, KEY)).toBe(newer);

  const second = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "导出另一个页面的数据" }).click();
  expect((await import("node:fs")).readFileSync((await (await second).path())!, "utf8")).toBe(newer);
  await dialog.getByRole("button", { name: /用本页数据覆盖/ }).click();
  await expect(dialog).toHaveCount(0);
  expect(await raw(page, KEY)).not.toBe(newer);
});

// ---------- migration limits and ID collisions (items 1, 11) ----------

test("a migration that needs more than 2000 changes is stopped before anything is sent or snapshotted", async ({ page }) => {
  const trips = Array.from({ length: 2100 }, (_, i) => ({ id: `big-${i}`, date: "2026-01-01", note: "" }));
  await seedLocalRaw(page, JSON.stringify({
    version: 2,
    visits: [{ code: "330100", name: "杭州市", province: "330000", color: green, trips }],
    provinces: {},
  }));
  const backend = fakeBackend(page, { visits: [{ ...hangzhou({ code: "330200", name: "宁波市" }) }] });
  await backend.install();
  await login(page);
  const dialog = page.getByRole("dialog", { name: "把本机足迹迁移到云端" });
  await expect(dialog.getByRole("alert")).toContainText("超过一次原子迁移的上限");
  await expect(dialog.getByRole("button", { name: "确认迁移" })).toBeDisabled();
  expect(await noOverflow(page)).toBe(true);
  expect(backend.state.batches).toEqual([]);
  expect(backend.state.snapshotCalls).toEqual([]);
  expect(backend.state.trips.size).toBe(1);
});

test("a local trip ID already used by another cloud city is uploaded under a new ID, losing nothing", async ({ page }) => {
  await seedLocalRaw(page, JSON.stringify({
    version: 2,
    visits: [{ code: "330200", name: "宁波市", province: "330000", color: gold, trips: [
      { id: "t-spring", date: "2026-04-01", note: "宁波的春天" },
    ] }],
    provinces: {},
  }));
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [spring] });
  await backend.install();
  await login(page);
  const dialog = page.getByRole("dialog", { name: "把本机足迹迁移到云端" });
  await expect(dialog).toContainText("编号已被云端另一座城市的旅行占用");
  await dialog.getByRole("button", { name: "确认迁移" }).click();
  await expect(dialog.getByText(/迁移完成/)).toBeVisible();
  const ningbo = [...backend.state.trips.values()].filter((t) => t.code === "330200");
  expect(ningbo).toHaveLength(1);
  expect(ningbo[0].note).toBe("宁波的春天");
  expect(ningbo[0].id).not.toBe("t-spring");
  expect(backend.state.trips.get("t-spring")?.code).toBe("330100");
  expect(backend.state.batches).toHaveLength(1);
});

test("an atomic migration that failed before its first batch still refuses to be split after a reload", async ({ page }) => {
  // 1 city + 1998 trips = 1999 operations: one batch, but two more and it would not fit.
  const trips = Array.from({ length: 1998 }, (_, i) => ({ id: `big-${i}`, date: "2026-01-01", note: "" }));
  await seedLocalRaw(page, JSON.stringify({
    version: 2,
    visits: [{ code: "330100", name: "杭州市", province: "330000", color: green, trips }],
    provinces: {},
  }));
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  await login(page);
  const dialog = page.getByRole("dialog", { name: "把本机足迹迁移到云端" });
  await expect(dialog).toBeVisible();
  // The network goes away right after the migration is accepted, before a batch exists.
  backend.state.failGet = 1000;
  await dialog.getByRole("button", { name: "确认迁移" }).click();
  await expect.poll(() => raw(page, CACHE)).toContain('"atomic":true');
  expect(backend.state.batches).toEqual([]);
  expect(JSON.parse((await raw(page, CACHE))!).pendingBatch).toBeUndefined();

  await page.reload();
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  const later = page.getByRole("button", { name: "稍后再说" });
  if (await later.isVisible().catch(() => false)) await later.click();
  expect(await raw(page, CACHE)).toContain('"atomic":true');
  // Another city makes the whole change two operations too large for one batch.
  await page.getByRole("button", { name: "添加足迹" }).first().click();
  const selects = page.getByRole("dialog").locator("select");
  await selects.nth(0).selectOption("330000");
  await selects.nth(1).selectOption("330200");
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  backend.state.failGet = 0;
  await pull(page);
  await expect(page.getByText(/需要 2001 项变更，超过一次原子迁移的上限 2000 项/)).toBeVisible();
  // Nothing was written, not even the first 2000 operations ...
  expect(backend.state.batches).toEqual([]);
  expect(backend.state.visits.size).toBe(0);
  expect(backend.state.trips.size).toBe(0);
  // ... and nothing the user accepted or typed was dropped: both cities are still unsent in the cache.
  await expect(page.getByText(/仍完整保留在本机/)).toBeVisible();
  const kept = JSON.parse((await raw(page, CACHE))!);
  expect(kept.atomic).toBe(true);
  expect(kept.saved.visits.map((v: { code: string }) => v.code).sort()).toEqual(["330100", "330200"]);
  expect(kept.saved.visits.find((v: { code: string }) => v.code === "330100").trips).toHaveLength(1998);

  // Reducing it to one batch (cancel the extra city) lets the whole change go out, once.
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await page.locator(".visit-card").filter({ hasText: "宁波" }).first().click();
  await editor(page).getByRole("button", { name: "取消城市打卡" }).click();
  await expect.poll(() => backend.state.trips.size).toBe(1998);
  expect(backend.state.batches).toHaveLength(1);
  expect(backend.state.visits.size).toBe(1);
  await expect.poll(async () => JSON.parse((await raw(page, CACHE))!).atomic).toBeUndefined();
});

test("a record that changes again while the conflict question is open must be chosen again, never silently overwritten", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [spring] });
  await backend.install();
  await login(page);
  await signedIn(page);
  await openCity(page);
  await card(page, "2026-10-01").locator("textarea").fill("本机改的");

  // Remote A: the same trip, plus an unrelated new one.
  backend.otherDeviceTrip({ id: "legacy-330100", code: "330100", date: "2026-10-01", note: "远端 A" });
  backend.otherDeviceTrip({ id: "t-remote", code: "330100", date: "2026-07-01", note: "无关的新增" });
  await pull(page);
  await expect(stat(page, "旅行次数")).toHaveText("3");
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  const dialog = page.getByRole("dialog", { name: "编辑期间这条记录有了新的变化" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("远端 A");
  await dialog.getByRole("button", { name: /用我的/ }).click();
  const confirm = dialog.getByRole("button", { name: "确认并保存" });
  await expect(confirm).toBeEnabled();

  // Remote B edits the same trip again, and another unrelated trip arrives.
  backend.otherDeviceTrip({ id: "legacy-330100", code: "330100", date: "2026-10-01", note: "远端 B" });
  backend.otherDeviceTrip({ id: "t-remote-2", code: "330100", date: "2026-08-01", note: "又一个无关的新增" });
  await pull(page);
  await expect(stat(page, "旅行次数")).toHaveText("4");
  // The question now shows B, the old choice is gone, and confirming is not possible until a new one is made.
  await expect(dialog).toContainText("远端 B");
  await expect(dialog).not.toContainText("远端 A");
  await expect(dialog.getByRole("status")).toContainText("又有了新的变化");
  await expect(confirm).toBeDisabled();
  expect(await noOverflow(page)).toBe(true);
  expect(backend.state.trips.get("legacy-330100")?.note).toBe("远端 B");

  await dialog.getByRole("button", { name: /用我的/ }).click();
  await expect(confirm).toBeEnabled();
  await confirm.click();
  await expect(dialog).toHaveCount(0);
  // My choice was made after seeing B; both unrelated trips are kept.
  await expect.poll(() => backend.state.trips.get("legacy-330100")?.note).toBe("本机改的");
  expect(backend.state.trips.get("t-remote")?.note).toBe("无关的新增");
  expect(backend.state.trips.get("t-remote-2")?.note).toBe("又一个无关的新增");
});

test("choosing the latest keeps a newer remote value that arrived while the question was open", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [spring] });
  await backend.install();
  await login(page);
  await signedIn(page);
  await openCity(page);
  await card(page, "2026-10-01").locator("textarea").fill("本机改的");
  backend.otherDeviceTrip({ id: "legacy-330100", code: "330100", date: "2026-10-01", note: "远端 A" });
  await pull(page);
  await expect.poll(() => page.evaluate(() => document.body.innerText.includes("远端 A"))).toBe(true);
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  const dialog = page.getByRole("dialog", { name: "编辑期间这条记录有了新的变化" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: /保留最新/ }).click();
  backend.otherDeviceTrip({ id: "legacy-330100", code: "330100", date: "2026-10-01", note: "远端 B" });
  await pull(page);
  await expect(dialog).toContainText("远端 B");
  await expect(dialog.getByRole("button", { name: "确认并保存" })).toBeDisabled();
  await dialog.getByRole("button", { name: /保留最新/ }).click();
  await dialog.getByRole("button", { name: "确认并保存" }).click();
  await expect(dialog).toHaveCount(0);
  expect(backend.state.trips.get("legacy-330100")?.note).toBe("远端 B");
});


test("the first accepted migration cache write already contains its atomic intent when later writes fail", async ({ page }) => {
  await seedLocalRaw(page, JSON.stringify({
    version: 2,
    visits: [{ code: "330100", name: "杭州市", province: "330000", color: green,
      trips: [{ id: "accepted-trip", date: "2026-01-01", note: "已接受的记录" }] }],
    provinces: {},
  }));
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  await login(page);
  const dialog = page.getByRole("dialog", { name: "把本机足迹迁移到云端" });
  await expect(dialog).toBeVisible();
  backend.state.failGet = 1000;
  await page.evaluate((key) => {
    const original = Storage.prototype.setItem;
    const attempts: string[] = [];
    (window as unknown as { cacheAttempts: string[] }).cacheAttempts = attempts;
    Storage.prototype.setItem = function (k, value) {
      if (this === localStorage && k === key) {
        attempts.push(value);
        if (attempts.length > 1) throw new DOMException("full", "QuotaExceededError");
      }
      return original.call(this, k, value);
    };
  }, CACHE);
  await dialog.getByRole("button", { name: "确认迁移" }).click();
  await expect.poll(() => page.evaluate(() =>
    (window as unknown as { cacheAttempts: string[] }).cacheAttempts.length)).toBeGreaterThan(1);
  const attempts = await page.evaluate(() =>
    (window as unknown as { cacheAttempts: string[] }).cacheAttempts);
  expect(JSON.parse(attempts[0]).atomic).toBe(true);
  const kept = JSON.parse((await raw(page, CACHE))!);
  expect(kept.atomic).toBe(true);
  expect(kept.saved.visits[0].trips[0].note).toBe("已接受的记录");
  expect(backend.state.batches).toEqual([]);
});
