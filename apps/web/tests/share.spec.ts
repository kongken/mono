import { test, expect, type Page } from "@playwright/test";
import { dongcheng, fakeBackend, login, xicheng } from "./fake-backend";

const noOverflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const token = "Ab3-_".repeat(8) + "Ab3";
const failure = (code: string, status: number) => ({ status, json: { code, message: code } });

type Settings = {
  visibility: string;
  showCities: boolean;
  showDates: boolean;
  showNotes: boolean;
  showPhotos: boolean;
  photoIds: string[];
};
const privateSettings = (): Settings => ({
  visibility: "private", showCities: false, showDates: false, showNotes: false, showPhotos: false, photoIds: [],
});

// A small in-memory ShareService that keeps the real contract: conditional on version,
// a rebuild only for the revision that was previewed, idempotent on mutation_id.
function fakeShare(page: Page) {
  const state = {
    version: 0,
    settings: privateSettings(),
    token: "",
    published: false,
    publishedRevision: 0,
    revision: 5,
    nextToken: 0,
    saves: [] as Record<string, unknown>[],
    previews: [] as Settings[],
    revokes: [] as string[],
    receipts: new Map<string, { share: unknown; request: string }>(),
    failSaveReply: 0,
    enabled: true,
  };
  const info = () => ({
    settings: state.settings,
    version: String(state.version),
    url: state.published ? `https://trip.example.com/s/${state.token}` : "",
    published: state.published,
    publishedAt: state.published ? "2026-10-14T01:02:03Z" : undefined,
    publishedRevision: String(state.publishedRevision),
    currentRevision: String(state.revision),
  });
  const content = (s: Settings) => ({
    v: 1,
    published_on: "2026-10-14",
    fields: { cities: s.showCities, dates: s.showDates, notes: s.showNotes, photos: s.showPhotos },
    stats: {
      explored_provinces: 1, total_provinces: 34, cities: 1, total_cities: 391, trips: 2,
      by_province: [{ code: "110000", cities: 2, total: 16 }],
    },
    provinces: ["110000"],
    ...(s.showCities
      ? {
          cities: [
            {
              code: "110101", name: "东城区", province: "110000", color: "#4b937d",
              trips: [{ ...(s.showDates ? { date: "2026-05-01" } : {}), ...(s.showNotes ? { note: "故宫" } : {}) }],
            },
          ],
        }
      : {}),
  });
  return {
    state,
    install: () =>
      page.route("**/api/bff.v1.ShareService/**", async (route) => {
        const method = route.request().url().split("/").pop();
        // JSON omits zero values; read them back as the server would.
        const raw = route.request().postDataJSON();
        const body = {
          expectedVersion: "0", previewRevision: "0", refreshContent: false, ...raw,
          settings: { ...privateSettings(), ...raw.settings },
        };
        expect(route.request().headers().authorization).toBeTruthy();
        const ok = (json: unknown) => route.fulfill({ status: 200, json });
        if (method === "GetShare") return ok({ share: info(), enabled: state.enabled });
        if (method === "PreviewShare") {
          state.previews.push(body.settings);
          return ok({ contentJson: JSON.stringify(content(body.settings)), footprintRevision: String(state.revision) });
        }
        if (method === "SaveShare") {
          state.saves.push(body);
          const request = JSON.stringify({ ...body, mutationId: undefined });
          const seen = state.receipts.get(body.mutationId);
          if (seen) {
            if (seen.request !== request) return route.fulfill(failure("already_exists", 409));
            return ok({ share: seen.share, replayed: true });
          }
          if (Number(body.expectedVersion) !== state.version) return route.fulfill(failure("aborted", 409));
          const s = body.settings as Settings;
          if (body.refreshContent && Number(body.previewRevision) !== state.revision) return route.fulfill(failure("aborted", 409));
          if (!state.published && s.visibility !== "private" && !body.refreshContent) return route.fulfill(failure("invalid_argument", 400));
          state.settings = { ...privateSettings(), ...s };
          state.version++;
          if (s.visibility === "private") {
            state.published = false;
            state.token = "";
          } else {
            if (!state.published) state.token = `${token.slice(0, 40)}${String(++state.nextToken).padStart(3, "0")}`;
            state.published = true;
            if (body.refreshContent) state.publishedRevision = state.revision;
          }
          state.receipts.set(body.mutationId, { share: info(), request });
          if (state.failSaveReply > 0) {
            state.failSaveReply--;
            return route.fulfill(failure("unavailable", 503));
          }
          return ok({ share: info() });
        }
        if (method === "RevokeShare") {
          state.revokes.push(body.mutationId);
          state.published = false;
          state.token = "";
          state.settings = { ...state.settings, visibility: "private" };
          state.version++;
          return ok({ share: info() });
        }
        return route.fulfill(failure("unimplemented", 404));
      }),
  };
}

