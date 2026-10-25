import { expect, test, type Page } from "@playwright/test";
import { failMetro, form, kind, sid, signIn, stack, state } from "./helpers";

/**
 * Closing an account in a real browser against the real BFF, usersrv, tripmap (with media in an
 * object store) and metro, started by internal/e2e TestOzidBrowserChain: HTTPS, sibling
 * sub-domains, the BFF's host-only Cookie. Nothing of the chain is mocked. The accounts are
 * isolated test accounts seeded for this scenario (each with a photo, a visit and a ride; one per
 * Playwright project, since an account can be closed once), and the "control" origin reports what
 * is left of them and switches an injected metro outage. The object store is the harness's
 * in-memory one: this proves the workflow and its recovery, not a vendor's S3 (the S3 deletion
 * itself is covered by the CI-only real-S3 test in internal/e2e).
 */
test.skip(!process.env.OZID_REAL_STACK, "started by internal/e2e TestOzidBrowserChain");

const violations = new WeakMap<Page, string[]>();
test.beforeEach(({ page }) => {
  const seen: string[] = [];
  violations.set(page, seen);
  page.on("console", (m) => {
    if (/Content Security Policy|Refused to (load|connect|execute|apply)/i.test(m.text())) seen.push(m.text());
  });
});
test.afterEach(async ({ page }) => {
  await failMetro(false);
  expect(violations.get(page)).toEqual([]);
});

