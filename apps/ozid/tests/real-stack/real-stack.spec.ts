import { expect, request, test, type Browser, type BrowserContext, type Page } from "@playwright/test";

/**
 * OzID in a real browser against the real BFF and usersrv (started by internal/e2e
 * TestOzidBrowserChain): HTTPS, separate sibling sub-domains for OzID, the API and the two
 * products, the BFF's host-only Cookie, exact CORS and CSRF. Nothing of the account chain is
 * mocked. The "products" are minimal stand-in pages that only ask the BFF who is signed in.
 */
type Stack = { id: string; api: string; metro: string; trip: string; evil: string; mailbox: string; landingHeaders: Record<string, string>; email: string; password: string; displayName: string };
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


/**
 * The mail loops, with the real outbox: mail is requested in one browser and finished in another
 * that only has the mailed link. The mail vendor is the only stand-in (the harness inbox, read
 * through the mailbox host). Every address here is a reserved example name.
 */
let counter = 0;
const uniqueEmail = (label: string) => `${label}-${Date.now().toString(36)}-${++counter}@example.com`;

async function freshBrowser(browser: Browser, track: (page: Page) => void): Promise<{ context: BrowserContext; page: Page }> {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: test.info().project.use.viewport });
  const page = await context.newPage();
  track(page);
  return { context, page };
}

/** The link of the nth mail to an address, as the mailbox host reports it. */
async function mailedLink(page: Page, to: string, count = 1) {
  await page.goto(`${stack.mailbox}/?to=${encodeURIComponent(to)}&count=${count}`);
  const { link } = JSON.parse(await page.locator("body").innerText()) as { link: string };
  return link;
}

/** Everything a page sent anywhere, to prove a mailed token never travels. */
function recordTraffic(page: Page, into: string[]) {
  page.on("request", (r) => into.push(r.url() + " " + (r.headers()["referer"] ?? "")));
}

