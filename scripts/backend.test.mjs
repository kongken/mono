import test from "node:test";
import assert from "node:assert/strict";
import { resolveApiConfig } from "../src/lib/backend.ts";

test("no configuration keeps the app in local-only mode", () => {
  assert.deepEqual(resolveApiConfig(), { enabled: false });
  assert.deepEqual(resolveApiConfig({}), { enabled: false });
  assert.deepEqual(resolveApiConfig({ VITE_API_BASE_URL: "   " }), { enabled: false });
});

test("accepts a same-origin path or an absolute http(s) URL", () => {
  assert.deepEqual(resolveApiConfig({ VITE_API_BASE_URL: "/api" }), { enabled: true, baseUrl: "/api" });
  assert.deepEqual(resolveApiConfig({ VITE_API_BASE_URL: " https://api.example.com/v1/ " }), {
    enabled: true,
    baseUrl: "https://api.example.com/v1",
  });
});

test("invalid addresses fall back to local-only mode", () => {
  for (const value of ["api", "//evil.example.com", "ftp://example.com", "javascript:alert(1)", "http://"])
    assert.deepEqual(resolveApiConfig({ VITE_API_BASE_URL: value }), { enabled: false }, value);
});
