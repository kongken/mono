import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import catalog from "@kongken/geo-data/world-catalog.json" with { type: "json" };
import { fakeBackend, green, login, seedLocalRaw, type V } from "./fake-backend";

// The world view (KON-23) in a real browser: marking and persistence, the China data staying as it
// was, same-origin requests only, offline reload, a catalog that fails to load, keyboard operation and
// layout. Everything runs against the built site, the way it is served.

const WORLD_KEY = "shanhai-world-v1";
const SHELL_CACHE = /^shanhai-world-shell-[0-9a-f]{16}$/;
const DATA_CACHE = `shanhai-world-data-${catalog.version}`;
const hangzhou: V = { code: "330100", name: "杭州市", province: "330000", color: green, date: "2026-10-01", note: "西湖" };
const gold = "#e2af5c";
const world = (extra: Record<string, unknown> = {}) =>
  JSON.stringify({ kind: "world", version: 1, countries: [], places: [], ...extra });
const paris = { id: "p1", name: "巴黎", country: "FR", lat: 48.8566, lng: 2.3522, trips: [{ id: "t1", date: "2026-05-01", note: "塞纳河" }] };

const installBackend = async (page: Page, visits: V[]) => {
  await fakeBackend(page, { visits }).install();
};
const viewSwitch = (page: Page) => page.getByRole("group", { name: "地图视图" });
const openWorld = async (page: Page) => {
  await viewSwitch(page).getByRole("button", { name: "世界视图", exact: true }).click();
  await expect(page.locator(".world-view")).toBeVisible();
};
const country = (page: Page, code: string) => page.locator(`path.world-country[data-code="${code}"]`);
const stat = (page: Page, label: string) => page.locator(".world-stats > div").filter({ hasText: label }).locator("dd");
const stored = (page: Page, key = WORLD_KEY) => page.evaluate((k) => localStorage.getItem(k), key);
const storedKeys = (page: Page) => page.evaluate(() => Object.keys(localStorage).sort());
const seedWorld = (page: Page, raw: string) =>
  page.addInitScript(
    ([key, text]) => {
      if (localStorage.getItem(key) === null) localStorage.setItem(key, text);
    },
    [WORLD_KEY, raw] as const,
  );
const mapReady = async (page: Page) => {
  await expect(page.locator(".world-view")).toHaveAttribute("data-catalog", "ready");
  await expect(country(page, "JP")).toBeAttached({ timeout: 20000 });
};
const offlineReady = (page: Page) => expect(page.locator(".world-view")).toHaveAttribute("data-offline", "ready", { timeout: 20000 });
/** Every request the browser makes (pages and service workers) as absolute URLs. */
const recordRequests = (context: BrowserContext) => {
  const urls: string[] = [];
  context.on("request", (request) => urls.push(request.url()));
  return urls;
};
const sameOrigin = (urls: string[], page: Page) => {
  const origin = new URL(page.url()).origin;
  return urls.filter((u) => !u.startsWith("data:") && !u.startsWith("blob:") && new URL(u).origin !== origin);
};
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

