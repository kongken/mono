import assert from "node:assert/strict";
import { test } from "node:test";
import { EMPTY_FILTERS, lookupCount, matchRoute, returnPathOf, roleOf } from "./routes.ts";

const ID = "0c3b2a10-5d4e-4f6a-8b7c-1234567890ab";

test("the console has a home, a user list and one detail page per user ID", () => {
  assert.deepEqual(matchRoute("/"), { name: "home" });
  assert.deepEqual(matchRoute("/users"), { name: "users" });
  assert.deepEqual(matchRoute("/users/"), { name: "users" });
  assert.deepEqual(matchRoute(`/users/${ID}`), { name: "user", id: ID });
});

test("anything else is not a page: no catch-all, no look-alike IDs", () => {
  for (const p of ["/login", "/admin", "/users/abc", `/users/${ID}/edit`, `/users/${ID.toUpperCase()}`, "/users//x", "/metro/cities", "/users/../x"]) {
    assert.deepEqual(matchRoute(p), { name: "not-found" }, p);
  }
});

test("user pages need the platform role, the home page only a session", () => {
  assert.equal(roleOf(matchRoute("/users")), "ozid-admin");
  assert.equal(roleOf(matchRoute(`/users/${ID}`)), "ozid-admin");
  assert.equal(roleOf(matchRoute("/")), "");
});

test("only console pages can become a return target; everything else returns home", () => {
  assert.equal(returnPathOf("/users"), "/users");
  assert.equal(returnPathOf(`/users/${ID}`), `/users/${ID}`);
  assert.equal(returnPathOf("/"), "/");
  for (const p of ["/nope", "//evil.example", "/users/x", "/users/..%2f", "https://evil.example/", ""]) assert.equal(returnPathOf(p), "/", p);
});

test("at most one lookup is used at a time", () => {
  assert.equal(lookupCount(EMPTY_FILTERS), 0);
  assert.equal(lookupCount({ ...EMPTY_FILTERS, email: "a@example.com" }), 1);
  assert.equal(lookupCount({ ...EMPTY_FILTERS, email: "a@example.com", nickname: "a" }), 2);
  assert.equal(lookupCount({ ...EMPTY_FILTERS, email: "  ", status: "active" }), 0);
});
