import assert from "node:assert/strict";
import { test } from "node:test";
import { formatKm, httpsOnly, lines, parseYear, socialLink, yearOf } from "./format.ts";

test("httpsOnly keeps https addresses and drops everything else", () => {
  assert.equal(httpsOnly("https://example.com/a.svg"), "https://example.com/a.svg");
  for (const bad of ["http://example.com/a.svg", "javascript:alert(1)", "data:image/svg+xml,<svg/>", "/relative.svg", "https://", "", undefined]) {
    assert.equal(httpsOnly(bad), undefined, String(bad));
  }
});

test("socialLink builds profile links from names and accepts https links only", () => {
  assert.equal(socialLink("github", "@alice"), "https://github.com/alice");
  assert.equal(socialLink("github", "https://github.com/alice"), "https://github.com/alice");
  assert.equal(socialLink("github", "javascript:alert(1)"), undefined);
  assert.equal(socialLink("github", "a/b"), undefined);
  assert.equal(socialLink("github", "  "), undefined);
  assert.equal(socialLink("unknown", "alice"), undefined);
});

test("parseYear and yearOf", () => {
  assert.equal(parseYear(""), undefined);
  assert.equal(parseYear(" 2019 "), 2019);
  assert.ok(Number.isNaN(parseYear("1800")));
  assert.ok(Number.isNaN(parseYear("abc")));
  assert.ok(Number.isNaN(parseYear(String(new Date().getFullYear() + 1))));
  assert.equal(yearOf("2019-05-01"), "2019");
  assert.equal(yearOf(""), "");
});

test("formatKm and lines", () => {
  assert.equal(formatKm(100.9), "100.9 km");
  assert.equal(formatKm(0), "0 km");
  assert.equal(formatKm(1234.5678), "1,234.57 km");
  assert.deepEqual(lines(" a \n\n b\n"), ["a", "b"]);
});
