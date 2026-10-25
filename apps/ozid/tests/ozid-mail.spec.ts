import { expect, test, type Page } from "@playwright/test";
import { createFakeAccounts, FAILURES, installFakeAccounts, type FakeAccounts } from "@kongken/account/testing";

// The mail flows of OzID against a fake BFF (UI only; the fake applies the registry rules of
// internal/bff/navigation and binds the return context to the mailed token like usersrv does).
// The real chain with the real mail outbox is internal/e2e (TestChainOzid*Mail*) and the real
// browser run (tests/real-stack).

const EMAIL = "newcomer@example.com";
const PASSWORD = "correct horse battery";
const NEXT_PASSWORD = "a much better password";

const REGISTRY = {
  ozid: { origin: "http://127.0.0.1:4176", paths: ["/security"] },
  metro: { origin: "https://metro.example.com", paths: ["/me", "/city/*"] },
  "trip-map": { origin: "https://trip.example.com" },
};

async function standIns(page: Page, visited: string[], stray: string[]) {
  for (const origin of ["https://metro.example.com", "https://trip.example.com"]) {
    await page.route(`${origin}/**`, (route) => {
      visited.push(route.request().url());
      return route.fulfill({ status: 200, contentType: "text/html", body: "<title>stand-in</title><p>stand-in</p>" });
    });
  }
  await page.route((url) => url.hostname.endsWith("evil.example"), (route) => {
    stray.push(route.request().url());
    return route.abort();
  });
}

const seen = new WeakMap<Page, { visited: string[]; stray: string[] }>();

async function setup(page: Page) {
  const accounts = createFakeAccounts({ navigation: REGISTRY });
  await installFakeAccounts(page, accounts);
  const visited: string[] = [];
  const stray: string[] = [];
  await standIns(page, visited, stray);
  seen.set(page, { visited, stray });
  return { accounts, visited, stray };
}

/** Another browser: its own storage and its own tabs, talking to the same (fake) BFF. */
async function otherBrowser(page: Page, accounts: FakeAccounts) {
  const context = await page.context().browser()!.newContext({ baseURL: test.info().project.use.baseURL });
  const other = await context.newPage();
  await installFakeAccounts(other, accounts);
  const known = seen.get(page)!;
  await standIns(other, known.visited, known.stray);
  return { other, context };
}

const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const FRAGMENT = /#/;

async function requestMail(page: Page, path: string, email = EMAIL) {
  await page.goto(path);
  await page.getByLabel("邮箱").fill(email);
  await page.getByRole("button", { name: /发送(验证|重置)邮件/ }).click();
  await expect(page.getByTestId(/^(register|forgot)-sent$/)).toBeVisible();
}

