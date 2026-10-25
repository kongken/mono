import { expect, request, test, type BrowserContext, type Page, type Request } from "@playwright/test";

/**
 * The trip-map product and OzID in a real browser against the real BFF, usersrv and tripmap (started by
 * internal/e2e TestTripMapBrowserChain): HTTPS, sibling sub-domains for the product, OzID and the API, the
 * BFF's host-only Cookie, exact CORS, CSRF and the X-Kongken-User guard. Nothing of the account chain or the
 * footprint service is mocked; only the network is interfered with, and only where a scenario says so.
 *
 * Every account is dedicated to one scenario of one Playwright project and was seeded with one footprint
 * (Hangzhou, 330100) on the server, so an account restored from its own cache or from the server is told apart
 * from an empty one. Hosts are reserved test names; the password is the harness's throwaway one.
 */
type Account = { id: string; email: string };
type Stack = { id: string; api: string; trip: string; control: string; password: string; accounts: Record<string, Account> };
const stack: Stack = JSON.parse(process.env.TRIPMAP_REAL_STACK ?? "{}");

test.skip(!process.env.TRIPMAP_REAL_STACK, "started by internal/e2e TestTripMapBrowserChain");

const SESSION_KEY = "shanhai-session-v1";
const CLOUD = "shanhai-cloud-v2:";
const LOGIN = `${stack.id}/login?app_id=trip-map&return_path=%2F`;
const acct = (kind: string, info: { project: { name: string } }): Account => stack.accounts[`tm-${kind}-${info.project.name}`];

/** What the server holds for a seeded account (the control origin of the scenario). */
type Server = { visits: string[]; receipts: string[]; changes: number };
async function server(user: string): Promise<Server> {
  const c = new URL(stack.control);
  const client = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const res = await client.fetch(`https://127.0.0.1:${c.port}/server?user=${user}`, { headers: { Host: c.host } });
    expect(res.status(), await res.text()).toBe(200);
    return (await res.json()) as Server;
  } finally {
    await client.dispose();
  }
}
async function control(path: string) {
  const c = new URL(stack.control);
  const client = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const res = await client.fetch(`https://127.0.0.1:${c.port}${path}`, { headers: { Host: c.host } });
    expect(res.status(), await res.text()).toBeLessThan(300);
    return res.status() === 204 ? undefined : await res.json();
  } finally {
    await client.dispose();
  }
}

// OzID is served with the Content-Security-Policy of deploy/ozid-web; a violation is a failure.
const violations = new WeakMap<Page, string[]>();
test.beforeEach(({ context, page }) => {
  const seen: string[] = [];
  violations.set(page, seen);
  context.on("console", (m) => {
    if (/Content Security Policy|Refused to (load|connect|execute|apply)/i.test(m.text())) seen.push(m.text());
  });
});
test.afterEach(({ page }) => {
  expect(violations.get(page)).toEqual([]);
});

const dialog = (page: Page) => page.getByRole("dialog");
const accountButton = (page: Page) => page.getByRole("button", { name: "账号", exact: true });
const signInButton = (page: Page) => page.getByRole("button", { name: "登录", exact: true });
const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const readKey = (page: Page, key: string) => page.evaluate((k) => localStorage.getItem(k), key);
const cloudKeys = (page: Page) => page.evaluate((p) => Object.keys(localStorage).filter((k) => k.startsWith(p)), CLOUD);
const sessionUser = async (page: Page) => JSON.parse((await readKey(page, SESSION_KEY)) ?? "{}").user?.id as string | undefined;
type Cache = { saved: { visits: { code: string }[] }; confirmed?: { visits: { code: string }[] }; revision: number; pendingBatch?: { id: string; revision: number; ops: unknown[] } };
const cacheOf = async (page: Page, id: string): Promise<Cache | null> => {
  const raw = await readKey(page, CLOUD + id);
  return raw ? (JSON.parse(raw) as Cache) : null;
};
const codes = (c: Cache | null) => (c?.saved.visits ?? []).map((v) => v.code).sort();

