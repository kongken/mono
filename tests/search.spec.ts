import { expect, test } from "@playwright/test";
import { fakeBackend, green, login } from "./fake-backend";

test("destination search supports pinyin, initials, typo correction and canonical islands", async ({
  page,
}) => {
  await page.goto("/");
  const search = page.getByRole("textbox", {
    name: "搜索省份或城市",
    exact: true,
  });
  for (const query of ["HANGZHOU", "hangzohu", "hz"]) {
    await search.fill(query);
    await expect(
      page.locator(".province-list").getByRole("button", { name: /浙江/ }),
    ).toBeVisible();
  }
  await search.fill("jinmen");
  await expect(
    page.locator(".province-list").getByRole("button", { name: /福建/ }),
  ).toBeVisible();
  await expect(
    page.locator(".province-list").getByRole("button", { name: /台湾/ }),
  ).toHaveCount(0);
  await search.fill("zhejiang");
  await page
    .locator(".province-list")
    .getByRole("button", { name: /浙江/ })
    .click();
  const citySearch = page.getByRole("textbox", {
    name: "搜索城市",
    exact: true,
  });
  await citySearch.fill("hangzou");
  await expect(
    page
      .locator(".city-list")
      .getByRole("button", { name: "杭州市，标记去过" }),
  ).toBeVisible();
  await citySearch.fill("不存在的地方");
  await expect(page.locator(".city-list")).toContainText("没有找到匹配");
  await page.getByRole("button", { name: "清空搜索", exact: true }).click();
  await expect(page.locator(".city-list .city-row")).toHaveCount(11);
  if (test.info().project.name === "mobile") {
    await page.getByRole("button", { name: "选择城市", exact: true }).click();
    const picker = page.getByRole("dialog", { name: /选择城市/ });
    await picker
      .getByRole("textbox", { name: "查找城市或区县" })
      .fill("hangzhou");
    await expect(
      picker.getByRole("button", { name: "杭州市，标记去过" }),
    ).toBeVisible();
    await picker.getByRole("button", { name: "完成选择" }).click();
  }
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
  ).toBe(false);
});

test("local note search finds non-latest trips, long notes, literal markup and opens records", async ({
  page,
}) => {
  const saved = {
    version: 2,
    provinces: {},
    visits: [
      {
        code: "330100",
        name: "杭州市",
        province: "330000",
        color: green,
        trips: [
          {
            id: "t-old",
            date: "2025-01-01",
            note: "山路".repeat(100) + "西湖 徒步 <img src=x onerror=alert(1)>",
          },
          { id: "t-new", date: "2026-01-01", note: "龙井" },
        ],
      },
    ],
  };
  await page.addInitScript(
    (data) => localStorage.setItem("shanhai-travel-v2", JSON.stringify(data)),
    saved,
  );
  await page.goto("/");
  await page.getByRole("button", { name: "旅行记忆", exact: true }).click();
  const panel = page.getByRole("region", { name: "旅行记忆搜索" });
  await panel.getByRole("textbox", { name: "搜索旅行记忆" }).fill("西湖 徒步");
  await expect(panel).toContainText("2025-01-01");
  await expect(panel).toContainText("<img src=x onerror=alert(1)>");
  await expect(panel.locator("img")).toHaveCount(0);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
  ).toBe(false);
  await panel.getByRole("button", { name: /杭州市/ }).click();
  const editor = page.getByRole("dialog", { name: "编辑城市足迹" });
  await expect(editor).toBeVisible();
  await expect(editor.locator('[data-trip-note="t-old"]')).toHaveValue(
    /西湖 徒步/,
  );
  await expect(editor.locator('[data-trip-note="t-old"]')).toBeFocused();
});

test("cloud note search paginates, falls back on failure, retries, and clears on logout", async ({
  page,
}) => {
  const api = fakeBackend(page, {
    visits: [
      {
        code: "330100",
        name: "杭州市",
        province: "330000",
        color: green,
        date: "2026-01-01",
        note: "西湖 记忆-0",
      },
    ],
    trips: Array.from({ length: 24 }, (_, i) => ({
      id: `t-search-${i + 1}`,
      code: "330100",
      date: "2026-01-01",
      note: `西湖 记忆-${i + 1}`,
    })),
  });
  await api.install();
  await login(page);
  await page.getByRole("button", { name: "旅行记忆", exact: true }).click();
  const panel = page.getByRole("region", { name: "旅行记忆搜索" });
  const input = panel.getByRole("textbox", { name: "搜索旅行记忆" });
  await input.fill("西湖");
  await expect(panel).toContainText("云端检索结果");
  await expect(panel.locator(".note-result")).toHaveCount(20);
  await panel.getByRole("button", { name: "加载更多记忆" }).click();
  await expect(panel.locator(".note-result")).toHaveCount(25);
  api.state.failSearch = 1;
  await input.fill("记忆-24");
  await expect(panel).toContainText("云端检索暂不可用，已检索本机缓存");
  await expect(panel.locator(".note-result")).toHaveCount(1);
  await panel.getByRole("button", { name: "重试云端检索" }).click();
  await expect(panel).toContainText("云端检索结果");
  await panel.getByRole("button", { name: "清空记忆搜索" }).click();
  await expect(panel.locator(".note-result")).toHaveCount(0);
  await input.fill("西湖");
  await expect(panel).toContainText("云端检索结果");
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await page.getByRole("button", { name: "确认退出登录", exact: true }).click();
  await expect(input).toHaveValue("");
  await expect(panel.locator(".note-result")).toHaveCount(0);
});
