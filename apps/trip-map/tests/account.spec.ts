import { expect, test, type Page } from "@playwright/test";
import { createFakeAccounts, installFakeAccounts } from "@kongken/account/testing";
import { fakeBackend } from "./fake-backend";

// The account screens of the trip-map app against a fake BFF (UI only). The real chain, with
// real mail, is in real-mail.spec.ts and runs only when E2E_REAL_MAIL=1.

const EMAIL = "traveller@example.com";
const PASSWORD = "correct horse battery";
const SESSION_KEY = "shanhai-session-v1";

async function setup(page: Page, opts: { signedIn?: boolean } = {}) {
  const backend = fakeBackend(page, { visits: [] });
  const accounts = createFakeAccounts({ navigation: { "trip-map": { origin: "https://trip.example.com" } } });
  await backend.install();
  await installFakeAccounts(page, accounts);
  // Lets a test make the footprint service answer "not signed in", as after an expired or revoked session.
  const gate = { unauthenticated: false };
  await page.route("**/api/bff.tripmap.v1.FootprintService/*", (route) =>
    gate.unauthenticated
      ? route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ code: "unauthenticated", message: "sign in required" }) })
      : route.fallback(),
  );
  const account = accounts.addAccount({ email: EMAIL, password: PASSWORD, displayName: "旅行者小山" });
  if (opts.signedIn) accounts.signIn(account);
  return { backend, accounts, account, gate };
}

const panel = (page: Page) => page.getByTestId("auth-panel");
const dialog = (page: Page) => page.getByRole("dialog");
const accountButton = (page: Page) => page.getByRole("button", { name: "账号", exact: true });
const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const stored = (page: Page) => page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));

async function fillLogin(page: Page, email: string, password: string) {
  await panel(page).getByLabel("邮箱").fill(email);
  await panel(page).getByLabel("密码", { exact: true }).fill(password);
  await panel(page).getByRole("button", { name: "登录", exact: true }).click();
}

async function openSettings(page: Page) {
  await accountButton(page).click();
  await dialog(page).getByRole("button", { name: "账号设置" }).click();
  await expect(dialog(page).getByRole("heading", { name: "账号设置" })).toBeVisible();
}

/** Saves one new city; with the footprint service refusing, it stays unsynced in the account cache. */
async function addCity(page: Page) {
  await page.getByRole("button", { name: "添加足迹" }).first().click();
  const selects = page.getByRole("dialog").locator("select");
  await selects.nth(0).selectOption("330000");
  await selects.nth(1).selectOption("330100");
  await page.getByRole("dialog").getByRole("button", { name: "保存足迹" }).click();
}

test.describe("sign in", () => {
  test("a traveller signs in from the account window with email and password", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(panel(page).getByLabel("邮箱")).toHaveAttribute("autocomplete", "username");
    await expect(panel(page).getByLabel("密码", { exact: true })).toHaveAttribute("autocomplete", "current-password");
    await expect(panel(page).getByLabel("密码", { exact: true })).toHaveAttribute("type", "password");
    await fillLogin(page, `  ${EMAIL.toUpperCase()} `, PASSWORD);
    await expect(accountButton(page)).toBeVisible();
    await expect(dialog(page)).toHaveCount(0);
    expect(accounts.state.writesWithoutCsrf).toEqual([]);
    // The session is the HttpOnly cookie: the page keeps no password or token.
    const kept = await stored(page);
    expect(kept).not.toContain(PASSWORD);
    expect(kept).not.toMatch(/token|csrf/i);
    // It stays signed in after a reload.
    await page.reload();
    await expect(accountButton(page)).toBeVisible();
    expect(await noOverflow(page)).toBe(true);
  });

  test("a wrong password and an unknown email read alike; field mistakes stop before any request", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await panel(page).getByRole("button", { name: "登录", exact: true }).click();
    await expect(panel(page).getByLabel("邮箱")).toHaveAttribute("aria-invalid", "true");
    expect(accounts.state.calls).not.toContain("LoginWithEmail");
    await fillLogin(page, EMAIL, "not the password!!");
    const first = await panel(page).getByRole("alert").innerText();
    // The password field is emptied after a failure.
    await fillLogin(page, "nobody@example.com", "not the password!!");
    await expect(panel(page).getByRole("alert")).toHaveText(first);
    expect(first).not.toMatch(/不存在|未注册/);
    await expect(page.getByRole("button", { name: "登录", exact: true }).first()).toBeVisible();
  });

  test("registration asks for the mail first, and says the same thing for any address", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await panel(page).getByRole("button", { name: "没有账号？申请注册" }).click();
    await panel(page).getByLabel("邮箱").fill("new@example.com");
    await panel(page).getByRole("button", { name: "发送验证邮件" }).click();
    await expect(page.getByTestId("register-sent")).toBeVisible();
    const sent = await page.getByTestId("register-sent").innerText();
    expect(accounts.state.mails).toHaveLength(1);
    await page.getByTestId("register-sent").getByRole("button", { name: "换一个邮箱" }).click();
    await panel(page).getByLabel("邮箱").fill(EMAIL);
    await panel(page).getByRole("button", { name: "发送验证邮件" }).click();
    await expect(page.getByTestId("register-sent")).toBeVisible();
    // The text does not depend on whether the address can register (only the echoed address differs).
    expect((await page.getByTestId("register-sent").innerText()).replace(EMAIL, "<e>")).toBe(sent.replace("new@example.com", "<e>"));
    // Nothing is mailed to an address that already has an account.
    expect(accounts.state.mails).toHaveLength(1);
  });
});

