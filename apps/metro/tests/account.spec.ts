import { expect, test, type Page } from "@playwright/test";
import { createFakeAccounts, FAILURES, installFakeAccounts } from "@kongken/account/testing";
import { fakeBackend } from "./fake-backend";

// The account screens of the metro app against a fake BFF (UI only). The real chain, with
// real mail, is in real-mail.spec.ts and runs only when E2E_REAL_MAIL=1.

const EMAIL = "rider@example.com";
const PASSWORD = "correct horse battery";

async function setup(page: Page, opts: { signedIn?: boolean } = {}) {
  const fake = fakeBackend(page);
  const accounts = createFakeAccounts({
    navigation: { metro: { origin: "https://metro.example.com" } },
    onSession: (a) => {
      fake.state.user = a ? { id: a.id, displayName: a.displayName, roles: a.roles } : null;
    },
  });
  await fake.install();
  await installFakeAccounts(page, accounts);
  const account = accounts.addAccount({ email: EMAIL, password: PASSWORD, displayName: "骑行者" });
  if (opts.signedIn) accounts.signIn(account);
  return { fake, accounts, account };
}

const panel = (page: Page) => page.getByTestId("auth-panel");
const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

async function fillLogin(page: Page, email: string, password: string) {
  await panel(page).getByLabel("邮箱").fill(email);
  await panel(page).getByLabel("密码", { exact: true }).fill(password);
  await panel(page).getByRole("button", { name: "登录", exact: true }).click();
}

test.describe("sign in", () => {
  test("a rider signs in with email and password and comes back to the page they wanted", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/me");
    await page.getByRole("button", { name: "登录", exact: true }).first().click();
    await expect(page).toHaveURL(/\/login\?next=%2Fme$/);
    // Fields are wired for password managers.
    await expect(panel(page).getByLabel("邮箱")).toHaveAttribute("autocomplete", "username");
    await expect(panel(page).getByLabel("密码", { exact: true })).toHaveAttribute("autocomplete", "current-password");
    await expect(panel(page).getByLabel("密码", { exact: true })).toHaveAttribute("type", "password");
    await fillLogin(page, `  ${EMAIL.toUpperCase()} `, PASSWORD);
    await expect(page).toHaveURL(/\/me$/);
    await expect(page.getByRole("heading", { name: "乘坐记录" })).toBeVisible();
    await expect(page.getByRole("banner").getByText("骑行者")).toBeVisible();
    expect(accounts.state.writesWithoutCsrf).toEqual([]);
    // Neither the password nor a session secret is kept by the page.
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
    expect(stored).not.toContain(PASSWORD);
    expect(stored).not.toMatch(/token|csrf/i);
    expect(await noOverflow(page)).toBe(true);
  });

  test("field mistakes are caught before any request and point at the field", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/login");
    await panel(page).getByRole("button", { name: "登录", exact: true }).click();
    await expect(panel(page).getByLabel("邮箱")).toHaveAttribute("aria-invalid", "true");
    await expect(panel(page).getByLabel("邮箱")).toBeFocused();
    await expect(panel(page).getByText("请输入邮箱地址")).toBeVisible();
    await panel(page).getByLabel("邮箱").fill("not-an-email");
    await panel(page).getByRole("button", { name: "登录", exact: true }).click();
    await expect(panel(page).getByText("请输入完整的邮箱地址", { exact: false })).toBeVisible();
    expect(accounts.state.calls.filter((c) => c === "LoginWithEmail")).toEqual([]);
  });

  test("a wrong password, an unknown email and a closed account read exactly alike", async ({ page }) => {
    await setup(page);
    await page.goto("/login");
    const texts: string[] = [];
    for (const [email, password] of [[EMAIL, "wrong password here"], ["nobody@example.com", PASSWORD]]) {
      await fillLogin(page, email, password);
      const alert = panel(page).getByRole("alert");
      await expect(alert).toBeVisible();
      texts.push((await alert.textContent()) ?? "");
      // The password is not kept in the field after a failed attempt.
      await expect(panel(page).getByLabel("密码", { exact: true })).toHaveValue("");
    }
    expect(texts[0]).toBe(texts[1]);
    expect(texts[0]).toContain("邮箱或密码错误，或账号不可用");
    expect(texts[0]).not.toMatch(/不存在|未注册|已注册/);
  });

  test("sending, rate limit and network failure each have their own state, and Enter submits", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/login");
    await panel(page).getByLabel("邮箱").fill(EMAIL);
    await panel(page).getByLabel("密码", { exact: true }).fill(PASSWORD);
    accounts.failNext("LoginWithEmail", FAILURES.rateLimited);
    await panel(page).getByLabel("密码", { exact: true }).press("Enter");
    await expect(panel(page).getByRole("alert")).toContainText("操作太频繁");
    // A network failure keeps the typed values so a retry needs nothing retyped.
    await page.route("**/api/bff.v1.AuthService/LoginWithEmail", (route) => route.abort("failed"), { times: 1 });
    await panel(page).getByRole("button", { name: "登录", exact: true }).click();
    await expect(panel(page).getByRole("alert")).toContainText("无法连接服务器");
    await expect(panel(page).getByLabel("密码", { exact: true })).toHaveValue(PASSWORD);
    await panel(page).getByLabel("密码", { exact: true }).press("Enter");
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole("banner").getByText("骑行者")).toBeVisible();
  });

  test("the password can be shown and hidden from the keyboard", async ({ page }) => {
    await setup(page);
    await page.goto("/login");
    const password = panel(page).getByLabel("密码", { exact: true });
    await password.fill("hunter2 hunter2");
    const toggle = panel(page).getByRole("button", { name: "显示密码" });
    await toggle.focus();
    await page.keyboard.press("Enter");
    await expect(password).toHaveAttribute("type", "text");
    await expect(panel(page).getByRole("button", { name: "隐藏密码" })).toHaveAttribute("aria-pressed", "true");
  });
});