test.describe("account mail, end to end", () => {
  test("register from a product, finish in another browser, then sign in back to the product page", async ({ page, browser }) => {
    const email = uniqueEmail("newcomer");
    const traffic: string[] = [];
    const other = await freshBrowser(browser, (p) => {
      recordTraffic(p, traffic);
      p.on("console", (m) => /Content Security Policy|Refused to (load|connect|execute|apply)/i.test(m.text()) && violations.get(page)!.push(m.text()));
    });

    // The first browser asks, from a product (the context travels in the address).
    await page.goto(`${stack.id}/login?app_id=metro&return_path=%2Fcity%2Fshanghai`);
    await page.getByRole("button", { name: "没有账号？申请注册" }).click();
    await expect(page).toHaveURL(`${stack.id}/register?app_id=metro&return_path=%2Fcity%2Fshanghai`);
    await page.getByLabel("邮箱").fill(email);
    await page.getByRole("button", { name: "发送验证邮件" }).click();
    await expect(page.getByTestId("register-sent")).toBeVisible();

    // The mail is at the central OzID origin, whichever product asked, with the token in the fragment.
    const link = await mailedLink(other.page, email);
    const url = new URL(link);
    expect(url.origin).toBe(stack.id);
    expect(url.pathname).toBe("/account/verify-email");
    expect(url.search).toBe("");
    const token = new URLSearchParams(url.hash.slice(1)).get("token")!;
    expect(token).toHaveLength(43);

    // Another browser (no session, no storage of the first) opens it: the page that serves the
    // link carries the headers of the image's landing location.
    const res = await other.page.goto(link);
    const headers = res!.headers();
    expect(headers["cache-control"]).toBe("no-store");
    expect(headers["referrer-policy"]).toBe("no-referrer");
    expect(headers["content-security-policy"]).toContain("script-src 'self'");
    for (const [name, value] of Object.entries(stack.landingHeaders)) expect(headers[name.toLowerCase()], name).toBe(value);
    await expect(other.page.getByTestId("landing-verify-email")).toBeVisible();
    // The token left the address bar and sits nowhere in the browser's storage.
    expect(other.page.url()).toBe(`${stack.id}/account/verify-email`);
    const stored = await other.page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + document.cookie);
    expect(stored).not.toContain(token);
    await expect(other.page.locator('meta[name="referrer"]')).toHaveAttribute("content", "no-referrer");

    await other.page.getByLabel("昵称（可选）").fill("新人");
    await other.page.getByLabel("密码", { exact: true }).fill(stack.password);
    await other.page.getByLabel("再次输入密码").fill(stack.password);
    await other.page.getByRole("button", { name: "完成注册" }).click();
    await expect(other.page.getByTestId("landing-done")).toContainText("账号已经创建");
    // Completing signs nobody in.
    expect((await other.context.cookies()).filter((c) => c.name === "kongken_sid")).toEqual([]);

    // On to sign-in with the bound context, and back to the product page it came from.
    await other.page.getByRole("link", { name: "去登录" }).click();
    await expect(other.page).toHaveURL(`${stack.id}/login?app_id=metro&return_path=%2Fcity%2Fshanghai`);
    await other.page.getByLabel("邮箱").fill(email);
    await other.page.getByLabel("密码", { exact: true }).fill(stack.password);
    await other.page.getByRole("button", { name: "登录", exact: true }).click();
    await other.page.waitForURL(`${stack.metro}/city/shanghai`);
    await expect(other.page.locator("#me")).toHaveText("user:新人");

    // The token appeared in no request URL and no Referer, whoever sent it.
    expect(traffic.filter((line) => line.includes(token))).toEqual([]);
    // The link is single-use.
    await other.page.goto(link);
    await other.page.getByLabel("密码", { exact: true }).fill(stack.password);
    await other.page.getByLabel("再次输入密码").fill(stack.password);
    await other.page.getByRole("button", { name: "完成注册" }).click();
    await expect(other.page.getByTestId("link-invalid")).toBeVisible();
    await other.context.close();
  });

  test("reset a password in another browser: every session ends, nothing signs in, the new password works", async ({ page, browser }) => {
    const email = uniqueEmail("forgetful");
    const next = "a much better password";
    const traffic: string[] = [];
    const other = await freshBrowser(browser, (p) => recordTraffic(p, traffic));
    const setup = await freshBrowser(browser, () => {});

    // An account to reset (registered through the real mail path).
    await setup.page.goto(`${stack.id}/register`);
    await setup.page.getByLabel("邮箱").fill(email);
    await setup.page.getByRole("button", { name: "发送验证邮件" }).click();
    await expect(setup.page.getByTestId("register-sent")).toBeVisible();
    await setup.page.goto(await mailedLink(other.page, email));
    await setup.page.getByLabel("密码", { exact: true }).fill(stack.password);
    await setup.page.getByLabel("再次输入密码").fill(stack.password);
    await setup.page.getByRole("button", { name: "完成注册" }).click();
    await expect(setup.page.getByTestId("landing-done")).toBeVisible();

    // Signed in here, then a reset is asked for from a product.
    await page.goto(`${stack.id}/login`);
    await page.getByLabel("邮箱").fill(email);
    await page.getByLabel("密码", { exact: true }).fill(stack.password);
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page.getByTestId("overview")).toBeVisible();
    await setup.page.goto(`${stack.id}/forgot-password?app_id=trip-map`);
    await setup.page.getByLabel("邮箱").fill(email);
    await setup.page.getByRole("button", { name: "发送重置邮件" }).click();
    await expect(setup.page.getByTestId("forgot-sent")).toBeVisible();

    const link = await mailedLink(other.page, email, 2);
    expect(new URL(link).pathname).toBe("/account/reset-password");
    const res = await other.page.goto(link);
    expect(res!.headers()["cache-control"]).toBe("no-store");
    expect(res!.headers()["referrer-policy"]).toBe("no-referrer");
    await other.page.getByLabel("新密码", { exact: true }).fill(next);
    await other.page.getByLabel("再次输入新密码").fill(next);
    await other.page.getByRole("button", { name: "重置密码" }).click();
    await expect(other.page.getByTestId("landing-done")).toContainText("密码已重置");
    expect((await other.context.cookies()).filter((c) => c.name === "kongken_sid")).toEqual([]);

    // The session of the browser that was signed in is gone.
    await page.goto(`${stack.id}/`);
    await expect(page).toHaveURL(new RegExp("/login"));
    // The old password no longer works; the new one does, and goes back to the asking product.
    await other.page.getByRole("link", { name: "去登录" }).click();
    await expect(other.page).toHaveURL(`${stack.id}/login?app_id=trip-map&return_path=%2F`);
    await other.page.getByLabel("邮箱").fill(email);
    await other.page.getByLabel("密码", { exact: true }).fill(stack.password);
    await other.page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(other.page.getByRole("alert")).toBeVisible();
    await other.page.getByLabel("密码", { exact: true }).fill(next);
    await other.page.getByRole("button", { name: "登录", exact: true }).click();
    await other.page.waitForURL(`${stack.trip}/`);
    const token = new URL(link).hash.slice(1).split("=")[1];
    expect(traffic.filter((line) => line.includes(token))).toEqual([]);
    await other.context.close();
    await setup.context.close();
  });

  test("both landing pages are served without cache or Referer and with same-origin scripts only", async ({ page }) => {
    for (const path of ["/account/verify-email", "/account/reset-password"]) {
      const res = await page.goto(`${stack.id}${path}#token=${"a".repeat(43)}`);
      const headers = res!.headers();
      for (const [name, value] of Object.entries(stack.landingHeaders)) expect(headers[name.toLowerCase()], `${path} ${name}`).toBe(value);
      expect(headers["cache-control"]).toBe("no-store");
      expect(headers["referrer-policy"]).toBe("no-referrer");
      expect(headers["content-security-policy"]).toMatch(/script-src 'self'(;|$)/);
      expect(page.url()).toBe(`${stack.id}${path}`);
    }
  });
});

