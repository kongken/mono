import { expect, test, type Page } from "@playwright/test";
import { createFakeAccounts, FAILURES, installFakeAccounts } from "@kongken/account/testing";

// OzID's self-service pages (profile, password, devices) against a fake BFF: UI behaviour only.
// The same rules against the real chain are in internal/e2e (device_sessions_chain_test.go) and,
// from a real browser, tests/real-stack.

const EMAIL = "ozid-user@example.com";
const PASSWORD = "correct horse battery";
const NEW_PASSWORD = "a brand new password";

async function setup(page: Page, opts: { signedIn?: boolean; others?: number } = {}) {
  const accounts = createFakeAccounts({ navigation: { ozid: { origin: "http://127.0.0.1:4176", paths: ["/security"] } } });
  await installFakeAccounts(page, accounts);
  const account = accounts.addAccount({ email: EMAIL, password: PASSWORD, displayName: "Oz 用户" });
  if (opts.signedIn !== false) accounts.signIn(account);
  accounts.state.otherSessions = opts.others ?? 0;
  return { accounts, account };
}

const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const nav = (page: Page) => page.getByRole("navigation", { name: "账号" });

test.describe("navigation", () => {
  test("every self-service page is reachable from the overview and the account navigation", async ({ page }) => {
    await setup(page);
    await page.goto("/");
    for (const [name, url, heading] of [
      ["资料", /\/profile$/, "资料"],
      ["安全", /\/security$/, "安全"],
      ["设备", /\/sessions$/, "设备与登录"],
      ["概览", /\/$/, "账号概览"],
    ] as const) {
      await nav(page).getByRole("link", { name, exact: true }).click();
      await expect(page).toHaveURL(url);
      await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
      expect(await noOverflow(page)).toBe(true);
    }
    // No sign-in, no navigation: the account links only exist with a session.
    await page.getByRole("button", { name: "退出登录" }).click();
    await expect(page).toHaveURL(/\/login$/);
    await expect(nav(page)).toHaveCount(0);
  });

  test("without a session each page leads to sign-in", async ({ page }) => {
    await setup(page, { signedIn: false });
    for (const path of ["/profile", "/security", "/sessions"]) {
      await page.goto(path);
      await expect(page).toHaveURL(/\/login/);
    }
  });

  test("an outage while checking the session is not a sign-out and nothing else is called", async ({ page }) => {
    const { accounts } = await setup(page);
    for (const path of ["/profile", "/security", "/sessions"]) {
      accounts.failNext("Me", FAILURES.unavailable);
      await page.goto(path);
      await expect(page.getByTestId("session-unavailable")).toContainText("这不代表你已退出登录");
      await expect(page).toHaveURL(new RegExp(`${path}$`));
      await page.getByRole("button", { name: "重试" }).click();
      await expect(page.getByTestId("session-unavailable")).toHaveCount(0);
    }
    expect(accounts.state.current).not.toBeNull();
    expect(accounts.state.calls).not.toContain("Logout");
  });
});

