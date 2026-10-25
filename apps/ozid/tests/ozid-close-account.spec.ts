import { expect, test, type Page } from "@playwright/test";
import { createFakeAccounts, FAILURES, installFakeAccounts } from "@kongken/account/testing";

// OzID's "close account" page against a fake BFF: what the page says, asks and does, including how it
// recovers (lost responses, reloads, an unfinished product). The fake stands in for the BFF only; the
// same workflow against the real BFF, usersrv and products is in internal/e2e (account_deletion_*,
// ozid_close_account_chain_test.go) and, from a real browser, tests/real-stack.

const EMAIL = "closing-user@example.com";
const PASSWORD = "correct horse battery";

async function setup(page: Page, opts: { signedIn?: boolean; roles?: string[] } = {}) {
  const accounts = createFakeAccounts({ navigation: { ozid: { origin: "http://127.0.0.1:4176", paths: ["/security"] } } });
  await installFakeAccounts(page, accounts);
  const account = accounts.addAccount({ email: EMAIL, password: PASSWORD, displayName: "Oz 用户", roles: opts.roles });
  if (opts.signedIn !== false) accounts.signIn(account);
  return { accounts, account };
}

const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const form = (page: Page) => page.getByTestId("close-form");

async function confirm(page: Page, password = PASSWORD) {
  await form(page).getByLabel("当前密码").fill(password);
  await form(page).getByLabel("输入“注销”确认").fill("注销");
  await form(page).getByRole("button", { name: "确认永久注销" }).click();
}

test.describe("what the page says", () => {
  test("it lists the shared account, both products, media and what is kept, and is reachable from the overview", async ({ page }) => {
    await setup(page);
    await page.goto("/");
    await page.getByRole("link", { name: "注销账号" }).first().click();
    await expect(page).toHaveURL(/\/close-account$/);
    await expect(page.getByRole("heading", { level: 1, name: "注销账号" })).toBeVisible();
    const scope = page.getByTestId("close-scope");
    await expect(scope).toContainText("永久删除");
    await expect(scope).toContainText("无法恢复");
    await expect(scope).toContainText("这不是退出登录");
    await expect(page.getByTestId("scope-shared")).toContainText("共用账号");
    await expect(page.getByTestId("scope-tripmap")).toContainText("山海足迹");
    await expect(page.getByTestId("scope-tripmap")).toContainText("照片与附件");
    await expect(page.getByTestId("scope-metro")).toContainText("地铁足迹");
    await expect(page.getByTestId("scope-media")).toContainText("媒体文件");
    // What it does not touch, and what to do first.
    await expect(page.getByTestId("scope-kept")).toContainText("不会删除你浏览器里没登录时保存的本地内容");
    await expect(page.getByTestId("scope-kept")).toContainText("先到对应产品里导出尚未同步的内容");
    await expect(page.getByTestId("scope-kept")).toContainText("不会替你向它们发出清除命令");
    // Platform administrators: no web path, no deletion on someone else's behalf.
    await expect(page.getByTestId("scope-admin")).toContainText("平台管理员账号不能在网页上注销");
    await expect(page.getByTestId("scope-admin")).toContainText("没有代他人注销的入口");
    expect(await noOverflow(page)).toBe(true);
  });

  test("the sign-out button is gone on this page: signing out is not part of closing", async ({ page }) => {
    await setup(page);
    await page.goto("/close-account");
    await expect(form(page)).toBeVisible();
    await expect(page.getByRole("button", { name: "退出登录" })).toHaveCount(0);
    await page.goto("/profile");
    await expect(page.getByRole("button", { name: "退出登录" })).toBeVisible();
  });

  test("an outage while checking the session is not a sign-out and starts nothing", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.failNext("Me", FAILURES.unavailable);
    await page.goto("/close-account");
    await expect(page.getByTestId("session-unavailable")).toContainText("不会开始注销");
    await page.getByRole("button", { name: "重试" }).click();
    await expect(form(page)).toBeVisible();
    expect(accounts.state.calls).not.toContain("DeleteAccount");
    expect(accounts.state.calls).not.toContain("Logout");
  });

  test("without a session and without a deletion the way on is signing in", async ({ page }) => {
    const { accounts } = await setup(page, { signedIn: false });
    await page.goto("/close-account");
    await expect(page).toHaveURL(/\/login$/);
    expect(accounts.state.calls).toContain("GetAccountDeletion");
  });
});