const media = [
  { id: "m-jpeg", regionCode: "110101", filename: "故宫.jpg", contentType: "image/jpeg", status: "ready", width: 40, height: 30, sizeBytes: "10" },
  { id: "m-png", regionCode: "110101", filename: "截图.png", contentType: "image/png", status: "ready", width: 40, height: 30, sizeBytes: "10" },
  { id: "m-away", regionCode: "330100", filename: "西湖.jpg", contentType: "image/jpeg", status: "ready", width: 40, height: 30, sizeBytes: "10" },
];

async function openShare(page: Page) {
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "分享页面" }).click();
  const dialog = page.getByRole("dialog", { name: "分享我的足迹" });
  await expect(dialog).toBeVisible();
  return dialog;
}

async function ownerPage(page: Page, shareEnabled = true) {
  const backend = fakeBackend(page, { visits: [dongcheng(), xicheng()] }, { shareEnabled });
  await backend.install();
  const share = fakeShare(page);
  await share.install();
  await page.route("**/api/bff.v1.MediaService/ListMedia", (route) => route.fulfill({ json: { media } }));
  await login(page);
  return share;
}

test("the owner previews, publishes, narrows, updates and revokes a share page", async ({ page }) => {
  const share = await ownerPage(page);
  const dialog = await openShare(page);
  expect(await noOverflow(page)).toBe(true);
  // Conservative defaults: province level only, nothing is published before confirming.
  await expect(dialog.getByRole("radio", { name: /知道链接的人/ })).toBeChecked();
  for (const name of ["城市名称和颜色", "旅行日期", "旅行备注", "照片"])
    await expect(dialog.getByRole("checkbox", { name })).not.toBeChecked();
  await expect(dialog.getByRole("checkbox", { name: "旅行日期" })).toBeDisabled();
  expect(share.state.saves).toHaveLength(0);

  await dialog.getByRole("button", { name: "预览公开内容" }).click();
  const preview = dialog.getByRole("region", { name: "公开内容预览" });
  await expect(preview).toContainText("已探索 1 / 34 个省份");
  await expect(preview).toContainText("不公开城市名称、日期、备注和照片");
  await expect(preview).not.toContainText("东城区");
  await dialog.getByRole("button", { name: "确认发布" }).click();
  const link = dialog.getByRole("textbox", { name: "公开链接" });
  await expect(link).toHaveValue(/\/s\/[A-Za-z0-9_-]{43}$/);
  const firstLink = await link.inputValue();
  expect(share.state.saves[0]).toMatchObject({ expectedVersion: "0", refreshContent: true, previewRevision: "5" });
  expect(await noOverflow(page)).toBe(true);

  // Adding fields invalidates the preview and needs a fresh confirmation.
  await dialog.getByRole("checkbox", { name: "城市名称和颜色" }).check();
  await dialog.getByRole("checkbox", { name: "旅行备注" }).check();
  await dialog.getByRole("checkbox", { name: "照片" }).check();
  const picker = dialog.getByRole("group", { name: /选择照片/ });
  await expect(picker).toContainText("故宫.jpg");
  await expect(picker).not.toContainText("截图.png");
  await expect(picker).not.toContainText("西湖.jpg");
  await picker.getByRole("checkbox", { name: /故宫\.jpg/ }).check();
  await expect(dialog.getByRole("button", { name: "确认更新" })).toHaveCount(0);
  await dialog.getByRole("button", { name: "预览公开内容" }).click();
  await expect(preview).toContainText("东城区");
  await expect(preview).toContainText("故宫");
  await dialog.getByRole("button", { name: "确认更新" }).click();
  await expect(dialog.getByText("分享页已更新")).toBeVisible();
  expect(share.state.saves[1]).toMatchObject({ refreshContent: true, settings: { showCities: true, showNotes: true, showPhotos: true, photoIds: ["m-jpeg"] } });
  expect(await dialog.getByRole("textbox", { name: "公开链接" }).inputValue()).toBe(firstLink);

  // Hiding a field is saved at once, without reading footprints again.
  await dialog.getByRole("checkbox", { name: "旅行备注" }).uncheck();
  await dialog.getByRole("button", { name: "保存设置" }).click();
  await expect(dialog.getByText("分享页已更新")).toBeVisible();
  expect(share.state.saves[2]).toMatchObject({ refreshContent: false, previewRevision: "0", settings: { showNotes: false } });
  expect(share.state.previews).toHaveLength(2);

  // Refreshing content is its own confirmed step.
  share.state.revision = 9;
  await dialog.getByRole("button", { name: "关闭", exact: true }).first().click();
  const again = await openShare(page);
  await expect(again.getByText("之后有 4 项足迹改动未包含在页面中")).toBeVisible();
  await again.getByRole("checkbox", { name: /用最新足迹更新页面内容/ }).check();
  await again.getByRole("button", { name: "预览公开内容" }).click();
  await again.getByRole("button", { name: "确认更新" }).click();
  await expect(again.getByText("分享页已更新")).toBeVisible();
  expect(share.state.saves[3]).toMatchObject({ refreshContent: true, previewRevision: "9" });

  // Revoking asks first and warns that copies cannot be recalled.
  await again.getByRole("button", { name: "撤销分享" }).click();
  await expect(again.getByRole("alert")).toContainText("已经被他人保存、转发或被平台缓存的内容无法收回");
  expect(share.state.revokes).toHaveLength(0);
  await again.getByRole("button", { name: "确认撤销链接" }).click();
  await expect(again.getByText("链接立即失效")).toBeVisible();
  await expect(again.getByRole("textbox", { name: "公开链接" })).toHaveCount(0);
  expect(share.state.revokes).toHaveLength(1);
  expect(share.state.token).toBe("");

  // Publishing again issues a new link.
  await again.getByRole("radio", { name: /知道链接的人/ }).check();
  await again.getByRole("button", { name: "预览公开内容" }).click();
  await again.getByRole("button", { name: "确认发布" }).click();
  expect(await again.getByRole("textbox", { name: "公开链接" }).inputValue()).not.toBe(firstLink);
  expect(await noOverflow(page)).toBe(true);
});

