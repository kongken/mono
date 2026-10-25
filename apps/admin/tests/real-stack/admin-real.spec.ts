import { expect, test, type Page } from "@playwright/test";

/**
 * The unified management console in a real browser against the real BFF and usersrv (started by
 * internal/e2e TestAdminBrowserChain): HTTPS, sibling sub-domains for OzID, the API, the console and
 * a foreign product, the host-only session Cookie, exact CORS/CSRF/Origin, the real role checks, the
 * source allowlist in front of the console and again at the BFF, and real user queries. Nothing of the
 * account or user-management chain is mocked. The gateway is a stand-in (see the Go test): what it
 * cannot prove is listed in docs/admin/features.md.
 */
type Account = { kind: string; email: string; displayName: string };
type Stack = { id: string; api: string; admin: string; trip: string; control: string; inside: string; outside: string; password: string; accounts: Account[]; csp: string };
const stack: Stack = JSON.parse(process.env.ADMIN_REAL_STACK ?? "{}");

test.skip(!process.env.ADMIN_REAL_STACK, "started by internal/e2e TestAdminBrowserChain");

const account = (kind: string) => stack.accounts.find((a) => a.kind === kind)!;
const host = (origin: string) => new URL(origin).hostname;
const USERS = "/api/bff.v1.UserAdminService/";

// The console is served with the Content-Security-Policy of deploy/admin-web; a violation is a failure.
const violations = new WeakMap<Page, string[]>();
test.beforeEach(async ({ page }) => {
  const seen: string[] = [];
  violations.set(page, seen);
  page.on("console", (m) => {
    if (/Content Security Policy|Refused to (load|connect|execute|apply)/i.test(m.text())) seen.push(m.text());
  });
  await setSource(page, stack.inside);
});
test.afterEach(({ page }) => {
  expect(violations.get(page)).toEqual([]);
});

/** Where the gateway pretends the browser is: inside or outside the allowlist. */
async function setSource(page: Page, ip: string) {
  await page.goto(`${stack.control}/source?ip=${ip}`);
  await expect(page.locator("body")).toHaveText(ip);
}

/** Signs in at OzID with the account of `kind` (the form the console sent the browser to). */
async function signInAs(page: Page, kind: string) {
  const panel = page.getByTestId("auth-panel");
  await panel.getByLabel("邮箱").fill(account(kind).email);
  await panel.getByLabel("密码", { exact: true }).fill(stack.password);
  await panel.getByRole("button", { name: "登录", exact: true }).click();
}

/** Opens a console page as an anonymous visitor, signs in at OzID and lands back on the page. */
async function openConsole(page: Page, kind: string, path: string) {
  await page.goto(`${stack.admin}${path}`);
  await page.waitForURL(new RegExp(`^${stack.id}/login\\?app_id=admin&return_path=${encodeURIComponent(path)}$`));
  await signInAs(page, kind);
  await page.waitForURL(`${stack.admin}${path}`);
}

const rows = (page: Page) => page.getByTestId("user-row");

test.describe("sign-in and the role gate", () => {
  test("an operator signs in at OzID from the console and lands back on the page they asked for", async ({ page, context }) => {
    await openConsole(page, "operator", "/users");
    await expect(page.getByRole("heading", { name: "用户" })).toBeVisible();
    await expect(rows(page).first()).toBeVisible();
    // Every account created for this scenario is listed (the newest first), nothing else is needed.
    await expect(page.getByTestId("user-table")).toContainText(account("other").email);
    await expect(page.getByTestId("sections").getByRole("link", { name: "用户" })).toBeVisible();

    // The session Cookie belongs to the API host alone, and the console set nothing of its own.
    const cookies = await context.cookies();
    const sid = cookies.find((c) => c.name === "kongken_sid");
    expect(sid, "session cookie").toBeTruthy();
    expect(sid!.domain).toBe(host(stack.api));
    expect(sid).toMatchObject({ httpOnly: true, secure: true, sameSite: "Lax" });
    expect(cookies.filter((c) => host(stack.admin) === c.domain.replace(/^\./, ""))).toEqual([]);
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + document.cookie);
    expect(stored).not.toContain(sid!.value);
    expect(stored).not.toContain(stack.password);

    // Signing out ends the shared session and the console goes back to OzID sign-in.
    await page.getByRole("button", { name: "退出登录" }).click();
    await page.waitForURL(new RegExp(`^${stack.id}/login\\?app_id=admin`));
  });

  test("an account with no platform role sees no permission: no loop, no data, no OzID round trip", async ({ page }) => {
    const toOzid: string[] = [];
    const userCalls: string[] = [];
    page.on("framenavigated", (f) => f === page.mainFrame() && f.url().startsWith(stack.id) && toOzid.push(f.url()));
    page.on("request", (r) => r.url().includes(USERS) && userCalls.push(r.url()));
    await openConsole(page, "plain", "/users");
    await expect(page.getByTestId("no-permission")).toBeVisible();
    const seen = toOzid.length;
    await page.waitForTimeout(800);
    expect(toOzid.length).toBe(seen); // OzID is not opened again
    expect(new URL(page.url()).pathname).toBe("/users");
    expect(userCalls).toEqual([]);
    await expect(page.getByTestId("sections")).toHaveCount(0);
  });

  test("a metro-admin alone gets neither the user section nor any user data", async ({ page }) => {
    const userCalls: string[] = [];
    page.on("request", (r) => r.url().includes(USERS) && userCalls.push(r.url()));
    await openConsole(page, "metro", "/");
    await expect(page.getByTestId("sections")).toHaveCount(0);
    await expect(page.getByTestId("my-roles")).toHaveText("地铁目录管理员");
    await page.goto(`${stack.admin}/users`);
    await expect(page.getByTestId("no-permission")).toBeVisible();
    expect(userCalls).toEqual([]);
  });
});