test.describe("beginning", () => {
  test("a recent password check and a typed confirmation are both required; nothing is sent before", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/close-account");
    const submit = form(page).getByRole("button", { name: "确认永久注销" });
    await expect(submit).toBeDisabled();
    await form(page).getByLabel("输入“注销”确认").fill("注销了");
    await expect(submit).toBeDisabled();
    await form(page).getByLabel("输入“注销”确认").fill("注销");
    await expect(submit).toBeEnabled();
    // The typed word alone is not enough: the password is checked again, and a wrong one stops here.
    await submit.click();
    await expect(form(page).getByText("请输入密码")).toBeVisible();
    await confirm(page, "not the password!");
    await expect(form(page)).toContainText("当前密码不正确");
    expect(accounts.state.calls).not.toContain("DeleteAccount");
    expect(accounts.state.current).not.toBeNull();
  });

  test("the happy path: password check, then one deletion request with a fresh key and the CSRF token; the Cookie is not signed out by the page", async ({ page }) => {
    const { accounts } = await setup(page);
    await page.goto("/close-account");
    await confirm(page);
    const done = page.getByTestId("close-done");
    await expect(done).toContainText("账号已注销");
    await expect(done).toContainText("包括媒体文件");
    expect(accounts.state.calls.filter((c) => c === "Reauthenticate" || c === "DeleteAccount")).toEqual(["Reauthenticate", "DeleteAccount"]);
    const [body] = accounts.state.bodies.get("DeleteAccount")!;
    expect(body).toMatchObject({ confirmation: "DELETE" });
    expect(String(body.mutationId)).toMatch(/^[A-Za-z0-9._-]{1,64}$/);
    expect(accounts.state.writesWithoutCsrf).toEqual([]);
    expect(accounts.state.calls).not.toContain("Logout");
    expect(accounts.state.accounts).toHaveLength(0);
    // The password is not kept anywhere in the page.
    expect(await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))).not.toContain(PASSWORD);
    // Reopening the page afterwards shows the same finished result (the receipt), not a form.
    await page.reload();
    await expect(page.getByTestId("close-done")).toBeVisible();
  });

  test("a platform administrator is refused with the way on; nothing is closed and the form stays", async ({ page }) => {
    const { accounts } = await setup(page, { roles: ["ozid-admin"] });
    await page.goto("/close-account");
    await confirm(page);
    await expect(form(page)).toContainText("平台管理员，不能在网页上注销");
    await expect(form(page)).toContainText("联系运维");
    expect(accounts.state.deletion).toBeNull();
    expect(accounts.state.current).not.toBeNull();
    // Nothing started: a second attempt asks again with a new key.
    await confirm(page);
    await expect.poll(() => accounts.state.bodies.get("DeleteAccount")?.length).toBe(2);
    const keys = accounts.state.bodies.get("DeleteAccount")!.map((b) => b.mutationId);
    expect(new Set(keys).size).toBe(2);
  });
});