/** The page is shown or focused again: it asks Me once more (the ask is rate limited, so time moves on first). */
async function refocus(page: Page, ms = 6_000) {
  await page.clock.fastForward(ms);
  await page.evaluate(() => {
    window.dispatchEvent(new Event("focus"));
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

async function addCity(page: Page, province: string, city: string) {
  await page.getByRole("button", { name: "添加足迹" }).first().click();
  const selects = dialog(page).locator("select");
  await selects.nth(0).selectOption(province);
  await selects.nth(1).selectOption(city);
  await dialog(page).getByRole("button", { name: "保存足迹" }).click();
}

const panel = (page: Page) => page.getByTestId("auth-panel");
async function submitLogin(page: Page, who: Account) {
  await panel(page).getByLabel("邮箱").fill(who.email);
  await panel(page).getByLabel("密码", { exact: true }).fill(stack.password);
  await panel(page).getByRole("button", { name: "登录", exact: true }).click();
}

/** From the product: its account window, its button, OzID's real sign-in page, and back to the start page. */
async function enterViaOzid(page: Page, who: Account, opts: { localDataOnDevice?: boolean } = {}) {
  await page.goto(`${stack.trip}/`);
  await signInButton(page).click();
  await dialog(page).getByTestId("ozid-sign-in").click();
  await expect(page).toHaveURL(LOGIN);
  await submitLogin(page, who);
  await page.waitForURL(`${stack.trip}/`);
  await expect(accountButton(page)).toBeVisible();
  await expect.poll(() => sessionUser(page)).toBe(who.id);
  // Footprints saved on this device before signing in are offered for import; "later" leaves them as they are.
  if (opts.localDataOnDevice) await page.getByRole("button", { name: "稍后再说" }).click();
}

/** The same browser's other tab at OzID: the shared session ends, or another account takes it. */
async function ozidSignOut(context: BrowserContext) {
  const tab = await context.newPage();
  await tab.goto(`${stack.id}/sessions`);
  await tab.getByTestId("sign-out-here").getByRole("button", { name: "退出这个浏览器的登录" }).click();
  await expect(tab).toHaveURL(`${stack.id}/login`);
  await tab.close();
}
async function ozidSignIn(context: BrowserContext, who: Account) {
  const tab = await context.newPage();
  await tab.goto(`${stack.id}/login`);
  await submitLogin(tab, who);
  await expect(tab.getByTestId("overview")).toBeVisible();
  await tab.close();
}

/** Every footprint write the page sends: which batch (mutation ID) went out under which account header, and the answer. */
type Write = { mutationId: string; account: string | undefined; status?: number };
function recordWrites(page: Page) {
  const writes: Write[] = [];
  const mine = new Map<Request, Write>();
  page.on("request", (r: Request) => {
    if (!r.url().endsWith("/bff.tripmap.v1.FootprintService/SyncChanges")) return;
    const w: Write = { mutationId: JSON.parse(r.postData() ?? "{}").mutationId, account: r.headers()["x-kongken-user"] };
    mine.set(r, w);
    writes.push(w);
  });
  page.on("response", (res) => {
    const w = mine.get(res.request());
    if (w) w.status = res.status();
  });
  return writes;
}

const LOCAL_V1 = JSON.stringify({ version: 1, visits: [], provinces: { "330000": "#4b937d" } });

test.describe("sign in through OzID and come back", () => {
  test("the real return restores the account under its own UUID, with its cache, and nothing else on this device changes", async ({ page, context }, info) => {
    const who = acct("round", info);
    await page.addInitScript((raw) => {
      if (!localStorage.getItem("shanhai-travel-v1")) localStorage.setItem("shanhai-travel-v1", raw);
    }, LOCAL_V1);
    const requested: string[] = [];
    page.on("request", (r) => requested.push(r.url()));

    await enterViaOzid(page, who, { localDataOnDevice: true });
    // The UUID comes from the real Me answer, and its cache is the one named after it, holding the server's data.
    await expect.poll(async () => codes(await cacheOf(page, who.id))).toEqual(["330100"]);
    expect(await cloudKeys(page)).toEqual([CLOUD + who.id]);
    expect(await noOverflow(page)).toBe(true);

    // The session is the Cookie of the API host alone: host-only, HttpOnly, Secure, Lax; never in the page.
    const cookies = await context.cookies();
    const sid = cookies.find((c) => c.name === "kongken_sid");
    expect(sid, "session cookie").toBeTruthy();
    expect(sid).toMatchObject({ domain: new URL(stack.api).hostname, httpOnly: true, secure: true, sameSite: "Lax", path: "/" });
    expect(cookies.filter((c) => c.domain.replace(/^\./, "") !== new URL(stack.api).hostname).map((c) => c.name)).toEqual([]);
    const reachable = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + document.cookie);
    expect(reachable).not.toContain(sid!.value);
    expect(reachable).not.toContain(stack.password);
    expect(requested.filter((u) => u.includes(sid!.value) || u.includes(stack.password))).toEqual([]);
    // The return address carried nothing about the person.
    expect(new URL(page.url()).search).toBe("");

    // A reload stays signed in; the footprint the server holds is shown, not an empty map.
    await page.reload();
    await page.getByRole("button", { name: "稍后再说" }).click();
    await expect(accountButton(page)).toBeVisible();
    expect(await sessionUser(page)).toBe(who.id);

    // OzID's own pages open in another tab, with no opener, and leave this page as it is.
    await accountButton(page).click();
    const [popup] = await Promise.all([context.waitForEvent("page"), dialog(page).getByTestId("ozid-link-home").click()]);
    await popup.waitForLoadState();
    expect(popup.url().startsWith(stack.id)).toBe(true);
    expect(await popup.evaluate(() => window.opener)).toBeNull();
    await expect(popup.getByTestId("overview")).toBeVisible();
    await popup.close();
    await expect(dialog(page)).toBeVisible();

    // Nothing the migration or the sign-in did touched what this device held before signing in.
    expect(await page.evaluate(() => [localStorage.getItem("shanhai-travel-v1"), localStorage.getItem("shanhai-travel-v2")])).toEqual([LOCAL_V1, null]);

    // Logging out of the product clears only the account cache and the product's session record.
    await dialog(page).getByRole("button", { name: "退出登录", exact: true }).click();
    await dialog(page).getByRole("button", { name: "确认退出登录", exact: true }).click();
    await expect(signInButton(page)).toBeVisible();
    expect(await cloudKeys(page)).toEqual([]);
    expect(await readKey(page, SESSION_KEY)).toBeNull();
    expect(await page.evaluate(() => [localStorage.getItem("shanhai-travel-v1"), localStorage.getItem("shanhai-travel-v2")])).toEqual([LOCAL_V1, null]);
  });

  test("a batch whose answer was lost survives the trip to OzID and the return, and is replayed under its original mutation ID exactly once", async ({ page, context }, info) => {
    const who = acct("lost", info);
    await page.clock.install();
    const writes = recordWrites(page);
    await enterViaOzid(page, who);
    await expect.poll(async () => codes(await cacheOf(page, who.id))).toEqual(["330100"]);

    // The server applies the next batch, and the browser never hears: the batch stays pending in the cache.
    await control("/hold?method=SyncChanges&phase=drop");
    await addCity(page, "440000", "440300");
    await expect.poll(async () => ((await control("/reached?method=SyncChanges")) as { reached: boolean }).reached).toBe(true);
    await expect.poll(async () => (await cacheOf(page, who.id))?.pendingBatch?.id).toBeTruthy();
    const pending = (await cacheOf(page, who.id))!;
    const mutationId = pending.pendingBatch!.id;
    // The server already holds it, once.
    const applied = await server(who.id);
    expect(applied.visits).toEqual(["330100", "440300"]);
    expect(applied.receipts).toEqual([mutationId]);

    // The shared session ends in another tab (another application, the password, an expiry): this page keeps the cache.
    await ozidSignOut(context);
    await refocus(page);
    await accountButton(page).click();
    await expect(page.getByTestId("session-expired")).toBeVisible();
    await expect(dialog(page).locator("input, form")).toHaveCount(0);
    expect((await cacheOf(page, who.id))!.pendingBatch?.id).toBe(mutationId);

    // Back through OzID's real sign-in. The product returns, asks Me, and finds the same UUID's own cache.
    await page.getByTestId("ozid-relogin").click();
    await expect(page).toHaveURL(LOGIN);
    expect((await server(who.id)).receipts).toEqual([mutationId]);
    await submitLogin(page, who);
    await page.waitForURL(`${stack.trip}/`);
    await expect(accountButton(page)).toBeVisible();
    expect(await sessionUser(page)).toBe(who.id);
    expect(await cloudKeys(page)).toEqual([CLOUD + who.id]);
    await expect(page.locator(".local-status")).toContainText("已同步到云端");
    await expect.poll(async () => (await cacheOf(page, who.id))?.pendingBatch).toBeUndefined();
    expect(codes(await cacheOf(page, who.id))).toEqual(["330100", "440300"]);

    // Every batch ever sent carries the original mutation ID and this account's header; the server applied it once.
    expect(writes.length).toBeGreaterThan(0);
    expect(new Set(writes.map((w) => w.mutationId))).toEqual(new Set([mutationId]));
    expect(new Set(writes.map((w) => w.account))).toEqual(new Set([who.id]));
    const after = await server(who.id);
    expect(after.visits).toEqual(["330100", "440300"]);
    expect(after.receipts).toEqual([mutationId]);
    expect(after.changes).toBe(applied.changes);
    expect(await noOverflow(page)).toBe(true);
  });
});

test.describe("accounts A and B", () => {
  test("a stale tab cannot write into the other account, and A's unsent edit comes back with A", async ({ page, context }, info) => {
    const a = acct("a", info);
    const b = acct("b", info);
    await page.clock.install();
    const writes = recordWrites(page);
    await enterViaOzid(page, a);
    await expect.poll(async () => codes(await cacheOf(page, a.id))).toEqual(["330100"]);

    // A edits while nothing can be sent: the edit is only in A's cache.
    let blocked = true;
    await page.route("**/bff.tripmap.v1.FootprintService/SyncChanges", (route) => (blocked ? route.abort("connectionrefused") : route.fallback()));
    await addCity(page, "440000", "440300");
    await expect.poll(async () => (await cacheOf(page, a.id))?.pendingBatch?.id).toBeTruthy();
    const keyA = CLOUD + a.id;
    const cacheA = (await readKey(page, keyA))!;
    const mutationA = (JSON.parse(cacheA) as Cache).pendingBatch!.id;
    const serverA = await server(a.id);
    expect(serverA.visits).toEqual(["330100"]);

    // In another tab OzID switches the browser to B. This page does not know yet: it still believes it is A.
    const serverB = await server(b.id);
    await ozidSignOut(context);
    await ozidSignIn(context, b);
    // The stale page syncs now. Its batch goes out under A's header with B's Cookie: the BFF refuses it before
    // any service sees it, so B's account is not written, and A's edit stays unsent.
    blocked = false;
    await page.evaluate(() => window.dispatchEvent(new Event("online")));
    await expect.poll(() => writes.filter((w) => w.status !== undefined).length).toBeGreaterThan(0);
    const stale = writes.filter((w) => w.status !== undefined);
    expect(new Set(stale.map((w) => w.account))).toEqual(new Set([a.id]));
    expect(stale.every((w) => w.status !== 200)).toBe(true);
    expect(await server(b.id)).toEqual(serverB);
    expect((await server(a.id)).visits).toEqual(["330100"]);
    expect((await cacheOf(page, a.id))!.pendingBatch?.id).toBe(mutationA);

    // The page is shown again: Me says B, so the page switches to B's own cache; A's cache is kept for A.
    await refocus(page);
    await expect.poll(() => sessionUser(page)).toBe(b.id);
    await expect.poll(async () => codes(await cacheOf(page, b.id))).toEqual(["330100"]);
    const keptA = JSON.parse((await readKey(page, keyA))!) as Cache;
    expect(keptA.pendingBatch?.id).toBe(mutationA);
    expect(keptA.confirmed?.visits.map((v) => v.code)).toEqual(["330100"]);
    expect(codes(keptA)).toContain("440300");
    // B edits and syncs; the edit lands in B's cache and on B's account only.
    await addCity(page, "440000", "440100");
    await expect(page.locator(".local-status")).toContainText("已同步到云端");
    await expect.poll(async () => (await server(b.id)).visits).toEqual(["330100", "440100"]);
    expect((await server(a.id)).visits).toEqual(["330100"]);
    expect(codes(await cacheOf(page, b.id))).toEqual(["330100", "440100"]);
    expect(codes(await cacheOf(page, a.id))).not.toContain("440100");
    expect(((await cacheOf(page, a.id)) as Cache).pendingBatch?.id).toBe(mutationA);

    // Back to A: A's own cache is restored under A's UUID, and the unsent edit goes out under its original ID.
    await ozidSignOut(context);
    await ozidSignIn(context, a);
    await refocus(page);
    await expect.poll(() => sessionUser(page)).toBe(a.id);
    await expect(page.locator(".local-status")).toContainText("已同步到云端");
    await expect.poll(async () => (await server(a.id)).visits).toEqual(["330100", "440300"]);
    expect((await server(a.id)).receipts).toContain(mutationA);
    expect((await server(b.id)).visits).toEqual(["330100", "440100"]);
    // Each write was sent under the header of the account whose cache it came from.
    expect(writes.filter((w) => w.mutationId === mutationA).every((w) => w.account === a.id)).toBe(true);
    expect(writes.filter((w) => w.account === b.id).every((w) => w.mutationId !== mutationA)).toBe(true);
    expect(await noOverflow(page)).toBe(true);
  });
});

test.describe("a session that ends elsewhere, and a network that fails", () => {
  test("signing out in OzID keeps the unsynced cache; signing in again resumes it; export works meanwhile", async ({ page, context }, info) => {
    const who = acct("out", info);
    await page.clock.install();
    await enterViaOzid(page, who);
    await expect.poll(async () => codes(await cacheOf(page, who.id))).toEqual(["330100"]);
    let blocked = true;
    await page.route("**/bff.tripmap.v1.FootprintService/SyncChanges", (route) => (blocked ? route.abort("connectionrefused") : route.fallback()));
    await addCity(page, "440000", "440300");
    await expect.poll(async () => (await cacheOf(page, who.id))?.pendingBatch?.id).toBeTruthy();
    const cache = await readKey(page, CLOUD + who.id);

    await ozidSignOut(context);
    await refocus(page);
    await accountButton(page).click();
    const expired = page.getByTestId("session-expired");
    await expect(expired).toBeVisible();
    const download = page.waitForEvent("download");
    await expired.getByRole("button", { name: "导出账号缓存" }).click();
    expect((await download).suggestedFilename()).toContain("账号");
    expect(await readKey(page, CLOUD + who.id)).toBe(cache);
    expect(await sessionUser(page)).toBe(who.id);
    expect(await noOverflow(page)).toBe(true);
    await page.keyboard.press("Escape");

    // Signed in again in another tab; the page is shown again and resumes without a reload.
    await ozidSignIn(context, who);
    blocked = false;
    await refocus(page);
    await expect(page.locator(".local-status")).toContainText("已同步到云端");
    await expect.poll(async () => (await server(who.id)).visits).toEqual(["330100", "440300"]);
    expect(await sessionUser(page)).toBe(who.id);
  });

  test("a network failure is not a sign-out: nothing is cleared, and syncing resumes by itself", async ({ page }, info) => {
    const who = acct("net", info);
    await page.clock.install();
    await enterViaOzid(page, who);
    await expect.poll(async () => codes(await cacheOf(page, who.id))).toEqual(["330100"]);
    let down = true;
    const gate = (route: import("@playwright/test").Route) => (down ? route.abort("connectionrefused") : route.fallback());
    await page.route("**/bff.v1.AuthService/Me", gate);
    await page.route("**/bff.tripmap.v1.FootprintService/**", gate);
    await addCity(page, "440000", "440300");
    // Nothing could be read or sent, so the edit is only in the cache (not yet even a batch).
    await expect.poll(async () => codes(await cacheOf(page, who.id))).toEqual(["330100", "440300"]);
    expect((await server(who.id)).visits).toEqual(["330100"]);
    const cache = await readKey(page, CLOUD + who.id);
    await refocus(page);
    await accountButton(page).click();
    await expect(page.getByTestId("session-expired")).toHaveCount(0);
    expect(await readKey(page, CLOUD + who.id)).toBe(cache);
    expect(await sessionUser(page)).toBe(who.id);
    await page.keyboard.press("Escape");

    down = false;
    await refocus(page, 60_000);
    await expect(page.locator(".local-status")).toContainText("已同步到云端");
    await expect.poll(async () => (await server(who.id)).visits).toEqual(["330100", "440300"]);
    expect(await noOverflow(page)).toBe(true);
  });
});

test.describe("old entry points", () => {
  test("/login?next=… opens the account window, follows nothing, and the button goes to OzID without it", async ({ page }) => {
    const hosts = new Set<string>();
    page.on("request", (r) => hosts.add(new URL(r.url()).host));
    await page.goto(`${stack.trip}/login?next=${encodeURIComponent("https://evil.example.net/steal")}`);
    await expect(dialog(page).getByRole("heading", { name: "登录山海足迹" })).toBeVisible();
    expect(new URL(page.url()).pathname + new URL(page.url()).search).toBe("/");
    await dialog(page).getByTestId("ozid-sign-in").click();
    await expect(page).toHaveURL(LOGIN);
    expect([...hosts].filter((h) => h.includes("evil"))).toEqual([]);
  });
});