test.describe("real user queries", () => {
  test("exact email, literal nickname, filters, paging and a detail page", async ({ page }) => {
    await openConsole(page, "operator", "/users");
    const target = account("other");
    await page.getByLabel("查找方式").selectOption({ label: "邮箱（精确）" });
    await page.getByLabel("查找内容").fill(target.email.toUpperCase());
    await page.getByRole("button", { name: "查询" }).click();
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText(target.email);
    // A near miss is not a lookup: only the whole address matches.
    await page.getByLabel("查找内容").fill(`x${target.email}`);
    await page.getByRole("button", { name: "查询" }).click();
    await expect(page.getByTestId("no-users")).toBeVisible();

    await page.getByLabel("查找方式").selectOption({ label: "昵称（包含）" });
    await page.getByLabel("查找内容").fill("MEMBER_");
    await page.getByRole("button", { name: "查询" }).click();
    await expect(rows(page)).toHaveCount(2);
    await page.getByLabel("查找内容").fill("%");
    await page.getByRole("button", { name: "查询" }).click();
    await expect(page.getByTestId("no-users")).toBeVisible();

    await page.getByRole("button", { name: "清除条件" }).click();
    await page.getByLabel("角色").selectOption("ozid-admin");
    await page.getByRole("button", { name: "查询" }).click();
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText(account("operator").email);

    await rows(page).first().getByRole("link").click();
    await expect(page.getByTestId("detail-email")).toHaveText(account("operator").email);
    await expect(page.getByTestId("detail-roles")).toHaveText("平台管理员");
    await expect(page.getByTestId("detail-status")).toHaveText("正常");
    // Nothing personal reaches the address.
    expect(page.url()).not.toContain("@");
    expect(page.url()).toMatch(/\/users\/[0-9a-f-]{36}$/);
  });
});

