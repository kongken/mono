import { expect, test, type Page } from "@playwright/test";
import { startMailSink } from "@kongken/account/smtp";

/**
 * The account chain end to end with a real BFF, usersrv and the real mail module: the
 * mail goes out over SMTP to a catcher this file starts, and the links are opened as a
 * person would. It needs the stack of scripts/real-mail-stack.sh, so it only runs with
 * E2E_REAL_MAIL=1 (see docs/usersrv/spec.md); the default suite uses the fake backend.
 */
const enabled = process.env.E2E_REAL_MAIL === "1";
const PASSWORD = "first long password here";
const CHANGED = "second long password here";
const RESET = "third long password here";

test.describe("real account chain with mail", () => {
  test.skip(!enabled, "set E2E_REAL_MAIL=1 with scripts/real-mail-stack.sh running");
  test.setTimeout(180_000);

  let sink: Awaited<ReturnType<typeof startMailSink>>;
  test.beforeAll(async () => {
    sink = await startMailSink(Number(process.env.E2E_SMTP_PORT ?? 1025));
  });
  test.afterAll(async () => {
    await sink?.close();
  });

  const panel = (page: Page) => page.getByTestId("auth-panel");
  const account = (page: Page) => page.getByRole("button", { name: "账号", exact: true });
  const signIn = async (page: Page, email: string, password: string) => {
    await panel(page).getByLabel("邮箱").fill(email);
    await panel(page).getByLabel("密码", { exact: true }).fill(password);
    await panel(page).getByRole("button", { name: "登录", exact: true }).click();
  };
  const openLink = async (page: Page, link: string) => {
    const url = new URL(link);
    await page.goto(url.pathname + url.hash);
  };

  test("register by mail, sign in, edit the profile, change and reset the password, close the account", async ({ page }, info) => {
    const email = `real-${info.project.name}-${Date.now()}@example.com`;

    // Registration: the request is answered neutrally, the mail carries the link.
    await page.goto("/");
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await panel(page).getByRole("button", { name: "没有账号？申请注册" }).click();
    await panel(page).getByLabel("邮箱").fill(email);
    await panel(page).getByRole("button", { name: "发送验证邮件" }).click();
    await expect(page.getByTestId("register-sent")).toBeVisible();
    const verify = await sink.waitForLink(email, "/account/verify-email");
    expect(new URL(verify).hash).toMatch(/^#token=[\w-]{20,}$/);

    // The mailed page clears the token from the address bar and signs nobody in.
    await openLink(page, verify);
    await expect(page.getByRole("heading", { name: "验证邮箱并设置密码" })).toBeVisible();
    expect(new URL(page.url()).hash).toBe("");
    await page.getByLabel("昵称（可选）").fill("真实旅行者");
    await page.getByLabel("密码", { exact: true }).fill(PASSWORD);
    await page.getByLabel("再次输入密码").fill(PASSWORD);
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("landing-done")).toContainText("注册完成");
    // The link works once: reloading the page has no token any more.
    await openLink(page, verify);
    await page.getByLabel("密码", { exact: true }).fill(PASSWORD);
    await page.getByLabel("再次输入密码").fill(PASSWORD);
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("link-invalid")).toBeVisible();

    // Sign in from the app, as the landing page's link leads.
    await page.goto("/?signin=1");
    await expect(panel(page)).toBeVisible();
    await signIn(page, email, "not the password!!");
    await expect(panel(page).getByRole("alert")).toBeVisible();
    await signIn(page, email, PASSWORD);
    await expect(account(page)).toBeVisible();
    const cookies = await page.context().cookies();
    const session = cookies.find((c) => c.name === "kongken_sid");
    expect(session?.httpOnly).toBe(true);
    expect(await page.evaluate(() => JSON.stringify({ ...localStorage }))).not.toContain(PASSWORD);

    // Settings: email, profile, devices, then the password (which ends this session).
    await account(page).click();
    await page.getByRole("dialog").getByRole("button", { name: "账号设置" }).click();
    await expect(page.getByLabel("登录邮箱")).toHaveValue(email);
    const profile = page.getByTestId("profile-form");
    await profile.getByLabel("昵称").fill("改过的名字");
    await profile.getByRole("button", { name: "保存资料" }).click();
    await expect(profile.getByRole("status")).toContainText("资料已保存");
    await expect(page.getByTestId("sessions").getByText("当前设备")).toBeVisible();
    const pw = page.getByTestId("password-form");
    await pw.getByLabel("当前密码").fill(PASSWORD);
    await pw.getByLabel("新密码", { exact: true }).fill(CHANGED);
    await pw.getByLabel("再次输入新密码").fill(CHANGED);
    await pw.getByRole("button", { name: "修改密码" }).click();
    await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await signIn(page, email, PASSWORD);
    await expect(panel(page).getByRole("alert")).toBeVisible();
    await signIn(page, email, CHANGED);
    await expect(account(page)).toBeVisible();

    // Forgot password: the reset mail, a page that signs nobody in, and the new password.
    await account(page).click();
    await page.getByRole("button", { name: "退出登录" }).click();
    await page.getByRole("button", { name: "确认退出登录" }).click();
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await panel(page).getByRole("button", { name: "忘记密码？" }).click();
    await panel(page).getByLabel("邮箱").fill(email);
    await panel(page).getByRole("button", { name: "发送重置邮件" }).click();
    await expect(page.getByTestId("forgot-sent")).toBeVisible();
    const reset = await sink.waitForLink(email, "/account/reset-password");
    await openLink(page, reset);
    expect(new URL(page.url()).hash).toBe("");
    await page.getByLabel("新密码", { exact: true }).fill(RESET);
    await page.getByLabel("再次输入新密码").fill(RESET);
    await page.getByRole("button", { name: "重置密码" }).click();
    await expect(page.getByTestId("landing-done")).toBeVisible();
    await page.goto("/?signin=1");
    await signIn(page, email, CHANGED);
    await expect(panel(page).getByRole("alert")).toBeVisible();
    await signIn(page, email, RESET);
    await expect(account(page)).toBeVisible();

    // Closing the account removes it for good: the same login no longer works.
    await account(page).click();
    await page.getByRole("dialog").getByRole("button", { name: "账号设置" }).click();
    const del = page.getByTestId("delete-account");
    await del.getByRole("button", { name: "注销账号…" }).click();
    await del.getByLabel("当前密码").fill(RESET);
    await del.getByLabel("输入“注销”确认").fill("注销");
    await del.getByRole("button", { name: "确认永久注销" }).click();
    await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await signIn(page, email, RESET);
    await expect(panel(page).getByRole("alert")).toBeVisible();
  });
});
