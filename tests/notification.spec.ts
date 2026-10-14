import { expect, test, type Page } from "@playwright/test";
import { fakeBackend, green, login } from "./fake-backend";

const visits = [{ code: "330100", name: "杭州市", province: "330000", color: green, date: "2025-10-15", note: "私密备注不应出现在通知里" }];

const openDialog = async (page: Page) => {
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "提醒与通知" }).click();
  const dialog = page.getByRole("dialog", { name: "提醒与通知" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("正在读取提醒设置…")).toHaveCount(0);
  return dialog;
};

const noOverflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);

/** A browser whose push service is faked: there is no real FCM in CI. */
const fakePush = (page: Page, permission: "granted" | "denied" = "granted") =>
  page.addInitScript((answer) => {
    let sub: unknown = null;
    const bytes = (...n: number[]) => new Uint8Array(n).buffer;
    const made = {
      endpoint: "https://push.example.com/e2e-device",
      getKey: (name: string) => (name === "p256dh" ? bytes(4, 5, 6) : bytes(1, 2, 3)),
      unsubscribe: async () => {
        sub = null;
        return true;
      },
    };
    const reg = {
      pushManager: {
        getSubscription: async () => sub,
        subscribe: async () => (sub = made),
      },
    };
    Object.defineProperty(navigator, "serviceWorker", {
      configurable: true,
      value: { register: async () => reg, ready: Promise.resolve(reg), getRegistration: async () => reg },
    });
    Object.defineProperty(window, "Notification", {
      configurable: true,
      value: Object.assign(function () {}, { permission: "default", requestPermission: async () => answer }),
    });
  }, permission);

