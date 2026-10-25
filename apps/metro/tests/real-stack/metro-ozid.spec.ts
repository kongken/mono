import { expect, request, test, type BrowserContext, type Page, type Request } from "@playwright/test";

/**
 * The metro product and OzID in a real browser against the real BFF, usersrv and metro (started by
 * internal/e2e TestMetroBrowserChain): HTTPS, sibling sub-domains for the product, OzID and the API, the BFF's
 * host-only Cookie, exact CORS, the navigation registry, CSRF and the X-Kongken-User guard. Nothing of the
 * account chain or the rider service is mocked; only the network is interfered with, and only where a scenario
 * says so.
 *
 * Every account is dedicated to one scenario of one Playwright project and was seeded with one ride (line
 * sh-1) on the server, so an account restored through the shared session is told apart from an empty one.
 * Hosts are reserved test names; the password is the harness's throwaway one.
 */
type Account = { id: string; email: string; displayName: string };
type Stack = { id: string; api: string; metro: string; control: string; password: string; accounts: Record<string, Account> };
const stack: Stack = JSON.parse(process.env.METRO_REAL_STACK ?? "{}");

test.skip(!process.env.METRO_REAL_STACK, "started by internal/e2e TestMetroBrowserChain");

const loginAt = (returnPath: string) => `${stack.id}/login?app_id=metro&return_path=${encodeURIComponent(returnPath)}`;
const acct = (kind: string, info: { project: { name: string } }): Account => stack.accounts[`mt-${kind}-${info.project.name}`];

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
/** The lines marked for a seeded account on the server. */
const rides = async (user: string): Promise<string[]> => ((await control(`/server?user=${user}`)) as { rides: string[] }).rides;

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

const header = (page: Page) => page.getByRole("banner");
const signIn = (page: Page) => header(page).getByRole("button", { name: "登录", exact: true });
const line = (page: Page, code: string) => page.getByTestId(`line-${code}`);
const mark = (page: Page, code: string) => line(page, code).getByRole("button", { name: "标记坐过" });
const marked = (page: Page, code: string) => line(page, code).getByRole("button", { name: "坐过", exact: true });
const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