test("the world view is opt-in: the page opens on China, and switching reads but never writes", async ({ page }) => {
  await installBackend(page, []);
  await page.goto("/");
  await expect(viewSwitch(page).getByRole("button", { name: "中国视图", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".intro")).toBeVisible();
  await expect(page.locator(".world-view")).toHaveCount(0);
  const before = await storedKeys(page);
  await openWorld(page);
  await mapReady(page);
  await expect(page.locator(".intro")).toBeHidden();
  await expect(viewSwitch(page).getByRole("button", { name: "世界视图", exact: true })).toHaveAttribute("aria-pressed", "true");
  expect(await stored(page), "opening writes nothing").toBeNull();
  expect(await storedKeys(page)).toEqual(before);
  await viewSwitch(page).getByRole("button", { name: "中国视图", exact: true }).click();
  await expect(page.locator(".intro")).toBeVisible();
  // No memory of the choice: a reload is China again.
  await openWorld(page);
  await page.reload();
  await expect(page.locator(".intro")).toBeVisible();
  await expect(page.locator(".world-view")).toHaveCount(0);
});

test("a country is marked and unmarked from the map and kept across a reload; a city's country stays lit", async ({ page }) => {
  await installBackend(page, []);
  await seedWorld(page, world({ countries: [{ code: "FR", color: gold }], places: [paris] }));
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await expect(stat(page, "国家/地区")).toHaveText("1");
  await expect(stat(page, "海外城市")).toHaveText("1");
  await expect(stat(page, "旅行次数")).toHaveText("1");

  const japan = page.getByRole("button", { name: "日本", exact: true });
  await expect(japan).toHaveAttribute("aria-pressed", "false");
  await country(page, "JP").dispatchEvent("click");
  await expect(japan).toHaveAttribute("aria-pressed", "true");
  await expect(stat(page, "国家/地区")).toHaveText("2");
  await expect(page.locator(".world-status")).toHaveText("已标记日本");
  const saved = JSON.parse((await stored(page))!);
  expect(saved.countries.map((m: { code: string }) => m.code).sort()).toEqual(["FR", "JP"]);
  expect(saved.places).toHaveLength(1);

  await page.reload();
  await openWorld(page);
  await mapReady(page);
  await expect(page.getByRole("button", { name: "日本", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(stat(page, "国家/地区")).toHaveText("2");

  // Unmarking an independently marked country that a city also lights keeps it lit.
  await country(page, "FR").dispatchEvent("click");
  const france = page.getByRole("button", { name: /^法国/ });
  await expect(france).toHaveAttribute("aria-pressed", "false");
  await expect(france).toHaveAttribute("aria-label", "法国（因海外城市点亮）");
  await expect(country(page, "FR")).toHaveAttribute("data-lit", "true");
  await expect(stat(page, "国家/地区")).toHaveText("2");
  expect(JSON.parse((await stored(page))!).countries.map((m: { code: string }) => m.code)).toEqual(["JP"]);
  // And unmarking Japan, which nothing else lights, puts it out.
  await country(page, "JP").dispatchEvent("click");
  await expect(country(page, "JP")).toHaveAttribute("data-lit", "false");
  await expect(stat(page, "国家/地区")).toHaveText("1");
});

test("a real pointer click marks a country, and dragging the map pans it without marking anything", async ({ page }) => {
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  const brazil = country(page, "BR");
  await brazil.click();
  await expect(page.getByRole("button", { name: "巴西", exact: true })).toHaveAttribute("aria-pressed", "true");
  await brazil.click();
  await expect(page.getByRole("button", { name: "巴西", exact: true })).toHaveAttribute("aria-pressed", "false");

  await page.getByRole("button", { name: "放大地图" }).click();
  await page.getByRole("button", { name: "放大地图" }).click();
  const group = page.locator(".world-map-stage svg > g");
  const before = await group.getAttribute("transform");
  const savedBefore = await stored(page);
  const box = (await brazil.boundingBox())!;
  const x = box.x + box.width / 2;
  const y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + 60, y + 20, { steps: 6 });
  await page.mouse.up();
  expect(await group.getAttribute("transform"), "the map moved").not.toBe(before);
  await expect(page.getByRole("button", { name: "巴西", exact: true })).toHaveAttribute("aria-pressed", "false");
  expect(await stored(page), "a drag marks and saves nothing").toBe(savedBefore);

  // The wheel zooms around the pointer, and the page does not scroll with it.
  const zoom = Number(await page.locator(".world-map").getAttribute("data-zoom"));
  await page.mouse.wheel(0, -300);
  await expect.poll(async () => Number(await page.locator(".world-map").getAttribute("data-zoom"))).toBeGreaterThan(zoom);
  await page.getByRole("button", { name: "复位地图" }).click();
  await expect(page.locator(".world-map")).toHaveAttribute("data-zoom", "1.00");
});

/**
 * A country whose click point is visible and well away from the middle of a zoomed map, where focusing it
 * would bring it back to the middle (the map pans when the centre of a focused country is in the outer
 * quarters). Found from what is drawn, so it holds at any viewport.
 */
const countryAwayFromMiddle = (page: Page) =>
  page.evaluate(() => {
    const stage = document.querySelector(".world-map-stage svg")!.getBoundingClientRect();
    for (const el of document.querySelectorAll<SVGPathElement>("path.world-country")) {
      const r = el.getBoundingClientRect();
      for (const [fx, fy] of [[0.5, 0.5], [0.3, 0.3], [0.7, 0.7], [0.3, 0.7], [0.7, 0.3]]) {
        const x = r.x + r.width * fx;
        const y = r.y + r.height * fy;
        const rx = (x - stage.x) / stage.width;
        const ry = (y - stage.y) / stage.height;
        if (rx < 0.05 || rx > 0.95 || ry < 0.05 || ry > 0.95) continue;
        if (rx > 0.3 && rx < 0.7 && ry > 0.3 && ry < 0.7) continue;
        if (document.elementFromPoint(x, y) === el) return { code: el.getAttribute("data-code")!, x, y };
      }
    }
    return null;
  });

test("zoomed in, the first mouse click on a country away from the middle marks it and does not move the map; the keyboard still brings it into view", async ({ page }) => {
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  for (let i = 0; i < 3; i++) await page.getByRole("button", { name: "放大地图" }).click();
  const group = page.locator(".world-map-stage svg > g");
  const target = await countryAwayFromMiddle(page);
  expect(target, "a country is drawn away from the middle of the zoomed map").not.toBeNull();
  const el = country(page, target!.code);
  const before = await group.getAttribute("transform");
  await page.mouse.click(target!.x, target!.y);
  await expect(el, "the first click marks the country").toHaveAttribute("aria-pressed", "true");
  expect(await group.getAttribute("transform"), "and the map stays where it was").toBe(before);
  expect(JSON.parse((await stored(page))!).countries.map((m: { code: string }) => m.code)).toEqual([target!.code]);
  await page.mouse.click(target!.x, target!.y);
  await expect(el).toHaveAttribute("aria-pressed", "false");

  // Reached with Tab, the countries are brought into view: this is why the click above is a test.
  await page.getByRole("button", { name: "复位地图" }).click();
  for (let i = 0; i < 3; i++) await page.getByRole("button", { name: "放大地图" }).click();
  const zoomed = await group.getAttribute("transform");
  await page.getByRole("button", { name: "复位地图" }).focus();
  let moved = false;
  for (let i = 0; i < 60 && !moved; i++) {
    await page.keyboard.press("Tab");
    moved = (await group.getAttribute("transform")) !== zoomed;
  }
  expect(moved, "Tab through the countries pans the zoomed map").toBe(true);
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-code"))).toMatch(/^[A-Z]{2}$/);
  // A click does not take that away from the keyboard: the arrows still pan and Tab still brings the next country in.
  await page.mouse.click(target!.x, target!.y);
  await page.keyboard.press("ArrowLeft");
  const afterArrow = await group.getAttribute("transform");
  expect(afterArrow).not.toBe(zoomed);
});

test("zoomed in, the first touch tap on a country away from the middle marks it and does not move the map", async ({ browser }) => {
  const context = await browser.newContext({
    hasTouch: true,
    viewport: test.info().project.use.viewport,
    baseURL: test.info().project.use.baseURL,
  });
  const page = await context.newPage();
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  for (let i = 0; i < 3; i++) await page.getByRole("button", { name: "放大地图" }).tap();
  const group = page.locator(".world-map-stage svg > g");
  await page.locator(".world-map-stage").scrollIntoViewIfNeeded();
  const target = await countryAwayFromMiddle(page);
  expect(target).not.toBeNull();
  const before = await group.getAttribute("transform");
  await page.touchscreen.tap(target!.x, target!.y);
  await expect(country(page, target!.code), "the first tap marks the country").toHaveAttribute("aria-pressed", "true");
  expect(await group.getAttribute("transform"), "and the map stays where it was").toBe(before);
  await context.close();
});

test("China is lit by the China footprints, the China data and backup stay byte for byte the same, and CN opens China", async ({ page }) => {
  const china = JSON.stringify({ version: 1, visits: [hangzhou], provinces: { "110000": green } });
  await seedLocalRaw(page, china);
  await installBackend(page, []);
  await page.goto("/");
  await expect(page.locator(".visit-card, .stats").first()).toBeVisible();
  const chinaKeys = async () =>
    page.evaluate(() => Object.fromEntries(Object.keys(localStorage).filter((k) => !k.startsWith("shanhai-world")).map((k) => [k, localStorage.getItem(k)])));
  const before = await chinaKeys();
  const statsBefore = await page.locator(".stats").innerText();
  expect(before["shanhai-travel-v1"]).toBe(china);

  await openWorld(page);
  await mapReady(page);
  await expect(page.locator("g.world-china")).toHaveAttribute("data-lit", "true");
  await expect(page.locator("g.world-china path")).toHaveCount(34);
  await expect(stat(page, "国家/地区")).toHaveText("1");
  await expect(stat(page, "海外城市")).toHaveText("0");
  await expect(page.locator('.world-list li[data-code="CN"] .world-list-state')).toHaveText("因中国足迹点亮");
  // A mark for another country, then back to China.
  await country(page, "JP").dispatchEvent("click");
  await expect(stat(page, "国家/地区")).toHaveText("2");
  await page.locator("g.world-china").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".intro")).toBeVisible();
  await expect(page.locator(".world-view")).toBeHidden();
  expect(await chinaKeys(), "China keys are unchanged").toEqual(before);
  expect(await page.locator(".stats").innerText()).toBe(statsBefore);

  // The China export is the same file after the world view was used (the header's export button is hidden on phones).
  if ((page.viewportSize()?.width ?? 0) > 700) {
    const exportText = async () => {
      const download = page.waitForEvent("download");
      await page.getByRole("button", { name: "导出足迹" }).click();
      return readFileSync(await (await download).path(), "utf8");
    };
    const afterExport = await exportText();
    await page.reload();
    expect(await exportText(), "the export of a fresh page equals the one after using the world view").toBe(afterExport);
    const parsed = JSON.parse(afterExport);
    expect(parsed.visits).toHaveLength(1);
    expect(Object.keys(parsed)).not.toContain("countries");
  } else {
    await page.reload();
  }

  // Without any China footprint, CN is not lit; with none readable it is not lit either.
  await page.evaluate(() => localStorage.setItem("shanhai-travel-v1", JSON.stringify({ version: 1, visits: [], provinces: {} })));
  await page.reload();
  await openWorld(page);
  await mapReady(page);
  await expect(page.locator("g.world-china")).toHaveAttribute("data-lit", "false");
  await expect(stat(page, "国家/地区")).toHaveText("1");
});

test("when the China footprints become unreadable (another tab), CN goes dark and nothing of China is written", async ({ page, context }) => {
  const china = JSON.stringify({ version: 1, visits: [hangzhou], provinces: {} });
  await seedLocalRaw(page, china);
  await seedWorld(page, world({ countries: [{ code: "JP", color: green }] }));
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await expect(page.locator("g.world-china")).toHaveAttribute("data-lit", "true");
  const broken = '{"version":1,"visits":[{"code":"3301';
  const other = await context.newPage();
  await other.goto("/");
  await other.evaluate((text) => localStorage.setItem("shanhai-travel-v1", text), broken);
  await other.close();
  // The China record dialog takes over the page (as it does in the China view); behind it the world view no longer lights CN.
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.locator("g.world-china")).toHaveAttribute("data-lit", "false");
  await expect(page.locator(".world-note")).toContainText("中国足迹暂时无法读取");
  await expect(stat(page, "国家/地区")).toHaveText("1");
  expect(await stored(page, "shanhai-travel-v1")).toBe(broken);
  expect((await storedKeys(page)).filter((k) => k.includes("unreadable") || k.includes("conflict"))).toEqual([]);
});

