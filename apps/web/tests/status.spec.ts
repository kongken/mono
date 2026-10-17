import { test, expect, type Page, type Request } from "@playwright/test";
import { create, toJsonString } from "@bufbuild/protobuf";
import * as statusPb from "@trip-map/api-client/bff/v1/status_pb";

// The public status page reads bff.v1.StatusService anonymously. The tests stand in for the
// BFF; the real BFF → TLS gRPC → tripmap chain is covered by the Go end-to-end tests.

const STATUS = "**/api/bff.v1.StatusService/GetStatus";
const body = (data: object) => toJsonString(statusPb.GetStatusResponseSchema, create(statusPb.GetStatusResponseSchema, data as never));

const healthy = { ready: true, footprintSchema: 2, shareEnabled: true, dependencies: [{ name: "postgres", ready: true }, { name: "redis", ready: true }, { name: "s3", ready: true }] };

const overflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);

test("anyone can read the service status without signing in, on desktop and phone", async ({ page }) => {
  const seen: Request[] = [];
  await page.route(STATUS, (route) => {
    seen.push(route.request());
    return route.fulfill({ status: 200, contentType: "application/json", body: body(healthy) });
  });
  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "服务状态" })).toBeVisible();
  await expect(page.getByText("全部服务正常")).toBeVisible();
  for (const label of ["数据库", "缓存", "对象存储"]) {
    await expect(page.locator(".status-list li").filter({ hasText: label })).toContainText("正常");
  }
  await expect(page.locator(".status-list li").filter({ hasText: "分享链接" })).toContainText("已开启");
  expect(await overflow(page)).toBeLessThanOrEqual(0);
  // Everything on the page is inside the viewport and readable.
  const width = page.viewportSize()!.width;
  for (const item of await page.locator(".status-card, .status-list li").all()) {
    const box = (await item.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(width);
  }
  expect(seen).toHaveLength(1);
  expect(seen[0].url()).toContain("/api/bff.v1.StatusService/GetStatus");
  expect(seen[0].headers()["connect-protocol-version"]).toBe("1");
  expect(seen[0].headers()["authorization"]).toBeUndefined();
});

test("an unhealthy dependency is shown, and a failed check can be retried", async ({ page }) => {
  let calls = 0;
  await page.route(STATUS, (route) => {
    calls++;
    if (calls === 1) return route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ code: "unavailable", message: "service unavailable" }) });
    if (calls === 2) return route.fulfill({ status: 200, contentType: "application/json", body: body({ ...healthy, ready: false, dependencies: [{ name: "postgres", ready: true }, { name: "redis", ready: false }] }) });
    return route.fulfill({ status: 200, contentType: "application/json", body: body(healthy) });
  });
  await page.goto("/status");
  await expect(page.getByText("暂时无法获取服务状态")).toBeVisible();
  await page.getByRole("button", { name: "重新检查" }).click();
  await expect(page.getByText("部分服务异常")).toBeVisible();
  await expect(page.locator(".status-list li").filter({ hasText: "缓存" })).toContainText("异常");
  expect(await overflow(page)).toBeLessThanOrEqual(0);
  await page.getByRole("button", { name: "重新检查" }).click();
  await expect(page.getByText("全部服务正常")).toBeVisible();
});

test("the status page leaves local footprints and any session alone", async ({ page }) => {
  const local = { version: 1, visits: [], provinces: { "330000": "#4b937d" } };
  await page.addInitScript((data) => localStorage.setItem("shanhai-travel-v1", JSON.stringify(data)), local);
  const headers: Record<string, string>[] = [];
  await page.route(STATUS, (route) => {
    headers.push(route.request().headers());
    return route.fulfill({ status: 200, contentType: "application/json", body: body(healthy) });
  });
  await page.goto("/status");
  await expect(page.getByText("全部服务正常")).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("shanhai-travel-v1"))).toBe(JSON.stringify(local));
  expect(headers[0]["authorization"]).toBeUndefined();
  expect(headers[0]["x-tripmap-schema"]).toBeUndefined();
});
