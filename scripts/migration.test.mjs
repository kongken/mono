import test from "node:test";
import assert from "node:assert/strict";
import {
  fingerprint, isNoop, legacyFingerprint, markKey, migratedSaved, planMigration, summarize, tripKey, visitKey,
} from "../src/lib/migration.ts";
import { isMigrated, markMigrated, migratedKey } from "../src/lib/session.ts";

const green = "#4b937d";
const gold = "#e2af5c";
// A city with its single legacy trip, which is what every version 1 record becomes.
const visit = (code, { date = "2026-10-01", note = "", ...extra } = {}) => ({
  code, name: code, province: "330000", color: green, trips: [{ id: `legacy-${code}`, date, note }], ...extra,
});
const saved = (visits = [], provinces = {}) => ({ version: 2, visits, provinces });

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
  assert.equal(plan.same, 3, "the city, its trip and the mark");
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

test("same city or trip with different content is a conflict; cloud wins unless chosen", () => {
  const local = saved([visit("a", { color: gold, note: "local" })]);
  const cloud = saved([visit("a", { note: "cloud" })]);
  const plan = planMigration(local, cloud);
  assert.deepEqual(plan.conflicts.map((c) => c.key), [visitKey("a"), tripKey("legacy-a")]);
  assert.deepEqual(migratedSaved(cloud, plan, {}), cloud);
  assert.deepEqual(migratedSaved(cloud, plan, { [visitKey("a")]: "cloud", [tripKey("legacy-a")]: "cloud" }), cloud);
  const chosen = migratedSaved(cloud, plan, { [visitKey("a")]: "local", [tripKey("legacy-a")]: "local" });
  assert.equal(chosen.visits.length, 1, "no duplicate record");
  assert.equal(chosen.visits[0].trips.length, 1, "no duplicate trip");
  assert.equal(chosen.visits[0].trips[0].note, "local");
  assert.equal(chosen.visits[0].color, gold);
  // The city and the trip are chosen separately.
  const colorOnly = migratedSaved(cloud, plan, { [visitKey("a")]: "local" });
  assert.equal(colorOnly.visits[0].color, gold);
  assert.equal(colorOnly.visits[0].trips[0].note, "cloud");
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
  const trip = (id) => tripKey(`legacy-${id}`);
  const choices = { [trip("a")]: "local", [trip("b")]: "cloud" };
  const result = migratedSaved(cloud, plan, choices);
  const by = Object.fromEntries(result.visits.map((v) => [v.code, v.trips[0].note]));
  assert.deepEqual(by, { a: "L", b: "", c: "" });
  assert.deepEqual(summarize(plan, choices), { added: 1, addedTrips: 1, overwritten: 1, keptCloud: 1, remapped: 0, same: 2, cloudOnly: 0 });
});

test("trips recorded on only one side are kept and counted", () => {
  const local = saved([{ ...visit("a"), trips: [
    { id: "legacy-a", date: "2026-10-01", note: "" },
    { id: "t-local", date: "2026-11-01", note: "本机" },
  ] }]);
  const cloud = saved([{ ...visit("a"), trips: [
    { id: "legacy-a", date: "2026-10-01", note: "" },
    { id: "t-cloud", date: "2026-12-01", note: "云端" },
  ] }]);
  const plan = planMigration(local, cloud);
  assert.deepEqual(plan.newTrips.map((t) => t.trip.id), ["t-local"]);
  assert.equal(plan.cloudOnly, 1);
  assert.equal(plan.conflicts.length, 0);
  const result = migratedSaved(cloud, plan, {});
  assert.deepEqual(result.visits[0].trips.map((t) => t.id), ["legacy-a", "t-cloud", "t-local"]);
});

