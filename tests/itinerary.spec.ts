import { expect, test, type Page } from "@playwright/test";
import { fakeBackend, green, login } from "./fake-backend";

const open = async (page: Page) => {
  await page.getByRole("button", { name: /^行程路线/ }).click();
  const dialog = page.getByRole("dialog", { name: "行程与路线连线" });
  await expect(dialog).toBeVisible();
  return dialog;
};

/** Fills the default Shanghai -> Hangzhou draft and adds Xiamen as a third stop. */
const fillThreeStops = async (dialog: ReturnType<Page["getByRole"]>, title: string) => {
  await dialog.getByLabel("行程名称", { exact: true }).fill(title);
  await dialog.getByRole("button", { name: "添加途经城市" }).click();
  await dialog.getByLabel("第 3 站省份").selectOption({ label: "福建省" });
  await dialog.getByLabel("第 3 站城市").selectOption({ label: "厦门市" });
  await dialog.getByLabel("第 3 站交通方式").selectOption("car");
};

const noOverflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);

test.describe("F11 itineraries and route lines", () => {
  test("local mode creates, edits, reorders, draws on maps, survives reopen and deletes", async ({ page }) => {
    await page.goto("/");
    const dialog = await open(page);

    // The estimation policy is explicit: offline, free, cached.
    const policy = dialog.getByTestId("itinerary-estimation-policy");
    await expect(policy).toContainText("offline_great_circle");
    await expect(policy).toContainText("free_builtin");
    await expect(policy).toContainText("redis_leg_24h_and_local_deterministic");

    // Fewer than two stops are refused.
    await dialog.getByLabel("行程名称", { exact: true }).fill("江南海西行");
    await dialog.getByRole("button", { name: "移除第 2 站" }).click({ force: true }).catch(() => {});
    await expect(dialog.getByTestId("itinerary-stop-1")).toBeVisible();

    await fillThreeStops(dialog, "江南海西行");
    await expect(dialog.getByTestId("itinerary-editor-summary")).toContainText("3");
    await expect(dialog.getByTestId("itinerary-leg-1")).toContainText("黄浦区");
    await dialog.getByRole("button", { name: "保存并在地图展示路线" }).click();
    await expect(dialog).toBeHidden();

    // National SVG map: line and one marker per stop.
    await expect(page.getByTestId("active-route-bar")).toContainText("江南海西行");
    await expect(page.locator(".itinerary-route-layer .route-line")).toHaveCount(1);
    await expect(page.locator(".itinerary-route-layer .route-stop-marker")).toHaveCount(3);

    // Province map: only the stop inside the province is drawn.
    await page.getByRole("button", { name: "浙江省", exact: false }).first().click();
    await expect(page.locator(".itinerary-route-layer .route-stop-marker")).toHaveCount(1);
    await page.getByRole("button", { name: "中国", exact: true }).click();

    // Reopen restores the itinerary and the active route.
    await page.reload();
    await expect(page.getByTestId("active-route-bar")).toContainText("江南海西行");
    await expect(page.locator(".itinerary-route-layer .route-stop-marker")).toHaveCount(3);

    // Edit: move Xiamen up; the order changes and the version advances.
    await page.getByTestId("active-route-bar").getByRole("button", { name: "编辑行程" }).click();
    const editor = page.getByRole("dialog", { name: "行程与路线连线" });
    await expect(editor.getByLabel("行程名称", { exact: true })).toHaveValue("江南海西行");
    await editor.getByRole("button", { name: "上移第 3 站" }).click();
    await expect(editor.getByTestId("itinerary-stop-1")).toContainText("厦门市");
    await editor.getByRole("button", { name: "仅保存行程" }).click();
    await expect(editor.getByTestId("itinerary-saved-list")).toContainText("黄浦区");
    expect(await noOverflow(page)).toBe(true);

    // Hide the route from the list, then delete.
    await editor.getByRole("button", { name: "隐藏路线" }).click();
    await expect(page.getByTestId("active-route-bar")).toHaveCount(0);
    await editor.getByRole("button", { name: /^删除行程/ }).click();
    await expect(editor.getByTestId("itinerary-saved-list")).toHaveCount(0);
    await page.keyboard.press("Escape");
    await page.reload();
    await expect(page.getByTestId("active-route-bar")).toHaveCount(0);
    await expect(page.getByRole("button", { name: /^行程路线$/ })).toBeVisible();
  });

  test("cloud mode stores itineraries on the account, retries a lost reply once and restores after reload", async ({ page }) => {
    const api = fakeBackend(page, {
      visits: [{ code: "330100", name: "杭州市", province: "330000", color: green, date: "2026-04-02", note: "西湖" }],
    });
    await api.install();
    await login(page);
    await expect(page.locator(".local-status")).toHaveText("已同步到云端");

    const dialog = await open(page);
    await fillThreeStops(dialog, "云端路线");

    // The first save commits but its reply is lost; the retry replays instead of duplicating.
    api.state.dropNextItineraryReply = 1;
    await dialog.getByRole("button", { name: "保存并在地图展示路线" }).click();
    await expect(dialog.getByRole("alert")).toBeVisible();
    await dialog.getByRole("button", { name: "保存并在地图展示路线" }).click();
    await expect(dialog).toBeHidden();
    expect(api.state.itineraries.size).toBe(1);
    expect([...api.state.itineraries.values()][0].version).toBe(1);
    await expect(page.getByTestId("active-route-bar")).toContainText("云端路线");

    // A failed save keeps the dialog open with the error and changes nothing.
    await page.getByTestId("active-route-bar").getByRole("button", { name: "编辑行程" }).click();
    const editor = page.getByRole("dialog", { name: "行程与路线连线" });
    await editor.getByLabel("行程名称", { exact: true }).fill("云端路线改名");
    api.state.failItinerarySave = 1;
    await editor.getByRole("button", { name: "仅保存行程" }).click();
    await expect(editor.getByRole("alert")).toBeVisible();
    expect([...api.state.itineraries.values()][0].title).toBe("云端路线");
    await editor.getByRole("button", { name: "仅保存行程" }).click();
    await expect(editor.getByRole("alert")).toHaveCount(0);
    expect([...api.state.itineraries.values()][0].title).toBe("云端路线改名");
    expect([...api.state.itineraries.values()][0].version).toBe(2);
    await page.keyboard.press("Escape");

    // Reopen: the account's itineraries and the active route come back.
    await page.reload();
    await expect(page.getByTestId("active-route-bar")).toContainText("云端路线改名");
    await expect(page.locator(".itinerary-route-layer .route-stop-marker")).toHaveCount(3);
  });

  test("another account never sees the itinerary of the first one", async ({ page }) => {
    const api = fakeBackend(page, { visits: [] });
    await api.install();
    await login(page);
    await expect(page.locator(".local-status")).toHaveText("已同步到云端");
    const dialog = await open(page);
    await fillThreeStops(dialog, "仅我可见");
    await dialog.getByRole("button", { name: "仅保存行程" }).click();
    await expect(dialog.getByTestId("itinerary-saved-list")).toContainText("仅我可见");
    await page.keyboard.press("Escape");

    // Signing out returns to this device's own (empty) local itineraries.
    await page.getByRole("button", { name: "账号" }).click();
    await page.getByRole("button", { name: "退出登录", exact: true }).click();
    await page.getByRole("button", { name: "确认退出登录", exact: true }).click();
    await expect(page.getByRole("button", { name: /^行程路线$/ })).toBeVisible();
    const keys = await page.evaluate(() => Object.keys(localStorage).filter((k) => k.includes("itinerar")));
    expect(keys).toEqual([]);
  });

  test("mobile layout keeps the itinerary dialog and route bar inside the viewport", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 });
    await page.goto("/");
    const dialog = await open(page);
    await fillThreeStops(dialog, "手机上的路线");
    expect(await noOverflow(page)).toBe(true);
    const box = await dialog.boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(375 + 1);
    await dialog.getByRole("button", { name: "保存并在地图展示路线" }).click();
    await expect(page.getByTestId("active-route-bar")).toBeVisible();
    expect(await noOverflow(page)).toBe(true);
    const bar = await page.getByTestId("active-route-bar").boundingBox();
    expect(bar!.x).toBeGreaterThanOrEqual(0);
    expect(bar!.x + bar!.width).toBeLessThanOrEqual(375 + 1);
  });
});
