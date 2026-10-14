import { test, expect, type Page } from "@playwright/test";
import { fakeBackend, green, login, seedLocalRaw, type V } from "./fake-backend";

// Keyboard acceptance for the editor and the dialogs that hand over from it: focus
// goes in on open, and comes back to what opened it on Escape or cancel.

const hangzhou = (extra: Partial<V> = {}): V => ({
  code: "330100", name: "杭州市", province: "330000", color: green, date: "2026-10-01", note: "西湖", ...extra,
});
const editor = (page: Page) => page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "保存足迹" }) });
const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const focused = (page: Page) =>
  page.evaluate(() => {
    const a = document.activeElement as HTMLElement | null;
    return a ? `${a.tagName}:${a.getAttribute("aria-label") ?? a.textContent?.trim().slice(0, 12)}` : "none";
  });

test("Enter on the add button opens the editor with focus inside, and Escape returns focus to it", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 640 });
  await page.goto("/");
  const add = page.getByRole("button", { name: "添加足迹" }).first();
  await add.focus();
  await page.keyboard.press("Enter");
  await expect(editor(page)).toBeVisible();
  await expect(editor(page).locator(":focus")).toHaveCount(1);
  expect(await noOverflow(page)).toBe(true);
  await page.keyboard.press("Escape");
  await expect(editor(page)).toHaveCount(0);
  await expect(add).toBeFocused();
  expect(await noOverflow(page)).toBe(true);
});

test("Tab reaches Save from the first field and Enter on it saves; focus then returns to the opener", async ({ page }) => {
  await page.goto("/");
  const add = page.getByRole("button", { name: "添加足迹" }).first();
  await add.focus();
  await page.keyboard.press("Enter");
  const dialog = editor(page);
  const selects = dialog.locator("select");
  await selects.nth(0).selectOption("330000");
  await selects.nth(1).selectOption("330100");
  const save = dialog.getByRole("button", { name: "保存足迹" });
  for (let i = 0; i < 20 && !(await save.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press("Tab");
  await expect(save).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(dialog).toHaveCount(0);
  await expect(add).toBeFocused();
});

test("when the city that opened the editor is cancelled, focus falls back to the add button", async ({ page }) => {
  await seedLocalRaw(page, JSON.stringify({ version: 1, visits: [hangzhou()], provinces: {} }));
  await page.goto("/");
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  const card = page.locator(".visit-card").first();
  await card.focus();
  await page.keyboard.press("Enter");
  const dialog = editor(page);
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "取消城市打卡" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("button", { name: "添加足迹" }).first()).toBeFocused();
});

test("the cancel question and the edit conflict question hand focus back to the editor", async ({ page }) => {
  const backend = fakeBackend(page, {
    visits: [hangzhou()],
    trips: [{ id: "t-spring", code: "330100", date: "2026-03-01", note: "春" }],
  });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await page.locator(".visit-card").first().click();
  const dialog = editor(page);
  await expect(dialog).toBeVisible();

  // Keeping the city: focus is back on the button that asked.
  const cancel = dialog.getByRole("button", { name: "取消城市打卡" });
  await cancel.focus();
  await page.keyboard.press("Enter");
  const confirm = page.getByRole("dialog", { name: /取消 杭州市 的打卡/ });
  await expect(confirm).toBeVisible();
  // Focus has moved into the question before the key is pressed.
  await expect(confirm.locator(":focus")).toHaveCount(1);
  await page.keyboard.press("Escape");
  await expect(confirm).toHaveCount(0);
  await expect(cancel).toBeFocused();
  await expect(dialog).toBeVisible();

  // An edit conflict: going back keeps the draft, and focus is on Save.
  await dialog.locator(".trip-card").filter({ has: page.locator('input[value="2026-10-01"]') }).locator("textarea").fill("本机改的");
  backend.otherDeviceTrip({ id: "legacy-330100", code: "330100", date: "2026-10-01", note: "别处改的" });
  backend.otherDeviceTrip({ id: "t-remote", code: "330100", date: "2026-07-01", note: "" });
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await expect(page.locator(".stats > div").filter({ hasText: "旅行次数" }).locator("strong")).toHaveText("3");
  const save = dialog.getByRole("button", { name: "保存足迹" });
  await save.focus();
  await page.keyboard.press("Enter");
  const conflict = page.getByRole("dialog", { name: "编辑期间这条记录有了新的变化" });
  await expect(conflict).toBeVisible();
  await expect(conflict.locator(":focus")).toHaveCount(1);
  expect(await noOverflow(page)).toBe(true);
  await page.keyboard.press("Escape");
  await expect(conflict).toHaveCount(0);
  await expect(dialog).toBeVisible();
  expect(await focused(page)).toContain("保存足迹");
  await expect(dialog.locator(".trip-card").filter({ has: page.locator('input[value="2026-10-01"]') }).locator("textarea")).toHaveValue("本机改的");
});

// Escape lands in the same task in which focus enters a question that opened on top of
// the editor, before the question has registered as the top layer. It must still only
// close the question: the editor stays, with the draft in it.
async function escapeAsFocusEnters(page: Page, titlePattern: string) {
  await page.evaluate((pattern) => {
    const re = new RegExp(pattern);
    const onFocus = (event: FocusEvent) => {
      const dialog = (event.target as HTMLElement | null)?.closest?.('[role="dialog"]');
      const title = dialog?.querySelector('[data-slot="dialog-title"]')?.textContent ?? "";
      if (!re.test(title)) return;
      document.removeEventListener("focusin", onFocus, true);
      (event.target as HTMLElement).dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    };
    document.addEventListener("focusin", onFocus, true);
  }, titlePattern);
}

test("Escape pressed the moment the cancel question takes focus only closes the question and keeps the draft", async ({ page }) => {
  const backend = fakeBackend(page, {
    visits: [hangzhou()],
    trips: [{ id: "t-spring", code: "330100", date: "2026-03-01", note: "春" }],
  });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await page.locator(".visit-card").first().click();
  const dialog = editor(page);
  await expect(dialog).toBeVisible();
  const note = dialog.locator(".trip-card").filter({ has: page.locator('input[value="2026-10-01"]') }).locator("textarea");
  await note.fill("还没保存的草稿");
  const cancel = dialog.getByRole("button", { name: "取消城市打卡" });
  await cancel.focus();
  await escapeAsFocusEnters(page, "取消 杭州市 的打卡");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: /取消 杭州市 的打卡/ })).toHaveCount(0);
  await expect(dialog).toBeVisible();
  await expect(cancel).toBeFocused();
  await expect(note).toHaveValue("还没保存的草稿");
  // A second Escape is the one that closes the editor.
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
});