test.describe("mailed links", () => {
  test("the registration link opens a page that clears the token, consumes nothing until submit and starts no app", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await panel(page).getByRole("button", { name: "没有账号？申请注册" }).click();
    await panel(page).getByLabel("邮箱").fill("new@example.com");
    await panel(page).getByRole("button", { name: "发送验证邮件" }).click();
    await expect(page.getByTestId("register-sent")).toBeVisible();
    const mail = accounts.lastMail("registration")!;
    expect(mail.link).toMatch(/^\/account\/verify-email#token=[\w-]{43}$/);

    const requested: string[] = [];
    page.on("request", (r) => requested.push(r.url()));
    const callsBefore = accounts.state.calls.length;
    const res = await page.goto(mail.link);
    expect(res!.headers()["referrer-policy"]).toBe("no-referrer");
    expect(res!.headers()["cache-control"]).toBe("no-store");
    await expect(page.getByRole("heading", { name: "验证邮箱并设置密码" })).toBeVisible();
    expect(new URL(page.url()).hash).toBe("");
    expect(page.url()).not.toContain(mail.token);
    expect(await stored(page)).not.toContain(mail.token);
    expect(await page.evaluate(() => document.querySelector('meta[name="referrer"]')?.getAttribute("content"))).toBe("no-referrer");
    // The page is only this form: no session, footprint or status request, and the token went nowhere.
    expect(accounts.state.calls.slice(callsBefore)).toEqual([]);
    expect(requested.filter((u) => u.includes("/api/"))).toEqual([]);
    expect(requested.filter((u) => u.includes(mail.token))).toEqual([]);
    expect(await page.evaluate(() => [...document.scripts].map((s) => s.src).filter((s) => !s.startsWith(location.origin)))).toEqual([]);
    expect(await noOverflow(page)).toBe(true);

    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByLabel("密码", { exact: true })).toHaveAttribute("aria-invalid", "true");
    await page.getByLabel("昵称（可选）").fill("新旅行者");
    await page.getByLabel("密码", { exact: true }).fill("a long enough password");
    await page.getByLabel("再次输入密码").fill("a long enough password");
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("landing-done")).toContainText("注册完成");
    expect(accounts.state.current).toBeNull();

    // "Log in" leads to the app with the sign-in form open, and the query is cleaned up.
    await page.getByRole("link", { name: "去登录" }).click();
    await expect(panel(page)).toBeVisible();
    expect(new URL(page.url()).search).toBe("");
    await fillLogin(page, "new@example.com", "a long enough password");
    await expect(accountButton(page)).toBeVisible();
  });

  test("a used or missing token shows the same unusable-link page", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/account/verify-email");
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    await page.goto("/account/reset-password#token=" + "a".repeat(43));
    await page.getByLabel("新密码", { exact: true }).fill("a brand new password");
    await page.getByLabel("再次输入新密码").fill("a brand new password");
    await page.getByRole("button", { name: "重置密码" }).click();
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    expect(accounts.state.current).toBeNull();
  });

  test("forgotten password: neutral answer, then a reset that does not sign in and replaces the password", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await panel(page).getByRole("button", { name: "忘记密码？" }).click();
    await panel(page).getByLabel("邮箱").fill(EMAIL);
    await panel(page).getByRole("button", { name: "发送重置邮件" }).click();
    await expect(page.getByTestId("forgot-sent")).toBeVisible();
    const mail = accounts.lastMail("reset")!;
    await page.goto(mail.link);
    expect(new URL(page.url()).hash).toBe("");
    await page.getByLabel("新密码", { exact: true }).fill("a brand new password");
    await page.getByLabel("再次输入新密码").fill("a brand new password");
    await page.getByRole("button", { name: "重置密码" }).click();
    await expect(page.getByTestId("landing-done")).toBeVisible();
    // Resetting does not sign in.
    expect(accounts.state.current).toBeNull();
    await page.getByRole("link", { name: "去登录" }).click();
    await fillLogin(page, EMAIL, PASSWORD);
    await expect(panel(page).getByRole("alert")).toBeVisible();
    await fillLogin(page, EMAIL, "a brand new password");
    await expect(accountButton(page)).toBeVisible();
  });
});

