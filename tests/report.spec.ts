import { expect, test } from "@playwright/test";
import { fakeBackend, green, gold, login } from "./fake-backend";

test.describe("F8 statistics, annual report, achievements and record traceability", () => {
  test("cloud report aggregates by timezone, switches years, unlocks achievements and traces back to records", async ({
    page,
  }) => {
    const api = fakeBackend(
      page,
      {
        visits: [
          { code: "110101", name: "东城区", province: "110000", color: green, date: "2025-05-01", note: "故宫春色与景山落日全记录" },
          { code: "110102", name: "西城区", province: "110000", color: green, date: "2026-04-10", note: "什刹海散步" },
          { code: "330100", name: "杭州市", province: "330000", color: green, date: "2025-03-15", note: "西湖苏堤春晓漫步一整天" },
          { code: "640100", name: "银川市", province: "640000", color: gold, date: "2026-05-01", note: "贺兰山岩画与镇北堡西部影城" },
          { code: "640200", name: "石嘴山市", province: "640000", color: gold, date: "2026-05-02", note: "沙湖芦苇荡" },
          { code: "640300", name: "吴忠市", province: "640000", color: gold, date: "2026-05-03", note: "黄河大峡谷" },
          { code: "640400", name: "固原市", province: "640000", color: gold, date: "2026-05-04", note: "六盘山红军长征景区" },
          { code: "640500", name: "中卫市", province: "640000", color: gold, date: "2026-05-06", note: "沙坡头黄河宿集观星露营之旅" },
        ],
        marks: { "530000": green },
        trips: [
          { id: "t-hz-2", code: "330100", date: "2026-04-02", note: "龙井问茶与九溪烟树徒步日记" },
          { id: "t-hz-3", code: "330100", date: "2026-10-05", note: "满觉陇赏桂花与宝石山看夕阳夜景" },
        ],
      },
    );
    await api.install();
    await login(page);

    await page.getByRole("button", { name: /足迹统计|生成图片/ }).click();
    const dialog = page.getByRole("dialog", { name: "我的足迹统计" });
    await expect(dialog).toBeVisible();

    // Overview metrics: 4 explored provinces (110000, 330000, 530000, 640000), 7 nationwide cities (Beijing collapsed), 10 trips.
    await expect(dialog.locator(".report-summary")).toContainText("4 / 34");
    await expect(dialog.locator(".report-summary")).toContainText("7 / 391");
    await expect(dialog.locator(".report-summary")).toContainText("10");
    await expect(dialog.getByLabel("宁夏回族自治区已打卡区域")).toContainText("银川市");
    await expect(dialog.getByLabel("宁夏回族自治区已打卡区域")).toContainText("中卫市");

    // Annual report tab
    await dialog.getByRole("tab", { name: /年度报告/ }).click();
    await expect(dialog.getByLabel("年度报告")).toContainText("云端已汇总");
    await expect(dialog.locator(".report-annual-metrics")).toContainText("8 次");
    await expect(dialog.locator(".report-annual-metrics")).toContainText("7 座");
    await expect(dialog.locator(".report-annual-metrics")).toContainText("5 座");
    await expect(dialog.locator(".report-annual-metrics")).toContainText("首次打卡 6 个区域");
    await expect(dialog.getByLabel("年度报告")).toContainText("满觉陇赏桂花与宝石山看夕阳夜景");

    // Switch to 2025 annual report
    await dialog.getByRole("button", { name: /2025 年/ }).click();
    await expect(dialog.locator(".report-annual-metrics")).toContainText("2 次");
    await expect(dialog.locator(".report-annual-metrics")).toContainText("2 座");

    // Switch timezone and verify timezone selector updates
    await dialog.getByLabel("统计时区").selectOption("America/New_York");
    await expect(dialog.getByLabel("统计时区")).toHaveValue("America/New_York");

    // Achievements tab
    await dialog.getByRole("tab", { name: /旅行成就/ }).click();
    await expect(dialog.getByLabel("旅行成就")).toContainText("已解锁 4 / 8 项成就");
    await expect(dialog.getByLabel("全省制霸关联记录")).toContainText("宁夏回族自治区 (2026-05-06)");
    await expect(dialog.getByLabel("故地重游关联记录")).toContainText("杭州市 · 2025-03-15");

    // Clicking a traceable record opens the city footprint editor for that exact record
    await dialog.getByLabel("故地重游关联记录").getByRole("button", { name: /杭州市 · 2025-03-15/ }).click();
    await expect(dialog).toBeHidden();
    const editor = page.getByRole("dialog", { name: "编辑城市足迹" });
    await expect(editor).toBeVisible();
    await expect(editor).toContainText("西湖苏堤春晓漫步一整天");
    await expect(editor).toContainText("满觉陇赏桂花与宝石山看夕阳夜景");
  });

  test("mobile layout renders overview, annual report and achievements without horizontal overflow", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 375, height: 667 });
    const api = fakeBackend(page, {
      visits: [
        { code: "330100", name: "杭州市", province: "330000", color: green, date: "2026-04-02", note: "龙井问茶与九溪烟树徒步日记" },
      ],
    });
    await api.install();
    await page.goto("/");

    // Mark a city in local mode or open statistics directly
    await page.getByRole("button", { name: /足迹统计|生成图片/ }).click();
    const dialog = page.getByRole("dialog", { name: "我的足迹统计" });
    await expect(dialog).toBeVisible();

    for (const tabName of [/总览与分享图/, /年度报告/, /旅行成就/]) {
      await dialog.getByRole("tab", { name: tabName }).click();
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      );
      expect(overflow).toBe(false);
    }
  });
});
