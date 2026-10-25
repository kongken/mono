import test from "node:test";
import assert from "node:assert/strict";
import { loadConfigFromFile } from "vite";
import { APP_ID, chooseReturn, legacyAccountEntry, resolveOzid, returnPathFor } from "../src/lib/ozid.ts";

// Reserved example names only (id.example.com, api.example.com); nothing here is a real deployment.
const ID = "https://id.example.com";

test("without a backend there is no account center at all", () => {
  const r = resolveOzid({ VITE_OZID_ORIGIN: ID }, false);
  assert.equal(r.enabled, false);
  // Even a configured OzID origin yields no address to render or request.
  assert.equal("login" in r, false);
});

test("a backend without a usable OzID origin offers no link", () => {
  for (const value of [undefined, "", "   ", "http://id.example.com", `${ID}/path`, `${ID}?x=1`, "javascript:alert(1)", "id.example.com"]) {
    const r = resolveOzid({ VITE_OZID_ORIGIN: value }, true);
    assert.equal(r.enabled, false, String(value));
    assert.equal("login" in r, false, String(value));
  }
});

test("the sign-in address names the product and its start page, nothing else", () => {
  const r = resolveOzid({ VITE_OZID_ORIGIN: ID }, true, "/");
  assert.equal(r.enabled, true);
  const url = new URL(r.login);
  assert.equal(url.origin, ID);
  assert.equal(url.pathname, "/login");
  assert.deepEqual([...url.searchParams.keys()].sort(), ["app_id", "return_path"]);
  assert.equal(url.searchParams.get("app_id"), APP_ID);
  assert.equal(url.searchParams.get("return_path"), "/");
  assert.equal(r.closeAccount, `${ID}/close-account`);
  assert.equal(r.home, ID);
});

test("a product served under a base path returns to that path", () => {
  const r = resolveOzid({ VITE_OZID_ORIGIN: ID }, true, "/footprints/");
  assert.equal(new URL(r.login).searchParams.get("return_path"), "/footprints/");
});

test("loopback development origins are accepted", () => {
  const r = resolveOzid({ VITE_OZID_ORIGIN: "http://localhost:5175" }, true);
  assert.equal(r.enabled, true);
  assert.equal(new URL(r.login).origin, "http://localhost:5175");
});

test("a mail landing page may only continue with a context the BFF validated", () => {
  const r = resolveOzid({ VITE_OZID_ORIGIN: ID }, true);
  assert.equal(r.loginFor(null), r.login);
  const url = new URL(r.loginFor({ appId: "metro", returnPath: "/me" }));
  assert.equal(url.origin, ID);
  assert.equal(url.searchParams.get("app_id"), "metro");
  assert.equal(url.searchParams.get("return_path"), "/me");
});

test("a sign-in returns to the page it started on when that page is a listed one, otherwise to the start page", () => {
  assert.equal(returnPathFor("/"), "/");
  assert.equal(returnPathFor("/status"), "/status");
  assert.equal(returnPathFor("/status/"), "/status", "trailing slash is normalised, the result is canonical");
  // Anything not listed, or that holds a capability, is the start page: never carried to OzID.
  const token = "A".repeat(43);
  for (const path of [
    `/s/${token}`,
    "/s",
    "/account/verify-email",
    "/account/reset-password",
    "/login",
    "/account",
    "/status/extra",
    "/statuses",
    "/anything/else",
    "//evil.example.net",
    "/status/../s",
    "/%73tatus",
    "",
  ]) {
    assert.equal(returnPathFor(path), "/", JSON.stringify(path));
  }
});

