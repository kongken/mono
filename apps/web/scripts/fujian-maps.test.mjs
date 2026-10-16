import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { geoContains } from "d3-geo";
import { normalizeMap } from "../src/lib/maps.ts";
import { wgs84ToGcj02 } from "../src/lib/coords.ts";
import { splitFujianRegions } from "./split-fujian-regions.mjs";

const load = code => JSON.parse(readFileSync(`public/maps/${code}.json`, "utf8"));

test("Fujian and Taiwan boundaries follow the same ownership as the index", () => {
  const fujian = load("350000"), taiwan = load("710000");
  assert.equal(fujian.features.length, 11);
  assert.equal(taiwan.features.length, 20);
  for (const code of ["tw-09020", "tw-09007"]) {
    assert.ok(fujian.features.some(f => f.properties.adcode === code));
    assert.ok(!taiwan.features.some(f => f.properties.adcode === code));
  }
});

test("Kinmen and Matsu are not also clickable as Quanzhou or Fuzhou", () => {
  const map = normalizeMap(load("350000"));
  for (const [code, point] of [["tw-09020", [118.385, 24.45]], ["tw-09007", [119.994, 26.215]]]) {
    assert.deepEqual(map.features.filter(f => geoContains(f, point)).map(f => String(f.properties.adcode)), [code]);
  }
});

test("download split removes duplicate DataV island components but keeps the mainland", () => {
  const fujian = load("350000"), taiwan = load("710000");
  const islands = fujian.features.filter(f => String(f.properties.adcode).startsWith("tw-"));
  const mainland = fujian.features.filter(f => !String(f.properties.adcode).startsWith("tw-"));
  for (const [code, [lng, lat]] of [["350500", [118.385, 24.45]], ["350100", [119.994, 26.215]]]) {
    const feature = mainland.find(f => String(f.properties.adcode) === code);
    const ring = [[lng - .001, lat - .001], [lng + .001, lat - .001], [lng, lat + .001], [lng - .001, lat - .001]].map(p => wgs84ToGcj02(...p));
    feature.geometry.coordinates.push([ring]);
  }
  const result = splitFujianRegions({ ...taiwan, features: [...taiwan.features, ...islands] }, { ...fujian, features: mainland });
  for (const [code, point] of [["tw-09020", [118.385, 24.45]], ["tw-09007", [119.994, 26.215]]]) {
    assert.deepEqual(normalizeMap(result.fujian).features.filter(f => geoContains(f, point)).map(f => String(f.properties.adcode)), [code]);
  }
  for (const code of ["350100", "350500"]) {
    assert.equal(result.fujian.features.find(f => String(f.properties.adcode) === code).geometry.coordinates.length, load("350000").features.find(f => String(f.properties.adcode) === code).geometry.coordinates.length);
  }
});

test("splitting downloaded boundaries is idempotent and preserves supplemental geometry", () => {
  const fujian = load("350000"), taiwan = load("710000");
  const islands = fujian.features.filter(f => String(f.properties.adcode).startsWith("tw-"));
  assert.equal(islands.length, 2);
  const combined = { ...taiwan, features: [...taiwan.features, ...islands] };
  const result = splitFujianRegions(combined, fujian);
  assert.deepEqual(result, { fujian, taiwan });
  assert.deepEqual(splitFujianRegions(result.taiwan, result.fujian), result);
});
