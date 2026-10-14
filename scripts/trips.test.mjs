import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  TRIP_NOTE_LIMIT,
  cityLatestDate,
  displayTrips,
  isRealDate,
  legacyTripId,
  newTripId,
  sortVisitsByLatest,
  tripCount,
} from "../src/lib/trips.ts";
import {
  InvalidDatesError,
  LEGACY_LOCAL_KEY,
  LOCAL_KEY,
  createLocalWriter,
  digestText,
  loadLocalSaved,
  preserveConflict,
  recoverSaved,
  validateSaved,
} from "../src/lib/persistence.ts";
import {
  CACHE_SCHEMA,
  cacheKey,
  clearAccountData,
  clearOtherCaches,
  legacyCacheKey,
  legacyCacheSeenKey,
  loadCloudCache,
  readLegacyCacheChange,
  saveCloudCache,
} from "../src/lib/session.ts";
import {
  MAX_BATCH_OPS,
  RevisionChanged,
  SyncEngine,
  applyOp,
  diffSaved,
  rebaseSaved,
} from "../src/lib/sync.ts";
import { applyDelta } from "../src/lib/delta.ts";
import { visitedCityCount, visitedTripCount } from "../src/lib/statistics.ts";
import { describeApiError, BackendNotUpgraded } from "../src/lib/errors.ts";
import { ConnectError, Code } from "@connectrpc/connect";

const regions = JSON.parse(readFileSync("src/regions.json", "utf8"));
const green = "#4b937d";
const gold = "#e2af5c";
const city = (code, trips = [], extra = {}) => ({
  code, name: code, province: "330000", color: green, trips, ...extra,
});
const trip = (id, date = "", note = "") => ({ id, date, note });
const saved = (visits = [], provinces = {}) => ({ version: 2, visits, provinces });
const hangzhou = { code: "330100", name: "杭州市", province: "330000", color: green };
const v1 = (visits, provinces = {}) => ({ version: 1, visits, provinces });
const v1visit = (extra = {}) => ({ ...hangzhou, date: "2026-10-01", note: "西湖", ...extra });
// Text an earlier version stored, in the key this version only reads.
const legacyStore = (raw) => ({ getItem: (key) => (key === LEGACY_LOCAL_KEY ? raw : null) });

function memory(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    data,
    get length() { return data.size; },
    key: (i) => [...data.keys()][i] ?? null,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, String(v)),
    removeItem: (k) => void data.delete(k),
  };
}

test("only dates that exist on the calendar are real", () => {
  for (const ok of ["2026-10-14", "2024-02-29", "2000-02-29", "2026-12-31", "0001-01-01"])
    assert.ok(isRealDate(ok), ok);
  for (const bad of ["2026-02-30", "2023-02-29", "1900-02-29", "2026-13-01", "2026-00-10", "2026-04-31", "2026-1-1", "26-10-14", "", "2026-10-14T00:00", "abcd-ef-gh"])
    assert.ok(!isRealDate(bad), bad);
});

test("new trip IDs are valid, unique and ordered by creation time", () => {
  const ids = Array.from({ length: 2000 }, (_, i) => newTripId(1_700_000_000_000 + i));
  assert.equal(new Set(ids).size, ids.length);
  for (const id of ids) assert.match(id, /^[A-Za-z0-9_-]{1,64}$/);
  assert.deepEqual([...ids].sort(), ids, "later trips sort after earlier ones");
  assert.match(newTripId(), /^t[0-9a-z]{17}$/);
  assert.equal(legacyTripId("330100"), "legacy-330100");
  assert.match(legacyTripId("tw-09020"), /^[A-Za-z0-9_-]{1,64}$/);
});

test("trips show newest first, undated last, and cities follow their latest trip", () => {
  const trips = [trip("a", "2026-01-01"), trip("b", ""), trip("c", "2026-05-01"), trip("d", "2026-05-01"), trip("e", "2025-01-01")];
  assert.deepEqual(displayTrips(trips).map((t) => t.id), ["d", "c", "a", "e", "b"]);
  const visits = [
    city("a", [trip("1", "2024-01-01")]),
    city("b", [trip("2", "2020-01-01"), trip("3", "2026-06-01")]),
    city("c", [trip("4", "")]),
    city("d", []),
    city("e", [trip("5", "2024-01-01")]),
  ];
  assert.equal(cityLatestDate(visits[1]), "2026-06-01");
  assert.equal(cityLatestDate(visits[3]), "");
  assert.deepEqual(sortVisitsByLatest(visits).map((v) => v.code), ["b", "a", "e", "c", "d"]);
  assert.deepEqual(visits.map((v) => v.code), ["a", "b", "c", "d", "e"], "input is not reordered");
});

test("a second trip never changes the city count, only the trip count", () => {
  const one = saved([city("330100", [trip("t1", "2026-01-01")])]);
  const two = saved([city("330100", [trip("t1", "2026-01-01"), trip("t2", "2026-02-01")])]);
  assert.equal(visitedCityCount(two), visitedCityCount(one));
  assert.equal(visitedTripCount(one), 1);
  assert.equal(tripCount(two), 2);
  assert.equal(tripCount(saved([city("330100", [])])), 0);
  assert.equal(visitedCityCount(saved([city("330100", [])])), 1, "a city without trips is still checked in");
});

// ---- storage format ----

test("version 1 files convert to one trip per city and re-import as version 2 without loss", () => {
  const input = v1([v1visit(), v1visit({ code: "330200", name: "宁波市", date: "", note: "" })], { 110000: green });
  const converted = validateSaved(input, regions);
  assert.equal(converted.version, 2);
  assert.deepEqual(converted.visits.map((v) => v.trips), [
    [trip("legacy-330100", "2026-10-01", "西湖")],
    [trip("legacy-330200", "", "")],
  ]);
  assert.deepEqual(validateSaved(JSON.parse(JSON.stringify(converted)), regions), converted);
});

test("version 2 backups with several trips, no trips and empty trips round-trip exactly", () => {
  const data = saved([
    { ...city("330100"), name: "杭州市", trips: [trip("t-b", "2026-02-01", "再访"), trip("t-a", "2026-01-01", "第一次"), trip("t-c", "", "")] },
    { ...city("330200"), name: "宁波市", trips: [] },
  ], { 330000: gold });
  const out = validateSaved(JSON.parse(JSON.stringify(data)), regions);
  assert.deepEqual(out.visits[0].trips.map((t) => t.id), ["t-a", "t-b", "t-c"], "stored in ID order");
  assert.deepEqual(out.visits[1].trips, []);
  assert.deepEqual(validateSaved(JSON.parse(JSON.stringify(out)), regions), out);
});