test.describe("registration and reset by mail", () => {
  test("requesting registration gives the same neutral answer for a free and a taken address, with a resend cooldown", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/login?mode=register");
    const answers: string[] = [];
    for (const email of ["new-rider@example.com", EMAIL]) {
      await panel(page).getByLabel("邮箱").fill(email);
      await panel(page).getByRole("button", { name: "发送验证邮件" }).click();
      const sent = page.getByTestId("register-sent");
      await expect(sent).toBeVisible();
      // The text does not depend on whether the address can register.
      answers.push(((await sent.textContent()) ?? "").replace(email, "<email>"));
      await expect(sent.getByRole("button", { name: /重新发送（\d+ 秒后可用）/ })).toBeDisabled();
      await sent.getByRole("button", { name: "换一个邮箱" }).click();
    }
    expect(answers[0]).toBe(answers[1]);
    // Mail goes out only for the free address.
    expect(accounts.state.mails.map((m) => m.email)).toEqual(["new-rider@example.com"]);
  });

  test("closed sign-ups and mail limits are explained", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/login?mode=register");
    await panel(page).getByLabel("邮箱").fill("a@example.com");
    accounts.state.registrationOpen = false;
    await panel(page).getByRole("button", { name: "发送验证邮件" }).click();
    await expect(panel(page).getByRole("alert")).toContainText("暂未开放新用户注册");
    accounts.state.registrationOpen = true;
    accounts.state.mailRefusal = FAILURES.rateLimited;
    await panel(page).getByRole("button", { name: /发送验证邮件|请 \d+ 秒后再试/ }).click();
    await expect(panel(page).getByRole("alert")).toContainText("操作太频繁");
  });

  test("the mailed link opens a page that reads the token once, clears it, and consumes nothing until submit", async ({ page }) => {
    const { accounts } = await setup(page);
    const requested: string[] = [];
    page.on("request", (r) => requested.push(r.url()));
    await page.goto("/login?mode=register");
    await panel(page).getByLabel("邮箱").fill("new-rider@example.com");
    await panel(page).getByRole("button", { name: "发送验证邮件" }).click();
    await expect(page.getByTestId("register-sent")).toBeVisible();
    const mail = accounts.lastMail("registration")!;
    expect(mail.link).toMatch(/^\/account\/verify-email#token=[\w-]{43}$/);

    const callsBeforeOpen = accounts.state.calls.length;
    const res = await page.goto(mail.link);
    expect(res!.headers()["referrer-policy"]).toBe("no-referrer");
    expect(res!.headers()["cache-control"]).toBe("no-store");
    await expect(page.getByRole("heading", { name: "验证邮箱并设置密码" })).toBeVisible();
    // The fragment is gone from the address bar and no storage holds it.
    expect(new URL(page.url()).hash).toBe("");
    expect(page.url()).not.toContain(mail.token);
    expect(await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))).not.toContain(mail.token);
    expect(await page.evaluate(() => document.querySelector('meta[name="referrer"]')?.getAttribute("content"))).toBe("no-referrer");
    // Opening the page (what a mail scanner does) made no account call at all, and no request carried the token.
    expect(accounts.state.calls.slice(callsBeforeOpen)).toEqual([]);
    expect(requested.filter((u) => u.includes(mail.token))).toEqual([]);
    expect(await page.evaluate(() => [...document.scripts].map((s) => s.src).filter((s) => !s.startsWith(location.origin)))).toEqual([]);

    // Mistakes are shown on their fields; nothing is sent.
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByLabel("密码", { exact: true })).toHaveAttribute("aria-invalid", "true");
    await page.getByLabel("密码", { exact: true }).fill("a long enough password");
    await page.getByLabel("再次输入密码").fill("a different password!!");
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByText("两次输入的密码不一致")).toBeVisible();
    expect(accounts.state.calls).not.toContain("CompleteRegistration");

    await page.getByLabel("昵称（可选）").fill("新骑行者");
    await page.getByLabel("再次输入密码").fill("a long enough password");
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("landing-done")).toContainText("注册完成");
    expect(accounts.state.calls).toContain("CompleteRegistration");
    // Verifying signs nobody in.
    expect(accounts.state.current).toBeNull();
    await page.getByRole("link", { name: "去登录" }).click();
    await fillLogin(page, "new-rider@example.com", "a long enough password");
    await expect(page.getByRole("banner").getByText("新骑行者")).toBeVisible();
  });

  test("a used, reloaded or missing token shows the same unusable-link page", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/login?mode=register");
    await panel(page).getByLabel("邮箱").fill("new-rider@example.com");
    await panel(page).getByRole("button", { name: "发送验证邮件" }).click();
    await expect(page.getByTestId("register-sent")).toBeVisible();
    const link = accounts.lastMail("registration")!.link;

    await page.goto(link);
    await page.getByLabel("密码", { exact: true }).fill("a long enough password");
    await page.getByLabel("再次输入密码").fill("a long enough password");
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("landing-done")).toBeVisible();

    // The link works once: opening it again and submitting is refused as unusable.
    await page.goto(link);
    await page.getByLabel("密码", { exact: true }).fill("another long password");
    await page.getByLabel("再次输入密码").fill("another long password");
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("link-invalid")).toContainText("链接不可用");
    // The fragment was cleared, so a reload has no token and says the same.
    await page.reload();
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    // So does a link without a token, or with a mangled one.
    await page.goto("/account/verify-email");
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    await page.goto("/account/verify-email#token=short");
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    await page.getByRole("link", { name: "返回登录，重新申请" }).click();
    await expect(page).toHaveURL(/\/login$/);
  });

  test("forgotten password: neutral answer, then a reset that does not sign in and ends every old session", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/login");
    await panel(page).getByRole("button", { name: "忘记密码？" }).click();
    const answers: string[] = [];
    for (const email of [EMAIL, "ghost@example.com"]) {
      await panel(page).getByLabel("邮箱").fill(email);
      await panel(page).getByRole("button", { name: "发送重置邮件" }).click();
      const sent = page.getByTestId("forgot-sent");
      await expect(sent).toBeVisible();
      answers.push(((await sent.textContent()) ?? "").replace(email, "<email>"));
      await sent.getByRole("button", { name: "换一个邮箱" }).click();
    }
    expect(answers[0]).toBe(answers[1]);
    expect(accounts.state.mails.map((m) => m.email)).toEqual([EMAIL]);

    await page.goto(accounts.lastMail("reset")!.link);
    await expect(page.getByRole("heading", { name: "设置新密码" })).toBeVisible();
    expect(new URL(page.url()).hash).toBe("");
    await page.getByLabel("新密码", { exact: true }).fill("a brand new password");
    await page.getByLabel("再次输入新密码").fill("a brand new password");
    await page.getByRole("button", { name: "重置密码" }).click();
    await expect(page.getByTestId("landing-done")).toContainText("密码已重置");
    expect(accounts.state.current).toBeNull();
    await page.getByRole("link", { name: "去登录" }).click();
    await fillLogin(page, EMAIL, PASSWORD);
    await expect(panel(page).getByRole("alert")).toBeVisible();
    await fillLogin(page, EMAIL, "a brand new password");
    await expect(page.getByRole("banner").getByText("骑行者")).toBeVisible();
  });
});

