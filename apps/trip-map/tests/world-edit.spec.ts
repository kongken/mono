import { readFileSync } from "node:fs";
import { join } from "node:path";
import { geoNaturalEarth1 } from "d3-geo";
import { test, expect, type Locator, type Page } from "@playwright/test";
import { fakeBackend, seedLocalRaw, type V } from "./fake-backend";

// The write side of the world view (KON-24) in a real browser, on desktop and at 390px: overseas
// cities picked on the map or typed, their trips, deleting a city, the data-protection dialogs
// (unreadable data, a write that fails, another tab), and the world backup with its import preview.
// Everything runs against the built site, the way it is served.

const WORLD_KEY = "shanhai-world-v1";
const CHINA_KEY = "shanhai-travel-v2";
const gold = "#e2af5c";
const world = (extra: Record<string, unknown> = {}) =>
  JSON.stringify({ kind: "world", version: 1, countries: [], places: [], ...extra });
const paris = { id: "p1", name: "巴黎", country: "FR", lat: 48.8566, lng: 2.3522, trips: [{ id: "t1", date: "2026-05-01", note: "塞纳河" }] };
const tokyo = (trips: object[] = []) => ({ id: "p2", name: "东京", country: "JP", lat: 35.6764, lng: 139.65, trips });

const projection = geoNaturalEarth1().fitExtent([[0, 0], [1000, 500]], { type: "Sphere" });

const installBackend = async (page: Page, visits: V[] = []) => {
  await fakeBackend(page, { visits }).install();
};
const openWorld = async (page: Page) => {
  await page.getByRole("group", { name: "地图视图" }).getByRole("button", { name: "世界视图", exact: true }).click();
  await expect(page.locator(".world-view")).toBeVisible();
  await expect(page.locator(".world-view")).toHaveAttribute("data-catalog", "ready");
  await expect(page.locator('path.world-country[data-code="JP"]')).toBeAttached({ timeout: 20000 });
};
/** Opens the world view; the China footprints are empty unless `china` gives some (they light CN, a country of the statistics). */
const start = async (page: Page, raw?: string, china: V[] = []) => {
  await installBackend(page, china);
  if (china.length) await seedLocalRaw(page, JSON.stringify({ version: 1, visits: china, provinces: {} }));
  if (raw !== undefined)
    await page.addInitScript(
      ([key, text]) => {
        if (localStorage.getItem(key) === null) localStorage.setItem(key, text);
      },
      [WORLD_KEY, raw] as const,
    );
  await page.goto("/");
  await openWorld(page);
};
const stat = (page: Page, label: string) => page.locator(".world-stats > div").filter({ hasText: label }).locator("dd");
const stored = (page: Page, key = WORLD_KEY) => page.evaluate((k) => localStorage.getItem(k), key);
const storedWorld = async (page: Page) => JSON.parse((await stored(page)) ?? "null");
const storedKeys = (page: Page) => page.evaluate(() => Object.keys(localStorage).sort());
const copies = async (page: Page, kind: "conflict" | "unreadable") =>
  (await storedKeys(page)).filter((key) => key.startsWith(`${WORLD_KEY}-${kind}-`));
const country = (page: Page, code: string) => page.locator(`path.world-country[data-code="${code}"]`);
const horizontalOverflow = (page: Page) =>
  page.evaluate(() => {
    const limit = window.innerWidth;
    const wide = [...document.querySelectorAll<HTMLElement>("body *")]
      .filter((el) => {
        const r = el.getBoundingClientRect();
        return r.width > 0 && r.right > limit + 0.5 && getComputedStyle(el).position !== "fixed" && !el.closest("[hidden]");
      })
      .slice(0, 5)
      .map((el) => `${el.tagName}.${el.getAttribute("class")}`);
    return { scroll: document.documentElement.scrollWidth, limit, wide };
  });
const noOverflow = async (page: Page) => {
  const { scroll, limit, wide } = await horizontalOverflow(page);
  expect(scroll, `scrollWidth ${scroll} > ${limit}: ${wide.join(", ")}`).toBeLessThanOrEqual(limit);
};