test.describe("profile", () => {
  test("the email is read only; nickname and avatar are saved and nothing else is sent", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/profile");
    const form = page.getByTestId("profile-form");
    await expect(form.getByLabel("登录邮箱")).toHaveValue(EMAIL);
    await expect(form.getByLabel("登录邮箱")).toHaveAttribute("readonly", "");
    expect(await form.getByLabel("登录邮箱").evaluate((el) => (el as HTMLInputElement).name)).toBe("");
    await expect(page.getByText("不会改动")).toBeVisible();

    // Mistakes are reported on the field and never reach the server.
    await form.getByLabel("昵称").fill("   ");
    await form.getByRole("button", { name: "保存资料" }).click();
    await expect(form.getByText("请输入昵称")).toBeVisible();
    await expect(form.getByLabel("昵称")).toBeFocused();
    await form.getByLabel("昵称").fill("夜班骑行者");
    await form.getByLabel("头像地址（可选）").fill("http://insecure.example/a.png");
    await form.getByRole("button", { name: "保存资料" }).click();
    await expect(form.getByText("头像需要是 https 地址")).toBeVisible();
    expect(accounts.state.calls).not.toContain("UpdateMyProfile");

    await form.getByLabel("头像地址（可选）").fill("https://cdn.example.com/me.png");
    await form.getByRole("button", { name: "保存资料" }).click();
    await expect(form.getByRole("status")).toContainText("资料已保存");
    await expect(page.getByTestId("header-name")).toHaveText("夜班骑行者");
    const bodies = accounts.state.bodies.get("UpdateMyProfile") ?? [];
    expect(bodies).toHaveLength(1);
    expect(Object.keys(bodies[0]).sort()).toEqual(["avatarUrl", "displayName"]);
    expect(accounts.state.writesWithoutCsrf).toEqual([]);
    // The overview and a reload agree.
    await page.goto("/");
    await expect(page.getByTestId("overview-name")).toHaveText("夜班骑行者");
    await expect(page.getByTestId("overview-email")).toHaveText(EMAIL);
  });

  test("a failed save keeps the form and the session; a lost session leads to sign-in", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/profile");
    const form = page.getByTestId("profile-form");
    await form.getByLabel("昵称").fill("新昵称");
    accounts.failNext("UpdateMyProfile", FAILURES.unavailable);
    await form.getByRole("button", { name: "保存资料" }).click();
    await expect(form.getByRole("alert")).toContainText("服务暂时不可用");
    await expect(form.getByLabel("昵称")).toHaveValue("新昵称");
    await expect(page.getByTestId("header-name")).toHaveText("Oz 用户");

    // The session ended elsewhere: a retry finds out and the page asks for a new sign-in.
    accounts.signOut();
    await form.getByRole("button", { name: "保存资料" }).click();
    await expect(page).toHaveURL(/\/login$/);
  });
});

test.describe("password", () => {
  test("a wrong current password and a weak or mismatched new one are refused in place", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/security");
    const form = page.getByTestId("password-form");
    await form.getByLabel("当前密码").fill("not my password");
    await form.getByLabel("新密码", { exact: true }).fill(NEW_PASSWORD);
    await form.getByLabel("再次输入新密码").fill(NEW_PASSWORD);
    await form.getByRole("button", { name: "修改密码" }).click();
    await expect(form.getByText("当前密码不正确")).toBeVisible();

    await form.getByLabel("当前密码").fill(PASSWORD);
    await form.getByLabel("新密码", { exact: true }).fill("short");
    await form.getByLabel("再次输入新密码").fill("short");
    await form.getByRole("button", { name: "修改密码" }).click();
    await expect(form.getByText(/12/)).toBeVisible();
    await form.getByLabel("新密码", { exact: true }).fill(NEW_PASSWORD);
    await form.getByLabel("再次输入新密码").fill(`${NEW_PASSWORD}!`);
    await form.getByRole("button", { name: "修改密码" }).click();
    await expect(form.getByText("两次输入的密码不一致")).toBeVisible();
    // Only the wrong-password attempt reached the server; the session is intact.
    expect(accounts.state.bodies.get("ChangePassword")).toHaveLength(1);
    expect(accounts.state.current).not.toBeNull();
    await expect(page.getByTestId("header-name")).toBeVisible();
  });

  test("a service failure is not a password change and not a sign-out", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/security");
    const form = page.getByTestId("password-form");
    await form.getByLabel("当前密码").fill(PASSWORD);
    await form.getByLabel("新密码", { exact: true }).fill(NEW_PASSWORD);
    await form.getByLabel("再次输入新密码").fill(NEW_PASSWORD);
    accounts.failNext("ChangePassword", FAILURES.unavailable);
    await form.getByRole("button", { name: "修改密码" }).click();
    await expect(form.getByRole("alert")).toContainText("这次请求没有生效");
    await expect(page).toHaveURL(/\/security$/);
    await expect(page.getByTestId("header-name")).toBeVisible();
    expect(accounts.state.accounts[0].password).toBe(PASSWORD);
  });

  test("a change signs this device out too and ends at sign-in with the new password", async ({ page }) => {
    const { accounts } = await setup(page, { others: 2 });
    await page.goto("/security");
    const form = page.getByTestId("password-form");
    await expect(form.getByText("所有设备（包括当前设备）都会退出登录")).toBeVisible();
    await form.getByLabel("当前密码").fill(PASSWORD);
    await form.getByLabel("新密码", { exact: true }).fill(NEW_PASSWORD);
    await form.getByLabel("再次输入新密码").fill(NEW_PASSWORD);
    await form.getByRole("button", { name: "修改密码" }).click();
    await expect(page).toHaveURL(/\/login\?reason=password-changed$/);
    await expect(page.getByText("密码已修改。所有设备上的登录都已退出")).toBeVisible();
    expect(accounts.state.current).toBeNull();
    expect(Object.keys(accounts.state.bodies.get("ChangePassword")![0]).sort()).toEqual(["currentPassword", "newPassword"]);
    expect(accounts.state.writesWithoutCsrf).toEqual([]);
    // The old password no longer works; the new one does.
    const panel = page.getByTestId("auth-panel");
    await panel.getByLabel("邮箱").fill(EMAIL);
    await panel.getByLabel("密码", { exact: true }).fill(PASSWORD);
    await panel.getByRole("button", { name: "登录", exact: true }).click();
    await expect(panel.getByRole("alert")).toBeVisible();
    await panel.getByLabel("密码", { exact: true }).fill(NEW_PASSWORD);
    await panel.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page.getByTestId("overview")).toBeVisible();
    // Neither password was kept by the page.
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
    expect(stored).not.toContain(PASSWORD);
    expect(stored).not.toContain(NEW_PASSWORD);
  });
});