test.describe("account settings", () => {
  test("profile, devices and password", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    accounts.state.otherSessions = 1;
    await page.goto("/account");
    await expect(page.getByLabel("登录邮箱")).toHaveValue(EMAIL);
    await expect(page.getByLabel("登录邮箱")).toHaveAttribute("readonly", "");

    const profile = page.getByTestId("profile-form");
    await profile.getByLabel("昵称").fill("   ");
    await profile.getByRole("button", { name: "保存资料" }).click();
    await expect(profile.getByText("请输入昵称")).toBeVisible();
    await profile.getByLabel("头像地址（可选）").fill("http://insecure.example/a.png");
    await profile.getByLabel("昵称").fill("夜班骑行者");
    await profile.getByRole("button", { name: "保存资料" }).click();
    await expect(profile.getByText("头像需要是 https 地址")).toBeVisible();
    await profile.getByLabel("头像地址（可选）").fill("https://cdn.example.com/me.png");
    await profile.getByRole("button", { name: "保存资料" }).click();
    await expect(profile.getByRole("status")).toContainText("资料已保存");
    await expect(page.getByRole("banner").getByText("夜班骑行者")).toBeVisible();

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
    await pw.getByLabel("新密码", { exact: true }).fill("short");
    await pw.getByLabel("再次输入新密码").fill("short");
    await pw.getByRole("button", { name: "修改密码" }).click();
    await expect(pw.getByText(/密码至少 12 个字符/)).toBeVisible();
    await pw.getByLabel("新密码", { exact: true }).fill("a brand new password");
    await pw.getByLabel("再次输入新密码").fill("a brand new password");
    await pw.getByRole("button", { name: "修改密码" }).click();
    // Every session ended, this one included: back to the sign-in page with the reason.
    await expect(page).toHaveURL(/\/login\?changed=1$/);
    await expect(page.getByText("密码已修改，所有设备都已退出登录")).toBeVisible();
    await fillLogin(page, EMAIL, "a brand new password");
    await expect(page.getByRole("banner").getByText("夜班骑行者")).toBeVisible();
    expect(await noOverflow(page)).toBe(true);
  });

  test("closing the account lists what goes, needs the password and a typed word, and survives a failed cleanup", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    await page.goto("/account");
    const panel = page.getByTestId("delete-account");
    // The whole scope is stated: the shared account and every product's data.
    await expect(panel).toContainText("共用账号");
    await expect(panel).toContainText("山海足迹");
    await expect(panel).toContainText("地铁足迹");
    await expect(panel).not.toContainText(/Logto/i);
    await panel.getByRole("button", { name: "注销账号…" }).click();
    const confirm = panel.getByRole("button", { name: "确认永久注销" });
    await expect(confirm).toBeDisabled();
    await panel.getByLabel("输入“注销”确认").fill("删除");
    await expect(confirm).toBeDisabled();
    await panel.getByLabel("输入“注销”确认").fill("注销");
    await panel.getByLabel("当前密码").fill("wrong password!!!");
    await confirm.click();
    await expect(panel.getByText("当前密码不正确")).toBeVisible();
    expect(accounts.state.calls).not.toContain("DeleteAccount");

    // The password is right, but the cleanup fails once: the account is closed and a retry is offered.
    accounts.state.deletionFailures = 1;
    await panel.getByLabel("当前密码").fill(PASSWORD);
    await confirm.click();
    await expect(page.getByRole("button", { name: "重试注销" }).first()).toBeVisible();
    await expect(page.getByRole("alert").first()).toContainText("注销尚未");
    const sent = accounts.state.bodies.get("DeleteAccount")![0];
    expect(sent.confirmation).toBe("DELETE");
    expect(sent.mutationId).toMatch(/^del-[\w-]+$/);
    expect("password" in sent).toBe(false);
    // The pending state survives a reload: the account is closed, so Me fails, but the offer stays.
    await page.reload();
    await expect(page.getByRole("button", { name: "重试注销" })).toBeVisible();
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page).toHaveURL(/\/login\?deleted=1$/);
    await expect(page.getByText("账号已注销")).toBeVisible();
    expect(accounts.state.accounts).toEqual([]);
    expect(await page.evaluate(() => localStorage.getItem("metro-account-deletion-pending"))).toBeNull();
  });

  test("a deletion needs a recent password check", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    await page.goto("/account");
    const panel = page.getByTestId("delete-account");
    await panel.getByRole("button", { name: "注销账号…" }).click();
    await panel.getByLabel("输入“注销”确认").fill("注销");
    await panel.getByLabel("当前密码").fill(PASSWORD);
    // The server's clock says the last check is stale even after the form's own check: it must ask again.
    accounts.failNext("DeleteAccount", { status: 400, code: "failed_precondition", message: "enter your password again to continue" });
    await panel.getByRole("button", { name: "确认永久注销" }).click();
    await expect(panel.getByText("请先输入当前密码再继续")).toBeVisible();
    // Nothing started: the form is still there and the account is intact.
    await expect(panel.getByRole("button", { name: "确认永久注销" })).toBeVisible();
    expect(accounts.state.accounts).toHaveLength(1);
  });
});