test("the list reaches every country, including those without a polygon, and searches Chinese, pinyin, English and codes", async ({ page }) => {
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await expect(page.locator(".world-list li")).toHaveCount(catalog.countries.length);
  const search = page.getByRole("searchbox", { name: "搜索国家或地区" });
  for (const [query, name] of [["日本", "日本"], ["riben", "日本"], ["Japan", "日本"], ["jp", "日本"], ["guiyana", "法属圭亚那"], ["Kosovo", "科索沃"]] as const) {
    await search.fill(query);
    await expect(page.locator(".world-list li").filter({ hasText: name }).first(), query).toBeVisible();
  }
  await search.fill("zzzzzzzz");
  await expect(page.locator(".world-empty")).toBeVisible();

  // A small region has no polygon: the list is the only way, and says so.
  const noShape = catalog.countries.filter((c) => !c.hasShape).map((c) => c.code);
  expect(noShape).toContain("GF");
  await expect(country(page, "GF")).toHaveCount(0);
  await search.fill("法属圭亚那");
  const row = page.locator('.world-list li[data-code="GF"]');
  await expect(row).toContainText("仅可从列表选择");
  await row.getByRole("button", { name: "标记 法属圭亚那" }).click();
  await expect(row.getByRole("button", { name: "取消标记 法属圭亚那" })).toBeVisible();
  await expect(row.locator(".world-list-state")).toHaveText("独立标记");
  expect(JSON.parse((await stored(page))!).countries.map((m: { code: string }) => m.code)).toEqual(["GF"]);
  await expect(stat(page, "国家/地区")).toHaveText("1");
  await row.getByRole("button", { name: "取消标记 法属圭亚那" }).click();
  expect(JSON.parse((await stored(page))!).countries).toEqual([]);
  // The list can also open China.
  await search.fill("中国");
  await page.locator('.world-list li[data-code="CN"]').getByRole("button", { name: /进入中国视图/ }).click();
  await expect(page.locator(".intro")).toBeVisible();
});

test("the notice with the source, limits and licence is always one click away, even before the catalog is in", async ({ page }) => {
  await installBackend(page, []);
  await page.route("**/assets/world-catalog-*.js", (route) => route.abort());
  await page.goto("/");
  await openWorld(page);
  await expect(page.getByText("目录暂不可用，可重试")).toBeVisible();
  await page.getByRole("button", { name: "地图来源与限制" }).click();
  const dialog = page.getByRole("dialog", { name: "地图来源与限制" });
  await expect(dialog).toContainText("不代表对任何领土主权的立场");
  await expect(dialog).toContainText("过小的国家和地区在地图上难以点中");
  await expect(dialog).toContainText("目录载入后");
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("button", { name: "地图来源与限制" })).toBeFocused();

  await page.unroute("**/assets/world-catalog-*.js");
  await page.getByRole("button", { name: "重试" }).click();
  await mapReady(page);
  await page.getByRole("button", { name: "地图来源与限制" }).click();
  await expect(dialog).toContainText("Natural Earth");
  await expect(dialog).toContainText(catalog.source.version);
  await expect(dialog.locator("details")).toContainText(catalog.source.license);
});

