import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { validateSaved, recoverSaved, loadLocalSaved } from "../src/lib/persistence.ts";
import { loadCloudCache, cacheKey } from "../src/lib/session.ts";
import { diffSaved } from "../src/lib/sync.ts";

const regions = JSON.parse(readFileSync("src/regions.json", "utf8"));
const legacy = () => ({
  version: 1,
  visits: [
    { code: "tw-09020", name: "金门县", province: "710000", color: "#4b937d", date: "2026-10-12", note: "金门旅行" },
    { code: "tw-09007", name: "连江县", province: "710000", color: "#719fc6", date: "", note: "马祖" },
  ],
  provinces: { "710000": "#e2af5c" },
});

test("Kinmen and Matsu belong only to Fujian in the current region index", () => {
  for (const code of ["tw-09020", "tw-09007"]) {
    assert.equal(regions.find(p => p.cities.some(c => c.code === code)).code, "350000");
  }
});

test("legacy backup and local records migrate without changing ID or travel details", () => {
  const input = legacy();
  const saved = validateSaved(input, regions);
  assert.deepEqual(saved.visits, input.visits.map(v => ({ ...v, province: "350000" })));
  assert.deepEqual(saved.provinces, input.provinces);
  assert.equal(input.visits[0].province, "710000", "do not mutate caller data");
  assert.deepEqual(validateSaved(saved, regions), saved, "migration is idempotent");
  const raw = JSON.stringify(input);
  const result = loadLocalSaved({ getItem: () => raw }, "test", regions);
  assert.equal(result.status, "ok");
  assert.deepEqual(result.saved, saved);
});

test("recovery retains old island records while still discarding invalid records", () => {
  const input = legacy();
  input.visits.push({ ...input.visits[0], code: "invalid" });
  const result = recoverSaved(JSON.stringify(input), regions);
  assert.equal(result.discardedVisits, 1);
  assert.deepEqual(result.saved, validateSaved(legacy(), regions));
});

test("wrong names, other wrong provinces, invalid colors and duplicates remain rejected", () => {
  for (const patch of [{ name: "伪造" }, { province: "330000" }, { color: "red" }]) {
    const input = legacy();
    Object.assign(input.visits[0], patch);
    assert.throws(() => validateSaved(input, regions));
  }
  const input = legacy();
  input.visits.push({ ...input.visits[0], province: "350000" });
  assert.throws(() => validateSaved(input, regions));
});

test("both cloud cache copies migrate, preserving pending edits and revision", () => {
  const confirmed = legacy();
  const saved = legacy();
  saved.visits[0].note = "离线修改";
  const raw = JSON.stringify({ saved, confirmed, revision: 17 });
  const cache = loadCloudCache({ getItem: key => key === cacheKey("u") ? raw : null }, "u", data => validateSaved(data, regions));
  assert.equal(cache.revision, 17);
  assert.equal(cache.saved.visits[0].province, "350000");
  assert.equal(cache.confirmed.visits[0].province, "350000");
  const ops = diffSaved(cache.confirmed, cache.saved);
  assert.equal(ops.length, 1);
  assert.equal(ops[0].visit.note, "离线修改");
});
