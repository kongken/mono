import { expect, test } from "@playwright/test";
import { setup, loginUrl, saveRide, removeRide } from "./support";

test("ride editor preserves the saved year until explicitly saved or removed", async ({ page }) => {
  const { fake, account } = await setup(page, { signedIn: true });
  fake.state.ridesBy.set(account.id, new Map([["sh-1", 2019]]));
  await page.goto("/cities/shanghai");
  const edit = page.getByTestId("line-sh-1").getByRole("button", { name: "编辑足迹" });
  await edit.click();
  const dialog = page.getByRole("dialog", { name: "编辑乘坐记录" });
  await expect(dialog.getByLabel("乘坐年份（可选）")).toHaveValue("2019");
  await expect(dialog.getByLabel("乘坐年份（可选）")).toBeFocused();
  expect(fake.state.writes).toEqual([]);
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press("Tab");
    expect(await dialog.evaluate((el) => el.contains(document.activeElement))).toBe(true);
  }
  await page.screenshot({ path: test.info().outputPath("ride-editor.png"), animations: "disabled" });
  await page.setViewportSize({ width: 320, height: 640 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  // Wait for the responsive dialog's enter animation to settle after the viewport change.
  await expect.poll(async () => {
    const bounds = await dialog.boundingBox();
    return Boolean(bounds && bounds.x >= -0.5 && bounds.x + bounds.width <= 320.5 && bounds.y >= -0.5 && bounds.y + bounds.height <= 640.5);
  }).toBe(true);
  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
  await expect(edit).toBeFocused();
  expect(fake.state.ridesBy.get(account.id)?.get("sh-1")).toBe(2019);
});

test("ride writes require save; validation, clearing a year and removal keep distinct meanings", async ({ page }) => {
  const { fake, account } = await setup(page, { signedIn: true });
  await page.goto("/cities/shanghai");
  await page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" }).click();
  const dialog = page.getByRole("dialog");
  const year = dialog.getByLabel("乘坐年份（可选）");
  await year.fill("1899");
  await dialog.getByRole("button", { name: "保存记录", exact: true }).click();
  await expect(year).toHaveAttribute("aria-invalid", "true");
  await expect(year).toBeFocused();
  expect(fake.state.writes).toEqual([]);
  await year.fill("2019");
  await dialog.getByRole("button", { name: "保存记录", exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await expect(page.getByTestId("line-sh-1")).toContainText("2019 年");
  expect(fake.state.ridesBy.get(account.id)?.get("sh-1")).toBe(2019);
  await page.getByTestId("line-sh-1").getByRole("button", { name: "编辑足迹" }).click();
  await dialog.getByRole("button", { name: "清空年份" }).click();
  expect(fake.state.ridesBy.get(account.id)?.get("sh-1")).toBe(2019);
  await dialog.getByRole("button", { name: "保存记录", exact: true }).click();
  await expect(page.getByTestId("line-sh-1")).toContainText("年份未记录");
  expect(fake.state.ridesBy.get(account.id)?.has("sh-1")).toBe(true);
  expect(fake.state.ridesBy.get(account.id)?.get("sh-1")).toBe(0);
  await removeRide(page, "sh-1");
  await expect(page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" })).toBeVisible();
  expect(fake.state.ridesBy.get(account.id)?.has("sh-1")).toBe(false);
});

test("failed save preserves the draft and allows a deliberate retry", async ({ page }) => {
  const { fake, account } = await setup(page, { signedIn: true });
  await page.route("**/bff.metro.v1.RiderService/MarkLine", async (route) => {
    await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ code: "unavailable", message: "offline" }) });
  }, { times: 1 });
  await page.goto("/cities/shanghai");
  await saveRide(page, "sh-1", "2020");
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("alert")).toBeVisible();
  await expect(dialog.getByLabel("乘坐年份（可选）")).toHaveValue("2020");
  expect(fake.state.writes).toEqual([]);
  await dialog.getByRole("button", { name: "保存记录", exact: true }).click();
  await expect(dialog).not.toBeVisible();
  expect(fake.state.ridesBy.get(account.id)?.get("sh-1")).toBe(2020);
});