test.describe("the perimeter, from a real browser", () => {
  test("a source outside the allowlist is not even given the page or its assets", async ({ page }) => {
    await openConsole(page, "operator", "/users");
    const js = await page.evaluate(() => document.querySelector<HTMLScriptElement>("script[src]")!.src);
    await setSource(page, stack.outside);
    for (const url of [`${stack.admin}/`, `${stack.admin}/users`, js, `${stack.admin}/favicon.svg`, `${stack.admin}/assets/nope.js`]) {
      const res = await page.goto(url);
      expect(res!.status(), url).toBe(403);
      expect(await page.content(), url).not.toContain('id="root"');
    }
  });

  test("a console that is already open is refused at the BFF too, and ordinary account features keep working", async ({ page }) => {
    await openConsole(page, "operator", "/users");
    await expect(rows(page).first()).toBeVisible();
    // Move the source from a second page of the same browser, so this page keeps its state: it was
    // loaded from inside, and its next query now comes from outside.
    // The BFF's perimeter answers a bare 403 with no CORS grant (the status codes are asserted in
    // TestChainUserAdminSourcePolicy), so the browser withholds the answer: the page sees a failed
    // call and can only say the service could not be reached, never a list or "no permission".
    const other = await page.context().newPage();
    await setSource(other, stack.outside);
    await other.close();
    await page.getByLabel("角色").selectOption("ozid-admin");
    await page.getByRole("button", { name: "查询" }).click();
    await expect(page.getByTestId("problem-network")).toBeVisible();
    await expect(page.getByTestId("no-permission")).toHaveCount(0);
    await expect(rows(page)).toHaveCount(0);

    // OzID (the account features) does not sit behind the allowlist: the same session still works.
    await page.goto(`${stack.id}/`);
    await expect(page.getByTestId("overview-email")).toHaveText(account("operator").email);
  });

  test("another product's page cannot use the operator's session to read users", async ({ page }) => {
    await openConsole(page, "operator", "/users");
    await page.goto(`${stack.trip}/`);
    await expect(page.locator("#me")).toHaveText(`user:${account("operator").displayName}`);
    // The product is in the CORS list and the browser sends the operator's Cookie; the BFF still
    // refuses because the Origin is not the console's. Either the preflight or the call is refused.
    const result = await page.evaluate(async (api) => {
      try {
        const r = await fetch(`${api}/api/bff.v1.UserAdminService/ListUsers`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" },
          body: "{}",
        });
        return { status: r.status, body: await r.text() };
      } catch {
        return { status: 0, body: "blocked" };
      }
    }, stack.api);
    expect([0, 403]).toContain(result.status);
    expect(result.body).not.toContain("@");
    expect(result.body).not.toContain("users");
  });
});

