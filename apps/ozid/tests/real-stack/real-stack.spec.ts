import { expect, request, test, type BrowserContext, type Page } from "@playwright/test";

/**
 * OzID in a real browser against the real BFF and usersrv (started by internal/e2e
 * TestOzidBrowserChain): HTTPS, separate sibling sub-domains for OzID, the API and the two
 * products, the BFF's host-only Cookie, exact CORS and CSRF. Nothing of the account chain is
 * mocked. The "products" are minimal stand-in pages that only ask the BFF who is signed in.
 */
type Stack = { id: string; api: string; metro: string; trip: string; evil: string; email: string; password: string; displayName: string };
const stack: Stack = JSON.parse(process.env.OZID_REAL_STACK ?? "{}");
const host = (origin: string) => new URL(origin).hostname;

test.skip(!process.env.OZID_REAL_STACK, "started by internal/e2e TestOzidBrowserChain");

/**
 * A request as a non-browser client (or a hostile page) could make it: straight to the local front
 * with the API's Host, any Origin and header set it likes, and the browser's cookies when given.
 * (Node does not resolve the reserved names; only Chromium is told to.)
 */
async function raw(path: string, init: { origin: string; headers?: Record<string, string>; method?: string; context?: BrowserContext }) {
  const api = new URL(stack.api);
  const cookies = init.context ? (await init.context.cookies()).map((c) => `${c.name}=${c.value}`).join("; ") : "";
  const client = await request.newContext({ ignoreHTTPSErrors: true });
  try {
    const res = await client.fetch(`https://127.0.0.1:${api.port}${path}`, {
      method: init.method ?? "POST",
      data: init.method === "OPTIONS" ? undefined : {},
      headers: { Host: api.host, Origin: init.origin, "Content-Type": "application/json", "Connect-Protocol-Version": "1", ...(cookies ? { Cookie: cookies } : {}), ...init.headers },
    });
    return { status: res.status(), headers: res.headers(), text: await res.text() };
  } finally {
    await client.dispose();
  }
}
const AUTH = "/api/bff.v1.AuthService/";

// OzID is served with the Content-Security-Policy of deploy/ozid-web; a violation is a failure.
const violations = new WeakMap<Page, string[]>();
test.beforeEach(({ page }) => {
  const seen: string[] = [];
  violations.set(page, seen);
  page.on("console", (m) => {
    if (/Content Security Policy|Refused to (load|connect|execute|apply)/i.test(m.text())) seen.push(m.text());
  });
});
test.afterEach(({ page }) => {
  expect(violations.get(page)).toEqual([]);
});

const panel = (page: Page) => page.getByTestId("auth-panel");

async function signIn(page: Page) {
  await panel(page).getByLabel("邮箱").fill(stack.email);
  await panel(page).getByLabel("密码", { exact: true }).fill(stack.password);
  await panel(page).getByRole("button", { name: "登录", exact: true }).click();
}

/** What a product page learned from the BFF with credentials: set by the stand-in page script. */
const me = (page: Page) => page.locator("#me");

