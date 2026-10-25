import assert from "node:assert/strict";
import { test } from "node:test";
import { matchRoute } from "./routes.ts";

test("only the three OzID pages exist", () => {
  assert.equal(matchRoute("/"), "overview");
  assert.equal(matchRoute("/login"), "login");
  assert.equal(matchRoute("/login/"), "login");
  assert.equal(matchRoute("/security"), "security");
  for (const other of ["/admin", "/register", "/login/x", "/account/verify-email", "//evil.example"]) assert.equal(matchRoute(other), "not-found", other);
});