test.describe("the Cookie of an unfinished deletion", () => {
  test("another account cannot sign in over it: the refusal says what to do, and the deletion is still there", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.addAccount({ email: "other-user@example.com", password: PASSWORD, displayName: "另一个人" });
    accounts.state.deletionFailures = 1;
    await page.goto("/close-account");
    await confirm(page);
    await expect(page.getByTestId("close-progress")).toBeVisible();

    await page.goto("/login");
    const panel = page.getByTestId("auth-panel");
    await panel.getByLabel("邮箱").fill("other-user@example.com");
    await panel.getByLabel("密码", { exact: true }).fill(PASSWORD);
    await panel.getByRole("button", { name: "登录", exact: true }).click();
    await expect(panel.getByRole("alert")).toContainText("尚未完成的账号注销");
    await expect(panel.getByRole("alert")).toContainText("换一个浏览器");
    await expect(page).toHaveURL(/\/login$/);
    expect(accounts.state.current).toBeNull();

    // The progress page still shows, refreshes and finishes it.
    await page.goto("/close-account");
    await expect(page.getByTestId("close-progress")).toBeVisible();
    await page.getByRole("button", { name: "刷新进度" }).click();
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    expect(accounts.state.accounts.map((a) => a.email)).toEqual(["other-user@example.com"]);
  });

  test("the form names the account it will close, and a Cookie that became another account's is refused, not closed", async ({ page }) => {
    const { accounts } = await setup(page);
    const other = accounts.addAccount({ email: "other-user@example.com", password: PASSWORD, displayName: "另一个人" });
    await page.goto("/close-account");
    await expect(page.getByTestId("close-account-who")).toContainText(`Oz 用户（${EMAIL}）`);
    // Another tab signs in as someone else while this page still shows the first account.
    accounts.signIn(other);
    await confirm(page);
    await expect(page).toHaveURL(/\/login$/);
    expect(accounts.state.calls).not.toContain("DeleteAccount");
    expect(accounts.state.accounts).toHaveLength(2);
  });

  test("where the deletion simply is, is a calm status; only a failure is an alert", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.state.deletionFailures = 2;
    await page.goto("/close-account");
    await confirm(page);
    const progress = page.getByTestId("close-progress");
    await expect(progress).toContainText("账号已经关闭");
    await expect(progress.getByRole("status")).toContainText("注销尚未完成");
    await expect(progress.getByRole("alert")).toHaveCount(0);
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(progress.getByRole("alert")).toContainText("注销尚未确认完成");
  });
});

