import { expect, test, type Page } from "@playwright/test";
import { createFakeAccounts, installFakeAccounts } from "@kongken/account/testing";
import { fakeBackend, green, type V } from "./fake-backend";

// The account entry of the trip-map app against a fake BFF (UI only). The product has no account forms of its
// own: every sign-in, registration, password and device screen is OzID's. These specs cover what the product
// still owns: the entry, the cache that survives the trip, session loss and account switching. The real
// cross-origin return (HTTPS, sibling hosts, the real BFF and usersrv) is internal/e2e TestTripMapBrowserChain.
// Hosts are reserved example names; the stand-in below answers for OzID, nothing is contacted.

const OZID = "https://id.example.com";
const EMAIL = "traveller@example.com";
const PASSWORD = "correct horse battery";
const SESSION_KEY = "shanhai-session-v1";
const CLOUD = "shanhai-cloud-v2:";

const hangzhou = (): V => ({ code: "330100", name: "杭州市", province: "330000", color: green, date: "2026-10-01", note: "西湖" });

async function setup(
  page: Page,
  opts: { signedIn?: boolean; seed?: Parameters<typeof fakeBackend>[1]; statusRegistered?: boolean } = {},
) {
  const backend = fakeBackend(page, opts.seed ?? { visits: [] }, { shareEnabled: true });
  const accounts = createFakeAccounts({
    // The BFF's registry: the start page, and (like a deployment that registered it) the status page.
    navigation: {
      "trip-map": { origin: "https://trip.example.com", ...(opts.statusRegistered === false ? {} : { paths: ["/status"] }) },
      metro: { origin: "https://metro.example.com", paths: ["/me"] },
    },
  });
  await backend.install();
  await installFakeAccounts(page, accounts);
  // Lets a test make the footprint service answer "not signed in", as after an expired or revoked session.
  const gate = { unauthenticated: false };
  await page.route("**/api/bff.tripmap.v1.FootprintService/*", (route) =>
    gate.unauthenticated
      ? route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ code: "unauthenticated", message: "sign in required" }) })
      : route.fallback(),
  );
  // The OzID stand-in records what the product sent it; it renders nothing real.
  const ozid: string[] = [];
  await page.route(`${OZID}/**`, (route) => {
    ozid.push(route.request().url());
    return route.fulfill({ status: 200, contentType: "text/html", body: "<title>OzID stand-in</title><p>OzID stand-in</p>" });
  });
  const account = accounts.addAccount({ email: EMAIL, password: PASSWORD, displayName: "旅行者小山" });
  if (opts.signedIn) accounts.signIn(account);
  return { backend, accounts, account, gate, ozid };
}

const dialog = (page: Page) => page.getByRole("dialog");
const accountButton = (page: Page) => page.getByRole("button", { name: "账号", exact: true });
const signInButton = (page: Page) => page.getByRole("button", { name: "登录", exact: true });
const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const stored = (page: Page) => page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
const cloudKeys = (page: Page) => page.evaluate((p) => Object.keys(localStorage).filter((k) => k.startsWith(p)), CLOUD);
const readKey = (page: Page, key: string) => page.evaluate((k) => localStorage.getItem(k), key);
const FORMS = "input[type=password], input[type=email], input[autocomplete=username], input[autocomplete=current-password]";
const LOGIN_URL = `${OZID}/login?app_id=trip-map&return_path=%2F`;

