import { expect, test, type Page } from "@playwright/test";
import { FAILURES } from "@kongken/account/testing";
import { OZID, setup } from "./fake";

// The console's screens against a fake BFF: UI behaviour only. The same rules against the real
// chain (perimeter, roles, queries) are in internal/e2e (TestChainUserAdmin*) and the real-stack spec.

const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const rows = (page: Page) => page.getByTestId("user-row");
const search = async (page: Page, by: "邮箱（精确）" | "用户 ID" | "昵称（包含）", value: string) => {
  await page.getByLabel("查找方式").selectOption({ label: by });
  await page.getByLabel("查找内容").fill(value);
  await page.getByRole("button", { name: "查询" }).click();
};

test.describe("sign-in and the role gate", () => {
  test("an anonymous visitor is sent to OzID login and comes back to the page they asked for", async ({ page }) => {
    const s = await setup(page, { signedIn: false });
    await page.goto("/users");
    await page.waitForURL(`${OZID}/login?**`);
    const url = new URL(page.url());
    expect(url.searchParams.get("app_id")).toBe("admin");
    expect(url.searchParams.get("return_path")).toBe("/users");
    expect(s.admin.userCalls()).toHaveLength(0);
  });

  test("a signed-in account with no management role sees no permission, stays put and loads nothing", async ({ page }) => {
    const s = await setup(page, { roles: [] });
    await page.goto("/users");
    await expect(page.getByTestId("no-permission")).toBeVisible();
    await expect(page.getByRole("heading", { name: "没有权限" })).toBeVisible();
    // No redirect loop: a second look later is the same page, and OzID was never opened.
    await page.waitForTimeout(600);
    expect(new URL(page.url()).pathname).toBe("/users");
    expect(s.visited).toHaveLength(0);
    expect(s.admin.userCalls()).toHaveLength(0);
    await expect(page.getByTestId("sections")).toHaveCount(0);
    await expect(page.getByRole("link", { name: "管理 OzID 账号" }).first()).toHaveAttribute("href", OZID);
  });

  test("a metro-admin alone gets no user section and no user data, even by address", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/");
    await expect(page.getByTestId("sections")).toHaveCount(0);
    await expect(page.getByText("地铁目录管理还没有迁入控制台")).toBeVisible();
    await page.goto("/users");
    await expect(page.getByTestId("no-permission")).toBeVisible();
    expect(s.admin.userCalls()).toHaveLength(0);
  });

  test("an ozid-admin sees the user section; holding both roles shows both roles", async ({ page }) => {
    await setup(page, { roles: ["ozid-admin", "metro-admin"] });
    await page.goto("/");
    await expect(page.getByTestId("my-roles")).toHaveText("平台管理员、地铁目录管理员");
    await expect(page.getByTestId("sections").getByRole("link", { name: "用户" })).toBeVisible();
    await expect(page.getByTestId("section-cards")).toBeVisible();
  });

  test("an unknown address asks nobody to sign in and shows no data", async ({ page }) => {
    const s = await setup(page, { signedIn: false });
    await page.goto("/nope/at/all");
    await expect(page.getByTestId("not-found")).toBeVisible();
    expect(s.visited).toHaveLength(0);
    expect(s.admin.userCalls()).toHaveLength(0);
  });

  test("signing out goes back to OzID login", async ({ page }) => {
    await setup(page, { roles: ["ozid-admin"] });
    await page.goto("/");
    await page.getByRole("button", { name: "退出登录" }).click();
    await page.waitForURL(`${OZID}/login?**`);
    expect(new URL(page.url()).searchParams.get("app_id")).toBe("admin");
  });

  test("a failure to ask for the session is shown as a failure, not as being signed out", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    s.accounts.failNext("Me", FAILURES.unavailable);
    await page.goto("/users");
    await expect(page.getByTestId("session-unavailable")).toBeVisible();
    expect(s.visited).toHaveLength(0);
    await page.getByRole("button", { name: "重试" }).click();
    await expect(page.getByRole("heading", { name: "用户" })).toBeVisible();
    await expect(rows(page).first()).toBeVisible();
  });
});

