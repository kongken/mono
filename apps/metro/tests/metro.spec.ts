import { expect, test } from "@playwright/test";
import { loginUrl, setup } from "./support";

// Acceptance scenarios 17-21 of docs/metro/spec.md, desktop and phone sizes.

test("17 anonymous visitors browse cities and topics, and cannot mark rides", async ({ page }) => {
  const { fake, urls } = await setup(page);

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
  // Anonymous visitors cannot mark rides: nothing is written and they are sent to OzID to sign in,
  // with the page they came from (a registered one) as the page to return to.
  await expect(page).toHaveURL(loginUrl("/topics/six-cars"));
  expect(urls()).toEqual([loginUrl("/topics/six-cars")]);
  expect(fake.state.writes).toEqual([]);
});

test("18 a signed-in rider marks and unmarks a line, and /me follows", async ({ page }) => {
  const { fake } = await setup(page, { signedIn: true });

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

test("19 the catalogue administration is not in this application: an administrator gets no entry and no old address works", async ({ page }) => {
  const { fake, accounts, account } = await setup(page);
  // An administrator is an account with the role; the product gives it no extra entry.
  account.roles = ["metro-admin"];
  accounts.signIn(account);

  await page.goto("/");
  await expect(page.getByRole("link", { name: "管理", exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "我的足迹" })).toBeVisible();
  for (const path of ["/admin", "/admin/cities", "/admin/lines", "/admin/import", "/admin/export", "/admin/audit", "/admin/tokens"]) {
    await page.goto(path);
    await expect(page.getByRole("heading", { name: "页面不存在" })).toBeVisible();
    await expect(page.getByRole("button", { name: /新建|确认导入|下载目录|创建/ })).toHaveCount(0);
  }
  // Nothing of the management API was asked for, and no public page links to one.
  expect(fake.state.calls.filter((c) => /AdminService|AccessTokenService/.test(c))).toEqual([]);
  await page.goto("/");
  expect(await page.locator("a[href*='/admin']").count()).toBe(0);
});
