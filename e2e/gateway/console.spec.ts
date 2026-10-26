import { expect, test, type Page } from "@playwright/test";
import { acct, failOnPolicyViolations, noOverflow, panel, stack, submitLogin } from "./support";

/**
 * The management console from an allowed address, through the real gateway and the real console container:
 * sign-in at the user center, the role gate, and the Origin rule of the BFF (an operator who is signed in at
 * the user center still cannot run a management call from any page but the console's).
 */
failOnPolicyViolations();

const USERS = "/api/bff.v1.UserAdminService/";

async function openConsole(page: Page, kind: string, path: string) {
  await page.goto(`${stack.admin}${path}`);
  await page.waitForURL(new RegExp(`^${stack.id}/login\\?app_id=admin&return_path=${encodeURIComponent(path)}$`));
  await panel(page).getByLabel("邮箱").fill(acct(kind).email);
  await panel(page).getByLabel("密码", { exact: true }).fill(stack.password);
  await panel(page).getByRole("button", { name: "登录", exact: true }).click();
  await page.waitForURL(`${stack.admin}${path}`);
}

test("an operator signs in at the user center from the console and lands on the users page; the console's headers hold", async ({ page, context }) => {
  const res = await page.goto(`${stack.admin}/users`);
  const headers = res!.headers();
  expect(headers["content-security-policy"]).toContain(`connect-src 'self' ${stack.api}`);
  expect(headers["x-frame-options"]).toBeTruthy();
  expect(headers["server"]).toBeUndefined();
  await page.waitForURL(new RegExp(`^${stack.id}/login\\?app_id=admin`));
  await page.goto(`${stack.admin}/users`);
  await page.waitForURL(new RegExp(`^${stack.id}/login\\?app_id=admin`));
  await submitLogin(page, acct("operator"));
  await page.waitForURL(`${stack.admin}/users`);
  await expect(page.getByRole("heading", { name: "用户" })).toBeVisible();
  await expect(page.getByTestId("user-row").first()).toBeVisible();
  await expect(page.getByTestId("user-table")).toContainText(acct("rider").email);
  expect(await noOverflow(page)).toBe(true);
  const sid = (await context.cookies()).find((c) => c.name === "kongken_sid");
  expect(sid!.domain).toBe(new URL(stack.api).hostname);
  expect((await context.cookies()).filter((c) => c.domain.replace(/^\./, "") === new URL(stack.admin).hostname)).toEqual([]);
  await page.getByRole("button", { name: "退出登录" }).click();
  await page.waitForURL(new RegExp(`^${stack.id}/login\\?app_id=admin`));
});

test("an account with no platform role sees no permission and never calls user management", async ({ page }) => {
  const userCalls: string[] = [];
  page.on("request", (r) => r.url().includes(USERS) && userCalls.push(r.url()));
  await openConsole(page, "plain", "/users");
  await expect(page.getByTestId("no-permission")).toBeVisible();
  await expect(page.getByTestId("sections")).toHaveCount(0);
  expect(userCalls).toEqual([]);
});

test("a metro administrator alone gets the metro section and no user data", async ({ page }) => {
  const userCalls: string[] = [];
  page.on("request", (r) => r.url().includes(USERS) && userCalls.push(r.url()));
  await openConsole(page, "metro", "/");
  await expect(page.getByTestId("sections").getByRole("link", { name: "地铁目录" })).toBeVisible();
  await expect(page.getByTestId("sections").getByRole("link", { name: "用户" })).toHaveCount(0);
  await page.goto(`${stack.admin}/users`);
  await expect(page.getByTestId("no-permission")).toBeVisible();
  expect(userCalls).toEqual([]);
});

test("an operator signed in at the user center cannot run management calls from the user center's own page", async ({ page }) => {
  await page.goto(`${stack.id}/login`);
  await submitLogin(page, acct("operator"));
  await expect(page.getByTestId("overview")).toBeVisible();
  // From a page of the user center, with the operator's own Cookie and a valid CSRF pair: the BFF's console-only
  // rule refuses it (HTTP 403, asserted by the gateway probes of internal/e2e), and its refusal carries no CORS
  // headers, so the browser fails the request and the page never reads an answer. The same page and the same
  // pair do get an answer for Me, and the console's own origin gets one for ListUsers (the first test above).
  const failed = new Set<string>();
  page.on("requestfailed", (r) => failed.add(new URL(r.url()).pathname.replace(/^\/api/, "")));
  const call = (procedure: string) =>
    page.evaluate(
      async ({ api, procedure }) => {
        const issued = await fetch(`${api}/bff.v1.AuthService/IssueCSRFToken`, { method: "POST", credentials: "include", headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" }, body: "{}" });
        const { token } = (await issued.json()) as { token: string };
        try {
          const res = await fetch(`${api}${procedure}`, { method: "POST", credentials: "include", headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1", "X-CSRF-Token": token }, body: "{}" });
          return { readable: true, status: res.status, body: await res.text() };
        } catch (e) {
          return { readable: false, status: 0, body: String(e) };
        }
      },
      { api: `${stack.api}/api`, procedure },
    );
  for (const procedure of ["/bff.v1.UserAdminService/ListUsers", "/bff.v1.AccessTokenService/ListAccessTokens", "/bff.metro.v1.AdminService/ListAdminCities"]) {
    const answer = await call(procedure);
    expect(answer.readable, procedure).toBe(false);
    expect(answer.body, procedure).not.toMatch(/email|@example\.com|secret/);
    expect(failed.has(procedure), procedure).toBe(true);
  }
  // The same page works for what the user center is for.
  expect((await call("/bff.v1.AuthService/Me")).status).toBe(200);
});
