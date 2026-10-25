import { expect, test, type Page } from "@playwright/test";
import { control, failMetro, form, kind, sid, signIn, stack, state } from "./helpers";

/**
 * What can replace or clear the session Cookie while an account deletion is unfinished, in a real
 * browser (two tabs of one browser profile) against the real BFF, usersrv, tripmap and metro
 * started by internal/e2e TestOzidBrowserChain. While a deletion is unfinished that Cookie is the
 * only credential to see, refresh and retry it.
 *
 * The late requests and responses are controlled: the HTTPS front in front of the BFF can hold a
 * request before it reaches the BFF, or hold the BFF's answer (Set-Cookie included) until the test
 * releases it. A page that stopped caring cannot stop the browser from applying that Set-Cookie,
 * so a late response is the one thing no check made when the request arrived can undo. What each
 * test proves, and the case that stays open, is spelled out in its title and comments.
 */
test.skip(!process.env.OZID_REAL_STACK, "started by internal/e2e TestOzidBrowserChain");

const violations = new WeakMap<Page, string[]>();
test.beforeEach(({ context, page }) => {
  const seen: string[] = [];
  violations.set(page, seen);
  context.on("console", (m) => {
    if (/Content Security Policy|Refused to (load|connect|execute|apply)/i.test(m.text())) seen.push(m.text());
  });
});
test.afterEach(async ({ page }) => {
  await failMetro(false);
  expect(violations.get(page)).toEqual([]);
});

const BYSTANDER = "close-bystander";
const hold = (method: string, phase: "request" | "response") => control(`/hold?method=${method}&phase=${phase}`);
const release = (method: string) => control(`/release?method=${method}`);
const reached = async (method: string) => (await control<{ reached: boolean }>(`/reached?method=${method}`)).reached;

const panel = (page: Page) => page.getByTestId("auth-panel");
async function submitLogin(page: Page, subject: string) {
  await panel(page).getByLabel("邮箱").fill(`${subject}@example.com`);
  await panel(page).getByLabel("密码", { exact: true }).fill(stack.password);
  await panel(page).getByRole("button", { name: "登录", exact: true }).click();
}
async function confirmClose(page: Page) {
  await form(page).getByLabel("当前密码").fill(stack.password);
  await form(page).getByLabel("输入“注销”确认").fill("注销");
  await form(page).getByRole("button", { name: "确认永久注销" }).click();
}
const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