test("version 2 files with malformed trips are rejected, notes at the limit are accepted", () => {
  const base = (trips) => ({ version: 2, visits: [{ ...hangzhou, trips }], provinces: {} });
  assert.doesNotThrow(() => validateSaved(base([trip("ok", "2024-02-29", "a".repeat(TRIP_NOTE_LIMIT))]), regions));
  assert.doesNotThrow(() => validateSaved(base([]), regions));
  assert.throws(() => validateSaved(base([trip("ok", "", "a".repeat(TRIP_NOTE_LIMIT + 1))]), regions));
  assert.throws(() => validateSaved(base([trip("a/b")]), regions));
  assert.throws(() => validateSaved(base([trip("")]), regions));
  assert.throws(() => validateSaved(base([trip("x".repeat(65))]), regions));
  assert.throws(() => validateSaved(base([trip("dup"), trip("dup")]), regions), /重复/);
  assert.throws(() => validateSaved(base([trip("ok", "2026-1-1")]), regions));
  assert.throws(() => validateSaved(base([{ id: "ok", date: "", note: 3 }]), regions));
  assert.throws(() => validateSaved({ version: 2, visits: [{ ...hangzhou }], provinces: {} }, regions), "trips are required");
  // The same trip ID in two cities is never valid: IDs identify a record account-wide.
  const two = { version: 2, visits: [{ ...hangzhou, trips: [trip("same")] }, { code: "330200", name: "宁波市", province: "330000", color: green, trips: [trip("same")] }], provinces: {} };
  assert.throws(() => validateSaved(two, regions), /重复/);
});

test("legal backups with many trips are not refused for their size", () => {
  const trips = Array.from({ length: 5000 }, (_, i) => trip(`t${String(i).padStart(5, "0")}`, "2026-01-01", "n".repeat(900)));
  const out = validateSaved({ version: 2, visits: [{ ...hangzhou, trips }], provinces: {} }, regions);
  assert.equal(out.visits[0].trips.length, 5000);
});

test("impossible v1 dates are never silently cleared or dropped", () => {
  const input = v1([v1visit({ date: "2026-02-30", note: "要保留的备注" }), v1visit({ code: "330200", name: "宁波市", date: "2026-04-31" })]);
  assert.throws(() => validateSaved(input, regions), (e) => e instanceof InvalidDatesError && e.count === 2 && /日历日期/.test(e.message));
  const raw = JSON.stringify(input);
  const result = loadLocalSaved(legacyStore(raw), regions);
  assert.equal(result.status, "unreadable");
  assert.equal(result.raw, raw, "the original text is kept for export");
  assert.match(result.reason, /2 个/);
  // The default recovery drops records it cannot keep as they are...
  assert.equal(result.recovery, null);
  // ...while the explicit correction clears only the dates and keeps every note.
  assert.equal(result.recoveryCorrected.correctedDates, 2);
  assert.equal(result.recoveryCorrected.discardedVisits, 0);
  assert.deepEqual(result.recoveryCorrected.saved.visits[0].trips, [trip("legacy-330100", "", "要保留的备注")]);
  assert.deepEqual(validateSaved(input, regions, { correctDates: true }).visits.map((v) => v.trips[0].date), ["", ""]);
  assert.equal(JSON.stringify(input), raw, "input is not mutated");
});

test("v2 files with impossible dates need the same explicit decision", () => {
  const input = { version: 2, visits: [{ ...hangzhou, trips: [trip("a", "2026-02-30", "n"), trip("b", "2026-03-01", "m")] }], provinces: {} };
  assert.throws(() => validateSaved(input, regions), InvalidDatesError);
  const fixed = validateSaved(input, regions, { correctDates: true });
  assert.deepEqual(fixed.visits[0].trips, [trip("a", "", "n"), trip("b", "2026-03-01", "m")]);
  const partial = recoverSaved(JSON.stringify(input), regions);
  assert.deepEqual(partial.saved.visits[0].trips.map((t) => t.id), ["b"], "recovery without correction drops just that trip");
  assert.equal(partial.discardedTrips, 1);
});

test("recovery of version 2 data keeps valid trips and reports what it dropped", () => {
  const raw = JSON.stringify({
    version: 2,
    visits: [
      { ...hangzhou, trips: [trip("ok", "2026-01-01", "好"), trip("bad/id"), trip("ok")] },
      { ...hangzhou, name: "错误" },
    ],
    provinces: { 330000: green, 999999: green },
  });
  const result = loadLocalSaved(legacyStore(raw), regions);
  assert.equal(result.status, "unreadable");
  assert.deepEqual(result.recovery.saved.visits[0].trips, [trip("ok", "2026-01-01", "好")]);
  assert.equal(result.recovery.discardedTrips, 2);
  assert.equal(result.recovery.discardedVisits, 1);
  assert.equal(result.recoveryCorrected, null);
});

test("the local writer upgrades into its own key without touching the legacy bytes, and refuses to overwrite another tab", () => {
  const raw = JSON.stringify(v1([v1visit()]));
  const store = memory({ [LEGACY_LOCAL_KEY]: raw });
  const load = loadLocalSaved(store, regions);
  const writer = createLocalWriter(store, load.canonicalRaw, load.legacyDigest);
  const next = saved([city("330100", [trip("t1")], { name: "杭州市" })]);
  assert.deepEqual(writer.write(next), { status: "ok" });
  assert.equal(store.getItem(LEGACY_LOCAL_KEY), raw, "the original text stays in its key, byte for byte");
  assert.equal(JSON.parse(store.getItem(LOCAL_KEY)).version, 2);
  // Later writes do not make copies.
  writer.write(saved());
  assert.deepEqual([...store.data.keys()].sort(), [LEGACY_LOCAL_KEY, LOCAL_KEY, "shanhai-travel-v2-legacy-seen"].sort());
  // Another tab changes the data: this page must stop and ask.
  store.setItem(LOCAL_KEY, "other tab");
  const blocked = writer.write(saved([city("330100", [trip("t2")], { name: "杭州市" })]));
  assert.deepEqual(blocked, { status: "conflict", other: "other tab" });
  assert.equal(store.getItem(LOCAL_KEY), "other tab", "nothing was overwritten");
  assert.equal(preserveConflict(store, "other tab", new Date(5)), true);
  assert.equal(store.getItem("shanhai-travel-v2-conflict-5"), "other tab");
  writer.adopt("other tab");
  assert.equal(writer.write(saved()).status, "ok");
});