test("a footprint change between preview and publish publishes nothing and asks again; a lost reply is retried safely", async ({ page }) => {
  const share = await ownerPage(page);
  const dialog = await openShare(page);
  await dialog.getByRole("button", { name: "预览公开内容" }).click();
  await expect(dialog.getByRole("region", { name: "公开内容预览" })).toBeVisible();
  share.state.revision = 6;
  await dialog.getByRole("button", { name: "确认发布" }).click();
  await expect(dialog.getByRole("alert")).toContainText("没有发布任何内容");
  expect(share.state.published).toBe(false);
  await expect(dialog.getByRole("region", { name: "公开内容预览" })).toHaveCount(0);

  await dialog.getByRole("button", { name: "预览公开内容" }).click();
  share.state.failSaveReply = 1;
  await dialog.getByRole("button", { name: "确认发布" }).click();
  await expect(dialog.getByRole("alert")).toBeVisible();
  await dialog.getByRole("button", { name: "确认发布" }).click();
  await expect(dialog.getByRole("textbox", { name: "公开链接" })).toBeVisible();
  const ids = share.state.saves.slice(-2).map((s) => s.mutationId);
  expect(ids[0]).toBe(ids[1]);
  expect(share.state.version).toBe(1);
});

test("sharing is offered only when the server has it", async ({ page }) => {
  await ownerPage(page, false);
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("历史版本");
  await expect(page.getByRole("button", { name: "分享页面" })).toHaveCount(0);
});

