import { expect, test, type Page } from "@playwright/test";
import { createFakeAccounts, FAILURES, installFakeAccounts } from "@kongken/account/testing";

// OzID's screens against a fake BFF (UI only). The fake applies the same registry rules as
// internal/bff/navigation. The real chain is internal/e2e TestOzidBrowserChain.

const EMAIL = "ozid-user@example.com";
const PASSWORD = "correct horse battery";

const REGISTRY = {
  ozid: { origin: "http://127.0.0.1:4176", paths: ["/security"] },
  metro: { origin: "https://metro.example.com", paths: ["/me", "/city/*"] },
  admin: { origin: "https://admin.example.com", defaultPath: "/users", paths: ["/metro/*"] },
  "trip-map": { origin: "https://trip.example.com" },
};

async function setup(page: Page, opts: { signedIn?: boolean } = {}) {
  const accounts = createFakeAccounts({ navigation: REGISTRY });
  await installFakeAccounts(page, accounts);
  const account = accounts.addAccount({ email: EMAIL, password: PASSWORD, displayName: "Oz 用户" });
  if (opts.signedIn) accounts.signIn(account);
  // Other origins are stand-ins: only the address matters. Anything outside the registry is recorded.
  const visited: string[] = [];
  for (const origin of ["https://metro.example.com", "https://admin.example.com", "https://trip.example.com"]) {
    await page.route(`${origin}/**`, (route) => {
      visited.push(route.request().url());
      return route.fulfill({ status: 200, contentType: "text/html", body: "<title>stand-in</title><p>stand-in</p>" });
    });
  }
  const stray: string[] = [];
  // By host: the page's own URL may carry the text of a hostile address in its query.
  await page.route((url) => url.hostname.endsWith("evil.example"), (route) => {
    stray.push(route.request().url());
    return route.abort();
  });
  return { accounts, account, visited, stray };
}

const panel = (page: Page) => page.getByTestId("auth-panel");
const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

async function fillLogin(page: Page, email = EMAIL, password = PASSWORD) {
  await panel(page).getByLabel("邮箱").fill(email);
  await panel(page).getByLabel("密码", { exact: true }).fill(password);
  await panel(page).getByRole("button", { name: "登录", exact: true }).click();
}

test.describe("direct visit", () => {
  test("anonymous visitors are led to sign in and, afterwards, to the account overview", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/");
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByRole("heading", { name: "登录OzID" })).toBeVisible();
    // Fields are wired for password managers; registration and reset are pages of OzID too.
    await expect(panel(page).getByLabel("邮箱")).toHaveAttribute("autocomplete", "username");
    await expect(panel(page).getByLabel("密码", { exact: true })).toHaveAttribute("autocomplete", "current-password");
    await expect(panel(page).getByRole("button", { name: "忘记密码？" })).toBeVisible();
    await expect(panel(page).getByRole("button", { name: "没有账号？申请注册" })).toBeVisible();
    expect(await noOverflow(page)).toBe(true);

    await fillLogin(page, ` ${EMAIL.toUpperCase()} `);
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByTestId("overview-name")).toHaveText("Oz 用户");
    await expect(page.getByTestId("overview-email")).toHaveText(EMAIL);
    expect(accounts.state.writesWithoutCsrf).toEqual([]);
    expect(accounts.state.calls).not.toContain("ResolveNavigation");
    // No password, token or CSRF value is kept by the page.
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + document.cookie);
    expect(stored).not.toContain(PASSWORD);
    expect(stored).not.toMatch(/token|csrf|session/i);
    expect(await noOverflow(page)).toBe(true);
  });

  test("the form is usable from the keyboard alone and reports field mistakes first", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/login");
    await panel(page).getByRole("button", { name: "登录", exact: true }).click();
    await expect(panel(page).getByLabel("邮箱")).toHaveAttribute("aria-invalid", "true");
    await expect(panel(page).getByLabel("邮箱")).toBeFocused();
    expect(accounts.state.calls).not.toContain("LoginWithEmail");

    await panel(page).getByLabel("邮箱").fill(EMAIL);
    await page.keyboard.press("Tab");
    await page.keyboard.type(PASSWORD);
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("overview")).toBeVisible();
  });

  test("a wrong password gives one neutral text and keeps the page on the form", async ({ page }) => {
    await setup(page);
    await page.goto("/login");
    await fillLogin(page, EMAIL, "not the password!!");
    await expect(panel(page).getByRole("alert")).toContainText("邮箱或密码错误，或账号不可用");
    await expect(panel(page).getByLabel("密码", { exact: true })).toHaveValue("");
    await expect(page).toHaveURL(/\/login$/);
  });

  test("an outage is not a sign-out: the page offers a retry instead of the form", async ({ page }) => {
    const { accounts, account } = await setup(page, { signedIn: true });
    accounts.failNext("Me", FAILURES.unavailable);
    await page.goto("/");
    await expect(page.getByRole("alert")).toContainText("这不代表你已退出登录");
    await expect(panel(page)).toHaveCount(0);
    await page.getByRole("button", { name: "重试" }).click();
    await expect(page.getByTestId("overview-name")).toHaveText(account.displayName);
  });
});

