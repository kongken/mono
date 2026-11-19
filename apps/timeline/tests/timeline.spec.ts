import { expect, test } from "@playwright/test";
import { dayLabel, monthLabel } from "../src/lib/entries.ts";
import { entryJson, fakeBackend, type EntryJson } from "./fake-backend.ts";

const pad = (value: number) => String(value).padStart(2, "0");
const iso = (clock: Date) => `${clock.getFullYear()}-${pad(clock.getMonth() + 1)}-${pad(clock.getDate())}`;

/** Today and yesterday in the page's own calendar, so the 今天 badge is what the design says it is. */
function seed(): { today: string; yesterday: string; entries: EntryJson[] } {
  const clock = new Date();
  const today = iso(clock);
  const back = new Date(clock);
  back.setDate(back.getDate() - 1);
  const yesterday = iso(back);
  return {
    today,
    yesterday,
    entries: [
      entryJson({ id: "e1", title: "和朋友吃午饭", date: today, time: "12:30", category: "外出就餐", location: "巷口小馆", amountMinor: "8600", currency: "CNY", note: "聊了很久，饭后沿着街走了一段。" }),
      entryJson({ id: "e2", title: "周末理发", date: today, time: "10:00", category: "理发", location: "青木理发店", amountMinor: "6800", currency: "CNY" }),
      entryJson({ id: "e3", title: "给猫买主粮", date: yesterday, time: "19:20", category: "宠物开销", location: "宠物店", amountMinor: "23900", currency: "CNY", note: "顺便带了一包冻干。" }),
      entryJson({ id: "e4", title: "江边慢跑 5 公里", date: yesterday, time: "07:10", category: "运动", location: "滨江步道" }),
    ],
  };
}

test("the page is the design's page: rail, month and day rules, records with what they cost", async ({ page }) => {
  const { today, yesterday, entries } = seed();
  await fakeBackend(page, { entries });
  await page.goto("/");

  const nav = page.getByRole("navigation", { name: "主导航" });
  await expect(nav.getByRole("button", { name: "时间线" })).toHaveAttribute("aria-current", "page");
  await expect(nav.getByRole("button", { name: "回收站" })).toBeVisible();
  // 分类管理 and 设置与 MCP have no server side yet: shown as the design shows them, but not reachable.
  await expect(nav.getByText("分类管理")).toBeVisible();
  await expect(nav.getByText("设置与 MCP")).toBeVisible();
  await expect(page.getByText("已同步")).toBeVisible();

  const year = Number(today.slice(0, 4));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(`${year} 年 阳历记录本`);
  await expect(page.getByRole("heading", { level: 2 })).toHaveText(monthLabel(today.slice(0, 7)));
  const todayHeading = page.getByRole("heading", { level: 3, name: dayLabel(today) });
  await expect(todayHeading).toBeVisible();
  await expect(page.locator("section", { has: todayHeading }).getByText("今天")).toBeVisible();
  await expect(page.getByRole("heading", { level: 3, name: dayLabel(yesterday) })).toBeVisible();

  const lunch = page.locator("article", { hasText: "和朋友吃午饭" });
  await expect(lunch).toContainText("外出就餐");
  await expect(lunch).toContainText("巷口小馆");
  await expect(lunch).toContainText("12:30");
  await expect(lunch).toContainText("86.00");
  await expect(page.locator("article", { hasText: "给猫买主粮" })).toContainText("239.00");
  await expect(page.getByText("4 条记录")).toBeVisible();

  await page.screenshot({ path: test.info().outputPath("timeline.png"), fullPage: true, animations: "disabled" });
});