test.describe("one account across origins", () => {
  test("sign in at OzID from a product, return to the page, and every origin sees the same identity", async ({ page, context }) => {
    const requested: string[] = [];
    page.on("request", (r) => requested.push(r.url()));

    // The product's own page, the starting point: anonymous.
    await page.goto(`${stack.metro}/city/shanghai`);
    await expect(me(page)).toHaveText(/^anonymous/);
    await page.locator("#login").click();

    // At OzID (another origin of the same site) with the context in the address.
    await expect(page).toHaveURL(new RegExp(`^${stack.id}/login\\?app_id=metro&return_path=%2Fcity%2Fshanghai$`));
    await expect(page.getByRole("heading", { name: "登录OzID" })).toBeVisible();
    await signIn(page);

    // Back on the product, at the original page, and the product learns who signed in.
    await page.waitForURL(`${stack.metro}/city/shanghai`);
    await expect(me(page)).toHaveText(`user:${stack.displayName}`);

    // The cookie belongs to the API host alone: host-only, HttpOnly, Secure, Lax.
    const cookies = await context.cookies();
    const sid = cookies.find((c) => c.name === "kongken_sid");
    expect(sid, "session cookie").toBeTruthy();
    expect(sid!.domain).toBe(host(stack.api));
    expect(sid).toMatchObject({ httpOnly: true, secure: true, sameSite: "Lax", path: "/" });
    expect(cookies.filter((c) => c.domain.replace(/^\./, "") !== host(stack.api)).map((c) => c.name)).toEqual([]);

    // The session token is nowhere a page or a URL could leak it from.
    expect(requested.filter((u) => u.includes(sid!.value))).toEqual([]);
    for (const origin of [stack.id, stack.metro]) {
      await page.goto(`${origin}/`);
      const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + document.cookie);
      expect(stored).not.toContain(sid!.value);
      expect(stored).not.toContain(stack.password);
    }

    // Another registered app, its own origin, the same Cookie: the same identity.
    await page.goto(`${stack.trip}/`);
    await expect(me(page)).toHaveText(`user:${stack.displayName}`);

    // OzID's overview shows the account (the login email only appears here).
    await page.goto(`${stack.id}/`);
    await expect(page.getByTestId("overview-email")).toHaveText(stack.email);
  });

  test("signing out at OzID ends the shared session: the products see it on their next check", async ({ page }) => {
    await page.goto(`${stack.id}/login`);
    await signIn(page);
    await expect(page.getByTestId("overview")).toBeVisible();
    await page.goto(`${stack.trip}/`);
    await expect(me(page)).toHaveText(`user:${stack.displayName}`);

    await page.goto(`${stack.id}/`);
    await page.getByRole("button", { name: "退出登录" }).click();
    await expect(page).toHaveURL(`${stack.id}/login`);
    for (const origin of [stack.trip, stack.metro]) {
      await page.goto(`${origin}/`);
      await expect(me(page)).toHaveText(/^anonymous/);
    }
  });

  test("an already signed-in browser continues straight back to the app", async ({ page }) => {
    await page.goto(`${stack.id}/login`);
    await signIn(page);
    await expect(page.getByTestId("overview")).toBeVisible();
    await page.goto(`${stack.id}/login?app_id=trip-map`);
    await expect(page.getByTestId("signed-in-here")).toBeVisible();
    await page.getByRole("button", { name: "继续" }).click();
    await page.waitForURL(`${stack.trip}/`);
    await expect(me(page)).toHaveText(`user:${stack.displayName}`);
  });
});

test.describe("password confirmation", () => {
  test("confirming the password renews the real authentication time and returns to the app", async ({ page }) => {
    await page.goto(`${stack.id}/login`);
    await signIn(page);
    await expect(page.getByTestId("overview")).toBeVisible();
    await page.goto(`${stack.metro}/me`);
    await expect(me(page)).toHaveText(`user:${stack.displayName}`);
    const before = await me(page).getAttribute("data-authenticated-at");
    expect(before).toBeTruthy();
    await page.waitForTimeout(1100); // timestamps are in seconds

    await page.goto(`${stack.id}/security?app_id=metro&return_path=%2Fme`);
    await page.getByLabel("当前密码").fill("not the password at all");
    await page.getByRole("button", { name: "确认密码" }).click();
    await expect(page.getByTestId("reauthenticate")).toContainText("当前密码不正确");
    await page.getByLabel("当前密码").fill(stack.password);
    await page.getByRole("button", { name: "确认密码" }).click();
    await page.waitForURL(`${stack.metro}/me`);
    await expect(me(page)).toHaveText(`user:${stack.displayName}`);
    const after = await me(page).getAttribute("data-authenticated-at");
    expect(new Date(after!).getTime()).toBeGreaterThan(new Date(before!).getTime());
  });

  test("without a session the confirmation page sends the user to sign in and back", async ({ page }) => {
    await page.goto(`${stack.id}/security?app_id=metro&return_path=%2Fme`);
    await expect(page).toHaveURL(new RegExp("/login\\?app_id=metro&return_path=%2Fme&reason=expired$"));
    await signIn(page);
    await page.waitForURL(`${stack.metro}/me`);
  });
});

