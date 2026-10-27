import assert from "node:assert/strict";
import { test } from "node:test";
import { loadConfigFromFile } from "vite";
import { APP_ID, chooseReturn, resolveApiBase, resolveOzid, returnPathFor } from "./ozid.ts";

const ID = "https://id.example.com";
const enabled = (env: { VITE_OZID_ORIGIN?: string }) => {
  const t = resolveOzid(env);
  assert.equal(t.enabled, true, JSON.stringify(t));
  return t as Extract<ReturnType<typeof resolveOzid>, { enabled: true }>;
};

test("a build without a usable OzID origin offers no way in", () => {
  for (const origin of [undefined, "", "  ", "id.example.com", "http://id.example.com", "https://id.example.com/x", "https://user@id.example.com", "https://id.example.com?x=1", "ftp://id.example.com"]) {
    const t = resolveOzid({ VITE_OZID_ORIGIN: origin });
    assert.equal(t.enabled, false, String(origin));
  }
});

test("the sign-in address names the product and a page, nothing else", () => {
  const t = enabled({ VITE_OZID_ORIGIN: `${ID}/` });
  assert.equal(APP_ID, "metro");
  assert.equal(t.login, `${ID}/login?app_id=metro&return_path=%2F`);
  assert.equal(t.loginAt("/me"), `${ID}/login?app_id=metro&return_path=%2Fme`);
  assert.equal(t.home, ID);
  assert.equal(t.closeAccount, `${ID}/close-account`);
  // The address holds no person, no session and no host of the product.
  assert.doesNotMatch(t.loginAt("/cities/shanghai"), /token|session|user|metro\.example|next=/i);
});

test("loopback development origins are accepted", () => {
  assert.equal(enabled({ VITE_OZID_ORIGIN: "http://localhost:5175" }).origin, "http://localhost:5175");
});

test("a mail landing page may only continue with a context the BFF validated", () => {
  const t = enabled({ VITE_OZID_ORIGIN: ID });
  assert.equal(t.loginFor(null), t.login);
  assert.equal(t.loginFor({ appId: "metro", returnPath: "/me" }), `${ID}/login?app_id=metro&return_path=%2Fme`);
});

test("a sign-in returns to the page it started on when that page is one of the product's, otherwise to the start page", () => {
  const cases: [string, string][] = [
    ["/", "/"],
    ["/me", "/me"],
    ["/me/", "/me"],
    ["/cities/shanghai", "/cities/shanghai"],
    ["/cities/shanghai/", "/cities/shanghai"],
    ["/topics/six-cars", "/topics/six-cars"],
    ["/topics", "/topics"],
    ["/topics/", "/topics"],
    ["/u/alice_1", "/u/alice_1"],
    // The old account addresses, the mail landing pages and unknown paths have no business being returned to.
    ["/login", "/"],
    ["/account", "/"],
    ["/account/verify-email", "/"],
    ["/account/reset-password", "/"],
    ["/admin", "/"],
    ["/nowhere", "/"],
    ["/cities", "/"],
    // A path that cannot be written canonically never goes out.
    ["/cities/%E4%B8%8A%E6%B5%B7", "/"],
    ["/cities/a%2Fb", "/"],
    ["/cities/%2E%2E", "/"],
    ["/cities/..", "/"],
    ["/cities/" + "x".repeat(65), "/"],
    ["/u/white space", "/"],
    ["//evil.example/me", "/"],
  ];
  for (const [from, to] of cases) assert.equal(returnPathFor(from), to, from);
});

