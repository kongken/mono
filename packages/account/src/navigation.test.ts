import assert from "node:assert/strict";
import { test } from "node:test";
import { isNavigableUrl, navQuery, ozidUrl, parseApiBase, parseOrigin, readNavContext } from "./navigation.ts";

test("a context needs one app and at most one path, and only plausible values", () => {
  assert.deepEqual(readNavContext(""), { kind: "none" });
  assert.deepEqual(readNavContext("?other=1"), { kind: "none" });
  assert.deepEqual(readNavContext("?app_id=metro&return_path=%2Fme"), { kind: "context", appId: "metro", returnPath: "/me" });
  assert.deepEqual(readNavContext("?app_id=trip-map"), { kind: "context", appId: "trip-map", returnPath: "" });
  for (const bad of [
    "?return_path=%2Fme", // no app
    "?app_id=metro&app_id=admin", // repeated
    "?app_id=metro&return_path=%2Fa&return_path=%2Fb",
    "?app_id=https%3A%2F%2Fevil.example",
    "?app_id=Metro",
    "?app_id=",
    `?app_id=${"a".repeat(33)}`,
    `?app_id=metro&return_path=%2F${"a".repeat(300)}`,
  ]) {
    assert.deepEqual(readNavContext(bad), { kind: "invalid" }, bad);
  }
});

test("the query round-trips and encodes the path", () => {
  const q = navQuery({ appId: "metro", returnPath: "/city/shanghai" });
  assert.equal(q, "?app_id=metro&return_path=%2Fcity%2Fshanghai");
  assert.deepEqual(readNavContext(q), { kind: "context", appId: "metro", returnPath: "/city/shanghai" });
  assert.equal(navQuery({ appId: "ozid" }), "?app_id=ozid");
});

test("the OzID address is built from the configured origin only", () => {
  assert.equal(ozidUrl("https://id.example.com/", "login", { appId: "metro", returnPath: "/me" }), "https://id.example.com/login?app_id=metro&return_path=%2Fme");
  assert.equal(ozidUrl("https://id.example.com", "security", { appId: "admin" }), "https://id.example.com/security?app_id=admin");
});

test("the API base must be an absolute https URL (http only on localhost)", () => {
  assert.deepEqual(parseApiBase("https://api.example.com/api/"), { ok: true, value: "https://api.example.com/api" });
  assert.deepEqual(parseApiBase(" https://api.example.com "), { ok: true, value: "https://api.example.com" });
  assert.deepEqual(parseApiBase("http://localhost:18081/api"), { ok: true, value: "http://localhost:18081/api" });
  assert.deepEqual(parseApiBase("http://127.0.0.1:18081"), { ok: true, value: "http://127.0.0.1:18081" });
  for (const bad of [undefined, "", "  ", "/api", "api.example.com", "http://api.example.com", "ftp://x.example.com", "https://u:p@api.example.com", "https://api.example.com?x=1", "https://api.example.com#x"]) {
    const r = parseApiBase(bad);
    assert.equal(r.ok, false, String(bad));
  }
  const missing = parseApiBase(undefined);
  assert.ok(!missing.ok && missing.error.includes("VITE_API_BASE_URL is required"));
});

test("an origin setting has no path", () => {
  assert.deepEqual(parseOrigin("https://id.example.com", "VITE_OZID_BASE_URL"), { ok: true, value: "https://id.example.com" });
  assert.deepEqual(parseOrigin("https://id.example.com/", "X"), { ok: true, value: "https://id.example.com" });
  assert.equal(parseOrigin("https://id.example.com/app", "X").ok, false);
  assert.equal(parseOrigin("http://id.example.com", "X").ok, false);
});

test("only http(s) addresses without credentials are navigated to", () => {
  assert.equal(isNavigableUrl("https://metro.example.com/me"), true);
  assert.equal(isNavigableUrl("http://localhost:5174/"), true);
  for (const bad of ["javascript:alert(1)", "data:text/html,x", "//evil.example", "/relative", "https://u:p@x.example.com", ""]) assert.equal(isNavigableUrl(bad), false, bad);
});
