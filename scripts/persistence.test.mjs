import test from "node:test";
import assert from "node:assert/strict";
import {
  LEGACY_LOCAL_KEY,
  LEGACY_SEEN_KEY,
  LOCAL_KEY,
  acknowledgeLegacy,
  createLocalWriter,
  digestText,
  loadLocalSaved,
  preserveConflict,
  preserveUnreadable,
  readLegacyChange,
  recoverSaved,
  validateSaved,
} from "../src/lib/persistence.ts";

const regions = [
  { code: "330000", cities: [{ code: "330100", name: "杭州市" }, { code: "330200", name: "宁波市" }] },
  { code: "110000", cities: [{ code: "110101", name: "东城区" }] },
];
const KEY = LEGACY_LOCAL_KEY;
const green = "#4b937d";
// A version 1 record, which carried one date and note on the city itself.
const visit = (extra = {}) => ({
  code: "330100", name: "杭州市", province: "330000", color: green, date: "2026-10-01", note: "西湖", ...extra,
});
// The same record as a version 2 city with its first trip.
const city = (extra = {}) => ({
  code: "330100", name: "杭州市", province: "330000", color: green,
  trips: [{ id: "legacy-330100", date: "2026-10-01", note: "西湖" }], ...extra,
});
// What an earlier version stored: input this version still reads, in the key it never writes.
const storage = (value) => ({ getItem: (key) => (key === LEGACY_LOCAL_KEY ? value : null) });
// What this version itself stores.
const canonical = (value) => ({ getItem: (key) => (key === LOCAL_KEY ? value : null) });
const memory = (initial = {}) => {
  const data = new Map(Object.entries(initial));
  return {
    data,
    getItem: (k) => (data.has(k) ? data.get(k) : null),
    setItem: (k, v) => void data.set(k, String(v)),
  };
};

test("valid version 2 data loads unchanged", () => {
  const data = { version: 2, visits: [city()], provinces: { 110000: green } };
  const raw = JSON.stringify(data);
  const result = loadLocalSaved(canonical(raw), regions);
  assert.equal(result.status, "ok");
  assert.deepEqual(result.saved, data);
  assert.equal(result.raw, raw);
  assert.equal(result.source, "v2");
  assert.equal(result.canonicalRaw, raw);
  assert.equal(result.legacyChange, undefined);
});

test("version 1 data converts to one trip per city, empty ones included, and keeps its raw text", () => {
  const data = { version: 1, visits: [visit(), visit({ code: "330200", name: "宁波市", date: "", note: "" })], provinces: { 110000: green } };
  const raw = JSON.stringify(data);
  const result = loadLocalSaved(storage(raw), regions);
  assert.equal(result.status, "ok");
  // Read from the legacy key, which stays as it is: the first save goes to the canonical key.
  assert.equal(result.source, "legacy");
  assert.equal(result.raw, raw);
  assert.equal(result.canonicalRaw, null);
  assert.deepEqual(result.saved, {
    version: 2,
    visits: [city(), city({ code: "330200", name: "宁波市", trips: [{ id: "legacy-330200", date: "", note: "" }] })],
    provinces: { 110000: green },
  });
});

test("reading never writes: the stored text is left for the writer to replace", () => {
  const writes = [];
  const raw = JSON.stringify({ version: 1, visits: [visit()], provinces: {} });
  loadLocalSaved({ getItem: () => raw, setItem: (...a) => writes.push(a) }, regions);
  assert.deepEqual(writes, []);
});

test("missing or blank cache is a genuine empty start", () => {
  for (const value of [null, ""]) {
    const result = loadLocalSaved(storage(value), regions);
    assert.equal(result.status, "empty");
    assert.deepEqual(result.saved, { version: 2, visits: [], provinces: {} });
  }
});

test("unavailable storage is reported separately from unreadable data", () => {
  const result = loadLocalSaved({ getItem: () => { throw new Error("denied"); } }, regions);
  assert.equal(result.status, "unavailable");
});

