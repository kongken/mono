import { readFile } from "node:fs/promises";
import { expect, test, type Page } from "@playwright/test";
import { OZID, setup } from "./fake";

// The metro catalogue administration inside the console, against a fake BFF (UI behaviour only). The same
// rules against the real chain (perimeter, roles, revision checks, atomic import, audit channels, tokens)
// are in internal/e2e (TestChainMetroAdmin*, TestChainAdminPerimeter*) and the real-stack spec.

const noOverflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const save = (page: Page) => page.getByRole("button", { name: "保存", exact: true });
const name = (page: Page) => page.getByLabel("名称", { exact: true });
const metroWrites = (s: Awaited<ReturnType<typeof setup>>) => s.metro.state.calls.filter((c) => /^(Create|Update|Delete|Import)/.test(c.method) && !(c.method === "ImportCatalog" && c.body.dryRun));

test.describe("who sees the metro administration", () => {
  test("a platform-only account has no metro section and no metro call, even by address", async ({ page }) => {
    const s = await setup(page, { roles: ["ozid-admin"] });
    await page.goto("/");
    await expect(page.getByTestId("sections").getByRole("link", { name: "地铁目录" })).toHaveCount(0);
    for (const path of ["/metro", "/metro/cities", "/metro/import", "/metro/tokens"]) {
      await page.goto(path);
      await expect(page.getByTestId("no-permission")).toBeVisible();
    }
    expect(s.metro.state.calls).toHaveLength(0);
  });

  test("an anonymous visitor is sent to OzID and comes back to the metro page they asked for", async ({ page }) => {
    const s = await setup(page, { signedIn: false });
    await page.goto("/metro/lines");
    await page.waitForURL(`${OZID}/login?**`);
    const url = new URL(page.url());
    expect(url.searchParams.get("app_id")).toBe("admin");
    expect(url.searchParams.get("return_path")).toBe("/metro/lines");
    expect(s.metro.state.calls).toHaveLength(0);
  });

  test("a metro administrator sees every section as part of the console, with no frame and no link to the old pages", async ({ page }) => {
    await setup(page, { roles: ["metro-admin"] });
    await page.goto("/");
    await page.getByTestId("sections").getByRole("link", { name: "地铁目录" }).click();
    await expect(page.getByRole("heading", { name: "地铁目录" })).toBeVisible();
    const sections = page.getByRole("navigation", { name: "地铁目录分区" });
    for (const label of ["城市", "线路", "车型", "专题", "导入", "导出", "审计日志", "管理令牌"]) await expect(sections.getByRole("link", { name: label, exact: true })).toBeVisible();
    await expect(page.locator("iframe")).toHaveCount(0);
    expect(await page.locator("a[href]").evaluateAll((as) => as.map((a) => (a as HTMLAnchorElement).href).filter((h) => !h.startsWith(location.origin) && !h.startsWith("https://id.example.com")))).toEqual([]);
    expect(new URL(page.url()).pathname).toBe("/metro");
    await sections.getByRole("link", { name: "线路", exact: true }).click();
    expect(new URL(page.url()).pathname).toBe("/metro/lines");
    await expect(sections.getByRole("link", { name: "线路", exact: true })).toHaveAttribute("aria-current", "page");
  });

  test("with no metro behind the BFF there is no section, no fake page and no further call", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"], metro: "absent" });
    await page.goto("/");
    await expect(page.getByText("这个环境没有部署地铁目录服务")).toBeVisible();
    await expect(page.getByTestId("sections")).toHaveCount(0);
    await page.goto("/metro/cities");
    await expect(page.getByTestId("metro-absent")).toBeVisible();
    await expect(page.getByRole("button", { name: "新建城市" })).toHaveCount(0);
    expect(s.metro.calls("CreateCity", "ListAccessTokens", "ImportCatalog")).toHaveLength(0);
  });

  test("a metro that does not answer at the moment keeps its section and says what happened on the page", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    s.metro.state.unavailable = 1;
    await page.goto("/");
    await expect(page.getByTestId("sections").getByRole("link", { name: "地铁目录" })).toBeVisible();
    // The probe of the section and the page's own list both find metro down.
    s.metro.state.unavailable = 2;
    await page.goto("/metro/cities");
    await expect(page.getByTestId("problem-unavailable")).toBeVisible();
    await page.getByRole("button", { name: "重试" }).click();
    await expect(page.getByRole("cell", { name: "shanghai", exact: true })).toBeVisible();
  });

  test("a closed perimeter is said plainly, shows no data and offers no retry", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    s.metro.state.perimeterClosed = true;
    await page.goto("/metro/cities");
    await expect(page.getByTestId("problem-not_allowed")).toBeVisible();
    await expect(page.getByRole("cell", { name: "shanghai" })).toHaveCount(0);
    await expect(page.getByTestId("no-permission")).toHaveCount(0);
    expect(s.visited).toHaveLength(0);
  });
});

