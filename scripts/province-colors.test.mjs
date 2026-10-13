import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { provinceNeighbors } from "./prepare-province-neighbors.mjs";
import { colors, nationalProvinceColors } from "../src/lib/travel.ts";

const graph = JSON.parse(readFileSync("src/province-neighbors.json", "utf8"));
const codes = Object.keys(graph);
const state = (selected) => ({ version: 1, visits: [], provinces: Object.fromEntries(selected.map(code => [code, colors[0].value])) });

test("province adjacency matches bundled boundaries, is symmetric and includes small regions", () => {
  const map = JSON.parse(readFileSync("public/maps/100000.json", "utf8"));
  assert.deepEqual(graph, provinceNeighbors(map));
  assert.equal(codes.length, 34);
  for (const code of codes) {
    assert.ok(!graph[code].includes(code));
    for (const neighbor of graph[code]) assert.ok(graph[neighbor].includes(code));
  }
  for (const [a, b] of [["110000", "120000"], ["320000", "330000"], ["340000", "370000"], ["440000", "810000"], ["440000", "820000"]]) assert.ok(graph[a].includes(b));
  assert.deepEqual(graph["460000"], []);
  assert.deepEqual(graph["710000"], []);
});

test("all neighboring explored provinces have different colors", () => {
  const assigned = nationalProvinceColors(state(codes));
  for (const code of codes) {
    assert.ok(colors.some(color => color.value === assigned[code]));
    for (const neighbor of graph[code]) assert.notEqual(assigned[code], assigned[neighbor], `${code} / ${neighbor}`);
  }
});

test("province colors stay stable across exploration subsets, order and city edits", () => {
  const baseline = nationalProvinceColors(state(codes));
  let seed = 42;
  for (let trial = 0; trial < 100; trial++) {
    const subset = codes.filter(() => { seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0; return seed % 3 !== 0; });
    const saved = state(subset.reverse());
    saved.visits = subset.map(code => ({ code: `city-${code}`, province: code, color: colors[4].value, name: "城市", date: "", note: "保留备注" }));
    const before = structuredClone(saved);
    const assigned = nationalProvinceColors(saved);
    assert.equal(Object.keys(assigned).length, subset.length);
    for (const code of subset) assert.equal(assigned[code], baseline[code]);
    assert.deepEqual(saved, before);
  }
  assert.deepEqual(nationalProvinceColors(state([])), {});
});