test("corrupt JSON keeps the raw string and offers no recovery", () => {
  const raw = '{"version":1,"visits":[{"code":"3301';
  const result = loadLocalSaved(storage(raw), regions);
  assert.equal(result.status, "unreadable");
  assert.equal(result.raw, raw);
  assert.match(result.reason, /JSON/);
  assert.equal(result.recovery, null);
});

test("an unknown newer version is unreadable and not recoverable", () => {
  const raw = JSON.stringify({ version: 3, visits: [visit()], provinces: {} });
  const result = loadLocalSaved(storage(raw), regions);
  assert.equal(result.status, "unreadable");
  assert.equal(result.raw, raw);
  assert.equal(result.recovery, null);
});

test("a record that fails validation makes the whole read unreadable but recoverable", () => {
  const raw = JSON.stringify({
    version: 1,
    visits: [visit(), visit({ code: "330200", name: "错误名称" }), visit({ note: "重复" })],
    provinces: { 110000: green, 999999: green, 330000: "#000000" },
  });
  const result = loadLocalSaved(storage(raw), regions);
  assert.equal(result.status, "unreadable");
  assert.equal(result.raw, raw);
  assert.equal(result.recovery.saved.visits.length, 1);
  assert.equal(result.recovery.saved.visits[0].trips[0].note, "西湖");
  assert.deepEqual(result.recovery.saved.provinces, { 110000: green });
  assert.equal(result.recovery.discardedVisits, 2);
  // Version 1 cities carried one trip each; the two dropped cities took theirs along.
  assert.equal(result.recovery.discardedTrips, 2);
  assert.equal(result.recovery.discardedProvinces, 2);
});

test("recovery returns null when nothing is usable", () => {
  const raw = JSON.stringify({ version: 1, visits: [visit({ color: "red" })], provinces: {} });
  assert.equal(recoverSaved(raw, regions), null);
  assert.equal(recoverSaved("null", regions), null);
  assert.equal(recoverSaved(JSON.stringify({ version: 1, visits: {} }), regions), null);
});

test("non-object records are rejected instead of throwing", () => {
  assert.throws(() => validateSaved({ version: 1, visits: [null], provinces: {} }, regions), /无效/);
  assert.throws(() => validateSaved([], regions), /格式/);
});

test("overlong notes are measured in UTF-16 code units like the editor", () => {
  const ok = { version: 1, visits: [visit({ note: "a".repeat(1000) })], provinces: {} };
  assert.doesNotThrow(() => validateSaved(ok, regions));
  const bad = { version: 1, visits: [visit({ note: "a".repeat(1001) })], provinces: {} };
  assert.throws(() => validateSaved(bad, regions));
});

test("unreadable data is copied to a timestamped key and failures are reported", () => {
  const written = {};
  const ok = preserveUnreadable({ setItem: (k, v) => { written[k] = v; } }, KEY, "raw", new Date(1700000000000));
  assert.equal(ok, true);
  assert.deepEqual(written, { "shanhai-travel-v1-unreadable-1700000000000": "raw" });
  // A v2 text is kept next to its own key.
  const v2 = {};
  preserveUnreadable({ setItem: (k, v) => { v2[k] = v; } }, LOCAL_KEY, "raw", new Date(1700000000000));
  assert.deepEqual(v2, { "shanhai-travel-v2-unreadable-1700000000000": "raw" });
  assert.equal(preserveUnreadable({ setItem: () => { throw new Error("quota"); } }, KEY, "raw"), false);
});

test("recovery counts the trips of dropped version 1 and version 2 cities", () => {
  const v1 = JSON.stringify({ version: 1, visits: [visit(), visit({ code: "330200", name: "宁波市", date: "2026-02-30" }), visit({ code: "999999" })], provinces: {} });
  const r1 = recoverSaved(v1, regions);
  assert.equal(r1.saved.visits.length, 1);
  assert.equal(r1.discardedVisits, 2);
  assert.equal(r1.discardedTrips, 2, "the impossible-date city and the unknown city each lose their one trip");
  const v2 = JSON.stringify({
    version: 2,
    visits: [
      city(),
      city({ code: "330200", name: "宁波市", trips: [{ id: "a", date: "", note: "" }, { id: "b", date: "2026-02-30", note: "x" }] }),
      city({ code: "999999", trips: [{ id: "c", date: "", note: "" }, { id: "d", date: "", note: "" }] }),
    ],
    provinces: {},
  });
  const r2 = recoverSaved(v2, regions);
  assert.equal(r2.discardedVisits, 1);
  assert.equal(r2.discardedTrips, 3, "one impossible date in a kept city and two trips of the unknown city");
  const fixed = recoverSaved(v2, regions, { correctDates: true });
  assert.equal(fixed.correctedDates, 1);
  assert.equal(fixed.discardedTrips, 2);
});