test("the share dialog is keyboard operable and returns control on Escape", async ({ page }) => {
  await ownerPage(page);
  const dialog = await openShare(page);
  await expect(dialog.getByRole("radio", { name: /不公开/ })).toBeVisible();
  await page.keyboard.press("Tab");
  expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'))).toBe(true);
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press("Tab");
    expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'))).toBe(true);
  }
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
});

// --- the anonymous page ---

const publicContent = (extra: Record<string, unknown> = {}) => ({
  v: 1,
  published_on: "2026-10-14",
  fields: { cities: true, dates: true, notes: true, photos: true },
  stats: { explored_provinces: 2, total_provinces: 34, cities: 2, total_cities: 391, trips: 3, by_province: [{ code: "330000", cities: 1, total: 11 }, { code: "110000", cities: 1, total: 1 }] },
  provinces: ["330000", "110000"],
  cities: [
    { code: "330100", name: "杭州市", province: "330000", color: "#4b937d", trips: [{ date: "2026-05-01", note: "<b>西湖</b> 雨天", photos: [{ id: "pAbCdEfGh", w: 40, h: 30 }] }, {}] },
    { code: "110101", name: "东城区", province: "110000", color: "#e2af5c", trips: [{ date: "2026-06-01" }] },
  ],
  ...extra,
});

async function recordStorageAndRequests(page: Page) {
  await page.addInitScript(() => {
    const touched: string[] = [];
    (window as unknown as { __touched: string[] }).__touched = touched;
    for (const name of ["getItem", "setItem", "removeItem", "key"] as const) {
      const original = Storage.prototype[name];
      Storage.prototype[name] = function (this: Storage, ...args: never[]) {
        touched.push(name);
        return (original as (...a: unknown[]) => unknown).apply(this, args);
      } as never;
    }
  });
  const requests: { url: string; auth?: string; cookie?: string }[] = [];
  page.on("request", (r) => {
    const url = new URL(r.url());
    if (url.pathname.startsWith("/api")) requests.push({ url: url.pathname + url.search, auth: r.headers().authorization });
  });
  return requests;
}

test("the public page is read-only, anonymous and never mounts the application", async ({ page }) => {
  const requests = await recordStorageAndRequests(page);
  const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jJ1sAAAAASUVORK5CYII=", "base64");
  await page.route(`**/api/share/${token}/data`, (route) => route.fulfill({ json: publicContent(), headers: { "Cache-Control": "no-store" } }));
  await page.route(`**/api/share/${token}/photos/**`, (route) => route.fulfill({ body: png, contentType: "image/png" }));
  await page.goto(`/s/${token}`);
  await expect(page.getByRole("heading", { name: "我的旅行足迹" })).toBeVisible();
  await expect(page.getByText("已探索省份")).toBeVisible();
  await expect(page.getByRole("img", { name: /中国地图，已探索 2 个省份/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: "杭州市" , exact: false }).or(page.getByText("杭州市")).first()).toBeVisible();
  // Notes are text, never markup.
  await expect(page.getByText("<b>西湖</b> 雨天")).toBeVisible();
  expect(await page.locator("main b").count()).toBe(0);
  const photo = page.getByRole("img", { name: "旅途照片" });
  await expect(photo).toBeVisible();
  await expect(photo).toHaveAttribute("referrerpolicy", "no-referrer");
  await expect(photo).toHaveAttribute("loading", "lazy");
  // Nothing editable, no application, no account, no local data, no credentials.
  await expect(page.locator("button, input, textarea, select")).toHaveCount(0);
  await expect(page.locator(".app-shell")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "账号" })).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { __touched: string[] }).__touched)).toEqual([]);
  expect(requests.length).toBeGreaterThan(0);
  for (const r of requests) {
    expect(r.url).toMatch(new RegExp(`^/api/share/${token}/`));
    expect(r.auth).toBeUndefined();
    expect(r.url).not.toMatch(/m-jpeg|ci-migrate|ci_user/);
  }
  expect(await noOverflow(page)).toBe(true);
});