/**
 * Profile, password and devices through the real chain and real Chromium profiles: each browser
 * context is a separate device with its own Cookie jar, so every sign-in is a separate BFF session.
 */
test.describe("profile, password and devices, end to end", () => {
  const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

  /** An account registered through the real mail path; the returned address signs in with stack.password. */
  async function newAccount(browser: Browser, label: string, display: string) {
    const email = uniqueEmail(label);
    const asker = await freshBrowser(browser, () => {});
    const reader = await freshBrowser(browser, () => {});
    await asker.page.goto(`${stack.id}/register`);
    await asker.page.getByLabel("邮箱").fill(email);
    await asker.page.getByRole("button", { name: "发送验证邮件" }).click();
    await expect(asker.page.getByTestId("register-sent")).toBeVisible();
    await asker.page.goto(await mailedLink(reader.page, email));
    await asker.page.getByLabel("昵称（可选）").fill(display);
    await asker.page.getByLabel("密码", { exact: true }).fill(stack.password);
    await asker.page.getByLabel("再次输入密码").fill(stack.password);
    await asker.page.getByRole("button", { name: "完成注册" }).click();
    await expect(asker.page.getByTestId("landing-done")).toBeVisible();
    await asker.context.close();
    await reader.context.close();
    return email;
  }

  /** A new device: its own context, signed in at OzID. */
  async function device(browser: Browser, email: string, password = stack.password) {
    const d = await freshBrowser(browser, () => {});
    await d.page.goto(`${stack.id}/login`);
    await d.page.getByLabel("邮箱").fill(email);
    await d.page.getByLabel("密码", { exact: true }).fill(password);
    await d.page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(d.page.getByTestId("overview")).toBeVisible();
    return d;
  }

  /** What a product on another origin learns from the shared Cookie of this device. */
  async function productSees(d: { page: Page }, origin: string) {
    await d.page.goto(`${origin}/`);
    return (await d.page.locator("#me").innerText()).trim();
  }

  const stillIn = async (d: { page: Page }) => {
    await d.page.goto(`${stack.id}/`);
    await d.page.waitForLoadState("networkidle");
    return !new URL(d.page.url()).pathname.startsWith("/login");
  };

  test("edit the profile, sign out other devices, sign out this browser, change the password", async ({ browser }) => {
    const email = await newAccount(browser, "devices", "起始昵称");
    const a = await device(browser, email);
    const b = await device(browser, email);
    const c = await device(browser, email);

    // Profile: the email is shown and cannot be edited; the nickname is shared with every device and product.
    await a.page.goto(`${stack.id}/profile`);
    const form = a.page.getByTestId("profile-form");
    await expect(form.getByLabel("登录邮箱")).toHaveValue(email);
    await expect(form.getByLabel("登录邮箱")).toHaveAttribute("readonly", "");
    await form.getByLabel("昵称").fill("改过的昵称");
    await form.getByRole("button", { name: "保存资料" }).click();
    await expect(form.getByRole("status")).toContainText("资料已保存");
    await expect(a.page.getByTestId("header-name")).toHaveText("改过的昵称");
    expect(await productSees(a, stack.metro)).toBe("user:改过的昵称");
    expect(await productSees(b, stack.trip)).toBe("user:改过的昵称");
    expect(await noOverflow(a.page)).toBe(true);

    // Devices: three real sessions, only real metadata.
    await a.page.goto(`${stack.id}/sessions`);
    const list = a.page.getByTestId("sessions");
    await expect(list.getByText("当前设备")).toHaveCount(1);
    await expect(list.locator("li").filter({ hasText: "其他设备" })).toHaveCount(2);
    await expect(list).toContainText("有效期至");
    expect(await noOverflow(a.page)).toBe(true);

    // One specific device: exactly one of the other two is out, this one stays.
    await list.getByRole("button", { name: /退出其他设备（最近使用/ }).first().click();
    await expect(list.locator("li").filter({ hasText: "其他设备" })).toHaveCount(1);
    const survivors = [await stillIn(b), await stillIn(c)];
    expect(survivors.filter(Boolean)).toHaveLength(1);

    // Sign out other devices: the rest go in one call, this device stays, a later sign-in is not affected.
    await a.page.goto(`${stack.id}/sessions`);
    const area = a.page.getByTestId("sign-out-others");
    await area.getByRole("button", { name: "退出其他 1 台设备" }).click();
    await expect(a.page.getByTestId("sessions").getByRole("status")).toContainText("已退出其他 1 台设备，本设备保持登录");
    expect(await stillIn(b)).toBe(false);
    expect(await stillIn(c)).toBe(false);
    expect(await productSees(b, stack.metro)).toMatch(/^anonymous/);
    expect(await productSees(a, stack.metro)).toBe("user:改过的昵称");
    const d = await device(browser, email);
    expect(await stillIn(d)).toBe(true);
    expect(await stillIn(a)).toBe(true);

    // Sign out this browser: every product on this device sees it; the other device keeps its login;
    // OzID leaves what the origin's own storage holds alone.
    await a.page.goto(`${stack.id}/sessions`);
    await a.page.evaluate(() => localStorage.setItem("kongken.sentinel", "keep"));
    const here = a.page.getByTestId("sign-out-here");
    await expect(here).toContainText("不会清除各应用保存在这个浏览器里的数据");
    await here.getByRole("button", { name: "退出这个浏览器的登录" }).click();
    await expect(a.page).toHaveURL(`${stack.id}/login`);
    expect(await a.page.evaluate(() => localStorage.getItem("kongken.sentinel"))).toBe("keep");
    expect(await productSees(a, stack.trip)).toMatch(/^anonymous/);
    expect(await productSees(a, stack.metro)).toMatch(/^anonymous/);
    expect(await stillIn(d)).toBe(true);

    // Password: a wrong current one changes nothing; the right one ends every session, this device included.
    const e = await device(browser, email);
    const next = "another long password 2";
    await e.page.goto(`${stack.id}/security`);
    expect(await noOverflow(e.page)).toBe(true);
    const pw = e.page.getByTestId("password-form");
    await pw.getByLabel("当前密码").fill("not the password at all");
    await pw.getByLabel("新密码", { exact: true }).fill(next);
    await pw.getByLabel("再次输入新密码").fill(next);
    await pw.getByRole("button", { name: "修改密码" }).click();
    await expect(pw.getByText("当前密码不正确")).toBeVisible();
    expect(await stillIn(d)).toBe(true);
    await e.page.goto(`${stack.id}/security`);
    await pw.getByLabel("当前密码").fill(stack.password);
    await pw.getByLabel("新密码", { exact: true }).fill(next);
    await pw.getByLabel("再次输入新密码").fill(next);
    await pw.getByRole("button", { name: "修改密码" }).click();
    await expect(e.page).toHaveURL(`${stack.id}/login?reason=password-changed`);
    await expect(e.page.getByText("所有设备上的登录都已退出")).toBeVisible();
    expect(await stillIn(d)).toBe(false);
    expect(await productSees(e, stack.metro)).toMatch(/^anonymous/);
    // Old password refused, new one works.
    await e.page.goto(`${stack.id}/login`);
    await e.page.getByLabel("邮箱").fill(email);
    await e.page.getByLabel("密码", { exact: true }).fill(stack.password);
    await e.page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(e.page.getByRole("alert")).toBeVisible();
    await e.page.getByLabel("密码", { exact: true }).fill(next);
    await e.page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(e.page.getByTestId("overview")).toBeVisible();

    for (const x of [a, b, c, d, e]) await x.context.close();
  });

  test("device operations refuse a foreign Origin and a missing CSRF token, and end nothing", async ({ browser }) => {
    const email = await newAccount(browser, "guarded", "守卫");
    const a = await device(browser, email);
    const b = await device(browser, email);
    const csrf = (await a.context.cookies()).find((c) => c.name === "kongken_csrf")!.value;
    const foreign = await raw(`${AUTH}RevokeOtherSessions`, { origin: stack.evil, headers: { "X-CSRF-Token": csrf }, context: a.context });
    expect(foreign.status, foreign.text).toBeGreaterThanOrEqual(400);
    const noCsrf = await raw(`${AUTH}RevokeOtherSessions`, { origin: stack.id, context: a.context });
    expect(noCsrf.status, noCsrf.text).toBeGreaterThanOrEqual(400);
    const anonymous = await raw(`${AUTH}RevokeOtherSessions`, { origin: stack.id, headers: { "X-CSRF-Token": "x" } });
    expect(anonymous.status).toBeGreaterThanOrEqual(400);
    expect(await stillIn(b)).toBe(true);
    // The registered Origin with the pair works, and ends exactly the other device.
    const ok = await raw(`${AUTH}RevokeOtherSessions`, { origin: stack.id, headers: { "X-CSRF-Token": csrf }, context: a.context });
    expect(ok.status, ok.text).toBe(200);
    expect(await stillIn(b)).toBe(false);
    expect(await stillIn(a)).toBe(true);
    await a.context.close();
    await b.context.close();
  });
});