/** The page asks Me again when it is shown or focused again; this is that moment (the ask is rate limited). */
async function refocus(page: Page) {
  await page.clock.fastForward(6_000);
  await page.evaluate(() => {
    window.dispatchEvent(new Event("focus"));
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

/** Saves one new city; with the footprint service refusing, it stays unsynced in the account cache. */
async function addCity(page: Page) {
  await page.getByRole("button", { name: "添加足迹" }).first().click();
  const selects = page.getByRole("dialog").locator("select");
  await selects.nth(0).selectOption("330000");
  await selects.nth(1).selectOption("330100");
  await page.getByRole("dialog").getByRole("button", { name: "保存足迹" }).click();
}

test.describe("the entry is OzID", () => {
  test("the signed-out window has one way in, a button to OzID, and no account form", async ({ page }) => {
    const { ozid } = await setup(page);
    await page.goto("/");
    await signInButton(page).click();
    await expect(dialog(page).getByRole("heading", { name: "登录山海足迹" })).toBeVisible();
    const entry = dialog(page).getByTestId("ozid-sign-in");
    await expect(entry).toBeVisible();
    // Not a form of any kind: no field, no password input, no hidden backup copy of one anywhere in the page.
    await expect(page.locator("input[type=password], input[type=email], input[autocomplete=username], input[autocomplete=current-password]")).toHaveCount(0);
    await expect(dialog(page).locator("form, input, textarea")).toHaveCount(0);
    await expect(page.getByTestId("auth-panel")).toHaveCount(0);
    await expect(page.getByText(/忘记密码|申请注册|发送验证邮件/)).toHaveCount(0);
    expect(await noOverflow(page)).toBe(true);

    await entry.click();
    await expect(page).toHaveURL(LOGIN_URL);
    // The product named itself and its start page; nothing about the person, the page or the session is in it.
    expect(ozid).toEqual([LOGIN_URL]);
  });

  test("the signed-in window keeps the product's own entries and has no account settings", async ({ page }) => {
    await setup(page, { signedIn: true });
    await page.goto("/");
    await accountButton(page).click();
    const d = dialog(page);
    await expect(d.getByRole("heading", { name: "旅行者小山" })).toBeVisible();
    await expect(d.getByText("已同步", { exact: true })).toBeVisible();
    for (const name of ["历史版本", "区域数据与兼容", "照片与附件", "好友与共同旅行", "提醒与通知", "分享页面", "退出登录"]) {
      await expect(d.getByRole("button", { name })).toBeVisible();
    }
    // No profile, password, devices or closing form, and no way to one inside the product.
    await expect(d.locator("form, input, textarea")).toHaveCount(0);
    for (const gone of ["账号设置", "修改密码", "已登录的设备", "注销账号", "保存资料", "登录邮箱"]) {
      await expect(page.getByText(gone)).toHaveCount(0);
    }
    const manage = d.getByTestId("ozid-link-home");
    await expect(manage).toHaveAttribute("href", OZID);
    await expect(manage).toHaveAttribute("target", "_blank");
    await expect(manage).toHaveAttribute("rel", /noopener/);
    await expect(manage).toHaveAttribute("rel", /noreferrer/);
    expect(await noOverflow(page)).toBe(true);
  });

  test("coming back from OzID signed in restores the account and its footprints", async ({ page }) => {
    const { accounts, account } = await setup(page);
    await page.goto("/");
    await signInButton(page).click();
    await dialog(page).getByTestId("ozid-sign-in").click();
    await expect(page).toHaveURL(LOGIN_URL);
    // OzID signs the person in (its Cookie) and returns to the start page: the product asks Me who it is.
    accounts.signIn(account);
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    await expect(dialog(page)).toHaveCount(0);
    expect(JSON.parse((await readKey(page, SESSION_KEY))!).user.id).toBe(account.id);
    // The session is the HttpOnly cookie: the page keeps no password, token or return address.
    const kept = await stored(page);
    expect(kept).not.toContain(PASSWORD);
    expect(kept).not.toMatch(/token|csrf|return_path|app_id/i);
    expect(await noOverflow(page)).toBe(true);
  });

  test("a profile edited in OzID shows up when the page is shown again", async ({ page }) => {
    const { accounts, account } = await setup(page, { signedIn: true });
    await page.clock.install();
    await page.goto("/");
    await expect(accountButton(page)).toContainText("旅");
    account.displayName = "夜行旅人";
    await refocus(page);
    await expect.poll(async () => JSON.parse((await readKey(page, SESSION_KEY)) ?? "{}").user?.displayName).toBe("夜行旅人");
    await accountButton(page).click();
    await expect(dialog(page).getByRole("heading", { name: "夜行旅人" })).toBeVisible();
    expect(accounts.state.current?.id).toBe(account.id);
  });

  test("closing the window and logging out still work, and keep this device's own footprints", async ({ page }) => {
    await setup(page, { signedIn: true });
    const local = { version: 1, visits: [], provinces: { "330000": "#4b937d" } };
    await page.addInitScript((data) => {
      if (!localStorage.getItem("shanhai-travel-v1")) localStorage.setItem("shanhai-travel-v1", JSON.stringify(data));
    }, local);
    await page.goto("/");
    await page.getByRole("button", { name: "稍后再说" }).click();
    await accountButton(page).click();
    await dialog(page).getByRole("button", { name: "退出登录", exact: true }).click();
    await dialog(page).getByRole("button", { name: "确认退出登录", exact: true }).click();
    await expect(signInButton(page)).toBeVisible();
    expect(await readKey(page, SESSION_KEY)).toBeNull();
    expect(await cloudKeys(page)).toEqual([]);
    expect(await page.evaluate(() => JSON.parse(localStorage.getItem("shanhai-travel-v1")!))).toEqual(local);
  });
});

test.describe("old entry points", () => {
  for (const [name, path] of [
    ["the mail 'log in' link", "/?signin=1"],
    ["/login", "/login"],
    ["/account", "/account"],
    ["/login with a next address", "/login?next=https%3A%2F%2Fevil.example.net%2Fsteal"],
    ["/?signin=1 with a next address", "/?signin=1&next=//evil.example.net/steal"],
  ] as const) {
    test(`${name} only opens the account window and follows nothing`, async ({ page }) => {
      const { ozid } = await setup(page);
      // Whatever the old link carried must never be requested: only the product's own host is contacted
      // (the OzID stand-in is the one other host, and only after the person clicks).
      const hosts = new Set<string>();
      page.on("request", (r) => hosts.add(new URL(r.url()).host));
      await page.goto(path);
      await expect(dialog(page).getByRole("heading", { name: "登录山海足迹" })).toBeVisible();
      // The address is cleaned up, whatever it carried.
      const url = new URL(page.url());
      expect(url.pathname).toBe("/");
      expect(url.search).toBe("");
      expect([...hosts].filter((h) => h !== "127.0.0.1:4174" && !h.startsWith("127.0.0.1"))).toEqual([]);
      expect(ozid).toEqual([]);
      await dialog(page).getByTestId("ozid-sign-in").click();
      await expect(page).toHaveURL(LOGIN_URL);
      expect([...hosts].filter((h) => h.includes("evil"))).toEqual([]);
      expect(ozid).toEqual([LOGIN_URL]);
    });
  }
});

test.describe("mailed links from before OzID", () => {
  // The product has no form for them any more: its two landing paths hand the browser, and the one-time token,
  // to OzID's page of the same name (which completes the mail or asks for a new one: apps/ozid). What is proved
  // here is the hand-over: where it leads, what it carries, and that nothing else in the address can steer it.
  const FOREIGN = /evil\.example|attacker/;

  async function mailed(page: Page, kind: "reset" | "registration") {
    const set = await setup(page);
    if (kind === "reset") set.accounts.handle("RequestPasswordReset", { email: EMAIL, appId: "trip-map", returnPath: "/" }, {});
    else set.accounts.handle("RequestRegistration", { email: "new-rider@example.com", appId: "trip-map", returnPath: "/" }, {});
    return { ...set, mail: set.accounts.lastMail(kind)! };
  }

  /**
   * What the page showed and held at the moment it left for OzID, reported by the page itself from the Navigation API's `navigate` event
   * (a held navigation cannot be inspected: the browser blocks the page's own locators while it is pending).
   */
  type Departure = { url: string; stored: string; referrer: string | null; foreignScripts: string[]; forms: number; buttons: number; link: string | null; overflow: boolean; heading: string };
  async function watchDeparture(page: Page) {
    const seen: Departure[] = [];
    page.on("console", (m) => {
      if (m.text().startsWith("DEPARTURE ")) seen.push(JSON.parse(m.text().slice("DEPARTURE ".length)));
    });
    await page.addInitScript((forms) => {
      // The Navigation API reports the move before it happens, while the page is still whole.
      (window as unknown as { navigation: EventTarget }).navigation.addEventListener("navigate", (e) => {
        if (!document.querySelector('[data-testid^="handoff-"]') || (e as unknown as { sameDocument: boolean }).sameDocument) return;
        console.log(
          "DEPARTURE " +
            JSON.stringify({
              url: location.href,
              stored: JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }),
              referrer: document.querySelector('meta[name="referrer"]')?.getAttribute("content") ?? null,
              foreignScripts: [...document.scripts].map((s) => s.src).filter((s) => s && !s.startsWith(location.origin)),
              forms: document.querySelectorAll(forms).length,
              buttons: document.querySelectorAll("button").length,
              link: document.querySelector('[data-testid="handoff-link"]')?.getAttribute("href") ?? null,
              overflow: document.documentElement.scrollWidth > window.innerWidth,
              heading: document.querySelector("h1")?.textContent ?? "",
            }),
        );
      });
    }, FORMS);
    return seen;
  }

  for (const [kind, path] of [
    ["registration", "/account/verify-email"],
    ["reset", "/account/reset-password"],
  ] as const) {
    test(`the ${kind} link clears its token, makes no request of its own and hands over to OzID's ${path}`, async ({ page }) => {
      const { accounts, mail } = await mailed(page, kind);
      expect(mail.link).toMatch(new RegExp(`^${path}#token=[\\w-]{43}$`));
      const left = await watchDeparture(page);
      const requested: string[] = [];
      page.on("request", (r) => requested.push(r.url()));
      const calls = accounts.state.calls.length;
      const res = await page.goto(mail.link, { waitUntil: "commit" });
      expect(res!.headers()["referrer-policy"]).toBe("no-referrer");
      expect(res!.headers()["cache-control"]).toBe("no-store");
      await page.waitForURL(`${OZID}${path}#token=${mail.token}`);
      // At the moment it left: no form, the token out of the address bar and out of storage, no referrer, no foreign script,
      // and the fallback link the same fixed address (the OzID page of this kind, the token in the fragment only).
      await expect.poll(() => left.length).toBe(1);
      const d = left[0]!;
      expect(new URL(d.url).hash).toBe("");
      expect(d.url).not.toContain(mail.token);
      expect(d.stored).not.toContain(mail.token);
      expect(d.referrer).toBe("no-referrer");
      expect(d.foreignScripts).toEqual([]);
      expect(d.forms).toBe(0);
      expect(d.buttons).toBe(0);
      expect(d.overflow).toBe(false);
      expect(d.link).toBe(`${OZID}${path}#token=${mail.token}`);
      // Nothing was asked of the BFF, and no request carried the token (the fragment is never sent).
      expect(accounts.state.calls.slice(calls)).toEqual([]);
      expect(requested.filter((u) => u.includes(mail.token))).toEqual([]);
    });
  }

  test("nothing in the address changes the destination: a query, other fragment parts and a foreign return are ignored", async ({ page }) => {
    const { mail } = await mailed(page, "reset");
    const requested: string[] = [];
    page.on("request", (r) => requested.push(r.url()));
    const evil = "https://evil.example/steal";
    await page.goto(
      `/account/reset-password?next=${encodeURIComponent(evil)}&return_path=//evil.example&app_id=attacker&redirect=${encodeURIComponent(evil)}#redirect=${encodeURIComponent(evil)}&token=${mail.token}&next=${encodeURIComponent(evil)}`,
    );
    await page.waitForURL(`${OZID}/account/reset-password#token=${mail.token}`);
    expect(requested.filter((u) => FOREIGN.test(new URL(u).hostname))).toEqual([]);
    expect(new URL(page.url()).search).toBe("");
  });

  test("a path that is not exactly one of the two landing paths is not a hand-over", async ({ page }) => {
    const { mail, ozid } = await mailed(page, "reset");
    for (const odd of ["/account/reset-password/extra", "/account/reset-passwords", "/account/reset-password.html", "/account/", "/account/verify-email/x/y"]) {
      await page.goto(`${odd}#token=${mail.token}`);
      await expect(page.getByTestId(/^handoff-/)).toHaveCount(0);
      expect(new URL(page.url()).origin).not.toBe(OZID);
    }
    expect(ozid).toEqual([]);
  });

  test("an unusable link hands over without a token, so OzID shows the same unusable-link page for a missing, a short, a spent or an expired token", async ({ page }) => {
    const { mail } = await mailed(page, "registration");
    for (const [from, to] of [
      ["/account/verify-email", `${OZID}/account/verify-email`],
      ["/account/verify-email#token=short", `${OZID}/account/verify-email`],
      [`/account/verify-email#token=${"a".repeat(600)}`, `${OZID}/account/verify-email`],
      [`/account/reset-password#token=${mail.token}%26x`, `${OZID}/account/reset-password`],
    ] as const) {
      await page.goto(from);
      await page.waitForURL(to);
      expect(new URL(page.url()).hash).toBe("");
    }
    // A well-formed token is carried whether it is fresh, spent or expired: the server alone knows, and says so at OzID.
    await page.goto(`/account/verify-email#token=${"b".repeat(43)}`);
    await page.waitForURL(`${OZID}/account/verify-email#token=${"b".repeat(43)}`);
  });
});

test.describe("a session lost outside this product", () => {
  test("a session that stops working keeps the cache and offers OzID sign-in and export", async ({ page }) => {
    const { accounts, gate, ozid } = await setup(page, { signedIn: true });
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    gate.unauthenticated = true;
    accounts.signOut();
    await addCity(page);
    await expect(page.locator(".local-status")).toContainText("待同步");
    await accountButton(page).click();
    const expired = page.getByTestId("session-expired");
    await expect(expired).toBeVisible();
    await expect(expired).toContainText("未同步的修改仍保留在本机");
    await expect(expired.locator("input, form")).toHaveCount(0);
    await expect(page.locator("input[type=password]")).toHaveCount(0);
    const download = page.waitForEvent("download");
    await expired.getByRole("button", { name: "导出账号缓存" }).click();
    expect((await download).suggestedFilename()).toContain("账号");
    const before = await cloudKeys(page);
    expect(before).toHaveLength(1);
    const cache = await readKey(page, before[0]);
    expect(await noOverflow(page)).toBe(true);

    await expired.getByTestId("ozid-relogin").click();
    await expect(page).toHaveURL(LOGIN_URL);
    expect(ozid).toEqual([LOGIN_URL]);
    // Leaving changed nothing in the cache: the unsynced edit, the ancestor and its mutation ID are intact
    // (read back from the product's origin; the stand-in is another origin with its own storage).
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    expect(await readKey(page, before[0])).toBe(cache);
    expect(await readKey(page, SESSION_KEY)).not.toBeNull();
  });

  test("another application's sign-out or a password change shows the same, without clearing anything", async ({ page }) => {
    const { accounts, gate } = await setup(page, { signedIn: true });
    await page.clock.install();
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    gate.unauthenticated = true;
    await addCity(page);
    await expect(page.locator(".local-status")).toContainText("待同步");
    const [key] = await cloudKeys(page);
    const cache = await readKey(page, key);
    // The shared session ends elsewhere; nothing here knows until the page is shown or focused again.
    accounts.signOut();
    await refocus(page);
    await accountButton(page).click();
    await expect(page.getByTestId("session-expired")).toBeVisible();
    expect(await readKey(page, key)).toBe(cache);
    expect(await readKey(page, SESSION_KEY)).not.toBeNull();
  });

  test("a network failure is not a sign-out and changes nothing", async ({ page }) => {
    await setup(page, { signedIn: true });
    await page.clock.install();
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    // The footprint service cannot be reached either, so the edit stays unsynced.
    await page.route("**/api/bff.tripmap.v1.FootprintService/*", (route) => route.abort("connectionrefused"));
    await addCity(page);
    await expect(page.locator(".local-status")).toContainText("待同步");
    const [key] = await cloudKeys(page);
    const cache = await readKey(page, key);
    // Me cannot be reached (offline, 5xx): neither is the answer "not signed in".
    let failing = 0;
    await page.route("**/api/bff.v1.AuthService/Me", async (route) => {
      failing++;
      await route.abort("connectionrefused");
    });
    await refocus(page);
    await expect.poll(() => failing).toBeGreaterThan(0);
    await accountButton(page).click();
    await expect(page.getByTestId("session-expired")).toHaveCount(0);
    expect(await readKey(page, key)).toBe(cache);
    expect(await readKey(page, SESSION_KEY)).not.toBeNull();
    expect(await noOverflow(page)).toBe(true);
  });

  test("signing in as the same account elsewhere resumes syncing the kept edits", async ({ page }) => {
    const { accounts, account, gate } = await setup(page, { signedIn: true });
    await page.clock.install();
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    gate.unauthenticated = true;
    accounts.signOut();
    await addCity(page);
    await expect(page.locator(".local-status")).toContainText("待同步");
    await accountButton(page).click();
    await expect(page.getByTestId("session-expired")).toBeVisible();
    await page.keyboard.press("Escape");
    // The person signs in again in another tab (OzID) and comes back to this page.
    accounts.signIn(account);
    gate.unauthenticated = false;
    await refocus(page);
    await expect(page.locator(".local-status")).toContainText("已同步到云端");
    await accountButton(page).click();
    await expect(page.getByTestId("session-expired")).toHaveCount(0);
  });

  test("the full-screen failure page leads to OzID, and coming back loads the footprints", async ({ page }) => {
    const { accounts, account, gate, ozid } = await setup(page, { signedIn: true });
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    // The session stops working and this device holds no cache for the account.
    gate.unauthenticated = true;
    accounts.signOut();
    await page.evaluate(() => {
      for (const k of Object.keys(localStorage)) if (k.startsWith("shanhai-cloud-v2:")) localStorage.removeItem(k);
    });
    await page.reload();
    const entry = page.getByTestId("gate-sign-in");
    await expect(page.getByRole("heading", { name: "登录已失效" })).toBeVisible();
    await expect(entry).toBeVisible();
    await expect(page.locator("input, form")).toHaveCount(0);
    expect(await noOverflow(page)).toBe(true);
    // The same page still offers the export-and-leave route and a retry.
    await expect(page.getByRole("button", { name: "导出原始账号缓存并退出" })).toBeVisible();
    await expect(page.getByRole("button", { name: "重试" })).toBeVisible();

    await entry.getByTestId("gate-ozid-relogin").click();
    await expect(page).toHaveURL(LOGIN_URL);
    expect(ozid).toEqual([LOGIN_URL]);
    accounts.signIn(account);
    gate.unauthenticated = false;
    await page.goto("/");
    await expect(page.getByTestId("gate-sign-in")).toHaveCount(0);
    await expect(accountButton(page)).toBeVisible();
  });
});

test.describe("switching accounts", () => {
  test("A's unsent edits are kept for A, B never writes into them, and A resumes them on return", async ({ page }) => {
    const { accounts, account: a, gate } = await setup(page, { signedIn: true });
    const b = accounts.addAccount({ email: "other@example.com", password: PASSWORD, displayName: "另一位旅人" });
    await page.clock.install();
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    gate.unauthenticated = true;
    await addCity(page);
    await expect(page.locator(".local-status")).toContainText("待同步");
    const keyA = CLOUD + a.id;
    const cacheA = await readKey(page, keyA);
    expect(cacheA).not.toBeNull();

    // OzID: A signs out, B signs in. The page is shown again and asks Me.
    gate.unauthenticated = false;
    accounts.signIn(b);
    await refocus(page);
    await expect.poll(async () => JSON.parse((await readKey(page, SESSION_KEY)) ?? "{}").user?.id).toBe(b.id);
    // A's cache is untouched (it holds unsent work, so the migration's clean-up leaves it alone).
    expect(await readKey(page, keyA)).toBe(cacheA);
    // B's own edit goes to B's cache, never into A's.
    await expect(page.locator(".local-status")).toContainText(/已同步到云端|同步/);
    await page.getByRole("button", { name: "添加足迹" }).first().click();
    const selects = page.getByRole("dialog").locator("select");
    await selects.nth(0).selectOption("440000");
    await selects.nth(1).selectOption("440300");
    await page.getByRole("dialog").getByRole("button", { name: "保存足迹" }).click();
    await expect.poll(() => readKey(page, CLOUD + b.id)).not.toBeNull();
    expect(await readKey(page, keyA)).toBe(cacheA);

    // Back to A: the kept edit is restored from A's cache under A's own id and goes out.
    accounts.signIn(a);
    await refocus(page);
    await expect.poll(async () => JSON.parse((await readKey(page, SESSION_KEY)) ?? "{}").user?.id).toBe(a.id);
    await expect(page.locator(".local-status")).toContainText("已同步到云端");
  });

  test("this device's anonymous footprints and the old-data protection are not touched by any of it", async ({ page }) => {
    const { accounts, account: a } = await setup(page, { signedIn: true });
    const b = accounts.addAccount({ email: "other@example.com", password: PASSWORD, displayName: "另一位旅人" });
    const local = JSON.stringify({ version: 1, visits: [], provinces: { "330000": "#4b937d" } });
    const original = JSON.stringify({ version: 2, visits: [{ code: "330100", name: "杭州市", province: "330000", color: "#4b937d", date: "2026-01-01", note: "local-only" }], trips: [], marks: {} });
    await page.addInitScript(([l, o]) => {
      if (!localStorage.getItem("shanhai-travel-v1")) localStorage.setItem("shanhai-travel-v1", l);
      if (!localStorage.getItem("shanhai-travel-v2")) localStorage.setItem("shanhai-travel-v2", o);
    }, [local, original]);
    await page.clock.install();
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    const before = await page.evaluate(() => [localStorage.getItem("shanhai-travel-v1"), localStorage.getItem("shanhai-travel-v2")]);
    accounts.signIn(b);
    await refocus(page);
    await expect.poll(async () => JSON.parse((await readKey(page, SESSION_KEY)) ?? "{}").user?.id).toBe(b.id);
    accounts.signIn(a);
    await refocus(page);
    await expect.poll(async () => JSON.parse((await readKey(page, SESSION_KEY)) ?? "{}").user?.id).toBe(a.id);
    expect(await page.evaluate(() => [localStorage.getItem("shanhai-travel-v1"), localStorage.getItem("shanhai-travel-v2")])).toEqual(before);
  });
});

test.describe("leaving for OzID is safe", () => {
  /** After this, a write of an account cache fails like a full or blocked store. */
  const breakStorage = (page: Page, prefix: string) =>
    page.evaluate((p) => {
      const original = Storage.prototype.setItem;
      (window as unknown as { __storage: { broken: boolean } }).__storage = { broken: true };
      Storage.prototype.setItem = function (key: string, value: string) {
        if ((window as unknown as { __storage: { broken: boolean } }).__storage.broken && key.startsWith(p)) throw new DOMException("quota", "QuotaExceededError");
        return original.call(this, key, value);
      };
    }, prefix);
  const fixStorage = (page: Page) => page.evaluate(() => ((window as unknown as { __storage: { broken: boolean } }).__storage.broken = false));

  test("an account cache that cannot be written blocks leaving, offers export, and goes on once it can", async ({ page }) => {
    const { accounts, gate, ozid } = await setup(page, { signedIn: true });
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    gate.unauthenticated = true;
    await addCity(page);
    await expect(page.locator(".local-status")).toContainText("待同步");
    await breakStorage(page, CLOUD);
    accounts.signOut();
    // A second edit cannot be saved on this device: it is refused instead of being held only in memory.
    await addCity(page);
    await expect(page.locator(".local-status")).toContainText("本机保存失败");
    await page.keyboard.press("Escape");
    await accountButton(page).click();
    await page.getByTestId("session-expired").getByTestId("ozid-relogin").click();
    const blocked = page.getByTestId("leave-blocked");
    await expect(blocked).toBeVisible();
    await expect(page).not.toHaveURL(/id\.example\.com/);
    expect(ozid).toEqual([]);
    expect(await noOverflow(page)).toBe(true);
    const download = page.waitForEvent("download");
    await blocked.getByRole("button", { name: "导出足迹备份" }).click();
    expect((await download).suggestedFilename()).toContain(".json");
    // Retrying while it still fails stays put; staying keeps the page.
    await blocked.getByRole("button", { name: "重试保存并前往" }).click();
    await expect(blocked).toBeVisible();
    await blocked.getByRole("button", { name: "留在本页" }).click();
    await expect(blocked).toHaveCount(0);
    await expect(page).not.toHaveURL(/id\.example\.com/);

    // Once the store works, the same action saves and leaves.
    await fixStorage(page);
    await accountButton(page).click();
    await page.getByTestId("session-expired").getByTestId("ozid-relogin").click();
    await expect(page).toHaveURL(LOGIN_URL);
    expect(ozid).toEqual([LOGIN_URL]);
  });

  test("local mode with a failed browser store blocks leaving as well", async ({ page }) => {
    const { ozid } = await setup(page);
    await page.goto("/");
    await breakStorage(page, "shanhai-travel-");
    await addCity(page);
    await expect(page.getByText("浏览器存储不可用")).toBeVisible();
    await signInButton(page).click();
    await dialog(page).getByTestId("ozid-sign-in").click();
    const blocked = page.getByTestId("leave-blocked");
    await expect(blocked).toBeVisible();
    expect(ozid).toEqual([]);
    await fixStorage(page);
    await blocked.getByRole("button", { name: "重试保存并前往" }).click();
    await expect(page).toHaveURL(LOGIN_URL);
    // The retry wrote the edit to the device before leaving.
    expect(ozid).toEqual([LOGIN_URL]);
  });
});

test.describe("an account closure left unfinished by an older version", () => {
  test("only the retry in OzID is offered, and a finished closure is noticed", async ({ page }) => {
    const { accounts, account } = await setup(page, { signedIn: true });
    await page.addInitScript((id) => {
      if (!localStorage.getItem("shanhai-session-v1"))
        localStorage.setItem("shanhai-session-v1", JSON.stringify({ user: { id, displayName: "旅行者小山", avatarUrl: "", deletionPending: true } }));
    }, account.id);
    await page.clock.install();
    await page.goto("/");
    await accountButton(page).click();
    const pending = dialog(page).getByTestId("deletion-pending");
    await expect(pending).toBeVisible();
    await expect(pending.getByTestId("ozid-link-closeAccount")).toHaveAttribute("href", `${OZID}/close-account`);
    await expect(dialog(page).locator("form, input")).toHaveCount(0);
    await page.keyboard.press("Escape");
    // The closure is finished in OzID: the session is gone, and the page leaves the account like a sign-out.
    accounts.signOut();
    await refocus(page);
    await expect(signInButton(page)).toBeVisible();
    expect(await readKey(page, SESSION_KEY)).toBeNull();
  });
});

// ---- where a sign-in returns to ----------------------------------------------------------------------------------

test.describe("the return path", () => {
  const STATUS_LOGIN = `${OZID}/login?app_id=trip-map&return_path=%2Fstatus`;
  const SECRET = "SECRET-capability-0123456789";

  test("the status page offers sign-in and returns to the status page, with nothing of the URL carried", async ({ page }) => {
    const { ozid } = await setup(page);
    await page.goto(`/status?share=${SECRET}&next=https://evil.example.net#${SECRET}`);
    await expect(page.getByRole("heading", { name: "服务状态" })).toBeVisible();
    // The page is anonymous: nothing of the account is read or rendered, only the way in.
    await expect(page.getByTestId("status-sign-in")).toBeVisible();
    expect(await noOverflow(page)).toBe(true);
    await page.getByTestId("status-sign-in").click();
    await expect(page).toHaveURL(STATUS_LOGIN);
    expect(ozid).toEqual([STATUS_LOGIN]);
    expect(ozid.join()).not.toContain(SECRET);
    expect(ozid.join()).not.toContain("evil");
  });

  test("a page the BFF has no rule for is never named: the sign-in returns to the start page", async ({ page }) => {
    const { ozid } = await setup(page, { statusRegistered: false });
    await page.goto("/status");
    await page.getByTestId("status-sign-in").click();
    await expect(page).toHaveURL(LOGIN_URL);
    expect(ozid).toEqual([LOGIN_URL]);
  });

  test("an unreachable BFF does not hold the sign-in back or change where it goes", async ({ page }) => {
    const { ozid } = await setup(page);
    await page.route("**/api/bff.v1.AuthService/ResolveNavigation", (route) =>
      route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ code: "unavailable", message: "down" }) }),
    );
    await page.goto("/status");
    await page.getByTestId("status-sign-in").click();
    await expect(page).toHaveURL(LOGIN_URL);
    expect(ozid).toEqual([LOGIN_URL]);
  });

  test("without a script the link still reaches OzID, at the start page", async ({ page }) => {
    await setup(page);
    await page.goto("/status");
    await expect(page.getByTestId("status-sign-in")).toHaveAttribute("href", LOGIN_URL);
  });

  test("the account window, opened at a path that is not a business page, returns to the start page without its query or fragment", async ({ page }) => {
    const { ozid } = await setup(page);
    await page.goto(`/somewhere/else?share=${SECRET}&token=${SECRET}#${SECRET}`);
    await signInButton(page).click();
    await dialog(page).getByTestId("ozid-sign-in").click();
    await expect(page).toHaveURL(LOGIN_URL);
    expect(ozid).toEqual([LOGIN_URL]);
    expect(ozid.join()).not.toContain(SECRET);
  });

  test("a share link has no account entry at all, so its capability can never be offered to OzID", async ({ page }) => {
    const { ozid } = await setup(page);
    await page.goto(`/s/${"A".repeat(43)}`);
    await expect(page.getByTestId("status-sign-in")).toHaveCount(0);
    await expect(page.getByTestId("ozid-sign-in")).toHaveCount(0);
    await expect(page.getByRole("link", { name: /OzID/ })).toHaveCount(0);
    expect(ozid).toEqual([]);
  });
});

