import test from "node:test";
import assert from "node:assert/strict";
import {
  fingerprint, isNoop, markKey, migratedSaved, planMigration, summarize, visitKey,
} from "../src/lib/migration.ts";
import { isMigrated, markMigrated, migratedKey } from "../src/lib/session.ts";

const green = "#4b937d";
const gold = "#e2af5c";
const visit = (code, extra = {}) => ({
  code, name: code, province: "330000", color: green, date: "2026-10-01", note: "", ...extra,
});
const saved = (visits = [], provinces = {}) => ({ version: 1, visits, provinces });

test("empty cloud: everything local is uploaded, nothing conflicts", () => {
  const local = saved([visit("a"), visit("b")], { 330000: green });
  const plan = planMigration(local, saved());
  assert.equal(plan.newVisits.length, 2);
  assert.equal(plan.newMarks.length, 1);
  assert.deepEqual(plan.conflicts, []);
  assert.deepEqual(migratedSaved(saved(), plan, {}), local);
});

test("identical data is a no-op", () => {
  const s = saved([visit("a")], { 330000: green });
  const plan = planMigration(s, structuredClone(s));
  assert.ok(isNoop(plan));
  assert.equal(plan.same, 2);
  assert.deepEqual(migratedSaved(s, plan, {}), s);
});

test("cloud-only records are kept and never deleted", () => {
  const cloud = saved([visit("cloud")], { 110000: gold });
  const plan = planMigration(saved([visit("local")]), cloud);
  assert.equal(plan.cloudOnly, 2);
  const result = migratedSaved(cloud, plan, {});
  assert.deepEqual(result.visits.map((v) => v.code), ["cloud", "local"]);
  assert.deepEqual(result.provinces, { 110000: gold });
});

test("same city with different content is a conflict; cloud wins unless chosen", () => {
  const local = saved([visit("a", { color: gold, note: "local" })]);
  const cloud = saved([visit("a", { note: "cloud" })]);
  const plan = planMigration(local, cloud);
  assert.equal(plan.conflicts.length, 1);
  assert.equal(plan.conflicts[0].key, visitKey("a"));
  assert.deepEqual(migratedSaved(cloud, plan, {}), cloud);
  assert.deepEqual(migratedSaved(cloud, plan, { [visitKey("a")]: "cloud" }), cloud);
  const chosen = migratedSaved(cloud, plan, { [visitKey("a")]: "local" });
  assert.equal(chosen.visits.length, 1, "no duplicate record");
  assert.equal(chosen.visits[0].note, "local");
});

test("province mark colour conflicts follow their own choice and unions otherwise", () => {
  const local = saved([], { 330000: gold, 110000: green });
  const cloud = saved([], { 330000: green, 310000: green });
  const plan = planMigration(local, cloud);
  assert.deepEqual(plan.conflicts.map((c) => c.key), [markKey("330000")]);
  const keepCloud = migratedSaved(cloud, plan, {});
  assert.deepEqual(keepCloud.provinces, { 330000: green, 310000: green, 110000: green });
  const useLocal = migratedSaved(cloud, plan, { [markKey("330000")]: "local" });
  assert.equal(useLocal.provinces[330000], gold);
});

test("mixed choices are applied per record and counted in the summary", () => {
  const local = saved([visit("a", { note: "L" }), visit("b", { note: "L" }), visit("c")]);
  const cloud = saved([visit("a"), visit("b")]);
  const plan = planMigration(local, cloud);
  const choices = { [visitKey("a")]: "local", [visitKey("b")]: "cloud" };
  const result = migratedSaved(cloud, plan, choices);
  const by = Object.fromEntries(result.visits.map((v) => [v.code, v.note]));
  assert.deepEqual(by, { a: "L", b: "", c: "" });
  assert.deepEqual(summarize(plan, choices), { added: 1, overwritten: 1, keptCloud: 1, same: 0, cloudOnly: 0 });
});

test("planning and migrating never mutate their inputs", () => {
  const local = saved([visit("a", { note: "L" }), visit("z")]);
  const cloud = saved([visit("a")]);
  const l = structuredClone(local), c = structuredClone(cloud);
  const plan = planMigration(local, cloud);
  migratedSaved(cloud, plan, { [visitKey("a")]: "local" });
  assert.deepEqual(local, l);
  assert.deepEqual(cloud, c);
});

test("fingerprint ignores order and changes with content", () => {
  const a = saved([visit("a"), visit("b")], { 1: green, 2: gold });
  const b = saved([visit("b"), visit("a")], { 2: gold, 1: green });
  assert.equal(fingerprint(a), fingerprint(b));
  assert.notEqual(fingerprint(a), fingerprint(saved([visit("a"), visit("b", { note: "x" })], { 1: green, 2: gold })));
});

test("migrated marker is per account and tied to the local data", () => {
  const data = new Map();
  const store = { getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v) };
  const fp = fingerprint(saved([visit("a")]));
  assert.equal(isMigrated(store, "u1", fp), false);
  assert.ok(markMigrated(store, "u1", fp));
  assert.equal(isMigrated(store, "u1", fp), true);
  assert.equal(isMigrated(store, "u2", fp), false);
  assert.equal(isMigrated(store, "u1", fingerprint(saved([visit("a"), visit("b")]))), false);
  assert.equal(data.has(migratedKey("u1")), true);
  assert.equal(markMigrated({ setItem() { throw new Error("full"); } }, "u1", fp), false);
});

import { SyncEngine, MAX_BATCH_OPS, applyOp, RevisionChanged } from "../src/lib/sync.ts";

test("a whole migration goes out as one atomic batch and survives a lost reply", async () => {
  assert.ok(MAX_BATCH_OPS >= 531, "must fit every city and province mark");
  const local = saved(
    Array.from({ length: 497 }, (_, i) => visit(String(100000 + i))),
    Object.fromEntries(Array.from({ length: 34 }, (_, i) => [String(10 + i), green])),
  );
  let server = saved(), revision = 0, lost = true;
  const batches = [];
  const receipts = new Map();
  const api = {
    get: async () => ({ saved: server, revision }),
    apply: async () => { throw new Error("legacy path"); },
    applyBatch: async (ops, base, id) => {
      if (receipts.has(id)) return receipts.get(id);
      if (base !== revision) throw new RevisionChanged();
      batches.push(ops.length);
      for (const op of ops) { server = applyOp(server, op); revision++; }
      receipts.set(id, revision);
      if (lost) { lost = false; throw new Error("reply lost"); }
      return revision;
    },
  };
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 });
  engine.push(migratedSaved(saved(), planMigration(local, saved()), {}));
  await engine.drain();
  assert.equal(engine.status, "error");
  engine.retry();
  await engine.drain();
  assert.equal(engine.status, "idle");
  assert.deepEqual(batches, [531], "one batch, applied once");
  assert.equal(server.visits.length, 497);
  assert.equal(Object.keys(server.provinces).length, 34);
});