test("only the pathname is read: a query or fragment can never reach the return path", () => {
  // The caller passes location.pathname; if a whole href were passed by mistake it still must not leak.
  for (const path of ["/status?x=secret", "/status#secret", "/?token=secret", "/status?next=https://evil.example.net"]) {
    const result = returnPathFor(path);
    assert.ok(!/[?#=]|secret|evil/.test(result), path);
  }
  const r = resolveOzid({ VITE_OZID_ORIGIN: ID }, true);
  const url = new URL(r.loginAt(returnPathFor("/status")));
  assert.deepEqual([...url.searchParams.keys()].sort(), ["app_id", "return_path"]);
  assert.equal(url.searchParams.get("return_path"), "/status");
});

test("a return path under a base path is canonical and registered per base", () => {
  assert.equal(returnPathFor("/footprints/", "/footprints/"), "/footprints/");
  assert.equal(returnPathFor("/footprints/status", "/footprints/"), "/footprints/status");
  assert.equal(returnPathFor("/status", "/footprints/"), "/footprints/", "outside the base is the start page");
  assert.equal(returnPathFor("/footprints/s/" + "A".repeat(43), "/footprints/"), "/footprints/");
});

test("a listed page is used only after the BFF confirmed that exact pathname; every other answer is the start page", async () => {
  const asked = [];
  const yes = async (p) => (asked.push(p), true);
  assert.equal(await chooseReturn("/status", "/", yes), "/status");
  assert.deepEqual(asked, ["/status"]);
  // The start page needs no question at all.
  assert.equal(await chooseReturn("/", "/", async () => assert.fail("no question for the start page")), "/");
  // A page holding a capability is never even asked about.
  assert.equal(await chooseReturn(`/s/${"A".repeat(43)}`, "/", async () => assert.fail("never asked")), "/");
  // Not registered, refused, an outage, a throw: the start page.
  assert.equal(await chooseReturn("/status", "/", async () => false), "/");
  assert.equal(await chooseReturn("/status", "/", async () => { throw new Error("unavailable"); }), "/");
  // A BFF that does not answer in time does not hold the sign-in back.
  const started = Date.now();
  assert.equal(await chooseReturn("/status", "/", () => new Promise(() => {}), 50), "/");
  assert.ok(Date.now() - started < 1000);
});

test("old account entry points only open the account window and are cleaned up", () => {
  assert.deepEqual(legacyAccountEntry("/", "?signin=1"), { path: "/", search: "" });
  assert.deepEqual(legacyAccountEntry("/", "?signin=1&z=1"), { path: "/", search: "" });
  assert.deepEqual(legacyAccountEntry("/login", ""), { path: "/", search: "" });
  assert.deepEqual(legacyAccountEntry("/account", ""), { path: "/", search: "" });
  assert.deepEqual(legacyAccountEntry("/login/", ""), { path: "/", search: "" });
  assert.deepEqual(legacyAccountEntry("/app/login", "", "/app/"), { path: "/app/", search: "" });
  // Nothing in an old URL is followed or kept: no next, no address, no return path.
  for (const search of ["?next=https://evil.example.net/", "?next=//evil.example.net", "?return_path=/x&app_id=metro", "?redirect=javascript:alert(1)"]) {
    const entry = legacyAccountEntry("/login", search);
    assert.deepEqual(entry, { path: "/", search: "" }, search);
  }
  const flagged = legacyAccountEntry("/", "?signin=1&next=https://evil.example.net/");
  assert.deepEqual(flagged, { path: "/", search: "" });
  assert.equal(new URL(flagged.path + flagged.search, "https://trip.example.com").origin, "https://trip.example.com");
  // Ordinary pages are not entries.
  assert.equal(legacyAccountEntry("/", ""), null);
  assert.equal(legacyAccountEntry("/", "?signin=0"), null);
  assert.equal(legacyAccountEntry("/loginx", ""), null);
  assert.equal(legacyAccountEntry("/status", ""), null);
  assert.equal(legacyAccountEntry("/s/token", ""), null);
});

// The build gate (vite.config.ts): a backend build needs the canonical API base and OzID's origin.
async function build(env) {
  const saved = {};
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

test("the build gate: local-only, complete and refused configurations", async () => {
  await build({}); // standalone local-only site needs neither address
  await build({ VITE_OZID_ORIGIN: ID }); // an origin without a backend is simply unused
  await build({ VITE_API_BASE_URL: "https://api.example.com/api", VITE_OZID_ORIGIN: ID });
  await assert.rejects(build({ VITE_API_BASE_URL: "/api", VITE_OZID_ORIGIN: ID }), /cannot be built/);
  await assert.rejects(build({ VITE_API_BASE_URL: "http://api.example.com/api", VITE_OZID_ORIGIN: ID }), /cannot be built/);
  await assert.rejects(build({ VITE_API_BASE_URL: "https://api.example.com/api" }), /VITE_OZID_ORIGIN/);
  await assert.rejects(build({ VITE_API_BASE_URL: "https://api.example.com/api", VITE_OZID_ORIGIN: "http://id.example.com" }), /VITE_OZID_ORIGIN/);
  await assert.rejects(build({ VITE_API_BASE_URL: "https://api.example.com/api", VITE_OZID_ORIGIN: `${ID}/x` }), /VITE_OZID_ORIGIN/);
});
