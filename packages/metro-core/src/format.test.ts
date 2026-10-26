import assert from "node:assert/strict";
import { test } from "node:test";
import { formatKm, lines } from "./format.ts";

test("formatKm and lines", () => {
  assert.equal(formatKm(100.9), "100.9 km");
  assert.equal(formatKm(0), "0 km");
  assert.equal(formatKm(1234.5678), "1,234.57 km");
  assert.deepEqual(lines(" a \n\n b\n"), ["a", "b"]);
});

// status.ts imports the generated enum, which Node's strip-types cannot load; the labels are asserted by the
// console's Playwright suite (the status column of the line list, apps/admin/tests/metro.spec.ts).
