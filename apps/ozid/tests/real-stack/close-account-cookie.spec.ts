import { expect, test, type Page } from "@playwright/test";
import { control, expectRecoveryAttributes, failMetro, form, kind, recoveries, recovery, sid, signIn, stack, state } from "./helpers";

/**
 * The recovery Cookie of an unfinished account deletion, in a real browser (two tabs of one
 * browser profile) against the real BFF, usersrv, tripmap and metro started by internal/e2e
 * TestOzidBrowserChain. The dedicated HttpOnly Cookie, not the session Cookie, is what shows,
 * refreshes and retries a deletion; the BFF has the browser store it before the irreversible
 * workflow begins; signing in, signing out and ending sessions leave it alone; the finished
 * deletion removes it and signs nobody else out.
 *
 * Lost and late requests and responses are controlled: the HTTPS front in front of the BFF can
 * hold a request before it reaches the BFF, hold the BFF's answer (Set-Cookie included) until the
 * test releases it, or drop the answer for good (the BFF did the work, the browser never hears).
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
const hold = (method: string, phase: "request" | "response" | "drop", skip = 0) => control(`/hold?method=${method}&phase=${phase}&skip=${skip}`);
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
const progress = (page: Page) => page.getByTestId("close-progress");

/** The page is the signed-in bystander's close page, showing the other account's unfinished deletion. */
async function expectsForeignProgress(page: Page) {
  await expect(progress(page)).toBeVisible();
  await expect(page.getByTestId("close-other-account")).toContainText(`${BYSTANDER}@example.com`);
  await expect(page.getByTestId("close-form")).toHaveCount(0);
  expect(await noOverflow(page)).toBe(true);
}