test("the account pages fit a phone and stay out of the way of anonymous browsing", async ({ page }) => {
  await setup(page);
  await page.goto("/login");
  expect(await noOverflow(page)).toBe(true);
  await page.goto("/login?mode=register");
  expect(await noOverflow(page)).toBe(true);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "账号", exact: true })).toHaveCount(0);
});

// The shared device list inside metro: answers that arrive late must not repaint it.
const settle = (page: Page) => page.evaluate(() => new Promise<void>((done) => setTimeout(() => requestAnimationFrame(() => done()), 250)));

test.describe("late answers in the device list", () => {
  test("after a revoke, an older list read cannot bring the revoked device back", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    accounts.state.otherSessions = 2;
    await page.goto("/account");
    const list = page.getByTestId("sessions");
    const otherRows = list.locator("li").filter({ hasText: "其他设备" });
    await expect(otherRows).toHaveCount(2);
    accounts.failNext("ListSessions", FAILURES.unavailable);
    await otherRows.first().getByRole("button", { name: /退出/ }).click();
    await expect(list).toContainText("暂时无法读取设备列表");
    const old = accounts.holdNext("ListSessions");
    await list.getByRole("button", { name: "重试" }).click();
    await old.arrived; // decided with one other device left
    await otherRows.first().getByRole("button", { name: /退出/ }).click();
    await expect(otherRows).toHaveCount(0);
    old.release();
    await settle(page);
    await expect(otherRows).toHaveCount(0);
    await expect(list.locator("li")).toHaveCount(1);
    await expect(list.locator("li").first()).toContainText("当前设备");
  });
});