/** The page is shown or focused again: it asks Me once more (the ask is rate limited, so time moves on first). */
async function refocus(page: Page, ms = 6_000) {
  await page.clock.fastForward(ms);
  await page.evaluate(() => {
    window.dispatchEvent(new Event("focus"));
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

const panel = (page: Page) => page.getByTestId("auth-panel");
async function submitLogin(page: Page, who: Account) {
  await panel(page).getByLabel("邮箱").fill(who.email);
  await panel(page).getByLabel("密码", { exact: true }).fill(stack.password);
  await panel(page).getByRole("button", { name: "登录", exact: true }).click();
}

/** From the product: its button, OzID's real sign-in page, and back to the page the sign-in started on. */
async function enterViaOzid(page: Page, who: Account, from = "/cities/shanghai") {
  await page.goto(`${stack.metro}${from}`);
  await signIn(page).click();
  await expect(page).toHaveURL(loginAt(from));
  await submitLogin(page, who);
  await page.waitForURL(`${stack.metro}${from}`);
  await expect(header(page).getByText(who.displayName)).toBeVisible();
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

type Hit = { kind: "write" | "csrf"; account?: string; token?: string; body?: string; status?: number; marker?: string };
/** Every rider write and token request the page sends, with what it carried and how it was answered. */
function recordTraffic(page: Page) {
  const timeline: Hit[] = [];
  const mine = new Map<Request, Hit>();
  page.on("request", (r: Request) => {
    let hit: Hit | undefined;
    if (/\/bff\.metro\.v1\.RiderService\/(MarkLine|UnmarkLine)$/.test(r.url())) {
      hit = { kind: "write", account: r.headers()["x-kongken-user"], token: r.headers()["x-csrf-token"], body: r.postData() ?? "" };
    } else if (r.url().endsWith("/bff.v1.AuthService/IssueCSRFToken")) {
      hit = { kind: "csrf" };
    }
    if (hit) {
      mine.set(r, hit);
      timeline.push(hit);
    }
  });
  page.on("response", (res) => {
    const hit = mine.get(res.request());
    if (hit) {
      hit.status = res.status();
      hit.marker = res.headers()["x-bff-rejection"];
    }
  });
  return timeline;
}

test.describe("sign in through OzID and come back", () => {
  test("an anonymous mark only goes to OzID; the real return restores the account and its own lines", async ({ page, context }, info) => {
    const who = acct("round", info);
    const requested: string[] = [];
    page.on("request", (r) => requested.push(r.url()));
    const before = await rides(who.id);

    await page.goto(`${stack.metro}/cities/shanghai`);
    await expect(line(page, "sh-1")).toBeVisible();
    await mark(page, "sh-1").click();
    // Nothing was written, and the sign-in names the product and the page, nothing else.
    await expect(page).toHaveURL(loginAt("/cities/shanghai"));
    expect(await rides(who.id)).toEqual(before);
    await submitLogin(page, who);
    await page.waitForURL(`${stack.metro}/cities/shanghai`);
    await expect(header(page).getByText(who.displayName)).toBeVisible();
    // The seeded ride is the real Me + rider answer for this account: sh-1 is marked, sh-2 is not.
    await expect(marked(page, "sh-1")).toHaveAttribute("aria-pressed", "true");
    await expect(mark(page, "sh-2")).toBeVisible();
    expect(await noOverflow(page)).toBe(true);
    expect(new URL(page.url()).search).toBe("");

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
    // No request went to the product's own origin for the API: the session is the API host's.
    expect(requested.filter((u) => u.startsWith(stack.metro) && u.includes("/api/"))).toEqual([]);

    // A reload stays signed in.
    await page.reload();
    await expect(header(page).getByText(who.displayName)).toBeVisible();
    await expect(marked(page, "sh-1")).toHaveAttribute("aria-pressed", "true");

    // OzID's own pages open in another tab, with no opener, and leave this page as it is.
    const [popup] = await Promise.all([context.waitForEvent("page"), header(page).getByTestId("ozid-link-home").click()]);
    await popup.waitForLoadState();
    expect(popup.url().startsWith(stack.id)).toBe(true);
    expect(await popup.evaluate(() => window.opener)).toBeNull();
    await expect(popup.getByTestId("overview")).toBeVisible();
    await popup.close();
    await expect(marked(page, "sh-1")).toBeVisible();
  });

  test("a signed-in rider marks and unmarks a line over the real CSRF check, and the server follows", async ({ page }, info) => {
    const who = acct("mark", info);
    await enterViaOzid(page, who);
    expect(await rides(who.id)).toEqual(["sh-1"]);
    await mark(page, "sh-2").click();
    await expect(marked(page, "sh-2")).toHaveAttribute("aria-pressed", "true");
    expect(await rides(who.id)).toEqual(["sh-1", "sh-2"]);
    await marked(page, "sh-2").click();
    await expect(mark(page, "sh-2")).toBeVisible();
    expect(await rides(who.id)).toEqual(["sh-1"]);
  });

  test("the personal page returns to itself, and the rider profile is not the account's nickname", async ({ page }, info) => {
    const who = acct("me", info);
    await enterViaOzid(page, who, "/me");
    await expect(page.getByRole("region", { name: "上海" })).toContainText("1号线");
    // The public name is the rider profile's own and starts empty; the account's nickname is only in the header.
    await expect(page.getByLabel("主页显示名称")).toHaveValue("");
    await expect(page.getByTestId("account-pointer").getByTestId("ozid-link-home-me")).toHaveAttribute("href", stack.id);
    await page.getByLabel("主页显示名称").fill("真实栈骑行者");
    await page.getByRole("button", { name: "保存资料" }).click();
    await expect(page.getByText("资料已保存。")).toBeVisible();
    await page.reload();
    await expect(page.getByLabel("主页显示名称")).toHaveValue("真实栈骑行者");
    await expect(header(page).getByText(who.displayName)).toBeVisible();
    expect(await noOverflow(page)).toBe(true);
  });

  test("a page the BFF cannot confirm, or a path that cannot be written canonically, signs in to the start page", async ({ page }, info) => {
    const who = acct("fb", info);
    // A city whose name is not ASCII has no canonical return path: the start page, without asking.
    await page.goto(`${stack.metro}/cities/%E4%B8%8A%E6%B5%B7`);
    await signIn(page).click();
    await expect(page).toHaveURL(loginAt("/"));
    // The BFF cannot be asked about the page: the sign-in is not held back and goes to the start page.
    await page.goto(`${stack.metro}/topics/six-cars`);
    const refuse = (route: { abort: (code: string) => Promise<void> }) => route.abort("connectionrefused");
    await page.route("**/bff.v1.AuthService/ResolveNavigation", refuse);
    await signIn(page).click();
    await expect(page).toHaveURL(loginAt("/"));
    await page.unroute("**/bff.v1.AuthService/ResolveNavigation", refuse); // OzID's own question is not part of the outage
    // And that really ends on the start page, signed in, not in an OzID refusal.
    await submitLogin(page, who);
    await page.waitForURL(`${stack.metro}/`);
    await expect(header(page).getByText(who.displayName)).toBeVisible();
  });
});

test.describe("old entry points", () => {
  test("/login?next=… follows nothing and starts nothing; the button goes to OzID without it", async ({ page }) => {
    const hosts = new Set<string>();
    page.on("request", (r) => hosts.add(new URL(r.url()).host));
    await page.goto(`${stack.metro}/login?next=https%3A%2F%2Fevil.example%2F&mode=register#frag-secret`);
    await expect(page.getByTestId("legacy-account")).toContainText("登录已迁移到 OzID");
    await expect(page.locator("input[type=password], input[type=email]")).toHaveCount(0);
    expect(new URL(page.url()).search + new URL(page.url()).hash).toBe("");
    expect([...hosts].filter((h) => h.includes("evil") || h.startsWith("id."))).toEqual([]);
    await page.getByTestId("legacy-sign-in").click();
    await expect(page).toHaveURL(loginAt("/"));
  });
});

test.describe("a session that ends or changes elsewhere, and a network that fails", () => {
  test("signing out in OzID shows on the next check without a loop; signing in again resumes", async ({ page, context }, info) => {
    const who = acct("out", info);
    await page.clock.install();
    await enterViaOzid(page, who);
    await expect(marked(page, "sh-1")).toHaveAttribute("aria-pressed", "true");
    await ozidSignOut(context);
    await refocus(page);
    await expect(signIn(page)).toBeVisible();
    await expect(page.getByTestId("session-notice")).toContainText("你已在其他页面退出登录");
    await expect(mark(page, "sh-1")).toBeVisible();
    await expect(page).toHaveURL(`${stack.metro}/cities/shanghai`);
    await page.waitForTimeout(300);
    await expect(page).toHaveURL(`${stack.metro}/cities/shanghai`);
    // Signing in again at OzID, then showing the page again, resumes the same account.
    await ozidSignIn(context, who);
    await refocus(page);
    await expect(header(page).getByText(who.displayName)).toBeVisible();
    await expect(marked(page, "sh-1")).toHaveAttribute("aria-pressed", "true");
  });

  test("a network failure is not a sign-out: nothing is cleared, nobody is sent to sign in, and it recovers", async ({ page }, info) => {
    const who = acct("net", info);
    await page.clock.install();
    await enterViaOzid(page, who);
    await expect(marked(page, "sh-1")).toHaveAttribute("aria-pressed", "true");
    let down = true;
    await page.route("**/bff.v1.AuthService/Me", (route) => (down ? route.abort("connectionrefused") : route.fallback()));
    await refocus(page);
    await expect(page.getByTestId("session-unverified")).toContainText("先按已登录的账号显示");
    await expect(header(page).getByText(who.displayName)).toBeVisible();
    await expect(marked(page, "sh-1")).toHaveAttribute("aria-pressed", "true");
    await expect(signIn(page)).toHaveCount(0);
    down = false;
    await page.getByTestId("session-unverified").getByRole("button", { name: "重新检查" }).click();
    await expect(page.getByTestId("session-unverified")).toHaveCount(0);
    // And a page that loads while the check is down says so instead of asking anybody to sign in.
    await page.reload();
    await refocus(page);
    await expect(header(page).getByText(who.displayName)).toBeVisible();
    down = true;
    await page.reload();
    await expect(page.getByTestId("session-unverified")).toContainText("暂时无法确认登录状态");
    await expect(signIn(page)).toHaveCount(0);
    await expect(page).toHaveURL(`${stack.metro}/cities/shanghai`);
    down = false;
    await page.getByTestId("session-unverified").getByRole("button", { name: "重新检查" }).click();
    await expect(header(page).getByText(who.displayName)).toBeVisible();
  });

  test("a stale tab cannot write into the other account: refused for A, nothing lands in B, and the page learns B", async ({ page, context }, info) => {
    const a = acct("swapa", info);
    const b = acct("swapb", info);
    const timeline = recordTraffic(page);
    await enterViaOzid(page, a);
    await expect(marked(page, "sh-1")).toHaveAttribute("aria-pressed", "true");
    const serverA = await rides(a.id);
    const serverB = await rides(b.id);

    // OzID switches the browser to B in another tab. This page still believes it is A.
    await ozidSignOut(context);
    await ozidSignIn(context, b);
    timeline.length = 0;
    await mark(page, "sh-2").click();
    await expect.poll(() => timeline.some((h) => h.kind === "write" && h.status !== undefined)).toBe(true);
    const writes = timeline.filter((h) => h.kind === "write");
    expect(writes).toHaveLength(1);
    expect(writes[0].account).toBe(a.id);
    expect(writes[0].status).toBe(401);
    // Nobody was written, and the page then asks who it is, shows B, and says so.
    expect(await rides(a.id)).toEqual(serverA);
    expect(await rides(b.id)).toEqual(serverB);
    await expect(header(page).getByText(b.displayName)).toBeVisible();
    await expect(page.getByTestId("session-notice")).toContainText(b.displayName);
    await expect(page).toHaveURL(`${stack.metro}/cities/shanghai`);
    // B's own act is named as B's and lands in B's lines only.
    await mark(page, "sh-2").click();
    await expect(marked(page, "sh-2")).toHaveAttribute("aria-pressed", "true");
    expect(await rides(b.id)).toEqual(["sh-1", "sh-2"]);
    expect(await rides(a.id)).toEqual(serverA);
  });
});

// ---- the CSRF token another tab replaced -----------------------------------------------------------------------------

/** Another tab of the same browser asks the BFF for a token: the shared Cookie is replaced, this page's copy is stale. */
async function rotateCSRF(context: BrowserContext) {
  const tab = await context.newPage();
  await tab.goto(`${stack.metro}/login`);
  const status = await tab.evaluate(async (api) => {
    const res = await fetch(`${api}/api/bff.v1.AuthService/IssueCSRFToken`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" },
      body: "{}",
    });
    return res.status;
  }, stack.api);
  expect(status).toBe(200);
  await tab.close();
}

/**
 * Runs `during` after the page has sent its next MarkLine and before the browser's network stack attaches the shared
 * Cookie to it: the write leaves with the token the page fetched a moment ago, and whatever another tab does in
 * between (replace the token, switch the account) has happened by the time the BFF compares them. Only the first
 * MarkLine is held; the repeat that follows a refusal goes straight out.
 */
async function interleave(page: Page, during: () => Promise<void>) {
  let taken = false;
  await page.route(/\/bff\.metro\.v1\.RiderService\/MarkLine$/, async (route) => {
    if (taken || route.request().method() !== "POST") return route.fallback();
    taken = true;
    await during();
    await route.continue();
  });
}

test.describe("a token replaced by another tab", () => {
  test("is refused once with the BFF's marker, replaced, and the same write goes through exactly once", async ({ page, context }, info) => {
    const who = acct("csrf", info);
    const timeline = recordTraffic(page);
    await enterViaOzid(page, who);
    expect(await rides(who.id)).toEqual(["sh-1"]);
    timeline.length = 0;

    // The write leaves the page with the token it just fetched; before it is sent on, another tab replaces the
    // shared token, so the double submit no longer matches when the BFF compares them.
    await interleave(page, () => rotateCSRF(context));
    await mark(page, "sh-2").click();
    await expect(marked(page, "sh-2")).toHaveAttribute("aria-pressed", "true");

    const calls = timeline.filter((h) => h.kind === "write" || h.kind === "csrf");
    // [token for the write, the write (refused), the repair token, the same write again]
    expect(calls.map((h) => h.kind)).toEqual(["csrf", "write", "csrf", "write"]);
    const [, first, , second] = calls;
    expect(first.status).toBe(403);
    expect(first.marker).toBe("csrf");
    expect(second.status).toBe(200);
    expect(second.marker).toBeUndefined();
    expect(second.body).toBe(first.body);
    expect(first.account).toBe(who.id);
    expect(second.account).toBe(who.id);
    expect(first.token).toBeTruthy();
    expect(second.token).toBeTruthy();
    expect(second.token).not.toBe(first.token);
    // Applied once: sh-2 is marked, nothing else changed.
    expect(await rides(who.id)).toEqual(["sh-1", "sh-2"]);
  });

  test("while it is replaced the browser switches to B: the repeat still names A, is refused, and nobody is written", async ({ page, context }, info) => {
    const a = acct("csrfa", info);
    const b = acct("csrfb", info);
    const timeline = recordTraffic(page);
    await enterViaOzid(page, a);
    const serverA = await rides(a.id);
    const serverB = await rides(b.id);
    timeline.length = 0;

    // OzID switches the browser to B in another tab (new session, new token); this page still believes it is A.
    await interleave(page, async () => {
      await ozidSignOut(context);
      await ozidSignIn(context, b);
    });
    await mark(page, "sh-2").click();

    // The write is refused for its stale token (marked), a new token is fetched with B's Cookie, and the repeat
    // goes out as it was: A's header. The BFF's guard refuses that, before any service sees it.
    await expect.poll(() => timeline.filter((h) => h.kind === "write" && h.status !== undefined).length).toBe(2);
    const writes = timeline.filter((h) => h.kind === "write");
    expect(writes.map((h) => h.status)).toEqual([403, 401]);
    expect(writes[0].marker).toBe("csrf");
    expect(writes[1].marker).toBeUndefined();
    expect(writes.map((h) => h.account)).toEqual([a.id, a.id]);
    expect(writes[1].body).toBe(writes[0].body);
    expect(writes[1].token).not.toBe(writes[0].token);

    expect(await rides(b.id)).toEqual(serverB);
    expect(await rides(a.id)).toEqual(serverA);
    // The page then asks who it is and shows B.
    await expect(header(page).getByText(b.displayName)).toBeVisible();
  });
});
