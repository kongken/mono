import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { parseSharePath, parseShareContent, SHARE_TOKEN } from "../src/lib/share.ts";
import { totalCities, visitedCityCount } from "../src/lib/statistics.ts";
import { nationalProvinceColors } from "../src/lib/travel.ts";
import { tripCount } from "../src/lib/trips.ts";

const token = "A".repeat(43);
const regions = JSON.parse(readFileSync("src/regions.json", "utf8"));
const geometry = JSON.parse(readFileSync("internal/share/assets/geometry.json", "utf8"));

test("share paths never fall back to the application", () => {
  assert.deepEqual(parseSharePath(`/s/${token}`), { kind: "share", token });
  assert.deepEqual(parseSharePath(`/s/${token}/`), { kind: "share", token });
  assert.deepEqual(parseSharePath(`/trip/s/${token}`, "/trip/"), { kind: "share", token });
  assert.deepEqual(parseSharePath(`/trip/s/${token}`, "/trip"), { kind: "share", token });
  for (const path of ["/", "/index.html", "/auth/callback/github", "/share", "/sx/abc", "/maps/100000.json"])
    assert.deepEqual(parseSharePath(path), { kind: "none" }, path);
  for (const path of [
    "/s", "/s/", "/s//", "/s/short", `/s/${token}x`, `/s/${token}/og.png`, `/s/${token}/extra/more`, `/s/${"%41".repeat(43)}`,
    `/s/${token.slice(1)}.`, "/s/../", "/s/ " + token.slice(1),
  ])
    assert.deepEqual(parseSharePath(path), { kind: "invalid" }, path);
  assert.deepEqual(parseSharePath(`/s/${token}`, "/trip/"), { kind: "none" }, "outside the base path");
  assert.ok(SHARE_TOKEN.test(token) && !SHARE_TOKEN.test("a b"));
});

const valid = () => ({
  v: 1,
  published_on: "2026-10-14",
  fields: { cities: true, dates: true, notes: true, photos: true },
  stats: { explored_provinces: 1, total_provinces: 34, cities: 1, total_cities: 391, trips: 1, by_province: [{ code: "330000", cities: 1, total: 11 }] },
  provinces: ["330000"],
  cities: [{ code: "330100", name: "杭州市", province: "330000", color: "#4b937d", trips: [{ date: "2026-05-01", note: "西湖", photos: [{ id: "pABCDEFGH", w: 10, h: 20 }] }] }],
});

test("public content is read strictly and only through the allow-list", () => {
  const parsed = parseShareContent({ ...valid(), user_id: "u1", username: "x" });
  assert.equal(parsed.cities[0].trips[0].photos[0].id, "pABCDEFGH");
  assert.ok(!("user_id" in parsed) && !("username" in parsed));
  const empty = parseShareContent({ ...valid(), fields: { cities: false }, cities: undefined, provinces: null, stats: { ...valid().stats, by_province: null } });
  assert.deepEqual([empty.cities, empty.provinces, empty.stats.by_province], [[], [], []]);
  const broken = [
    (c) => { c.v = 2; },
    (c) => { c.provinces = ["999999"]; },
    (c) => { c.cities[0].code = "000000"; },
    (c) => { c.cities[0].color = "red; background:url(x)"; },
    (c) => { c.cities[0].trips[0].photos[0].id = "../../x"; },
    (c) => { c.stats.cities = -1; },
    (c) => { c.cities = {}; },
  ];
  for (const mutate of broken) {
    const c = valid();
    mutate(c);
    assert.throws(() => parseShareContent(c));
  }
});

// scripts/fixtures/share-stats.json also drives the Go implementation (internal/share).
test("national statistics follow the shared fixtures", () => {
  const fixture = JSON.parse(readFileSync("scripts/fixtures/share-stats.json", "utf8"));
  assert.equal(totalCities, fixture.totalCities);
  assert.equal(regions.length, fixture.totalProvinces);
  for (const tc of fixture.cases) {
    const byCode = new Map();
    for (const c of tc.cities) {
      if (!byCode.has(c.province)) byCode.set(c.province, []);
      byCode.get(c.province).push(c);
    }
    const saved = {
      version: 2,
      provinces: Object.fromEntries(tc.marks.map((code) => [code, "#4b937d"])),
      visits: tc.cities.map((c) => ({
        code: c.code, name: c.code, province: c.province, color: "#4b937d",
        trips: Array.from({ length: c.trips }, (_, i) => ({ id: `t${i}`, date: "", note: "" })),
      })),
    };
    assert.equal(Object.keys(nationalProvinceColors(saved)).length, tc.expect.exploredProvinces, tc.name);
    assert.equal(visitedCityCount(saved), tc.expect.cities, tc.name);
    assert.equal(tripCount(saved), tc.expect.trips, tc.name);
    const perProvince = Object.fromEntries([...byCode].map(([province, cities]) => [province, new Set(cities.map((c) => c.code)).size]));
    assert.deepEqual(perProvince, tc.expect.byProvince, tc.name);
  }
});

test("the preview image and the page colour provinces with the same fixed palette", () => {
  const all = nationalProvinceColors({ version: 2, visits: [], provinces: Object.fromEntries(regions.map((r) => [r.code, "x"])) });
  assert.equal(geometry.provinces.length, 34);
  for (const p of geometry.provinces) assert.equal(p.color, all[p.code], p.code);
  // A province keeps its colour whichever others are explored.
  const some = nationalProvinceColors({ version: 2, visits: [], provinces: { 330000: "x", 530000: "x" } });
  for (const [code, color] of Object.entries(some)) assert.equal(color, all[code]);
});
