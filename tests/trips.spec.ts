import { test, expect, type Page } from "@playwright/test";
import { fakeBackend, gold, green, login, seedLocalRaw, type V } from "./fake-backend";

const KEY = "shanhai-travel-v2";
const LEGACY = "shanhai-travel-v1";
const hangzhou = (extra: Partial<V> = {}): V => ({
  code: "330100", name: "杭州市", province: "330000", color: green, date: "2026-10-01", note: "西湖", ...extra,
});
const v1File = (visits: object[], provinces: object = {}) => JSON.stringify({ version: 1, visits, provinces });
const noOverflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const stored = (page: Page) =>
  page.evaluate((key) => JSON.parse(localStorage.getItem(key) ?? "null"), KEY);
const keys = (page: Page) => page.evaluate(() => Object.keys(localStorage));
const raw = (page: Page, key: string) => page.evaluate((k) => localStorage.getItem(k), key);

const editor = (page: Page) => page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "保存足迹" }) });
const stat = (page: Page, label: string) => page.locator(".stats > div").filter({ hasText: label }).locator("strong");

async function addCity(page: Page, province = "330000", city = "330100") {
  await page.getByRole("button", { name: "添加足迹" }).first().click();
  const selects = page.getByRole("dialog").locator("select");
  await selects.nth(0).selectOption(province);
  await selects.nth(1).selectOption(city);
}
async function openCity(page: Page) {
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await page.locator(".visit-card").first().click();
  await expect(editor(page)).toBeVisible();
}

// ---------- local mode ----------

test("two trips in one city keep the city count and color; each can be edited and deleted", async ({ page }) => {
  await page.goto("/");
  await addCity(page);
  const dialog = editor(page);
  await dialog.locator('input[type="date"]').fill("2026-01-10");
  await dialog.locator("textarea").fill("春天");
  await dialog.getByRole("button", { name: "保存足迹" }).click();
  await expect(stat(page, "已打卡城市")).toHaveText(/^1\s*\/\s*\d+$/);
  await expect(stat(page, "旅行次数")).toHaveText("1");
  const first = await stored(page);
  expect(first.version).toBe(2);
  const color = first.visits[0].color;

  // A second trip: the city count and its color do not change.
  await openCity(page);
  await dialog.getByRole("button", { name: "添加另一次旅行" }).click();
  await expect(dialog.locator('input[type="date"]')).toHaveCount(2);
  await dialog.locator('input[type="date"]').nth(1).fill("2026-03-05");
  await dialog.locator("textarea").nth(1).fill("樱花");
  expect(await noOverflow(page)).toBe(true);
  await dialog.getByRole("button", { name: "保存足迹" }).click();
  await expect(stat(page, "已打卡城市")).toHaveText(/^1\s*\/\s*\d+$/);
  await expect(stat(page, "旅行次数")).toHaveText("2");
  const two = await stored(page);
  expect(two.visits).toHaveLength(1);
  expect(two.visits[0].color).toBe(color);
  expect(two.visits[0].trips.map((t: { note: string }) => t.note).sort()).toEqual(["春天", "樱花"]);
  const ids = two.visits[0].trips.map((t: { id: string }) => t.id);
  expect(new Set(ids).size).toBe(2);
  expect(ids).toContain(first.visits[0].trips[0].id);

  // The list shows the newest trip and how many there are; the map color is unchanged.
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  const card = page.locator(".visit-card").first();
  await expect(card).toContainText("共 2 次旅行");
  await expect(card).toContainText("2026-03-05");
  await expect(card).toContainText("樱花");
  expect(await noOverflow(page)).toBe(true);

  // Delete one trip: the other and the city stay.
  await openCity(page);
  await dialog.getByRole("button", { name: "删除第 2 次旅行" }).click();
  await dialog.getByRole("button", { name: "保存足迹" }).click();
  await expect(stat(page, "旅行次数")).toHaveText("1");
  expect((await stored(page)).visits[0].trips).toHaveLength(1);

  // Delete the last trip: the city check-in stays.
  await openCity(page);
  await dialog.getByRole("button", { name: "删除第 1 次旅行" }).click();
  await dialog.getByRole("button", { name: "保存足迹" }).click();
  await expect(stat(page, "旅行次数")).toHaveText("0");
  await expect(stat(page, "已打卡城市")).toHaveText(/^1\s*\/\s*\d+$/);
  const none = await stored(page);
  expect(none.visits).toHaveLength(1);
  expect(none.visits[0].trips).toEqual([]);
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await expect(page.locator(".visit-card").first()).toContainText("暂无旅行记录");
});