test("a refused write is an error and leaves the stored text alone", () => {
  const store = memory({ [LOCAL_KEY]: "old" });
  const writer = createLocalWriter({ getItem: store.getItem, setItem: () => { throw new Error("quota"); } }, "old");
  assert.deepEqual(writer.write(saved()), { status: "error" });
  assert.equal(store.getItem(LOCAL_KEY), "old");
});

// ---- account cache ----

const wireV1 = (code, extra = {}) => ({ ...hangzhou, code, date: "", note: "", ...extra });
const validateAny = (data) => validateSaved(data, [{ code: "330000", cities: Array.from({ length: 300 }, (_, i) => ({ code: `c${i}`, name: `c${i}` })).concat([{ code: "330100", name: "杭州市" }]) }]);

test("a cache with more than 100 pending operations loads (regression for the old limit)", () => {
  const store = memory();
  const ops = Array.from({ length: 150 }, (_, i) => ({ type: "upsertVisit", visit: { code: `c${i}`, name: `c${i}`, province: "330000", color: green, date: "", note: "" } }));
  const confirmed = v1([]);
  const desired = v1(ops.map((op) => ({ ...op.visit })));
  store.setItem(legacyCacheKey("u"), JSON.stringify({ saved: desired, confirmed, revision: 3, pendingBatch: { id: "b1", revision: 3, ops } }));
  const loaded = loadCloudCache(store, "u", validateAny);
  assert.equal(loaded.pendingBatch.ops.length, 150);
  assert.equal(loaded.pendingBatch.id, "b1");
  assert.equal(loaded.pendingBatch.revision, 3);
  assert.ok(loaded.pendingBatch.ops.every((op) => op.type === "legacyUpsertVisit"));
  assert.ok(MAX_BATCH_OPS >= 150);
  // The sender can produce batches up to MAX_BATCH_OPS, so the reader accepts exactly that many.
  const wide = Array.from({ length: MAX_BATCH_OPS }, (_, i) => ({ type: "upsertTrip", code: "330100", trip: trip(`t${i}`, "", "") }));
  const cache = { schema: CACHE_SCHEMA, saved: saved([city("330100", wide.map((o) => o.trip), { name: "杭州市" })]), confirmed: saved([city("330100", [], { name: "杭州市" })]), revision: 1, pendingBatch: { id: "b2", revision: 1, ops: wide } };
  const wideStore = memory();
  assert.ok(saveCloudCache(wideStore, "u", cache));
  assert.equal(loadCloudCache(wideStore, "u", validateAny).pendingBatch.ops.length, MAX_BATCH_OPS);
  const tooMany = { ...cache, pendingBatch: { ...cache.pendingBatch, ops: [...wide, wide[0]] } };
  const rejected = memory({ [cacheKey("u")]: JSON.stringify(tooMany) });
  assert.throws(() => loadCloudCache(rejected, "u", validateAny), /原始数据/);
  assert.equal(rejected.getItem(cacheKey("u")), JSON.stringify(tooMany), "raw data stays in place");
});

test("a version 1 cache converts, keeps its pending batch verbatim, and its original key is never rewritten", () => {
  const confirmed = v1([wireV1("330100", { note: "老" })]);
  const desired = v1([wireV1("330100", { note: "新" })]);
  const ops = [{ type: "upsertVisit", visit: wireV1("330100", { note: "批量中" }) }, { type: "deleteMark", province: "330000" }];
  const raw = JSON.stringify({ saved: desired, confirmed, revision: 7, pendingBatch: { id: "lost-reply", revision: 7, ops } });
  const store = memory({ [legacyCacheKey("u")]: raw });
  const loaded = loadCloudCache(store, "u", validateAny);
  assert.equal(loaded.schema, 2);
  assert.equal(loaded.revision, 7);
  assert.equal(loaded.confirmed.visits[0].trips[0].note, "老");
  assert.equal(loaded.saved.visits[0].trips[0].note, "新");
  assert.deepEqual(loaded.pendingBatch, {
    id: "lost-reply",
    revision: 7,
    ops: [{ type: "legacyUpsertVisit", visit: wireV1("330100", { note: "批量中" }) }, { type: "deleteMark", province: "330000" }],
  });
  assert.equal(loaded.legacyDigest, digestText(raw));
  assert.equal(loaded.legacyChange, undefined, "the first load takes the legacy cache over, nothing is pending a decision");
  assert.equal(store.data.size, 1, "loading writes nothing");
  // The first save creates the canonical cache with the same request ID and wire operations.
  assert.ok(saveCloudCache(store, "u", { saved: loaded.saved, confirmed: loaded.confirmed, revision: 7, pendingBatch: loaded.pendingBatch }, loaded.legacyDigest));
  const written = JSON.parse(store.getItem(cacheKey("u")));
  assert.equal(written.schema, 2);
  assert.deepEqual(written.pendingBatch, loaded.pendingBatch);
  assert.equal(written.pendingBatch.id, "lost-reply");
  assert.equal(store.getItem(legacyCacheKey("u")), raw, "the legacy text is byte for byte what it was");
  assert.equal(store.getItem(legacyCacheSeenKey("u")), digestText(raw));
  assert.ok(!legacyCacheSeenKey("u").startsWith("shanhai-cloud-v2:"), "the marker is not mistaken for a cache");
  // Reading again prefers the canonical cache and reports nothing.
  const again = loadCloudCache(store, "u", validateAny);
  assert.deepEqual(again.pendingBatch, loaded.pendingBatch);
  assert.equal(again.legacyChange, undefined);
});

