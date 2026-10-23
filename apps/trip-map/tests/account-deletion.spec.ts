import { test, expect } from "@playwright/test";
import { create, toBinary, toJsonString, fromBinary } from "@bufbuild/protobuf";
import * as auth from "@kongken/api-client/bff/v1/auth_pb";
import * as footprint from "@kongken/api-client/bff/tripmap/v1/footprint_pb";
import * as statusPb from "@kongken/api-client/bff/tripmap/v1/status_pb";
import { REGION_VERSION } from "../src/lib/region-catalog";

test("confirmed account deletion retries cleanup, clears account cache and preserves local footprints", async ({
  page,
}) => {
  let attempts = 0;
  let authenticated = false;
  const local = { version: 1, visits: [], provinces: { "330000": "#4b937d" } };
  await page.addInitScript(
    (data) => localStorage.setItem("shanhai-travel-v1", JSON.stringify(data)),
    local,
  );
  await page.route("**/auth/login?*", async (route) => {
    authenticated = true;
    await route.fulfill({ status: 303, headers: { Location: "/" } });
  });
  await page.route("**/api/**", async (route) => {
    const method = route.request().url().split("/").pop();
    const binary = route.request().headers()["content-type"]?.includes("proto");
    let schema: any,
      data: any = {};
    switch (method) {
      case "IssueCSRFToken":
        schema = auth.IssueCSRFTokenResponseSchema;
        data = { token: "ci-csrf" };
        break;
      case "ListOAuthProviders":
        schema = auth.ListOAuthProvidersResponseSchema;
        data = { providers: [{ name: "logto", displayName: "Logto" }] };
        break;
      case "Me":
        if (!authenticated) {
          await route.fulfill({status:401,contentType:"application/json",body:'{"code":"unauthenticated"}'});
          return;
        }
        schema = auth.MeResponseSchema;
        data = {
          user: {
            id: "ci-account",
            username: "ci_user",
            displayName: "CI 旅行者",
          },
        };
        break;
      case "ListSessions":
        schema = auth.ListSessionsResponseSchema;
        break;
      case "GetStatus":
        schema = statusPb.GetStatusResponseSchema;
        data = { footprintSchema: 2, regionVersion: REGION_VERSION };
        break;
      case "GetFootprints":
        schema = footprint.GetFootprintsResponseSchema;
        data = { schemaVersion: 2, regionVersion: REGION_VERSION };
        break;
      case "GetChanges":
        schema = footprint.GetChangesResponseSchema;
        data = { schemaVersion: 2, regionVersion: REGION_VERSION };
        break;
      case "DeleteAccount": {
        expect(route.request().headers()["x-kongken-user"]).toBe("ci-account");
        const raw = route.request().postDataBuffer()!;
        const request = binary
          ? fromBinary(auth.DeleteAccountRequestSchema, raw)
          : JSON.parse(raw.toString());
        expect(request.confirmation).toBe("DELETE");
        expect("password" in request).toBe(false);
        attempts++;
        if (attempts === 1) {
          await route.fulfill({
            status: 503,
            contentType: "application/json",
            body: JSON.stringify({
              code: "unavailable",
              message: "cleanup incomplete",
            }),
          });
          return;
        }
        authenticated = false;
        schema = auth.DeleteAccountResponseSchema;
        break;
      }
      default:
        await route.fulfill({
          status: 404,
          contentType: "application/json",
          body: '{"code":"unimplemented"}',
        });
        return;
    }
    const message = create(schema, data);
    await route.fulfill({
      status: 200,
      contentType: binary ? "application/proto" : "application/json",
      body: binary
        ? Buffer.from(toBinary(schema, message))
        : toJsonString(schema, message),
    });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("button", { name: "前往用户中心登录", exact: true }).click();
  // The local footprint is offered for migration; this test is about deletion.
  await page.getByRole("button", { name: "稍后再说" }).click();
  await expect(
    page.getByRole("button", { name: "账号", exact: true }),
  ).toBeVisible();
  // An older page that is still open changes this account's old-format cache and leaves it
  // unreadable; another account's unsynced cache sits in the same browser.
  const theirs = JSON.stringify({ schema: 2, saved: { version: 2, visits: [], provinces: {} }, revision: 1, pendingBatch: { id: "b", revision: 1, ops: [] } });
  // (storage events reach other pages only, so the old page is a second one)
  const old = await page.context().newPage();
  await old.goto("/");
  await old.evaluate((other) => {
    localStorage.setItem("shanhai-cloud-v1:ci-account", "{invalid json from an old page");
    localStorage.setItem("shanhai-cloud-v2:someone-else", other);
    localStorage.setItem("shanhai-cloud-v1:someone-else", "{their unreadable cache");
  }, theirs);
  await old.close();
  const notice = page.getByRole("dialog", { name: "旧版页面修改了账号缓存" });
  await expect(notice).toBeVisible();
  await notice.getByRole("button", { name: "不合并，保留在旧位置" }).click();
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "删除云端数据", exact: true }).click();
  const confirm = page.getByRole("button", {
    name: "确认永久删除",
    exact: true,
  });
  await expect(confirm).toBeDisabled();
  await expect(page.getByText("不会注销用户中心账号", { exact: false })).toBeVisible();
  await expect(page.getByRole("dialog")).not.toContainText(/Logto/i);
  await expect(page.locator('input[type="password"]')).toHaveCount(0);
  await page.getByLabel("输入“删除”确认", { exact: true }).fill("删除");
  await confirm.click();
  await expect(page.getByText(/云端数据删除可能尚未完成/)).toBeVisible();
  await expect.poll(() => attempts).toBe(1);
  await confirm.click();
  await expect(
    page.getByRole("button", { name: "登录", exact: true }),
  ).toBeVisible();
  await expect.poll(() => attempts).toBe(2);
  expect(
    await page.evaluate(() => localStorage.getItem("shanhai-session-v1")),
  ).toBeNull();
  expect(
    await page.evaluate(() =>
      Object.keys(localStorage).filter((k) => k.includes("ci-account")),
    ),
  ).toEqual([]);
  // The other account's data is not this account's to discard.
  expect(
    await page.evaluate(() => [
      localStorage.getItem("shanhai-cloud-v2:someone-else") !== null,
      localStorage.getItem("shanhai-cloud-v1:someone-else"),
    ]),
  ).toEqual([true, "{their unreadable cache"]);
  expect(
    await page.evaluate(() =>
      JSON.parse(localStorage.getItem("shanhai-travel-v1")!),
    ),
  ).toEqual(local);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
});
