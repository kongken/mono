import { test, expect } from "@playwright/test";
import { create, toBinary, toJsonString, fromBinary } from "@bufbuild/protobuf";
import * as auth from "../src/gen/tripmap/v1/auth_pb";
import * as footprint from "../src/gen/tripmap/v1/footprint_pb";

test("confirmed account deletion retries cleanup, clears account cache and preserves local footprints", async ({
  page,
}) => {
  let attempts = 0;
  const local = { version: 1, visits: [], provinces: { "330000": "#4b937d" } };
  await page.addInitScript(
    (data) => localStorage.setItem("shanhai-travel-v1", JSON.stringify(data)),
    local,
  );
  await page.route("**/api/**", async (route) => {
    const method = route.request().url().split("/").pop();
    const binary = route.request().headers()["content-type"]?.includes("proto");
    let schema: any,
      data: any = {};
    switch (method) {
      case "Login":
        schema = auth.LoginResponseSchema;
        data = {
          token: "ci-delete-token",
          user: {
            id: "ci-account",
            username: "ci_user",
            displayName: "CI 旅行者",
          },
          expiresAt: { seconds: BigInt(Math.floor(Date.now() / 1000) + 3600) },
        };
        break;
      case "ListOAuthProviders":
        schema = auth.ListOAuthProvidersResponseSchema;
        break;
      case "Me":
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
      case "GetFootprints":
        schema = footprint.GetFootprintsResponseSchema;
        break;
      case "GetChanges":
        schema = footprint.GetChangesResponseSchema;
        break;
      case "DeleteAccount": {
        const raw = route.request().postDataBuffer()!;
        const request = binary
          ? fromBinary(auth.DeleteAccountRequestSchema, raw)
          : JSON.parse(raw.toString());
        expect(request.confirmation).toBe("DELETE");
        expect(request.password).toBe("correct horse battery");
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
  await page.getByLabel("用户名", { exact: true }).fill("ci_user");
  await page.getByLabel("密码", { exact: true }).fill("correct horse battery");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  // The local footprint is offered for migration; this test is about deletion.
  await page.getByRole("button", { name: "稍后再说" }).click();
  await expect(
    page.getByRole("button", { name: "账号", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "注销账号", exact: true }).click();
  const confirm = page.getByRole("button", {
    name: "确认永久注销",
    exact: true,
  });
  await expect(confirm).toBeDisabled();
  await page
    .getByLabel("确认当前密码", { exact: true })
    .fill("correct horse battery");
  await page.getByLabel("输入“注销”确认", { exact: true }).fill("注销");
  await confirm.click();
  await expect(page.getByText(/账号注销可能尚未完成/)).toBeVisible();
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
      localStorage.getItem("shanhai-cloud-v1:ci-account"),
    ),
  ).toBeNull();
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