test("v2 work survives an older page overwriting its own cache key while the v2 page is closed", () => {
  const confirmed = saved([city("330100", [trip("legacy-330100", "", "")], { name: "杭州市" })]);
  const work = saved([city("330100", [trip("legacy-330100", "", ""), trip("t-new", "2027-01-01", "只在本机")], { name: "杭州市" })]);
  const batch = { id: "b-v2", revision: 4, ops: [{ type: "upsertTrip", code: "330100", trip: trip("t-new", "2027-01-01", "只在本机") }] };
  const legacyRaw = JSON.stringify({ saved: v1([wireV1("330100")]), confirmed: v1([wireV1("330100")]), revision: 4 });
  const store = memory({ [legacyCacheKey("u")]: legacyRaw });
  const first = loadCloudCache(store, "u", validateAny);
  assert.ok(saveCloudCache(store, "u", { saved: work, confirmed, revision: 4, pendingBatch: batch }, first.legacyDigest));
  const canonical = store.getItem(cacheKey("u"));
  // The older page, still open, saves its own (stale) state to the key it knows.
  const older = JSON.stringify({ saved: v1([wireV1("330100", { note: "旧页面" }), wireV1("c1", { name: "c1" })]), confirmed: v1([wireV1("330100")]), revision: 4 });
  store.setItem(legacyCacheKey("u"), older);
  assert.equal(store.getItem(cacheKey("u")), canonical, "the canonical cache is out of its reach");
  const reopened = loadCloudCache(store, "u", validateAny);
  assert.deepEqual(reopened.saved, work, "every unsynced v2 record is still there");
  assert.deepEqual(reopened.pendingBatch, batch, "with its original request ID");
  assert.equal(reopened.legacyChange.raw, older, "the older page's cache is offered as stored");
  assert.equal(reopened.legacyChange.saved.visits.length, 2);
  assert.equal(readLegacyCacheChange(store, "u", validateAny).digest, digestText(older));
  store.setItem(legacyCacheSeenKey("u"), digestText(older));
  assert.equal(loadCloudCache(store, "u", validateAny).legacyChange, undefined);
  assert.equal(readLegacyCacheChange(store, "u", validateAny), null);
});

test("an unreadable legacy cache next to a good canonical one is offered for export only", () => {
  const store = memory({
    [cacheKey("u")]: JSON.stringify({ schema: 2, saved: saved(), confirmed: saved(), revision: 1 }),
    [legacyCacheKey("u")]: "{nope",
  });
  const loaded = loadCloudCache(store, "u", validateAny);
  assert.equal(loaded.legacyChange.raw, "{nope");
  assert.equal(loaded.legacyChange.saved, null);
  assert.equal(store.getItem(legacyCacheKey("u")), "{nope");
});

test("logout removes the canonical cache and a legacy cache that was taken over, but keeps older-page changes", () => {
  const s = v1([wireV1("330100")]);
  const dirty = v1([wireV1("330100", { note: "旧页面未同步" })]);
  const takenOver = JSON.stringify({ saved: s, confirmed: s, revision: 1 });
  const store = memory({ [legacyCacheKey("u")]: takenOver });
  const loaded = loadCloudCache(store, "u", validateAny);
  saveCloudCache(store, "u", { saved: loaded.saved, confirmed: loaded.confirmed, revision: 1 }, loaded.legacyDigest);
  clearAccountData(store, "u", validateAny);
  assert.deepEqual([...store.data.keys()], [], "a taken-over legacy cache and both markers are gone");

  // An older page wrote unsynced work after the takeover: it must outlive the logout.
  const other = memory({ [legacyCacheKey("u")]: takenOver });
  const l2 = loadCloudCache(other, "u", validateAny);
  saveCloudCache(other, "u", { saved: l2.saved, confirmed: l2.confirmed, revision: 1 }, l2.legacyDigest);
  const newer = JSON.stringify({ saved: dirty, confirmed: s, revision: 1 });
  other.setItem(legacyCacheKey("u"), newer);
  clearAccountData(other, "u", validateAny);
  assert.equal(other.getItem(legacyCacheKey("u")), newer);
  assert.equal(other.getItem(cacheKey("u")), null);
  // The next sign-in takes that cache over, with its unsent work.
  const resumed = loadCloudCache(other, "u", validateAny);
  assert.equal(resumed.saved.visits[0].trips[0].note, "旧页面未同步");
});

test("a confirmed account deletion removes every key of that account, changed or unreadable ones too, and nothing else", () => {
  const s = v1([wireV1("330100")]);
  const dirty = v1([wireV1("330100", { note: "旧页面未同步" })]);
  const takenOver = JSON.stringify({ saved: s, confirmed: s, revision: 1 });
  const local = JSON.stringify({ version: 1, visits: [], provinces: {} });
  const theirs = JSON.stringify({ schema: 2, saved: saved(), confirmed: saved(), revision: 1, pendingBatch: { id: "b", revision: 1, ops: [] } });
  const build = (legacy) => {
    const store = memory({ [legacyCacheKey("u")]: takenOver });
    const loaded = loadCloudCache(store, "u", validateAny);
    saveCloudCache(store, "u", { saved: loaded.saved, confirmed: loaded.confirmed, revision: 1 }, loaded.legacyDigest);
    store.setItem(legacyCacheKey("u"), legacy);
    store.setItem("shanhai-migrated-v1:u", "fp");
    store.setItem("shanhai-travel-v1", local);
    store.setItem(cacheKey("other"), theirs);
    store.setItem(legacyCacheKey("other"), "{unreadable");
    return store;
  };
  for (const legacy of [JSON.stringify({ saved: dirty, confirmed: s, revision: 1 }), "{not json"]) {
    // Logout keeps what an older page changed ...
    const out = build(legacy);
    clearAccountData(out, "u", validateAny);
    assert.equal(out.getItem(legacyCacheKey("u")), legacy);
    // ... deletion of the account does not: it discards its unsynced data.
    const store = build(legacy);
    clearAccountData(store, "u", validateAny, true);
    assert.deepEqual(
      [...store.data.keys()].filter((k) => k.endsWith(":u") || k.includes(":u")),
      [],
      "no key of the deleted account remains",
    );
    assert.equal(store.getItem("shanhai-travel-v1"), local, "local-mode footprints stay");
    assert.equal(store.getItem(cacheKey("other")), theirs, "another account's unsynced cache stays");
    assert.equal(store.getItem(legacyCacheKey("other")), "{unreadable", "another account's unreadable cache stays");
  }
});

