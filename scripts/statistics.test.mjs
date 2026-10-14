import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import * as statistics from "../src/lib/statistics.ts";
import { toggleVisit } from "../src/lib/travel.ts";

const regions = JSON.parse(readFileSync("src/regions.json", "utf8"));
const empty = () => ({ version: 2, visits: [], provinces: {} });

test("national city total collapses the six district-level destinations", () => {
  assert.equal(statistics.totalCities, 391);
});

test("multiple districts count as one city nationally, but remain separate visits", () => {
  let saved = empty();
  for (const code of ["110000", "120000", "310000", "500000", "810000", "820000"]) {
    const region = regions.find(p => p.code === code);
    for (const city of region.cities.slice(0, 2)) saved = toggleVisit(saved, code, city, "");
  }
  assert.equal(saved.visits.length, 12);
  assert.equal(statistics.visitedCityCount(saved), 6);
  const beijing = regions.find(p => p.code === "110000");
  saved = toggleVisit(saved, beijing.code, beijing.cities[0], "");
  assert.equal(statistics.visitedCityCount(saved), 6);
  saved = toggleVisit(saved, beijing.code, beijing.cities[1], "");
  assert.equal(statistics.visitedCityCount(saved), 5);
});

test("ordinary destinations stay distinct and province marks alone do not count", () => {
  let saved = { ...empty(), provinces: { "110000": "#4b937d" } };
  const region = regions.find(p => p.code === "330000");
  for (const city of region.cities.slice(0, 2)) saved = toggleVisit(saved, region.code, city, "");
  assert.equal(statistics.visitedCityCount(saved), 2);
  assert.equal(statistics.visitedCityCount(empty()), 0);
});

test("marking every recordable region reaches exactly 100 percent", () => {
  const saved = { ...empty(), visits: regions.flatMap(p => p.cities.map(c => ({ ...c, province: p.code, trips: [] }))) };
  assert.equal(statistics.visitedCityCount(saved), statistics.totalCities);
  assert.equal(statistics.percentage(statistics.visitedCityCount(saved), statistics.totalCities), "100.0%");
});