test("city formation and station search filters intersect and can be reset", async ({ page }) => {
  const { fake } = await setup(page, { signedIn: true });
  Object.assign(fake.state.lines[0], { vehicleType: "a", stations: 3, stationNames: ["富锦路", "人民广场", "莘庄"] });
  Object.assign(fake.state.lines[1], { vehicleType: "a", cars: 8, stations: 3, stationNames: ["徐泾东", "静安寺", "浦东国际机场"] });
  await page.goto("/cities/shanghai");
  const formations = page.getByRole("group", { name: "筛选编组" });
  await formations.getByRole("button", { name: "6A/6B" }).click();
  await expect(page.getByTestId("line-sh-1")).toBeVisible();
  await expect(page.getByTestId("line-sh-2")).toHaveCount(0);
  await page.getByRole("searchbox", { name: "搜索线路或站点" }).fill("静安寺");
  await expect(page.getByText("没有符合当前筛选条件的线路。")).toBeVisible();
  await page.getByRole("button", { name: "重置筛选条件" }).click();
  await page.getByRole("searchbox", { name: "搜索线路或站点" }).fill("静安寺");
  await expect(page.getByTestId("line-sh-1")).toHaveCount(0);
  await expect(page.getByTestId("line-sh-2")).toContainText("徐泾东 ⇄ 浦东国际机场");
  await formations.getByRole("button", { name: "8A", exact: true }).click();
  await expect(page.getByTestId("line-sh-2")).toBeVisible();
});

test("topic counts drive chart order, city sections and live completion", async ({ page }) => {
  const { fake, account } = await setup(page, { signedIn: true });
  fake.state.cities.push({ ...fake.state.cities[0], id: "c2", slug: "beijing", name: "北京", region: "north" });
  fake.state.lines.push({ ...fake.state.lines[0], id: "l4", code: "bj-1", citySlug: "beijing", lengthKm: 1000 });
  fake.state.ridesBy.set(account.id, new Map([["sh-1", 2019]]));
  await page.goto("/topics/six-cars");
  const chart = page.getByRole("region", { name: "按城市分布" });
  await expect(chart.getByRole("img").first()).toHaveAttribute("aria-label", /上海 2 条/);
  expect(await chart.getByRole("img").first().locator("div").getAttribute("style")).toContain("100%");
  const second = chart.getByRole("img").nth(1);
  expect(await second.locator("div").evaluate((el) => el.getAttribute("style"))).toContain("50%");
  await expect(page.getByRole("region", { name: "上海线路台账" }).getByTestId("line-sh-1")).toContainText("2019 年");
  await expect(page.getByRole("region", { name: "北京线路台账" }).getByTestId("line-bj-1")).toBeVisible();
  await expect(page.getByText("33.3% 已完成")).toBeVisible();
  await saveRide(page, "bj-1", "2021");
  await expect(page.getByText("66.7% 已完成")).toBeVisible();
  await expect(page.getByRole("link", { name: "北京城市详情" })).toHaveAttribute("href", "/cities/beijing");
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
  await page.screenshot({ path: test.info().outputPath("topic-ledger.png"), fullPage: true, animations: "disabled" });
});

test("failed ride read cannot present an empty archive as editable", async ({ page }) => {
  await setup(page, { signedIn: true });
  await page.route("**/bff.metro.v1.RiderService/ListMyLines", (route) => route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ code: "unavailable", message: "offline" }) }), { times: 1 });
  await page.goto("/cities/shanghai");
  await expect(page.getByRole("button", { name: "重新加载足迹" })).toBeVisible();
  await expect(page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" })).toBeDisabled();
  await page.getByRole("button", { name: "重新加载足迹" }).click();
  await expect(page.getByTestId("line-sh-1").getByRole("button", { name: "标记坐过" })).toBeEnabled();
});

test("home searches line names and intersects the region filter", async ({ page }) => {
  const { fake } = await setup(page);
  fake.state.cities[0].region = "east";
  fake.state.cities.push({ ...fake.state.cities[0], id: "c2", slug: "beijing", name: "北京", region: "north" });
  fake.state.lines.push({ ...fake.state.lines[0], id: "l4", code: "bj-1", citySlug: "beijing" });
  await page.goto("/");
  await page.getByRole("searchbox", { name: "搜索城市或线路" }).fill("1号线");
  await expect(page.getByRole("link", { name: /上海/ })).toBeVisible();
  await expect(page.getByRole("link", { name: /北京/ })).toBeVisible();
  await page.getByRole("group", { name: "筛选区域" }).getByRole("button", { name: "华东", exact: true }).click();
  await expect(page.getByRole("link", { name: /上海/ })).toBeVisible();
  await expect(page.getByRole("link", { name: /北京/ })).toHaveCount(0);
  await page.setViewportSize({ width: 320, height: 640 });
  await page.evaluate(() => window.scrollTo({ top: document.body.scrollHeight, behavior: "instant" }));
  const disclaimer = await page.getByText("线路数据由管理员维护，仅供参考。", { exact: true }).boundingBox();
  const navigation = await page.getByRole("navigation", { name: "底部导航" }).boundingBox();
  expect(disclaimer && navigation && disclaimer.y + disclaimer.height <= navigation.y).toBe(true);
});

test("sign-in from the topic index returns to the topic index", async ({ page }) => {
  await setup(page);
  await page.goto("/topics");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page).toHaveURL(loginUrl("/topics"));
});