test("only the pathname is read: a query or fragment can never reach the return path", () => {
  // The caller passes location.pathname; even a full address-bar string cannot smuggle anything through.
  for (const dirty of ["/me?next=https://evil.example", "/me#token=secret", "/cities/shanghai?x=1#y", "/u/alice?return_path=//evil"]) {
    const out = returnPathFor(dirty);
    assert.doesNotMatch(out, /[?#=]|evil|secret|token/, dirty);
  }
});

test("a page is used only after the BFF confirmed that exact pathname; every other answer is the start page", async () => {
  const asked: string[] = [];
  const ask = (answer: () => Promise<boolean>) => async (path: string) => {
    asked.push(path);
    return answer();
  };
  assert.equal(await chooseReturn("/me", ask(async () => true)), "/me");
  assert.equal(await chooseReturn("/me", ask(async () => false)), "/");
  assert.equal(await chooseReturn("/me", ask(async () => Promise.reject(new Error("offline")))), "/");
  // Slow answers lose to the deadline.
  assert.equal(await chooseReturn("/cities/shanghai", ask(() => new Promise(() => {})), 20), "/");
  // The start page needs no question; neither does a page that falls back to it.
  asked.length = 0;
  assert.equal(await chooseReturn("/", ask(async () => assert.fail("not asked"))), "/");
  assert.equal(await chooseReturn("/login", ask(async () => assert.fail("not asked"))), "/");
  assert.deepEqual(asked, []);
});

test("the API base is absolute for a build; only the development server may use its own proxy", () => {
  assert.deepEqual(resolveApiBase({ VITE_API_BASE_URL: "https://api.example.com/api/" }, false), { ok: true, base: "https://api.example.com/api" });
  assert.deepEqual(resolveApiBase({}, true), { ok: true, base: "/api" });
  assert.deepEqual(resolveApiBase({ VITE_API_BASE_URL: "https://api.example.com/api" }, true), { ok: true, base: "https://api.example.com/api" });
  for (const bad of [undefined, "", "/api", "api.example.com", "http://api.example.com/api", "https://api.example.com/api?x=1", "https://u:p@api.example.com"]) {
    assert.equal(resolveApiBase({ VITE_API_BASE_URL: bad }, false).ok, false, String(bad));
  }
  // A malformed value is an error even in development: it is not silently replaced by the proxy.
  assert.equal(resolveApiBase({ VITE_API_BASE_URL: "/api" }, true).ok, false);
});

// The build gate (vite.config.ts): the product has no standalone mode, so a build needs both addresses.
async function build(env: Record<string, string>) {
  const saved: Record<string, string | undefined> = {};
  for (const key of ["VITE_API_BASE_URL", "VITE_OZID_ORIGIN"]) {
    saved[key] = process.env[key];
    delete process.env[key];
  }
  Object.assign(process.env, env);
  try {
    return await loadConfigFromFile({ command: "build", mode: "production" }, "vite.config.ts", process.cwd(), "silent");
  } finally {
    for (const [key, value] of Object.entries(saved)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
}

test("the build gate: a complete configuration builds, every other one is refused", async () => {
  await build({ VITE_API_BASE_URL: "https://api.example.com/api", VITE_OZID_ORIGIN: ID });
  await assert.rejects(build({}), /cannot be built/);
  await assert.rejects(build({ VITE_OZID_ORIGIN: ID }), /VITE_API_BASE_URL/);
  await assert.rejects(build({ VITE_API_BASE_URL: "/api", VITE_OZID_ORIGIN: ID }), /cannot be built/);
  await assert.rejects(build({ VITE_API_BASE_URL: "http://api.example.com/api", VITE_OZID_ORIGIN: ID }), /cannot be built/);
  await assert.rejects(build({ VITE_API_BASE_URL: "https://api.example.com/api" }), /VITE_OZID_ORIGIN/);
  await assert.rejects(build({ VITE_API_BASE_URL: "https://api.example.com/api", VITE_OZID_ORIGIN: "http://id.example.com" }), /VITE_OZID_ORIGIN/);
  await assert.rejects(build({ VITE_API_BASE_URL: "https://api.example.com/api", VITE_OZID_ORIGIN: `${ID}/x` }), /VITE_OZID_ORIGIN/);
});