test("cancelling a city with several trips asks first, and undo restores every trip with its ID", async ({ page }) => {
  const seed = {
    version: 2,
    visits: [{ code: "330100", name: "杭州市", province: "330000", color: gold, trips: [
      { id: "trip-a", date: "2026-01-01", note: "一" },
      { id: "trip-b", date: "2026-02-01", note: "二" },
    ] }],
    provinces: {},
  };
  await seedLocalRaw(page, JSON.stringify(seed));
  await page.goto("/");
  await openCity(page);
  await editor(page).getByRole("button", { name: "取消城市打卡" }).click();
  const confirm = page.getByRole("dialog", { name: /取消 杭州市 的打卡/ });
  await expect(confirm).toContainText("2 次旅行记录");
  expect(await noOverflow(page)).toBe(true);
  await confirm.getByRole("button", { name: "保留足迹" }).click();
  // Keeping the city leaves the editor open and the data untouched.
  await expect(editor(page)).toBeVisible();
  expect(await stored(page)).toBeNull();
  await editor(page).getByRole("button", { name: "取消城市打卡" }).click();
  await page.getByRole("button", { name: "确认取消打卡" }).click();
  await expect(stat(page, "旅行次数")).toHaveText("0");
  expect((await stored(page)).visits).toEqual([]);
  await page.getByRole("button", { name: "撤销" }).click();
  await expect(stat(page, "旅行次数")).toHaveText("2");
  expect(await stored(page)).toEqual(seed);
});

test("version 1 data is shown, left untouched on load and converted into the version 2 key on the first edit", async ({ page }) => {
  const original = v1File([hangzhou({ note: "旧备注" })], { "110000": gold });
  await seedLocalRaw(page, original);
  await page.goto("/");
  await expect(stat(page, "旅行次数")).toHaveText("1");
  await expect(stat(page, "已打卡城市")).toHaveText(/^1\s*\/\s*\d+$/);
  // Merely opening the page writes nothing: the old key is only read.
  expect(await raw(page, LEGACY)).toBe(original);
  expect(await raw(page, KEY)).toBeNull();

  await openCity(page);
  await editor(page).locator("textarea").fill("新备注");
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  const now = await stored(page);
  expect(now.version).toBe(2);
  expect(now.provinces).toEqual({ "110000": gold });
  expect(now.visits[0].trips).toEqual([{ id: "legacy-330100", date: "2026-10-01", note: "新备注" }]);
  // The version 2 data lives under its own key; the original text is never rewritten or copied away.
  expect(await raw(page, LEGACY)).toBe(original);
  expect((await keys(page)).filter((k) => k.includes("before-v2"))).toEqual([]);
});

test("a version 1 backup imports, exports as version 2 and imports again without loss", async ({ page }) => {
  await page.goto("/");
  const backup = v1File([
    hangzhou({ note: "西湖边" }),
    { code: "330200", name: "宁波市", province: "330000", color: gold, date: "", note: "" },
  ], { "310000": green });
  await page.locator('input[type="file"]').setInputFiles({ name: "old.json", mimeType: "application/json", buffer: Buffer.from(backup) });
  const dialog = page.getByRole("dialog", { name: "导入旅行足迹" });
  await expect(dialog).toContainText("2 条城市足迹");
  await expect(dialog).toContainText("2 次旅行");
  await dialog.getByRole("button", { name: "确认替换并导入" }).click();
  await expect(stat(page, "旅行次数")).toHaveText("2");
  const imported = await stored(page);
  expect(imported.version).toBe(2);
  expect(imported.visits.map((v: { trips: unknown[] }) => v.trips)).toEqual([
    [{ id: "legacy-330100", date: "2026-10-01", note: "西湖边" }],
    [{ id: "legacy-330200", date: "", note: "" }],
  ]);

  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "导出备份" }).click();
  const file = await download;
  const exported = JSON.parse(Buffer.concat(await (async () => {
    const chunks: Buffer[] = [];
    for await (const chunk of (await file.createReadStream())!) chunks.push(chunk as Buffer);
    return chunks;
  })()).toString());
  expect(exported).toEqual(imported);

  await page.locator('input[type="file"]').setInputFiles({ name: "new.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify(exported)) });
  await dialog.getByRole("button", { name: "确认替换并导入" }).click();
  expect(await stored(page)).toEqual(imported);
});

