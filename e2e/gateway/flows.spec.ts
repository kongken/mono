import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { acct, failOnPolicyViolations, loginAt, mailedLink, noOverflow, panel, stack, submitLogin, tokenOf, uniqueEmail } from "./support";

/**
 * The products and the user center in a real browser through the real gateway (Caddy with the shipped
 * example, Nginx with the images' templates) on this scenario's own reserved site: sign-in at OzID and
 * the return, the shared session, registration by mail (the page of the mail link in another browser,
 * and the old-style mail that lands on a product during the transition), and what an API host change
 * means for an old session. See internal/e2e TestGatewayDomainSets for what stands behind the gateway.
 */
failOnPolicyViolations();

const header = (page: Page) => page.getByRole("banner");
const metroSignIn = (page: Page) => header(page).getByRole("button", { name: "登录", exact: true });
const line = (page: Page, code: string) => page.getByTestId(`line-${code}`);
const mark = (page: Page, code: string) => line(page, code).getByRole("button", { name: "标记坐过" });
const marked = (page: Page, code: string) => line(page, code).getByRole("button", { name: "编辑足迹", exact: true });
const tripAccount = (page: Page) => page.getByRole("button", { name: "账号", exact: true });
const tripSignIn = (page: Page) => page.getByRole("button", { name: "登录", exact: true });
const tripDialog = (page: Page) => page.getByRole("dialog");

async function metroIn(page: Page, who: { email: string; displayName: string }, from = "/cities/shanghai") {
  await page.goto(`${stack.metro}${from}`);
  await metroSignIn(page).click();
  await expect(page).toHaveURL(loginAt("metro", from));
  await submitLogin(page, who);
  await page.waitForURL(`${stack.metro}${from}`);
  await expect(header(page).getByText(who.displayName)).toBeVisible();
}

async function tripIn(page: Page, who: { email: string }) {
  await page.goto(`${stack.trip}/`);
  await tripSignIn(page).click();
  await tripDialog(page).getByTestId("ozid-sign-in").click();
  await expect(page).toHaveURL(loginAt("trip-map", "/"));
  await submitLogin(page, who);
  await page.waitForURL(`${stack.trip}/`);
  await expect(tripAccount(page)).toBeVisible();
}

async function ozidSignOut(context: BrowserContext) {
  const tab = await context.newPage();
  await tab.goto(`${stack.id}/sessions`);
  await tab.getByTestId("sign-out-here").getByRole("button", { name: "退出这个浏览器的登录" }).click();
  await expect(tab).toHaveURL(`${stack.id}/login`);
  await tab.close();
}

test.describe("sign-in at the user center and the return", () => {
  test("metro: an anonymous mark goes to OzID, the return restores the page, and the mark is saved through the gateway", async ({ page, context }) => {
    const who = acct("rider");
    await page.goto(`${stack.metro}/cities/shanghai`);
    await expect(line(page, "sh-1")).toBeVisible();
    await mark(page, "sh-1").click();
    await expect(page).toHaveURL(loginAt("metro", "/cities/shanghai"));
    expect(await noOverflow(page)).toBe(true);
    await submitLogin(page, who);
    await page.waitForURL(`${stack.metro}/cities/shanghai`);
    await expect(header(page).getByText(who.displayName)).toBeVisible();

    // The session is the Cookie of the API host alone: host-only, HttpOnly, Secure, Lax.
    const sid = (await context.cookies()).find((c) => c.name === "kongken_sid");
    expect(sid).toMatchObject({ domain: new URL(stack.api).hostname, httpOnly: true, secure: true, sameSite: "Lax", path: "/" });
    expect((await context.cookies()).filter((c) => c.domain.replace(/^\./, "") !== new URL(stack.api).hostname)).toEqual([]);

    await mark(page, "sh-1").click();
    await expect(marked(page, "sh-1")).toBeVisible();
    await page.reload();
    await expect(marked(page, "sh-1")).toBeVisible();

    // Another device (no cookie, no storage) of the same account finds the saved mark.
    const other = await page.context().browser()!.newContext({ viewport: test.info().project.use.viewport });
    const tab = await other.newPage();
    await metroIn(tab, who);
    await expect(marked(tab, "sh-1")).toBeVisible();
    await other.close();
  });

  test("trip-map: sign in from the product, return, and a footprint is kept on the server for another device", async ({ page, browser }) => {
    const who = acct("trip");
    await tripIn(page, who);
    await page.getByRole("button", { name: "添加足迹" }).first().click();
    const selects = tripDialog(page).locator("select");
    await selects.nth(0).selectOption("440000");
    await selects.nth(1).selectOption("440300");
    await tripDialog(page).getByRole("button", { name: "保存足迹" }).click();
    expect(await noOverflow(page)).toBe(true);

    // Another browser of the same account reads it back from the server.
    const other = await browser.newContext({ viewport: test.info().project.use.viewport });
    const tab = await other.newPage();
    await tripIn(tab, who);
    await expect
      .poll(async () => tab.evaluate(() => Object.entries(localStorage).filter(([k]) => k.startsWith("shanhai-cloud-v2:")).map(([, v]) => JSON.stringify(v)).join()))
      .toContain("440300");
    await other.close();
  });

  test("one sign-in serves both products, and signing out at the user center ends both", async ({ page, context }) => {
    const who = acct("plain");
    await page.goto(`${stack.id}/login`);
    await submitLogin(page, who);
    await expect(page.getByTestId("overview")).toBeVisible();

    const metro = await context.newPage();
    await metro.goto(`${stack.metro}/cities/shanghai`);
    await expect(header(metro).getByText(who.displayName)).toBeVisible();
    const trip = await context.newPage();
    await trip.goto(`${stack.trip}/`);
    await expect(tripAccount(trip)).toBeVisible();

    await ozidSignOut(context);
    await metro.reload();
    await expect(metroSignIn(metro)).toBeVisible();
    await trip.reload();
    // The product keeps the account's cache and says the shared session ended (it does not pretend to be signed in).
    await tripAccount(trip).click();
    await expect(tripDialog(trip).getByTestId("session-expired")).toBeVisible();
  });
});