test("a local trip equal to a cloud trip with another ID is the same trip, not a new one", () => {
  const local = saved([{ ...visit("a"), trips: [{ id: "t-device", date: "2026-10-01", note: "西湖" }] }]);
  const cloud = saved([{ ...visit("a"), trips: [{ id: "t-other", date: "2026-10-01", note: "西湖" }] }]);
  const plan = planMigration(local, cloud);
  assert.equal(plan.newTrips.length, 0);
  assert.ok(isNoop(plan));
  assert.deepEqual(migratedSaved(cloud, plan, {}), cloud);
});

test("an older fingerprint still recognises data a version 1 device already migrated", () => {
  const data = saved([visit("a", { note: "x" }), visit("b")], { 330000: green });
  // Computed with the formula version 1 used, over { code, name, province, color, date, note } records.
  const text = JSON.stringify([
    [
      { code: "a", name: "a", province: "330000", color: green, date: "2026-10-01", note: "x" },
      { code: "b", name: "b", province: "330000", color: green, date: "2026-10-01", note: "" },
    ],
    [["330000", green]],
  ]);
  let h = 5381;
  for (let i = 0; i < text.length; i++) h = ((h << 5) + h + text.charCodeAt(i)) | 0;
  const old = `2:1:${(h >>> 0).toString(36)}`;
  assert.equal(legacyFingerprint(data), old);
  // Data with a second trip never existed before, so it has no old fingerprint.
  assert.equal(legacyFingerprint(saved([{ ...visit("a"), trips: [...visit("a").trips, { id: "t2", date: "", note: "" }] }])), null);
  const store = new Map();
  const s = { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => void store.set(k, v) };
  markMigrated(s, "u1", old);
  assert.ok(isMigrated(s, "u1", legacyFingerprint(data)));
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
  assert.match(fp, /^v2:/);
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
  assert.ok(MAX_BATCH_OPS >= 1028, "must fit every city with a trip and every province mark");
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
  assert.deepEqual(batches, [1028], "one batch (497 cities, their trips and 34 marks), applied once");
  assert.equal(server.visits.length, 497);
  assert.equal(Object.keys(server.provinces).length, 34);
});

// ---- trip IDs are unique per account (F5 review item 11) ----
import { migrationFits, migrationOps, remappedTripId, sameSaved } from "../src/lib/migration.ts";
import { validateSaved } from "../src/lib/persistence.ts";
import { diffSaved } from "../src/lib/sync.ts";

const cityWith = (code, trips, extra = {}) => ({ code, name: code, province: "330000", color: green, trips, ...extra });
const tripOf = (id, date = "", note = "") => ({ id, date, note });
const allTripIds = (s) => s.visits.flatMap((v) => v.trips.map((t) => t.id));

test("a local trip whose ID belongs to another cloud city is uploaded under a new ID, losing nothing", () => {
  const cloud = saved([cityWith("a", [tripOf("same-id", "2026-01-01", "cloud A")])]);
  const local = saved([cityWith("b", [tripOf("same-id", "2026-02-02", "local B"), tripOf("other", "", "x")])]);
  const plan = planMigration(local, cloud);
  assert.equal(plan.remapped.length, 1);
  assert.equal(plan.remapped[0].from, "same-id");
  assert.match(plan.remapped[0].to, /^same-id-m-b$/);
  const target = migratedSaved(cloud, plan, {});
  const ids = allTripIds(target);
  assert.equal(new Set(ids).size, ids.length, "every ID exists once in the account");
  assert.deepEqual(target.visits.find((v) => v.code === "a").trips, [tripOf("same-id", "2026-01-01", "cloud A")]);
  const b = target.visits.find((v) => v.code === "b").trips;
  assert.deepEqual(b.map((t) => [t.date, t.note]).sort(), [["", "x"], ["2026-02-02", "local B"]]);
  // The preview and the final result agree, and the target is something the backend accepts.
  assert.equal(summarize(plan, {}).addedTrips, 2);
  assert.equal(summarize(plan, {}).remapped, 1);
  assert.doesNotThrow(() => validateSaved(target, [{ code: "330000", cities: [{ code: "a", name: "a" }, { code: "b", name: "b" }] }]));
});