// ---- a conflict when leaving ---------------------------------------------------------------------------------------

test.describe("leaving with a conflict open", () => {
  test("the conflict is still there on return, nothing was decided for the user, and the choice still works", async ({ page }) => {
    const { backend, account, ozid } = await setup(page, { signedIn: true, seed: { visits: [hangzhou()], trips: [{ id: "t-spring", code: "330100", date: "2026-03-01", note: "春" }] } });
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    await page.getByRole("button", { name: "旅行足迹" }).first().click();
    await page.locator(".visit-card").first().click();
    const editor = page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "保存足迹" }) });
    await editor.getByRole("button", { name: "添加另一次旅行" }).click();
    // The other device deletes the city only now, with the local edit already made: a deletion that arrived
    // earlier would simply be applied by the periodic sync (nothing local to conflict with) and the card gone.
    backend.otherDeviceDeleteVisit("330100");
    await editor.getByRole("button", { name: "保存足迹" }).click();
    const conflict = page.getByRole("dialog", { name: "多台设备修改了相同足迹" });
    await expect(conflict).toBeVisible();
    expect(backend.state.visits.size).toBe(0);
    const key = CLOUD + account.id;
    const before = JSON.parse((await readKey(page, key))!);

    // The browser goes to OzID (the conflict window cannot be closed from inside the product) and comes back.
    await page.goto(LOGIN_URL);
    expect(ozid).toEqual([LOGIN_URL]);
    await page.goto("/");

    await expect(conflict).toBeVisible();
    await expect(conflict).toContainText("杭州市");
    expect(await noOverflow(page)).toBe(true);
    // Nothing was resolved by leaving: the server is as the other device left it, and the cache holds the same
    // local edit and the same ancestor it held before.
    expect(backend.state.visits.size).toBe(0);
    const after = JSON.parse((await readKey(page, key))!);
    expect(after.saved).toEqual(before.saved);
    expect(after.confirmed).toEqual(before.confirmed);
    expect(after.pendingBatch?.id).toBe(before.pendingBatch?.id);

    await conflict.getByRole("button", { name: "冲突以本机为准" }).click();
    await expect(conflict).toHaveCount(0);
    await expect.poll(() => backend.state.trips.size).toBe(3);
    expect(backend.state.visits.has("330100")).toBe(true);
  });

  test("the choice for the other side keeps the other device's deletion, and the local edit is not applied", async ({ page }) => {
    const { backend, account } = await setup(page, { signedIn: true, seed: { visits: [hangzhou()], trips: [{ id: "t-spring", code: "330100", date: "2026-03-01", note: "春" }] } });
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    await page.getByRole("button", { name: "旅行足迹" }).first().click();
    await page.locator(".visit-card").first().click();
    const editor = page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "保存足迹" }) });
    await editor.getByRole("button", { name: "添加另一次旅行" }).click();
    // The other device deletes the city only now, with the local edit already made: a deletion that arrived
    // earlier would simply be applied by the periodic sync (nothing local to conflict with) and the card gone.
    backend.otherDeviceDeleteVisit("330100");
    await editor.getByRole("button", { name: "保存足迹" }).click();
    const conflict = page.getByRole("dialog", { name: "多台设备修改了相同足迹" });
    await expect(conflict).toBeVisible();
    await page.goto(LOGIN_URL);
    await page.goto("/");
    await expect(conflict).toBeVisible();
    await conflict.getByRole("button", { name: "冲突以云端为准" }).click();
    await expect(conflict).toHaveCount(0);
    expect(backend.state.visits.size).toBe(0);
    expect(JSON.parse((await readKey(page, CLOUD + account.id))!).pendingBatch ?? null).toBeNull();
  });
});