test.describe("asking for a mail", () => {
  test("sign-in offers both, and the context follows the user from page to page", async ({ page }) => {
    await setup(page);
    await page.goto("/login?app_id=metro&return_path=%2Fcity%2Fshanghai");
    await page.getByRole("button", { name: "没有账号？申请注册" }).click();
    await expect(page).toHaveURL("/register?app_id=metro&return_path=%2Fcity%2Fshanghai");
    await expect(page.getByRole("heading", { name: "申请注册" })).toBeVisible();
    await page.getByRole("button", { name: "忘记密码？" }).click();
    await expect(page).toHaveURL("/forgot-password?app_id=metro&return_path=%2Fcity%2Fshanghai");
    await expect(page.getByRole("heading", { name: "找回密码" })).toBeVisible();
    await page.getByRole("button", { name: "返回登录" }).click();
    await expect(page).toHaveURL("/login?app_id=metro&return_path=%2Fcity%2Fshanghai");
    expect(await noOverflow(page)).toBe(true);
  });

  test("registration binds the validated context, answers neutrally and explains the wait", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.addAccount({ email: "taken@example.com", password: PASSWORD });
    await requestMail(page, "/register?app_id=metro&return_path=%2Fcity%2Fshanghai");
    await expect(page.getByTestId("register-sent")).toContainText(EMAIL);
    // Only the registry's answer is bound; the page sends the canonical pair, no URL.
    expect(accounts.state.bodies.get("ResolveNavigation")).toEqual([{ appId: "metro", returnPath: "/city/shanghai" }]);
    expect(accounts.state.bodies.get("RequestRegistration")).toEqual([{ email: EMAIL, appId: "metro", returnPath: "/city/shanghai" }]);
    expect(accounts.state.writesWithoutCsrf).toEqual([]);
    // The link of the mail lands at OzID whichever app asked, and carries only the token.
    expect(accounts.lastMail("registration")!.link).toMatch(/^\/account\/verify-email#token=[A-Za-z0-9_-]{43}$/);
    // Resending waits.
    const resend = page.getByRole("button", { name: /重新发送/ });
    await expect(resend).toBeDisabled();
    await expect(resend).toContainText("秒后可用");
    expect(await noOverflow(page)).toBe(true);

    // An address that cannot register looks exactly the same to the user.
    const taken = await page.context().newPage();
    await installFakeAccounts(taken, accounts);
    await taken.goto("/register");
    await taken.getByLabel("邮箱").fill("taken@example.com");
    await taken.getByRole("button", { name: "发送验证邮件" }).click();
    await expect(taken.getByTestId("register-sent")).toContainText("taken@example.com");
    expect(accounts.state.mails.filter((m) => m.email === "taken@example.com")).toHaveLength(0);
  });

  test("opened directly, no context is bound", async ({ page }) => {
    const { accounts } = await setup(page);
    await requestMail(page, "/register");
    await expect(page.getByTestId("register-sent")).toBeVisible();
    expect(accounts.state.calls).not.toContain("ResolveNavigation");
    expect(accounts.state.bodies.get("RequestRegistration")).toEqual([{ email: EMAIL }]);
    expect(accounts.lastMail("registration")!.bound).toBeNull();
  });

  test("a context the server refuses is not sent, and the user is told", async ({ page }) => {
    const { accounts, stray } = await setup(page);
    await page.goto("/forgot-password?app_id=metro&return_path=https%3A%2F%2Fevil.example%2F");
    await expect(page.getByText("无法确认要返回的应用或页面，因此不会跳转。")).toBeVisible();
    accounts.addAccount({ email: EMAIL, password: PASSWORD });
    await page.getByLabel("邮箱").fill(EMAIL);
    await page.getByRole("button", { name: "发送重置邮件" }).click();
    await expect(page.getByTestId("forgot-sent")).toBeVisible();
    expect(accounts.state.bodies.get("RequestPasswordReset")).toEqual([{ email: EMAIL }]);
    expect(stray).toEqual([]);
  });

  test("password reset binds the context of the asking app", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.addAccount({ email: EMAIL, password: PASSWORD });
    await requestMail(page, "/forgot-password?app_id=trip-map");
    await expect(page.getByTestId("forgot-sent")).toContainText(EMAIL);
    expect(accounts.state.bodies.get("RequestPasswordReset")).toEqual([{ email: EMAIL, appId: "trip-map", returnPath: "/" }]);
  });

  test("closed registration, rate limit, outage and network failure each say so", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/register");
    const send = async () => {
      await page.getByLabel("邮箱").fill(EMAIL);
      await page.getByRole("button", { name: /发送验证邮件|秒后再试/ }).click();
    };
    accounts.state.registrationOpen = false;
    await send();
    await expect(page.getByRole("alert")).toContainText("目前暂未开放新用户注册");
    accounts.state.registrationOpen = true;

    accounts.failNext("RequestRegistration", FAILURES.rateLimited);
    await page.getByRole("button", { name: "发送验证邮件" }).click();
    await expect(page.getByRole("alert")).toContainText("操作太频繁");
    await expect(page.getByRole("button", { name: /请 \d+ 秒后再试/ })).toBeDisabled();

    // A new load: the cooldown of the page before is not remembered, a real outage is explained.
    await page.goto("/register");
    accounts.failNext("RequestRegistration", FAILURES.unavailable);
    await send();
    await expect(page.getByRole("alert")).toContainText("服务暂时不可用");
    expect(accounts.state.mails).toHaveLength(0);

    await page.route("**/api/bff.v1.AuthService/RequestRegistration", (route) => route.abort("failed"));
    await page.getByRole("button", { name: /发送验证邮件|秒后再试/ }).click();
    await expect(page.getByRole("alert")).toContainText(/无法连接服务器|服务暂时不可用/);
    expect(await noOverflow(page)).toBe(true);
  });

  test("the form is usable from the keyboard alone and reports a bad address first", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/register");
    await page.getByLabel("邮箱").focus();
    await page.keyboard.type("not-an-address");
    await page.keyboard.press("Enter");
    await expect(page.getByLabel("邮箱")).toHaveAttribute("aria-invalid", "true");
    await expect(page.getByLabel("邮箱")).toBeFocused();
    expect(accounts.state.calls).not.toContain("RequestRegistration");
    await page.getByLabel("邮箱").fill(EMAIL);
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("register-sent")).toBeVisible();
  });
});

