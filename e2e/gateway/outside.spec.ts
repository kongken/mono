import { expect, test } from "@playwright/test";
import { acct, failOnPolicyViolations, loginAt, stack, submitLogin } from "./support";

/**
 * A browser that is NOT on the console's allowlist: here the console host goes to the gateway instance that
 * has the shipped example's own source rule (documentation range only, so no real network is allowed). The
 * console is closed, with nothing shown and nothing leaked; everything public keeps working on the same
 * browser and the same gateway.
 */
failOnPolicyViolations();

test("the console is closed: no page, no asset, no single-page fallback, and nothing in the answer", async ({ page }) => {
  for (const path of ["/", "/users", "/index.html", "/assets/index.js", "/no/such/page"]) {
    const res = await page.goto(`${stack.admin}${path}`);
    expect(res!.status(), path).toBe(403);
    expect((await res!.text()).trim(), path).toBe("");
    expect(res!.headers()["content-security-policy"], path).toBeUndefined();
    expect(res!.headers()["server"], path).toBeUndefined();
  }
  // A fetch from another origin gets no answer it may read either (the refusal carries no CORS headers).
  await page.goto(`${stack.trip}/`);
  const outcome = await page.evaluate(async (url) => fetch(url, { mode: "cors" }).then((r) => `status ${r.status}`, (e) => `blocked: ${(e as Error).message}`), `${stack.admin}/`);
  expect(outcome).toMatch(/^blocked/);
});

test("an operator signed in at the user center is still kept out of the console, and the user center and both products keep working", async ({ page, context }) => {
  await page.goto(`${stack.id}/login`);
  await submitLogin(page, acct("operator"));
  await expect(page.getByTestId("overview")).toBeVisible();
  const res = await page.goto(`${stack.admin}/users`);
  expect(res!.status()).toBe(403);

  const metro = await context.newPage();
  await metro.goto(`${stack.metro}/cities/shanghai`);
  await expect(metro.getByRole("banner").getByText(acct("operator").displayName)).toBeVisible();
  const trip = await context.newPage();
  await trip.goto(`${stack.trip}/`);
  await expect(trip.getByRole("button", { name: "账号", exact: true })).toBeVisible();
});

test("signing in from a product works from outside the console's list", async ({ page }) => {
  await page.goto(`${stack.metro}/cities/shanghai`);
  await page.getByRole("banner").getByRole("button", { name: "登录", exact: true }).click();
  await expect(page).toHaveURL(loginAt("metro", "/cities/shanghai"));
  await submitLogin(page, acct("rider"));
  await page.waitForURL(`${stack.metro}/cities/shanghai`);
  await expect(page.getByRole("banner").getByText(acct("rider").displayName)).toBeVisible();
});
