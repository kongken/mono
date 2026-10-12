import test from "node:test";
import assert from "node:assert/strict";
import {
  loadLocalSaved,
  preserveUnreadable,
  recoverSaved,
  validateSaved,
} from "../src/lib/persistence.ts";

const regions = [
  { code: "330000", cities: [{ code: "330100", name: "杭州市" }, { code: "330200", name: "宁波市" }] },
  { code: "110000", cities: [{ code: "110101", name: "东城区" }] },
];
const KEY = "shanhai-travel-v1";
const green = "#4b937d";
const visit = (extra = {}) => ({
  code: "330100", name: "杭州市", province: "330000", color: green, date: "2026-10-01", note: "西湖", ...extra,
});
const storage = (value) => ({ getItem: () => value });

test("valid data loads unchanged", () => {
  const data = { version: 1, visits: [visit()], provinces: { 110000: green } };
  const result = loadLocalSaved(storage(JSON.stringify(data)), KEY, regions);
  assert.equal(result.status, "ok");
  assert.deepEqual(result.saved, data);
});

test("missing or blank cache is a genuine empty start", () => {
  for (const value of [null, ""]) {
    const result = loadLocalSaved(storage(value), KEY, regions);
    assert.equal(result.status, "empty");
    assert.deepEqual(result.saved, { version: 1, visits: [], provinces: {} });
  }
});

test("unavailable storage is reported separately from unreadable data", () => {
  const result = loadLocalSaved({ getItem: () => { throw new Error("denied"); } }, KEY, regions);
  assert.equal(result.status, "unavailable");
});

test("corrupt JSON keeps the raw string and offers no recovery", () => {
  const raw = '{"version":1,"visits":[{"code":"3301';
  const result = loadLocalSaved(storage(raw), KEY, regions);
  assert.equal(result.status, "unreadable");
  assert.equal(result.raw, raw);
  assert.match(result.reason, /JSON/);
  assert.equal(result.recovery, null);
});

test("unsupported version is unreadable and not recoverable", () => {
  const raw = JSON.stringify({ version: 2, visits: [visit()], provinces: {} });
  const result = loadLocalSaved(storage(raw), KEY, regions);
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
  const result = loadLocalSaved(storage(raw), KEY, regions);
  assert.equal(result.status, "unreadable");
  assert.equal(result.raw, raw);
  assert.equal(result.recovery.saved.visits.length, 1);
  assert.equal(result.recovery.saved.visits[0].note, "西湖");
  assert.deepEqual(result.recovery.saved.provinces, { 110000: green });
  assert.equal(result.recovery.discardedVisits, 2);
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
  assert.equal(preserveUnreadable({ setItem: () => { throw new Error("quota"); } }, KEY, "raw"), false);
});