test("the canonical key wins over the legacy key, and an unchanged legacy key is not reported", () => {
  const legacyRaw = JSON.stringify({ version: 1, visits: [visit()], provinces: {} });
  const v2Raw = JSON.stringify({ version: 2, visits: [city({ color: "#e2af5c" })], provinces: {} });
  const s = memory({ [LOCAL_KEY]: v2Raw, [LEGACY_LOCAL_KEY]: legacyRaw, [LEGACY_SEEN_KEY]: digestText(legacyRaw) });
  const load = loadLocalSaved(s, regions);
  assert.equal(load.status, "ok");
  assert.equal(load.source, "v2");
  assert.equal(load.saved.visits[0].color, "#e2af5c");
  assert.equal(load.legacyChange, undefined);
});

test("first save upgrades into the canonical key and leaves the legacy bytes exactly as they were", () => {
  const legacyRaw = JSON.stringify({ version: 1, visits: [visit()], provinces: {} });
  const s = memory({ [LEGACY_LOCAL_KEY]: legacyRaw });
  const load = loadLocalSaved(s, regions);
  const writer = createLocalWriter(s, load.canonicalRaw, load.legacyDigest);
  assert.deepEqual(s.data.size, 1, "loading wrote nothing");
  const next = { ...load.saved, provinces: { 110000: green } };
  assert.deepEqual(writer.write(next), { status: "ok" });
  assert.equal(s.getItem(LEGACY_LOCAL_KEY), legacyRaw, "legacy bytes untouched");
  assert.equal(s.getItem(LOCAL_KEY), JSON.stringify(next));
  assert.equal(s.getItem(LEGACY_SEEN_KEY), digestText(legacyRaw));
  assert.deepEqual([...s.data.keys()].sort(), [LEGACY_LOCAL_KEY, LEGACY_SEEN_KEY, LOCAL_KEY].sort(), "no stray copies");
  const again = loadLocalSaved(s, regions);
  assert.equal(again.source, "v2");
  assert.equal(again.legacyChange, undefined);
});

test("an older page that overwrites the legacy key cannot touch the canonical data, and is reported on the next start", () => {
  const legacyRaw = JSON.stringify({ version: 1, visits: [visit()], provinces: {} });
  const s = memory({ [LEGACY_LOCAL_KEY]: legacyRaw });
  const load = loadLocalSaved(s, regions);
  const writer = createLocalWriter(s, load.canonicalRaw, load.legacyDigest);
  // Version 2 work that only exists on this device.
  const work = {
    version: 2,
    visits: [city({ trips: [{ id: "legacy-330100", date: "2026-10-01", note: "西湖" }, { id: "t-second", date: "2027-01-02", note: "只在本机" }] })],
    provinces: {},
  };
  writer.write(work);
  const canonicalBefore = s.getItem(LOCAL_KEY);
  // The v2 page is closed; an older page still open writes the key it knows.
  const olderPage = JSON.stringify({ version: 1, visits: [visit({ note: "旧页面改的" }), visit({ code: "330200", name: "宁波市", date: "", note: "旧页面新增" })], provinces: {} });
  s.setItem(LEGACY_LOCAL_KEY, olderPage);
  assert.equal(s.getItem(LOCAL_KEY), canonicalBefore, "canonical data is not reachable by the older page");
  const reopened = loadLocalSaved(s, regions);
  assert.equal(reopened.source, "v2");
  assert.deepEqual(reopened.saved, work, "every unsynced v2 record is still there");
  assert.equal(reopened.legacyChange.raw, olderPage, "the older page's text is offered as stored");
  assert.equal(reopened.legacyChange.saved.visits.length, 2);
  // Acknowledging records the digest; the older text stays where it is.
  assert.equal(acknowledgeLegacy(s, reopened.legacyChange && digestText(olderPage)), true);
  assert.equal(s.getItem(LEGACY_LOCAL_KEY), olderPage);
  assert.equal(loadLocalSaved(s, regions).legacyChange, undefined);
  assert.equal(readLegacyChange(s, regions), null);
  // A further write by the older page is noticed again.
  s.setItem(LEGACY_LOCAL_KEY, olderPage + " ");
  assert.ok(readLegacyChange(s, regions));
});