test.describe("the recovery Cookie of an unfinished deletion", () => {
  test("another account signs in meanwhile: the Cookie stays, the page tells whose deletion it is, refresh and retry work, and finishing it leaves the other account signed in", async ({ page, context }, testInfo) => {
    const subject = `close-guard-${kind(testInfo)}`;
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await failMetro(true);
    await confirmClose(page);
    await expect(progress(page)).toBeVisible();
    const cookie = await expectRecoveryAttributes(context, page);

    // A second tab of the same browser signs in as someone else: nothing refuses it, nothing changes the Cookie.
    const other = await context.newPage();
    await other.goto(`${stack.id}/login`);
    await submitLogin(other, BYSTANDER);
    await expect(other.getByTestId("overview")).toBeVisible();
    expect((await recovery(context))?.value).toBe(cookie.value);
    expect((await sid(context))?.value).toBeTruthy();
    expect((await state(BYSTANDER)).workflow).toBe("none");

    // The other tab's close page shows the unfinished deletion as the other account's, never as its own.
    await other.goto(`${stack.id}/close-account`);
    await expectsForeignProgress(other);
    // The first tab reloads: the same browser, now signed in as the other account, sees the same.
    await page.reload();
    await expectsForeignProgress(page);
    await page.getByRole("button", { name: "刷新进度" }).click();
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    // Retrying while metro is down fails and keeps everything, the other account's session included.
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(progress(page).getByRole("alert")).toContainText("注销尚未确认完成");
    expect((await recovery(context))?.value).toBe(cookie.value);
    expect((await state(subject)).workflow).toBe("cleanup");

    await failMetro(false);
    await page.getByRole("button", { name: "重试注销" }).click();
    // Finished: the page goes back to the signed-in account's own form, which was not closed.
    await expect(form(page)).toBeVisible();
    expect((await state(subject)).workflow).toBe("done");
    expect(await recovery(context), "the finished deletion removes the recovery Cookie").toBeUndefined();
    expect((await sid(context))?.value, "and does not sign the other account out").toBeTruthy();
    await other.reload();
    await expect(other.getByTestId("close-form")).toBeVisible();
    await other.goto(`${stack.id}/`);
    await expect(other.getByTestId("overview")).toBeVisible();
    expect((await state(BYSTANDER)).workflow).toBe("none");
  });

  test("the first response, the one that carries the Cookie, is lost: nothing irreversible began; the retry stores the Cookie and then begins", async ({ page, context }, testInfo) => {
    const subject = `close-lost1-${kind(testInfo)}`;
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await expect(form(page)).toBeVisible();
    await failMetro(true);
    await hold("DeleteAccount", "drop"); // the BFF answers "store the Cookie and repeat"; the browser never hears it
    await confirmClose(page);
    await expect.poll(() => reached("DeleteAccount")).toBe(true);
    await expect(progress(page)).toBeVisible();
    await expect(progress(page)).toContainText("注销尚未确认完成");
    // The BFF only asked for the Cookie: no workflow, no Cookie in the browser, the session is still the account's.
    expect((await state(subject)).workflow).toBe("none");
    expect(await recovery(context)).toBeUndefined();
    expect(await sid(context)).toBeTruthy();

    // The retry is the same call with the same key: the Cookie is stored, then the deletion begins.
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(progress(page).getByRole("alert")).toContainText("注销尚未确认完成"); // metro is down
    expect((await state(subject)).workflow).toBe("cleanup");
    await expectRecoveryAttributes(context, page);
    await page.reload();
    await expect(progress(page)).toBeVisible();
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    await failMetro(false);
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    expect(await state(subject)).toMatchObject({ mediaRows: 0, object: false, visits: 0, metroRows: 0, workflow: "done" });
    expect(await recovery(context)).toBeUndefined();
  });

  test("the start response is lost after the deletion began: the Cookie was stored before, so the page still shows, refreshes and retries it, even as another account", async ({ page, context }, testInfo) => {
    const subject = `close-lost2-${kind(testInfo)}`;
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await expect(form(page)).toBeVisible();
    await failMetro(true);
    await hold("DeleteAccount", "drop", 1); // the first call stores the Cookie; the second begins, and its answer is lost
    await confirmClose(page);
    await expect.poll(() => reached("DeleteAccount")).toBe(true);
    await expect(progress(page)).toBeVisible();
    expect((await state(subject)).workflow).toBe("cleanup"); // began, though the browser never got the answer
    const cookie = await expectRecoveryAttributes(context, page);
    // The page found it by the Cookie and lists the products.
    await expect(page.getByTestId("close-product-tripmap")).toHaveAttribute("data-state", "done");

    // The browser still holds the stale session Cookie of the account that closed; another account signs in over it.
    const other = await context.newPage();
    await other.goto(`${stack.id}/login`);
    await submitLogin(other, BYSTANDER);
    await expect(other.getByTestId("overview")).toBeVisible();
    expect((await recovery(context))?.value).toBe(cookie.value);

    await page.reload();
    await expectsForeignProgress(page);
    await failMetro(false);
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(form(page)).toBeVisible();
    expect(await state(subject)).toMatchObject({ mediaRows: 0, object: false, visits: 0, metroRows: 0, workflow: "done" });
    expect(await recovery(context)).toBeUndefined();
    await other.reload();
    await other.goto(`${stack.id}/`);
    await expect(other.getByTestId("overview")).toBeVisible(); // the bystander is still signed in
  });

  test("a restarted BFF finishes it from the persisted state; the reloaded page shows the receipt for the same Cookie, then the Cookie goes", async ({ page, context }, testInfo) => {
    const subject = `close-rst-${kind(testInfo)}`;
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await failMetro(true);
    await confirmClose(page);
    await expect(progress(page)).toBeVisible();
    const cookie = await expectRecoveryAttributes(context, page);
    expect((await state(subject)).workflow).toBe("cleanup");

    await failMetro(false);
    await control("/run-deletions?restart=1"); // a fresh BFF process runs the background pass
    expect(await state(subject)).toMatchObject({ mediaRows: 0, object: false, visits: 0, metroRows: 0, workflow: "done" });
    expect((await recovery(context))?.value, "nothing in the browser changed behind its back").toBe(cookie.value);

    await page.reload();
    await expect(page.getByTestId("close-done")).toBeVisible();
    expect(await recovery(context), "reading the receipt removes the recovery Cookie").toBeUndefined();
    expect(await noOverflow(page)).toBe(true);
  });

  test("late sign-out request: one sent before the deletion began but handled after it leaves the recovery Cookie alone", async ({ page, context }, testInfo) => {
    const subject = `close-lreq-${kind(testInfo)}`;
    await signIn(page, subject);
    const signedOutTab = await context.newPage();
    await signedOutTab.goto(`${stack.id}/profile`);
    await expect(signedOutTab.getByRole("button", { name: "退出登录" })).toBeVisible();
    await page.goto(`${stack.id}/close-account`);
    await expect(form(page)).toBeVisible();

    await hold("Logout", "request");
    const signOut = signedOutTab.getByRole("button", { name: "退出登录" }).click();
    await expect.poll(() => reached("Logout")).toBe(true); // held in front of the BFF: not handled yet
    await failMetro(true);
    await confirmClose(page);
    await expect(progress(page)).toBeVisible();
    const cookie = await expectRecoveryAttributes(context, page);

    await release("Logout"); // the BFF now handles the old sign-out, after the deletion began
    await signOut;
    await expect(signedOutTab).toHaveURL(/\/login$/);
    expect((await recovery(context))?.value, "a late sign-out request must not touch the recovery Cookie").toBe(cookie.value);

    await page.reload();
    await expect(progress(page)).toBeVisible();
    await failMetro(false);
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    expect((await state(subject)).workflow).toBe("done");
  });

  test("late sign-out response: a sign-out the BFF handled first revokes the session, so no deletion begins; nothing is left half-closed", async ({ page, context }, testInfo) => {
    const subject = `close-open-${kind(testInfo)}`;
    await signIn(page, subject);
    const signedOutTab = await context.newPage();
    await signedOutTab.goto(`${stack.id}/profile`);
    await expect(signedOutTab.getByRole("button", { name: "退出登录" })).toBeVisible();
    await page.goto(`${stack.id}/close-account`);
    await expect(form(page)).toBeVisible();

    await hold("Logout", "response");
    const signOut = signedOutTab.getByRole("button", { name: "退出登录" }).click();
    await expect.poll(() => reached("Logout")).toBe(true); // handled: the session is revoked, the answer is still on its way
    expect(await sid(context)).toBeTruthy(); // the browser has not seen the clearing yet

    // The form of the first tab tries to begin with the revoked session: refused, nothing began.
    await confirmClose(page);
    await expect(page).toHaveURL(/\/login$/);
    expect((await state(subject)).workflow).toBe("none");
    expect(await recovery(context)).toBeUndefined();

    await release("Logout");
    await signOut;
    await expect.poll(async () => (await sid(context))?.value).toBeUndefined();
    // The account was never touched: it signs in again and has all its data.
    await signIn(page, subject);
    expect(await state(subject)).toMatchObject({ mediaRows: 1, object: true, visits: 1, metroRows: 1, workflow: "none" });
  });

  test("another account's sign-in in another tab while nothing is pending: the page that shows the first account is refused, the second account is not closed", async ({ page, context }, testInfo) => {
    const subject = `close-swap-${kind(testInfo)}`;
    const other = await context.newPage();
    await other.goto(`${stack.id}/login`); // an anonymous form, opened before the first tab signed in
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await expect(page.getByTestId("close-account-who")).toContainText(`${subject}@example.com`);

    await submitLogin(other, BYSTANDER); // the session Cookie is now the bystander's
    await expect(other.getByTestId("overview")).toBeVisible();

    // The password is the same for every test account, so only the expected-account check can stop this.
    await confirmClose(page);
    await expect(page).toHaveURL(/\/login$/);
    expect((await state(BYSTANDER)).workflow).toBe("none");
    expect((await state(subject)).workflow).toBe("none");
    expect(await recovery(context)).toBeUndefined();
    await other.reload();
    await expect(other.getByTestId("overview")).toBeVisible(); // the bystander is still signed in
  });

  /**
   * What used to be unrecoverable: a sign-in the BFF accepted while nothing was pending, whose
   * answer reaches the browser after the deletion began and replaces the session Cookie. The
   * recovery Cookie is a different credential, so the original workflow is still visible,
   * refreshable and retryable, and finishing it leaves the late sign-in's account signed in.
   */
  test("a sign-in response that arrives after the deletion began replaces only the session Cookie: the original deletion is still shown, refreshed and retried", async ({ page, context }, testInfo) => {
    const subject = `close-late-${kind(testInfo)}`;
    const other = await context.newPage();
    await other.goto(`${stack.id}/login`); // anonymous form, opened before the first tab signed in
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await expect(form(page)).toBeVisible();

    await hold("LoginWithEmail", "response");
    await submitLogin(other, BYSTANDER);
    await expect.poll(() => reached("LoginWithEmail")).toBe(true); // accepted and answered by the BFF, answer held
    const before = (await sid(context))?.value;

    await failMetro(true);
    await confirmClose(page);
    await expect(progress(page)).toBeVisible();
    expect((await state(subject)).workflow).toBe("cleanup");
    const cookie = await expectRecoveryAttributes(context, page);

    await release("LoginWithEmail");
    await expect(other.getByTestId("overview")).toBeVisible();
    // The late answer replaced the session Cookie, as any sign-in does, and nothing else.
    expect((await sid(context))?.value).not.toBe(before);
    expect((await recovery(context))?.value).toBe(cookie.value);

    // The page is the signed-in bystander's own, and still shows the original deletion: view, refresh, retry.
    await page.reload();
    await expectsForeignProgress(page);
    await page.getByRole("button", { name: "刷新进度" }).click();
    await expect(page.getByTestId("close-product-tripmap")).toHaveAttribute("data-state", "done");
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(progress(page).getByRole("alert")).toContainText("注销尚未确认完成");

    await failMetro(false);
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(form(page)).toBeVisible();
    expect(await state(subject)).toMatchObject({ mediaRows: 0, object: false, visits: 0, metroRows: 0, workflow: "done" });
    expect((await state(BYSTANDER)).workflow).toBe("none");
    expect(await recovery(context)).toBeUndefined();
    await other.reload();
    await other.goto(`${stack.id}/`);
    await expect(other.getByTestId("overview")).toBeVisible(); // the bystander is still signed in
  });
  /**
   * S1: a status read made before the begin is answered after it. That answer writes nothing, so
   * the Cookie confirmed a moment ago is still there and the deletion is still shown and retried.
   */
  test("a status answer held until after the deletion began cannot take the recovery Cookie away", async ({ page, context }, testInfo) => {
    const subject = `close-s1-${kind(testInfo)}`;
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await expect(form(page)).toBeVisible();
    await failMetro(true);
    await hold("DeleteAccount", "request", 1); // the first call stores the Cookie; the second, the begin, waits in front of the BFF
    await confirmClose(page);
    await expect.poll(() => reached("DeleteAccount")).toBe(true);
    expect((await state(subject)).workflow).toBe("none");
    const cookie = await expectRecoveryAttributes(context, page);

    // Another tab of the same browser asks for the status now (nothing exists yet); its answer is held.
    await hold("GetAccountDeletion", "response");
    const reader = await context.newPage();
    await reader.goto(`${stack.id}/close-account`, { waitUntil: "commit" });
    await expect.poll(() => reached("GetAccountDeletion")).toBe(true);

    await release("DeleteAccount"); // the begin is handled now
    await expect(progress(page)).toBeVisible();
    expect((await state(subject)).workflow).toBe("cleanup");
    await release("GetAccountDeletion"); // the late answer reaches the browser after the begin
    await reader.waitForLoadState("networkidle");
    expect((await recovery(context))?.value, "the late status answer must not take the Cookie away").toBe(cookie.value);

    await page.reload();
    await expect(progress(page)).toBeVisible();
    await failMetro(false);
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    expect(await state(subject)).toMatchObject({ mediaRows: 0, object: false, visits: 0, metroRows: 0, workflow: "done" });
    expect(await recovery(context)).toBeUndefined();
  });

  /**
   * S2/R2: the browser holds the revoked session Cookie of the closing account while a status answer is on
   * its way and another account signs in. No deletion answer writes the session Cookie, so the late one
   * leaves the signed-in account signed in.
   */
  test("a status answer that arrives after another account signed in leaves the session Cookie and the signed-in account alone", async ({ page, context }, testInfo) => {
    const subject = `close-r2-${kind(testInfo)}`;
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await failMetro(true);
    await confirmClose(page);
    await expect(progress(page)).toBeVisible();
    const cookie = await expectRecoveryAttributes(context, page);

    await hold("GetAccountDeletion", "response");
    const reader = await context.newPage();
    await reader.goto(`${stack.id}/close-account`, { waitUntil: "commit" });
    await expect.poll(() => reached("GetAccountDeletion")).toBe(true); // handled with the revoked session Cookie, answer held

    const other = await context.newPage();
    await other.goto(`${stack.id}/login`);
    await submitLogin(other, BYSTANDER);
    await expect(other.getByTestId("overview")).toBeVisible();
    const signedIn = (await sid(context))?.value;
    expect(signedIn).toBeTruthy();

    await release("GetAccountDeletion");
    await expect(reader.getByTestId("close-progress")).toBeVisible();
    expect((await sid(context))?.value, "the late answer must not rewrite the session Cookie").toBe(signedIn);
    expect((await recovery(context))?.value).toBe(cookie.value);
    await other.goto(`${stack.id}/`);
    await expect(other.getByTestId("overview")).toBeVisible();
    expect((await state(BYSTANDER)).workflow).toBe("none");

    await failMetro(false);
    await page.reload();
    await expectsForeignProgress(page);
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(form(page)).toBeVisible();
    expect((await state(subject)).workflow).toBe("done");
    expect((await sid(context))?.value).toBe(signedIn);
    expect(await recovery(context)).toBeUndefined();
  });

  /**
   * R3: the completion answer of the first deletion arrives after a second account began its own. It
   * clears only its own generation's Cookie, so the second deletion stays recoverable.
   */
  test("the completion answer of an older deletion held until a newer one began leaves the newer Cookie alone", async ({ page, context }, testInfo) => {
    const first = `close-r3a-${kind(testInfo)}`;
    const second = `close-r3b-${kind(testInfo)}`;
    await signIn(page, first);
    await page.goto(`${stack.id}/close-account`);
    await failMetro(true);
    await confirmClose(page);
    await expect(progress(page)).toBeVisible();
    const g1 = await expectRecoveryAttributes(context, page);

    await failMetro(false);
    await hold("RetryAccountDeletion", "response"); // the retry finishes it; the answer, which clears G1, is held
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect.poll(() => reached("RetryAccountDeletion")).toBe(true);
    expect((await state(first)).workflow).toBe("done");
    expect((await recovery(context))?.value).toBe(g1.value);

    // Meanwhile another account signs in in the same browser and begins its own deletion.
    const other = await context.newPage();
    await other.goto(`${stack.id}/login`);
    await submitLogin(other, second);
    await expect(other.getByTestId("overview")).toBeVisible();
    await other.goto(`${stack.id}/close-account`);
    await failMetro(true);
    await confirmClose(other);
    await expect(progress(other)).toBeVisible();
    expect((await state(second)).workflow).toBe("cleanup");
    // (The second account's close page already read the first receipt, which may have removed G1: either way
    // the second deletion has its own, different, Cookie.)
    const g2 = (await recoveries(context)).map((c) => c.value).find((v) => v !== g1.value)!;
    expect(g2, "the second deletion has its own recovery Cookie").toBeTruthy();

    await release("RetryAccountDeletion"); // the first completion answer, late
    await expect.poll(async () => (await recoveries(context)).map((c) => c.value)).toEqual([g2]);

    // The second deletion is still shown, refreshed and finished by its own Cookie.
    await other.reload();
    await expect(progress(other)).toBeVisible();
    await failMetro(false);
    await other.getByRole("button", { name: "重试注销" }).click();
    await expect(other.getByTestId("close-done")).toBeVisible();
    expect((await state(second)).workflow).toBe("done");
    expect(await recoveries(context)).toEqual([]);
  });
});