test.describe("account settings", () => {
  test("email, profile, devices and password", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    accounts.state.otherSessions = 1;
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    await openSettings(page);
    await expect(dialog(page).getByLabel("登录邮箱")).toHaveValue(EMAIL);

    const profile = page.getByTestId("profile-form");
    await profile.getByLabel("昵称").fill("   ");
    await profile.getByRole("button", { name: "保存资料" }).click();
    await expect(profile.getByText("请输入昵称")).toBeVisible();
    await profile.getByLabel("昵称").fill("夜行旅人");
    await profile.getByRole("button", { name: "保存资料" }).click();
    await expect(profile.getByRole("status")).toContainText("资料已保存");
    expect(JSON.parse(await page.evaluate((k) => localStorage.getItem(k)!, SESSION_KEY)).user.displayName).toBe("夜行旅人");

    const sessions = page.getByTestId("sessions");
    await expect(sessions.getByText("当前设备")).toBeVisible();
    await expect(sessions.getByText("其他设备")).toBeVisible();
    await sessions.getByRole("button", { name: /退出其他设备/ }).click();
    await expect(sessions.getByText("其他设备")).toHaveCount(0);

    const pw = page.getByTestId("password-form");
    await pw.getByLabel("当前密码").fill("not my password");
    await pw.getByLabel("新密码", { exact: true }).fill("a brand new password");
    await pw.getByLabel("再次输入新密码").fill("a brand new password");
    await pw.getByRole("button", { name: "修改密码" }).click();
    await expect(pw.getByText("当前密码不正确")).toBeVisible();
    await pw.getByLabel("当前密码").fill(PASSWORD);
    await pw.getByLabel("新密码", { exact: true }).fill("a brand new password");
    await pw.getByLabel("再次输入新密码").fill("a brand new password");
    await pw.getByRole("button", { name: "修改密码" }).click();
    // Every session ended, this one included, and nothing was unsynced: signed out, ready for the new password.
    await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();
    expect(await page.evaluate((k) => localStorage.getItem(k), SESSION_KEY)).toBeNull();
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await fillLogin(page, EMAIL, "a brand new password");
    await expect(accountButton(page)).toBeVisible();
    expect(await noOverflow(page)).toBe(true);
  });

  test("a password change with unsynced edits keeps them and asks to sign in again", async ({ page }) => {
    const { accounts, gate } = await setup(page, { signedIn: true });
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    gate.unauthenticated = true;
    await addCity(page);
    await expect(page.locator(".local-status")).toContainText("待同步");

    await openSettings(page);
    const pw = page.getByTestId("password-form");
    await pw.getByLabel("当前密码").fill(PASSWORD);
    await pw.getByLabel("新密码", { exact: true }).fill("a brand new password");
    await pw.getByLabel("再次输入新密码").fill("a brand new password");
    await pw.getByRole("button", { name: "修改密码" }).click();

    // The account window shows the sign-in form on top of the kept edits.
    const expired = page.getByTestId("session-expired");
    await expect(expired).toBeVisible();
    await expect(expired).toContainText("未同步的修改仍保留在本机");
    expect(await page.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith("shanhai-cloud-v2:")))).toHaveLength(1);
    expect(await page.evaluate((k) => localStorage.getItem(k), SESSION_KEY)).not.toBeNull();
    expect(await noOverflow(page)).toBe(true);

    // Signing in again, with the service reachable, syncs them.
    gate.unauthenticated = false;
    await fillLogin(page, EMAIL, "a brand new password");
    await expect(page.getByTestId("session-expired")).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(page.locator(".local-status")).toContainText("已同步到云端");
    expect(accounts.state.current?.email).toBe(EMAIL);
  });

  test("a session that stops working keeps the cache and offers sign-in and export", async ({ page }) => {
    const { accounts, gate } = await setup(page, { signedIn: true });
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    gate.unauthenticated = true;
    accounts.signOut();
    await addCity(page);
    await expect(page.locator(".local-status")).toContainText("待同步");
    await accountButton(page).click();
    const expired = page.getByTestId("session-expired");
    await expect(expired).toBeVisible();
    const download = page.waitForEvent("download");
    await expired.getByRole("button", { name: "导出账号缓存" }).click();
    expect((await download).suggestedFilename()).toContain("账号");
    expect(await page.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith("shanhai-cloud-v2:")))).toHaveLength(1);
  });
});