test("a catalog that cannot be loaded leaves the stored world data untouched and is not a data error", async ({ page }) => {
  const raw = world({ countries: [{ code: "JP", color: green }], places: [paris] });
  await seedWorld(page, raw);
  await installBackend(page, []);
  await page.route(/\/assets\/world-(catalog|search-index)-.*\.js/, (route) => route.abort());
  await page.goto("/");
  const keys = await storedKeys(page);
  await openWorld(page);
  await expect(page.getByRole("alert").filter({ hasText: "目录暂不可用，可重试" })).toBeVisible();
  await expect(page.getByText("没有被读取、修改或覆盖")).toBeVisible();
  await expect(page.locator(".world-protect")).toHaveCount(0);
  await expect(page.locator(".world-country")).toHaveCount(0);
  expect(await stored(page)).toBe(raw);
  expect(await storedKeys(page)).toEqual(keys);
  // Retrying while it is still down changes nothing either.
  await page.getByRole("button", { name: "重试" }).click();
  await expect(page.getByText("目录暂不可用，可重试")).toBeVisible();
  expect(await stored(page)).toBe(raw);
  // When the catalog is back, the data is read in the normal way and is still there.
  await page.unroute(/\/assets\/world-(catalog|search-index)-.*\.js/);
  await page.getByRole("button", { name: "重试" }).click();
  await mapReady(page);
  await expect(page.getByRole("button", { name: "日本", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(stat(page, "海外城市")).toHaveText("1");
  expect(await stored(page)).toBe(raw);
  expect(await storedKeys(page)).toEqual(keys);
});

test("unreadable world data is kept as it is: no write, no copy, nothing marked until it is dealt with", async ({ page }) => {
  for (const [label, raw] of [
    ["not JSON", '{"kind":"world","version":1,"countries":[{"code":"JP"'],
    ["newer version", world({ version: 2 })],
    ["unknown country", world({ countries: [{ code: "ZZ", color: green }] })],
  ] as const) {
    const fresh = await page.context().newPage();
    await installBackend(fresh, []);
    await fresh.addInitScript(
      ([key, text]) => {
        if (!sessionStorage.getItem("seeded")) {
          localStorage.setItem(key, text);
          sessionStorage.setItem("seeded", "1");
        }
      },
      [WORLD_KEY, raw] as const,
    );
    await fresh.goto("/");
    const keys = await storedKeys(fresh);
    await openWorld(fresh);
    await mapReady(fresh);
    await expect(fresh.locator(".world-protect"), label).toContainText("无法读取");
    await expect(fresh.locator(".world-stats dd").first(), label).toHaveText("—");
    await country(fresh, "JP").dispatchEvent("click");
    await fresh.getByRole("searchbox", { name: "搜索国家或地区" }).fill("日本");
    await expect(fresh.locator('.world-list li[data-code="JP"] button'), label).toBeDisabled();
    expect(await stored(fresh), label).toBe(raw);
    expect(await storedKeys(fresh), label).toEqual(keys);
    // The original text can be exported.
    const download = fresh.waitForEvent("download");
    await fresh.getByRole("button", { name: "导出原文" }).click();
    expect(readFileSync(await (await download).path(), "utf8"), label).toBe(raw);
    await fresh.close();
  }
});

test("another tab's change pauses saving instead of being overwritten, and a full disk keeps the edit in memory", async ({ page, context }) => {
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await country(page, "JP").dispatchEvent("click");
  const other = await context.newPage();
  await installBackend(other, []);
  await other.goto("/");
  const theirs = world({ countries: [{ code: "US", color: gold }] });
  await other.evaluate(([key, text]) => localStorage.setItem(key, text), [WORLD_KEY, theirs] as const);
  await other.close();
  await country(page, "FR").dispatchEvent("click");
  await expect(page.locator(".world-protect")).toContainText("其他标签页修改了世界足迹");
  expect(await stored(page), "their text is untouched").toBe(theirs);
  expect((await storedKeys(page)).filter((k) => k.includes("-conflict-"))).toEqual([]);
  await expect(page.getByRole("button", { name: "法国", exact: true })).toHaveAttribute("aria-pressed", "false");
  await country(page, "DE").dispatchEvent("click");
  await expect(page.getByRole("button", { name: "德国", exact: true })).toHaveAttribute("aria-pressed", "false");
  expect(await stored(page)).toBe(theirs);
});

test("when the browser refuses to save, the edit stays on the page and can be exported", async ({ page }) => {
  await installBackend(page, []);
  await page.addInitScript(() => {
    const original = Storage.prototype.setItem;
    Storage.prototype.setItem = function (key: string, value: string) {
      if (key.startsWith("shanhai-world")) throw new DOMException("quota", "QuotaExceededError");
      return original.call(this, key, value);
    };
  });
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await country(page, "JP").dispatchEvent("click");
  await expect(page.locator(".world-protect")).toContainText("没有保存成功");
  await expect(page.getByRole("button", { name: "日本", exact: true })).toHaveAttribute("aria-pressed", "true");
  expect(await stored(page)).toBeNull();
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "导出当前世界足迹" }).click();
  const file = await download;
  expect(file.suggestedFilename()).toMatch(/^山海足迹-世界-\d{4}-\d{2}-\d{2}\.json$/);
  expect(JSON.parse(readFileSync(await file.path(), "utf8")).countries.map((m: { code: string }) => m.code)).toEqual(["JP"]);
});

test("signed in, the world view is a placeholder: the local world data is neither shown nor changed", async ({ page, context }) => {
  const raw = world({ countries: [{ code: "JP", color: green }] });
  await seedWorld(page, raw);
  const urls = recordRequests(context);
  await installBackend(page, [hangzhou]);
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await expect(page.locator(".intro")).toBeVisible();
  await openWorld(page);
  await expect(page.getByText("云端同步将在后续版本提供")).toBeVisible();
  await expect(page.locator(".world-country")).toHaveCount(0);
  await expect(page.locator(".world-stats")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "地图来源与限制" })).toBeVisible();
  expect(await stored(page)).toBe(raw);
  expect(urls.filter((u) => /maps\/world\.json|world-catalog|world-search-index/.test(u)), "no world map or catalog is fetched").toEqual([]);
});