test("an unreadable legacy change is offered for export only", () => {
  const legacyRaw = JSON.stringify({ version: 1, visits: [visit()], provinces: {} });
  const s = memory({ [LOCAL_KEY]: JSON.stringify({ version: 2, visits: [], provinces: {} }), [LEGACY_LOCAL_KEY]: "{broken", [LEGACY_SEEN_KEY]: digestText(legacyRaw) });
  const load = loadLocalSaved(s, regions);
  assert.equal(load.legacyChange.raw, "{broken");
  assert.equal(load.legacyChange.saved, null);
});

test("an unreadable legacy key is never overwritten: resolving writes the canonical key only", () => {
  const raw = '{"version":1,"visits":[';
  const s = memory({ [LEGACY_LOCAL_KEY]: raw });
  const load = loadLocalSaved(s, regions);
  assert.equal(load.status, "unreadable");
  assert.equal(load.source, "legacy");
  assert.equal(load.sourceKey, LEGACY_LOCAL_KEY);
  assert.equal(preserveUnreadable(s, load.sourceKey, load.raw, new Date(5)), true);
  const writer = createLocalWriter(s, load.canonicalRaw, load.legacyDigest);
  assert.deepEqual(writer.write({ version: 2, visits: [], provinces: {} }), { status: "ok" });
  assert.equal(s.getItem(LEGACY_LOCAL_KEY), raw);
  assert.equal(s.getItem("shanhai-travel-v1-unreadable-5"), raw);
  assert.equal(loadLocalSaved(s, regions).status, "ok");
});

test("the writer refuses to overwrite another page's canonical write and keeps its text for backup", () => {
  const s = memory({ [LOCAL_KEY]: JSON.stringify({ version: 2, visits: [], provinces: {} }) });
  const load = loadLocalSaved(s, regions);
  const writer = createLocalWriter(s, load.canonicalRaw);
  const theirs = JSON.stringify({ version: 2, visits: [city()], provinces: {} });
  s.setItem(LOCAL_KEY, theirs);
  const result = writer.write({ version: 2, visits: [], provinces: { 110000: green } });
  assert.deepEqual(result, { status: "conflict", other: theirs });
  assert.equal(s.getItem(LOCAL_KEY), theirs, "nothing was written");
  // The text that would be lost is the other page's, and that is what gets preserved.
  assert.equal(preserveConflict(s, theirs, new Date(7)), true);
  assert.equal(s.getItem("shanhai-travel-v2-conflict-7"), theirs);
  writer.adopt(theirs);
  assert.equal(writer.write({ version: 2, visits: [], provinces: {} }).status, "ok");
});

test("the first write is refused when the legacy digest cannot be recorded, so nothing looks unaccounted for", () => {
  const legacyRaw = JSON.stringify({ version: 1, visits: [visit()], provinces: {} });
  const s = memory({ [LEGACY_LOCAL_KEY]: legacyRaw });
  const real = s.setItem;
  s.setItem = (k, v) => {
    if (k === LEGACY_SEEN_KEY) throw new Error("quota");
    real(k, v);
  };
  const load = loadLocalSaved(s, regions);
  const writer = createLocalWriter(s, load.canonicalRaw, load.legacyDigest);
  assert.deepEqual(writer.write(load.saved), { status: "error" });
  assert.equal(s.getItem(LOCAL_KEY), null);
});
