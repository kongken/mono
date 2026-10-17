import test from "node:test";
import assert from "node:assert/strict";
import {
  hasFootprints, planChanges, planRestore, reasonLabel, snapshotToSaved,
} from "../src/lib/snapshots.ts";

import { createRegionCatalog } from "../src/lib/region-catalog.ts";
const syntheticCatalog = createRegionCatalog([{ code: "330000", cities: [{ code: "a", name: "a" }, { code: "b", name: "b" }] }]);
const green = "#4b937d";
const gold = "#e2af5c";
const visit = (code, { date = "2026-10-01", note = "", ...extra } = {}) => ({
  code, name: code, province: "330000", color: green, trips: [{ id: `legacy-${code}`, date, note }], ...extra,
});
const saved = (visits = [], provinces = {}) => ({ version: 2, visits, provinces });

test("snapshot data becomes the app's saved shape without server-only fields", () => {
  const wire = { code: "a", name: "a", province: "330000", color: green, updatedAt: { seconds: 1n } };
  const result = snapshotToSaved({
    schemaVersion: 2,
    visits: [wire],
    trips: [
      { id: "t2", regionCode: "a", date: "", note: "later", createdAt: {}, updatedAt: {} },
      { id: "t1", regionCode: "a", date: "2026-10-01", note: "", createdAt: {}, updatedAt: {} },
    ],
    provinceMarks: [{ province: "330000", color: gold, updatedAt: { seconds: 1n } }],
  }, syntheticCatalog);
  assert.deepEqual(result, saved([{ ...visit("a"), trips: [
    { id: "t1", date: "2026-10-01", note: "" },
    { id: "t2", date: "", note: "later" },
  ] }], { 330000: gold }));
});

test("a snapshot from before several trips per city becomes one legacy trip per city", () => {
  const result = snapshotToSaved({
    visits: [
      { code: "a", name: "a", province: "330000", color: green, date: "2026-10-01", note: "x" },
      { code: "b", name: "b", province: "330000", color: green, date: "", note: "" },
    ],
    provinceMarks: [],
  }, syntheticCatalog);
  assert.deepEqual(result, saved([visit("a", { note: "x" }), visit("b", { date: "" })]));
});

test("a snapshot city without trips stays a city with no trips", () => {
  const result = snapshotToSaved({
    schemaVersion: 2,
    visits: [{ code: "a", name: "a", province: "330000", color: green }],
    trips: [],
    provinceMarks: [],
  }, syntheticCatalog);
  assert.deepEqual(result.visits[0].trips, []);
});

test("restoring identical data changes nothing", () => {
  const s = saved([visit("a")], { 330000: green });
  const plan = planRestore(s, structuredClone(s));
  assert.equal(planChanges(plan), 0);
  assert.equal(plan.unchanged, 3, "the city, its trip and the mark");
});

test("a restore brings back, rewrites and removes records", () => {
  const snapshot = saved([visit("kept"), visit("lost"), visit("edited", { note: "old" }), visit("recolored")], { 330000: green, 110000: gold });
  const current = saved([visit("kept"), visit("edited", { note: "new" }), visit("later"), visit("recolored", { color: gold })], { 330000: gold, 310000: green });
  const plan = planRestore(current, snapshot);
  assert.deepEqual(plan, {
    addedVisits: 1, // lost
    changedVisits: 1, // recolored
    removedVisits: 1, // later
    addedTrips: 1, // lost's trip
    changedTrips: 1, // edited's note
    removedTrips: 1, // later's trip goes with its city
    addedMarks: 1, // 110000
    changedMarks: 1, // 330000 colour
    removedMarks: 1, // 310000
    unchanged: 4, // kept (city, trip), edited (city), recolored (trip)
  });
  assert.equal(planChanges(plan), 9);
});

test("restoring into an empty account only adds, and to an emptied one only removes", () => {
  const snapshot = saved([visit("a"), visit("b")], { 330000: green });
  const intoEmpty = planRestore(saved(), snapshot);
  assert.deepEqual([intoEmpty.addedVisits, intoEmpty.addedTrips, intoEmpty.addedMarks, intoEmpty.removedVisits, intoEmpty.removedMarks], [2, 2, 1, 0, 0]);
  const toEmpty = planRestore(snapshot, saved());
  assert.deepEqual([toEmpty.addedVisits, toEmpty.removedVisits, toEmpty.removedTrips, toEmpty.removedMarks], [0, 2, 2, 1]);
  assert.equal(toEmpty.unchanged, 0);
});

test("province marks and city visits are counted separately", () => {
  const plan = planRestore(saved([visit("a")], { 330000: green }), saved([visit("a")], {}));
  assert.deepEqual([plan.removedMarks, plan.removedVisits, plan.unchanged], [1, 0, 2]);
});

test("hasFootprints is true for either kind of record", () => {
  assert.equal(hasFootprints(saved()), false);
  assert.equal(hasFootprints(saved([visit("a")])), true);
  assert.equal(hasFootprints(saved([], { 330000: green })), true);
});

test("every reason has a label", () => {
  for (const reason of ["manual", "scheduled", "beforeMigration", "beforeRestore"]) {
    assert.ok(reasonLabel[reason]);
  }
});