test("every request of a world session is same-origin: no third-party map, tile, font or key", async ({ page, context }) => {
  const urls = recordRequests(context);
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await country(page, "JP").dispatchEvent("click");
  await page.getByRole("searchbox", { name: "搜索国家或地区" }).fill("riben");
  await page.getByRole("button", { name: "地图来源与限制" }).click();
  await page.getByRole("dialog").getByText("授权文本与来源").click();
  await page.keyboard.press("Escape");
  await offlineReady(page);
  await page.reload();
  await openWorld(page);
  await mapReady(page);
  expect(urls.some((u) => u.includes("/maps/world.json")), "the world map was really requested").toBe(true);
  expect(sameOrigin(urls, page), "requests to other origins").toEqual([]);
  expect(urls.filter((u) => /[?&](access_token|key|apikey|api_key|token)=/i.test(u)), "requests carrying a key").toEqual([]);
  expect(urls.filter((u) => /openstreetmap|mapbox|naturalearth|github|googleapis|unpkg|jsdelivr|cdn/i.test(u))).toEqual([]);
});

test("after one online visit the world view reloads offline: map, catalog, list, search, notice and marks", async ({ page, context }) => {
  test.setTimeout(90000);
  await installBackend(page, []);
  const urls = recordRequests(context);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await offlineReady(page);
  await country(page, "JP").dispatchEvent("click");
  const marks = await stored(page);
  const names = await page.evaluate(() => caches.keys()).then((k) => k.sort());
  expect(names.length, "one shell cache (one build) and one data cache named after the catalog version").toBe(2);
  expect(names.filter((n) => n === DATA_CACHE)).toHaveLength(1);
  expect(names.filter((n) => SHELL_CACHE.test(n))).toHaveLength(1);

  await context.setOffline(true);
  const offlineFrom = urls.length;
  const chinaMap = page.waitForResponse((r) => r.url().endsWith("/maps/100000.json"));
  await page.reload();
  const served = await chinaMap;
  expect(served.status(), "the China view's map comes from the shared cache").toBe(200);
  expect(served.fromServiceWorker()).toBe(true);
  await expect(page.locator(".intro"), "the page itself starts offline").toBeVisible();
  await openWorld(page);
  await mapReady(page);
  await expect(page.getByText("目录暂不可用")).toHaveCount(0);
  await expect(page.getByText("世界地图暂不可用")).toHaveCount(0);
  await expect(page.locator(".world-country")).toHaveCount(catalog.countries.filter((c) => c.hasShape && c.code !== "CN").length);
  await expect(page.locator("g.world-china path")).toHaveCount(34);
  await expect(page.getByRole("button", { name: "日本", exact: true })).toHaveAttribute("aria-pressed", "true");
  expect(await stored(page), "the stored data is read, not treated as damaged").toBe(marks);
  await expect(page.locator(".world-protect")).toHaveCount(0);
  await page.getByRole("searchbox", { name: "搜索国家或地区" }).fill("Germany");
  await expect(page.locator('.world-list li[data-code="DE"]')).toBeVisible();
  await page.getByRole("button", { name: "地图来源与限制" }).click();
  await expect(page.getByRole("dialog")).toContainText("Natural Earth");
  await page.keyboard.press("Escape");
  // Offline edits still save locally.
  await country(page, "DE").dispatchEvent("click");
  expect(JSON.parse((await stored(page))!).countries.map((m: { code: string }) => m.code).sort()).toEqual(["DE", "JP"]);
  expect(sameOrigin(urls.slice(offlineFrom), page)).toEqual([]);

  await page.getByRole("group", { name: "地图视图" }).getByRole("button", { name: "中国视图", exact: true }).click();
  await context.setOffline(false);
});

/** Every shell and data cache with the URLs in it: equal values are an unchanged offline copy. */
const worldCaches = (page: Page) =>
  page.evaluate(async () => {
    const out: Record<string, string[]> = {};
    for (const name of (await caches.keys()).sort())
      if (name.startsWith("shanhai-world-")) out[name] = (await (await caches.open(name)).keys()).map((r) => new URL(r.url).pathname).sort();
    return out;
  });

test("a shell refresh that fails keeps the earlier offline copy whole: the page still starts offline", async ({ page, context }) => {
  test.setTimeout(90000);
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await offlineReady(page);
  const earlier = await worldCaches(page);
  const shellName = Object.keys(earlier).find((n) => SHELL_CACHE.test(n))!;
  expect(earlier[shellName].some((u) => u.endsWith("/shell-manifest.json")), "complete: its manifest is in").toBe(true);

  // After the page has loaded, the server answers the refresh's download of the page with an index.html that
  // names a file this page never loaded, as after a deployment in between: it must not be stored.
  let changed = false;
  await context.route("**/", async (route) => {
    if (!changed) return route.continue();
    const response = await route.fetch();
    await route.fulfill({ response, body: (await response.text()).replace("</head>", '<link rel="modulepreload" href="/assets/not-loaded-ffffffff.js"></head>') });
  });
  await page.reload();
  changed = true; // the page itself has loaded; the world view, and with it the refresh, has not started
  await openWorld(page);
  await mapReady(page);
  await expect(page.locator(".world-view")).toHaveAttribute("data-offline", "failed", { timeout: 20000 });
  await context.unroute("**/");
  expect(await worldCaches(page), "the earlier shell and data caches are exactly as they were").toEqual(earlier);

  await context.setOffline(true);
  await page.reload();
  await expect(page.locator(".intro"), "index.html and its files come from the earlier, complete copy").toBeVisible();
  await openWorld(page);
  await mapReady(page);
  await context.setOffline(false);
});

