import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createRegionCatalog, REGION_VERSION, regionCatalog, stampRegions } from "../src/lib/region-catalog.ts";
import { LOCAL_KEY, createLocalWriter, loadLocalSaved, regionOriginalPrefix, recoverSaved, validateSaved } from "../src/lib/persistence.ts";
import { cacheKey, clearAccountData, loadCloudCache, saveCloudCache } from "../src/lib/session.ts";
import { projectRegionOps } from "../src/lib/region-operations.ts";
import { applyDelta } from "../src/lib/delta.ts";
import { SyncEngine, applyOp } from "../src/lib/sync.ts";
import { snapshotToSaved } from "../src/lib/snapshots.ts";

const regions = JSON.parse(readFileSync("src/regions.json", "utf8"));
const metadata = JSON.parse(readFileSync("src/region-catalog.json", "utf8"));
const fixture = JSON.parse(readFileSync("scripts/fixtures/region-upgrades.json", "utf8"));
const catalog = createRegionCatalog(regions, { ...metadata, mappings: [...metadata.mappings, fixture.mapping] });
const green = "#4b937d";
const old = { code: fixture.mapping.fromCode, name: fixture.mapping.fromName, province: fixture.mapping.fromProvince, color: green, date: "2026-10-14", note: "保留旧旅行 🏔️" };
const saved = (visits = []) => ({ version: 2, visits, provinces: { 710000: green } });
const validate = data => validateSaved(data, regions);
const v1 = { version: 1, visits: [old], provinces: { 710000: green } };
const expected = saved([{ ...fixture.current, color: green, trips: [{ id: "legacy-old-hangzhou", date: old.date, note: old.note }] }]);
function memory(initial = {}) {
  const data = new Map(Object.entries(initial));
  return { data, get length() { return data.size; }, key: i => [...data.keys()][i] ?? null, getItem: k => data.get(k) ?? null, setItem: (k, v) => { data.set(k, String(v)); }, removeItem: k => { data.delete(k); } };
}

test("region catalog digest and both generated copies are current", () => {
  execFileSync(process.execPath, ["scripts/prepare-region-catalog.mjs", "--check"]);
  const expectedHash = "sha256:" + createHash("sha256").update(JSON.stringify({ regions, mappings: metadata.mappings })).digest("hex");
  assert.equal(REGION_VERSION, expectedHash);
  assert.equal(readFileSync("internal/regions/catalog.json", "utf8"), readFileSync("src/region-catalog.json", "utf8"));
  assert.equal(metadata.mappings.length, 2, "do not ship fictitious retired IDs");
  const dockerfile = readFileSync("deploy/frontend/Dockerfile", "utf8");
  for (const file of ["internal/regions/regions.json", "internal/regions/catalog.json"]) assert.ok(dockerfile.includes(`COPY ${file} ${file}`), `Frontend image must copy ${file} before executing this check`);
});

test("an exact retired tuple migrates v1/v2 without changing legacy trip IDs or user content", () => {
  assert.deepEqual(validateSaved(v1, regions, { catalog }), expected);
  const version2 = { ...expected, visits: [{ ...old, trips: expected.visits[0].trips }] };
  assert.deepEqual(validateSaved(version2, regions, { catalog }), expected);
  assert.deepEqual(catalog.city(old.code, old.name, old.province), fixture.current);
  assert.equal(catalog.city(old.code, "猜测的名字", old.province), null);
  assert.equal(catalog.city(old.code, old.name, "710000"), null);
  assert.equal(catalog.code(old.code), fixture.current.code);
  assert.equal(catalog.code("never-reviewed"), null);
});

test("alias/current ID collisions block full migration and partial salvage", () => {
  const collision = { ...v1, visits: [old, { ...fixture.current, color: green, date: "", note: "独立记录" }] };
  assert.throws(() => validateSaved(collision, regions, { catalog }), /碰撞/);
  assert.equal(recoverSaved(JSON.stringify(collision), regions, { catalog }), null);
});

test("unknown/future region versions keep raw caches and do not offer destructive recovery", () => {
  const raw = JSON.stringify({ ...saved(), regionVersion: "sha256:future" });
  const store = memory({ [LOCAL_KEY]: raw });
  const loaded = loadLocalSaved(store, regions);
  assert.equal(loaded.status, "unreadable");
  assert.equal(loaded.raw, raw);
  assert.equal(loaded.recovery, null);
  assert.equal(store.data.size, 1);
  assert.equal(recoverSaved(raw, regions), null);
  const accountRaw = JSON.stringify({ schema: 2, saved: saved(), confirmed: saved(), revision: 0, regionVersion: "future" });
  const account = memory({ [cacheKey("u")]: accountRaw });
  assert.throws(() => loadCloudCache(account, "u", validate), /保留原始/);
  assert.equal(account.getItem(cacheKey("u")), accountRaw);
});

test("first local region-version write preserves exact bytes once; opening alone never writes", () => {
  const raw = '{ "version": 2, "visits": [], "provinces": {"710000":"#4b937d"} }';
  const store = memory({ [LOCAL_KEY]: raw });
  const loaded = loadLocalSaved(store, regions);
  assert.equal(store.data.size, 1);
  const writer = createLocalWriter(store, loaded.canonicalRaw);
  assert.equal(writer.write(loaded.saved).status, "ok");
  const keys = [...store.data.keys()].filter(k => k.startsWith(regionOriginalPrefix(LOCAL_KEY)));
  assert.equal(keys.length, 1);
  assert.equal(store.getItem(keys[0]), raw);
  assert.equal(JSON.parse(store.getItem(LOCAL_KEY)).regionVersion, REGION_VERSION);
  assert.equal(writer.write(loaded.saved).status, "ok");
  assert.equal([...store.data.keys()].filter(k => k.startsWith(regionOriginalPrefix(LOCAL_KEY))).length, 1);
});