test("Escape pressed the moment the edit conflict question takes focus only closes the question and keeps the draft", async ({ page }) => {
  const backend = fakeBackend(page, {
    visits: [hangzhou()],
    trips: [{ id: "t-spring", code: "330100", date: "2026-03-01", note: "春" }],
  });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await page.locator(".visit-card").first().click();
  const dialog = editor(page);
  await expect(dialog).toBeVisible();
  const note = dialog.locator(".trip-card").filter({ has: page.locator('input[value="2026-10-01"]') }).locator("textarea");
  await note.fill("本机改的");
  backend.otherDeviceTrip({ id: "legacy-330100", code: "330100", date: "2026-10-01", note: "别处改的" });
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await expect(page.locator(".stats > div").filter({ hasText: "旅行次数" }).locator("strong")).toHaveText("2");
  const save = dialog.getByRole("button", { name: "保存足迹" });
  await save.focus();
  await escapeAsFocusEnters(page, "编辑期间这条记录有了新的变化");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: "编辑期间这条记录有了新的变化" })).toHaveCount(0);
  await expect(dialog).toBeVisible();
  expect(await focused(page)).toContain("保存足迹");
  await expect(note).toHaveValue("本机改的");
});

test("the close button and a click outside the cancel question only close the question and keep the draft", async ({ page }) => {
  const backend = fakeBackend(page, {
    visits: [hangzhou()],
    trips: [{ id: "t-spring", code: "330100", date: "2026-03-01", note: "春" }],
  });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await page.locator(".visit-card").first().click();
  const dialog = editor(page);
  await expect(dialog).toBeVisible();
  const note = dialog.locator(".trip-card").filter({ has: page.locator('input[value="2026-10-01"]') }).locator("textarea");
  await note.fill("还没保存的草稿");
  const cancel = dialog.getByRole("button", { name: "取消城市打卡" });
  const confirm = page.getByRole("dialog", { name: /取消 杭州市 的打卡/ });

  await cancel.click();
  await expect(confirm).toBeVisible();
  await confirm.getByRole("button", { name: "关闭" }).click();
  await expect(confirm).toHaveCount(0);
  await expect(dialog).toBeVisible();
  await expect(cancel).toBeFocused();
  await expect(note).toHaveValue("还没保存的草稿");

  await cancel.click();
  await expect(confirm).toBeVisible();
  await page.mouse.click(4, 4);
  await expect(confirm).toHaveCount(0);
  await expect(dialog).toBeVisible();
  await expect(note).toHaveValue("还没保存的草稿");
});