test("an update that fails keeps the earlier version's shell and map data and the page still starts offline; the next one that completes cleans up", async ({ page, context }) => {
  test.setTimeout(120000);
  const EARLIER_DATA = "shanhai-world-data-sha256:earlier-version";
  const EARLIER_SHELL = "shanhai-world-shell-00000000000000aa";
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await offlineReady(page);

  // Make what is stored the offline copy of an earlier version: a shell of another build whose manifest names a
  // data cache of another catalog version. This page's own data cache stays, as it would after loading online.
  await page.evaluate(
    async ([shellName, dataName, earlierShell, earlierData]) => {
      const manifestUrl = new URL("/shell-manifest.json", location.origin).href;
      const shell = await caches.open(shellName);
      const target = await caches.open(earlierShell);
      for (const request of await shell.keys()) {
        if (request.url === manifestUrl) continue;
        await target.put(request, (await shell.match(request))!);
      }
      const manifest = await (await shell.match(manifestUrl))!.json();
      await target.put(manifestUrl, new Response(JSON.stringify({ ...manifest, dataCache: earlierData }), { headers: { "content-type": "application/json" } }));
      const data = await caches.open(dataName);
      const old = await caches.open(earlierData);
      for (const request of await data.keys()) await old.put(request, (await data.match(request))!);
      await caches.delete(shellName);
    },
    [(await page.evaluate(() => caches.keys())).find((n) => SHELL_CACHE.test(n))!, DATA_CACHE, EARLIER_SHELL, EARLIER_DATA],
  );
  const earlier = await worldCaches(page);
  expect(Object.keys(earlier).sort()).toEqual([DATA_CACHE, EARLIER_DATA, EARLIER_SHELL].sort());

  // The update fails: the page the server now sends names a file this page never loaded.
  let changed = false;
  await context.route("**/", async (route) => {
    if (!changed) return route.continue();
    const response = await route.fetch();
    await route.fulfill({ response, body: (await response.text()).replace("</head>", '<link rel="modulepreload" href="/assets/not-loaded-ffffffff.js"></head>') });
  });
  await page.reload();
  changed = true;
  await openWorld(page);
  await mapReady(page);
  await expect(page.locator(".world-view")).toHaveAttribute("data-offline", "failed", { timeout: 20000 });
  expect(await worldCaches(page), "the earlier shell and the data it reads are as they were").toEqual(earlier);

  // Offline, the earlier copy serves the page and the China map (from the earlier data cache: this page's own is gone).
  await page.evaluate((name) => caches.delete(name), DATA_CACHE);
  await context.setOffline(true);
  await page.reload();
  await expect(page.locator(".intro"), "the page starts from the earlier shell").toBeVisible();
  const china = await page.evaluate(() => fetch("/maps/100000.json").then((r) => r.status, () => "network error"));
  expect(china, "the China map comes from the data cache the earlier shell names").toBe(200);
  await context.setOffline(false);

  // The next update completes: the earlier shell and the data nothing refers to any more are removed.
  changed = false;
  await context.unroute("**/");
  await page.reload();
  await openWorld(page);
  await mapReady(page);
  await offlineReady(page);
  await expect
    .poll(async () => Object.keys(await worldCaches(page)).sort().map((n) => (SHELL_CACHE.test(n) ? "shell" : n)))
    .toEqual([DATA_CACHE, "shell"].sort());
});

test("offline, a national map whose bytes are not the catalog's is not served to the China view", async ({ page, context }) => {
  test.setTimeout(90000);
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await offlineReady(page);
  await context.setOffline(true);
  const status = () => page.evaluate(() => fetch("/maps/100000.json").then((r) => r.status, () => "network error"));
  expect(await status(), "the map the manifest vouches for").toBe(200);
  await page.evaluate(async (name) => {
    await (await caches.open(name)).put("/maps/100000.json", new Response('{"type":"FeatureCollection","features":[]}'));
  }, DATA_CACHE);
  expect(await status(), "a cached copy of other bytes is refused").toBe("network error");
  await context.setOffline(false);
});

test("a cached map that does not match the catalog, or belongs to another version, is never drawn", async ({ browser }) => {
  test.setTimeout(90000);
  // No service worker here, so the page's own requests can be failed to stand in for being offline.
  const context = await browser.newContext({ serviceWorkers: "block", baseURL: test.info().project.use.baseURL });
  const page = await context.newPage();
  await installBackend(page, []);
  const raw = world({ countries: [{ code: "JP", color: green }] });
  await seedWorld(page, raw);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  expect(await page.evaluate(() => caches.keys())).toEqual([DATA_CACHE]);

  // 1. A leftover cache of an older version stays while no complete offline copy of this version exists (here
  //    the worker is blocked, so none can be made): an earlier shell might still be reading it. The
  //    update tests below show it going once the copy is complete.
  await page.evaluate(async () => {
    const old = await caches.open("shanhai-world-data-sha256:0000");
    await old.put("/maps/world.json", new Response("{}"));
  });
  await page.reload();
  await openWorld(page);
  await mapReady(page);
  await expect(page.locator(".world-view")).toHaveAttribute("data-offline", /failed|unsupported/, { timeout: 20000 });
  expect((await page.evaluate(() => caches.keys())).sort()).toEqual([DATA_CACHE, "shanhai-world-data-sha256:0000"].sort());
  await page.evaluate(() => caches.delete("shanhai-world-data-sha256:0000"));

  // 2. The same, correct bytes under another version's name are not read: with the network gone, no map.
  await page.evaluate(async (name) => {
    const mine = await caches.open(name);
    const copy = await caches.open("shanhai-world-data-sha256:previous");
    for (const request of await mine.keys()) await copy.put(request, (await mine.match(request))!);
    await caches.delete(name);
  }, DATA_CACHE);
  await page.route("**/maps/**", (route) => route.abort());
  await page.reload();
  await openWorld(page);
  await expect(page.getByText("世界地图暂不可用")).toBeVisible();
  await expect(page.locator(".world-country")).toHaveCount(0);
  // The list still works, and the stored data is read normally: it only lacked the map.
  await expect(page.locator(".world-list li")).toHaveCount(catalog.countries.length);
  await expect(page.locator(".world-protect")).toHaveCount(0);
  expect(await stored(page)).toBe(raw);

  // 3. A cached file of this version that was cut short fails the digest: it is deleted, not drawn.
  await page.route("**/maps/**", (route) => route.abort());
  await page.evaluate(async (name) => {
    const cache = await caches.open(name);
    await cache.put("/maps/world.json", new Response('{"type":"FeatureCollection","features":[]}'));
    await cache.put("/maps/100000.json", new Response("{}"));
  }, DATA_CACHE);
  await page.getByRole("button", { name: "重试" }).click();
  await expect(page.getByText("世界地图暂不可用")).toBeVisible();
  await expect(page.locator(".world-country")).toHaveCount(0);
  expect(await page.evaluate(async (name) => (await (await caches.open(name)).keys()).length, DATA_CACHE), "the damaged copies are dropped").toBe(0);

  // 4. With the network back, retrying draws the real map and re-caches it.
  await page.unroute("**/maps/**");
  await page.getByRole("button", { name: "重试" }).click();
  await expect(country(page, "JP")).toBeAttached({ timeout: 20000 });
  expect(await page.evaluate(async (name) => (await (await caches.open(name)).keys()).length, DATA_CACHE)).toBe(2);
  await context.close();
});

