import test from "node:test";
import assert from "node:assert/strict";
import { SyncEngine, applyOp, diffSaved } from "../src/lib/sync.ts";

const green = "#4b937d";
const visit = (code, extra = {}) => ({
  code, name: code, province: "330000", color: green, date: "", note: "", ...extra,
});
const saved = (visits = [], provinces = {}) => ({ version: 1, visits, provinces });

test("identical states produce no operations", () => {
  const s = saved([visit("a")], { 110000: green });
  assert.deepEqual(diffSaved(s, structuredClone(s)), []);
});

test("diff reports adds, edits, removals for visits and marks", () => {
  const base = saved([visit("a"), visit("b")], { 1: green, 2: green });
  const next = saved([visit("b", { note: "x" }), visit("c")], { 2: "#fff", 3: green });
  const ops = diffSaved(base, next);
  assert.deepEqual(ops.map((o) => o.type).sort(), [
    "deleteMark", "deleteVisit", "upsertMark", "upsertMark", "upsertVisit", "upsertVisit",
  ]);
  // Applying every operation yields the target state (order of visits aside).
  const result = ops.reduce(applyOp, base);
  assert.deepEqual([...result.visits].sort((x, y) => x.code.localeCompare(y.code)), next.visits);
  assert.deepEqual(result.provinces, next.provinces);
});

test("applyOp does not mutate its input", () => {
  const base = saved([visit("a")], { 1: green });
  const copy = structuredClone(base);
  applyOp(base, { type: "deleteVisit", code: "a" });
  applyOp(base, { type: "deleteMark", province: "1" });
  assert.deepEqual(base, copy);
});

function fakeApi(failOn) {
  const calls = [];
  let revision = 5;
  return {
    calls,
    get: async () => { throw new Error("unused"); },
    apply: async (op) => {
      calls.push(op);
      if (failOn?.(op, calls.length)) throw new Error("boom");
      return ++revision;
    },
  };
}

test("engine sends only the difference and tracks the revision", async () => {
  const api = fakeApi();
  const engine = new SyncEngine(api, { saved: saved([visit("a")]), revision: 5 });
  engine.push(saved([visit("a"), visit("b")]));
  await engine.drain();
  assert.deepEqual(api.calls.map((c) => c.type), ["upsertVisit"]);
  assert.equal(api.calls[0].visit.code, "b");
  assert.equal(engine.confirmed.revision, 6);
  assert.equal(engine.status, "idle");
  assert.equal(engine.pending, false);
});

test("pushes during a send are folded into the next diff", async () => {
  const api = fakeApi();
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 });
  engine.push(saved([visit("a")]));
  engine.push(saved([visit("a"), visit("b")]));
  engine.push(saved([visit("b")]));
  await engine.drain();
  assert.deepEqual(engine.confirmed.saved, saved([visit("b")]));
  // "a" is never worth sending once it was removed again before it went out.
  assert.ok(!api.calls.some((c) => c.type === "upsertVisit" && c.visit.code === "a" && api.calls.indexOf(c) > 0));
});

test("a failure stops the engine and keeps the last confirmed state", async () => {
  const api = fakeApi((_, n) => n === 2);
  const errors = [];
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 }, { onError: (e) => errors.push(e) });
  engine.push(saved([visit("a"), visit("b")]));
  await engine.drain();
  assert.equal(engine.status, "error");
  assert.equal(errors.length, 1);
  assert.equal(engine.confirmed.saved.visits.length, 1);
  assert.equal(engine.pending, true);
  engine.reset({ saved: saved([visit("a")]), revision: 9 });
  assert.equal(engine.status, "idle");
  assert.equal(engine.pending, false);
});

test("an engine built from a snapshot never overwrites unseen records", async () => {
  const api = fakeApi();
  const engine = new SyncEngine(api, { saved: saved([visit("server")]), revision: 3 });
  engine.push(saved([visit("server"), visit("new")]));
  await engine.drain();
  assert.ok(!api.calls.some((c) => c.type === "deleteVisit"));
});