test.describe("sign in from an app and come back", () => {
  test("the user returns to the page they came from, an address the server confirmed", async ({ page }) => {
    const { accounts, visited } = await setup(page);
    await page.goto("/login?app_id=metro&return_path=%2Fcity%2Fshanghai");
    await fillLogin(page);
    await page.waitForURL("https://metro.example.com/city/shanghai");
    expect(visited).toEqual(["https://metro.example.com/city/shanghai"]);
    expect(accounts.state.bodies.get("ResolveNavigation")).toEqual([{ appId: "metro", returnPath: "/city/shanghai" }]);
    // The sign-in request carries credentials only: no return address, user or time.
    expect(accounts.state.bodies.get("LoginWithEmail")).toEqual([{ email: EMAIL, password: PASSWORD }]);
  });

  test("an app without a path returns to its default page", async ({ page }) => {
    await setup(page);
    await page.goto("/login?app_id=admin");
    await fillLogin(page);
    await page.waitForURL("https://admin.example.com/users");
  });

  for (const [name, query] of Object.entries({
    "an unknown app": "app_id=billing&return_path=%2F",
    "an app that is an address": "app_id=https%3A%2F%2Fevil.example&return_path=%2F",
    "an absolute return address": "app_id=metro&return_path=https%3A%2F%2Fevil.example%2F",
    "a protocol-relative path": "app_id=metro&return_path=%2F%2Fevil.example",
    "an encoded traversal": "app_id=metro&return_path=%2Fcity%2F%252e%252e%2F",
    "a dot segment": "app_id=metro&return_path=%2Fcity%2F..%2Fme",
    "a backslash": "app_id=metro&return_path=%2F%5Cevil.example",
    "a query in the path": "app_id=metro&return_path=%2Fme%3Fnext%3Dhttps%3A%2F%2Fevil.example",
    "a fragment in the path": "app_id=metro&return_path=%2Fme%23x",
    "a path the app did not register": "app_id=metro&return_path=%2Fadmin",
    "a path without an app": "return_path=%2Fme",
    "repeated apps": "app_id=metro&app_id=admin",
  })) {
    test(`${name} is refused: sign-in works, nothing is navigated to, the user lands in OzID`, async ({ page }) => {
      const { visited, stray } = await setup(page);
      await page.goto(`/login?${query}`);
      await expect(page.getByRole("alert").filter({ hasText: "不会跳转" })).toBeVisible();
      await fillLogin(page);
      await expect(page.getByTestId("overview")).toBeVisible();
      expect(visited).toEqual([]);
      expect(stray).toEqual([]);
      await expect(page).toHaveURL(/\/$/);
    });
  }

  test("a registry outage is told apart from a refusal, and still never navigates", async ({ page }) => {
    const { accounts, visited } = await setup(page);
    accounts.failNext("ResolveNavigation", FAILURES.unavailable);
    await page.goto("/login?app_id=metro&return_path=%2Fme");
    await expect(page.getByRole("alert").filter({ hasText: "暂时无法确认" })).toBeVisible();
    await fillLogin(page);
    await expect(page.getByTestId("overview")).toBeVisible();
    expect(visited).toEqual([]);
  });
});