test.describe("F12 reminders and notification settings", () => {
  test("everything starts off; email needs a verified address; settings save and survive reopening", async ({ page }) => {
    const api = fakeBackend(page, { visits });
    await api.install();
    await login(page);
    const dialog = await openDialog(page);

    // Nothing is switched on by default, and email cannot be enabled before verification.
    for (const box of await dialog.getByRole("checkbox").all()) await expect(box).not.toBeChecked();
    await expect(dialog.getByRole("checkbox", { name: /^邮件/ }).first()).toBeDisabled();
    await expect(dialog.getByRole("button", { name: "保存设置" })).toBeDisabled();
    await expect(dialog.getByLabel("通知预览")).not.toContainText("杭州");

    // Verify an address: a wrong code is refused, the right one unlocks email.
    await dialog.getByLabel("邮箱地址").fill("traveler@example.com");
    await dialog.getByRole("button", { name: "发送验证码" }).click();
    await expect(dialog.getByRole("status").filter({ hasText: "验证码已发送到 t***@example.com" })).toBeVisible();
    await dialog.getByLabel(/^验证码/).fill("000000");
    await dialog.getByRole("button", { name: "确认", exact: true }).click();
    await expect(dialog.getByRole("alert")).toContainText("验证码");
    await dialog.getByLabel(/^验证码/).fill(api.state.notify.code);
    await dialog.getByRole("button", { name: "确认", exact: true }).click();
    await expect(dialog.getByText("t***@example.com")).toBeVisible();

    // Turn memory email on, show places, change hour and time zone, then save.
    await dialog.getByRole("group", { name: "回忆提醒" }).getByRole("checkbox", { name: /^邮件/ }).check();
    await dialog.getByRole("checkbox", { name: /在通知中显示城市名称/ }).check();
    await expect(dialog.getByLabel("通知预览")).toContainText("杭州市");
    await dialog.getByLabel("当天几点后发送").selectOption("20");
    await dialog.getByLabel("时区").selectOption("Asia/Tokyo");
    await expect(dialog.getByText("有未保存的修改")).toBeVisible();
    await dialog.getByRole("button", { name: "保存设置" }).click();
    await expect(dialog.getByText("设置已保存。")).toBeVisible();
    await expect(dialog.getByRole("button", { name: "保存设置" })).toBeDisabled();
    expect(api.state.notify.updates.at(-1)).toEqual({ timezone: "Asia/Tokyo", sendHour: 20, showPlaces: true, on: ["memory/email"] });

    // A test email can be sent, and a failure is explained.
    await dialog.getByRole("button", { name: "发送测试邮件" }).click();
    await expect(dialog.getByText(/测试邮件已发送/)).toBeVisible();
    expect(api.state.notify.tests).toEqual(["email"]);
    api.state.notify.failTest = true;
    await dialog.getByRole("button", { name: "发送测试邮件" }).click();
    await expect(dialog.getByRole("alert")).toBeVisible();

    // Reopen: the saved values come back from the server.
    await dialog.getByRole("button", { name: "关闭", exact: true }).first().click();
    const again = await openDialog(page);
    await expect(again.getByRole("group", { name: "回忆提醒" }).getByRole("checkbox", { name: /^邮件/ })).toBeChecked();
    await expect(again.getByLabel("当天几点后发送")).toHaveValue("20");
    await expect(again.getByLabel("时区")).toHaveValue("Asia/Tokyo");

    // Removing the address turns every email reminder off but keeps the other settings.
    await again.getByRole("button", { name: "移除邮箱" }).click();
    await expect(again.getByText(/已移除邮箱/)).toBeVisible();
    await expect(again.getByRole("group", { name: "回忆提醒" }).getByRole("checkbox", { name: /^邮件/ })).not.toBeChecked();
    expect(api.state.notify.email).toBe("");
    expect(api.state.notify.showPlaces).toBe(true);
    expect(await noOverflow(page)).toBe(true);
  });

  test("push: enable this device, test it, disable it; denial and missing server support are explained", async ({ page }) => {
    await fakePush(page);
    const api = fakeBackend(page, { visits });
    await api.install();
    await login(page);
    let dialog = await openDialog(page);

    await dialog.getByRole("button", { name: "在此设备启用推送" }).click();
    await expect(dialog.getByText("已在此设备启用推送")).toBeVisible();
    expect(api.state.notify.endpoints).toEqual(["https://push.example.com/e2e-device"]);
    await dialog.getByRole("group", { name: "年度旅行回顾" }).getByRole("checkbox", { name: /^浏览器推送/ }).check();
    await dialog.getByRole("button", { name: "保存设置" }).click();
    await expect(dialog.getByText("设置已保存。")).toBeVisible();
    expect(api.state.notify.updates.at(-1)?.on).toEqual(["annual_report/push"]);

    await dialog.getByRole("button", { name: "发送测试推送" }).click();
    await expect(dialog.getByText("测试推送已发出。")).toBeVisible();
    expect(api.state.notify.tests).toEqual(["push"]);

    await dialog.getByRole("button", { name: "停用此设备" }).click();
    await expect(dialog.getByText("已停用此设备的推送。")).toBeVisible();
    expect(api.state.notify.endpoints).toEqual([]);
    expect(await noOverflow(page)).toBe(true);
  });

  test("push permission refusal is explained and nothing is registered", async ({ page }) => {
    await fakePush(page, "denied");
    const api = fakeBackend(page, { visits });
    await api.install();
    await login(page);
    const dialog = await openDialog(page);
    await dialog.getByRole("button", { name: "在此设备启用推送" }).click();
    await expect(dialog.getByRole("alert")).toContainText("没有获得通知权限");
    expect(api.state.notify.endpoints).toEqual([]);
  });

  test("a server without email or push says so and offers no switches for them", async ({ page }) => {
    const api = fakeBackend(page, { visits }, { email: false, push: false });
    await api.install();
    await login(page);
    const dialog = await openDialog(page);
    await expect(dialog.getByText("此服务器尚未配置邮件发送")).toBeVisible();
    await expect(dialog.getByText("此服务器尚未配置浏览器推送")).toBeVisible();
    for (const box of await dialog.getByRole("checkbox", { name: /^(邮件|浏览器推送)/ }).all()) await expect(box).toBeDisabled();
    await expect(dialog.getByRole("button", { name: "发送验证码" })).toHaveCount(0);
  });

  test("the reminder service worker is served as javascript", async ({ request }) => {
    const res = await request.get("/sw.js");
    expect(res.ok()).toBe(true);
    expect(res.headers()["content-type"]).toMatch(/javascript/);
    expect(await res.text()).toContain("notificationclick");
  });
});
