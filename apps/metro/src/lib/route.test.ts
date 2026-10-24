import assert from "node:assert/strict";
import { test } from "node:test";
import { matchRoute } from "./route.ts";

test("matches the public and admin routes", () => {
  assert.deepEqual(matchRoute("/"), { name: "home" });
  assert.deepEqual(matchRoute("/cities/shanghai"), { name: "city", slug: "shanghai" });
  assert.deepEqual(matchRoute("/cities/shanghai/"), { name: "city", slug: "shanghai" });
  assert.deepEqual(matchRoute("/topics/six-cars"), { name: "topic", slug: "six-cars" });
  assert.deepEqual(matchRoute("/u/alice"), { name: "profile", handle: "alice" });
  assert.deepEqual(matchRoute("/me"), { name: "me" });
  assert.deepEqual(matchRoute("/login"), { name: "login" });
  assert.deepEqual(matchRoute("/account"), { name: "account" });
  // The mail landing pages are rendered before the router starts, so they are not routes.
  assert.deepEqual(matchRoute("/account/verify-email"), { name: "notfound" });
  assert.deepEqual(matchRoute("/admin"), { name: "admin", section: "cities" });
  assert.deepEqual(matchRoute("/admin/tokens"), { name: "admin", section: "tokens" });
});

test("decodes slugs and rejects everything else", () => {
  assert.deepEqual(matchRoute("/cities/%E4%B8%8A%E6%B5%B7"), { name: "city", slug: "上海" });
  assert.deepEqual(matchRoute("/cities/%E0%A4%A"), { name: "notfound" });
  assert.deepEqual(matchRoute("/admin/nope"), { name: "notfound" });
  assert.deepEqual(matchRoute("/admin/cities/extra"), { name: "notfound" });
  assert.deepEqual(matchRoute("/cities"), { name: "notfound" });
  assert.deepEqual(matchRoute("/me/more"), { name: "notfound" });
});