test.describe("records: create, edit, delete", () => {
  test("a city is created, found, edited and deleted with no password or reason asked", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/cities");
    await expect(page.getByRole("cell", { name: "shanghai", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "新建城市" }).click();
    // Ordinary catalogue edits ask for exactly their fields: no password, no reason.
    await expect(page.getByLabel(/密码|原因/)).toHaveCount(0);
    await page.getByLabel("标识 slug").fill("beijing");
    await name(page).fill("北京");
    await save(page).click();
    await expect(page.getByRole("cell", { name: "beijing", exact: true })).toBeVisible();
    await expect(page.getByText("已创建城市")).toBeVisible();

    await page.getByLabel("搜索城市").fill("北");
    await expect(page.getByRole("cell", { name: "shanghai" })).toHaveCount(0);
    await page.getByRole("button", { name: "编辑 beijing" }).click();
    await name(page).fill("北京市");
    await save(page).click();
    await expect(page.getByRole("cell", { name: "北京市", exact: true })).toBeVisible();

    await page.getByRole("button", { name: "删除 beijing" }).click();
    await page.getByRole("button", { name: "确认删除" }).click();
    // The page behind an open dialog is hidden from role queries, so wait for the dialog itself to close.
    await expect(page.getByRole("button", { name: "确认删除" })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: "beijing" })).toHaveCount(0);
    expect(metroWrites(s).map((c) => c.method)).toEqual(["CreateCity", "UpdateCity", "DeleteCity"]);
    expect(s.metro.state.writesWithoutCsrf).toEqual([]);
    expect(s.metro.state.calls.every((c) => c.origin === "http://127.0.0.1:4177")).toBe(true);
  });

  test("field errors stay on their field, a taken key is named, and a line's code is fixed after creation", async ({ page }) => {
    await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/lines");
    await page.getByRole("button", { name: "新建线路" }).click();
    await page.getByLabel("代码", { exact: true }).fill("bj-1");
    await page.getByLabel("所属城市 slug").fill("nowhere");
    await name(page).fill("北京1号线");
    await page.getByLabel("颜色").fill("E4002B");
    await save(page).click();
    // Each problem is on the field the server named (it calls the city "city"), and said once.
    await expect(page.getByText("必须是 #RRGGBB")).toHaveCount(1);
    await expect(page.getByLabel("颜色")).toHaveAttribute("aria-invalid", "true");
    await expect(page.getByText("城市不存在")).toHaveCount(1);
    await expect(page.getByLabel("所属城市 slug")).toHaveAttribute("aria-invalid", "true");
    await page.getByLabel("颜色").fill("#E4002B");
    await page.getByLabel("所属城市 slug").fill("shanghai");
    await save(page).click();
    await expect(page.getByRole("cell", { name: "bj-1", exact: true })).toBeVisible();
    // The status column uses the shared labels (packages/metro-core).
    await expect(page.getByRole("row", { name: /bj-1/ }).getByRole("cell", { name: "运营中", exact: true })).toBeVisible();

    await page.getByRole("button", { name: "新建线路" }).click();
    await page.getByLabel("代码", { exact: true }).fill("bj-1");
    await page.getByLabel("所属城市 slug").fill("shanghai");
    await page.getByLabel("颜色").fill("#E4002B");
    await save(page).click();
    await expect(page.getByText("已存在")).toBeVisible();
    await page.getByRole("button", { name: "取消" }).click();

    await page.getByRole("button", { name: "编辑 bj-1" }).click();
    await expect(page.getByLabel("代码", { exact: true })).toBeDisabled();
  });

  test("a record that others still use is not deleted, and the answer says by what", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/cities");
    await page.getByRole("button", { name: "删除 shanghai" }).click();
    await page.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "仍被引用，不能删除：线路 2" })).toBeVisible();
    await page.getByRole("button", { name: "取消" }).click();
    await expect(page.getByRole("cell", { name: "shanghai", exact: true })).toBeVisible();
    expect(s.metro.state.cities.map((c) => c.slug)).toContain("shanghai");

    await page.getByRole("navigation", { name: "地铁目录分区" }).getByRole("link", { name: "车型", exact: true }).click();
    await page.getByRole("button", { name: "删除 02A02" }).click();
    await page.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "线路 2、专题 1" })).toBeVisible();
  });

  test("rolling stock and topics have their own pages and rules", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/rolling-stocks");
    await page.getByRole("button", { name: "新建车型" }).click();
    await page.getByLabel("代码", { exact: true }).fill("08A01");
    await page.getByLabel("载客量（可空）").fill("2500");
    await save(page).click();
    await expect(page.getByRole("cell", { name: "08A01", exact: true })).toBeVisible();
    expect(s.metro.state.stocks.find((x) => x.code === "08A01")?.capacity).toBe(2500);

    await page.getByRole("navigation", { name: "地铁目录分区" }).getByRole("link", { name: "专题", exact: true }).click();
    await page.getByRole("button", { name: "新建专题" }).click();
    await page.getByLabel("标识 slug").fill("empty");
    await name(page).fill("空专题");
    await save(page).click();
    await expect(page.getByText("车型与编组至少填一项")).toBeVisible();
    await page.getByLabel("车型代码（每行一个）").fill("08A01\n02A02");
    await save(page).click();
    await expect(page.getByRole("cell", { name: "08A01、02A02", exact: true })).toBeVisible();
  });

  test("an older answer arriving late never replaces the newer list", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    s.metro.state.delay = (method, body) => (method === "ListAdminCities" && body.query === "上" ? 900 : 0);
    await page.goto("/metro/cities");
    await expect(page.getByRole("cell", { name: "tokyo", exact: true })).toBeVisible();
    await page.getByLabel("搜索城市").fill("上");
    // The slow answer for "上" is still on its way when the search changes to "东".
    await expect.poll(() => s.metro.calls("ListAdminCities").some((c) => c.body.query === "上")).toBe(true);
    await page.getByLabel("搜索城市").fill("东");
    await expect(page.getByRole("cell", { name: "tokyo", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "shanghai" })).toHaveCount(0);
    await page.waitForTimeout(1200);
    await expect(page.getByRole("cell", { name: "tokyo", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "shanghai" })).toHaveCount(0);
  });
});