test("搜索 narrows the list, the chip says what is in effect, 清除筛选 brings everything back", async ({ page }) => {
  const { entries } = seed();
  const fake = await fakeBackend(page, { entries });
  await page.goto("/");
  await expect(page.locator("article")).toHaveCount(4);

  await page.getByRole("searchbox", { name: "检索手账" }).fill("午饭");
  await expect(page.locator("article")).toHaveCount(1);
  await expect(page.locator("article")).toContainText("和朋友吃午饭");
  await expect(page.getByText("搜索：午饭")).toBeVisible();
  // The search word reaches the server, it is not filtered in the page.
  expect(fake.state.calls).toContain("bff.timeline.v1.TimelineService/ListEntries");

  await page.getByRole("button", { name: "清除筛选" }).click();
  await expect(page.getByRole("searchbox", { name: "检索手账" })).toHaveValue("");
  await expect(page.locator("article")).toHaveCount(4);

  // The categories the filter offers are the ones the loaded records use: the server has no catalogue yet.
  await page.getByLabel("分类筛选").selectOption("理发");
  await expect(page.locator("article")).toHaveCount(1);
  await expect(page.locator("article")).toContainText("周末理发");
  await expect(page.getByText("分类：理发")).toBeVisible();

  await page.getByRole("searchbox", { name: "检索手账" }).fill("主粮");
  await expect(page.locator("article")).toHaveCount(0);
  await expect(page.getByText("时间线还是空的")).toBeVisible();
  await expect(page.getByText("分类：理发")).toBeVisible();

  await page.getByRole("button", { name: "清除筛选" }).click();
  await expect(page.getByLabel("分类筛选")).toHaveValue("");
  await expect(page.locator("article")).toHaveCount(4);
});

test("记一笔 refuses an empty title, then saves the design's fields in one write", async ({ page }) => {
  const { today } = seed();
  const fake = await fakeBackend(page, { entries: [] });
  await page.goto("/");
  await expect(page.getByText("时间线还是空的")).toBeVisible();

  await page.getByRole("button", { name: "写手账" }).click();
  const drawer = page.getByRole("dialog", { name: "记一笔" });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByText("点击或拖拽文件添加附件")).toBeVisible();

  await drawer.getByRole("button", { name: "保存记录" }).click();
  await expect(drawer.getByLabel(/今天发生了什么/)).toHaveAttribute("aria-invalid", "true");
  expect(fake.state.writes).toEqual([]);

  await drawer.getByLabel(/今天发生了什么/).fill("和朋友吃午饭");
  await drawer.getByLabel("日期").fill(today);
  await drawer.getByLabel("时间（可留空）").fill("12:30");
  await drawer.getByLabel(/分类/).fill("外出就餐");
  await drawer.getByLabel(/地点/).fill("巷口小馆");
  await drawer.getByLabel(/金额/).fill("86");
  await drawer.getByLabel(/备注/).fill("聊了很久。");
  await page.screenshot({ path: test.info().outputPath("composer.png"), fullPage: true, animations: "disabled" });
  await drawer.getByRole("button", { name: "保存记录" }).click();

  await expect(drawer).not.toBeVisible();
  expect(fake.state.writes).toHaveLength(1);
  const [write] = fake.state.writes;
  expect(write?.csrf).toMatch(/^csrf-/);
  expect(write?.body.entry).toMatchObject({ title: "和朋友吃午饭", date: today, time: "12:30", category: "外出就餐", location: "巷口小馆", amountMinor: "8600", currency: "CNY", note: "聊了很久。" });
  expect(write?.body.mutationId).toBeTruthy();

  await expect(page.locator("article", { hasText: "和朋友吃午饭" })).toContainText("86.00");
  await expect(page.getByText("1 条记录")).toBeVisible();
});

test("a record moved to 回收站 can be restored from there", async ({ page }) => {
  const { today } = seed();
  const fake = await fakeBackend(page, { entries: [entryJson({ id: "e1", title: "和朋友吃午饭", date: today, category: "外出就餐" })] });
  await page.goto("/");

  await page.getByRole("button", { name: "把「和朋友吃午饭」移到回收站" }).click();
  await expect(page.locator("article")).toHaveCount(0);
  expect(fake.state.entries[0]?.deletedAt).toBeDefined();

  await page.getByRole("button", { name: "回收站" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("回收站");
  await page.getByRole("button", { name: "恢复", exact: true }).click();
  await expect(page.locator("article")).toHaveCount(0);

  await page.getByRole("button", { name: "时间线" }).click();
  await expect(page.locator("article", { hasText: "和朋友吃午饭" })).toBeVisible();
  expect(fake.state.entries[0]?.deletedAt).toBeUndefined();
});

test("a failed read is an error with a retry, never an empty timeline", async ({ page }) => {
  const { entries } = seed();
  const fake = await fakeBackend(page, { entries });
  fake.state.listFailures = 1;
  await page.goto("/");
  await expect(page.getByText("时间线还是空的")).toHaveCount(0);
  await page.getByRole("button", { name: "重试" }).click();
  await expect(page.locator("article")).toHaveCount(4);
});