test("a failed preservation write cannot replace a local/cache original", () => {
  const raw = JSON.stringify(saved());
  const store = memory({ [LOCAL_KEY]: raw });
  const blocked = { ...store, setItem: (key, value) => { if (key.startsWith("shanhai-regions-original:")) throw new Error("quota"); store.setItem(key, value); } };
  assert.equal(createLocalWriter(blocked, raw).write(saved()).status, "error");
  assert.equal(store.getItem(LOCAL_KEY), raw);
  const accountRaw = JSON.stringify({ schema: 2, saved: saved(), confirmed: saved(), revision: 3 });
  store.setItem(cacheKey("u"), accountRaw);
  assert.equal(saveCloudCache(blocked, "u", { saved: saved(), confirmed: saved(), revision: 3 }), false);
  assert.equal(store.getItem(cacheKey("u")), accountRaw);
});

test("explicit F0 recovery after an exact external export does not require a second storage copy", () => {
  const raw = "unreadable source";
  const store = memory({ [LOCAL_KEY]: raw });
  const noCopies = { ...store, setItem: (key, value) => { if (key !== LOCAL_KEY) throw new Error("quota"); store.setItem(key, value); } };
  assert.equal(createLocalWriter(noCopies, raw, undefined, true).write(saved()).status, "ok");
  assert.equal(JSON.parse(store.getItem(LOCAL_KEY)).regionVersion, REGION_VERSION);
});

test("account preservation keeps pending requests verbatim and is cleared only for that account", () => {
  const city = { code: "tw-09020", name: "金门县", province: "710000", color: green };
  const confirmed = saved([{ ...city, trips: [] }]);
  const target = saved([{ ...city, trips: [{ id: "t-new", date: "", note: "离线" }] }]);
  const batch = { id: "original-batch", revision: 7, ops: [{ type: "upsertTrip", code: city.code, trip: target.visits[0].trips[0] }] };
  const raw = JSON.stringify({ schema: 2, saved: target, confirmed, revision: 7, pendingBatch: batch });
  const store = memory({ [cacheKey("u")]: raw });
  const loaded = loadCloudCache(store, "u", validate);
  assert.equal(loaded.saved.visits[0].province, "350000");
  assert.equal(loaded.confirmed.visits[0].province, "350000");
  assert.deepEqual(loaded.pendingBatch, batch);
  assert.equal(saveCloudCache(store, "u", loaded), true);
  assert.deepEqual(JSON.parse(store.getItem(cacheKey("u"))).pendingBatch, batch);
  const key = [...store.data.keys()].find(k => k.startsWith(regionOriginalPrefix(cacheKey("u"))));
  assert.equal(store.getItem(key), raw);
  store.setItem(regionOriginalPrefix(cacheKey("other")) + "test", "other account");
  clearAccountData(store, "u", validate, true);
  assert.equal(store.getItem(key), null);
  assert.equal(store.getItem(regionOriginalPrefix(cacheKey("other")) + "test"), "other account");
});

test("legacy mutation projection preserves its original trip ID and its raw request", () => {
  const ops = [{ type: "legacyUpsertVisit", visit: old }];
  const original = JSON.stringify(ops);
  let result = saved();
  for (const op of projectRegionOps(ops, catalog)) result = applyOp(result, op);
  assert.deepEqual(result, expected);
  assert.equal(JSON.stringify(ops), original);
});

test("one legacy journal revision may project to city and trip without changing pagination", () => {
  const page = { changes: [{ revision: 8, op: { type: "legacyUpsertVisit", visit: old } }], nextRevision: 8, currentRevision: 8, hasMore: false, resyncRequired: false };
  const result = applyDelta({ revision: 7, saved: saved() }, page, ops => projectRegionOps(ops, catalog));
  assert.equal(result.revision, 8);
  assert.deepEqual(result.saved, expected);
  assert.equal(page.changes.length, 1);
});

test("an acknowledged old pending batch projects only its ancestor, never its wire operations", async () => {
  const ops = [{ type: "legacyUpsertVisit", visit: old }];
  const original = JSON.stringify(ops);
  const initial = { saved: expected, confirmed: saved(), revision: 7, pendingBatch: { id: "old-batch", revision: 7, ops } };
  let sent = 0;
  const engine = new SyncEngine({
    get: async () => ({ saved: expected, revision: 8 }),
    apply: async () => { throw new Error("should not rewrite request"); },
    project: ops => projectRegionOps(ops, catalog),
    applyBatch: async (request, revision, id) => { sent++; assert.equal(JSON.stringify(request), original); assert.equal(id, "old-batch"); assert.equal(revision, 7); return 8; },
  }, { saved: initial.confirmed, revision: initial.revision }, {}, initial.saved, initial.pendingBatch);
  engine.retry();
  await engine.drain();
  assert.equal(sent, 1);
  assert.deepEqual(engine.confirmed.saved, expected);
  assert.equal(JSON.stringify(ops), original);
});

test("historical snapshot projections keep payloads and legacy trip IDs unchanged", () => {
  const wire = { visits: [old], provinceMarks: [{ province: "710000", color: green }] };
  const raw = JSON.stringify(wire);
  assert.deepEqual(snapshotToSaved(wire, catalog), expected);
  assert.equal(JSON.stringify(wire), raw);
  assert.throws(() => snapshotToSaved({ ...wire, regionVersion: "future" }, catalog), /未知/);
  assert.deepEqual(regionCatalog.city("tw-09007", "连江县", "710000"), { code: "tw-09007", name: "连江县", province: "350000" });
  assert.equal(stampRegions(expected).version, 2);
});