test.describe("user queries", () => {
  test("lists the newest accounts first, 50 to a page, and pages forward and back", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await page.goto("/users");
    await expect(rows(page)).toHaveCount(50);
    await expect(rows(page).first()).toContainText("user125@example.com");
    await expect(page.getByTestId("page-number")).toHaveText("第 1 页");
    await page.getByRole("button", { name: "下一页" }).click();
    await expect(page.getByTestId("page-number")).toHaveText("第 2 页");
    await expect(rows(page).first()).toContainText("user75@example.com");
    await page.getByRole("button", { name: "下一页" }).click();
    await expect(rows(page)).toHaveCount(25);
    await expect(page.getByRole("button", { name: "下一页" })).toBeDisabled();
    await page.getByRole("button", { name: "上一页" }).click();
    await expect(rows(page).first()).toContainText("user75@example.com");
    expect(await noOverflow(page)).toBe(true);
    expect(s.admin.userCalls().every((c) => c.origin === "http://127.0.0.1:4177")).toBe(true);
  });

  test("looks an account up by exact email, by id and by literal nickname", async ({ page }) => {
    await setup(page, { roles: ["ozid-admin"] });
    await page.goto("/users");
    await search(page, "邮箱（精确）", " USER42@Example.com ");
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText("user42@example.com");
    await search(page, "邮箱（精确）", "user4@example.com");
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText("user4@example.com");
    await search(page, "用户 ID", "00000000-0000-4000-8000-000000000010");
    await expect(rows(page)).toHaveCount(1);
    // % and _ are ordinary characters, not wildcards.
    await search(page, "昵称（包含）", "0%_l");
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText("100%_literal");
    await search(page, "昵称（包含）", "%");
    await expect(rows(page)).toHaveCount(1);
    await search(page, "邮箱（精确）", "nobody@example.com");
    await expect(page.getByTestId("no-users")).toBeVisible();
  });

  test("filters by status and role and clears the conditions", async ({ page }) => {
    await setup(page, { roles: ["ozid-admin"] });
    await page.goto("/users");
    await page.getByLabel("角色").selectOption("ozid-admin");
    await page.getByRole("button", { name: "查询" }).click();
    await expect(rows(page)).toHaveCount(2);
    await page.getByLabel("角色").selectOption("");
    await page.getByLabel("账号状态").selectOption("disabled");
    await page.getByRole("button", { name: "查询" }).click();
    await expect(rows(page)).toHaveCount(3);
    await page.getByRole("button", { name: "清除条件" }).click();
    await expect(rows(page)).toHaveCount(50);
  });

  test("a detail page shows only the necessary fields; nothing personal reaches the address or storage", async ({ page }) => {
    await setup(page, { roles: ["ozid-admin"] });
    await page.goto("/users");
    await search(page, "邮箱（精确）", "user2@example.com");
    await rows(page).first().getByRole("link").click();
    await expect(page.getByTestId("detail-email")).toHaveText("user2@example.com");
    await expect(page.getByTestId("detail-roles")).toHaveText("平台管理员");
    await expect(page.getByTestId("detail-id")).toHaveText("00000000-0000-4000-8000-000000000002");
    await expect(page.getByText(/password|token|令牌哈希/i)).toHaveCount(0);
    const stored = await page.evaluate(() => JSON.stringify([{ ...localStorage }, { ...sessionStorage }]));
    expect(stored).not.toContain("example.com");
    expect(page.url()).not.toContain("user2@");
    await page.getByRole("link", { name: /返回用户列表/ }).click();
    await expect(page.getByRole("heading", { name: "用户" })).toBeVisible();
    // The list was searched with an email: that is not in the address, so going back never replays it.
    expect(page.url()).not.toContain("@");
  });

  test("an account that is gone says so", async ({ page }) => {
    await setup(page, { roles: ["ozid-admin"] });
    await page.goto("/users/00000000-0000-4000-8000-0000000fffff");
    await expect(page.getByTestId("user-missing")).toBeVisible();
  });

  test("a slow older answer never replaces a newer one", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await page.goto("/users");
    await expect(rows(page)).toHaveCount(50);
    const slow = s.admin.holdNext();
    await search(page, "邮箱（精确）", "user5@example.com");
    await slow.arrived;
    await search(page, "邮箱（精确）", "user6@example.com");
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText("user6@example.com");
    slow.release();
    await page.waitForTimeout(300);
    await expect(rows(page).first()).toContainText("user6@example.com");
  });
});

