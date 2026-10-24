import assert from "node:assert/strict";
import { test } from "node:test";
import { loginPath, safeNext } from "./next.ts";

test("only same-origin paths are returned to after signing in", () => {
  assert.equal(safeNext("/cities/shanghai"), "/cities/shanghai");
  assert.equal(safeNext("/me?tab=rides"), "/me?tab=rides");
  for (const bad of [null, undefined, "", "me", "https://evil.example/", "//evil.example/", "/\\evil.example", "/login", "/login?next=/me", "/account", "/account/verify-email", "/a\nb"])
    assert.equal(safeNext(bad), "/", String(bad));
});

test("the sign-in address carries the page to come back to", () => {
  assert.equal(loginPath("/"), "/login");
  assert.equal(loginPath("/me"), "/login?next=%2Fme");
  assert.equal(loginPath("/login"), "/login");
});