test("other accounts' clean caches are cleared in both layouts, ones with unsent work are kept", () => {
  const s = v1([wireV1("330100")]);
  const dirty = v1([wireV1("330100", { note: "未同步" })]);
  const v2s = saved([city("330100", [trip("legacy-330100")], { name: "杭州市" })]);
  const v2dirty = saved([city("330100", [trip("legacy-330100", "", "未同步")], { name: "杭州市" })]);
  const store = memory({
    [legacyCacheKey("clean")]: JSON.stringify({ saved: s, confirmed: s, revision: 1 }),
    [legacyCacheKey("dirty")]: JSON.stringify({ saved: dirty, confirmed: s, revision: 1 }),
    [cacheKey("clean2")]: JSON.stringify({ schema: 2, saved: v2s, confirmed: v2s, revision: 1 }),
    [cacheKey("dirty2")]: JSON.stringify({ schema: 2, saved: v2dirty, confirmed: v2s, revision: 1 }),
  });
  clearOtherCaches(store, "me", validateAny);
  assert.equal(store.getItem(legacyCacheKey("clean")), null);
  assert.notEqual(store.getItem(legacyCacheKey("dirty")), null);
  assert.equal(store.getItem(cacheKey("clean2")), null);
  assert.notEqual(store.getItem(cacheKey("dirty2")), null);
});

test("a version 1 cache with an impossible date is unreadable and stays untouched", () => {
  const bad = v1([wireV1("330100", { date: "2026-02-30" })]);
  const raw = JSON.stringify({ saved: bad, confirmed: bad, revision: 1 });
  const store = memory({ [legacyCacheKey("u")]: raw });
  assert.throws(() => loadCloudCache(store, "u", validateAny), /原始数据/);
  assert.equal(store.getItem(legacyCacheKey("u")), raw);
});

test("unknown cache schemas are protected rather than guessed at", () => {
  const raw = JSON.stringify({ schema: 3, saved: saved(), confirmed: saved(), revision: 1 });
  const store = memory({ [cacheKey("u")]: raw });
  assert.throws(() => loadCloudCache(store, "u", validateAny), /原始数据/);
  assert.equal(store.getItem(cacheKey("u")), raw);
});

// ---- sync engine ----

function serverApi(initial = saved(), revision = 0) {
  let state = initial;
  const receipts = new Map();
  const calls = [];
  return {
    get state() { return state; },
    get revision() { return revision; },
    calls,
    change(ops) { for (const op of ops) { state = applyOp(state, op); revision++; } },
    get: async () => ({ saved: state, revision }),
    apply: async () => { throw new Error("single-op path"); },
    applyBatch: async (ops, base, id) => {
      if (receipts.has(id)) return receipts.get(id);
      if (base !== revision) throw new RevisionChanged();
      calls.push({ id, ops });
      for (const op of ops) { state = applyOp(state, op); revision++; }
      receipts.set(id, revision);
      return revision;
    },
    applied: (id) => receipts.has(id),
  };
}

const legacyBatch = { id: "p1", revision: 2, ops: [{ type: "legacyUpsertVisit", visit: { ...hangzhou, date: "2026-10-01", note: "first" } }] };

test("a version 1 pending batch whose reply was lost is replayed unchanged, then a newer edit follows", async () => {
  const api = serverApi();
  api.change([{ type: "upsertVisit", visit: hangzhou }, { type: "upsertTrip", code: "330100", trip: trip("legacy-330100", "2026-09-01", "older") }]);
  // The first request reached the server; only its reply was lost.
  assert.equal(await api.applyBatch(legacyBatch.ops, 2, "p1"), 3);
  const confirmed = saved([city("330100", [trip("legacy-330100", "2026-09-01", "older")], { name: "杭州市" })]);
  const desired = saved([city("330100", [trip("legacy-330100", "2026-10-01", "second")], { name: "杭州市" })]);
  const engine = new SyncEngine(api, { saved: confirmed, revision: 2 }, {}, desired, legacyBatch);
  engine.retry();
  await engine.drain();
  assert.equal(engine.status, "idle");
  assert.equal(api.calls.filter((c) => c.id === "p1").length, 1, "the original request is never applied twice");
  const followUp = api.calls.at(-1);
  assert.notEqual(followUp.id, "p1");
  assert.deepEqual(followUp.ops, [{ type: "upsertTrip", code: "330100", trip: trip("legacy-330100", "2026-10-01", "second") }]);
  assert.equal(api.state.visits[0].trips[0].note, "second");
  assert.equal(engine.pending, false);
});

test("a version 1 pending batch the server never applied is dropped and recomputed from the edits", async () => {
  const api = serverApi(saved(), 0);
  const stale = { id: "never-applied", revision: 0, ops: [{ type: "legacyUpsertVisit", visit: { ...hangzhou, date: "2026-10-01", note: "kept" } }] };
  api.change([{ type: "upsertVisit", visit: { ...hangzhou, code: "330200", name: "宁波市" } }]);
  const desired = saved([city("330100", [trip("legacy-330100", "2026-10-01", "kept")], { name: "杭州市" })]);
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 }, {}, desired, stale);
  engine.retry();
  await engine.drain();
  assert.equal(engine.status, "idle");
  assert.ok(!api.applied("never-applied"));
  const hz = api.state.visits.find((v) => v.code === "330100");
  assert.deepEqual(hz.trips, [trip("legacy-330100", "2026-10-01", "kept")]);
  assert.ok(api.state.visits.some((v) => v.code === "330200"), "the other device's city is preserved");
});

test("a backend that is not upgraded blocks every request and keeps the edits", async () => {
  const api = serverApi();
  api.ready = async () => { throw new BackendNotUpgraded(); };
  const errors = [];
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 }, { onError: (e) => errors.push(e) });
  engine.push(saved([city("330100", [trip("t1")], { name: "杭州市" })]));
  await engine.drain();
  assert.equal(engine.status, "error");
  assert.ok(errors[0] instanceof BackendNotUpgraded);
  assert.equal(api.calls.length, 0, "nothing was sent");
  assert.equal(engine.pending, true);
  assert.equal(engine.cache.saved.visits[0].trips[0].id, "t1");
  assert.match(describeApiError(errors[0]), /尚未升级/);
});

