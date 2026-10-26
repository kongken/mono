import assert from "node:assert/strict";
import { test } from "node:test";
import { auditFilterProblem, auditPathOf, EMPTY_AUDIT_FILTERS, EMPTY_FILTERS, lookupCount, matchRoute, METRO_SECTIONS, metroPathOf, returnPathOf, RETURN_PATHS_DOC, roleOf, userOfSearch } from "./routes.ts";
import { METRO_SECTION, sectionsFor } from "./sections.ts";

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
  for (const p of ["/login", "/admin", "/users/abc", `/users/${ID}/edit`, `/users/${ID.toUpperCase()}`, "/users//x", "/metro/cities/x", "/metro/Cities", "/metro//cities", "/metro/users", "/metro/../users", "/metro/cities?x=1", "/users/../x", "/audit", "/audit/", "/audit/users/x", "/audit/metro"]) {
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

test("the metro administration is /metro and one page per section, nothing else", () => {
  assert.deepEqual(matchRoute("/metro"), { name: "metro", section: "cities" });
  assert.deepEqual(matchRoute("/metro/"), { name: "metro", section: "cities" });
  for (const s of METRO_SECTIONS) {
    assert.deepEqual(matchRoute(`/metro/${s}`), { name: "metro", section: s });
    assert.deepEqual(matchRoute(`/metro/${s}/`), { name: "metro", section: s });
    assert.equal(metroPathOf(s), `/metro/${s}`);
  }
  assert.deepEqual([...METRO_SECTIONS], ["cities", "lines", "rolling-stocks", "topics", "import", "export", "audit", "tokens"]);
});

test("every metro page needs the metro role and no other role opens it", () => {
  for (const s of METRO_SECTIONS) assert.equal(roleOf(matchRoute(`/metro/${s}`)), "metro-admin", s);
  assert.equal(roleOf(matchRoute("/metro")), "metro-admin");
});

test("a metro page can be returned to; the return stays inside /metro/*", () => {
  for (const s of METRO_SECTIONS) assert.equal(returnPathOf(`/metro/${s}`), `/metro/${s}`);
  assert.equal(returnPathOf("/metro"), "/metro/cities");
  assert.equal(returnPathOf("/metro/nope"), "/");
  assert.equal(returnPathOf("/metro/cities/x"), "/");
  assert.match(RETURN_PATHS_DOC, /"\/metro\/\*"/);
});

test("the metro section is offered to the metro role, and only while metro is there", () => {
  assert.ok(sectionsFor(["metro-admin"]).includes(METRO_SECTION));
  assert.ok(!sectionsFor(["metro-admin"], false).includes(METRO_SECTION));
  assert.ok(!sectionsFor(["ozid-admin"]).includes(METRO_SECTION));
  assert.ok(!sectionsFor([]).includes(METRO_SECTION));
  assert.ok(sectionsFor(["ozid-admin"], false).length === 2);
});