const placeDialog = (page: Page) => page.getByRole("dialog", { name: /海外城市/ }).filter({ has: page.getByLabel("城市名称") });
const nameField = (dialog: Locator) => dialog.getByLabel("城市名称");
const latField = (dialog: Locator) => dialog.getByLabel(/^纬度/);
const lngField = (dialog: Locator) => dialog.getByLabel(/^经度/);
const countrySelect = (dialog: Locator) => dialog.getByRole("combobox", { name: "所在国家或地区" });
const addCity = async (page: Page) => {
  await page.getByRole("button", { name: "添加海外城市" }).click();
  const dialog = placeDialog(page);
  await expect(dialog).toBeVisible();
  return dialog;
};
const cityRow = (page: Page, name: string) => page.locator(".world-place-list li").filter({ hasText: name });
/** A click on the map where the given place is (the map shows the whole world, unzoomed). */
async function clickMapAt(page: Page, lat: number, lng: number) {
  const svg = page.locator(".world-map-stage svg");
  await svg.scrollIntoViewIfNeeded();
  const box = (await svg.boundingBox())!;
  const [x, y] = projection([lng, lat])!;
  await page.mouse.click(box.x + (x / 1000) * box.width, box.y + (y / 500) * box.height);
}
async function pickOnMap(page: Page, dialog: Locator, lat: number, lng: number) {
  await dialog.getByRole("button", { name: "在地图上选点" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("group", { name: "选择城市位置", exact: true })).toBeVisible();
  await clickMapAt(page, lat, lng);
  await expect(dialog).toBeVisible();
}
const download = async (page: Page, click: () => Promise<void>) => {
  const event = page.waitForEvent("download");
  await click();
  return readFileSync((await (await event).path())!, "utf8");
};

test("a city picked on the map with two trips: Japan, 1 city, 2 trips, kept across a reload; unmarking Japan keeps Tokyo", async ({ page }) => {
  await start(page);
  await page.getByRole("button", { name: "标记 日本" }).click();
  await expect(stat(page, "国家/地区")).toHaveText("1");

  const dialog = await addCity(page);
  await nameField(dialog).fill("东京");
  await expect(dialog.locator(".world-located")).toContainText("点击“在地图上选点”");
  await pickOnMap(page, dialog, 35.68, 139.76);
  // The pick fills the coordinates (4 decimals at most) and, until the user chooses one, the country under it.
  // (A pixel is a degree or two at 390px: the pick is where the pointer was, not where the test aimed.)
  expect(Math.abs(Number(await latField(dialog).inputValue()) - 35.68)).toBeLessThan(3);
  expect(Math.abs(Number(await lngField(dialog).inputValue()) - 139.76)).toBeLessThan(3);
  await expect(dialog.locator(".world-located")).toHaveText("这个位置在日本。");
  await expect(countrySelect(dialog)).toHaveValue("JP");
  await expect(nameField(dialog)).toHaveValue("东京");
  await expect(page.locator(".world-pending")).toBeAttached();

  await dialog.getByRole("button", { name: "添加一次旅行" }).click();
  await dialog.getByLabel(/旅行日期/).last().fill("2026-03-01");
  await dialog.getByLabel(/旅行记忆/).last().fill("樱花");
  await dialog.getByRole("button", { name: "添加一次旅行" }).click();
  await dialog.getByLabel(/旅行日期/).last().fill("2026-10-05");
  await dialog.getByRole("button", { name: "添加城市" }).click();
  await expect(dialog).toBeHidden();

  await expect(stat(page, "国家/地区")).toHaveText("1");
  await expect(stat(page, "海外城市")).toHaveText("1");
  await expect(stat(page, "旅行次数")).toHaveText("2");
  await expect(page.locator(".world-status")).toHaveText("已添加城市东京");
  await expect(cityRow(page, "东京")).toContainText("日本 · 2 次旅行");
  await expect(page.locator(".world-place")).toHaveCount(1);

  const saved = await storedWorld(page);
  expect(saved.places).toHaveLength(1);
  expect(saved.places[0]).toMatchObject({ name: "东京", country: "JP" });
  expect(saved.places[0].trips.map((t: { date: string }) => t.date).sort()).toEqual(["2026-03-01", "2026-10-05"]);
  expect(saved.places[0].lat).toBeGreaterThan(35);
  expect(saved.places[0].lat).toBeLessThan(36.5);
  expect(saved.countries).toEqual([expect.objectContaining({ code: "JP" })]);

  await page.reload();
  await openWorld(page);
  await expect(stat(page, "海外城市")).toHaveText("1");
  await expect(stat(page, "旅行次数")).toHaveText("2");
  await expect(cityRow(page, "东京")).toBeVisible();

  // Unmarking Japan does not remove Tokyo, and Japan stays lit because of it.
  await page.getByRole("button", { name: "取消标记 日本" }).click();
  await expect(stat(page, "海外城市")).toHaveText("1");
  await expect(stat(page, "国家/地区")).toHaveText("1");
  await expect(country(page, "JP")).toHaveAttribute("data-lit", "true");
  expect((await storedWorld(page)).countries).toEqual([]);
  await noOverflow(page);
});

test("the city form and the point on the map are operated from the keyboard, with typed coordinates", async ({ page }) => {
  await start(page);
  await page.getByRole("button", { name: "添加海外城市" }).focus();
  await page.keyboard.press("Enter");
  const dialog = placeDialog(page);
  await expect(dialog).toBeVisible();
  await nameField(dialog).fill("悉尼");
  await latField(dialog).fill("-33.8688");
  await lngField(dialog).fill("151.2093");
  await expect(dialog.locator(".world-located")).toHaveText("这个位置在澳大利亚。");
  await expect(countrySelect(dialog)).toHaveValue("AU");
  await page.keyboard.press("Enter"); // Enter in a field saves, as in any form
  await expect(dialog).toBeHidden();
  await expect(cityRow(page, "悉尼")).toContainText("澳大利亚");
  await expect(page.locator(".world-status")).toHaveText("已添加城市悉尼");

  // Tab from China reaches the city's point; Enter opens its form; Esc closes it and the focus returns.
  await page.locator(".world-china").focus();
  await page.keyboard.press("Tab");
  const point = page.locator('.world-place[data-place]');
  await expect(point).toBeFocused();
  await expect(point).toHaveAttribute("aria-label", "悉尼（澳大利亚），0 次旅行");
  await page.keyboard.press("Enter");
  await expect(placeDialog(page)).toBeVisible();
  await expect(nameField(placeDialog(page))).toHaveValue("悉尼");
  await page.keyboard.press("Escape");
  await expect(placeDialog(page)).toBeHidden();
  await expect(point).toBeFocused();

  // The same city from the list: Edit, change the name, save.
  await page.getByRole("button", { name: "编辑城市 悉尼" }).focus();
  await page.keyboard.press("Space");
  await nameField(placeDialog(page)).fill("悉尼歌剧院");
  await placeDialog(page).getByRole("button", { name: "保存修改" }).click();
  await expect(cityRow(page, "悉尼歌剧院")).toBeVisible();
  expect((await storedWorld(page)).places).toHaveLength(1);
  await noOverflow(page);
});

test("picking a location can be cancelled with Esc and keeps the draft; a pick over a chosen country offers to change it", async ({ page }) => {
  await start(page);
  const dialog = await addCity(page);
  await nameField(dialog).fill("某地");
  await countrySelect(dialog).selectOption("FR");
  await dialog.getByRole("button", { name: "在地图上选点" }).click();
  await expect(page.getByRole("group", { name: "选择城市位置", exact: true })).toContainText("正在选择“某地”的位置");
  await expect(page.locator(".world-map")).toHaveAttribute("data-picking", "true");
  // While picking, clicking a country does not mark it and the city dialog is out of the way.
  await country(page, "JP").dispatchEvent("click");
  await expect(stat(page, "国家/地区")).toHaveText("0");
  await page.keyboard.press("Escape");
  await expect(dialog).toBeVisible();
  await expect(nameField(dialog)).toHaveValue("某地");
  await expect(countrySelect(dialog)).toHaveValue("FR");
  await expect(page.locator(".world-map")).not.toHaveAttribute("data-picking", "true");

  // A pick in Japan: the user already chose France, so the country is kept and the difference is shown.
  await pickOnMap(page, dialog, 35.68, 139.76);
  await expect(countrySelect(dialog)).toHaveValue("FR");
  await expect(dialog.getByRole("note")).toContainText("这个位置在日本，你选择了法国");
  await dialog.getByRole("button", { name: "改为日本" }).click();
  await expect(countrySelect(dialog)).toHaveValue("JP");
  await expect(dialog.getByRole("note")).toHaveCount(0);
  expect(await stored(page)).toBeNull();
  await dialog.getByRole("button", { name: "取消" }).click();
  await expect(dialog).toBeHidden();
  expect(await stored(page), "cancel writes nothing").toBeNull();
  await expect(stat(page, "海外城市")).toHaveText("0");
});

test("a point in China, open sea or a region without a polygon: the reason is shown and the country comes from the list", async ({ page }) => {
  await start(page);
  const dialog = await addCity(page);
  await nameField(dialog).fill("测试");
  await pickOnMap(page, dialog, 30.25, 120.17); // Hangzhou
  await expect(dialog.locator(".world-located")).toContainText("这个位置在中国");
  await expect(countrySelect(dialog)).toHaveValue("");
  await dialog.getByRole("button", { name: "添加城市" }).click();
  await expect(dialog.getByRole("alert")).toHaveText("请选择城市所在的国家或地区");
  await expect(countrySelect(dialog)).toBeFocused();
  // China is not offered as a country for a city at all.
  await expect(countrySelect(dialog).locator('option[value="CN"]')).toHaveCount(0);

  await pickOnMap(page, dialog, 0, -30); // the Atlantic
  await expect(dialog.locator(".world-located")).toContainText("没有对应的国家或地区");
  await expect(countrySelect(dialog)).toHaveValue("");

  // Gibraltar has no polygon of its own: typed coordinates, then the list.
  await latField(dialog).fill("36.1408");
  await lngField(dialog).fill("-5.3536");
  await dialog.getByRole("searchbox", { name: "搜索所在国家或地区" }).fill("直布罗陀");
  await expect(countrySelect(dialog).locator("option", { hasText: "仅可从列表选择" })).toHaveCount(1);
  await countrySelect(dialog).selectOption("GI");
  await dialog.getByRole("button", { name: "添加城市" }).click();
  await expect(dialog).toBeHidden();
  expect((await storedWorld(page)).places[0]).toMatchObject({ name: "测试", country: "GI", lat: 36.1408, lng: -5.3536 });
  await expect(country(page, "ES")).toHaveAttribute("data-lit", "false");
  await expect(page.getByRole("button", { name: /^直布罗陀/ })).toHaveCount(0); // no shape on the map
});

test("a form that cannot be saved says what is wrong and takes the user to the field; nothing is written", async ({ page }) => {
  await start(page);
  const dialog = await addCity(page);
  const save = dialog.getByRole("button", { name: "添加城市" });
  const alert = dialog.getByRole("alert");
  const rejected = async (message: string, focused: Locator) => {
    await save.click();
    await expect(alert).toHaveText(message);
    await expect(focused).toBeFocused();
    await expect(focused).toHaveAttribute("aria-invalid", "true");
    expect(await stored(page), message).toBeNull();
  };
  await rejected("请填写城市名称", nameField(dialog));
  await nameField(dialog).fill("   ");
  await rejected("请填写城市名称", nameField(dialog));
  await nameField(dialog).fill("东京");
  await rejected("请选择城市所在的国家或地区", countrySelect(dialog));
  await countrySelect(dialog).selectOption("JP");
  await rejected("纬度应为十进制数字，例如 35.6764", latField(dialog));
  await latField(dialog).fill("91");
  await rejected("纬度应在 -90 到 90 之间", latField(dialog));
  await latField(dialog).fill("35,6");
  await rejected("纬度应为十进制数字，例如 35.6764", latField(dialog));
  await latField(dialog).fill("35.6764");
  await rejected("经度应为十进制数字，例如 139.65", lngField(dialog));
  await lngField(dialog).fill("-181");
  await rejected("经度应在 -180 到 180 之间", lngField(dialog));
  await nameField(dialog).fill("东".repeat(61));
  await expect(alert).toHaveCount(0); // editing clears the message
  await lngField(dialog).fill("139.65");
  await rejected("城市名称最多 60 个字符", nameField(dialog));
  await nameField(dialog).fill("东京");
  await save.click();
  await expect(dialog).toBeHidden();
  expect((await storedWorld(page)).places).toHaveLength(1);
});

test("trips of a city are added, edited and removed, each change saved with the form", async ({ page }) => {
  await start(page, world({ places: [tokyo([{ id: "ta", date: "2026-01-01", note: "初诣" }, { id: "tb", date: "", note: "" }])] }));
  await expect(stat(page, "旅行次数")).toHaveText("2");
  await page.getByRole("button", { name: "编辑城市 东京" }).click();
  const dialog = placeDialog(page);
  await expect(dialog.locator(".world-trip")).toHaveCount(2);
  await expect(dialog.getByLabel(/旅行日期/).first()).toHaveValue("2026-01-01");
  await dialog.getByLabel(/旅行记忆/).first().fill("初诣和烟花");
  await dialog.getByRole("button", { name: "删除第 2 次旅行" }).click();
  await dialog.getByRole("button", { name: "添加一次旅行" }).click();
  await expect(dialog.getByLabel(/旅行日期/).last()).toBeFocused();
  await dialog.getByLabel(/旅行日期/).last().fill("2026-12-31");
  // Nothing is saved until the form is.
  expect((await storedWorld(page)).places[0].trips).toHaveLength(2);
  await dialog.getByRole("button", { name: "保存修改" }).click();
  await expect(dialog).toBeHidden();
  const trips = (await storedWorld(page)).places[0].trips as { id: string; date: string; note: string }[];
  expect(trips.map((t) => [t.date, t.note]).sort()).toEqual([["2026-01-01", "初诣和烟花"], ["2026-12-31", ""]]);
  expect(trips.find((t) => t.id === "ta")?.note).toBe("初诣和烟花");
  expect(trips.some((t) => t.id === "tb")).toBe(false);
  await expect(stat(page, "旅行次数")).toHaveText("2");
  // Cancelling discards the edits.
  await page.getByRole("button", { name: "编辑城市 东京" }).click();
  await dialog.getByRole("button", { name: "删除第 1 次旅行" }).click();
  await dialog.getByRole("button", { name: "取消" }).click();
  expect((await storedWorld(page)).places[0].trips).toHaveLength(2);
});

test("deleting a city asks a second time, says what goes with it, and leaves country marks alone", async ({ page }) => {
  await start(page, world({ countries: [{ code: "FR", color: gold }], places: [paris, tokyo([{ id: "ta", date: "2026-01-01", note: "" }, { id: "tb", date: "", note: "" }])] }));
  await expect(country(page, "JP")).toHaveAttribute("data-lit", "true");
  await page.getByRole("button", { name: "删除城市 东京" }).click();
  const confirm = page.getByRole("dialog", { name: "确定删除城市“东京”？" });
  await expect(confirm).toContainText("2 次旅行记录会一并删除");
  await expect(confirm).toContainText("日本的国家/地区标记不受影响");
  await expect(confirm).toContainText("日本目前只因这座城市而点亮，删除后会变回未点亮");
  await confirm.getByRole("button", { name: "保留城市" }).click();
  await expect(cityRow(page, "东京")).toBeVisible();
  expect((await storedWorld(page)).places).toHaveLength(2);

  await page.getByRole("button", { name: "删除城市 东京" }).click();
  await confirm.getByRole("button", { name: "确认删除" }).click();
  await expect(cityRow(page, "东京")).toHaveCount(0);
  await expect(page.locator(".world-status")).toHaveText("已删除城市东京及其 2 次旅行");
  await expect(country(page, "JP")).toHaveAttribute("data-lit", "false");
  await expect(stat(page, "旅行次数")).toHaveText("1");
  expect((await storedWorld(page)).countries).toEqual([{ code: "FR", color: gold }]);

  // France is marked and also has a city: deleting the city leaves the mark, and France stays lit.
  await page.getByRole("button", { name: "编辑城市 巴黎" }).click();
  await placeDialog(page).getByRole("button", { name: "删除这座城市" }).click();
  await expect(page.getByRole("dialog", { name: "确定删除城市“巴黎”？" })).not.toContainText("变回未点亮");
  await page.getByRole("button", { name: "确认删除" }).click();
  await expect(stat(page, "海外城市")).toHaveText("0");
  await expect(country(page, "FR")).toHaveAttribute("data-lit", "true");
  expect((await storedWorld(page)).countries).toEqual([{ code: "FR", color: gold }]);
});

// ---------- data protection (unreadable data, a write that fails, another tab) ----------

/** Stored text with one good mark, one good city with one good trip, and three records that are not. */
const damaged = JSON.stringify({
  kind: "world",
  version: 1,
  countries: [{ code: "JP", color: gold }, { code: "ZZ", color: gold }],
  places: [
    {
      id: "p1",
      name: "东京",
      country: "JP",
      lat: 35.6764,
      lng: 139.65,
      trips: [{ id: "t1", date: "2026-03-01", note: "樱花" }, { id: "t2", date: "2026-02-30", note: "" }],
    },
    { id: "p2", name: "", country: "FR", lat: 1, lng: 1, trips: [] },
  ],
});
const unreadableDialog = (page: Page) => page.getByRole("dialog", { name: "世界足迹无法读取" });
const conflictDialog = (page: Page) => page.getByRole("dialog", { name: "其他标签页修改了世界足迹" });
/** Makes the browser refuse to store any key containing `part`; `arm` turns it on (so that seeding is not refused). */
const refuseWrites = (page: Page, part: string) =>
  page.addInitScript((needle) => {
    const original = Storage.prototype.setItem;
    const state = window as unknown as { __refuse?: boolean };
    Storage.prototype.setItem = function (key: string, value: string) {
      if (state.__refuse && key.includes(needle)) throw new DOMException("full", "QuotaExceededError");
      return original.call(this, key, value);
    };
  }, part);
const arm = (page: Page) => page.evaluate(() => ((window as unknown as { __refuse?: boolean }).__refuse = true));

test("unreadable data: nothing is written, the original can be exported, and the recognisable records are recovered after a copy is kept", async ({ page }) => {
  await start(page, damaged);
  const dialog = unreadableDialog(page);
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId("world-recovery-summary")).toHaveText(
    "可以恢复 1 个国家或地区标记、1 座城市、1 次旅行。无法识别而被丢弃的有 1 个标记、1 座城市、1 次旅行。恢复前，原文会先保留为浏览器内的副本。",
  );
  await expect(stat(page, "海外城市")).toHaveText("—");
  expect(await stored(page)).toBe(damaged);
  expect(await copies(page, "unreadable")).toEqual([]);
  await noOverflow(page);

  expect(await download(page, () => dialog.getByRole("button", { name: "导出原文" }).click())).toBe(damaged);
  // Later: the page stays paused, editing is off, and the banner leads back to the choice.
  await dialog.getByRole("button", { name: "稍后处理" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator(".world-protect")).toContainText("已暂停保存");
  await expect(page.getByRole("button", { name: "添加海外城市" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "导入世界备份" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "导出世界备份" })).toBeDisabled();
  expect(await stored(page)).toBe(damaged);
  await page.getByRole("button", { name: "恢复或重新开始" }).click();
  await expect(dialog).toBeVisible();

  await dialog.getByRole("button", { name: "恢复可识别的记录" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator(".world-protect")).toHaveCount(0);
  await expect(stat(page, "国家/地区")).toHaveText("1");
  await expect(stat(page, "海外城市")).toHaveText("1");
  await expect(stat(page, "旅行次数")).toHaveText("1");
  const keys = await copies(page, "unreadable");
  expect(keys).toHaveLength(1);
  expect(await stored(page, keys[0]), "the copy is the original, byte for byte").toBe(damaged);
  const after = await storedWorld(page);
  expect(after.countries).toEqual([{ code: "JP", color: gold }]);
  expect(after.places).toEqual([{ id: "p1", name: "东京", country: "JP", lat: 35.6764, lng: 139.65, trips: [{ id: "t1", date: "2026-03-01", note: "樱花" }] }]);
  await page.reload();
  await openWorld(page);
  await expect(stat(page, "海外城市")).toHaveText("1");
  await expect(page.locator(".world-protect")).toHaveCount(0);
  await expect(unreadableDialog(page)).toHaveCount(0);
});

test("unreadable data that cannot be recovered: starting empty asks twice, keeps a copy, and a newer version is never salvaged", async ({ page }) => {
  const newer = JSON.stringify({ kind: "world", version: 2, countries: [{ code: "JP", color: gold }], places: [] });
  await start(page, newer);
  const dialog = unreadableDialog(page);
  await expect(dialog.getByTestId("world-recovery-summary")).toContainText("没有可以识别并安全恢复的记录");
  await expect(dialog.getByRole("button", { name: "恢复可识别的记录" })).toHaveCount(0);
  await dialog.getByRole("button", { name: "以空数据重新开始" }).click();
  await expect(dialog).toContainText("再点一次“确认以空数据重新开始”才会执行");
  expect(await stored(page), "one click changes nothing").toBe(newer);
  expect(await copies(page, "unreadable")).toEqual([]);
  // Closing and coming back asks again from the start.
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await page.getByRole("button", { name: "恢复或重新开始" }).click();
  await expect(dialog.getByRole("button", { name: "以空数据重新开始" })).toBeVisible();
  await dialog.getByRole("button", { name: "以空数据重新开始" }).click();
  await dialog.getByRole("button", { name: "确认以空数据重新开始" }).click();
  await expect(dialog).toBeHidden();
  await expect(stat(page, "海外城市")).toHaveText("0");
  expect(await storedWorld(page)).toEqual({ kind: "world", version: 1, countries: [], places: [] });
  const keys = await copies(page, "unreadable");
  expect(keys).toHaveLength(1);
  expect(await stored(page, keys[0])).toBe(newer);
  // And the page works again.
  await page.getByRole("button", { name: "标记 日本" }).click();
  expect((await storedWorld(page)).countries).toHaveLength(1);
});

test("when the browser will not keep a copy of unreadable data, nothing is replaced until the original has been exported", async ({ page }) => {
  await refuseWrites(page, "-unreadable-");
  await start(page, damaged);
  await arm(page);
  const dialog = unreadableDialog(page);
  await dialog.getByRole("button", { name: "恢复可识别的记录" }).click();
  await expect(dialog.getByRole("alert")).toContainText("浏览器没能在本机保留原文的副本。请先导出原文");
  expect(await stored(page)).toBe(damaged);
  await expect(stat(page, "海外城市")).toHaveText("—");
  expect(await download(page, () => dialog.getByRole("button", { name: "导出原文" }).click())).toBe(damaged);
  await expect(dialog.getByRole("alert")).toContainText("已导出，请再点一次你的选择");
  await dialog.getByRole("button", { name: "恢复可识别的记录" }).click();
  await expect(dialog).toBeHidden();
  await expect(stat(page, "海外城市")).toHaveText("1");
  expect((await storedWorld(page)).places).toHaveLength(1);
  expect(await copies(page, "unreadable")).toEqual([]);
});

test("a write the browser refuses keeps the city on the page, says so, and the export has it", async ({ page }) => {
  await refuseWrites(page, WORLD_KEY);
  await start(page, world({ countries: [{ code: "FR", color: gold }] }));
  await arm(page);
  await expect(page.locator(".world-protect")).toHaveCount(0);
  const dialog = await addCity(page);
  await nameField(dialog).fill("罗马");
  await latField(dialog).fill("41.9");
  await lngField(dialog).fill("12.5");
  await dialog.getByRole("button", { name: "添加城市" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator(".world-protect")).toContainText("没有保存成功");
  await expect(cityRow(page, "罗马")).toBeVisible();
  await expect(stat(page, "海外城市")).toHaveText("1");
  expect((await storedWorld(page)).places, "what was stored is untouched").toEqual([]);
  const exported = JSON.parse(await download(page, () => page.locator(".world-protect").getByRole("button", { name: "导出当前世界足迹" }).click()));
  expect(exported.places.map((p: { name: string }) => p.name)).toEqual(["罗马"]);
  expect(exported.countries).toEqual([{ code: "FR", color: gold }]);
});

test("another tab's change is not overwritten: the page keeps its edit, both sides can be exported first, and the choice is explicit", async ({ page, context }) => {
  await start(page);
  const theirs = world({ countries: [{ code: "US", color: gold }], places: [paris] });
  const other = await context.newPage();
  await installBackend(other);
  await other.goto("/");
  await other.evaluate(([key, text]) => localStorage.setItem(key, text), [WORLD_KEY, theirs] as const);
  await other.close();

  const form = await addCity(page);
  await nameField(form).fill("罗马");
  await latField(form).fill("41.9");
  await lngField(form).fill("12.5");
  await form.getByRole("button", { name: "添加城市" }).click();
  const dialog = conflictDialog(page);
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId("world-conflict-summary")).toHaveText(
    "本页现有：0 个国家或地区标记、1 座城市、0 次旅行。另一个标签页：1 个国家或地区标记、1 座城市、1 次旅行。",
  );
  expect(await stored(page), "their text is untouched").toBe(theirs);
  expect(await copies(page, "conflict")).toEqual([]);
  await expect(cityRow(page, "罗马")).toBeVisible(); // the edit is still on the page
  await noOverflow(page);

  const mine = JSON.parse(await download(page, () => dialog.getByRole("button", { name: "导出本页数据" }).click()));
  expect(mine.places.map((p: { name: string }) => p.name)).toEqual(["罗马"]);
  expect(await download(page, () => dialog.getByRole("button", { name: "导出另一个标签页的数据" }).click())).toBe(theirs);
  await dialog.getByRole("button", { name: "稍后处理" }).click();
  await expect(page.locator(".world-protect")).toContainText("已暂停保存");
  await expect(page.getByRole("button", { name: "添加海外城市" })).toBeDisabled();
  await country(page, "FR").dispatchEvent("click");
  expect(await stored(page)).toBe(theirs);
  await page.getByRole("button", { name: "选择如何处理" }).click();

  await dialog.getByRole("button", { name: "用本页数据覆盖（对方数据保留为副本）" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator(".world-protect")).toHaveCount(0);
  const saved = await storedWorld(page);
  expect(saved.places.map((p: { name: string }) => p.name)).toEqual(["罗马"]);
  expect(saved.countries).toEqual([]);
  const keys = await copies(page, "conflict");
  expect(keys).toHaveLength(1);
  expect(await stored(page, keys[0]), "their text was kept first").toBe(theirs);
  // Saving works again.
  await page.getByRole("button", { name: "标记 日本" }).click();
  expect((await storedWorld(page)).countries).toHaveLength(1);
});

test("another tab's change can be taken instead: this page's unsaved edit is given up on purpose, and unreadable theirs cannot be taken", async ({ page, context }) => {
  await start(page);
  const theirs = world({ places: [paris] });
  const other = await context.newPage();
  await installBackend(other);
  await other.goto("/");
  await other.evaluate(([key, text]) => localStorage.setItem(key, text), [WORLD_KEY, theirs] as const);
  await page.getByRole("button", { name: "标记 日本" }).click();
  const dialog = conflictDialog(page);
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: /^载入另一个标签页的数据/ }).click();
  await expect(dialog).toBeHidden();
  await expect(cityRow(page, "巴黎")).toBeVisible();
  await expect(page.getByRole("button", { name: "取消标记 日本" })).toHaveCount(0);
  expect(await stored(page)).toBe(theirs);
  expect(await copies(page, "conflict")).toEqual([]);
  await page.getByRole("button", { name: "标记 日本" }).click();
  expect((await storedWorld(page)).places).toHaveLength(1);
  expect((await storedWorld(page)).countries).toHaveLength(1);

  // Now the other tab writes something that cannot be read: it can be exported and kept, never loaded.
  const garbage = "{not json";
  await other.evaluate(([key, text]) => localStorage.setItem(key, text), [WORLD_KEY, garbage] as const);
  await page.getByRole("button", { name: "取消标记 日本" }).click();
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId("world-conflict-summary")).toContainText("内容无法识别，只能导出原文");
  await expect(dialog.getByRole("button", { name: /^载入另一个标签页的数据/ })).toBeDisabled();
  expect(await download(page, () => dialog.getByRole("button", { name: "导出另一个标签页的数据" }).click())).toBe(garbage);
  await other.close();
});