test.describe("refusals keep their meaning", () => {
  test("an old password check offers confirmation at OzID without redirecting by itself", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    s.admin.state.reauthNeeded = true;
    await page.goto("/users");
    await expect(page.getByTestId("problem-reauth")).toBeVisible();
    await page.waitForTimeout(400);
    expect(s.visited).toHaveLength(0);
    await page.getByRole("button", { name: "前往 OzID 验证密码" }).click();
    await page.waitForURL(`${OZID}/security?**`);
    const url = new URL(page.url());
    expect(url.searchParams.get("app_id")).toBe("admin");
    expect(url.searchParams.get("return_path")).toBe("/users");
  });

  test("a session lost during a query goes back to OzID login", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await page.goto("/users");
    await expect(rows(page).first()).toBeVisible();
    s.accounts.signOut();
    await page.getByLabel("角色").selectOption("ozid-admin");
    await page.getByRole("button", { name: "查询" }).click();
    await page.waitForURL(`${OZID}/login?**`);
  });

  test("a closed network perimeter is said plainly and is not mistaken for a permission or a sign-out", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    s.admin.state.perimeterClosed = true;
    await page.goto("/users");
    await expect(page.getByTestId("problem-not_allowed")).toBeVisible();
    await expect(page.getByTestId("no-permission")).toHaveCount(0);
    expect(s.visited).toHaveLength(0);
    await expect(page.getByRole("button", { name: "重试" })).toHaveCount(0);
  });

  test("rate limiting and an unavailable service can be retried", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    s.admin.state.rateLimited = 1;
    await page.goto("/users");
    await expect(page.getByTestId("problem-rate_limited")).toBeVisible();
    s.admin.state.unavailable = 1;
    await page.getByRole("button", { name: "重试" }).click();
    await expect(page.getByTestId("problem-unavailable")).toBeVisible();
    await page.getByRole("button", { name: "重试" }).click();
    await expect(rows(page)).toHaveCount(50);
  });
});

test("the console works on a narrow screen", async ({ page }) => {
  await setup(page, { roles: ["ozid-admin"] });
  await page.goto("/users");
  await expect(rows(page).first()).toBeVisible();
  expect(await noOverflow(page)).toBe(true);
});

// ---- account changes -------------------------------------------------------------------------------
// UI behaviour against the fake BFF, in the desktop and the mobile project alike. The same rules against the
// real stack are in internal/e2e (TestChainUserAdminChanges*, ...Writes*, ...Forbidden*) and the real-stack spec.

const uid = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const ACTIVE = uid(5); // an ordinary active account
const DISABLED = uid(40); // disabled
const CLOSING = uid(3); // being closed
const PLATFORM = uid(1); // holds ozid-admin
const METRO = uid(9); // holds metro-admin
const changes = (s: Awaited<ReturnType<typeof setup>>) => s.admin.state.calls.filter((c) => /User$|MetroAdmin$/.test(c.method) && c.method !== "GetUser");

const open = async (page: Page, id: string) => {
  await page.goto(`/users/${id}`);
  await expect(page.getByTestId("detail-id")).toHaveText(id);
};
const confirm = async (page: Page, reason = "工单 77：用户申请") => {
  await page.getByTestId("change-reason").fill(reason);
  await page.getByTestId("change-confirm").scrollIntoViewIfNeeded();
  await page.getByTestId("change-confirm").click();
};