test.describe("devices", () => {
  test("lists only what the server records and signs out one device", async ({ page }) => {
    const { accounts } = await setup(page, { others: 2 });
    await page.goto("/sessions");
    const list = page.getByTestId("sessions");
    await expect(list.getByText("当前设备")).toHaveCount(1);
    const rows = list.locator("li").filter({ hasText: "其他设备" });
    await expect(rows).toHaveCount(2);
    // Real metadata only: times, no device model, address or place is shown or asked for.
    await expect(list).toContainText("最近使用");
    await expect(list).toContainText("有效期至");
    await expect(list).not.toContainText(/IP|地址|位置|浏览器|型号|iPhone|Windows/);
    expect(await noOverflow(page)).toBe(true);

    await list.getByRole("button", { name: /退出其他设备（最近使用/ }).first().click();
    await expect(rows).toHaveCount(1);
    expect(accounts.state.otherSessions).toBe(1);
  });

  test("sign out other devices is one call that keeps this device and is safe to repeat", async ({ page }) => {
    const { accounts } = await setup(page, { others: 3 });
    await page.goto("/sessions");
    const area = page.getByTestId("sign-out-others");
    await expect(area).toContainText("不影响本设备");
    await expect(area).toContainText("不会修改密码或已创建的管理令牌");
    await area.getByRole("button", { name: "退出其他 3 台设备" }).click();
    await expect(page.getByTestId("sessions").getByRole("status")).toContainText("已退出其他 3 台设备，本设备保持登录");
    await expect(page.getByTestId("sessions").getByText("当前设备")).toHaveCount(1);
    await expect(page.getByTestId("sessions").locator("li").filter({ hasText: "其他设备" })).toHaveCount(0);
    await expect(area.getByRole("button", { name: "没有其他设备" })).toBeDisabled();
    expect(accounts.state.calls.filter((c) => c === "RevokeOtherSessions")).toHaveLength(1);
    expect(accounts.state.writesWithoutCsrf).toEqual([]);
    expect(Object.keys(accounts.state.bodies.get("RevokeOtherSessions")![0])).toEqual([]);
    // Still signed in here, and no sign-out happened.
    await expect(page.getByTestId("header-name")).toBeVisible();
    expect(accounts.state.calls).not.toContain("Logout");
    expect(accounts.state.calls).not.toContain("ChangePassword");
  });

  test("a refused or failed sign-out of other devices says so and changes nothing", async ({ page }) => {
    const { accounts } = await setup(page, { others: 2 });
    await page.goto("/sessions");
    const area = page.getByTestId("sign-out-others");
    accounts.failNext("RevokeOtherSessions", FAILURES.rateLimited);
    await area.getByRole("button", { name: "退出其他 2 台设备" }).click();
    await expect(page.getByTestId("sessions").getByRole("alert")).toContainText("操作太频繁");
    accounts.failNext("RevokeOtherSessions", FAILURES.unavailable);
    await area.getByRole("button", { name: "退出其他 2 台设备" }).click();
    await expect(page.getByTestId("sessions").getByRole("alert")).toContainText("这次请求没有生效");
    expect(accounts.state.otherSessions).toBe(2);
    await expect(page.getByTestId("header-name")).toBeVisible();
  });

  test("a failure to read the list is not a sign-out; a lost session is", async ({ page }) => {
    const { accounts } = await setup(page, { others: 1 });
    accounts.failNext("ListSessions", FAILURES.unavailable);
    await page.goto("/sessions");
    await expect(page.getByTestId("sessions")).toContainText("暂时无法读取设备列表，这不代表你已退出登录");
    await expect(page.getByTestId("header-name")).toBeVisible();
    await page.getByTestId("sessions").getByRole("button", { name: "重试" }).click();
    await expect(page.getByTestId("sessions").getByText("当前设备")).toBeVisible();

    // Now the session really ends elsewhere: the next read finds out and the page leads to sign-in.
    accounts.signOut();
    await page.getByTestId("sign-out-others").getByRole("button").click();
    await expect(page).toHaveURL(/\/login$/);
  });

  test("signing out of this browser explains its reach, keeps other devices and clears no browser data", async ({ page }) => {
    const { accounts } = await setup(page, { others: 1 });
    await page.goto("/sessions");
    const area = page.getByTestId("sign-out-here");
    await expect(area).toContainText("已接入的应用");
    await expect(area).toContainText("下一次检查登录状态");
    await expect(area).toContainText("其他设备保持登录");
    await expect(area).toContainText("不会清除各应用保存在这个浏览器里的数据");
    await page.evaluate(() => localStorage.setItem("kongken.sentinel", "keep"));

    // A failed sign-out is its own message and leaves the session alone.
    accounts.failNext("Logout", FAILURES.unavailable);
    await area.getByRole("button", { name: "退出这个浏览器的登录" }).click();
    await expect(area.getByRole("alert")).toContainText("你仍处于登录状态");
    await expect(page.getByTestId("header-name")).toBeVisible();

    await area.getByRole("button", { name: "退出这个浏览器的登录" }).click();
    await expect(page).toHaveURL(/\/login$/);
    expect(accounts.state.current).toBeNull();
    // Other devices are not touched by it, and OzID does not clear its origin's storage.
    expect(accounts.state.otherSessions).toBe(1);
    expect(await page.evaluate(() => localStorage.getItem("kongken.sentinel"))).toBe("keep");
    expect(accounts.state.calls).not.toContain("RevokeOtherSessions");
  });
});

