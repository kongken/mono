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