test.describe("a lost race keeps what was typed and asks again", () => {
  const edit = async (page: Page) => {
    await page.goto("/metro/cities");
    await page.getByRole("button", { name: "编辑 shanghai" }).click();
    await expect(name(page)).toHaveValue("上海");
  };

  test("the typed content stays, the differences are shown, and saving needs a second, explicit confirmation", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await edit(page);
    s.metro.touchCity("shanghai", "上海（别人改的）");
    await name(page).fill("上海市");
    await save(page).click();
    const conflict = page.getByTestId("conflict");
    await expect(conflict).toContainText("已被他人修改");
    // Nothing was overwritten: the field still holds the typed value, the server holds the other editor's.
    await expect(name(page)).toHaveValue("上海市");
    expect(s.metro.state.cities.find((c) => c.slug === "shanghai")?.name).toBe("上海（别人改的）");
    await expect(conflict.getByRole("row", { name: /名称/ })).toContainText("上海（别人改的）");
    await expect(conflict.getByRole("row", { name: /名称/ })).toContainText("上海市");
    // The way forward is a choice, and until it is made there is nothing to save.
    await expect(save(page)).toBeDisabled();
    await page.getByRole("button", { name: "保留我的修改，基于最新版本" }).click();
    await expect(page.getByText("请确认后再次点击保存")).toBeVisible();
    await expect(name(page)).toHaveValue("上海市");
    expect(metroWrites(s).filter((c) => c.method === "UpdateCity")).toHaveLength(1);
    await save(page).click();
    await expect(page.getByRole("cell", { name: "上海市", exact: true })).toBeVisible();
    const updates = metroWrites(s).filter((c) => c.method === "UpdateCity");
    expect(updates.map((c) => c.body.expectedRevision)).toEqual(["1", "2"]);
  });

  test("taking the latest content instead discards what was typed and writes nothing", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await edit(page);
    s.metro.touchCity("shanghai", "上海（别人改的）");
    await name(page).fill("上海市");
    await save(page).click();
    await page.getByRole("button", { name: "放弃我的修改，载入最新内容" }).click();
    await expect(name(page)).toHaveValue("上海（别人改的）");
    expect(metroWrites(s).filter((c) => c.method === "UpdateCity")).toHaveLength(1);
  });

  test("a record deleted meanwhile cannot be saved and the typed content stays to be copied", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await edit(page);
    s.metro.removeCity("shanghai");
    await name(page).fill("上海市");
    await save(page).click();
    await expect(page.getByTestId("conflict")).toContainText("已被删除");
    await expect(name(page)).toHaveValue("上海市");
    await expect(save(page)).toBeDisabled();
  });

  test("a delete that lost the race is not repeated by itself", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/cities");
    await page.getByRole("button", { name: "删除 tokyo" }).click();
    s.metro.touchCity("tokyo");
    await page.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByText("这条记录已被他人修改")).toBeVisible();
    expect(s.metro.state.cities.map((c) => c.slug)).toContain("tokyo");
    await page.getByRole("button", { name: "载入最新版本后重新确认" }).click();
    expect(metroWrites(s).filter((c) => c.method === "DeleteCity")).toHaveLength(1);
    await page.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByRole("button", { name: "确认删除" })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: /tokyo/ })).toHaveCount(0);
    expect(metroWrites(s).filter((c) => c.method === "DeleteCity").map((c) => c.body.expectedRevision)).toEqual(["1", "2"]);
  });
});