test.describe("an expired session with nothing cached", () => {
  test("the full-screen failure page offers signing in again, and signing in loads the footprints", async ({ page }) => {
    const { accounts, gate } = await setup(page, { signedIn: true });
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    // The session stops working and this device holds no cache for the account.
    gate.unauthenticated = true;
    accounts.signOut();
    await page.evaluate(() => {
      for (const k of Object.keys(localStorage)) if (k.startsWith("shanhai-cloud-v2:")) localStorage.removeItem(k);
    });
    await page.reload();
    const gateSignIn = page.getByTestId("gate-sign-in");
    await expect(page.getByRole("heading", { name: "登录已失效" })).toBeVisible();
    await expect(gateSignIn).toBeVisible();
    expect(await noOverflow(page)).toBe(true);
    await expect(gateSignIn.getByLabel("密码", { exact: true })).toHaveAttribute("autocomplete", "current-password");
    // The same page still offers the export-and-leave route and a retry.
    await expect(page.getByRole("button", { name: "导出原始账号缓存并退出" })).toBeVisible();

    gate.unauthenticated = false;
    await fillLogin(page, EMAIL, PASSWORD);
    await expect(page.getByTestId("gate-sign-in")).toHaveCount(0);
    await expect(accountButton(page)).toBeVisible();
  });
});

test.describe("closing the account", () => {
  test("lists what goes, needs the password and a typed word, and survives a failed cleanup", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    const local = { version: 1, visits: [], provinces: { "330000": "#4b937d" } };
    await page.addInitScript((data) => {
      if (!localStorage.getItem("shanhai-travel-v1")) localStorage.setItem("shanhai-travel-v1", JSON.stringify(data));
    }, local);
    await page.goto("/");
    await page.getByRole("button", { name: "稍后再说" }).click();
    await openSettings(page);
    const del = page.getByTestId("delete-account");
    // The whole scope: the shared account and every product's data, not just this product.
    await expect(del).toContainText("共用账号");
    await expect(del).toContainText("山海足迹");
    await expect(del).toContainText("地铁足迹");
    await expect(del).not.toContainText(/Logto|用户中心/);
    await expect(dialog(page).getByRole("button", { name: "删除云端数据" })).toHaveCount(0);
    await del.getByRole("button", { name: "注销账号…" }).click();
    const confirm = del.getByRole("button", { name: "确认永久注销" });
    await expect(confirm).toBeDisabled();
    await del.getByLabel("输入“注销”确认").fill("删除");
    await expect(confirm).toBeDisabled();
    await del.getByLabel("输入“注销”确认").fill("注销");
    await del.getByLabel("当前密码").fill("wrong password!!!");
    await confirm.click();
    await expect(del.getByText("当前密码不正确")).toBeVisible();
    expect(accounts.state.calls).not.toContain("DeleteAccount");

    accounts.state.deletionFailures = 1;
    await del.getByLabel("当前密码").fill(PASSWORD);
    await confirm.click();
    await expect(del.getByRole("button", { name: "重试注销" })).toBeVisible();
    const sent = accounts.state.bodies.get("DeleteAccount")![0];
    expect(sent.confirmation).toBe("DELETE");
    expect(sent.mutationId).toMatch(/^del-[\w-]+$/);
    expect("password" in sent).toBe(false);
    // The pending state survives a reload: the account is closed, so only the retry is offered, and nothing syncs.
    await page.reload();
    await openSettings(page);
    await expect(page.getByTestId("profile-form")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "重试注销" })).toBeVisible();
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();
    expect(accounts.state.accounts).toEqual([]);
    expect(await page.evaluate((k) => localStorage.getItem(k), SESSION_KEY)).toBeNull();
    expect(await page.evaluate(() => Object.keys(localStorage).filter((k) => k.includes("user-")))).toEqual([]);
    // Footprints saved on this device before signing in are not account data.
    expect(await page.evaluate(() => JSON.parse(localStorage.getItem("shanhai-travel-v1")!))).toEqual(local);
    expect(await noOverflow(page)).toBe(true);
  });

  test("a stale password check is asked for again and nothing starts", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    await page.goto("/");
    await expect(accountButton(page)).toBeVisible();
    await openSettings(page);
    const del = page.getByTestId("delete-account");
    await del.getByRole("button", { name: "注销账号…" }).click();
    await del.getByLabel("输入“注销”确认").fill("注销");
    await del.getByLabel("当前密码").fill(PASSWORD);
    accounts.failNext("DeleteAccount", { status: 400, code: "failed_precondition", message: "enter your password again to continue" });
    await del.getByRole("button", { name: "确认永久注销" }).click();
    await expect(del.getByText("请先输入当前密码再继续")).toBeVisible();
    expect(accounts.state.accounts).toHaveLength(1);
    // Sync was paused for the attempt and is back: the page is still usable.
    await page.getByRole("dialog").getByRole("button", { name: "返回" }).click();
    await expect(page.getByTestId("session-expired")).toHaveCount(0);
  });
});