test("the public page at 320px has no horizontal overflow, with long notes", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 640 });
  const long = "很长的备注".repeat(60) + " https://example.com/" + "a".repeat(80);
  await page.route(`**/api/share/${token}/data`, (route) =>
    route.fulfill({ json: publicContent({ cities: [{ code: "330100", name: "杭州市", province: "330000", color: "#4b937d", trips: [{ date: "2026-05-01", note: long }] }] }) }),
  );
  await page.goto(`/s/${token}`);
  await expect(page.getByText("很长的备注").first()).toBeVisible();
  expect(await noOverflow(page)).toBe(true);
});

test("a province-level share shows no cities, and a revoked or malformed link is one plain not-found page", async ({ page }) => {
  const requests = await recordStorageAndRequests(page);
  await page.route(`**/api/share/${token}/data`, (route) =>
    route.fulfill({ json: publicContent({ fields: { cities: false, dates: false, notes: false, photos: false }, cities: undefined }) }),
  );
  await page.goto(`/s/${token}`);
  await expect(page.getByText("已探索省份")).toBeVisible();
  await expect(page.getByRole("heading", { name: "城市足迹" })).toHaveCount(0);
  await expect(page.getByText("杭州市")).toHaveCount(0);

  await page.unroute(`**/api/share/${token}/data`);
  await page.route(`**/api/share/${token}/data`, (route) => route.fulfill({ status: 404, body: "not found", contentType: "text/plain" }));
  await page.reload();
  const missing = page.getByRole("heading", { name: "链接不存在或已撤销" });
  await expect(missing).toBeVisible();
  const text = await page.locator("main").innerText();

  const before = requests.length;
  for (const path of ["/s", "/s/", "/s/not-a-token", `/s/${token}/extra`, `/s/${token}x`]) {
    await page.goto(path);
    await expect(missing).toBeVisible();
    expect(await page.locator("main").innerText()).toBe(text);
    await expect(page.locator(".app-shell")).toHaveCount(0);
  }
  expect(requests.length).toBe(before);
  expect(await page.evaluate(() => (window as unknown as { __touched: string[] }).__touched)).toEqual([]);
});

test("a temporary failure can be retried and is not shown as a missing link", async ({ page }) => {
  let calls = 0;
  await page.route(`**/api/share/${token}/data`, (route) =>
    ++calls === 1 ? route.fulfill({ status: 503, body: "x" }) : route.fulfill({ json: publicContent() }),
  );
  await page.goto(`/s/${token}`);
  await expect(page.getByRole("alert")).toContainText("暂时不可用");
  await expect(page.getByRole("heading", { name: "链接不存在或已撤销" })).toHaveCount(0);
  await page.getByRole("button", { name: "重试" }).click();
  await expect(page.getByRole("heading", { name: "我的旅行足迹" })).toBeVisible();
});

test("the normal application is unaffected outside /s/", async ({ page }) => {
  await ownerPage(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await page.goto("/shares");
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "链接不存在或已撤销" })).toHaveCount(0);
});