test("a backup with impossible dates is imported only after the user agrees, keeping every note", async ({ page }) => {
  await page.goto("/");
  const backup = v1File([hangzhou({ date: "2026-02-30", note: "必须保留的备注" })]);
  await page.locator('input[type="file"]').setInputFiles({ name: "bad.json", mimeType: "application/json", buffer: Buffer.from(backup) });
  const dialog = page.getByRole("dialog", { name: "导入旅行足迹" });
  await expect(dialog).toContainText("不是真实存在的日历日期");
  // Nothing has been changed yet.
  expect(await stored(page)).toBeNull();
  await dialog.getByRole("button", { name: "清除不存在的日期并导入" }).click();
  expect((await stored(page)).visits[0].trips).toEqual([{ id: "legacy-330100", date: "", note: "必须保留的备注" }]);
});

test("unreadable version 1 data with an impossible date is kept until the user picks a recovery", async ({ page }) => {
  const original = v1File([hangzhou({ date: "2026-02-30", note: "不能丢" }), { ...hangzhou({ code: "330200", name: "宁波市" }) }]);
  await seedLocalRaw(page, original);
  await page.goto("/");
  const dialog = page.getByRole("dialog", { name: "本地足迹数据无法读取" });
  await expect(dialog).toBeVisible();
  expect(await raw(page, LEGACY)).toBe(original);
  expect(await noOverflow(page)).toBe(true);
  await dialog.getByRole("button", { name: /清除 1 个不存在的日期并恢复/ }).click();
  await expect(dialog).toHaveCount(0);
  const fixed = await stored(page);
  expect(fixed.visits.map((v: { trips: unknown[] }) => v.trips)).toEqual([
    [{ id: "legacy-330100", date: "", note: "不能丢" }],
    [{ id: "legacy-330200", date: "2026-10-01", note: "西湖" }],
  ]);
  const copies = (await keys(page)).filter((k) => k.includes("-unreadable-"));
  expect(copies).toHaveLength(1);
  expect(await raw(page, copies[0])).toBe(original);
});

test("a second tab changing the data is never overwritten silently", async ({ page, context }) => {
  await page.goto("/");
  const other = await context.newPage();
  await other.goto("/");
  await addCity(other);
  await editor(other).getByRole("button", { name: "保存足迹" }).click();
  await expect(stat(other, "旅行次数")).toHaveText("1");

  const dialog = page.getByRole("dialog", { name: "另一个页面修改了本机足迹" });
  await expect(dialog).toBeVisible();
  expect(await noOverflow(page)).toBe(true);
  // Overwriting keeps the other tab's data as a copy.
  const otherText = await raw(page, KEY);
  await dialog.getByRole("button", { name: /用本页数据覆盖/ }).click();
  await expect(dialog).toHaveCount(0);
  const copies = (await keys(page)).filter((k) => k.startsWith(`${KEY}-conflict-`));
  expect(copies).toHaveLength(1);
  expect(await raw(page, copies[0])).toBe(otherText);

  // Loading the other tab's data instead adopts it.
  await other.reload();
  await addCity(other, "330000", "330200");
  await editor(other).getByRole("button", { name: "保存足迹" }).click();
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "载入另一个页面的数据" }).click();
  await expect(stat(page, "旅行次数")).toHaveText("1");
  expect((await stored(page)).visits.length).toBeGreaterThan(0);
});

// ---------- signed in ----------

const withTrip = { id: "t-spring", code: "330100", date: "2026-03-01", note: "春" };

test("a city with two trips is saved as one city and two trips, and every request declares schema 2", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await addCity(page);
  const dialog = editor(page);
  await dialog.getByRole("button", { name: "添加另一次旅行" }).click();
  await dialog.locator('input[type="date"]').nth(0).fill("2026-01-01");
  await dialog.locator('input[type="date"]').nth(1).fill("2026-02-01");
  await dialog.getByRole("button", { name: "保存足迹" }).click();
  await expect.poll(() => backend.state.trips.size).toBe(2);
  expect(backend.state.visits.size).toBe(1);
  expect(backend.state.batches).toEqual([["upsertCity", "upsertTrip", "upsertTrip"]]);
  expect([...backend.state.schemaHeaders]).toEqual(["2"]);
  await expect(stat(page, "旅行次数")).toHaveText("2");
  await expect(stat(page, "已打卡城市")).toHaveText(/^1\s*\/\s*\d+$/);

  // Deleting one trip sends just that trip; deleting the city sends one city deletion.
  await openCity(page);
  await dialog.getByRole("button", { name: "删除第 1 次旅行" }).click();
  await dialog.getByRole("button", { name: "保存足迹" }).click();
  await expect.poll(() => backend.state.trips.size).toBe(1);
  expect(backend.state.batches.at(-1)).toEqual(["deleteTrip"]);
  expect(backend.state.visits.size).toBe(1);
  await openCity(page);
  await dialog.getByRole("button", { name: "取消城市打卡" }).click();
  await expect.poll(() => backend.state.visits.size).toBe(0);
  expect(backend.state.batches.at(-1)).toEqual(["deleteVisit"]);
  expect(backend.state.trips.size).toBe(0);
});