test.describe("import and export", () => {
  const good = JSON.stringify({ format: "metro-catalog/1", cities: [{ slug: "beijing", name: "北京" }] });

  test("a document is previewed first, an error blocks it, and only a clean one is applied, all or nothing", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/import");
    const apply = page.getByRole("button", { name: "确认导入" });
    await expect(apply).toBeDisabled();

    const bad = JSON.stringify({ format: "metro-catalog/1", cities: [{ slug: "beijing", name: "北京" }], lines: [{ code: "bj-1", color: "E4002B", city: "beijing" }] });
    await page.getByLabel("目录 JSON").fill(bad);
    await expect(page.getByText("lines[0].color")).toBeVisible();
    await expect(apply).toBeDisabled();
    expect(s.metro.state.cities.map((c) => c.slug)).toEqual(["shanghai", "tokyo"]);

    await page.getByLabel("目录 JSON").fill(good);
    await expect(page.getByText(/预览（尚未写入）/)).toContainText("城市 新增 1");
    await expect(apply).toBeEnabled();
    // The preview wrote nothing, and was a dry run.
    expect(s.metro.state.cities.map((c) => c.slug)).toEqual(["shanghai", "tokyo"]);
    expect(s.metro.calls("ImportCatalog").every((c) => c.body.dryRun === true)).toBe(true);

    await apply.click();
    await expect(page.getByText(/已导入：.*目录版本 2/)).toBeVisible();
    expect(s.metro.state.cities.map((c) => c.slug)).toEqual(["shanghai", "tokyo", "beijing"]);
    expect(s.metro.calls("ImportCatalog").some((c) => !c.body.dryRun)).toBe(true);
    expect(s.metro.state.writesWithoutCsrf).toEqual([]);
  });

  test("an invalid JSON text is a preview error, not a write", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/import");
    await page.getByLabel("目录 JSON").fill("{not json");
    await expect(page.getByText("不是有效的 JSON")).toBeVisible();
    await expect(page.getByRole("button", { name: "确认导入" })).toBeDisabled();
    expect(metroWrites(s)).toHaveLength(0);
  });

  test("an export is the file the import takes back, and importing it again changes nothing", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/export");
    const download = page.waitForEvent("download");
    await page.getByRole("button", { name: "下载目录 JSON" }).click();
    const file = await download;
    expect(file.suggestedFilename()).toBe("metro-catalog-v1.json");
    const text = await readFile((await file.path())!, "utf8");
    expect(JSON.parse(text)).toEqual(s.metro.exportDoc());

    await page.getByRole("navigation", { name: "地铁目录分区" }).getByRole("link", { name: "导入", exact: true }).click();
    await page.getByLabel("目录 JSON").fill(text);
    const preview = page.getByText(/预览（尚未写入）/);
    await expect(preview).toContainText("城市 新增 0 / 更新 0 / 无变化 2");
    await expect(preview).toContainText("线路 新增 0 / 更新 0 / 无变化 2");
    await expect(preview).toContainText("专题 新增 0 / 更新 0 / 无变化 1");
  });
});