// Late, overtaken and stale answers. The fake decides each held reply when the call arrives and
// delivers it only on release, so these specs make the answer of an old read land after a newer
// one, a sign-out or a change of account.
const recheck = (page: Page) => page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
const settle = (page: Page) => page.evaluate(() => new Promise<void>((done) => setTimeout(() => requestAnimationFrame(() => done()), 250)));

test.describe("late answers", () => {
  test("an older 'signed in' cannot undo a newer 'not signed in'", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/profile");
    await expect(page.getByTestId("header-name")).toHaveText("Oz 用户");
    const old = accounts.holdNext("Me");
    await recheck(page);
    await old.arrived; // decided while the account was still signed in
    accounts.signOut();
    await recheck(page); // the newer read finds the session gone
    await expect(page).toHaveURL(/\/login$/);
    old.release();
    await settle(page);
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByTestId("signed-in-here")).toHaveCount(0);
    await expect(page.getByTestId("header-name")).toHaveCount(0);
    await expect(nav(page)).toHaveCount(0);
  });

  test("an older 'not signed in' cannot hide a newer valid identity", async ({ page }) => {
    const { accounts, account } = await setup(page);
    await page.goto("/profile");
    await expect(page.getByTestId("header-name")).toHaveText("Oz 用户");
    accounts.signOut();
    const old = accounts.holdNext("Me");
    await recheck(page);
    await old.arrived; // decided while there was no session
    accounts.signIn(account);
    await recheck(page); // the newer read finds a valid session
    await expect(page.getByTestId("header-name")).toHaveText("Oz 用户");
    old.release();
    await settle(page);
    await expect(page).toHaveURL(/\/profile$/);
    await expect(page.getByTestId("header-name")).toHaveText("Oz 用户");
    await expect(page.getByTestId("profile-form")).toBeVisible();
  });

  test("after the account changed, a late answer about the previous account is dropped", async ({ page }) => {
    const { accounts } = await setup(page);
    const other = accounts.addAccount({ email: "ozid-second@example.com", password: PASSWORD, displayName: "第二位用户" });
    await page.goto("/profile");
    await expect(page.getByTestId("header-name")).toHaveText("Oz 用户");
    const old = accounts.holdNext("Me");
    await recheck(page);
    await old.arrived; // still the first account
    accounts.signIn(other);
    await recheck(page);
    await expect(page.getByTestId("header-name")).toHaveText("第二位用户");
    await expect(page.getByTestId("profile-form").getByLabel("登录邮箱")).toHaveValue("ozid-second@example.com");
    old.release();
    await settle(page);
    await expect(page.getByTestId("header-name")).toHaveText("第二位用户");
    await expect(page.getByTestId("profile-form").getByLabel("登录邮箱")).toHaveValue("ozid-second@example.com");
  });

  test("signing out here, a read that began before it cannot sign the page back in", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/sessions");
    await expect(page.getByTestId("header-name")).toBeVisible();
    const old = accounts.holdNext("Me");
    await recheck(page);
    await old.arrived;
    await page.getByRole("button", { name: "退出登录" }).click();
    await expect(page).toHaveURL(/\/login$/);
    expect(accounts.state.current).toBeNull();
    old.release();
    await settle(page);
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByTestId("signed-in-here")).toHaveCount(0);
    await expect(page.getByTestId("header-name")).toHaveCount(0);
  });

  test("after a revoke, an older list read cannot bring the revoked devices back", async ({ page }) => {
    const { accounts } = await setup(page, { others: 2 });
    await page.goto("/sessions");
    const list = page.getByTestId("sessions");
    const otherRows = list.locator("li").filter({ hasText: "其他设备" });
    await expect(otherRows).toHaveCount(2);
    // A revoke whose follow-up read fails leaves the old rows and a retry link on screen.
    accounts.failNext("ListSessions", FAILURES.unavailable);
    await otherRows.first().getByRole("button", { name: /退出/ }).click();
    await expect(list).toContainText("暂时无法读取设备列表");
    expect(accounts.state.otherSessions).toBe(1);
    // The retry is the older read: decided now (one other device) and delivered last.
    const old = accounts.holdNext("ListSessions");
    await list.getByRole("button", { name: "重试" }).click();
    await old.arrived;
    // The other device is revoked next; its follow-up read is the newer one.
    await otherRows.first().getByRole("button", { name: /退出/ }).click();
    await expect(otherRows).toHaveCount(0);
    expect(accounts.state.otherSessions).toBe(0);
    old.release();
    await settle(page);
    await expect(otherRows).toHaveCount(0);
    await expect(list.locator("li")).toHaveCount(1);
    await expect(list.locator("li").first()).toContainText("当前设备");
    await expect(page.getByTestId("sign-out-others").getByRole("button")).toHaveText("没有其他设备");
    await expect(list).not.toContainText("暂时无法读取设备列表");
  });

  test("after the account changed, the old device list goes at once and its late answer is dropped", async ({ page }) => {
    const { accounts } = await setup(page, { others: 2 });
    const other = accounts.addAccount({ email: "ozid-second@example.com", password: PASSWORD, displayName: "第二位用户" });
    await page.goto("/sessions");
    const list = page.getByTestId("sessions");
    const otherRows = list.locator("li").filter({ hasText: "其他设备" });
    await expect(otherRows).toHaveCount(2);
    accounts.failNext("ListSessions", FAILURES.unavailable);
    await otherRows.first().getByRole("button", { name: /退出/ }).click();
    await expect(list).toContainText("暂时无法读取设备列表");
    // The first account's retry is still on its way (it would show one other device).
    const old = accounts.holdNext("ListSessions");
    await list.getByRole("button", { name: "重试" }).click();
    await old.arrived;
    // Another account takes over this browser; it has no other device.
    accounts.signIn(other);
    accounts.state.otherSessions = 0;
    await recheck(page);
    await expect(page.getByTestId("header-name")).toHaveText("第二位用户");
    await expect(list.locator("li")).toHaveCount(1);
    await expect(otherRows).toHaveCount(0);
    await expect(list).not.toContainText("暂时无法读取设备列表");
    old.release();
    await settle(page);
    await expect(list.locator("li")).toHaveCount(1);
    await expect(list.locator("li").first()).toContainText("当前设备");
    await expect(otherRows).toHaveCount(0);
    await expect(page.getByTestId("header-name")).toHaveText("第二位用户");
  });
});
