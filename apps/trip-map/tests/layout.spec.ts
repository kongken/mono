import { test, expect, type Page } from "@playwright/test";
import { fakeBackend, green, login, type V } from "./fake-backend";

// No horizontal scrolling at the narrowest supported width, with the statistics row
// (which now has a fourth item for trips), with the editors open, and at 200% zoom
// (which leaves half the CSS pixels, so 640 and 320 stand for 1280 and 640 at 200%).

const WIDTHS = [320, 360, 390, 640, 700, 768, 900, 1024, 1280];
const hangzhou: V = { code: "330100", name: "杭州市", province: "330000", color: green, date: "2026-10-01", note: "西湖" };

const overflow = (page: Page) =>
  page.evaluate(() => {
    const limit = window.innerWidth;
    const wide = [...document.querySelectorAll<HTMLElement>("body *")]
      .filter((el) => {
        const r = el.getBoundingClientRect();
        return r.width > 0 && r.right > limit + 0.5 && getComputedStyle(el).position !== "fixed";
      })
      .slice(0, 5)
      .map((el) => `${el.tagName}.${el.className}`);
    return { scroll: document.documentElement.scrollWidth, limit, wide };
  });

test("the statistics row and the page never scroll sideways at any width", async ({ page }) => {
  await page.goto("/");
  for (const width of WIDTHS) {
    await page.setViewportSize({ width, height: 700 });
    await expect(page.locator(".stats")).toBeVisible();
    const o = await overflow(page);
    expect(o, `width ${width}`).toEqual({ scroll: expect.any(Number), limit: width, wide: [] });
    expect(o.scroll, `width ${width}`).toBeLessThanOrEqual(width);
    // Every statistic is fully inside the viewport and readable.
    for (const item of await page.locator(".stats > div").all()) {
      const box = (await item.boundingBox())!;
      expect(box.x, `width ${width}`).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width, `width ${width}`).toBeLessThanOrEqual(width);
    }
  }
});

test("the add dialog opens, scrolls to Save and closes without sideways scrolling, at narrow widths and 200% zoom", async ({ page }) => {
  for (const [width, height] of [[320, 640], [390, 844], [640, 480], [1280, 960]] as const) {
    await page.setViewportSize({ width, height });
    await page.goto("/");
    await page.getByRole("button", { name: "添加足迹" }).first().click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    const selects = dialog.locator("select");
    await selects.nth(0).selectOption("330000");
    await selects.nth(1).selectOption("330100");
    await dialog.getByRole("button", { name: "添加另一次旅行" }).click();
    await dialog.getByRole("button", { name: "添加另一次旅行" }).click();
    const o = await overflow(page);
    expect(o.wide.filter((w) => !w.includes("overlay")), `width ${width}`).toEqual([]);
    expect(o.scroll, `width ${width}`).toBeLessThanOrEqual(width);
    // The Save button can be reached by scrolling inside the dialog and stays inside the viewport.
    const save = dialog.getByRole("button", { name: "保存足迹" });
    await save.scrollIntoViewIfNeeded();
    await expect(save).toBeInViewport({ ratio: 1 });
    const box = (await save.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(width);
    expect(box.y + box.height).toBeLessThanOrEqual(height);
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    expect((await overflow(page)).scroll, `width ${width} after close`).toBeLessThanOrEqual(width);
  }
});

test("a signed-in editor with several trips and photo buttons fits a 320px screen", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 640 });
  const backend = fakeBackend(page, {
    visits: [hangzhou],
    trips: [
      { id: "t-a", code: "330100", date: "2026-03-01", note: "第二次，备注很长很长很长很长很长很长很长很长很长很长很长" },
      { id: "t-b", code: "330100", date: "", note: "" },
    ],
  });
  await backend.install();
  await page.route("**/api/bff.tripmap.v1.MediaService/**", (route) => route.fulfill({ status: 200, json: { media: [] } }));
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await page.locator(".visit-card").first().click();
  const dialog = page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "保存足迹" }) });
  await expect(dialog).toBeVisible();
  const o = await overflow(page);
  expect(o.scroll).toBeLessThanOrEqual(320);
  await dialog.getByRole("button", { name: "保存足迹" }).scrollIntoViewIfNeeded();
  await expect(dialog.getByRole("button", { name: "保存足迹" })).toBeInViewport({ ratio: 1 });
  await dialog.getByRole("button", { name: "照片与附件", exact: true }).click();
  const media = page.getByRole("dialog", { name: "照片与附件" });
  await expect(media).toBeVisible();
  expect((await overflow(page)).scroll).toBeLessThanOrEqual(320);
});
