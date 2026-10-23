import { expect, test } from "@playwright/test";
import { fakeBackend } from "./fake-backend";

// Acceptance scenarios 17-21 of docs/metro/spec.md, desktop and phone sizes.

test("17 anonymous visitors browse cities and topics, and marking sends them to sign in", async ({ page }) => {
  const fake = fakeBackend(page);
  await fake.install();

  await page.goto("/");
  await expect(page.getByRole("heading", { name: "城市", exact: true })).toBeVisible();
  await page.getByRole("link", { name: /上海/ }).click();
  await expect(page).toHaveURL(/\/cities\/shanghai$/);
  await expect(page.getByTestId("line-sh-1")).toContainText("1号线");
  await expect(page.getByTestId("line-sh-2")).toContainText("2号线");
  // Planned lines are not listed among the operating ones.
  await expect(page.getByTestId("line-sh-17")).toHaveCount(0);
  // The unnumbered/numbered order and the totals come from the server.
  await expect(page.getByText("100.9 km")).toBeVisible();

  // The catalogue is revalidated with its version, not downloaded again.
  await page.getByRole("link", { name: "城市", exact: true }).first().click();
  await expect(page.getByRole("link", { name: /上海/ })).toBeVisible();

  await page.getByRole("link", { name: "6 节编组" }).click();
  await expect(page).toHaveURL(/\/topics\/six-cars$/);
  await expect(page.getByRole("heading", { name: "6 节编组" })).toBeVisible();
  await expect(page.getByRole("img", { name: /上海 2 条/ })).toBeVisible();

  await page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" }).click();
  await expect.poll(() => fake.state.loginRequests.length).toBe(1);
  expect(new URL(fake.state.loginRequests[0]).searchParams.get("app")).toBe("metro");
  // After signing in the visitor lands on the page they started from.
  await expect(page).toHaveURL(/\/topics\/six-cars$/);
  await expect(page.getByRole("button", { name: "退出" })).toBeVisible();
});

test("18 a signed-in rider marks and unmarks a line, and /me follows", async ({ page }) => {
  const fake = fakeBackend(page, { signedIn: "user" });
  await fake.install();

  await page.goto("/cities/shanghai");
  const line1 = page.getByTestId("line-sh-1");
  await line1.getByRole("button", { name: "标记坐过" }).click();
  await expect(line1.getByRole("button", { name: "坐过" })).toHaveAttribute("aria-pressed", "true");

  await page.getByRole("link", { name: "我的足迹" }).click();
  await expect(page.getByRole("heading", { name: "乘坐记录" })).toBeVisible();
  await expect(page.getByRole("region", { name: "上海" })).toContainText("1号线");
  await page.getByLabel("1号线 乘坐年份").fill("2019");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("region", { name: "上海" })).toContainText("2019 年");

  await page.goto("/cities/shanghai");
  await page.getByTestId("line-sh-1").getByRole("button", { name: "坐过" }).click();
  await expect(page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" })).toBeVisible();
  await page.getByRole("link", { name: "我的足迹" }).click();
  await expect(page.getByText("还没有记录")).toBeVisible();
  expect(fake.state.writesWithoutCsrf).toEqual([]);
});

