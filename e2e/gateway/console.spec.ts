import { expect, test, type Page } from "@playwright/test";
import { acct, failOnPolicyViolations, noOverflow, panel, stack, submitLogin } from "./support";

/**
 * The management console from an allowed address, through the real gateway and the real console container:
 * sign-in at the user center, the role gate, and the Origin rule of the BFF (an operator who is signed in at
 * the user center still cannot run a management call from any page but the console's).
 */
failOnPolicyViolations();

const USERS = "/api/bff.v1.UserAdminService/";
const TOKENS = "/bff.v1.AccessTokenService/";

type Answer = { readable: boolean; status: number; body: string };
type Token = { id: string; userId: string; name: string; audience: string; scopes: string[]; revokedAt?: string };

/**
 * One Connect call from the page the browser is on, so the Origin header is the page's own (a page cannot
 * choose it): the same Cookie, a CSRF token issued for the call, and a body. `readable` is false when the
 * browser refused to hand the answer to the page (a refusal that carries no CORS headers).
 */
function rpc(page: Page, procedure: string, body: object = {}): Promise<Answer> {
  return page.evaluate(
    async ({ api, procedure, body }) => {
      const issued = await fetch(`${api}/bff.v1.AuthService/IssueCSRFToken`, { method: "POST", credentials: "include", headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" }, body: "{}" });
      const { token } = (await issued.json()) as { token: string };
      try {
        const res = await fetch(`${api}${procedure}`, { method: "POST", credentials: "include", headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1", "X-CSRF-Token": token }, body: JSON.stringify(body) });
        return { readable: true, status: res.status, body: await res.text() };
      } catch (e) {
        return { readable: false, status: 0, body: String(e) };
      }
    },
    { api: `${stack.api}/api`, procedure, body },
  );
}

/** The call was answered and succeeded. */
const ok = (a: Answer) => a.readable && a.status === 200;
const json = <T>(a: Answer): T => {
  expect(ok(a), a.body).toBe(true);
  return JSON.parse(a.body) as T;
};
const tokensOf = (a: Answer) => json<{ tokens?: Token[] }>(a).tokens ?? [];

/** A refusal: either the browser never gave the page an answer, or the answer is not a success and carries no token data. */
function expectRefused(a: Answer, label: string) {
  expect(ok(a), `${label}: ${a.status} ${a.body}`).toBe(false);
  expect(a.body, label).not.toMatch(/"tokens"|"secret"|mcp_|email|@example\.com/);
}

async function signInAtUserCenter(page: Page, kind: string) {
  await page.goto(`${stack.id}/login`);
  await submitLogin(page, acct(kind));
  await expect(page.getByTestId("overview")).toBeVisible();
}

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

test("an operator signed in at the user center cannot run the console's management calls from the user center's own page", async ({ page }) => {
  await signInAtUserCenter(page, "operator");
  // From a page of the user center, with the operator's own Cookie and a valid CSRF pair: the BFF's console-only
  // rule refuses user management and metro administration (HTTP 403, asserted by the gateway probes of
  // internal/e2e), and its refusal carries no CORS headers, so the browser fails the request and the page never
  // reads an answer. The same page and the same pair do get an answer for Me.
  const failed = new Set<string>();
  page.on("requestfailed", (r) => failed.add(new URL(r.url()).pathname.replace(/^\/api/, "")));
  for (const procedure of ["/bff.v1.UserAdminService/ListUsers", "/bff.metro.v1.AdminService/ListAdminCities"]) {
    const answer = await rpc(page, procedure);
    expect(answer.readable, procedure).toBe(false);
    expect(answer.body, procedure).not.toMatch(/email|@example\.com|secret/);
    expect(failed.has(procedure), procedure).toBe(true);
  }
  // The same page works for what the user center is for.
  expect((await rpc(page, "/bff.v1.AuthService/Me")).status).toBe(200);
});

test("Timeline token self-service at the user center: own tokens only, Timeline only, and nothing of another account", async ({ page, browser }) => {
  await signInAtUserCenter(page, "operator");
  // The legitimate path: the user center's origin manages the signed-in account's own Timeline tokens.
  const created = json<{ token: Token; secret: string }>(await rpc(page, `${TOKENS}CreateAccessToken`, { name: "gateway timeline self-service", scopes: ["timeline:read"], expiresInDays: 7 }));
  expect(created.token.audience).toBe("timeline");
  expect(created.token.scopes).toEqual(["timeline:read"]);
  expect(created.secret).toMatch(/^mcp_/);
  const own = tokensOf(await rpc(page, `${TOKENS}ListAccessTokens`));
  expect(own.map((t) => t.id)).toContain(created.token.id);
  expect(own.every((t) => t.audience === "timeline" && t.userId === created.token.userId)).toBe(true);

  // What self-service can never do, whoever the account is: a metro token, the metro scopes, or everyone's list.
  const before = tokensOf(await rpc(page, `${TOKENS}ListAccessTokens`)).length;
  expectRefused(await rpc(page, `${TOKENS}CreateAccessToken`, { name: "not allowed", audience: "metro", scopes: ["metro:read"], expiresInDays: 7 }), "metro token from the user center");
  expectRefused(await rpc(page, `${TOKENS}CreateAccessToken`, { name: "not allowed", scopes: ["metro:write"], expiresInDays: 7 }), "metro scope on a Timeline token");
  expectRefused(await rpc(page, `${TOKENS}ListAccessTokens`, { allUsers: true }), "every account's tokens from the user center");
  expect(tokensOf(await rpc(page, `${TOKENS}ListAccessTokens`)).length, "a refused call created something").toBe(before);

  // Another account at the same origin sees none of it and can neither revoke it nor list it.
  const other = await browser.newContext({ viewport: test.info().project.use.viewport, ignoreHTTPSErrors: true });
  try {
    const otherPage = await other.newPage();
    await signInAtUserCenter(otherPage, "rider");
    expect(tokensOf(await rpc(otherPage, `${TOKENS}ListAccessTokens`)).map((t) => t.id)).not.toContain(created.token.id);
    expectRefused(await rpc(otherPage, `${TOKENS}RevokeAccessToken`, { id: created.token.id }), "revoking another account's token");
    expectRefused(await rpc(otherPage, `${TOKENS}ListAccessTokens`, { allUsers: true }), "another account listing everyone's tokens");
  } finally {
    await other.close();
  }
  const afterOther = tokensOf(await rpc(page, `${TOKENS}ListAccessTokens`)).find((t) => t.id === created.token.id);
  expect(afterOther?.revokedAt, "another account's call revoked the token").toBeFalsy();

  // The owner revokes it (and leaves the account as it was).
  json(await rpc(page, `${TOKENS}RevokeAccessToken`, { id: created.token.id }));
  const revoked = tokensOf(await rpc(page, `${TOKENS}ListAccessTokens`)).find((t) => t.id === created.token.id);
  expect(revoked?.revokedAt).toBeTruthy();
});

test("Metro management tokens stay at the console: role and origin both required, and the user center never reaches them", async ({ page, browser }) => {
  // Without the metro-admin role the console's own origin is still refused, and nothing is created.
  await openConsole(page, "operator", "/users");
  expectRefused(await rpc(page, `${TOKENS}ListAccessTokens`), "list without the role");
  expectRefused(await rpc(page, `${TOKENS}CreateAccessToken`, { name: "no role", expiresInDays: 7 }), "create without the role");
  const plain = await browser.newContext({ viewport: test.info().project.use.viewport, ignoreHTTPSErrors: true });
  try {
    const plainPage = await plain.newPage();
    await openConsole(plainPage, "plain", "/users");
    expectRefused(await rpc(plainPage, `${TOKENS}ListAccessTokens`, { allUsers: true }), "list everyone without any role");
  } finally {
    await plain.close();
  }

  // With the role, at the console: the management token (audience metro) is created and listed, everyone's list is readable.
  const admin = await browser.newContext({ viewport: test.info().project.use.viewport, ignoreHTTPSErrors: true });
  try {
    const adminPage = await admin.newPage();
    await openConsole(adminPage, "metro", "/");
    const created = json<{ token: Token; secret: string }>(await rpc(adminPage, `${TOKENS}CreateAccessToken`, { name: "gateway metro management", scopes: ["metro:read"], expiresInDays: 7 }));
    expect(created.token.audience).toBe("metro");
    expect(tokensOf(await rpc(adminPage, `${TOKENS}ListAccessTokens`)).map((t) => t.id)).toContain(created.token.id);
    json(await rpc(adminPage, `${TOKENS}ListAccessTokens`, { allUsers: true }));

    // The same account, signed in, at the user center: the management token is neither listed nor revocable there.
    await adminPage.goto(`${stack.id}/`);
    await expect(adminPage.getByTestId("overview")).toBeVisible();
    expect(tokensOf(await rpc(adminPage, `${TOKENS}ListAccessTokens`)).map((t) => t.id)).not.toContain(created.token.id);
    expectRefused(await rpc(adminPage, `${TOKENS}RevokeAccessToken`, { id: created.token.id }), "revoking a metro token from the user center");
    expectRefused(await rpc(adminPage, `${TOKENS}CreateAccessToken`, { name: "not from here", audience: "metro", expiresInDays: 7 }), "creating a metro token from the user center");

    // A product's origin is neither surface: the BFF refuses it without CORS headers, so the page reads nothing.
    await adminPage.goto(`${stack.metro}/`);
    await adminPage.waitForLoadState("networkidle");
    const product = await rpc(adminPage, `${TOKENS}ListAccessTokens`);
    expect(product.readable, product.body).toBe(false);
    expect(await rpc(adminPage, `${TOKENS}CreateAccessToken`, { name: "from a product", expiresInDays: 7 })).toMatchObject({ readable: false });

    // Nothing above revoked it, and nothing above made a second one; the console revokes its own token.
    await adminPage.goto(`${stack.admin}/`);
    await expect(adminPage.getByTestId("sections")).toBeVisible();
    const still = tokensOf(await rpc(adminPage, `${TOKENS}ListAccessTokens`)).filter((t) => t.audience === "metro" && t.name === "gateway metro management");
    expect(still.map((t) => t.id)).toEqual([created.token.id]);
    expect(still[0].revokedAt).toBeFalsy();
    json(await rpc(adminPage, `${TOKENS}RevokeAccessToken`, { id: created.token.id }));
  } finally {
    await admin.close();
  }
});