test("changes of more than one batch are sent as consecutive atomic batches", async () => {
  const api = serverApi();
  const trips = Array.from({ length: MAX_BATCH_OPS + 500 }, (_, i) => trip(`t${String(i).padStart(5, "0")}`, "2026-01-01", ""));
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 });
  engine.push(saved([city("330100", trips, { name: "杭州市" })]));
  await engine.drain();
  assert.equal(engine.status, "idle");
  assert.deepEqual(api.calls.map((c) => c.ops.length), [MAX_BATCH_OPS, 501 + 0], "city first, then trips; every prefix is consistent");
  assert.equal(api.state.visits[0].trips.length, trips.length);
  assert.equal(engine.pending, false);
});

test("diff order keeps every prefix of a batch consistent", () => {
  const base = saved([city("a", [trip("t1"), trip("t2")]), city("gone", [trip("g1")])]);
  const next = saved([city("a", [trip("t2", "2026-01-01")], { color: gold }), city("new", [trip("n1")])]);
  const kinds = diffSaved(base, next).map((op) => op.type);
  assert.deepEqual(kinds, ["deleteTrip", "deleteVisit", "upsertVisit", "upsertVisit", "upsertTrip", "upsertTrip"]);
  let state = base;
  for (const op of diffSaved(base, next)) {
    state = applyOp(state, op);
    assert.ok(state.visits.every((v) => v.code !== "g1"), "no trip outlives its city");
  }
  assert.deepEqual(state.visits.map((v) => v.code).sort(), ["a", "new"]);
});

test("applyOp keeps trips ordered, ignores a trip without its city and cascades city deletion", () => {
  let s = saved([city("a")]);
  s = applyOp(s, { type: "upsertTrip", code: "a", trip: trip("t2") });
  s = applyOp(s, { type: "upsertTrip", code: "a", trip: trip("t1") });
  s = applyOp(s, { type: "upsertTrip", code: "a", trip: trip("t2", "2026-01-01", "x") });
  assert.deepEqual(s.visits[0].trips, [trip("t1"), trip("t2", "2026-01-01", "x")]);
  assert.deepEqual(applyOp(s, { type: "upsertTrip", code: "missing", trip: trip("t3") }), s);
  assert.deepEqual(applyOp(s, { type: "deleteTrip", code: "a", id: "t1" }).visits[0].trips, [trip("t2", "2026-01-01", "x")]);
  assert.deepEqual(applyOp(s, { type: "deleteVisit", code: "a" }).visits, []);
  const legacy = applyOp(saved(), { type: "legacyUpsertVisit", visit: { ...hangzhou, date: "2026-10-01", note: "n" } });
  assert.deepEqual(legacy.visits, [{ ...hangzhou, trips: [trip("legacy-330100", "2026-10-01", "n")] }]);
  const recolor = applyOp(s, { type: "upsertVisit", visit: { ...city("a"), color: gold } });
  assert.equal(recolor.visits[0].trips.length, 2, "editing the city keeps its trips");
});

test("editing different trips of one city merges; the same trip needs a choice", () => {
  const base = saved([city("a", [trip("t1", "", "base1"), trip("t2", "", "base2")])]);
  const local = saved([city("a", [trip("t1", "", "local1"), trip("t2", "", "base2")])]);
  const remote = saved([city("a", [trip("t1", "", "base1"), trip("t2", "", "remote2"), trip("t3")])]);
  const merged = rebaseSaved(base, local, remote);
  assert.deepEqual(merged.conflicts, []);
  assert.deepEqual(merged.saved.visits[0].trips.map((t) => t.note), ["local1", "remote2", ""]);
  const clash = rebaseSaved(base, local, saved([city("a", [trip("t1", "", "remote1"), trip("t2", "", "base2")])]));
  assert.deepEqual(clash.conflicts.map((c) => [c.type, c.trip?.id]), [["upsertTrip", "t1"]]);
});

test("a city recolored locally and a trip edited remotely both survive", () => {
  const base = saved([city("a", [trip("t1", "", "x")])]);
  const result = rebaseSaved(base, saved([city("a", [trip("t1", "", "x")], { color: gold })]), saved([city("a", [trip("t1", "", "remote")])]));
  assert.deepEqual(result.conflicts, []);
  assert.equal(result.saved.visits[0].color, gold);
  assert.equal(result.saved.visits[0].trips[0].note, "remote");
});

for (const choice of ["local", "cloud"]) {
  test(`deleting a city locally while its trip changed remotely waits for the ${choice} choice`, async () => {
    const base = saved([city("a", [trip("t1", "", "x")]), city("b")]);
    const api = serverApi(base, 4);
    const engine = new SyncEngine(api, { saved: base, revision: 4 });
    api.change([{ type: "upsertTrip", code: "a", trip: trip("t1", "", "edited elsewhere") }, { type: "upsertTrip", code: "a", trip: trip("t2", "", "new elsewhere") }]);
    engine.push(saved([city("b")]));
    await engine.drain();
    assert.equal(engine.status, "conflict");
    assert.deepEqual(engine.conflicts.map((c) => c.type), ["deleteVisit"]);
    assert.equal(api.calls.length, 0);
    engine.resolve(choice);
    await engine.drain();
    assert.equal(engine.status, "idle");
    const a = api.state.visits.find((v) => v.code === "a");
    if (choice === "local") assert.equal(a, undefined, "the city and every trip in it are gone");
    else assert.deepEqual(a.trips.map((t) => t.note), ["edited elsewhere", "new elsewhere"], "the cloud's whole city is kept");
    assert.ok(api.state.visits.some((v) => v.code === "b"));
  });

  test(`adding a trip locally while its city was cancelled remotely waits for the ${choice} choice`, async () => {
    const base = saved([city("a", [trip("t1", "2026-01-01", "old")])]);
    const api = serverApi(base, 2);
    const engine = new SyncEngine(api, { saved: base, revision: 2 });
    api.change([{ type: "deleteVisit", code: "a" }]);
    engine.push(saved([city("a", [trip("t1", "2026-01-01", "old"), trip("t2", "2026-02-01", "new here")])]));
    await engine.drain();
    assert.equal(engine.status, "conflict");
    assert.equal(api.calls.length, 0, "nothing is sent before the user chooses");
    engine.resolve(choice);
    await engine.drain();
    assert.equal(engine.status, "idle");
    const a = api.state.visits.find((v) => v.code === "a");
    if (choice === "local") {
      assert.deepEqual(a.trips.map((t) => t.id), ["t1", "t2"], "the city returns with everything this device had in it");
    } else assert.equal(a, undefined);
    assert.equal(engine.pending, false);
  });
}