test.describe("audit log", () => {
  test("the channel filter separates web from MCP, the MCP token is named, and details show before and after", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/audit");
    await expect(page.getByRole("cell", { name: "city.create" })).toBeVisible();
    await expect(page.getByRole("cell", { name: "line.update" })).toBeVisible();
    await expect(page.getByRole("cell", { name: "MCP (tok-abcd)" })).toBeVisible();

    await page.getByLabel("渠道").selectOption("mcp");
    await expect(page.getByRole("cell", { name: "city.create" })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: "line.update" })).toBeVisible();
    await page.getByLabel("渠道").selectOption("web");
    await expect(page.getByRole("cell", { name: "line.update" })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: "网页" }).first()).toBeVisible();
    expect(s.metro.calls("ListAuditLogs").map((c) => c.body.channel)).toContain("mcp");

    await page.getByLabel("渠道").selectOption("");
    await page.getByRole("row", { name: /line\.update/ }).getByRole("button", { name: "详情" }).click();
    await expect(page.getByText("一号线")).toBeVisible();
    await expect(page.locator("pre").filter({ hasText: '"name": "1号线"' })).toBeVisible();
  });

  test("a web edit made in the console shows up in the log", async ({ page }) => {
    await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/cities");
    await page.getByRole("button", { name: "编辑 tokyo" }).click();
    await name(page).fill("东京都");
    await save(page).click();
    await expect(page.getByRole("cell", { name: "东京都", exact: true })).toBeVisible();
    await page.getByRole("navigation", { name: "地铁目录分区" }).getByRole("link", { name: "审计日志", exact: true }).click();
    await expect(page.getByRole("cell", { name: "city.update" })).toBeVisible();
  });
});

test.describe("management tokens", () => {
  test("the secret is shown once and is nowhere afterwards", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/tokens");
    await page.getByLabel("名称").fill("我的 AI 助手");
    await page.getByLabel("权限").selectOption("metro:write");
    await page.getByLabel("有效期").selectOption("7");
    await page.getByRole("button", { name: "创建", exact: true }).click();
    const secret = page.getByTestId("token-secret");
    await expect(secret).toContainText("mcp_");
    const text = await secret.innerText();
    await expect(page.getByRole("cell", { name: "我的 AI 助手", exact: true })).toBeVisible();
    await expect(page.locator("code", { hasText: "/mcp/metro" })).toBeVisible();
    expect(s.metro.state.calls.find((c) => c.method === "CreateAccessToken")?.body).toMatchObject({ audience: "metro", scopes: ["metro:write"], expiresInDays: 7 });
    // Not in the address, not in any storage.
    expect(page.url()).not.toContain(text);
    expect(await page.evaluate(() => JSON.stringify([{ ...localStorage }, { ...sessionStorage }]))).not.toContain("mcp_");

    await page.reload();
    await expect(page.getByRole("cell", { name: "我的 AI 助手", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "读写", exact: true })).toBeVisible();
    await expect(page.getByText("有效", { exact: true })).toBeVisible();
    await expect(page.getByTestId("token-secret")).toHaveCount(0);
    expect(await page.content()).not.toContain(text);

    await page.getByRole("button", { name: "吊销 我的 AI 助手" }).click();
    await expect(page.getByText("已吊销")).toBeVisible();
    await expect(page.getByRole("button", { name: "吊销 我的 AI 助手" })).toHaveCount(0);
  });

  test("the limit of active tokens is reported, not swallowed", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    const future = new Date(Date.now() + 5 * 864e5).toISOString();
    for (let i = 0; i < 10; i++) s.metro.state.tokens.push({ id: `t${i}`, userId: "x", name: `令牌 ${i}`, audience: "metro", scopes: ["metro:read"], createdAt: future, expiresAt: future });
    await page.goto("/metro/tokens");
    await page.getByLabel("名称").fill("第十一个");
    await page.getByRole("button", { name: "创建", exact: true }).click();
    await expect(page.getByRole("alert")).toBeVisible();
    await expect(page.getByTestId("token-secret")).toHaveCount(0);
  });
});

