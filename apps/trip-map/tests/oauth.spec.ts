import { test, expect } from "@playwright/test";
import { fakeBackend } from "./fake-backend";
import { create, toJsonString } from "@bufbuild/protobuf";
import { ListOAuthProvidersResponseSchema } from "@kongken/api-client/bff/v1/auth_pb";

test("missing Logto offers local mode and refresh, never a password form", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  let available = false;
  await page.route("**/api/bff.v1.AuthService/ListOAuthProviders", route => route.fulfill({
    contentType: "application/json",
    body: toJsonString(ListOAuthProvidersResponseSchema, create(ListOAuthProvidersResponseSchema, {
      providers: available ? [{ name: "logto", displayName: "Logto" }] : [],
    })),
  }));
  await page.goto("/");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByText(/统一登录暂不可用/)).toBeVisible();
  await expect(page.locator('input[type="password"]')).toHaveCount(0);
  await expect(page.getByRole("button", { name: "注册", exact: true })).toHaveCount(0);
  available = true;
  await page.getByRole("button", { name: "刷新登录入口" }).click();
  await expect(page.getByRole("button", { name: "使用 Logto 登录" })).toBeVisible();
  await expect(page.getByText(/统一登录暂不可用/)).toHaveCount(0);
  await page.getByRole("button", { name: "暂不登录" }).click();
  await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();
});

test("BFF login uses top-level navigation and restores via Me without SPA authorization material", async ({ page }) => {
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  await page.route("**/api/bff.v1.AuthService/ListOAuthProviders", route => route.fulfill({
    contentType: "application/json",
    body: toJsonString(ListOAuthProvidersResponseSchema,create(ListOAuthProvidersResponseSchema,{providers:[{name:"logto",displayName:"Logto"}]})),
  }));
  let navigated = false;
  await page.route("**/auth/login?*", async route => {
    expect(route.request().isNavigationRequest()).toBe(true);
    const url = new URL(route.request().url());
    expect(url.searchParams.get("app")).toBe("trip-map");
    expect(url.searchParams.get("provider")).toBe("logto");
    backend.state.authenticated = true; // HTTP callback established a BFF session.
    navigated = true;
    await route.fulfill({ status: 303, headers: { Location: "/" } });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("button", { name: /Logto/ }).click();
  await expect(page.getByRole("button", { name: "账号", exact: true })).toBeVisible();
  expect(navigated).toBe(true);
  expect(new URL(page.url()).pathname).toBe("/");
  expect(new URL(page.url()).search).toBe("");
  const session = await page.evaluate(() => ({local:localStorage.getItem("shanhai-session-v1"),pending:sessionStorage.getItem("shanhai-oauth-pending-v1")}));
  expect(session.pending).toBeNull();
  expect(session.local).not.toMatch(/"token"|"state"|"code"/);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