test.describe("the Cookie of an unfinished deletion", () => {
  test("F1: another account cannot sign in over it; the deletion can still be viewed, refreshed and retried, then the other account signs in", async ({ page, context }, testInfo) => {
    const subject = `close-guard-${kind(testInfo)}`;
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await failMetro(true);
    await confirmClose(page);
    await expect(page.getByTestId("close-progress")).toBeVisible();
    const cookie = await sid(context);
    expect(cookie).toBeTruthy();

    // A second tab of the same browser signs in as someone else.
    const other = await context.newPage();
    await other.goto(`${stack.id}/login`);
    await submitLogin(other, BYSTANDER);
    await expect(panel(other).getByRole("alert")).toContainText("尚未完成的账号注销");
    await expect(panel(other).getByRole("alert")).toContainText("换一个浏览器");
    await expect(other).toHaveURL(/\/login$/);
    expect(await noOverflow(other)).toBe(true);
    expect((await sid(context))?.value, "the refused sign-in left the Cookie alone").toBe(cookie!.value);
    expect((await state(BYSTANDER)).workflow).toBe("none");

    // The first tab still views, refreshes and retries; the first retry fails (metro is down) and keeps everything.
    await expect(page.getByTestId("close-progress")).toBeVisible();
    await page.reload();
    await expect(page.getByTestId("close-progress")).toBeVisible();
    await page.getByRole("button", { name: "刷新进度" }).click();
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-progress").getByRole("alert")).toContainText("注销尚未确认完成");
    expect((await sid(context))?.value).toBe(cookie!.value);

    await failMetro(false);
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    expect((await state(subject)).workflow).toBe("done");
    expect(await sid(context), "the finished deletion clears the Cookie").toBeUndefined();

    // Only now is the browser free: the same form signs the other account in.
    await submitLogin(other, BYSTANDER);
    await expect(other.getByTestId("overview")).toBeVisible();
  });

  test("F3, late sign-out request: a sign-out sent before the deletion began but handled after it leaves the Cookie alone", async ({ page, context }, testInfo) => {
    const subject = `close-lreq-${kind(testInfo)}`;
    await signIn(page, subject);
    const signedOutTab = await context.newPage();
    await signedOutTab.goto(`${stack.id}/profile`);
    await expect(signedOutTab.getByRole("button", { name: "退出登录" })).toBeVisible();
    await page.goto(`${stack.id}/close-account`);
    const cookie = await sid(context);

    await hold("Logout", "request");
    const signOut = signedOutTab.getByRole("button", { name: "退出登录" }).click();
    await expect.poll(() => reached("Logout")).toBe(true); // held in front of the BFF: not handled yet
    await failMetro(true);
    await confirmClose(page);
    await expect(page.getByTestId("close-progress")).toBeVisible();

    await release("Logout"); // the BFF now handles the old sign-out, after the deletion began
    await signOut;
    await expect(signedOutTab).toHaveURL(/\/login$/);
    expect((await sid(context))?.value, "a late sign-out request must not clear the Cookie").toBe(cookie!.value);

    await page.reload();
    await expect(page.getByTestId("close-progress")).toBeVisible();
    await failMetro(false);
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    expect((await state(subject)).workflow).toBe("done");
  });

  test("F3, late sign-out response: a sign-out the BFF handled before the deletion cannot be followed by one; nothing is left half-closed", async ({ page, context }, testInfo) => {
    const subject = `close-open-${kind(testInfo)}`;
    await signIn(page, subject);
    const signedOutTab = await context.newPage();
    await signedOutTab.goto(`${stack.id}/profile`);
    await expect(signedOutTab.getByRole("button", { name: "退出登录" })).toBeVisible();
    await page.goto(`${stack.id}/close-account`);
    await expect(form(page)).toBeVisible();
    const cookie = await sid(context);

    await hold("Logout", "response");
    const signOut = signedOutTab.getByRole("button", { name: "退出登录" }).click();
    await expect.poll(() => reached("Logout")).toBe(true); // handled: the session is revoked, the answer is still on its way
    expect(await sid(context)).toBeTruthy(); // the browser has not seen the clearing yet

    // The form of the first tab now tries to begin the deletion with the revoked session: refused,
    // so no deletion exists that the arriving Set-Cookie could orphan.
    await confirmClose(page);
    await expect(page).toHaveURL(/\/login$/);
    expect((await state(subject)).workflow).toBe("none");

    await release("Logout");
    await signOut;
    await expect.poll(async () => (await sid(context))?.value).toBeUndefined();
    // The account was never touched: it signs in again and has all its data.
    await signIn(page, subject);
    expect(await state(subject)).toMatchObject({ mediaRows: 1, object: true, visits: 1, metroRows: 1, workflow: "none" });
  });

  test("F1, another account's sign-in in another tab while nothing is pending: the page that shows the first account is refused, the second account is not closed", async ({ page, context }, testInfo) => {
    const subject = `close-swap-${kind(testInfo)}`;
    const other = await context.newPage();
    await other.goto(`${stack.id}/login`); // an anonymous form, opened before the first tab signed in
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await expect(page.getByTestId("close-account-who")).toContainText(`${subject}@example.com`);

    await submitLogin(other, BYSTANDER); // allowed: no deletion is pending; the Cookie is now the bystander's
    await expect(other.getByTestId("overview")).toBeVisible();

    // The password is the same for every test account, so only the expected-account check can stop this.
    await confirmClose(page);
    await expect(page).toHaveURL(/\/login$/);
    expect((await state(BYSTANDER)).workflow).toBe("none");
    expect((await state(subject)).workflow).toBe("none");
    await other.reload();
    await expect(other.getByTestId("overview")).toBeVisible(); // the bystander is still signed in
  });

  /**
   * The case no request-time check can reach, shown on purpose: a sign-in whose request the BFF
   * accepted while the Cookie had no deletion, and whose answer reaches the browser after the
   * deletion began. The deletion is persisted and finishes server-side, but the browser has lost
   * the Cookie that could show it. The page that asked is irrelevant: the browser applies the
   * Set-Cookie of the response itself.
   */
  test("F1, residual boundary: a sign-in response that arrives after the deletion began replaces the Cookie; the deletion still finishes, and the page never shows another account's deletion", async ({ page, context }, testInfo) => {
    const subject = `close-late-${kind(testInfo)}`;
    const other = await context.newPage();
    await other.goto(`${stack.id}/login`); // anonymous form, opened before the first tab signed in
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await expect(form(page)).toBeVisible();
    const cookie = await sid(context);

    await hold("LoginWithEmail", "response");
    await submitLogin(other, BYSTANDER);
    await expect.poll(() => reached("LoginWithEmail")).toBe(true); // accepted and answered by the BFF, answer held
    expect((await sid(context))?.value).toBe(cookie!.value);

    await failMetro(true);
    await confirmClose(page);
    await expect(page.getByTestId("close-progress")).toBeVisible();
    expect((await state(subject)).workflow).toBe("cleanup");

    await release("LoginWithEmail");
    await expect(other.getByTestId("overview")).toBeVisible();
    // The boundary: the late answer's Set-Cookie replaced the Cookie of the unfinished deletion.
    expect((await sid(context))?.value).not.toBe(cookie!.value);

    // What the user sees is not wrong: the reloaded page is the bystander's own, named, with nothing begun.
    await page.reload();
    await expect(page.getByTestId("close-account-who")).toContainText(`${BYSTANDER}@example.com`);
    expect((await state(BYSTANDER)).workflow).toBe("none");

    // And the deletion is not lost: the server finishes it, data and media included.
    await failMetro(false);
    await control("/run-deletions");
    expect(await state(subject)).toMatchObject({ mediaRows: 0, object: false, visits: 0, metroRows: 0, workflow: "done" });
    expect((await state(BYSTANDER)).workflow).toBe("none");
  });
});