test.describe("account mail", () => {
  test("the mail leads to the user center from any product; finish in another browser, then sign in back to the product page", async ({ page, browser }) => {
    const email = uniqueEmail("newcomer");
    await page.goto(loginAt("metro", "/cities/shanghai"));
    await page.getByRole("button", { name: "没有账号？申请注册" }).click();
    await expect(page).toHaveURL(`${stack.id}/register?app_id=metro&return_path=%2Fcities%2Fshanghai`);
    await page.getByLabel("邮箱").fill(email);
    await page.getByRole("button", { name: "发送验证邮件" }).click();
    await expect(page.getByTestId("register-sent")).toBeVisible();

    const link = await mailedLink(email);
    const url = new URL(link);
    expect(url.origin).toBe(stack.id); // the central user center, whichever product asked
    expect(url.pathname).toBe("/account/verify-email");
    expect(url.search).toBe("");
    const token = tokenOf(link);
    expect(token).toHaveLength(43);

    const other = await browser.newContext({ viewport: test.info().project.use.viewport });
    const tab = await other.newPage();
    const traffic: string[] = [];
    tab.on("request", (r) => traffic.push(r.url() + " " + (r.headers()["referer"] ?? "")));
    const res = await tab.goto(link);
    // The page that serves the link carries what the image's landing location gives it, through the gateway.
    const headers = res!.headers();
    for (const [name, value] of Object.entries(stack.landingHeaders)) expect(headers[name.toLowerCase()], name).toBe(value);
    expect(headers["server"]).toBeUndefined();
    await expect(tab.getByTestId("landing-verify-email")).toBeVisible();
    expect(tab.url()).toBe(`${stack.id}/account/verify-email`);
    await tab.getByLabel("昵称（可选）").fill("新人");
    await tab.getByLabel("密码", { exact: true }).fill(stack.password);
    await tab.getByLabel("再次输入密码").fill(stack.password);
    await tab.getByRole("button", { name: "完成注册" }).click();
    await expect(tab.getByTestId("landing-done")).toContainText("账号已经创建");
    expect((await other.cookies()).filter((c) => c.name === "kongken_sid")).toEqual([]);

    await tab.getByRole("link", { name: "去登录" }).click();
    await expect(tab).toHaveURL(loginAt("metro", "/cities/shanghai"));
    await panel(tab).getByLabel("邮箱").fill(email);
    await panel(tab).getByLabel("密码", { exact: true }).fill(stack.password);
    await panel(tab).getByRole("button", { name: "登录", exact: true }).click();
    await tab.waitForURL(`${stack.metro}/cities/shanghai`);
    await expect(header(tab).getByText("新人")).toBeVisible();
    expect(traffic.filter((l) => l.includes(token))).toEqual([]);
    await other.close();
  });

  test("an old mail that still points at a product is handed over to the user center, which finishes it; a spent link is only unusable", async ({ page, browser }) => {
    const email = uniqueEmail("oldmail");
    await page.goto(`${stack.id}/register`);
    await page.getByLabel("邮箱").fill(email);
    await page.getByRole("button", { name: "发送验证邮件" }).click();
    await expect(page.getByTestId("register-sent")).toBeVisible();
    const token = tokenOf(await mailedLink(email));

    // The same token as an old-style link: the product's own landing page, behind the gateway's landing rule.
    for (const [label, origin] of [["metro", stack.metro], ["trip-map", stack.trip]] as const) {
      const other = await browser.newContext({ viewport: test.info().project.use.viewport });
      const tab = await other.newPage();
      const res = await tab.goto(`${origin}/account/verify-email`, { waitUntil: "commit" });
      const headers = res!.headers();
      expect(headers["referrer-policy"], label).toBe("no-referrer");
      expect(headers["cache-control"], label).toBe("no-store");
      expect(headers["content-security-policy"], label).toContain("script-src 'self'");
      await other.close();
    }

    const other = await browser.newContext({ viewport: test.info().project.use.viewport });
    const tab = await other.newPage();
    const traffic: { url: string; origin: string | undefined; referer: string | undefined }[] = [];
    tab.on("request", (r) => traffic.push({ url: r.url(), origin: r.headers()["origin"], referer: r.headers()["referer"] }));
    await tab.goto(`${stack.metro}/account/verify-email#token=${token}`, { waitUntil: "commit" });
    // The product does not finish the mail: it leaves for the user center's page of the same name, token in the
    // fragment, and the user center completes the registration (its page clears the fragment itself).
    await tab.waitForURL(`${stack.id}/account/verify-email`);
    await expect(tab.getByTestId("landing-verify-email")).toBeVisible();
    await expect(tab.getByLabel("密码", { exact: true })).toBeVisible();
    // Nothing the product's page sent carried the token, called the API or named a different destination.
    expect(traffic.filter((t) => t.url.includes(token) || (t.referer ?? "").includes(token))).toEqual([]);
    expect(traffic.filter((t) => t.origin === stack.metro && !t.url.startsWith(`${stack.metro}/`))).toEqual([]);
    await tab.getByLabel("密码", { exact: true }).fill(stack.password);
    await tab.getByLabel("再次输入密码").fill(stack.password);
    await tab.getByRole("button", { name: "完成注册" }).click();
    await expect(tab.getByTestId("landing-done")).toContainText("账号已经创建");
    await tab.getByRole("link", { name: "去登录" }).click();
    await expect(tab).toHaveURL(new RegExp(`^${stack.id}/login`));
    await panel(tab).getByLabel("邮箱").fill(email);
    await panel(tab).getByLabel("密码", { exact: true }).fill(stack.password);
    await panel(tab).getByRole("button", { name: "登录", exact: true }).click();
    await expect(tab).toHaveURL(new RegExp(`^(${stack.id}/|${stack.metro}/)`));
    await other.close();

    // The same link again, from the other product: handed over the same way, and now it is only unusable.
    const again = await browser.newContext({ viewport: test.info().project.use.viewport });
    const page2 = await again.newPage();
    await page2.goto(`${stack.trip}/account/verify-email#token=${token}`, { waitUntil: "commit" });
    await page2.waitForURL(`${stack.id}/account/verify-email`);
    await page2.getByLabel("密码", { exact: true }).fill(stack.password);
    await page2.getByLabel("再次输入密码").fill(stack.password);
    await page2.getByRole("button", { name: "完成注册" }).click();
    await expect(page2.getByTestId("link-invalid")).toBeVisible();
    await again.close();
  });
});