test.describe("what the BFF refuses", () => {
  test("a return address outside the registry never navigates away", async ({ page }) => {
    const hostile: string[] = [];
    page.on("request", (r) => {
      if (host(r.url()) === host(stack.evil)) hostile.push(r.url());
    });
    const evilPath = encodeURIComponent(`//${host(stack.evil)}/x`);
    for (const query of [
      `app_id=metro&return_path=${evilPath}`,
      `app_id=metro&return_path=${encodeURIComponent(`${stack.evil}/x`)}`,
      `app_id=metro&return_path=%2Fcity%2F%252e%252e%2Fsecret`,
      `app_id=nonexistent&return_path=%2F`,
    ]) {
      await page.goto(`${stack.id}/login?${query}`);
      await expect(page.getByRole("alert").filter({ hasText: "不会跳转" })).toBeVisible();
      await signIn(page);
      await expect(page.getByTestId("overview")).toBeVisible();
      await expect(page).toHaveURL(`${stack.id}/`);
      await page.getByRole("button", { name: "退出登录" }).click();
      await expect(page).toHaveURL(`${stack.id}/login`);
    }
    expect(hostile).toEqual([]);
  });

  test("a same-site sibling that is not registered holds the Cookie but gets neither CORS nor a login", async ({ page }) => {
    await page.goto(`${stack.id}/login`);
    await signIn(page);
    await expect(page.getByTestId("overview")).toBeVisible();

    await page.goto(`${stack.evil}/`);
    // The sibling's own script asks the BFF who is signed in, with credentials: the
    // browser blocks the answer for want of an Access-Control-Allow-Origin for it.
    await expect(me(page)).toHaveText("blocked");
    await expect(page.locator("#login-attempt")).toHaveText("blocked");

    // The BFF's answer to the sibling's preflight carries no CORS permission at all.
    const res = await raw(`${AUTH}Me`, { method: "OPTIONS", origin: stack.evil, headers: { "Access-Control-Request-Method": "POST" } });
    expect(res.headers["access-control-allow-origin"]).toBeUndefined();
    expect(res.headers["access-control-allow-credentials"]).toBeUndefined();
  });

  test("a write that carries the Cookie and a valid CSRF pair, but a foreign Origin, is rejected", async ({ page, context }) => {
    await page.goto(`${stack.id}/login`);
    await signIn(page);
    await expect(page.getByTestId("overview")).toBeVisible();
    // A valid pair, taken from the browser's own cookie, with the registered Origin: accepted.
    const csrf = (await context.cookies()).find((c) => c.name === "kongken_csrf")!.value;
    const withPair = { "X-CSRF-Token": csrf };
    const foreign = await raw(`${AUTH}Logout`, { origin: stack.evil, headers: withPair, context });
    expect(foreign.status, foreign.text).toBeGreaterThanOrEqual(400);
    const missing = await raw(`${AUTH}Logout`, { origin: stack.id, context }); // right Origin, no CSRF header
    expect(missing.status, missing.text).toBeGreaterThanOrEqual(400);
    // Neither rejected request did anything; the same request from the registered Origin works.
    await page.goto(`${stack.id}/`);
    await expect(page.getByTestId("overview")).toBeVisible();
    const ok = await raw(`${AUTH}Logout`, { origin: stack.id, headers: withPair, context });
    expect(ok.status, ok.text).toBe(200);
    await page.goto(`${stack.id}/`);
    await expect(page).toHaveURL(new RegExp("/login"));
  });
});

test("the public pages are usable on a phone-sized screen", async ({ page }) => {
  await page.goto(`${stack.id}/login`);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await signIn(page);
  await expect(page.getByTestId("overview")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