test("a colliding trip in a city the cloud also has is added as a new trip under a new ID", () => {
  const cloud = saved([cityWith("a", [tripOf("x1", "2026-01-01", "")]), cityWith("b", [tripOf("shared", "2026-03-03", "B cloud")])]);
  const local = saved([cityWith("a", [tripOf("x1", "2026-01-01", ""), tripOf("shared", "2026-04-04", "A local")])]);
  const plan = planMigration(local, cloud);
  assert.equal(plan.newTrips.length, 1);
  assert.notEqual(plan.newTrips[0].trip.id, "shared");
  assert.deepEqual([plan.newTrips[0].trip.date, plan.newTrips[0].trip.note], ["2026-04-04", "A local"]);
  const target = migratedSaved(cloud, plan, {});
  assert.equal(new Set(allTripIds(target)).size, allTripIds(target).length);
  assert.equal(target.visits.find((v) => v.code === "b").trips[0].note, "B cloud");
});

test("remapping is stable, avoids IDs on both sides, and the plan can be recomputed to the same result", () => {
  const cloud = saved([cityWith("a", [tripOf("dup", "2026-01-01", ""), tripOf("dup-m-b", "2026-01-02", "")])]);
  const local = saved([cityWith("b", [tripOf("dup", "2026-05-05", "mine")])]);
  const first = planMigration(local, cloud);
  const second = planMigration(local, cloud);
  assert.deepEqual(first.remapped, second.remapped);
  assert.equal(first.remapped[0].to, "dup-m-b-2", "the first candidate is taken by the cloud");
  assert.equal(remappedTripId("x".repeat(64), "tw-09020", new Set()).length <= 64, true);
});

test("the same ID in the same city is the same record, never remapped", () => {
  const cloud = saved([cityWith("a", [tripOf("t1", "2026-01-01", "cloud")])]);
  const local = saved([cityWith("a", [tripOf("t1", "2026-01-01", "local")])]);
  const plan = planMigration(local, cloud);
  assert.deepEqual(plan.remapped, []);
  assert.equal(plan.conflicts.length, 1);
});

// ---- one migration is one atomic batch (F5 review item 1) ----

test("the operation count of a migration is exactly what is sent, and the limit is the batch size", () => {
  const cloud = saved([cityWith("a", [tripOf("c1", "2026-01-01", "cloud")])]);
  const trips = Array.from({ length: 10 }, (_, i) => tripOf(`n${i}`, "2026-01-01", `${i}`));
  const local = saved([cityWith("a", [tripOf("c1", "2026-01-01", "local"), ...trips]), cityWith("b", [tripOf("b1")])], { 330000: green });
  const plan = planMigration(local, cloud);
  const count = migrationOps(cloud, plan, { [tripKey("c1")]: "local" });
  const target = migratedSaved(cloud, plan, { [tripKey("c1")]: "local" });
  assert.equal(count, diffSaved(cloud, target).length);
  // 1 upsertTrip override + 10 new trips + 1 city + 1 trip + 1 mark.
  assert.equal(count, 14);
  assert.equal(migrationOps(cloud, plan, {}), 13, "choosing the cloud version of the conflict removes that operation");
  assert.equal(migrationFits(MAX_BATCH_OPS), true);
  assert.equal(migrationFits(MAX_BATCH_OPS + 1), false);
});

test("a migration larger than one batch is detected before anything is created", () => {
  const trips = Array.from({ length: 2100 }, (_, i) => tripOf(`n${String(i).padStart(4, "0")}`, "2026-01-01", ""));
  const local = saved([cityWith("a", trips)]);
  const plan = planMigration(local, saved());
  assert.equal(migrationOps(saved(), plan, {}), 2101);
  assert.equal(migrationFits(migrationOps(saved(), plan, {})), false);
  assert.ok(!sameSaved(saved(), migratedSaved(saved(), plan, {})));
});