test.describe("a password confirmation in the middle of an edit", () => {
  test("the typed content is kept for the trip, offered again after it, and never submitted by itself", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/cities");
    await expect(page.getByRole("cell", { name: "tokyo", exact: true })).toBeVisible();
    s.metro.state.reauthNeeded = true;
    await page.getByRole("button", { name: "新建城市" }).click();
    await page.getByLabel("标识 slug").fill("osaka");
    await name(page).fill("大阪");
    await save(page).click();
    await expect(page.getByTestId("problem-reauth")).toBeVisible();
    await expect(page.getByTestId("problem-reauth")).toContainText("不会自动重放");
    // The prompt does not redirect by itself; the dialog and its content are still there.
    await page.waitForTimeout(400);
    expect(s.visited).toHaveLength(0);
    await expect(name(page)).toHaveValue("大阪");

    await page.getByRole("button", { name: "前往 OzID 验证密码" }).click();
    await page.waitForURL(`${OZID}/security?**`);
    const url = new URL(page.url());
    expect(url.searchParams.get("app_id")).toBe("admin");
    expect(url.searchParams.get("return_path")).toBe("/metro/cities");
    expect(metroWrites(s).filter((c) => c.method === "CreateCity")).toHaveLength(1);

    // Back from OzID with a fresh password check: the content is offered, nothing was replayed.
    s.metro.state.reauthNeeded = false;
    await page.goto("/metro/cities");
    await expect(page.getByText("已恢复你在验证密码前填写、尚未提交的内容")).toBeVisible();
    await expect(page.getByLabel("标识 slug")).toHaveValue("osaka");
    await expect(name(page)).toHaveValue("大阪");
    await page.waitForTimeout(400);
    expect(metroWrites(s).filter((c) => c.method === "CreateCity")).toHaveLength(1);
    expect(s.metro.state.cities.map((c) => c.slug)).not.toContain("osaka");
    await save(page).click();
    await expect(page.getByRole("cell", { name: "osaka", exact: true })).toBeVisible();
    expect(metroWrites(s).filter((c) => c.method === "CreateCity")).toHaveLength(2);

    // It comes back once only.
    await page.reload();
    await expect(page.getByRole("cell", { name: "osaka", exact: true })).toBeVisible();
    await expect(page.getByLabel("标识 slug")).toHaveCount(0);
  });

  test("an edit whose record changed during the trip comes back as a conflict to confirm", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/cities");
    await page.getByRole("button", { name: "编辑 tokyo" }).click();
    s.metro.state.reauthNeeded = true;
    await name(page).fill("东京都");
    await save(page).click();
    await page.getByRole("button", { name: "前往 OzID 验证密码" }).click();
    await page.waitForURL(`${OZID}/security?**`);
    s.metro.state.reauthNeeded = false;
    s.metro.touchCity("tokyo", "东京（别人改的）");
    await page.goto("/metro/cities");
    await expect(page.getByTestId("conflict")).toContainText("已被他人修改");
    await expect(name(page)).toHaveValue("东京都");
    await expect(save(page)).toBeDisabled();
    expect(metroWrites(s).filter((c) => c.method === "UpdateCity")).toHaveLength(1);
    await page.getByRole("button", { name: "保留我的修改，基于最新版本" }).click();
    await save(page).click();
    await expect(page.getByRole("cell", { name: "东京都", exact: true })).toBeVisible();
    expect(metroWrites(s).filter((c) => c.method === "UpdateCity").map((c) => c.body.expectedRevision)).toEqual(["1", "2"]);
  });

  test("an import text and a token form survive the trip the same way", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    s.metro.state.reauthNeeded = true;
    await page.goto("/metro/import");
    const text = JSON.stringify({ format: "metro-catalog/1", cities: [{ slug: "beijing", name: "北京" }] });
    await page.getByLabel("目录 JSON").fill(text);
    await expect(page.getByTestId("problem-reauth")).toBeVisible();
    await page.getByRole("button", { name: "前往 OzID 验证密码" }).click();
    await page.waitForURL(`${OZID}/security?**`);
    expect(new URL(page.url()).searchParams.get("return_path")).toBe("/metro/import");
    s.metro.state.reauthNeeded = false;
    await page.goto("/metro/import");
    await expect(page.getByLabel("目录 JSON")).toHaveValue(text);
    await expect(page.getByText(/预览（尚未写入）/)).toContainText("城市 新增 1");
    expect(metroWrites(s)).toHaveLength(0);
    await page.getByRole("button", { name: "确认导入" }).click();
    await expect(page.getByText(/已导入/)).toBeVisible();

    s.metro.state.reauthNeeded = true;
    await page.getByRole("navigation", { name: "地铁目录分区" }).getByRole("link", { name: "管理令牌", exact: true }).click();
    await expect(page.getByTestId("problem-reauth")).toBeVisible();
    s.metro.state.reauthNeeded = false;
    await page.goto("/metro/tokens");
    await expect(page.getByRole("cell", { name: "还没有令牌。" })).toBeVisible();
    await page.getByLabel("名称").fill("夜间任务");
    await page.getByLabel("权限").selectOption("metro:write");
    s.metro.state.reauthNeeded = true;
    await page.getByRole("button", { name: "创建", exact: true }).click();
    await page.getByRole("button", { name: "前往 OzID 验证密码" }).click();
    await page.waitForURL(`${OZID}/security?**`);
    s.metro.state.reauthNeeded = false;
    await page.goto("/metro/tokens");
    await expect(page.getByLabel("名称")).toHaveValue("夜间任务");
    await expect(page.getByLabel("权限")).toHaveValue("metro:write");
    expect(s.metro.state.tokens).toHaveLength(0);
  });

  test("what is offered back is taken out of storage the moment it is read", async ({ page }) => {
    const s = await setup(page, { roles: ["metro-admin"] });
    await page.goto("/metro/cities");
    s.metro.state.reauthNeeded = true;
    await page.getByRole("button", { name: "新建城市" }).click();
    await page.getByLabel("标识 slug").fill("osaka");
    await name(page).fill("大阪");
    await save(page).click();
    await page.getByRole("button", { name: "前往 OzID 验证密码" }).click();
    await page.waitForURL(`${OZID}/security?**`);
    s.metro.state.reauthNeeded = false;
    await page.goto("/metro/cities");
    await expect(page.getByLabel("标识 slug")).toHaveValue("osaka");
    expect(await page.evaluate(() => sessionStorage.getItem("admin.draft.v1"))).toBeNull();
  });
});

test.describe("on a narrow screen", () => {
  test("every metro page fits the screen", async ({ page }) => {
    await setup(page, { roles: ["metro-admin"] });
    for (const path of ["/metro/cities", "/metro/lines", "/metro/rolling-stocks", "/metro/topics", "/metro/import", "/metro/export", "/metro/audit", "/metro/tokens"]) {
      await page.goto(path);
      await expect(page.getByRole("navigation", { name: "地铁目录分区" })).toBeVisible();
      await page.waitForTimeout(150);
      expect(await noOverflow(page), path).toBe(true);
    }
    await page.goto("/metro/lines");
    await page.getByRole("button", { name: "新建线路" }).click();
    await expect(page.getByRole("dialog")).toBeVisible();
    expect(await noOverflow(page)).toBe(true);
  });
});