test("deleting one trip while another changes remotely is not a conflict", async () => {
  const base = saved([city("a", [trip("t1", "", "1"), trip("t2", "", "2")])]);
  const api = serverApi(base, 3);
  const engine = new SyncEngine(api, { saved: base, revision: 3 });
  api.change([{ type: "upsertTrip", code: "a", trip: trip("t2", "", "edited") }]);
  engine.push(saved([city("a", [trip("t2", "", "2")])]));
  await engine.drain();
  assert.equal(engine.status, "idle");
  assert.deepEqual(api.state.visits[0].trips, [trip("t2", "", "edited")]);
});

test("deleting the last trip keeps the city check-in", async () => {
  const base = saved([city("a", [trip("t1", "", "1")])]);
  const api = serverApi(base, 2);
  const engine = new SyncEngine(api, { saved: base, revision: 2 });
  engine.push(saved([city("a", [])]));
  await engine.drain();
  assert.deepEqual(api.calls.map((c) => c.ops.map((o) => o.type)), [["deleteTrip"]]);
  assert.deepEqual(api.state.visits, [city("a", [])]);
});

test("change-log entries from older clients and new ones apply to the same state", () => {
  const start = { saved: saved(), revision: 0 };
  const page = {
    changes: [
      { revision: 1, op: { type: "legacyUpsertVisit", visit: { ...hangzhou, date: "2026-10-01", note: "旧" } } },
      { revision: 2, op: { type: "upsertTrip", code: "330100", trip: trip("t-new", "2026-11-01", "新") } },
      { revision: 3, op: { type: "deleteTrip", code: "330100", id: "legacy-330100" } },
    ],
    nextRevision: 3, currentRevision: 3, hasMore: false, resyncRequired: false,
  };
  const result = applyDelta(start, page);
  assert.deepEqual(result.saved.visits[0].trips, [trip("t-new", "2026-11-01", "新")]);
});

test("backend errors for trips have Chinese descriptions", () => {
  for (const [message, pattern] of [
    ["client upgrade required: reload", /版本过旧/],
    ["trip not found: sync the trip", /尚未同步|还没有同步/],
    ["trip city does not exist", /城市/],
  ])
    assert.match(describeApiError(new ConnectError(message, Code.FailedPrecondition)), pattern);
});

// ---- review fixes ----
import { AtomicBatchTooLarge, AtomicBatchUnavailable } from "../src/lib/sync.ts";
import { tripLabel } from "../src/lib/trips.ts";

test("the frontend date check agrees with the shared fixtures the backend is tested against", () => {
  const fixtures = JSON.parse(readFileSync("scripts/fixtures/trip-dates.json", "utf8"));
  for (const date of fixtures.valid) assert.equal(isRealDate(date) || date === "", true, `valid ${JSON.stringify(date)}`);
  for (const date of fixtures.invalid) assert.equal(isRealDate(date), false, `invalid ${JSON.stringify(date)}`);
  // A stored year 0000 stays readable instead of locking the data.
  const input = { version: 2, visits: [{ ...hangzhou, trips: [trip("old", "0000-02-29", "古")] }], provinces: {} };
  assert.equal(validateSaved(input, regions).visits[0].trips[0].date, "0000-02-29");
});

test("a trip is named by position, date and note so equal dates stay distinguishable", () => {
  const trips = [trip("a", "2026-05-01", "第一天"), trip("b", "2026-05-01", "第一天"), trip("c", "", ""), trip("d", "", "")];
  const labels = trips.map((t) => tripLabel(trips, t.id));
  assert.equal(new Set(labels).size, 4);
  assert.match(tripLabel(trips, "c"), /日期未记录/);
  assert.match(tripLabel(trips, "a"), /第 \d 次 · 2026-05-01 · 第一天/);
  assert.equal(tripLabel(trips, "missing"), "");
  assert.match(tripLabel([trip("l", "", "x".repeat(40))], "l"), /…$/);
});

test("an atomic change that does not fit one batch pauses with everything kept, and fits again once reduced", async () => {
  const api = serverApi(saved([city("330100", [], { name: "杭州市" })]), 1);
  const many = Array.from({ length: MAX_BATCH_OPS + 5 }, (_, i) => trip(`t${String(i).padStart(5, "0")}`));
  const target = saved([city("330100", many, { name: "杭州市" })]);
  const errors = [];
  const stored = [];
  const checked = new SyncEngine(api, { saved: api.state, revision: 1 }, { onError: (e) => errors.push(e), onChange: (c) => stored.push(JSON.parse(JSON.stringify(c))) });
  assert.equal(checked.pushAtomic(target), true);
  await checked.drain();
  assert.equal(api.calls.length, 0, "no first batch went out");
  assert.ok(errors[0] instanceof AtomicBatchTooLarge);
  assert.equal(errors[0].count, MAX_BATCH_OPS + 5);
  assert.equal(checked.status, "error");
  assert.match(describeApiError(errors[0]), /原子迁移.*2000.*仍完整保留在本机/s);
  // Nothing was rolled back: the accepted data and the intent are still there, also in the stored cache.
  assert.equal(checked.pending, true);
  assert.deepEqual(checked.cache.saved, target);
  assert.equal(checked.cache.atomic, true);
  assert.deepEqual(stored.at(-1).saved, target);
  assert.equal(stored.at(-1).atomic, true);
  // Retrying without changing anything pauses again, still without sending or discarding.
  checked.retry();
  await checked.drain();
  assert.equal(api.calls.length, 0);
  assert.deepEqual(checked.cache.saved, target);
  // The user reduces it to fit; it then goes out as one batch and the intent is cleared and stored.
  const fits = many.slice(0, MAX_BATCH_OPS);
  checked.push(saved([city("330100", fits, { name: "杭州市" })]));
  await checked.drain();
  assert.equal(api.calls.length, 1);
  assert.equal(api.calls[0].ops.length, MAX_BATCH_OPS);
  assert.equal(checked.pending, false);
  assert.equal(checked.cache.atomic, undefined);
  assert.equal(stored.at(-1).atomic, undefined, "clearing the intent is persisted");
  // A change that fits goes out as one batch of exactly the limit, even when pushed atomically.
  const engine = new SyncEngine(api, { saved: api.state, revision: api.revision }, {});
  const exact = Array.from({ length: MAX_BATCH_OPS }, (_, i) => trip(`u${String(i).padStart(5, "0")}`));
  const kept = api.state.visits[0].trips;
  assert.equal(engine.pushAtomic(saved([city("330100", [...kept, ...exact], { name: "杭州市" })])), true);
  await engine.drain();
  assert.equal(api.calls.length, 2);
  assert.equal(api.calls[1].ops.length, MAX_BATCH_OPS);
  // Queued edits are never mixed into an atomic change.
  engine.push(saved([city("330100", [...kept, ...exact, trip("extra")], { name: "杭州市" })]));
  assert.equal(engine.pending, true);
  assert.equal(engine.pushAtomic(saved()), false);
});

