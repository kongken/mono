import assert from "node:assert/strict";
import { test } from "node:test";
import { matchRoute, pageOf } from "./routes.ts";

test("only the OzID pages exist", () => {
  assert.equal(matchRoute("/"), "overview");
  assert.equal(matchRoute("/login"), "login");
  assert.equal(matchRoute("/login/"), "login");
  assert.equal(matchRoute("/register"), "register");
  assert.equal(matchRoute("/forgot-password"), "forgot-password");
  assert.equal(matchRoute("/profile"), "profile");
  assert.equal(matchRoute("/security"), "security");
  assert.equal(matchRoute("/sessions"), "sessions");
  assert.equal(matchRoute("/sessions/"), "sessions");
  assert.equal(matchRoute("/close-account"), "close-account");
  assert.equal(matchRoute("/close-account/"), "close-account");
  // The mail landing pages are not routes of the app: they are served before it starts.
  for (const other of ["/admin", "/login/x", "/register/x", "/account/verify-email", "/account/reset-password", "//evil.example"]) assert.equal(matchRoute(other), "not-found", other);
});

test("switching forms keeps the query that says where the user came from", () => {
  const q = "?app_id=metro&return_path=%2Fcity%2Fshanghai";
  assert.equal(pageOf("login", q), `/login${q}`);
  assert.equal(pageOf("register", q), `/register${q}`);
  assert.equal(pageOf("forgot", q), `/forgot-password${q}`);
  assert.equal(pageOf("register", ""), "/register");
});
