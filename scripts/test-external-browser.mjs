// Real Chromium against the Go test's BFF -> signed usersrv -> PostgreSQL/Redis.
// Provider navigation is intercepted locally; no real provider is contacted.
import assert from "node:assert/strict";
import { chromium } from "@playwright/test";

const base = process.env.EXTERNAL_BFF_URL;
assert.ok(base, "EXTERNAL_BFF_URL is required");
const browser = await chromium.launch({ args: ["--no-sandbox"] });
try {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(`${base}/ping`);
  const call = (page, method, body = {}, csrf = true) => page.evaluate(async ({ base, method, body, csrf }) => {
    let token;
    if (csrf) {
      const r = await fetch(`${base}/api/bff.v1.AuthService/IssueCSRFToken`, {
        method: "POST", credentials: "include", body: "{}",
        headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" },
      });
      token = (await r.json()).token;
    }
    const r = await fetch(`${base}/api/${method}`, {
      method: "POST", credentials: "include", body: JSON.stringify(body),
      headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1", ...(token ? { "X-CSRF-Token": token } : {}) },
    });
    return { status: r.status, data: await r.json() };
  }, { base, method, body, csrf });
  const ext = "bff.v1.ExternalAuthService/";
  const auth = "bff.v1.AuthService/";
  const start = async (page) => {
    const r = await call(page, `${ext}StartExternalLogin`, { provider: "google", intent: "login", appId: "trip-map", returnPath: "/city/shanghai" });
    assert.equal(r.status, 200);
    return r.data;
  };
  await context.route("https://accounts.google.com/o/oauth2/v2/auth?*", async (route) => {
    const u = new URL(route.request().url());
    const state = u.searchParams.get("state");
    assert.ok(u.searchParams.get("nonce"));
    assert.equal(u.searchParams.get("code_challenge_method"), "S256");
    await route.fulfill({ status: 302, headers: { Location: `${base}/api/identity/callback/google?state=${state}&code=${state}` } });
  });
  const first = await start(page);
  const other = await browser.newContext();
  const stolenPage = await other.newPage();
  const state = new URL(first.authorizationUrl).searchParams.get("state");
  await stolenPage.goto(`${base}/api/identity/callback/google?state=${state}&code=${state}`);
  assert.equal(new URL(stolenPage.url()).searchParams.get("result"), "STATE_INVALID");
  assert.equal((await other.cookies()).some(c => c.name === "kongken_sid"), false);
  await other.close();

  await page.goto(first.authorizationUrl);
  assert.ok(page.url().startsWith(`${base}/account/external?`));
  assert.equal(page.url().includes(state), false);
  assert.equal((await context.cookies()).some(c => c.name === "kongken_sid"), false);
  const pending = await call(page, `${ext}GetExternalFlow`, { flowId: first.flowId }, false);
  assert.equal(pending.data.status, "registration_pending");
  assert.equal(pending.data.emailConfirmed, true);
  const denied = await call(page, `${ext}CompleteExternalRegistration`, { flowId: first.flowId, mutationId: "browser-create", displayName: "Browser owner" }, false);
  assert.equal(denied.status, 403);
  const completed = await call(page, `${ext}CompleteExternalRegistration`, { flowId: first.flowId, mutationId: "browser-create", displayName: "Browser owner" });
  assert.equal(completed.status, 200);
  assert.equal(new URL(completed.data.returnUrl).pathname, "/city/shanghai");
  const me = await call(page, `${auth}Me`, {}, false);
  assert.equal(me.status, 200);
  assert.equal(me.data.authenticatedAt, undefined);
  assert.equal(me.data.user.displayName, "Browser owner");
  const sid = (await context.cookies()).find(c => c.name === "kongken_sid");
  assert.ok(sid?.httpOnly);
  const privateCookies = (await context.cookies()).filter(c => c.name.startsWith("kongken_ext_") || c.name === "kongken_browser");
  assert.ok(privateCookies.length >= 2 && privateCookies.every(c => c.httpOnly && c.sameSite === "Lax"));
  assert.equal((await page.evaluate(() => document.cookie)).includes(sid.value), false);
  assert.deepEqual(await page.evaluate(() => [localStorage.length, sessionStorage.length]), [0, 0]);
  await page.goto(`${base}/api/identity/callback/google?state=${state}&code=${state}`);
  assert.equal(new URL(page.url()).searchParams.get("result"), "STATE_INVALID");
  assert.equal((await context.cookies()).find(c => c.name === "kongken_sid").value, sid.value);

  // Two tabs share the browser context, but have independent flow Cookies.
  const second = await start(page);
  const tab = await context.newPage();
  await tab.goto(`${base}/ping`);
  const cancelled = await start(tab);
  const cancelledState = new URL(cancelled.authorizationUrl).searchParams.get("state");
  await tab.goto(`${base}/api/identity/callback/google?state=${cancelledState}&error=access_denied&error_description=private`);
  assert.equal(new URL(tab.url()).searchParams.get("result"), "PROVIDER_REJECTED");
  assert.equal((await context.cookies()).find(c => c.name === "kongken_sid").value, sid.value);
  await page.goto(second.authorizationUrl);
  const me2 = await call(page, `${auth}Me`, {}, false);
  assert.equal(me2.status, 200);
  assert.equal(me2.data.user.id, me.data.user.id);
  assert.equal(me2.data.authenticatedAt, undefined);
  // Even a physically late response containing an old Cookie can never restore
  // an earlier session generation. The server rejects it on every request.
  await context.addCookies([{ name: "kongken_sid", value: sid.value, url: base }]);
  assert.equal((await call(page, `${auth}Me`, {}, false)).status, 401);
  assert.equal((await page.evaluate(() => document.referrer)).includes("code="), false);
  console.log("Chromium: browser binding, explicit creation, CSRF, isolated tabs, stable UUID, replay, password age and stale generation passed");
  await context.close();
} finally {
  await browser.close();
}