test.describe("a session that already exists", () => {
  test("continues to the app with one click", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    await page.goto("/login?app_id=trip-map");
    await expect(page.getByTestId("signed-in-here")).toContainText("Oz 用户");
    await expect(panel(page)).toHaveCount(0);
    await page.getByRole("button", { name: "继续" }).click();
    await page.waitForURL("https://trip.example.com/");
    expect(accounts.state.calls).not.toContain("LoginWithEmail");
  });

  test("switching account says it ends the shared session, then offers the form", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    await page.goto("/login?app_id=metro&return_path=%2Fme");
    await page.getByRole("button", { name: "使用其他账号登录" }).click();
    await expect(page.getByTestId("switch-account")).toContainText("所有应用");
    await page.getByRole("button", { name: "取消" }).click();
    await expect(page.getByTestId("signed-in-here")).toBeVisible();
    await page.getByRole("button", { name: "使用其他账号登录" }).click();
    await page.getByRole("button", { name: "退出并切换账号" }).click();
    await expect(panel(page)).toBeVisible();
    expect(accounts.state.current).toBeNull();
    await fillLogin(page);
    await page.waitForURL("https://metro.example.com/me");
  });

  test("signing out from the header ends the session and returns to sign-in", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    await page.goto("/");
    await page.getByRole("button", { name: "退出登录" }).click();
    await expect(page).toHaveURL(/\/login$/);
    expect(accounts.state.current).toBeNull();
  });
});

test.describe("password confirmation", () => {
  test("confirms the password and comes back to the app; nothing from before is replayed", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: true });
    accounts.ageAuthentication();
    const before = accounts.state.authenticatedAt;
    await page.goto("/security?app_id=admin&return_path=%2Fmetro%2Flines");
    await expect(page.getByTestId("reauthenticate")).toBeVisible();
    await expect(page.getByLabel("当前密码")).toHaveAttribute("autocomplete", "current-password");

    await page.getByLabel("当前密码").fill("not the password!!");
    await page.getByRole("button", { name: "确认密码" }).click();
    await expect(page.getByTestId("reauthenticate")).toContainText("当前密码不正确");
    expect(accounts.state.authenticatedAt).toBe(before);

    await page.getByLabel("当前密码").fill(PASSWORD);
    await page.getByRole("button", { name: "确认密码" }).click();
    await page.waitForURL("https://admin.example.com/metro/lines");
    expect(accounts.state.authenticatedAt).toBeGreaterThan(before);
    // The request carries the password and nothing the browser could forge: no user, time or target.
    for (const body of accounts.state.bodies.get("Reauthenticate") ?? []) expect(Object.keys(body)).toEqual(["password"]);
    expect(accounts.state.writesWithoutCsrf).toEqual([]);
  });

  test("without a target the user stays in OzID after confirming", async ({ page }) => {
    await setup(page, { signedIn: true });
    await page.goto("/security");
    await page.getByLabel("当前密码").fill(PASSWORD);
    await page.getByRole("button", { name: "确认密码" }).click();
    await expect(page.getByTestId("reauthenticated")).toBeVisible();
  });

  test("a refused target is reported and the user stays in OzID", async ({ page }) => {
    const { visited } = await setup(page, { signedIn: true });
    await page.goto("/security?app_id=metro&return_path=%2F%2Fevil.example");
    await expect(page.getByRole("alert").filter({ hasText: "不会跳转" })).toBeVisible();
    await page.getByLabel("当前密码").fill(PASSWORD);
    await page.getByRole("button", { name: "确认密码" }).click();
    await expect(page.getByTestId("reauthenticated")).toBeVisible();
    expect(visited).toEqual([]);
  });

  test("an expired identity is sent to sign in, not upgraded; the way back survives", async ({ page }) => {
    const { accounts, visited } = await setup(page, { signedIn: true });
    await page.goto("/security?app_id=admin&return_path=%2Fmetro%2Flines");
    await expect(page.getByTestId("reauthenticate")).toBeVisible();
    // The session is invalidated elsewhere (password or role change) after the page loaded.
    accounts.signOut();
    await page.getByLabel("当前密码").fill(PASSWORD);
    await page.getByRole("button", { name: "确认密码" }).click();
    await expect(page).toHaveURL(/\/login\?.*reason=expired/);
    await expect(page.getByText("登录已失效，请重新登录。")).toBeVisible();
    expect(visited).toEqual([]);
    expect(accounts.state.current).toBeNull();
    await fillLogin(page);
    await page.waitForURL("https://admin.example.com/metro/lines");
  });

  test("with no session at all the page leads to sign-in", async ({ page }) => {
    await setup(page);
    await page.goto("/security?app_id=metro&return_path=%2Fme");
    await expect(page).toHaveURL(/\/login\?app_id=metro&return_path=%2Fme&reason=expired$/);
  });
});

test("unknown pages say so and there is no other way in", async ({ page }) => {
  await setup(page);
  for (const path of ["/admin", "/login/x", "/register/x", "/account"]) {
    await page.goto(path);
    await expect(page.getByRole("heading", { name: "页面不存在" })).toBeVisible();
  }
});