test.describe("a change of domains", () => {
  // The first set has no previous set, so there it only proves that a browser with nothing is anonymous; from the
  // second set on, the browser state the first set left (its session Cookie, for its own API host) is loaded.
  test("a session of another set's API host is not valid here: signing in again is required", async ({ browser }, info) => {
    const context = await browser.newContext({ ...(stack.stateIn ? { storageState: stack.stateIn } : {}), viewport: info.project.use.viewport });
    const page = await context.newPage();
    const cookies: string[] = [];
    page.on("request", (r) => r.url().startsWith(stack.api) && cookies.push(r.headers()["cookie"] ?? ""));
    await page.goto(`${stack.id}/login`);
    await expect(panel(page)).toBeVisible(); // not signed in on these names
    expect(cookies.length).toBeGreaterThan(0);
    expect(cookies.filter((c) => c.includes("kongken_sid"))).toEqual([]);
    if (stack.stateIn) expect((await context.cookies()).some((c) => c.name === "kongken_sid")).toBe(true); // still in the jar, for the old host
    await page.goto(`${stack.metro}/cities/shanghai`);
    await expect(metroSignIn(page)).toBeVisible();
    await context.close();
  });

  test("a session of this set is written down for the next set's run", async ({ page, context }, info) => {
    const who = acct("out");
    await page.goto(`${stack.id}/login`);
    await submitLogin(page, who);
    await expect(page.getByTestId("overview")).toBeVisible();
    if (info.project.name === "inside-desktop") await context.storageState({ path: stack.stateOut });
  });
});