test("when a copy of the other tab's data cannot be kept, overwriting waits for its export", async ({ page, context }) => {
  await refuseWrites(page, "-conflict-");
  await start(page);
  await arm(page);
  const theirs = world({ countries: [{ code: "US", color: gold }] });
  const other = await context.newPage();
  await installBackend(other);
  await other.goto("/");
  await other.evaluate(([key, text]) => localStorage.setItem(key, text), [WORLD_KEY, theirs] as const);
  await other.close();
  await page.getByRole("button", { name: "标记 日本" }).click();
  const dialog = conflictDialog(page);
  await dialog.getByRole("button", { name: "用本页数据覆盖（对方数据保留为副本）" }).click();
  await expect(dialog.getByRole("alert")).toContainText("请先导出另一个标签页的数据");
  expect(await stored(page)).toBe(theirs);
  await download(page, () => dialog.getByRole("button", { name: "导出另一个标签页的数据" }).click());
  await dialog.getByRole("button", { name: "用本页数据覆盖（对方数据保留为副本）" }).click();
  await expect(dialog).toBeHidden();
  expect((await storedWorld(page)).countries.map((m: { code: string }) => m.code)).toEqual(["JP"]);
  expect(await copies(page, "conflict")).toEqual([]);
});

// ---------- world backup: export, import preview, and the two backup types keeping apart ----------