test.describe("finishing from the mail", () => {
  test("another browser completes registration; nothing signs in, the way back is kept", async ({ page }) => {
    const { accounts, visited } = await setup(page);
    // The first browser asks, from an app, and a different account is signed in there.
    const bystander = accounts.addAccount({ email: "bystander@example.com", password: PASSWORD, displayName: "旁人" });
    await requestMail(page, "/register?app_id=metro&return_path=%2Fcity%2Fshanghai");
    await expect(page.getByTestId("register-sent")).toBeVisible();
    accounts.signIn(bystander);
    const mail = accounts.lastMail("registration")!;

    // The mail is opened elsewhere: no state is shared with the browser that asked.
    const { other, context } = await otherBrowser(page, accounts);
    const calls = accounts.state.calls.length;
    await other.goto(mail.link);
    // The token left the address bar before anything could read or log it, and went nowhere else.
    expect(other.url()).not.toMatch(FRAGMENT);
    await expect(other.getByTestId("landing-verify-email")).toBeVisible();
    const stored = await other.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + document.cookie + location.href);
    expect(stored).not.toContain(mail.token);
    await expect(other.locator('meta[name="referrer"]')).toHaveAttribute("content", "no-referrer");
    // Opening the page asked the server nothing: no session check, no navigation lookup, no consumption.
    expect(accounts.state.calls.length).toBe(calls);
    await expect(other.getByText("完成后不会自动登录，也不会改变这个浏览器上已有的登录。")).toBeVisible();
    expect(await noOverflow(other)).toBe(true);

    await other.getByLabel("昵称（可选）").fill("新人");
    await other.getByLabel("密码", { exact: true }).fill(PASSWORD);
    await other.getByLabel("再次输入密码").fill(PASSWORD);
    await other.getByRole("button", { name: "完成注册" }).click();
    await expect(other.getByTestId("landing-done")).toContainText("账号已经创建");
    // Nothing signed in, and the account that was signed in elsewhere is untouched.
    expect(accounts.state.current?.email).toBe("bystander@example.com");
    expect(accounts.state.calls).not.toContain("LoginWithEmail");
    expect(accounts.state.writesWithoutCsrf).toEqual([]);

    // On to sign-in, with the bound context; signing in returns to exactly that page.
    // (The fake has one session; the bystander's ends here so this browser shows the plain sign-in.)
    accounts.signOut();
    await other.getByRole("link", { name: "去登录" }).click();
    await expect(other).toHaveURL("/login?app_id=metro&return_path=%2Fcity%2Fshanghai");
    await other.getByLabel("邮箱").fill(EMAIL);
    await other.getByLabel("密码", { exact: true }).fill(PASSWORD);
    await other.getByRole("button", { name: "登录", exact: true }).click();
    await other.waitForURL("https://metro.example.com/city/shanghai");
    expect(visited).toEqual(["https://metro.example.com/city/shanghai"]);
    await context.close();
  });

  test("the reset in another browser ends every session and leads on to sign-in", async ({ page }) => {
    const { accounts } = await setup(page);
    const account = accounts.addAccount({ email: EMAIL, password: PASSWORD, displayName: "忘性" });
    await requestMail(page, "/forgot-password?app_id=trip-map");
    await expect(page.getByTestId("forgot-sent")).toBeVisible();
    accounts.signIn(account);
    const mail = accounts.lastMail("reset")!;
    expect(mail.link).toMatch(/^\/account\/reset-password#token=/);

    const { other, context } = await otherBrowser(page, accounts);
    await other.goto(mail.link);
    expect(other.url()).not.toMatch(FRAGMENT);
    await other.getByLabel("新密码", { exact: true }).fill(NEXT_PASSWORD);
    await other.getByLabel("再次输入新密码").fill(NEXT_PASSWORD);
    await other.getByRole("button", { name: "重置密码" }).click();
    await expect(other.getByTestId("landing-done")).toContainText("密码已重置");
    // The reset signed nobody in and every session of the account is gone.
    expect(accounts.state.current).toBeNull();
    expect(accounts.state.calls).not.toContain("LoginWithEmail");
    await other.getByRole("link", { name: "去登录" }).click();
    await expect(other).toHaveURL("/login?app_id=trip-map&return_path=%2F");
    await context.close();
  });

  test("a link works once: a replay, an expired link and a link without a token are all just unusable", async ({ page }) => {
    const { accounts } = await setup(page);
    await requestMail(page, "/register");
    const used = accounts.lastMail("registration")!;
    const finish = async (p: Page) => {
      await p.getByLabel("密码", { exact: true }).fill(PASSWORD);
      await p.getByLabel("再次输入密码").fill(PASSWORD);
      await p.getByRole("button", { name: "完成注册" }).click();
    };
    await page.goto(used.link);
    await finish(page);
    await expect(page.getByTestId("landing-done")).toBeVisible();

    // Replay.
    await page.goto(used.link);
    await finish(page);
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    await expect(page.getByTestId("link-invalid")).not.toContainText(used.token);

    // Expired.
    await requestMail(page, "/register", "later@example.com");
    const expired = accounts.lastMail("registration")!;
    accounts.expireToken(expired.token);
    await page.goto(expired.link);
    await finish(page);
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    // The way out leads to sign-in, where a new mail can be asked for.
    await page.getByRole("link", { name: "返回登录，重新申请" }).click();
    await expect(page).toHaveURL("/login");

    // No token at all (a copied address without the fragment, or a reload).
    await page.goto("/account/reset-password");
    await expect(page.getByTestId("link-invalid")).toBeVisible();
    expect(await noOverflow(page)).toBe(true);
  });

  test("a bound target the registry no longer allows is dropped; the account step still succeeded", async ({ page }) => {
    const { accounts } = await setup(page);
    await requestMail(page, "/register?app_id=metro&return_path=%2Fme");
    const mail = accounts.lastMail("registration")!;
    // The app left the registry (or the path was withdrawn) after the mail was requested.
    accounts.state.tokens.get(mail.token)!.bound = { appId: "retired-app", returnPath: "/" };
    await page.goto(mail.link);
    await page.getByLabel("密码", { exact: true }).fill(PASSWORD);
    await page.getByLabel("再次输入密码").fill(PASSWORD);
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("landing-done")).toContainText("账号已经创建");
    // Safe landing: OzID's own sign-in, with no app to return to.
    await expect(page.getByRole("link", { name: "去登录" })).toHaveAttribute("href", "/login");
  });

  test("a mistake keeps the token usable, and a second mailed link replaces the first in the same tab", async ({ page }) => {
    const { accounts } = await setup(page);
    await requestMail(page, "/register");
    const first = accounts.lastMail("registration")!;
    await page.goto(first.link);
    await page.getByLabel("密码", { exact: true }).fill("short");
    await page.getByLabel("再次输入密码").fill("short");
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByLabel("密码", { exact: true })).toHaveAttribute("aria-invalid", "true");
    expect(accounts.state.tokens.get(first.token)!.used).toBe(false);

    // A second mail for the same address; its link only changes the fragment of the open tab.
    const again = await page.context().newPage();
    await installFakeAccounts(again, accounts);
    await requestMail(again, "/register");
    const second = accounts.lastMail("registration")!;
    expect(second.token).not.toBe(first.token);
    await page.evaluate((hash) => (location.hash = hash), second.link.slice(second.link.indexOf("#")));
    await expect(page.getByLabel("密码", { exact: true })).toHaveValue("");
    expect(page.url()).not.toMatch(FRAGMENT);
    await page.getByLabel("密码", { exact: true }).fill(PASSWORD);
    await page.getByLabel("再次输入密码").fill(PASSWORD);
    await page.getByRole("button", { name: "完成注册" }).click();
    await expect(page.getByTestId("landing-done")).toBeVisible();
    expect(accounts.state.tokens.get(second.token)!.used).toBe(true);
  });
});

test("the mail landing pages show no app chrome and ask the server nothing on load", async ({ page }) => {
  const { accounts } = await setup(page);
  for (const path of ["/account/verify-email", "/account/reset-password"]) {
    await page.goto(path);
    await expect(page.getByTestId("link-invalid")).toBeVisible();
  }
  expect(accounts.state.calls).toEqual([]);
  // The signed-in header of the app is not rendered here.
  await expect(page.getByTestId("header-name")).toHaveCount(0);
});