test.describe("account changes", () => {
  test("disabling shows the target and the impact, needs a reason, and sends only what was asked", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await open(page, ACTIVE);
    await expect(page.getByTestId("act-status")).toHaveText("禁用账号");
    await page.getByTestId("act-status").click();

    const dialog = page.getByTestId("change-dialog");
    await expect(dialog).toBeVisible();
    await expect(page.getByTestId("change-target")).toContainText("user5@example.com");
    await expect(page.getByTestId("change-target")).toContainText(ACTIVE);
    await expect(page.getByTestId("change-now")).toContainText("状态：正常");
    await expect(page.getByTestId("change-after")).toContainText("状态：已禁用");
    await expect(page.getByTestId("change-impact")).toContainText("退出");
    await expect(page.getByTestId("change-impact")).toContainText("不会恢复");
    expect(await noOverflow(page)).toBe(true);

    // No reason: nothing is sent.
    await page.getByTestId("change-confirm").scrollIntoViewIfNeeded();
    await page.getByTestId("change-confirm").click();
    await expect(dialog.getByText("请填写原因")).toBeVisible();
    await page.getByTestId("change-reason").fill("联系 bob@example.com");
    await page.getByTestId("change-confirm").click();
    await expect(dialog.getByText("不要写邮箱、密码或令牌")).toBeVisible();
    expect(changes(s)).toHaveLength(0);

    await confirm(page);
    await expect(page.getByTestId("change-notice")).toContainText("账号已禁用");
    await expect(dialog).toHaveCount(0);
    await expect(page.getByTestId("detail-status")).toHaveText("已禁用");
    await expect(page.getByTestId("act-status")).toHaveText("启用账号");
    await expect(page.getByTestId("act-role")).toBeDisabled();
    await expect(page.getByTestId("act-role-hint")).toContainText("先启用");

    const [call] = changes(s);
    expect(call!.method).toBe("DisableUser");
    expect(Object.keys(call!.body).sort()).toEqual(["expectedAuthVersion", "mutationId", "reason", "userId"]);
    expect(call!.body).toMatchObject({ userId: ACTIVE, expectedAuthVersion: "1", reason: "工单 77：用户申请" });
    expect(call!.body.mutationId).toMatch(/^[A-Za-z0-9._-]{1,64}$/);
    expect(call!.origin).toBe("http://127.0.0.1:4177");
    expect(s.admin.state.writesWithoutCsrf).toEqual([]);
    expect(s.admin.state.users.find((u) => u.id === ACTIVE)!.status).toBe("DISABLED");
    expect(await noOverflow(page)).toBe(true);
  });

  test("enabling a disabled account, then granting and revoking the metro role", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await open(page, DISABLED);
    await page.getByTestId("act-status").click();
    await expect(page.getByTestId("change-impact")).toContainText("不会恢复");
    await confirm(page);
    await expect(page.getByTestId("detail-status")).toHaveText("正常");
    await expect(page.getByTestId("act-role")).toHaveText("授予地铁目录管理员");

    await page.getByTestId("act-role").click();
    await expect(page.getByTestId("change-after")).toContainText("地铁目录管理员");
    await confirm(page, "工单 78");
    await expect(page.getByTestId("detail-roles")).toHaveText("地铁目录管理员");
    await expect(page.getByTestId("act-role")).toHaveText("收回地铁目录管理员");

    // The page now names the version the server answered with: the next change is not stale.
    await page.getByTestId("act-role").click();
    await confirm(page, "工单 79");
    await expect(page.getByTestId("detail-roles")).toHaveText("无");
    const sent = changes(s).map((c) => [c.method, c.body.expectedAuthVersion]);
    expect(sent).toEqual([["EnableUser", "1"], ["GrantMetroAdmin", "2"], ["RevokeMetroAdmin", "3"]]);
  });

  test("a request that changes nothing says so", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await open(page, METRO);
    s.admin.state.beforeNextChange = (users) => {
      // Somebody already revoked the role, without moving the version this page knows (not possible for real; the
      // server's unchanged answer is what is shown).
      users.find((u) => u.id === METRO)!.roles = [];
    };
    await page.getByTestId("act-role").click();
    await confirm(page);
    await expect(page.getByTestId("change-notice")).toContainText("没有做任何修改");
  });

  test("platform administrators and closing accounts offer no change at all", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await open(page, PLATFORM);
    await expect(page.getByTestId("actions-protected")).toBeVisible();
    await expect(page.getByTestId("actions")).toHaveCount(0);
    await expect(page.getByRole("button", { name: /禁用|启用|授予|收回/ })).toHaveCount(0);
    await open(page, CLOSING);
    await expect(page.getByTestId("actions-closing")).toContainText("不会取消注销");
    await expect(page.getByRole("button", { name: /禁用|启用|授予|收回/ })).toHaveCount(0);
    expect(changes(s)).toHaveLength(0);
  });

  test("a version conflict changes nothing, asks for a refresh, and the next attempt names the new version", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await open(page, ACTIVE);
    s.admin.state.beforeNextChange = (users) => {
      const u = users.find((x) => x.id === ACTIVE)!;
      u.roles = ["metro-admin"];
      u.authVersion += 1;
    };
    await page.getByTestId("act-status").click();
    await confirm(page);
    await expect(page.getByTestId("change-problem-conflict")).toContainText("发生了变化");
    await expect(page.getByTestId("change-confirm")).toHaveCount(0);
    await expect(page.getByTestId("detail-status")).toHaveText("正常");
    expect(changes(s)).toHaveLength(1);

    await page.getByTestId("change-refresh").click();
    await expect(page.getByTestId("change-dialog")).toHaveCount(0);
    await expect(page.getByTestId("detail-roles")).toHaveText("地铁目录管理员");
    // Nothing was sent again by itself.
    await page.waitForTimeout(300);
    expect(changes(s)).toHaveLength(1);

    await page.getByTestId("act-status").click();
    await confirm(page);
    await expect(page.getByTestId("detail-status")).toHaveText("已禁用");
    expect(changes(s)[1]!.body.expectedAuthVersion).toBe("2");
  });

  test("an old password check goes to OzID and back, and the refused change is never replayed", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await open(page, ACTIVE);
    s.accounts.ageAuthentication();
    await page.getByTestId("act-status").click();
    await confirm(page);
    await expect(page.getByTestId("change-problem-reauth")).toContainText("验证后会回到这个页面");
    await expect(page.getByTestId("change-confirm")).toHaveCount(0);
    await page.waitForTimeout(400);
    expect(s.visited).toHaveLength(0); // no redirect by itself
    expect(changes(s)).toHaveLength(1);

    await page.getByTestId("change-reauth").click();
    await page.waitForURL(`${OZID}/security?**`);
    const url = new URL(page.url());
    expect(url.searchParams.get("app_id")).toBe("admin");
    expect(url.searchParams.get("return_path")).toBe(`/users/${ACTIVE}`);

    // The password is confirmed at OzID; the browser returns to the page it came from.
    s.accounts.state.authenticatedAt = Date.now();
    await page.goto(`/users/${ACTIVE}`);
    await expect(page.getByTestId("detail-id")).toHaveText(ACTIVE);
    await expect(page.getByTestId("change-dialog")).toHaveCount(0);
    await page.waitForTimeout(500);
    expect(changes(s)).toHaveLength(1); // the old write was not sent again
    expect(s.admin.state.users.find((u) => u.id === ACTIVE)!.status).toBe("ACTIVE");

    // The administrator decides again, on purpose.
    await page.getByTestId("act-status").click();
    await confirm(page);
    await expect(page.getByTestId("detail-status")).toHaveText("已禁用");
    expect(changes(s)).toHaveLength(2);
  });

  test("a lost response is retried with the same operation ID and does not change the account twice", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await open(page, ACTIVE);
    s.admin.state.loseNextResponse = true;
    await page.getByTestId("act-status").click();
    await confirm(page);
    await expect(page.getByTestId("change-problem-network")).toContainText("不一定已经生效");
    await expect(page.getByTestId("change-confirm")).toHaveText("用同一操作编号重试");
    expect(s.admin.state.users.find((u) => u.id === ACTIVE)!.status).toBe("DISABLED"); // it did happen

    await page.getByTestId("change-confirm").click();
    await expect(page.getByTestId("change-notice")).toContainText("没有重复执行");
    await expect(page.getByTestId("detail-status")).toHaveText("已禁用");
    const [first, second] = changes(s);
    expect(second!.body).toEqual(first!.body);
    expect(s.admin.state.users.find((u) => u.id === ACTIVE)!.authVersion).toBe(2); // moved once
  });

  test("changing the reason after a failure is a new intent with a new operation ID", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await open(page, ACTIVE);
    s.admin.state.unavailable = 1;
    await page.getByTestId("act-status").click();
    await confirm(page, "第一版原因");
    await expect(page.getByTestId("change-problem-unavailable")).toBeVisible();
    await page.getByTestId("change-reason").fill("第二版原因");
    await page.getByTestId("change-confirm").click();
    await expect(page.getByTestId("detail-status")).toHaveText("已禁用");
    const [first, second] = changes(s);
    expect(second!.body.mutationId).not.toBe(first!.body.mutationId);
    expect(second!.body.reason).toBe("第二版原因");
  });

  test("refusals inside the dialog keep their meaning", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await open(page, ACTIVE);
    s.admin.state.rateLimited = 1;
    await page.getByTestId("act-status").click();
    await confirm(page);
    await expect(page.getByTestId("change-problem-rate_limited")).toBeVisible();
    await page.getByTestId("change-cancel").click();
    await expect(page.getByTestId("change-dialog")).toHaveCount(0);
    // A closed perimeter is not a permission problem, and a lost session goes back to sign-in.
    s.admin.state.perimeterClosed = true;
    await page.getByTestId("act-status").click();
    await confirm(page);
    await expect(page.getByTestId("change-problem-not_allowed")).toBeVisible();
    s.admin.state.perimeterClosed = false;
    s.accounts.signOut();
    await page.getByTestId("change-confirm").click();
    await page.waitForURL(`${OZID}/login?**`);
  });

  test("the dialog works on a narrow screen: nothing overflows and the buttons can be reached", async ({ page }) => {
    await setup(page, { roles: ["ozid-admin"] });
    await open(page, ACTIVE);
    await page.getByTestId("act-status").click();
    await page.getByTestId("change-reason").fill("工单 77");
    for (const id of ["change-confirm", "change-cancel"]) {
      await page.getByTestId(id).scrollIntoViewIfNeeded();
      await expect(page.getByTestId(id)).toBeInViewport();
    }
    expect(await noOverflow(page)).toBe(true);
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("change-dialog")).toHaveCount(0);
    await expect(page.getByTestId("act-status")).toBeFocused();
  });
});