test("offline edits survive a refresh and are replayed exactly once, even when the reply was lost", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  // The server commits the batch but its reply is lost.
  backend.state.dropNextSyncReply = 1;
  await addCity(page);
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  await expect.poll(() => backend.state.trips.size).toBe(1);
  await expect(page.locator(".local-status")).toContainText("本机已保存，待同步");
  await page.reload();
  await expect(page.locator(".local-status")).toContainText("已同步到云端");
  expect(backend.state.batches).toHaveLength(1);
  expect(backend.state.trips.size).toBe(1);
  expect(backend.state.visits.size).toBe(1);

  // A plain offline failure: the edit stays on the device and goes out after the refresh.
  backend.state.failSync = 1;
  await openCity(page);
  await editor(page).getByRole("button", { name: "添加另一次旅行" }).click();
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  await expect(page.locator(".local-status")).toContainText("本机已保存，待同步");
  expect(backend.state.trips.size).toBe(1);
  await page.reload();
  await expect(page.locator(".local-status")).toContainText("已同步到云端");
  expect(backend.state.trips.size).toBe(2);
  expect(backend.state.batches).toHaveLength(2);
});

test("adding a trip while another device cancelled the city asks which side to keep", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [withTrip] });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await expect(stat(page, "旅行次数")).toHaveText("2");
  backend.otherDeviceDeleteVisit("330100");
  await openCity(page);
  await editor(page).getByRole("button", { name: "添加另一次旅行" }).click();
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  const dialog = page.getByRole("dialog", { name: "多台设备修改了相同足迹" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("杭州市");
  expect(await noOverflow(page)).toBe(true);
  expect(backend.state.visits.size).toBe(0);
  await dialog.getByRole("button", { name: "冲突以本机为准" }).click();
  await expect(dialog).toHaveCount(0);
  await expect.poll(() => backend.state.trips.size).toBe(3);
  expect(backend.state.visits.has("330100")).toBe(true);
});

test("two devices editing different trips of one city merge without a conflict", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [withTrip] });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  backend.otherDeviceTrip({ ...withTrip, note: "另一台设备改的" });
  await openCity(page);
  await editor(page).locator("textarea").filter({ hasText: "西湖" }).fill("本机改的");
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  await expect.poll(() => backend.state.trips.get("legacy-330100")?.note).toBe("本机改的");
  expect(backend.state.trips.get("t-spring")?.note).toBe("另一台设备改的");
  await expect(page.getByRole("dialog", { name: "多台设备修改了相同足迹" })).toHaveCount(0);
});

test("a backend that predates trips is refused with a clear message and nothing is sent", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [] }, { footprintSchema: 0 });
  await backend.install();
  await login(page);
  await expect(page.getByRole("alert")).toContainText("尚未升级");
  expect(backend.state.batches).toEqual([]);
  expect(backend.state.syncCalls).toEqual([]);
});

test("migration shows trips and uploads them as trips", async ({ page }) => {
  await seedLocalRaw(page, JSON.stringify({
    version: 2,
    visits: [{ code: "330100", name: "杭州市", province: "330000", color: green, trips: [
      { id: "m1", date: "2026-01-01", note: "一" },
      { id: "m2", date: "2026-02-01", note: "二" },
    ] }],
    provinces: {},
  }));
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  await login(page);
  const dialog = page.getByRole("dialog", { name: "把本机足迹迁移到云端" });
  await expect(dialog).toContainText("2 次旅行");
  await expect(dialog.getByLabel("迁移预览")).toContainText("2新增旅行记录");
  expect(await noOverflow(page)).toBe(true);
  await dialog.getByRole("button", { name: "确认迁移" }).click();
  await expect(dialog.getByText(/迁移完成/)).toBeVisible();
  expect([...backend.state.trips.keys()].sort()).toEqual(["m1", "m2"]);
});

