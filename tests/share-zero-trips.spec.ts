import { test, expect } from "@playwright/test";

test("a shared city kept after its last trip is deleted shows zero trips", async ({ page }) => {
  const token = "Z".repeat(43);
  const content = {
    v: 1,
    published_on: "2026-10-14",
    fields: { cities: true, dates: false, notes: false, photos: false },
    stats: {
      explored_provinces: 1, total_provinces: 34,
      cities: 1, total_cities: 391, trips: 0,
      by_province: [{ code: "330000", cities: 1, total: 11 }],
    },
    provinces: ["330000"],
    cities: [{ code: "330100", name: "杭州市", province: "330000", color: "#4b937d", trips: [] }],
  };
  await page.route(`**/s/${token}`, async route => {
    const index = await page.request.get("/");
    await route.fulfill({ status: 200, contentType: "text/html", body: await index.text() });
  });
  await page.route(`**/api/share/${token}/data`, route => route.fulfill({ json: content }));
  await page.goto(`/s/${token}`);
  await expect(page.getByRole("heading", { name: "城市足迹" })).toBeVisible();
  await expect(page.locator(".share-city-head").filter({ hasText: "杭州市" })).toContainText("0 次旅行");
  await expect(page.locator(".share-stats > div").filter({ hasText: "旅行次数" }).locator("dd")).toHaveText("0");
});