test("19 non-admins get no admin entry; admins edit records and see revision conflicts", async ({ page }) => {
  const fake = fakeBackend(page, { signedIn: "user" });
  await fake.install();

  await page.goto("/");
  await expect(page.getByRole("link", { name: "管理" })).toHaveCount(0);
  await page.goto("/admin");
  await expect(page.getByRole("heading", { name: "无权限" })).toBeVisible();

  fake.signInAs("admin");
  await page.goto("/");
  await page.getByRole("link", { name: "管理" }).click();
  await expect(page.getByRole("heading", { name: "管理" })).toBeVisible();

  // Create a city, then a line with a bad colour: the field error is shown.
  await page.getByRole("button", { name: "新建城市" }).click();
  await page.getByLabel("标识 slug").fill("beijing");
  await page.getByLabel("名称", { exact: true }).fill("北京");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("cell", { name: "beijing", exact: true })).toBeVisible();

  await page.getByRole("link", { name: "线路", exact: true }).click();
  await page.getByRole("button", { name: "新建线路" }).click();
  await page.getByLabel("代码", { exact: true }).fill("bj-1");
  await page.getByLabel("所属城市 slug").fill("beijing");
  await page.getByLabel("名称", { exact: true }).fill("1号线");
  await page.getByLabel("颜色").fill("E4002B");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("color");
  await page.getByLabel("颜色").fill("#E4002B");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("cell", { name: "bj-1", exact: true })).toBeVisible();

  // Another editor changes the city while this page holds the old revision.
  await page.getByRole("navigation", { name: "管理分区" }).getByRole("link", { name: "城市", exact: true }).click();
  await page.getByRole("button", { name: "编辑 shanghai" }).click();
  fake.touchCity("shanghai");
  await page.getByLabel("名称", { exact: true }).fill("上海市");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("已被他人修改");
  await page.getByRole("button", { name: "刷新并重新编辑" }).click();
  await expect(page.getByLabel("名称", { exact: true })).toHaveValue("上海（已改）");
  await page.getByLabel("名称", { exact: true }).fill("上海市");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("cell", { name: "上海市", exact: true })).toBeVisible();
  expect(fake.state.writesWithoutCsrf).toEqual([]);
});

test("20 import previews errors first and only applies a clean document", async ({ page }) => {
  const fake = fakeBackend(page, { signedIn: "admin" });
  await fake.install();
  await page.goto("/admin/import");

  const apply = page.getByRole("button", { name: "确认导入" });
  await expect(apply).toBeDisabled();

  const bad = JSON.stringify({ format: "metro-catalog/1", cities: [{ slug: "beijing", name: "北京" }], lines: [{ code: "bj-1", color: "E4002B" }] });
  await page.getByLabel("目录 JSON").fill(bad);
  await expect(page.getByText("lines[0].color")).toBeVisible();
  await expect(apply).toBeDisabled();

  const good = JSON.stringify({ format: "metro-catalog/1", cities: [{ slug: "beijing", name: "北京" }] });
  await page.getByLabel("目录 JSON").fill(good);
  await expect(page.getByText(/预览（尚未写入）/)).toContainText("城市 新增 1");
  await expect(apply).toBeEnabled();
  // Nothing was written by the preview.
  expect(fake.state.cities.map((c) => c.slug)).toEqual(["shanghai"]);

  await apply.click();
  await expect(page.getByText(/已导入/)).toBeVisible();
  expect(fake.state.cities.map((c) => c.slug)).toEqual(["shanghai", "beijing"]);

  await page.goto("/");
  await expect(page.getByRole("link", { name: /北京/ })).toBeVisible();
});

test("21 a management token's secret is shown once", async ({ page }) => {
  const fake = fakeBackend(page, { signedIn: "admin" });
  await fake.install();
  await page.goto("/admin/tokens");

  await page.getByLabel("名称").fill("我的 AI 助手");
  await page.getByLabel("权限").selectOption("metro:write");
  await page.getByRole("button", { name: "创建", exact: true }).click();
  const secret = page.getByTestId("token-secret");
  await expect(secret).toContainText("mcp_");
  const text = await secret.innerText();
  await expect(page.getByRole("cell", { name: "我的 AI 助手", exact: true })).toBeVisible();
  await expect(page.locator("code", { hasText: "/mcp/metro" })).toBeVisible();

  await page.reload();
  await expect(page.getByRole("cell", { name: "我的 AI 助手", exact: true })).toBeVisible();
  await expect(page.getByRole("cell", { name: "读写", exact: true })).toBeVisible();
  await expect(page.getByText("有效", { exact: true })).toBeVisible();
  await expect(page.getByTestId("token-secret")).toHaveCount(0);
  expect(await page.content()).not.toContain(text);

  await page.getByRole("button", { name: "吊销 我的 AI 助手" }).click();
  await expect(page.getByText("已吊销")).toBeVisible();
});
