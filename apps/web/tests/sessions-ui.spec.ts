import { test, expect } from "@playwright/test";
import * as bffAuth from "@trip-map/api-client/bff/v1/auth_pb";
import { create, toJsonString } from "@bufbuild/protobuf";
import { fakeBackend, login } from "./fake-backend";

// UI-only: real Cookie/BFF chain is covered in Go e2e (internal/e2e/sessions_chain_test.go).
test.describe("account sessions list", () => {
  test("desktop shows devices from bff ListSessions", async ({ page }) => {
    const backend = fakeBackend(page, { visits: [] });
    await backend.install();
    await page.route("**/api/bff.v1.AuthService/ListSessions", async (route) => {
      const body = create(bffAuth.ListSessionsResponseSchema, {
        sessions: [
          {
            id: "s-current",
            current: true,
            createdAt: { seconds: BigInt(1_700_000_000), nanos: 0 },
            lastUsedAt: { seconds: BigInt(1_700_000_100), nanos: 0 },
          },
          {
            id: "s-other",
            current: false,
            createdAt: { seconds: BigInt(1_699_000_000), nanos: 0 },
            lastUsedAt: { seconds: BigInt(1_699_000_500), nanos: 0 },
          },
        ],
      });
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: toJsonString(bffAuth.ListSessionsResponseSchema, body),
      });
    });
    await login(page);
    await page.getByRole("button", { name: "账号", exact: true }).click();
    await expect(page.getByText("已登录的设备")).toBeVisible();
    await expect(page.getByText("当前设备")).toBeVisible();
    await expect(page.locator(".account-sessions li").filter({ hasText: "其他设备" })).toBeVisible();
  });
});