test("the service worker leaves online requests alone and does not touch the API or other origins", async ({ page, context }) => {
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await offlineReady(page);
  // A reload with the worker in control still gets a fresh page from the network.
  const [response] = await Promise.all([page.waitForResponse((r) => r.request().isNavigationRequest()), page.reload()]);
  expect(response.status()).toBe(200);
  const handled = await page.evaluate(async () => {
    const registration = await navigator.serviceWorker.getRegistration();
    return { scope: registration?.scope, active: registration?.active?.scriptURL };
  });
  expect(handled.active).toMatch(/\/sw\.js$/);
  // Nothing under /api/ is ever stored by it.
  const cached = await page.evaluate(async () => {
    const urls: string[] = [];
    for (const name of await caches.keys()) for (const request of await (await caches.open(name)).keys()) urls.push(request.url);
    return urls;
  });
  expect(cached.filter((u) => u.includes("/api/"))).toEqual([]);
  expect(cached.filter((u) => new URL(u).origin !== new URL(page.url()).origin)).toEqual([]);
  expect(cached.some((u) => /\/assets\/world-catalog-.*\.js$/.test(u)), "the on-demand catalog chunk is in the shell").toBe(true);
  expect(cached.some((u) => /\/assets\/world-search-index-.*\.js$/.test(u))).toBe(true);
  void context;
});

test("keyboard: switch, focusable named countries, Enter and Space mark, arrows and +/- move the map, the list is another entry", async ({ page }) => {
  await installBackend(page, []);
  await page.goto("/");
  const world = viewSwitch(page).getByRole("button", { name: "世界视图", exact: true });
  await world.focus();
  await page.keyboard.press("Enter");
  await mapReady(page);
  await expect(page.locator(".world-view")).toBeVisible();

  // Every country with a polygon is a focusable button with a name and a state.
  const polygons = catalog.countries.filter((c) => c.hasShape && c.code !== "CN");
  await expect(page.locator("path.world-country[tabindex='0'][role='button'][aria-pressed]")).toHaveCount(polygons.length);
  const unnamed = await page.locator("path.world-country").evaluateAll((els) => els.filter((el) => !el.getAttribute("aria-label")?.trim()).length);
  expect(unnamed).toBe(0);

  const japan = page.getByRole("button", { name: "日本", exact: true });
  await japan.focus();
  await expect(japan).toBeFocused();
  // The focus is visible: a thicker, dark outline.
  expect(await japan.evaluate((el) => getComputedStyle(el).strokeWidth)).toMatch(/^2\.6/);
  await page.keyboard.press("Enter");
  await expect(japan).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".world-status")).toHaveText("已标记日本");
  await page.keyboard.press(" ");
  await expect(japan).toHaveAttribute("aria-pressed", "false");
  expect(JSON.parse((await stored(page))!).countries).toEqual([]);
  expect(await page.evaluate(() => window.scrollY), "Space does not scroll the page").toBe(0);

  // Tab walks through the countries in order (not stuck on one) ...
  await page.keyboard.press("Tab");
  const next = await page.evaluate(() => document.activeElement?.getAttribute("data-code"));
  expect(next, "Tab moved to another country").toMatch(/^[A-Z]{2}$/);
  expect(next).not.toBe("JP");

  // ... +/- zoom and the arrows pan, from the keyboard.
  const map = page.locator(".world-map");
  await page.keyboard.press("+");
  await page.keyboard.press("+");
  const zoomed = Number(await map.getAttribute("data-zoom"));
  expect(zoomed).toBeGreaterThan(1);
  const group = page.locator(".world-map-stage svg > g");
  const t0 = await group.getAttribute("transform");
  await page.keyboard.press("ArrowLeft");
  const t1 = await group.getAttribute("transform");
  expect(t1).not.toBe(t0);
  await page.keyboard.press("ArrowDown");
  expect(await group.getAttribute("transform")).not.toBe(t1);
  await page.keyboard.press("-");
  expect(Number(await map.getAttribute("data-zoom"))).toBeLessThan(zoomed);
  await page.keyboard.press("0");
  await expect(map).toHaveAttribute("data-zoom", "1.00");
  // Focusing a country that is off screen brings it into view.
  await page.getByRole("button", { name: "放大地图" }).click();
  await page.getByRole("button", { name: "放大地图" }).click();
  await page.getByRole("button", { name: "放大地图" }).click();
  await page.getByRole("button", { name: "复位地图" }).focus();
  const newZealand = page.getByRole("button", { name: "新西兰", exact: true });
  let reached = false;
  for (let i = 0; i < 300 && !reached; i++) {
    await page.keyboard.press("Tab");
    reached = await newZealand.evaluate((el) => el === document.activeElement);
  }
  expect(reached, "Tab reaches New Zealand").toBe(true);
  const inView = await page.evaluate(() => {
    const s = document.querySelector(".world-map-stage svg")!.getBoundingClientRect();
    const r = document.activeElement!.getBoundingClientRect();
    const cx = r.x + r.width / 2;
    const cy = r.y + r.height / 2;
    return cx >= s.x && cx <= s.right && cy >= s.y && cy <= s.bottom;
  });
  expect(inView, "the focused country was brought into view").toBe(true);

  // The skip control jumps to the list's search box; the list marks too.
  await page.getByRole("button", { name: "跳到国家/地区列表" }).focus();
  await page.keyboard.press("Enter");
  const search = page.getByRole("searchbox", { name: "搜索国家或地区" });
  await expect(search).toBeFocused();
  await page.keyboard.type("Brazil");
  await page.keyboard.press("Tab");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("button", { name: "巴西", exact: true })).toHaveAttribute("aria-pressed", "true");

  // The notice dialog opens from the keyboard and returns focus on close.
  const notice = page.getByRole("button", { name: "地图来源与限制" });
  await notice.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(notice).toBeFocused();
});

