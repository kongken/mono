import assert from "node:assert/strict";
import { createHash, randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import catalog from "@kongken/geo-data/world-catalog.json" with { type: "json" };
import regions from "@kongken/geo-data/regions.json" with { type: "json" };
import { sha256Fallback, sha256Hex } from "../src/lib/sha256.ts";
import {
  createVerifiedFetch,
  deleteOtherWorldVersions,
  prepareWorldOffline,
  shellResources,
  worldDataCacheName,
  WORLD_DATA_CACHE_PREFIX,
  WORLD_SHELL_CACHE,
} from "../src/lib/world-offline.ts";
import { loadWorldMap, WorldMapError } from "../src/lib/world-map.ts";

const base = "/";
const worldUrl = `${base}${catalog.geometry.file}`;
const chinaUrl = `${base}${catalog.china.file}`;
const world = readFileSync("public/maps/world.json");
const china = readFileSync("public/maps/100000.json");
const bytes = (file) => file.buffer.slice(file.byteOffset, file.byteOffset + file.byteLength);

/** An in-memory CacheStorage: enough of Cache and CacheStorage for the offline module. */
function memoryCaches(initial = {}) {
  const stores = new Map(Object.entries(initial).map(([name, entries]) => [name, new Map(Object.entries(entries))]));
  const cache = (entries) => ({
    async match(url) {
      const hit = entries.get(typeof url === "string" ? url : url.url);
      return hit ? new Response(hit.slice(0)) : undefined;
    },
    async put(url, response) {
      entries.set(typeof url === "string" ? url : url.url, await response.arrayBuffer());
    },
    async delete(url) {
      return entries.delete(typeof url === "string" ? url : url.url);
    },
    async keys() {
      return [...entries.keys()].map((url) => ({ url }));
    },
  });
  return {
    stores,
    async open(name) {
      if (!stores.has(name)) stores.set(name, new Map());
      return cache(stores.get(name));
    },
    async keys() {
      return [...stores.keys()];
    },
    async delete(name) {
      return stores.delete(name);
    },
  };
}
const network = (files) => {
  const calls = [];
  const fetchImpl = async (url) => {
    calls.push(url);
    const file = files[url];
    return file ? new Response(file.slice(0), { status: 200 }) : new Response("", { status: 404 });
  };
  return { fetchImpl, calls };
};
const offline = { calls: [], fetchImpl: async () => Promise.reject(new TypeError("Failed to fetch")) };
const wanted = { catalog, baseUrl: base };

test("the fallback SHA-256 equals Node's for empty, short, block-boundary and large inputs", async () => {
  for (const length of [0, 1, 3, 55, 56, 57, 63, 64, 65, 119, 120, 1000, 100_000]) {
    const input = randomBytes(length);
    const expected = createHash("sha256").update(input).digest("hex");
    assert.equal(sha256Fallback(new Uint8Array(input)), expected, `length ${length}`);
    assert.equal(await sha256Hex(bytes(input)), expected, `length ${length} (SubtleCrypto)`);
  }
  assert.equal(sha256Fallback(new Uint8Array(world)), catalog.geometry.sha256, "the shipped world map has the recorded digest");
  assert.equal(sha256Fallback(new Uint8Array(china)), catalog.china.sha256, "the shipped national map has the recorded digest");
});

test("online, a map file is fetched, verified, cached under this catalog version, then served from the cache", async () => {
  const caches = memoryCaches();
  const net = network({ [worldUrl]: bytes(world), [chinaUrl]: bytes(china) });
  const verified = createVerifiedFetch({ ...wanted, caches, fetchImpl: net.fetchImpl });
  const response = await verified(worldUrl);
  assert.equal(Buffer.from(await response.arrayBuffer()).equals(world), true);
  assert.deepEqual([...caches.stores.keys()], [worldDataCacheName(catalog)]);
  assert.deepEqual(net.calls, [worldUrl]);
  // The second read is the cache's: no request leaves the page.
  const again = await verified(worldUrl);
  assert.equal(again.ok, true);
  assert.deepEqual(net.calls, [worldUrl]);
  // And once cached, the network may be gone.
  const gone = createVerifiedFetch({ ...wanted, caches, fetchImpl: offline.fetchImpl });
  assert.equal((await gone(worldUrl)).ok, true);
  // The whole map loads through it, offline, after both files were seen online.
  await createVerifiedFetch({ ...wanted, caches, fetchImpl: net.fetchImpl })(chinaUrl);
  const loaded = await loadWorldMap(base, catalog, regions.map((r) => r.code), undefined, gone);
  assert.equal(loaded.countries.length, catalog.countries.filter((c) => c.hasShape && c.code !== "CN").length);
  assert.equal(loaded.china.length, 34);
});

test("a cached copy that does not match the catalog is deleted and never used, offline or not", async () => {
  const damaged = bytes(Buffer.concat([world.subarray(0, world.length - 10), Buffer.from("0000000000")]));
  const caches = memoryCaches({ [worldDataCacheName(catalog)]: { [worldUrl]: damaged } });
  const gone = createVerifiedFetch({ ...wanted, caches, fetchImpl: offline.fetchImpl });
  await assert.rejects(() => gone(worldUrl), WorldMapError);
  assert.equal(caches.stores.get(worldDataCacheName(catalog)).has(worldUrl), false, "the damaged copy is dropped");
  // Online it is replaced by a good one.
  const net = network({ [worldUrl]: bytes(world) });
  const online = createVerifiedFetch({ ...wanted, caches: memoryCaches({ [worldDataCacheName(catalog)]: { [worldUrl]: damaged } }), fetchImpl: net.fetchImpl });
  assert.equal((await online(worldUrl)).ok, true);
  assert.deepEqual(net.calls, [worldUrl]);
});

test("a network copy that does not match is refused and not cached; an error status and a lost network are refused too", async () => {
  const caches = memoryCaches();
  const truncated = bytes(world.subarray(0, 1000));
  const net = network({ [worldUrl]: truncated });
  await assert.rejects(() => createVerifiedFetch({ ...wanted, caches, fetchImpl: net.fetchImpl })(worldUrl), /校验值不一致/);
  assert.equal(caches.stores.get(worldDataCacheName(catalog))?.size ?? 0, 0, "nothing incomplete is cached");
  await assert.rejects(() => createVerifiedFetch({ ...wanted, caches, fetchImpl: network({}).fetchImpl })(worldUrl), /404/);
  await assert.rejects(() => createVerifiedFetch({ ...wanted, caches, fetchImpl: offline.fetchImpl })(worldUrl), /网络不可用/);
});

test("another catalog version never reads this version's cache; old versions are deleted after a load", async () => {
  const mine = worldDataCacheName(catalog);
  const next = { ...catalog, version: "sha256:next" };
  const caches = memoryCaches({ [mine]: { [worldUrl]: bytes(world), [chinaUrl]: bytes(china) } });
  // The upgraded catalog has the same digests here, so only the cache name keeps the old copy out.
  const upgraded = createVerifiedFetch({ catalog: next, baseUrl: base, caches, fetchImpl: offline.fetchImpl });
  await assert.rejects(() => upgraded(worldUrl), WorldMapError);
  assert.notEqual(worldDataCacheName(next), mine);
  const other = memoryCaches({
    [mine]: { a: new ArrayBuffer(1) },
    [worldDataCacheName(next)]: { a: new ArrayBuffer(1) },
    [WORLD_SHELL_CACHE]: { a: new ArrayBuffer(1) },
    "shanhai-travel-v2": { a: new ArrayBuffer(1) },
    unrelated: { a: new ArrayBuffer(1) },
  });
  assert.deepEqual(await deleteOtherWorldVersions(next, other), [mine]);
  assert.deepEqual([...other.stores.keys()].sort(), [worldDataCacheName(next), WORLD_SHELL_CACHE, "shanhai-travel-v2", "unrelated"].sort());
  assert.ok(mine.startsWith(WORLD_DATA_CACHE_PREFIX));
});

test("only the two map files can be asked for, and a broken cache is a miss, not an error", async () => {
  const net = network({ [worldUrl]: bytes(world), "https://example.com/maps/world.json": bytes(world) });
  const verified = createVerifiedFetch({ ...wanted, fetchImpl: net.fetchImpl });
  for (const url of ["https://example.com/maps/world.json", "/maps/110000.json", "/maps/world.json?x=1", "/api/anything"])
    await assert.rejects(() => verified(url), WorldMapError, url);
  assert.deepEqual(net.calls, [], "nothing is requested for them");
  const broken = { open: async () => Promise.reject(new Error("quota")), keys: async () => [], delete: async () => false };
  assert.equal((await createVerifiedFetch({ ...wanted, caches: broken, fetchImpl: net.fetchImpl })(worldUrl)).ok, true);
});

test("the shell lists the page and the hashed files it loaded, same origin only", () => {
  const origin = "https://trip.example";
  const list = shellResources(
    [
      "https://trip.example/assets/index-abc.js",
      "https://trip.example/assets/index-abc.js?v=2",
      "https://trip.example/assets/WorldView-1.css",
      "https://trip.example/favicon.svg",
      "https://trip.example/maps/world.json",
      "https://trip.example/maps/100000.json",
      "https://trip.example/api/bff.v1.AuthService/Me",
      "https://cdn.example/assets/evil.js",
      "https://tile.openstreetmap.org/1/2/3.png",
      "not a url at all \u0000",
    ],
    origin,
    "/",
  );
  assert.deepEqual(list, [
    "https://trip.example/",
    "https://trip.example/assets/WorldView-1.css",
    "https://trip.example/assets/index-abc.js",
    "https://trip.example/favicon.svg",
  ]);
  assert.deepEqual(shellResources(["https://trip.example/app/assets/a.js", "https://trip.example/assets/b.js"], origin, "/app/"), [
    "https://trip.example/app/",
    "https://trip.example/app/assets/a.js",
  ]);
});

test("preparing the offline cache registers the worker, fills the shell and drops what the page no longer uses", async () => {
  const origin = "https://trip.example";
  const files = {
    "https://trip.example/": new TextEncoder().encode("<html></html>").buffer,
    "https://trip.example/assets/a.js": new ArrayBuffer(3),
  };
  const net = network(files);
  const caches = memoryCaches({ [WORLD_SHELL_CACHE]: { "https://trip.example/assets/old.js": new ArrayBuffer(1) } });
  const registered = [];
  const serviceWorker = { register: async (url) => registered.push(url), ready: Promise.resolve({}) };
  const env = { baseUrl: "/", origin, loaded: ["https://trip.example/assets/a.js", "https://cdn.example/x.js"], caches, serviceWorker, fetchImpl: net.fetchImpl };
  assert.equal(await prepareWorldOffline(env), "ready");
  assert.deepEqual(registered, ["/sw.js"]);
  assert.deepEqual([...caches.stores.get(WORLD_SHELL_CACHE).keys()].sort(), Object.keys(files).sort());
  assert.deepEqual(net.calls.sort(), Object.keys(files).sort(), "only same-origin files were requested");
  // No Cache Storage or no worker support: nothing is attempted, and it is reported.
  assert.equal(await prepareWorldOffline({ ...env, caches: undefined }), "unsupported");
  assert.equal(await prepareWorldOffline({ ...env, serviceWorker: undefined }), "unsupported");
  // A file that cannot be fetched fails the whole preparation, and the old shell is kept as it was.
  const half = memoryCaches({ [WORLD_SHELL_CACHE]: { "https://trip.example/assets/old.js": new ArrayBuffer(1) } });
  assert.equal(await prepareWorldOffline({ ...env, caches: half, fetchImpl: network({ "https://trip.example/": files["https://trip.example/"] }).fetchImpl }), "failed");
  assert.deepEqual([...half.stores.get(WORLD_SHELL_CACHE).keys()].includes("https://trip.example/assets/old.js"), true);
});
