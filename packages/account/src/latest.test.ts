import test from "node:test";
import assert from "node:assert/strict";
import { createEpoch } from "./latest.ts";

test("only the newest read is current", () => {
  const epoch = createEpoch();
  const first = epoch.begin();
  assert.equal(first(), true);
  const second = epoch.begin();
  assert.equal(first(), false, "a newer read supersedes the older one");
  assert.equal(second(), true);
});

test("invalidate makes every started read stale, and later reads are valid again", () => {
  const epoch = createEpoch();
  const a = epoch.begin();
  const b = epoch.begin();
  epoch.invalidate();
  assert.equal(a(), false);
  assert.equal(b(), false);
  const c = epoch.begin();
  assert.equal(c(), true);
});

test("a read stays current while nothing else happens", () => {
  const epoch = createEpoch();
  const read = epoch.begin();
  assert.equal(read(), true);
  assert.equal(read(), true);
});
