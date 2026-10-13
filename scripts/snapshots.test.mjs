import test from "node:test";
import assert from "node:assert/strict";
import {
  hasFootprints, planChanges, planRestore, reasonLabel, snapshotToSaved,
} from "../src/lib/snapshots.ts";

const green = "#4b937d";
const gold = "#e2af5c";
const visit = (code, extra = {}) => ({
  code, name: code, province: "330000", color: green, date: "2026-10-01", note: "", ...extra,
});
const saved = (visits = [], provinces = {}) => ({ version: 1, visits, provinces });

test("snapshot data becomes the app's saved shape without server-only fields", () => {
  const result = snapshotToSaved({
    visits: [{ ...visit("a"), updatedAt: { seconds: 1n } }],
    provinceMarks: [{ province: "330000", color: gold, updatedAt: { seconds: 1n } }],
  });
  assert.deepEqual(result, saved([visit("a")], { 330000: gold }));
});

test("restoring identical data changes nothing", () => {
  const s = saved([visit("a")], { 330000: green });
  const plan = planRestore(s, structuredClone(s));
  assert.equal(planChanges(plan), 0);
  assert.equal(plan.unchanged, 2);
});

test("a restore brings back, rewrites and removes records", () => {
  const snapshot = saved([visit("kept"), visit("lost"), visit("edited", { note: "old" })], { 330000: green, 110000: gold });
  const current = saved([visit("kept"), visit("edited", { note: "new" }), visit("later")], { 330000: gold, 310000: green });
  const plan = planRestore(current, snapshot);
  assert.deepEqual(plan, {
    addedVisits: 1, // lost
    changedVisits: 1, // edited
    removedVisits: 1, // later
    addedMarks: 1, // 110000
    changedMarks: 1, // 330000 colour
    removedMarks: 1, // 310000
    unchanged: 1, // kept
  });
  assert.equal(planChanges(plan), 6);
});

test("restoring into an empty account only adds, and to an emptied one only removes", () => {
  const snapshot = saved([visit("a"), visit("b")], { 330000: green });
  const intoEmpty = planRestore(saved(), snapshot);
  assert.deepEqual([intoEmpty.addedVisits, intoEmpty.addedMarks, intoEmpty.removedVisits, intoEmpty.removedMarks], [2, 1, 0, 0]);
  const toEmpty = planRestore(snapshot, saved());
  assert.deepEqual([toEmpty.addedVisits, toEmpty.removedVisits, toEmpty.removedMarks], [0, 2, 1]);
  assert.equal(toEmpty.unchanged, 0);
});

test("province marks and city visits are counted separately", () => {
  const plan = planRestore(saved([visit("a")], { 330000: green }), saved([visit("a")], {}));
  assert.deepEqual([plan.removedMarks, plan.removedVisits, plan.unchanged], [1, 0, 1]);
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