test("an atomic change is refused while other edits are waiting", () => {
  const api = serverApi(saved(), 0);
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 }, {}, saved([city("330100", [], { name: "杭州市" })]));
  assert.equal(engine.pending, true);
  assert.equal(engine.pushAtomic(saved()), false);
});

test("the atomic requirement survives a reload before the first batch exists, so a later growth never splits it", async () => {
  const base = saved([city("330100", [], { name: "杭州市" })]);
  const api = serverApi(base, 1);
  let failGet = true;
  const flaky = { ...api, get: async () => { if (failGet) throw new Error("network down"); return api.get(); } };
  const fits = Array.from({ length: MAX_BATCH_OPS - 5 }, (_, i) => trip(`a${String(i).padStart(5, "0")}`));
  const stored = [];
  const first = new SyncEngine(flaky, { saved: base, revision: 1 }, { onChange: (c) => stored.push(JSON.parse(JSON.stringify(c))) });
  assert.equal(first.pushAtomic(saved([city("330100", fits, { name: "杭州市" })])), true);
  await first.drain();
  assert.equal(first.status, "error");
  assert.equal(api.calls.length, 0);
  const cache = stored.at(-1);
  assert.equal(cache.pendingBatch, undefined, "the failure came before any batch was built");
  assert.equal(cache.atomic, true, "the intent is persisted with the unsent change");

  // The page is reloaded: the cache goes through the same text the browser stores.
  const store = memory();
  assert.equal(saveCloudCache(store, "u", cache), true);
  const loaded = loadCloudCache(store, "u", validateAny);
  assert.equal(loaded.atomic, true);
  failGet = false;
  const more = Array.from({ length: 20 }, (_, i) => trip(`b${String(i).padStart(5, "0")}`));
  const errors = [];
  const second = new SyncEngine(
    api,
    { saved: loaded.confirmed, revision: loaded.revision },
    { onError: (e) => errors.push(e) },
    loaded.saved,
    loaded.pendingBatch,
    loaded.atomic === true,
  );
  // A later edit makes the whole change larger than one batch.
  second.push(saved([city("330100", [...fits, ...more], { name: "杭州市" })]));
  await second.drain();
  assert.equal(api.calls.length, 0, "not even the first 2000 operations were sent");
  assert.ok(errors[0] instanceof AtomicBatchTooLarge);
  // The accepted migration and the later edit are both still there, with the intent.
  assert.equal(second.pending, true);
  assert.equal(second.cache.atomic, true);
  assert.equal(second.cache.saved.visits[0].trips.length, fits.length + more.length);
  // Reduced to one batch, it goes out whole.
  second.push(saved([city("330100", [...fits, ...more.slice(0, 3)], { name: "杭州市" })]));
  await second.drain();
  assert.equal(api.calls.length, 1);
  assert.equal(api.calls[0].ops.length, fits.length + 3);
  assert.equal(second.cache.atomic, undefined);
});

test("an atomic change that still fits after a reload goes out as one batch and then clears the flag", async () => {
  const base = saved([city("330100", [], { name: "杭州市" })]);
  const api = serverApi(base, 1);
  const fits = Array.from({ length: MAX_BATCH_OPS }, (_, i) => trip(`a${String(i).padStart(5, "0")}`));
  const engine = new SyncEngine(api, { saved: base, revision: 1 }, {}, saved([city("330100", fits, { name: "杭州市" })]), undefined, true);
  assert.equal(engine.cache.atomic, true);
  engine.retry();
  await engine.drain();
  assert.equal(api.calls.length, 1);
  assert.equal(api.calls[0].ops.length, MAX_BATCH_OPS);
  assert.equal(engine.pending, false);
  assert.equal(engine.cache.atomic, undefined);
});

test("an atomic change is never sent one operation at a time when batches are unavailable", async () => {
  const base = saved([city("330100", [], { name: "杭州市" })]);
  const singles = [];
  const api = {
    get: async () => ({ saved: base, revision: 1 }),
    apply: async (op) => { singles.push(op); return 2; },
  };
  const errors = [];
  const engine = new SyncEngine(api, { saved: base, revision: 1 }, { onError: (e) => errors.push(e) });
  assert.equal(engine.pushAtomic(saved([city("330100", [trip("x"), trip("y")], { name: "杭州市" })])), true);
  await engine.drain();
  assert.deepEqual(singles, []);
  assert.equal(errors.length, 1);
  assert.ok(errors[0] instanceof AtomicBatchUnavailable);
  assert.match(describeApiError(errors[0]), /仍完整保留在本机/);
  // The accepted change stays unsent, with its intent.
  assert.equal(engine.pending, true);
  assert.equal(engine.cache.atomic, true);
  assert.equal(engine.cache.saved.visits[0].trips.length, 2);
  // An ordinary change keeps using the single-operation path.
  const plain = new SyncEngine(api, { saved: base, revision: 1 }, {});
  plain.push(saved([city("330100", [trip("x")], { name: "杭州市" })]));
  await plain.drain();
  assert.equal(singles.length, 1);
});

test("a stored atomic flag must be exactly true and only exists in the current cache layout", () => {
  const good = { schema: 2, saved: saved(), confirmed: saved(), revision: 1, atomic: true };
  for (const bad of [{ ...good, atomic: false }, { ...good, atomic: "yes" }, { saved: v1([]), confirmed: v1([]), revision: 1, atomic: true }]) {
    const store = memory({ [cacheKey("u")]: JSON.stringify(bad) });
    assert.throws(() => loadCloudCache(store, "u", validateAny), /无法读取/, JSON.stringify(bad).slice(0, 40));
    assert.notEqual(store.getItem(cacheKey("u")), null, "an unreadable cache is kept, not deleted");
  }
});