test.describe("closing the account", () => {
  test("a product outage keeps it open and recoverable: Cookie kept, progress survives a reload, retry removes everything including media", async ({ page, context }, testInfo) => {
    const subject = `close-main-${kind(testInfo)}`;
    const bystander = "close-bystander";
    // Local footprints that are not the account's: an anonymous product footprint and an OzID item.
    await page.goto(`${stack.trip}/`);
    await page.evaluate(() => localStorage.setItem("anonymous-footprint", "kept"));
    await page.goto(`${stack.id}/login`);
    await page.evaluate(() => localStorage.setItem("someone-elses-cache", "kept"));

    await signIn(page, subject);
    const before = await state(subject);
    expect(before).toMatchObject({ mediaRows: 1, object: true, visits: 1, metroRows: 1, workflow: "none" });
    const bystanderBefore = await state(bystander);

    // The page lists the whole scope before anything happens.
    await page.getByRole("link", { name: "注销账号" }).first().click();
    await expect(page.getByRole("heading", { level: 1, name: "注销账号" })).toBeVisible();
    for (const text of ["共用账号", "山海足迹", "地铁足迹", "媒体文件", "无法恢复", "先到对应产品里导出尚未同步的内容"]) await expect(page.getByTestId("close-scope")).toContainText(text);
    await expect(page.getByRole("button", { name: "退出登录" })).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    // The password is checked again: a wrong one closes nothing.
    await form(page).getByLabel("当前密码").fill("not the password");
    await form(page).getByLabel("输入“注销”确认").fill("注销");
    await form(page).getByRole("button", { name: "确认永久注销" }).click();
    await expect(form(page)).toContainText("当前密码不正确");
    expect((await state(subject)).workflow).toBe("none");

    // Metro goes down, then the real thing: tripmap (and its media) is cleaned, metro is not.
    await failMetro(true);
    await form(page).getByLabel("当前密码").fill(stack.password);
    await form(page).getByRole("button", { name: "确认永久注销" }).click();
    const progress = page.getByTestId("close-progress");
    await expect(progress).toBeVisible();
    await expect(page.getByTestId("close-product-tripmap")).toHaveAttribute("data-state", "done");
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    await expect(page.getByRole("button", { name: "退出登录" })).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    const mid = await state(subject);
    expect(mid).toMatchObject({ mediaRows: 0, object: false, visits: 0, metroRows: 1, workflow: "cleanup" });

    // The Cookie is still there and unchanged: it is the only way to see and continue this.
    const cookie = await sid(context);
    expect(cookie, "session cookie kept while the deletion is unfinished").toBeTruthy();
    expect(cookie).toMatchObject({ httpOnly: true, secure: true, sameSite: "Lax" });

    // The account is closed everywhere: the products see an anonymous browser and do not touch the Cookie.
    await page.goto(`${stack.trip}/`);
    await expect(page.locator("#me")).toHaveText(/^anonymous/);
    expect((await sid(context))?.value).toBe(cookie!.value);
    // A sign-out request from a page of OzID (another tab) is harmless too: it does not clear the Cookie.
    await page.goto(`${stack.id}/close-account`);
    await expect(page.getByTestId("close-progress")).toBeVisible();
    const logoutStatus = await page.evaluate(async (api) => {
      const call = (method: string, headers: Record<string, string> = {}) =>
        fetch(`${api}/api/bff.v1.AuthService/${method}`, { method: "POST", credentials: "include", body: "{}", headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1", ...headers } });
      const token = ((await (await call("IssueCSRFToken")).json()) as { token: string }).token;
      return (await call("Logout", { "X-CSRF-Token": token })).status;
    }, stack.api);
    expect(logoutStatus).toBeLessThan(500);
    expect((await sid(context))?.value).toBe(cookie!.value);

    // A reload: no session, but the Cookie finds the deletion and its progress.
    await page.reload();
    await expect(page.getByTestId("close-progress")).toBeVisible();
    await expect(page.getByTestId("close-product-metro")).toHaveAttribute("data-state", "pending");
    await expect(page).toHaveURL(`${stack.id}/close-account`);

    // Retrying while metro is still down fails and keeps everything.
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-progress")).toContainText("注销尚未确认完成");
    expect((await state(subject)).workflow).toBe("cleanup");
    expect((await sid(context))?.value).toBe(cookie!.value);

    // Metro is back: the retry finishes it, the Cookie goes only now.
    await failMetro(false);
    await page.getByRole("button", { name: "重试注销" }).click();
    await expect(page.getByTestId("close-done")).toBeVisible();
    const after = await state(subject);
    expect(after).toMatchObject({ mediaRows: 0, object: false, visits: 0, metroRows: 0, workflow: "done" });
    expect(await sid(context), "the finished deletion clears the Cookie").toBeUndefined();

    // Nobody else's data moved, and the local content that was not the account's is untouched.
    expect(await state(bystander)).toEqual(bystanderBefore);
    await page.goto(`${stack.trip}/`);
    expect(await page.evaluate(() => localStorage.getItem("anonymous-footprint"))).toBe("kept");
    await page.goto(`${stack.id}/login`);
    expect(await page.evaluate(() => localStorage.getItem("someone-elses-cache"))).toBe("kept");

    // The account cannot sign in any more; opening the page without the Cookie leads to sign-in.
    await page.getByTestId("auth-panel").getByLabel("邮箱").fill(`${subject}@example.com`);
    await page.getByTestId("auth-panel").getByLabel("密码", { exact: true }).fill(stack.password);
    await page.getByTestId("auth-panel").getByRole("button", { name: "登录", exact: true }).click();
    await expect(page.getByRole("alert")).toBeVisible();
    await page.goto(`${stack.id}/close-account`);
    await expect(page).toHaveURL(/\/login$/);
  });

  test("a platform administrator is refused on the page: the account stays open", async ({ page }, testInfo) => {
    const subject = `close-admin-${kind(testInfo)}`;
    await signIn(page, subject);
    await page.goto(`${stack.id}/close-account`);
    await form(page).getByLabel("当前密码").fill(stack.password);
    await form(page).getByLabel("输入“注销”确认").fill("注销");
    await form(page).getByRole("button", { name: "确认永久注销" }).click();
    await expect(form(page)).toContainText("平台管理员，不能在网页上注销");
    await expect(form(page)).toContainText("联系运维");
    // Nothing was closed: the overview still opens with the same session.
    await page.goto(`${stack.id}/`);
    await expect(page.getByTestId("overview")).toBeVisible();
  });
});
