import { test, expect, type Page } from "@playwright/test";
import {
  dongcheng, fakeBackend, gold, green, login, seedLocal, tianjin, xicheng,
} from "./fake-backend";

const dialog = (page: Page) => page.getByRole("dialog", { name: "云端备份与历史版本" });
const noOverflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

// The signed-in account's cached footprints; it follows what the page shows.
const cachedVisits = (page: Page) =>
  page.evaluate(() => {
    const raw = localStorage.getItem("shanhai-cloud-v2:ci-migrate");
    return raw ? JSON.parse(raw).saved.visits.length : -1;
  });

async function openSnapshots(page: Page) {
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "历史版本" }).click();
  await expect(dialog(page)).toBeVisible();
}

test("manual snapshot is listed, and saving an unchanged account does not duplicate it", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [dongcheng(), xicheng()], marks: { "310000": green } });
  await backend.install();
  await login(page);
  await openSnapshots(page);
  await expect(dialog(page).getByText("还没有快照")).toBeVisible();
  expect(await noOverflow(page)).toBe(true);

  await dialog(page).getByRole("button", { name: "立即保存快照" }).click();
  await expect(dialog(page).getByText("已保存当前版本的快照")).toBeVisible();
  const rows = dialog(page).getByRole("list", { name: "快照列表" }).getByRole("listitem");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("手动保存");
  await expect(rows.first()).toContainText("2 个城市足迹、2 次旅行、1 个省份标记");

  await dialog(page).getByRole("button", { name: "立即保存快照" }).click();
  await expect(dialog(page).getByText("当前内容已有相同的快照")).toBeVisible();
  await expect(rows).toHaveCount(1);
  expect(backend.state.snapshots).toHaveLength(1);
});

test("restore previews the change, saves the current version first and can be undone", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [dongcheng(), xicheng(), tianjin()], marks: { "310000": green } });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  // The good version, then another device damages it.
  const good = backend.seedSnapshot("manual");
  backend.otherDeviceUpsert(dongcheng({ color: gold, note: "误改" }));
  backend.otherDeviceUpsert({ code: "110105", name: "朝阳区", province: "110000", color: gold, date: "", note: "" });
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await expect.poll(() => cachedVisits(page)).toBe(4);

  await openSnapshots(page);
  const row = dialog(page).getByRole("listitem").filter({ hasText: "手动保存" });
  await row.getByRole("button", { name: "预览并恢复" }).click();
  const preview = dialog(page).getByLabel("恢复预览");
  await expect(preview).toBeVisible();
  // One record is rewritten and one added afterwards is removed; the rest is unchanged.
  await expect(preview.locator("li").nth(0)).toContainText("0");
  await expect(preview.locator("li").nth(1)).toContainText("1");
  await expect(preview.locator("li").nth(2)).toContainText("1");
  // 2 unchanged cities, their 2 trips and the province mark.
  await expect(preview.locator("li").nth(3)).toContainText("5");
  expect(await noOverflow(page)).toBe(true);
  // Nothing has been changed by merely previewing.
  expect(backend.state.restoreCalls).toBe(0);

  await dialog(page).getByRole("button", { name: "确认恢复" }).click();
  // 东城区's color and its trip's note, and the removal of 朝阳区 with its trip.
  await expect(dialog(page).getByText(/已恢复，共调整 3 条记录/)).toBeVisible();
  expect(backend.noteOf("110101")).toBe("");
  expect(backend.state.visits.has("110105")).toBe(false);
  expect(backend.state.visits.size).toBe(3);
  // The state being replaced was saved first.
  const undo = backend.state.snapshots.find((s) => s.reason === "before_restore");
  expect(undo?.visits).toHaveLength(4);
  expect(good.revision).toBeLessThan(undo!.revision);
  // The page shows the restored data.
  await expect.poll(() => cachedVisits(page)).toBe(3);

  // The restore itself is undone through the saved snapshot, with its own confirmation.
  await dialog(page).getByRole("button", { name: "撤销本次恢复" }).click();
  await expect(dialog(page).getByLabel("恢复预览")).toBeVisible();
  await dialog(page).getByRole("button", { name: "确认恢复" }).click();
  await expect(dialog(page).getByText(/已恢复，共调整 4 条记录/)).toBeVisible();
  expect(backend.noteOf("110101")).toBe("误改");
  expect(backend.state.visits.has("110105")).toBe(true);
  expect(backend.state.visits.size).toBe(4);
});

