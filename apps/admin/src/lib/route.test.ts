import assert from "node:assert/strict";
import { test } from "node:test";
import { auditFilterProblem, auditPathOf, EMPTY_AUDIT_FILTERS, EMPTY_FILTERS, lookupCount, matchRoute, returnPathOf, roleOf, userOfSearch } from "./routes.ts";

const ID = "0c3b2a10-5d4e-4f6a-8b7c-1234567890ab";

test("the console has a home, a user list and one detail page per user ID", () => {
  assert.deepEqual(matchRoute("/"), { name: "home" });
  assert.deepEqual(matchRoute("/users"), { name: "users" });
  assert.deepEqual(matchRoute("/users/"), { name: "users" });
  assert.deepEqual(matchRoute(`/users/${ID}`), { name: "user", id: ID });
  assert.deepEqual(matchRoute("/audit/users"), { name: "audit-users" });
  assert.deepEqual(matchRoute("/audit/users/"), { name: "audit-users" });
});

test("anything else is not a page: no catch-all, no look-alike IDs", () => {
  for (const p of ["/login", "/admin", "/users/abc", `/users/${ID}/edit`, `/users/${ID.toUpperCase()}`, "/users//x", "/metro/cities", "/users/../x", "/audit", "/audit/", "/audit/users/x", "/audit/metro"]) {
    assert.deepEqual(matchRoute(p), { name: "not-found" }, p);
  }
});

test("user pages need the platform role, the home page only a session", () => {
  assert.equal(roleOf(matchRoute("/users")), "ozid-admin");
  assert.equal(roleOf(matchRoute(`/users/${ID}`)), "ozid-admin");
  assert.equal(roleOf(matchRoute("/audit/users")), "ozid-admin");
  assert.equal(roleOf(matchRoute("/")), "");
});

test("only console pages can become a return target; everything else returns home", () => {
  assert.equal(returnPathOf("/users"), "/users");
  assert.equal(returnPathOf(`/users/${ID}`), `/users/${ID}`);
  assert.equal(returnPathOf("/audit/users"), "/audit/users");
  assert.equal(returnPathOf("/"), "/");
  for (const p of ["/nope", "//evil.example", "/users/x", "/users/..%2f", "https://evil.example/", ""]) assert.equal(returnPathOf(p), "/", p);
});

test("at most one lookup is used at a time", () => {
  assert.equal(lookupCount(EMPTY_FILTERS), 0);
  assert.equal(lookupCount({ ...EMPTY_FILTERS, email: "a@example.com" }), 1);
  assert.equal(lookupCount({ ...EMPTY_FILTERS, email: "a@example.com", nickname: "a" }), 2);
  assert.equal(lookupCount({ ...EMPTY_FILTERS, email: "  ", status: "active" }), 0);
});

test("the audit page takes an account only as a valid ID from the address", () => {
  assert.equal(userOfSearch(`?user=${ID}`), ID);
  for (const q of ["", "?user=", "?user=abc", `?user=${ID.toUpperCase()}`, "?user=a@example.com", `?other=${ID}`, `?user=${ID}x`]) assert.equal(userOfSearch(q), "", q);
  assert.equal(auditPathOf(ID), `/audit/users?user=${ID}`);
});

test("audit filters are checked like the server checks them, and say what is wrong", () => {
  assert.equal(auditFilterProblem(EMPTY_AUDIT_FILTERS), "");
  assert.equal(auditFilterProblem({ ...EMPTY_AUDIT_FILTERS, userId: ID, action: "admin.account.disable", mutationId: "key-1.a_b", from: "2026-01-01T00:00", to: "2026-01-02T00:00" }), "");
  for (const f of [
    { userId: "nope" },
    { operatorId: ID + "0" },
    { action: "Admin Disable" },
    { action: "-x" },
    { mutationId: "has space" },
    { mutationId: "x".repeat(65) },
    { from: "2026-01-02T00:00", to: "2026-01-01T00:00" },
    { from: "2026-01-01T00:00", to: "2026-01-01T00:00" },
  ])
    assert.notEqual(auditFilterProblem({ ...EMPTY_AUDIT_FILTERS, ...f }), "", JSON.stringify(f));
});
