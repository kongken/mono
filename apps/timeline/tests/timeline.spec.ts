import { expect, test } from "@playwright/test";

test("creates, edits, bookmarks, and deletes a timeline record", async ({ page }) => {
  let entry: any = null;
  await page.route("**/api/bff.timeline.v1.TimelineService/**", async route => {
    const method = route.request().url().split("/").pop();
    if (method === "ListEntries") return route.fulfill({ status: 200, json: { entries: entry ? [entry] : [], nextPageToken: "" } });
    if (method === "CreateEntry") { const body = await route.request().postDataJSON(); entry = { id: "entry-1", revision: "1", ...body.entry }; return route.fulfill({ status: 200, json: { entry } }); }
    if (method === "UpdateEntry") { const body = await route.request().postDataJSON(); entry = { ...entry, ...body.entry, revision: "2" }; return route.fulfill({ status: 200, json: { entry } }); }
    if (method === "DeleteEntry") { entry = null; return route.fulfill({ status: 200, json: {} }); }
    return route.fulfill({ status: 200, json: {} });
  });
  await page.goto("/");
  await page.getByRole("button", { name: /记一笔/ }).click();
  await page.getByRole("textbox", { name: "标题 *" }).fill("周末咖啡");
  await page.getByLabel("URL").fill("https://example.com/read");
  await page.getByLabel("书签标题").fill("阅读清单");
  await page.getByRole("button", { name: "保存" }).click();
  await expect(page.getByText("周末咖啡")).toBeVisible();
  await expect(page.getByRole("link", { name: "阅读清单" })).toHaveAttribute("href", "https://example.com/read");
  await page.getByRole("button", { name: "编辑" }).click();
  await page.getByRole("textbox", { name: "标题 *" }).fill("周末咖啡（更新）");
  await page.getByRole("button", { name: "保存" }).click();
  await expect(page.getByText("周末咖啡（更新）")).toBeVisible();
  await page.getByRole("button", { name: "删除" }).click();
  await expect(page.getByText("周末咖啡（更新）")).toHaveCount(0);
});

test("opens the trash and exposes restore controls", async ({ page }) => {
  await page.route("**/api/bff.timeline.v1.TimelineService/**", route => route.fulfill({ status: 200, json: { entries: [{ id: "deleted", revision: "1", title: "已删除", date: "2026-11-07", deletedAt: "2026-11-07T00:00:00Z" }], nextPageToken: "" } }));
  await page.goto("/");
  await page.getByRole("button", { name: "回收站" }).click();
  await expect(page.getByText("已删除")).toBeVisible();
  await expect(page.getByRole("button", { name: "恢复" })).toBeVisible();
  await expect(page.getByRole("button", { name: "永久删除" })).toBeVisible();
});