test.describe("real account changes", () => {
  const CHANGE_CALLS = /UserAdminService\/(Disable|Enable)User|UserAdminService\/(Grant|Revoke)MetroAdmin/;

  /** Finds an account by its exact email in the user list and opens its detail page. */
  async function openDetail(page: Page, email: string) {
    await page.goto(`${stack.admin}/users`);
    await page.getByLabel("查找方式").selectOption({ label: "邮箱（精确）" });
    await page.getByLabel("查找内容").fill(email);
    await page.getByRole("button", { name: "查询" }).click();
    await expect(rows(page)).toHaveCount(1);
    await rows(page).first().getByRole("link").click();
    await expect(page.getByTestId("detail-email")).toHaveText(email);
  }

  async function change(page: Page, button: "act-status" | "act-role", reason: string) {
    await page.getByTestId(button).click();
    await page.getByTestId("change-reason").fill(reason);
    await page.getByTestId("change-confirm").scrollIntoViewIfNeeded();
    await page.getByTestId("change-confirm").click();
  }

  test("an operator disables, enables, grants and revokes; the account's own session ends each time and nothing old comes back", async ({ page, browser }) => {
    const target = account("target");
    const ownerContext = await browser.newContext({ ignoreHTTPSErrors: true, viewport: page.viewportSize() });
    const owner = await ownerContext.newPage();
    try {
      // The account being changed is signed in elsewhere.
      await openConsole(owner, "target", "/");
      await expect(owner.getByTestId("no-permission")).toBeVisible();

      await openConsole(page, "operator", "/users");
      // A platform administrator's own page offers no change.
      await openDetail(page, account("operator").email);
      await expect(page.getByTestId("actions-protected")).toBeVisible();
      await expect(page.getByTestId("actions")).toHaveCount(0);

      const writes: string[] = [];
      page.on("request", (r) => CHANGE_CALLS.test(r.url()) && writes.push(r.url()));
      await openDetail(page, target.email);
      await expect(page.getByTestId("detail-status")).toHaveText("正常");

      // Disable: the dialog names the target and the impact; the owner's session is gone at the next call.
      await page.getByTestId("act-status").click();
      await expect(page.getByTestId("change-target")).toContainText(target.email);
      await expect(page.getByTestId("change-impact")).toContainText("退出");
      await page.getByTestId("change-reason").fill("工单 77：用户申请");
      await page.getByTestId("change-confirm").click();
      await expect(page.getByTestId("change-notice")).toContainText("账号已禁用");
      await expect(page.getByTestId("detail-status")).toHaveText("已禁用");
      await owner.reload();
      await owner.waitForURL(new RegExp(`^${stack.id}/login\\?app_id=admin`));
      await signInAs(owner, "target");
      await expect(owner.getByText("邮箱或密码错误，或账号不可用")).toBeVisible(); // a disabled account cannot sign in

      // Enable: it can sign in again, but only anew.
      await change(page, "act-status", "工单 77：已处理");
      await expect(page.getByTestId("detail-status")).toHaveText("正常");
      await signInAs(owner, "target");
      await owner.waitForURL(/^https:\/\/admin\./);
      await expect(owner.getByTestId("no-permission")).toBeVisible();

      // Grant the metro role: the owner's session ends and the new one carries the role.
      await change(page, "act-role", "工单 78");
      await expect(page.getByTestId("detail-roles")).toHaveText("地铁目录管理员");
      await owner.reload();
      await owner.waitForURL(new RegExp(`^${stack.id}/login\\?app_id=admin`));
      await signInAs(owner, "target");
      await owner.waitForURL(/^https:\/\/admin\./);
      await expect(owner.getByTestId("my-roles")).toHaveText("地铁目录管理员");

      // Revoke it again: the page names the version it got back, so this is not stale.
      await change(page, "act-role", "工单 79");
      await expect(page.getByTestId("detail-roles")).toHaveText("无");
      await owner.reload();
      await owner.waitForURL(new RegExp(`^${stack.id}/login\\?app_id=admin`));

      expect(writes).toHaveLength(4);
      // The console never calls anything that sets a role by name, a password or a deletion.
      const ozidCalls: string[] = [];
      page.on("request", (r) => /AuthService\/(DeleteAccount|ChangePassword)|UserService\//.test(r.url()) && ozidCalls.push(r.url()));
      await page.reload();
      expect(ozidCalls).toEqual([]);
    } finally {
      await ownerContext.close();
    }
  });

  test("a password check older than 15 minutes goes to OzID and back; the refused change is not replayed", async ({ page, context }) => {
    const target = account("target");
    await openConsole(page, "operator", "/users");
    await openDetail(page, target.email);
    const detailPath = new URL(page.url()).pathname;
    const writes: string[] = [];
    page.on("request", (r) => CHANGE_CALLS.test(r.url()) && writes.push(r.url()));

    // The gateway's control host makes the operator's password check 16 minutes old (a browser cannot wait 15).
    const control = await context.newPage();
    await control.goto(`${stack.control}/age?age=${encodeURIComponent(account("operator").email)}`);
    await control.close();
    await change(page, "act-status", "工单 80");
    await expect(page.getByTestId("change-problem-reauth")).toBeVisible();
    await expect(page.getByTestId("detail-status")).toHaveText("正常");
    expect(writes).toHaveLength(1);
    await page.waitForTimeout(500);
    expect(page.url().startsWith(stack.id)).toBe(false); // no redirect by itself

    await page.getByTestId("change-reauth").click();
    await page.waitForURL(new RegExp(`^${stack.id}/security\\?app_id=admin&return_path=${encodeURIComponent(detailPath)}$`));
    await page.getByLabel("当前密码").fill("not the password at all");
    await page.getByRole("button", { name: "确认密码" }).click();
    await expect(page.getByTestId("reauthenticate")).toContainText("当前密码不正确");
    await page.getByLabel("当前密码").fill(stack.password);
    await page.getByRole("button", { name: "确认密码" }).click();
    await page.waitForURL(`${stack.admin}${detailPath}`);

    // Back on the page: nothing was sent again, and the account is untouched.
    await expect(page.getByTestId("detail-status")).toHaveText("正常");
    await expect(page.getByTestId("change-dialog")).toHaveCount(0);
    await page.waitForTimeout(500);
    expect(writes).toHaveLength(1);

    // The administrator decides again, on purpose, and puts it back afterwards.
    await change(page, "act-status", "工单 80");
    await expect(page.getByTestId("detail-status")).toHaveText("已禁用");
    await change(page, "act-status", "工单 80：恢复");
    await expect(page.getByTestId("detail-status")).toHaveText("正常");
    expect(writes).toHaveLength(3);
  });

  test("outside the allowlist the console cannot change anything, and a console that is open is refused at the BFF", async ({ page }) => {
    const target = account("target");
    await openConsole(page, "operator", "/users");
    await openDetail(page, target.email);
    const other = await page.context().newPage();
    await setSource(other, stack.outside);
    await other.close();
    await change(page, "act-status", "工单 81");
    // The perimeter's bare 403 carries no CORS grant: the browser withholds it, the page says it could not be reached.
    await expect(page.getByTestId("change-problem-network")).toBeVisible();
    await expect(page.getByTestId("detail-status")).toHaveText("正常");
    await setSource(page, stack.inside);
  });
});