const backupFile = (text: string, name = "world.json") => ({ name, mimeType: "application/json", buffer: Buffer.from(text) });
const importInput = (page: Page) => page.getByTestId("world-import-input");
const importDialog = (page: Page) => page.getByRole("dialog", { name: "导入世界足迹备份" });
const cell = (page: Page, row: string, column: string) => importDialog(page).locator(`tr[data-row="${row}"] td[data-column="${column}"]`);

test("importing shows what will be added, overwritten and discarded, writes only after confirmation, and nothing before", async ({ page }) => {
  const current = world({
    countries: [{ code: "JP", color: gold }],
    places: [paris, tokyo([{ id: "ta", date: "2026-01-01", note: "" }])],
  });
  await start(page, current);
  const incoming = JSON.stringify({
    kind: "world",
    version: 1,
    countries: [{ code: "FR", color: gold }, { code: "JP", color: gold }],
    places: [
      { ...tokyo([{ id: "ta", date: "2026-01-01", note: "" }, { id: "tc", date: "2026-06-01", note: "新" }]), name: "东京都" },
      { id: "p3", name: "悉尼", country: "AU", lat: -33.87, lng: 151.21, trips: [{ id: "td", date: "", note: "" }] },
    ],
  });
  await importInput(page).setInputFiles(backupFile(incoming, "我的世界备份.json"));
  const dialog = importDialog(page);
  await expect(dialog).toContainText("“我的世界备份.json”通过了检查");
  const expected: Record<string, Record<string, string>> = {
    countries: { added: "1", overwritten: "0", unchanged: "1", discarded: "0" },
    places: { added: "1", overwritten: "1", unchanged: "0", discarded: "1" },
    trips: { added: "2", overwritten: "0", unchanged: "1", discarded: "1" },
  };
  for (const [row, columns] of Object.entries(expected))
    for (const [column, value] of Object.entries(columns)) await expect(cell(page, row, column), `${row} ${column}`).toHaveText(value);
  await noOverflow(page);
  expect(await stored(page), "nothing is written before the confirmation").toBe(current);
  await expect(cityRow(page, "巴黎")).toBeVisible();

  expect(JSON.parse(await download(page, () => dialog.getByRole("button", { name: "先导出当前世界足迹" }).click())).places).toHaveLength(2);
  await dialog.getByRole("button", { name: "取消导入" }).click();
  await expect(dialog).toBeHidden();
  expect(await stored(page)).toBe(current);

  await importInput(page).setInputFiles(backupFile(incoming));
  await dialog.getByRole("button", { name: "确认导入" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator(".world-status")).toHaveText("已导入世界足迹备份");
  await expect(cityRow(page, "巴黎")).toHaveCount(0);
  await expect(cityRow(page, "东京都")).toBeVisible();
  await expect(cityRow(page, "悉尼")).toBeVisible();
  await expect(stat(page, "旅行次数")).toHaveText("3");
  expect(await storedWorld(page)).toEqual(JSON.parse(incoming));
});

test("an import file that is not a valid world backup is refused with the reason, and nothing changes", async ({ page }) => {
  const current = world({ countries: [{ code: "JP", color: gold }], places: [paris] });
  await start(page, current);
  const place = (extra: object) => ({ id: "p1", name: "东京", country: "JP", lat: 1, lng: 2, trips: [], ...extra });
  const doc = (extra: object) => JSON.stringify({ kind: "world", version: 1, countries: [], places: [], ...extra });
  const cases: [string, string, string][] = [
    ["not JSON", "{nope", "文件不是有效的 JSON"],
    ["a China backup", JSON.stringify({ version: 2, visits: [], provinces: {} }), "这是中国足迹备份，请在中国视图导入"],
    ["something else", JSON.stringify({ hello: 1 }), "请选择山海足迹导出的世界备份"],
    ["an unknown version", doc({ version: 2 }), "世界数据的版本未知或比当前页面新"],
    ["a duplicate city ID", doc({ places: [place({}), place({})] }), "城市 ID p1 重复"],
    ["a date that does not exist", doc({ places: [place({ trips: [{ id: "t1", date: "2026-02-30", note: "" }] })] }), "日期不是真实存在的日历日期"],
    ["a coordinate out of range", doc({ places: [place({ lat: 95 })] }), "坐标超出范围"],
    ["an unknown country", doc({ places: [place({ country: "ZZ" })] }), "国家/地区码不在目录中"],
    ["China as a city's country", doc({ places: [place({ country: "CN" })] }), "中国由中国足迹派生"],
    ["an empty name", doc({ places: [place({ name: "" })] }), "名称不能为空"],
  ];
  for (const [label, text, reason] of cases) {
    await importInput(page).setInputFiles(backupFile(text));
    const alert = page.locator(".world-import-error");
    await expect(alert, label).toContainText("无法导入：");
    await expect(alert, label).toContainText(reason);
    await expect(importDialog(page), label).toHaveCount(0);
    expect(await stored(page), label).toBe(current);
  }
  const big = Buffer.alloc(4_000_001, " ");
  await importInput(page).setInputFiles({ name: "big.json", mimeType: "application/json", buffer: big });
  await expect(page.locator(".world-import-error")).toContainText("不是世界足迹备份");
  await page.locator(".world-import-error").getByRole("button", { name: "知道了" }).click();
  await expect(page.locator(".world-import-error")).toHaveCount(0);
  expect(await stored(page)).toBe(current);
});

test("the world backup is its own file, and the China view refuses it before any China rule runs", async ({ page }) => {
  await installBackend(page, []);
  await seedLocalRaw(page, JSON.stringify({ version: 1, visits: [], provinces: {} }));
  await page.addInitScript(
    ([key, text]) => {
      if (localStorage.getItem(key) === null) localStorage.setItem(key, text);
    },
    [WORLD_KEY, world({ countries: [{ code: "JP", color: gold }], places: [paris]})] as const,
  );
  await page.goto("/");
  await openWorld(page);
  const text = await download(page, () => page.getByRole("button", { name: "导出世界备份" }).click());
  expect(JSON.parse(text)).toEqual(await storedWorld(page));
  const filename = await (async () => {
    const event = page.waitForEvent("download");
    await page.getByRole("button", { name: "导出世界备份" }).click();
    return (await event).suggestedFilename();
  })();
  expect(filename).toMatch(/^山海足迹-世界-\d{4}-\d{2}-\d{2}\.json$/);

  // The China view: a world backup is named as such, and the China data is as it was.
  const chinaBefore = await stored(page, CHINA_KEY);
  await page.getByRole("group", { name: "地图视图" }).getByRole("button", { name: "中国视图", exact: true }).click();
  await page.locator('input[accept=".json,application/json"]').setInputFiles(backupFile(text, filename));
  await expect(page.getByText("这是世界足迹备份，请在世界视图导入")).toBeVisible();
  await expect(page.getByRole("dialog", { name: /导入/ })).toHaveCount(0);
  expect(await stored(page, CHINA_KEY)).toBe(chinaBefore);
  // Anything that is neither backup keeps the China view's own message.
  await page.locator('input[accept=".json,application/json"]').setInputFiles(backupFile(JSON.stringify({ hello: 1 })));
  await expect(page.getByText("请选择山海足迹导出的 JSON 文件")).toBeVisible();
});

test("the world view never writes the China keys, however the world data is edited, recovered or imported", async ({ page }) => {
  await start(page, world(), []);
  const before = await storedKeys(page);
  const chinaBefore = await stored(page, CHINA_KEY);
  const dialog = await addCity(page);
  await nameField(dialog).fill("罗马");
  await latField(dialog).fill("41.9");
  await lngField(dialog).fill("12.5");
  await dialog.getByRole("button", { name: "添加城市" }).click();
  await importInput(page).setInputFiles(backupFile(world({ places: [paris] })));
  await importDialog(page).getByRole("button", { name: "确认导入" }).click();
  await expect(cityRow(page, "巴黎")).toBeVisible();
  const added = (await storedKeys(page)).filter((key) => !before.includes(key));
  expect(added.filter((key) => !key.startsWith(WORLD_KEY))).toEqual([]);
  expect(await stored(page, CHINA_KEY)).toBe(chinaBefore);
  expect((await storedKeys(page)).filter((key) => key.startsWith("shanhai-travel") || key.startsWith("shanhai-cloud"))).toEqual(
    before.filter((key) => key.startsWith("shanhai-travel") || key.startsWith("shanhai-cloud")),
  );
});

// ---------- layout: the dialogs fit a 390px screen ----------

test("every dialog fits the screen without horizontal overflow, and its buttons can be reached", async ({ page }, info) => {
  await start(page, world({ places: [paris, tokyo([{ id: "ta", date: "2026-01-01", note: "初诣" }])] }));
  const fits = async (dialog: Locator) => {
    const box = (await dialog.boundingBox())!;
    const view = page.viewportSize()!;
    expect(box.x, "left edge").toBeGreaterThanOrEqual(0);
    expect(box.x + box.width, "right edge").toBeLessThanOrEqual(view.width + 0.5);
    expect(box.y + box.height, "bottom edge").toBeLessThanOrEqual(view.height + 0.5);
    await noOverflow(page);
  };
  await page.getByRole("button", { name: "编辑城市 东京" }).click();
  await fits(placeDialog(page));
  await expect(placeDialog(page).getByRole("button", { name: "保存修改" })).toBeInViewport();
  await placeDialog(page).getByRole("button", { name: "删除这座城市" }).click();
  await fits(page.getByRole("dialog", { name: "确定删除城市“东京”？" }));
  await page.getByRole("button", { name: "保留城市" }).click();
  await importInput(page).setInputFiles(backupFile(world({ places: [paris] })));
  await fits(importDialog(page));
  await expect(importDialog(page).getByRole("button", { name: "确认导入" })).toBeInViewport();
  await importDialog(page).getByRole("button", { name: "取消导入" }).click();
  await page.getByRole("button", { name: "导入世界备份" }).focus();
  // Pick mode on a narrow screen: the banner and the map are both on screen.
  await page.getByRole("button", { name: "添加海外城市" }).click();
  await placeDialog(page).getByRole("button", { name: "在地图上选点" }).click();
  await expect(page.locator(".world-pick-banner")).toBeInViewport();
  await noOverflow(page);
  await page.keyboard.press("Escape");
  expect(info.project.name).toMatch(/desktop|mobile/);
});

test("screenshots of the editing flows for the review (WORLD_SHOTS=<dir>)", async ({ page, context }, info) => {
  test.skip(!process.env.WORLD_SHOTS, "only when asked for");
  const shot = async (name: string) => {
    await page.waitForTimeout(500); // after the dialog's own animation
    await page.screenshot({ path: join(process.env.WORLD_SHOTS!, `${name}-${info.project.name}.png`) });
  };
  await start(page, world({ countries: [{ code: "FR", color: gold }], places: [paris] }));
  const dialog = await addCity(page);
  await nameField(dialog).fill("东京");
  await dialog.getByRole("button", { name: "在地图上选点" }).click();
  await page.locator(".world-map-card").scrollIntoViewIfNeeded();
  await shot("1-picking");
  await clickMapAt(page, 36.2, 138.2);
  await expect(dialog).toBeVisible();
  await shot("2-city-form-location");
  await dialog.getByRole("button", { name: "添加一次旅行" }).click();
  await dialog.getByLabel(/旅行日期/).last().fill("2026-03-01");
  await dialog.getByLabel(/旅行记忆/).last().fill("樱花");
  await dialog.getByRole("button", { name: "添加一次旅行" }).click();
  await dialog.getByLabel(/旅行日期/).last().fill("2026-10-05");
  await shot("3-city-form-trips");
  await dialog.getByRole("button", { name: "添加城市" }).click();
  await expect(dialog).toBeHidden();
  await page.waitForTimeout(300);
  await shot("4-after-save");
  await page.getByRole("button", { name: "删除城市 巴黎" }).click();
  await shot("5-delete-confirm");
  await page.getByRole("button", { name: "保留城市" }).click();
  await importInput(page).setInputFiles(
    backupFile(world({ countries: [{ code: "JP", color: gold }], places: [{ ...tokyo(), name: "东京都" }, { id: "p3", name: "悉尼", country: "AU", lat: -33.87, lng: 151.21, trips: [] }] })),
  );
  await expect(importDialog(page)).toBeVisible();
  await shot("6-import-preview");
  await importDialog(page).getByRole("button", { name: "取消导入" }).click();
  await importInput(page).setInputFiles(backupFile(JSON.stringify({ version: 2, visits: [], provinces: {} })));
  await expect(page.locator(".world-import-error")).toBeVisible();
  await shot("7-import-refused");
  // Another tab changes the data: the conflict dialog.
  const other = await context.newPage();
  await installBackend(other);
  await other.goto("/");
  await other.evaluate(([key, text]) => localStorage.setItem(key, text), [WORLD_KEY, world({ countries: [{ code: "US", color: gold }] })] as const);
  await other.close();
  await page.getByRole("button", { name: "标记 日本" }).click();
  await expect(conflictDialog(page)).toBeVisible();
  await shot("8-conflict");

  const damagedPage = await context.newPage();
  await installBackend(damagedPage);
  await damagedPage.addInitScript(([key, text]) => localStorage.setItem(key, text), [WORLD_KEY, damaged] as const);
  await damagedPage.goto("/");
  await openWorld(damagedPage);
  await expect(unreadableDialog(damagedPage)).toBeVisible();
  await damagedPage.waitForTimeout(500);
  await damagedPage.screenshot({ path: join(process.env.WORLD_SHOTS!, `9-unreadable-${info.project.name}.png`) });
});