test("keyboard: Enter on CN returns to China, and the header tabs do too", async ({ page }) => {
  await installBackend(page, []);
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  const cn = page.locator("g.world-china");
  await expect(cn).toHaveAttribute("aria-label", /中国（未点亮），按 Enter 进入中国视图/);
  await cn.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".intro")).toBeVisible();
  await openWorld(page);
  await page.getByRole("button", { name: /旅行足迹/ }).first().click();
  await expect(page.locator(".intro")).toBeVisible();
  await expect(page.locator(".world-view")).toBeHidden();
});

test("the world view never scrolls sideways at phone, tablet and desktop widths, with the notice open and in China view", async ({ page }) => {
  await installBackend(page, []);
  await seedWorld(page, world({ countries: [{ code: "JP", color: green }], places: [paris] }));
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  for (const width of [320, 360, 390, 640, 768, 1024, 1280]) {
    await page.setViewportSize({ width, height: 800 });
    const o = await horizontalOverflow(page);
    expect(o.wide, `width ${width}`).toEqual([]);
    expect(o.scroll, `width ${width}`).toBeLessThanOrEqual(width);
    for (const sel of [".view-switch", ".world-map-stage", ".world-search", ".world-stats"]) {
      const box = (await page.locator(sel).first().boundingBox())!;
      expect(box.x, `${sel} at ${width}`).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width, `${sel} at ${width}`).toBeLessThanOrEqual(width + 0.5);
    }
  }
  for (const width of [320, 390, 1280]) {
    await page.setViewportSize({ width, height: 700 });
    await page.getByRole("button", { name: "地图来源与限制" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    const box = (await dialog.boundingBox())!;
    expect(box.x, `dialog at ${width}`).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width, `dialog at ${width}`).toBeLessThanOrEqual(width + 0.5);
    expect((await horizontalOverflow(page)).scroll, `dialog at ${width}`).toBeLessThanOrEqual(width);
    await page.keyboard.press("Escape");
  }
  // The China view's header still fits with the switch in it.
  for (const width of [320, 390, 1280]) {
    await page.setViewportSize({ width, height: 700 });
    await viewSwitch(page).getByRole("button", { name: "中国视图", exact: true }).click();
    const o = await horizontalOverflow(page);
    expect(o.wide, `China at ${width}`).toEqual([]);
    expect(o.scroll, `China at ${width}`).toBeLessThanOrEqual(width);
    await openWorld(page);
  }
});

test("the built world files carry no third-party map domain or key (the optional OSM/Mapbox base map is outside the world view)", async () => {
  const dist = join(process.cwd(), "dist");
  const assets = readdirSync(join(dist, "assets"));
  // The world view's own code and data: its chunk, stylesheet, the on-demand catalog and search index, and the map files.
  const world = assets.filter((f) => /^(WorldView|world-catalog|world-search-index)-/.test(f)).map((f) => join(dist, "assets", f));
  expect(world.length, "the world chunks exist").toBeGreaterThanOrEqual(4);
  const files = [...world, join(dist, "maps", "world.json"), join(dist, "maps", "100000.json"), join(dist, "sw.js")];
  const urlPattern = /(?:https?:)?\/\/[a-z0-9.-]+\.[a-z]{2,}[^\s"'`)\\]*/gi;
  // `source.url` and `source.licenseUrl` of the catalog are text shown in the notice, not requests.
  const shownText = new Set([catalog.source.url, catalog.source.licenseUrl]);
  const found = new Set<string>();
  for (const file of files) {
    const text = readFileSync(file, "utf8");
    for (const match of text.match(urlPattern) ?? []) found.add(`${file.split("/").pop()}: ${match}`);
    expect(text, `${file} has no key`).not.toMatch(/pk\.eyJ|access_token|api[_-]?key|VITE_MAPBOX|VITE_OSM/i);
  }
  // Every URL that is in these files is either the catalog's shown source fields or part of a licence text.
  const licenceText = /naturalearthdata\.com|creativecommons|publicdomain/i;
  const unexpected = [...found].filter((entry) => {
    const url = entry.slice(entry.indexOf(": ") + 2);
    return ![...shownText].some((shown) => url.startsWith(shown) || shown.startsWith(url)) && !licenceText.test(url);
  });
  expect(unexpected, "URLs that are neither shown source fields nor licence text").toEqual([]);
  // The world view does not import the tile code.
  const view = readFileSync(world.find((f) => /WorldView-.*\.js$/.test(f))!, "utf8");
  expect(view).not.toMatch(/OsmProvinceMap|tile\.openstreetmap|api\.mapbox/);
  for (const imported of view.matchAll(/from"\.\/([^"]+)"/g)) expect(imported[1]).not.toMatch(/Osm|Mapbox|tile/i);
});

test("screenshots of the world view for the review (WORLD_SHOTS=<dir>)", async ({ page }, info) => {
  test.skip(!process.env.WORLD_SHOTS, "only when asked for");
  await installBackend(page, [hangzhou]);
  await seedLocalRaw(page, JSON.stringify({ version: 1, visits: [hangzhou], provinces: {} }));
  await seedWorld(
    page,
    world({
      countries: [{ code: "JP", color: gold }, { code: "BR", color: green }, { code: "AU", color: "#d88370" }, { code: "GF", color: "#a08cbd" }],
      places: [paris, { id: "p2", name: "纽约", country: "US", lat: 40.7128, lng: -74.006, trips: [] }],
    }),
  );
  await page.goto("/");
  await openWorld(page);
  await mapReady(page);
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(process.env.WORLD_SHOTS!, `world-${info.project.name}.png`), fullPage: true });
  // Keyboard only, so that the focus ring shows: focus Japan, zoom in with +.
  await page.getByRole("button", { name: "日本", exact: true }).focus();
  await page.keyboard.press("+");
  await page.keyboard.press("+");
  // Leave and come back: focusing a country that is off screen pans it into view.
  await page.keyboard.press("Tab");
  await page.keyboard.press("Shift+Tab");
  await page.waitForTimeout(300);
  await page.locator(".world-map-card").screenshot({ path: join(process.env.WORLD_SHOTS!, `world-zoom-focus-${info.project.name}.png`) });
  await page.getByRole("button", { name: "地图来源与限制" }).click();
  await page.waitForTimeout(300);
  await page.screenshot({ path: join(process.env.WORLD_SHOTS!, `world-notice-${info.project.name}.png`) });
});
