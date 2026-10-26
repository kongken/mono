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

  test("a metro-admin alone gets the metro section, neither the user section nor any user data", async ({ page }) => {
    const userCalls: string[] = [];
    page.on("request", (r) => r.url().includes(USERS) && userCalls.push(r.url()));
    await openConsole(page, "metro", "/");
    await expect(page.getByTestId("sections").getByRole("link", { name: "地铁目录" })).toBeVisible();
    await expect(page.getByTestId("sections").getByRole("link", { name: "用户" })).toHaveCount(0);
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

test.describe("real account audit", () => {
  const AUDIT = /UserAdminService\/ListSecurityAudit/;
  const auditRows = (page: Page) => page.getByTestId("audit-row");
  const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

  test("an operator signs in from the audit address and lands back on it; the registry allows exactly this page", async ({ page }) => {
    await openConsole(page, "operator", "/audit/users");
    await expect(page.getByRole("heading", { name: "账号审计" })).toBeVisible();
    await expect(auditRows(page).first()).toBeVisible();
    await expect(page.getByTestId("sections").getByRole("link", { name: "账号审计" })).toHaveAttribute("aria-current", "page");
    expect(await noOverflow(page)).toBe(true);
    // The newest rows are the sign-ins and audit reads of this scenario: classified, with no address in them.
    const text = await page.getByTestId("audit-list").innerText();
    expect(text).toMatch(/登录|查询账号审计/);
    for (const a of stack.accounts) expect(text).not.toContain(a.email);
    expect(text).not.toContain(stack.password);
  });

  test("an account with no platform role and a metro-admin alone get no audit data", async ({ page, browser }) => {
    const calls: string[] = [];
    page.on("request", (r) => AUDIT.test(r.url()) && calls.push(r.url()));
    await openConsole(page, "plain", "/audit/users");
    await expect(page.getByTestId("no-permission")).toBeVisible();
    expect(calls).toEqual([]);
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: page.viewportSize() });
    const metro = await ctx.newPage();
    try {
      metro.on("request", (r) => AUDIT.test(r.url()) && calls.push(r.url()));
      await openConsole(metro, "metro", "/audit/users");
      await expect(metro.getByTestId("no-permission")).toBeVisible();
      await expect(metro.getByTestId("sections").getByRole("link", { name: "账号审计" })).toHaveCount(0);
      expect(calls).toEqual([]);
    } finally {
      await ctx.close();
    }
  });

  test("a change made in the console shows in that account's audit: who, what, why, before and after - and the command line is told apart", async ({ page }) => {
    const target = account("target");
    await openConsole(page, "operator", "/users");
    await page.getByLabel("查找方式").selectOption({ label: "邮箱（精确）" });
    await page.getByLabel("查找内容").fill(target.email);
    await page.getByRole("button", { name: "查询" }).click();
    await expect(rows(page)).toHaveCount(1);
    await rows(page).first().getByRole("link").click();
    await expect(page.getByTestId("detail-email")).toHaveText(target.email);
    const id = await page.getByTestId("detail-id").innerText();

    const reason = `工单 ${Date.now() % 1000}：审计验证`;
    for (const done of ["已禁用", "正常"]) {
      await page.getByTestId("act-status").click();
      await page.getByTestId("change-reason").fill(reason);
      await page.getByTestId("change-confirm").scrollIntoViewIfNeeded();
      await page.getByTestId("change-confirm").click();
      await expect(page.getByTestId("detail-status")).toHaveText(done);
    }

    await page.getByTestId("audit-link").click();
    await expect(page).toHaveURL(`${stack.admin}/audit/users?user=${id}`);
    await expect(auditRows(page).first()).toBeVisible();
    const web = auditRows(page).filter({ hasText: "网页控制台" }).filter({ hasText: reason });
    await expect(web).toHaveCount(2);
    await expect(web.nth(0).getByTestId("audit-action")).toHaveText("启用账号");
    await expect(web.nth(0).getByTestId("audit-change")).toHaveText("状态：已禁用 → 正常");
    await expect(web.nth(1).getByTestId("audit-action")).toHaveText("禁用账号");
    await expect(web.nth(1).getByTestId("audit-change")).toHaveText("状态：正常 → 已禁用");
    await expect(web.nth(1).getByTestId("audit-result")).toContainText("已完成");
    // The web operator is an account (a link), the same one on both rows; every row of the page is about this account.
    const operatorHref = await web.nth(0).getByTestId("audit-operator").getByRole("link").getAttribute("href");
    expect(operatorHref).toMatch(/^\/users\/[0-9a-f-]{36}$/);
    expect(await web.nth(1).getByTestId("audit-operator").getByRole("link").getAttribute("href")).toBe(operatorHref);
    for (const u of await page.getByTestId("audit-user").all()) await expect(u).toHaveText(id);
    // The command line (how the platform role of the operator was granted) is a different channel, with only a name.
    await page.getByRole("button", { name: "清除条件" }).click();
    await page.getByLabel("渠道").selectOption("cli");
    await page.getByRole("button", { name: "查询" }).click();
    await expect(auditRows(page).first()).toBeVisible();
    await expect(auditRows(page).first().getByTestId("audit-operator")).toContainText("不是网页账号");
    await expect(auditRows(page).first().getByTestId("audit-operator").getByRole("link")).toHaveCount(0);
    // The page and the address hold no email, password or token; storage holds nothing of the audit.
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + document.cookie);
    expect(stored).not.toContain(id);
    expect(stored).not.toContain(stack.password);
    expect(await page.locator("main").innerText()).not.toContain("@");
    expect(await noOverflow(page)).toBe(true);
  });

  test("filters and paging work against the real list", async ({ page }) => {
    await openConsole(page, "operator", "/audit/users");
    await expect(auditRows(page).first()).toBeVisible();
    await page.getByLabel("结果").selectOption("ok");
    await page.getByLabel("操作类型").fill("account.create");
    await page.getByRole("button", { name: "查询" }).click();
    await expect(auditRows(page).first()).toBeVisible();
    for (const r of await auditRows(page).all()) await expect(r.getByTestId("audit-action")).toHaveText("创建账号");
    await page.getByLabel("操作类型").fill("Not A Class");
    await page.getByRole("button", { name: "查询" }).click();
    await expect(page.getByTestId("audit-filter-problem")).toContainText("操作类型");
    await page.getByRole("button", { name: "清除条件" }).click();
    await expect(page.getByTestId("page-number")).toHaveText("第 1 页");
    await expect(page.getByRole("button", { name: "上一页" })).toBeDisabled();
  });

  test("another product's page cannot read the audit with the operator's session, and a source outside the allowlist gets nothing", async ({ page }) => {
    await openConsole(page, "operator", "/audit/users");
    await expect(auditRows(page).first()).toBeVisible();
    await page.goto(`${stack.trip}/`);
    await expect(page.locator("#me")).toHaveText(`user:${account("operator").displayName}`);
    const result = await page.evaluate(async (api) => {
      try {
        const r = await fetch(`${api}/api/bff.v1.UserAdminService/ListSecurityAudit`, {
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
    expect(result.body).not.toContain("entries");

    // A console page that is already open, queried again from outside the allowlist, is refused at the BFF.
    await page.goto(`${stack.admin}/audit/users`);
    await expect(auditRows(page).first()).toBeVisible();
    const other = await page.context().newPage();
    await setSource(other, stack.outside);
    await other.close();
    await page.getByRole("button", { name: "查询" }).click();
    await expect(page.getByTestId("problem-network")).toBeVisible();
    await expect(auditRows(page)).toHaveCount(0);
    await setSource(page, stack.inside);
  });
});

// ---- the metro catalogue administration ----------------------------------------------------------------------
// The same console, the real metro service behind the real BFF. Names carry the project (desktop or mobile) so
// the two runs of one stack never meet the other's records.

const METRO = /AdminService\/|AccessTokenService\//;
const METRO_WRITES = /bff\.metro\.v1\.AdminService\/(Create|Update|Delete)|AccessTokenService\/(Create|Revoke)|ImportCatalog/;
const tag = () => test.info().project.name;
const section = (page: Page, label: string) => page.getByRole("navigation", { name: "地铁目录分区" }).getByRole("link", { name: label, exact: true });
const save = (page: Page) => page.getByRole("button", { name: "保存", exact: true });
const nameField = (page: Page) => page.getByLabel("名称", { exact: true });

test.describe("real metro administration", () => {
  test("a metro administrator signs in from a metro address and manages cities and lines; every rule comes from the real service", async ({ page }) => {
    const slug = `e2e-${tag()}`;
    const failed: string[] = [];
    page.on("response", (r) => METRO.test(r.url()) && r.status() >= 400 && failed.push(`${r.status()} ${r.url()}`));
    await openConsole(page, "metro", "/metro/cities");
    await expect(page.getByRole("heading", { name: "地铁目录" })).toBeVisible();
    await expect(page.locator("iframe")).toHaveCount(0);

    await page.getByRole("button", { name: "新建城市" }).click();
    await expect(page.getByLabel(/密码|原因/)).toHaveCount(0);
    await page.getByLabel("标识 slug").fill("Bad Slug");
    await nameField(page).fill("测试城");
    await save(page).click();
    await expect(page.getByLabel("标识 slug")).toHaveAttribute("aria-invalid", "true");
    await page.getByLabel("标识 slug").fill(slug);
    await save(page).click();
    await expect(page.getByRole("cell", { name: slug, exact: true })).toBeVisible();

    // A second city with the same slug: the real unique rule, said on the field.
    await page.getByRole("button", { name: "新建城市" }).click();
    await page.getByLabel("标识 slug").fill(slug);
    await nameField(page).fill("重复");
    await save(page).click();
    await expect(page.getByLabel("标识 slug")).toHaveAttribute("aria-invalid", "true");
    await page.getByRole("button", { name: "取消" }).click();

    await section(page, "线路").click();
    await page.getByRole("button", { name: "新建线路" }).click();
    await page.getByLabel("代码", { exact: true }).fill(`${slug}-1`);
    await page.getByLabel("所属城市 slug").fill("no-such-city");
    await nameField(page).fill("一号线");
    await page.getByLabel("颜色").fill("112233");
    await save(page).click();
    await expect(page.getByLabel("颜色")).toHaveAttribute("aria-invalid", "true");
    // The existence of the city is checked after the shape of the form: the next answer names the city field.
    await page.getByLabel("颜色").fill("#112233");
    await save(page).click();
    await expect(page.getByLabel("所属城市 slug")).toHaveAttribute("aria-invalid", "true");
    await expect(page.getByLabel("颜色")).not.toHaveAttribute("aria-invalid", "true");
    await page.getByLabel("所属城市 slug").fill(slug);
    await save(page).click();
    await expect(page.getByRole("cell", { name: `${slug}-1`, exact: true })).toBeVisible();

    // The city is in use: the real references answer, and the city stays.
    await section(page, "城市").click();
    await page.getByLabel("搜索城市").fill(slug);
    await page.getByRole("button", { name: `删除 ${slug}` }).click();
    await page.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "仍被引用，不能删除：线路 1" })).toBeVisible();
    await page.getByRole("button", { name: "取消" }).click();
    await expect(page.getByRole("cell", { name: slug, exact: true })).toBeVisible();

    // Clean up in the order the references allow.
    await section(page, "线路").click();
    await page.getByLabel("搜索线路").fill(slug);
    await page.getByRole("button", { name: `删除 ${slug}-1` }).click();
    await page.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByRole("button", { name: "确认删除" })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: `${slug}-1` })).toHaveCount(0);
    await section(page, "城市").click();
    await page.getByLabel("搜索城市").fill(slug);
    await page.getByRole("button", { name: `删除 ${slug}` }).click();
    await page.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByRole("button", { name: "确认删除" })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: slug })).toHaveCount(0);
    // The only refusals were the ones this scenario asked for (field errors, the duplicate, the reference).
    expect(failed.every((f) => f.startsWith("400 ") || f.startsWith("409 "))).toBe(true);
  });

  test("a change made elsewhere while a record is open is a conflict that keeps what was typed, and nothing is overwritten by itself", async ({ page, context }) => {
    const slug = `e2e-race-${tag()}`;
    await openConsole(page, "metro", "/metro/cities");
    await page.getByRole("button", { name: "新建城市" }).click();
    await page.getByLabel("标识 slug").fill(slug);
    await nameField(page).fill("原名");
    await save(page).click();
    await page.getByLabel("搜索城市").fill(slug);
    await page.getByRole("button", { name: `编辑 ${slug}` }).click();
    await nameField(page).fill("我的名字");

    // The same administrator, another tab, changes the record first.
    const other = await context.newPage();
    await other.goto(`${stack.admin}/metro/cities`);
    await other.getByLabel("搜索城市").fill(slug);
    await other.getByRole("button", { name: `编辑 ${slug}` }).click();
    await nameField(other).fill("别人的名字");
    await save(other).click();
    await expect(other.getByRole("cell", { name: "别人的名字", exact: true })).toBeVisible();
    await other.close();

    await save(page).click();
    const conflict = page.getByTestId("conflict");
    await expect(conflict).toContainText("已被他人修改");
    await expect(conflict.getByRole("row", { name: /名称/ })).toContainText("别人的名字");
    await expect(nameField(page)).toHaveValue("我的名字");
    await expect(save(page)).toBeDisabled();
    await page.getByRole("button", { name: "保留我的修改，基于最新版本" }).click();
    await save(page).click();
    await expect(page.getByRole("cell", { name: "我的名字", exact: true })).toBeVisible();

    await page.getByRole("button", { name: `删除 ${slug}` }).click();
    await page.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByRole("button", { name: "确认删除" })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: slug })).toHaveCount(0);
  });

  test("import is a preview first and all or nothing; an export is a file the import takes back unchanged", async ({ page }) => {
    const slug = `e2e-imp-${tag()}`;
    await openConsole(page, "metro", "/metro/import");
    const apply = page.getByRole("button", { name: "确认导入" });
    const box = page.getByLabel("目录 JSON");
    const calls: string[] = [];
    page.on("request", (r) => r.url().includes("ImportCatalog") && calls.push(r.postData() ?? ""));

    await box.fill(JSON.stringify({ format: "metro-catalog/1", cities: [{ slug, name: "导入城" }], lines: [{ code: `${slug}-1`, city: slug, name: "坏线", color: "nope" }] }));
    await expect(page.getByText(/lines\[0\]\.color/)).toBeVisible();
    await expect(apply).toBeDisabled();

    await box.fill(JSON.stringify({ format: "metro-catalog/1", cities: [{ slug, name: "导入城" }] }));
    await expect(page.getByText(/预览（尚未写入）/)).toContainText("城市 新增 1");
    await expect(apply).toBeEnabled();
    expect(calls.every((c) => JSON.parse(c).dryRun === true)).toBe(true);
    await section(page, "城市").click();
    await page.getByLabel("搜索城市").fill(slug);
    await expect(page.getByRole("cell", { name: slug })).toHaveCount(0);

    await section(page, "导入").click();
    await page.getByLabel("目录 JSON").fill(JSON.stringify({ format: "metro-catalog/1", cities: [{ slug, name: "导入城" }] }));
    await expect(page.getByText(/预览（尚未写入）/)).toContainText("城市 新增 1");
    await apply.click();
    await expect(page.getByText(/已导入：.*目录版本/)).toBeVisible();
    await section(page, "城市").click();
    await page.getByLabel("搜索城市").fill(slug);
    await expect(page.getByRole("cell", { name: slug, exact: true })).toBeVisible();

    await section(page, "导出").click();
    const download = page.waitForEvent("download");
    await page.getByRole("button", { name: "下载目录 JSON" }).click();
    const text = await (await import("node:fs/promises")).readFile((await (await download).path())!, "utf8");
    const doc = JSON.parse(text);
    expect(doc.format).toBe("metro-catalog/1");
    expect(doc.cities.map((c: { slug: string }) => c.slug)).toContain(slug);

    await section(page, "导入").click();
    await page.getByLabel("目录 JSON").fill(text);
    const preview = page.getByText(/预览（尚未写入）/);
    await expect(preview).toContainText(`城市 新增 0 / 更新 0 / 无变化 ${doc.cities.length}`);
    await expect(preview).toContainText("线路 新增 0 / 更新 0");
    await expect(preview).toContainText("专题 新增 0 / 更新 0");

    await section(page, "城市").click();
    await page.getByLabel("搜索城市").fill(slug);
    await page.getByRole("button", { name: `删除 ${slug}` }).click();
    await page.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByRole("button", { name: "确认删除" })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: slug })).toHaveCount(0);
  });

  test("the audit log tells the web edit from the operator and shows before and after; a token's secret is shown once", async ({ page }) => {
    const slug = `e2e-audit-${tag()}`;
    await openConsole(page, "metro", "/metro/cities");
    await page.getByRole("button", { name: "新建城市" }).click();
    await page.getByLabel("标识 slug").fill(slug);
    await nameField(page).fill("审计城");
    await save(page).click();
    await page.getByLabel("搜索城市").fill(slug);
    await page.getByRole("button", { name: `编辑 ${slug}` }).click();
    await nameField(page).fill("审计城二");
    await save(page).click();
    await expect(page.getByRole("cell", { name: "审计城二", exact: true })).toBeVisible();

    await section(page, "审计日志").click();
    await page.getByLabel("对象").selectOption("city");
    await page.getByLabel("渠道").selectOption("web");
    const update = page.getByRole("row", { name: /city\.update/ }).first();
    await expect(update).toBeVisible();
    await expect(update).toContainText("网页");
    await update.getByRole("button", { name: "详情" }).click();
    const [before, after] = [page.locator("pre").nth(0), page.locator("pre").nth(1)];
    await expect(before).toContainText('"name": "审计城"');
    await expect(before).not.toContainText("审计城二");
    await expect(after).toContainText('"name": "审计城二"');
    await page.getByLabel("渠道").selectOption("mcp");
    await expect(page.getByRole("cell", { name: "city.update" })).toHaveCount(0);

    // The management token: the secret appears once, in memory only.
    await section(page, "管理令牌").click();
    await page.getByLabel("名称").fill(`e2e-${tag()}`);
    await page.getByRole("button", { name: "创建", exact: true }).click();
    const secret = page.getByTestId("token-secret");
    await expect(secret).toContainText("mcp_");
    const value = await secret.innerText();
    await expect(page.locator("code", { hasText: `${stack.api}/mcp/metro` })).toBeVisible();
    expect(await page.evaluate(() => JSON.stringify([{ ...localStorage }, { ...sessionStorage }]) + document.cookie + location.href)).not.toContain(value);
    await page.reload();
    await expect(page.getByRole("cell", { name: `e2e-${tag()}`, exact: true })).toBeVisible();
    await expect(page.getByTestId("token-secret")).toHaveCount(0);
    expect(await page.content()).not.toContain(value);
    await page.getByRole("button", { name: `吊销 e2e-${tag()}` }).click();
    await expect(page.getByRole("row", { name: new RegExp(`e2e-${tag()} `) }).getByText("已吊销")).toBeVisible();

    await section(page, "城市").click();
    await page.getByLabel("搜索城市").fill(slug);
    await page.getByRole("button", { name: `删除 ${slug}` }).click();
    await page.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByRole("button", { name: "确认删除" })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: slug })).toHaveCount(0);
  });

  test("a password check older than 15 minutes goes to OzID and back to the same metro page; the typed token form is offered again and never replayed", async ({ page, context }) => {
    const label = `e2e-reauth-${tag()}`;
    await openConsole(page, "metro", "/metro/tokens");
    const writes: string[] = [];
    page.on("request", (r) => METRO_WRITES.test(r.url()) && writes.push(r.url()));
    await expect(page.getByLabel("名称")).toBeVisible();

    // The gateway's control host makes the administrator's password check 16 minutes old (a browser cannot wait 15).
    const control = await context.newPage();
    await control.goto(`${stack.control}/age?age=${encodeURIComponent(account("metro").email)}`);
    await control.close();

    await page.getByLabel("名称").fill(label);
    await page.getByLabel("权限").selectOption("metro:write");
    await page.getByRole("button", { name: "创建", exact: true }).click();
    await expect(page.getByTestId("problem-reauth")).toBeVisible();
    await expect(page.getByTestId("token-secret")).toHaveCount(0);
    expect(writes).toHaveLength(1);
    await page.waitForTimeout(500);
    expect(page.url().startsWith(stack.id)).toBe(false); // no redirect by itself

    await page.getByRole("button", { name: "前往 OzID 验证密码" }).click();
    await page.waitForURL(new RegExp(`^${stack.id}/security\\?app_id=admin&return_path=${encodeURIComponent("/metro/tokens")}$`));
    await page.getByLabel("当前密码").fill(stack.password);
    await page.getByRole("button", { name: "确认密码" }).click();
    await page.waitForURL(`${stack.admin}/metro/tokens`);

    // Back on the page: the content is there, and nothing was sent again.
    await expect(page.getByText("已恢复你在验证密码前填写的内容")).toBeVisible();
    await expect(page.getByLabel("名称")).toHaveValue(label);
    await expect(page.getByLabel("权限")).toHaveValue("metro:write");
    await page.waitForTimeout(500);
    expect(writes).toHaveLength(1);
    await expect(page.getByRole("cell", { name: label })).toHaveCount(0);

    await page.getByRole("button", { name: "创建", exact: true }).click();
    await expect(page.getByTestId("token-secret")).toContainText("mcp_");
    expect(writes).toHaveLength(2);
    await page.getByRole("button", { name: `吊销 ${label}` }).click();
    await expect(page.getByRole("row", { name: new RegExp(`${label} `) }).getByText("已吊销")).toBeVisible();
  });

  test("a draft left by one administrator is not offered to another who signs in on the same tab, and is gone afterwards", async ({ page, context }) => {
    const label = `e2e-iso-${tag()}`;
    await openConsole(page, "metro", "/metro/tokens");
    await expect(page.getByLabel("名称")).toBeVisible();
    const control = await context.newPage();
    await control.goto(`${stack.control}/age?age=${encodeURIComponent(account("metro").email)}`);
    await control.close();
    await page.getByLabel("名称").fill(label);
    await page.getByRole("button", { name: "创建", exact: true }).click();
    await expect(page.getByTestId("problem-reauth")).toBeVisible();
    await page.getByRole("button", { name: "前往 OzID 验证密码" }).click();
    await page.waitForURL(new RegExp(`^${stack.id}/security\\?app_id=admin&return_path=${encodeURIComponent("/metro/tokens")}$`));

    // The first session is gone (ended elsewhere) and a different metro administrator signs in on this tab. What the
    // first one typed is still in this tab's storage; it must not come to the second one.
    await context.clearCookies();
    const writes: string[] = [];
    page.on("request", (r) => METRO_WRITES.test(r.url()) && writes.push(r.url()));
    await openConsole(page, "metro2", "/metro/tokens");
    await expect(page.getByLabel("名称")).toHaveValue("");
    await expect(page.getByText("已恢复")).toHaveCount(0);
    await expect(page.getByRole("cell", { name: label })).toHaveCount(0);
    expect(await page.evaluate(() => sessionStorage.getItem("admin.draft.v1"))).toBeNull();
    expect(writes).toEqual([]);
  });

  test("a change made in the console shows at once in the public catalogue: the version the reader holds goes stale and the read brings the new data", async ({ page }) => {
    const slug = `e2e-ver-${tag()}`;
    // What a public product's page does: an anonymous read of the cities, remembering the version it got.
    const read = async (known: string) => {
      await page.goto(`${stack.trip}/`);
      return page.evaluate(
        async ([api, knownVersion]) => {
          const r = await fetch(`${api}/api/bff.metro.v1.CatalogService/ListCities`, {
            method: "POST",
            headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" },
            body: JSON.stringify({ knownVersion }),
          });
          const body = await r.json();
          return { status: r.status, version: String(body.version ?? "0"), notModified: body.notModified === true, slugs: (body.cities ?? []).map((c: { slug: string }) => c.slug) as string[] };
        },
        [stack.api, known],
      );
    };
    const first = await read("0");
    expect(first.status).toBe(200);
    expect(first.slugs).not.toContain(slug);
    const unchanged = await read(first.version);
    expect(unchanged).toMatchObject({ status: 200, notModified: true, slugs: [] });

    await openConsole(page, "metro", "/metro/cities");
    await page.getByRole("button", { name: "新建城市" }).click();
    await page.getByLabel("标识 slug").fill(slug);
    await nameField(page).fill("版本城");
    await save(page).click();
    await expect(page.getByRole("cell", { name: slug, exact: true })).toBeVisible();

    const after = await read(first.version);
    expect(after.notModified).toBe(false);
    expect(after.slugs).toContain(slug);
    expect(BigInt(after.version)).toBeGreaterThan(BigInt(first.version));
    expect(await read(after.version)).toMatchObject({ notModified: true });

    // Deleting it moves the version again and the reader loses it.
    await page.goto(`${stack.admin}/metro/cities`);
    await page.getByLabel("搜索城市").fill(slug);
    await page.getByRole("button", { name: `删除 ${slug}` }).click();
    await page.getByRole("button", { name: "确认删除" }).click();
    // The dialog closes when the service has answered; an empty table alone could also be a list still loading.
    await expect(page.getByRole("button", { name: "确认删除" })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: slug })).toHaveCount(0);
    const gone = await read(after.version);
    expect(gone.notModified).toBe(false);
    expect(gone.slugs).not.toContain(slug);
  });

  test("a platform administrator without the metro role gets no metro section and no metro call", async ({ page }) => {
    const calls: string[] = [];
    page.on("request", (r) => METRO.test(r.url()) && calls.push(r.url()));
    await openConsole(page, "operator", "/metro/cities");
    await expect(page.getByTestId("no-permission")).toBeVisible();
    await expect(page.getByTestId("sections").getByRole("link", { name: "地铁目录" })).toHaveCount(0);
    await page.goto(`${stack.admin}/metro/tokens`);
    await expect(page.getByTestId("no-permission")).toBeVisible();
    expect(calls).toEqual([]);
  });

  test("outside the allowlist the metro pages cannot be opened and an open console is refused at the BFF, reads included", async ({ page }) => {
    await openConsole(page, "metro", "/metro/cities");
    await expect(page.getByRole("cell", { name: /./ }).first()).toBeVisible();
    const other = await page.context().newPage();
    await setSource(other, stack.outside);
    await other.close();
    // The page that is already open: its next read is refused by the BFF's own perimeter (a bare 403 with no
    // CORS grant, so the browser withholds it and the page can only say it could not be reached).
    await section(page, "审计日志").click();
    await expect(page.getByTestId("problem-network")).toBeVisible();
    await expect(page.getByTestId("no-permission")).toHaveCount(0);
    await section(page, "管理令牌").click();
    await expect(page.getByTestId("problem-network")).toBeVisible();
    // And a fresh load gets not even the page.
    const res = await page.goto(`${stack.admin}/metro/cities`);
    expect(res!.status()).toBe(403);
    await setSource(page, stack.inside);
  });

  test("another product's page cannot use the metro administrator's session for the catalogue administration or the tokens", async ({ page }) => {
    await openConsole(page, "metro", "/metro/cities");
    await page.goto(`${stack.trip}/`);
    await expect(page.locator("#me")).toHaveText(`user:${account("metro").displayName}`);
    for (const call of ["bff.metro.v1.AdminService/ListAdminCities", "bff.metro.v1.AdminService/ExportCatalog", "bff.metro.v1.AdminService/ListAuditLogs", "bff.v1.AccessTokenService/ListAccessTokens"]) {
      const result = await page.evaluate(async ([api, path]) => {
        try {
          const r = await fetch(`${api}/api/${path}`, {
            method: "POST",
            credentials: "include",
            headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" },
            body: "{}",
          });
          return { status: r.status, body: await r.text() };
        } catch {
          return { status: 0, body: "blocked" };
        }
      }, [stack.api, call]);
      expect([0, 403], call).toContain(result.status);
      expect(result.body, call).not.toContain("cities");
      expect(result.body, call).not.toContain("tokens");
      expect(result.body, call).not.toContain("logs");
    }
  });
});