test("restoring a snapshot brings back a deleted trip and previews the trip changes", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [withTrip] });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  backend.seedSnapshot("manual");
  backend.otherDeviceDeleteTrip("t-spring");
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await expect(stat(page, "旅行次数")).toHaveText("1");
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "历史版本" }).click();
  const dialog = page.getByRole("dialog", { name: "云端备份与历史版本" });
  const row = dialog.getByRole("listitem").filter({ hasText: "手动保存" });
  await expect(row).toContainText("2 次旅行");
  await row.getByRole("button", { name: "预览并恢复" }).click();
  await expect(dialog.getByRole("status").filter({ hasText: "旅行记录：找回 1 次" })).toBeVisible();
  expect(await noOverflow(page)).toBe(true);
  await dialog.getByRole("button", { name: "确认恢复" }).click();
  await expect(dialog.getByText(/已恢复，共调整 1 条记录/)).toBeVisible();
  expect(backend.state.trips.has("t-spring")).toBe(true);
  expect(backend.state.trips.size).toBe(2);
});

test("photos link to one trip, survive deleting that trip and cancelling the city", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [hangzhou()], trips: [withTrip] });
  await backend.install();
  const files: Record<string, unknown>[] = [];
  const prepared: { regionCode: string; tripId: string }[] = [];
  let deletes = 0;
  await page.route("**/api/tripmap.v1.MediaService/**", async (route) => {
    const method = route.request().url().split("/").pop();
    const body = route.request().postDataJSON() ?? {};
    let result: unknown = {};
    if (method === "ListMedia")
      result = {
        media: files.filter(
          (m) => (!body.regionCode || m.regionCode === body.regionCode) && (!body.tripId || m.tripId === body.tripId),
        ),
      };
    if (method === "PrepareUpload") {
      prepared.push({ regionCode: body.regionCode, tripId: body.tripId ?? "" });
      const media = {
        id: `m${files.length + 1}`, regionCode: body.regionCode, tripId: body.tripId ?? "", filename: body.filename,
        contentType: "application/pdf", sizeBytes: body.sizeBytes, status: "ready", width: 0, height: 0,
      };
      files.push(media);
      result = { media };
    }
    if (method === "DeleteMedia") deletes++;
    await route.fulfill({ status: 200, json: result });
  });
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await openCity(page);
  // A trip that was only just added is not synced yet, so it cannot own media.
  await editor(page).getByRole("button", { name: "添加另一次旅行" }).click();
  await expect(editor(page).getByRole("button", { name: /这次旅行的照片与附件（第 3 次）/ })).toBeDisabled();
  await editor(page).getByRole("button", { name: "删除第 3 次旅行" }).click();

  // The spring trip is the second card (newest first: 2026-03-01 before 2026-10-01 is not true; check by label).
  const card = editor(page).locator(".trip-card").filter({ has: page.locator('input[value="2026-03-01"]') });
  await card.getByRole("button", { name: /这次旅行的照片与附件/ }).click();
  const media = page.getByRole("dialog", { name: "照片与附件" });
  await expect(media.getByLabel("关联旅行")).toHaveValue("t-spring");
  expect(await noOverflow(page)).toBe(true);
  await media.getByLabel("上传照片或附件").setInputFiles({ name: "票据.pdf", mimeType: "application/pdf", buffer: Buffer.from("%PDF-1.4") });
  await expect(media.getByText("票据.pdf").first()).toBeVisible();
  expect(prepared).toEqual([{ regionCode: "330100", tripId: "t-spring" }]);
  await media.getByRole("button", { name: "关闭", exact: true }).first().click();

  // Closing the media dialog returns to the editor that opened it.
  await expect(editor(page)).toBeVisible();
  // Delete that trip. Its file stays and shows up in the city's attachments.
  await editor(page).locator(".trip-card").filter({ has: page.locator('input[value="2026-03-01"]') }).getByRole("button", { name: /删除第/ }).click();
  await editor(page).getByRole("button", { name: "保存足迹" }).click();
  await expect.poll(() => backend.state.trips.has("t-spring")).toBe(false);
  await openCity(page);
  await editor(page).getByRole("button", { name: "照片与附件", exact: true }).click();
  const all = page.getByRole("dialog", { name: "照片与附件" });
  await all.getByLabel("关联旅行").selectOption("");
  await expect(all.getByText("票据.pdf")).toBeVisible();
  await expect(all.getByText("关联的旅行已删除")).toBeVisible();
  await all.getByRole("button", { name: "关闭", exact: true }).first().click();

  // Cancelling the whole city keeps the file too: nothing was deleted.
  await expect(editor(page)).toBeVisible();
  await editor(page).getByRole("button", { name: "取消城市打卡" }).click();
  await expect.poll(() => backend.state.visits.size).toBe(0);
  expect(deletes).toBe(0);
  expect(files).toHaveLength(1);
});