test("a restore based on stale data is rejected and nothing changes", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [dongcheng()] });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  backend.seedSnapshot("manual");
  backend.otherDeviceUpsert(xicheng());
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await expect.poll(() => cachedVisits(page)).toBe(2);

  await openSnapshots(page);
  await dialog(page).getByRole("button", { name: "预览并恢复" }).click();
  await expect(dialog(page).getByLabel("恢复预览")).toBeVisible();
  // Another device writes between the preview and the confirmation.
  backend.otherDeviceUpsert(tianjin());
  await dialog(page).getByRole("button", { name: "确认恢复" }).click();
  await expect(dialog(page).getByText(/没有恢复任何内容/)).toBeVisible();
  expect(backend.state.visits.size).toBe(3);
  expect(backend.state.snapshots.some((s) => s.reason === "before_restore")).toBe(false);
});

test("a lost restore response is retried with the same ID and applied once", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [dongcheng()] });
  await backend.install();
  await login(page);
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  backend.seedSnapshot("manual");
  backend.otherDeviceUpsert(xicheng());
  await page.evaluate(() => window.dispatchEvent(new Event("online")));
  await expect.poll(() => cachedVisits(page)).toBe(2);
  backend.state.failRestore = 1;

  await openSnapshots(page);
  await dialog(page).getByRole("button", { name: "预览并恢复" }).click();
  await dialog(page).getByRole("button", { name: "确认恢复" }).click();
  await expect(dialog(page).getByRole("alert")).toBeVisible();
  expect(backend.state.visits.size).toBe(2);
  await dialog(page).getByRole("button", { name: "确认恢复" }).click();
  await expect(dialog(page).getByText(/已恢复，共调整 1 条记录/)).toBeVisible();
  expect(backend.state.visits.size).toBe(1);
  expect(backend.state.restoreCalls).toBe(2);
  // The retry reuses the ID, so a lost reply could never be applied twice.
  expect(new Set(backend.state.restoreIds).size).toBe(1);
  expect(backend.state.snapshots.filter((s) => s.reason === "before_restore")).toHaveLength(1);
});

test("migration saves a cloud snapshot first and refuses to start without one", async ({ page }) => {
  await seedLocal(page, [dongcheng({ color: gold }), tianjin()]);
  const backend = fakeBackend(page, { visits: [dongcheng(), xicheng()] });
  backend.state.failCreateSnapshot = 1;
  await backend.install();
  await login(page);
  const migration = page.getByRole("dialog", { name: "把本机足迹迁移到云端" });
  await expect(migration).toBeVisible();

  await migration.getByRole("button", { name: "确认迁移" }).click();
  await expect(migration.getByText(/无法先保存云端快照，未开始迁移/)).toBeVisible();
  expect(backend.state.syncCalls).toEqual([]);
  expect(backend.state.visits.size).toBe(2);

  await migration.getByRole("button", { name: "确认迁移" }).click();
  await expect(migration.getByText(/迁移完成/)).toBeVisible();
  expect(backend.state.snapshotCalls).toEqual(["before_migration", "before_migration"]);
  expect(backend.state.snapshots).toHaveLength(1);
  // The snapshot holds the cloud data from before the migration.
  expect(backend.state.snapshots[0].visits.map((v) => v.code).sort()).toEqual(["110101", "110102"]);
  expect(backend.state.visits.size).toBe(3);
});

test("migration into an empty account does not need a snapshot", async ({ page }) => {
  await seedLocal(page, [dongcheng()]);
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  await login(page);
  const migration = page.getByRole("dialog", { name: "把本机足迹迁移到云端" });
  await migration.getByRole("button", { name: "确认迁移" }).click();
  await expect(migration.getByText(/迁移完成/)).toBeVisible();
  expect(backend.state.snapshotCalls).toEqual([]);
});