test.describe("recovery", () => {
  test("a product that fails keeps the deletion open: progress per product, survives a reload, retry finishes it", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.state.deletionFailures = 1;
    await page.goto("/close-account");
    await confirm(page);
    const progress = page.getByTestId("close-progress");
    await expect(progress).toContainText("账号已经关闭");
    await expect(page.getByTestId("close-product-tripmap")).toHaveAttribute("data-state", "done");
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    // The scope stays on screen, the sign-out button stays gone.
    await expect(page.getByTestId("close-scope")).toBeVisible();
    await expect(page.getByRole("button", { name: "退出登录" })).toHaveCount(0);
    expect(await noOverflow(page)).toBe(true);

    // A reload: the session is gone (the account is closed) but the Cookie finds the deletion.
    await page.reload();
    await expect(page.getByTestId("close-progress")).toBeVisible();
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    await expect(page).toHaveURL(/\/close-account$/);

    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    expect(accounts.state.calls).not.toContain("Logout");
    expect(accounts.state.accounts).toHaveLength(0);
    expect(accounts.state.calls.filter((c) => c === "DeleteAccount")).toHaveLength(1);
    expect(accounts.state.calls).toContain("RetryAccountDeletion");
  });

  test("a retry that still fails keeps the progress and can be repeated", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.state.deletionFailures = 2;
    await page.goto("/close-account");
    await confirm(page);
    await expect(page.getByTestId("close-progress")).toBeVisible();
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-progress")).toContainText("注销尚未确认完成");
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
  });

  test("a lost response to the start: the Cookie says where the deletion that did begin stands, and retry continues it", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.state.deletionFailures = 1;
    accounts.loseNextResponse("DeleteAccount");
    await page.goto("/close-account");
    await confirm(page);
    // No answer: the page does not claim that nothing happened; it reads the deletion the Cookie started.
    await expect(page.getByTestId("close-progress")).toContainText("账号已经关闭");
    await expect(page.getByTestId("close-product-tripmap")).toHaveAttribute("data-state", "done");
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    expect(accounts.state.calls.filter((c) => c === "DeleteAccount")).toHaveLength(1);
    expect(accounts.state.calls).toContain("RetryAccountDeletion");
    expect(accounts.state.calls).not.toContain("Logout");
  });

  test("a lost response and an unreadable progress: the same key continues the deletion, once", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.state.deletionFailures = 1;
    accounts.loseNextResponse("DeleteAccount");
    accounts.failNext("GetAccountDeletion", FAILURES.unavailable);
    await page.goto("/close-account");
    await confirm(page);
    // Nothing is known: the page says the account may be closed and lists no products.
    await expect(page.getByTestId("close-progress")).toContainText("账号可能已经关闭");
    await expect(page.getByTestId("close-products")).toHaveCount(0);
    expect(accounts.state.deletion).not.toBeNull();
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    const keys = accounts.state.bodies.get("DeleteAccount")!.map((b) => b.mutationId);
    expect(keys).toHaveLength(2);
    expect(new Set(keys).size).toBe(1);
    expect(accounts.state.calls.filter((c) => c === "Reauthenticate")).toHaveLength(1);
  });

  test("a lost response to a deletion that finished: the receipt shows the result, the account is not touched twice", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.loseNextResponse("DeleteAccount");
    await page.goto("/close-account");
    await confirm(page);
    await expect(page.getByTestId("close-done")).toBeVisible();
    expect(accounts.state.accounts).toHaveLength(0);
    expect(accounts.state.calls.filter((c) => c === "DeleteAccount")).toHaveLength(1);
  });

  test("a lost success response and an unreadable progress: replaying the same key returns the receipt", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.loseNextResponse("DeleteAccount");
    accounts.failNext("GetAccountDeletion", FAILURES.unavailable);
    await page.goto("/close-account");
    await confirm(page);
    await expect(page.getByTestId("close-progress")).toBeVisible();
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    const keys = accounts.state.bodies.get("DeleteAccount")!.map((b) => b.mutationId);
    expect(keys).toHaveLength(2);
    expect(new Set(keys).size).toBe(1);
    expect(accounts.state.accounts).toHaveLength(0);
  });

  test("a start request that never arrived (an outage): retrying with the same key starts it", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.failNext("DeleteAccount", FAILURES.unavailable);
    await page.goto("/close-account");
    await confirm(page);
    // An outage is reported as "not confirmed": the account may be closed. The server did nothing here,
    // and the Cookie has no deletion to show, so the products are not listed.
    await expect(page.getByTestId("close-progress")).toContainText("可能已经关闭");
    await expect(page.getByTestId("close-products")).toHaveCount(0);
    await page.getByRole("button", { name: "重试注销" }).click();
    // The same key now starts it (the session is still valid); nothing is lost.
    await expect(page.getByTestId("close-done")).toBeVisible();
    const keys = accounts.state.bodies.get("DeleteAccount")!.map((b) => b.mutationId);
    expect(new Set(keys).size).toBe(1);
  });

  test("a start request that never arrived and a session that ended meanwhile: nothing began, the form comes back", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.failNext("DeleteAccount", FAILURES.unavailable);
    await page.goto("/close-account");
    await confirm(page);
    await expect(page.getByTestId("close-progress")).toContainText("可能已经关闭");
    accounts.signOut();
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(form(page)).toBeVisible();
    await expect(page.getByTestId("close-form-page")).toContainText("注销没有开始，账号仍然有效");
    expect(accounts.state.deletion).toBeNull();
    expect(accounts.state.accounts).toHaveLength(1);
  });

  test("an outage while reading the progress is not a verdict: it is shown, with a retry", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.state.deletionFailures = 1;
    await page.goto("/close-account");
    await confirm(page);
    await expect(page.getByTestId("close-progress")).toBeVisible();
    accounts.failNext("GetAccountDeletion", FAILURES.unavailable);
    await page.reload();
    await expect(page.getByTestId("close-lookup-failed")).toContainText("不影响注销本身");
    await expect(page).toHaveURL(/\/close-account$/);
    await page.getByRole("button", { name: "重新读取进度" }).click();
    await expect(page.getByTestId("close-progress")).toBeVisible();
    expect(accounts.state.calls).not.toContain("Logout");
  });

  test("the session going away mid-deletion (another tab, the page coming back) does not restart or leave the page", async ({ page }) => {
    const { accounts } = await setup(page);
    accounts.state.deletionFailures = 1;
    await page.goto("/close-account");
    await confirm(page);
    await expect(page.getByTestId("close-progress")).toBeVisible();
    const reads = () => accounts.state.calls.filter((c) => c === "GetAccountDeletion").length;
    const meCalls = () => accounts.state.calls.filter((c) => c === "Me").length;
    const readsBefore = reads();
    const meBefore = meCalls();
    await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
    await expect.poll(meCalls).toBeGreaterThan(meBefore);
    await expect(page.getByTestId("close-progress")).toBeVisible();
    await expect(page).toHaveURL(/\/close-account$/);
    expect(reads()).toBe(readsBefore);
  });
});
