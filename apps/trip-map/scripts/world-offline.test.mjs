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
  WORLD_SHELL_PREFIX,
  indexAssets,
  shellManifestUrl,
} from "../src/lib/world-offline.ts";
import { loadWorldMap, WorldMapError } from "../src/lib/world-map.ts";

const base = "/";
const worldUrl = `${base}${catalog.geometry.file}`;
const chinaUrl = `${base}${catalog.china.file}`;
const world = readFileSync("public/maps/world.json");
const china = readFileSync("public/maps/100000.json");
const bytes = (file) => file.buffer.slice(file.byteOffset, file.byteOffset + file.byteLength);

/** An in-memory CacheStorage: enough of Cache and CacheStorage for the offline module. */
function memoryCaches(initial = {}, fail = {}) {
  const stores = new Map(Object.entries(initial).map(([name, entries]) => [name, new Map(Object.entries(entries))]));
  const puts = [];
  const cache = (entries, cacheName) => ({
    async match(url) {
      const hit = entries.get(typeof url === "string" ? url : url.url);
      return hit ? new Response(hit.slice(0)) : undefined;
    },
    async put(url, response) {
      const key = typeof url === "string" ? url : url.url;
      puts.push([cacheName, key]);
      if (fail.put?.(cacheName, key)) throw new DOMException("quota", "QuotaExceededError");
      entries.set(key, await response.arrayBuffer());
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
    puts,
    async open(name) {
      if (!stores.has(name)) stores.set(name, new Map());
      return cache(stores.get(name), name);
    },
    async keys() {
      return [...stores.keys()];
    },
    async delete(name) {
      if (fail.delete?.(name)) throw new Error("delete failed");
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
    [`${WORLD_SHELL_PREFIX}abc`]: { a: new ArrayBuffer(1) },
    "shanhai-travel-v2": { a: new ArrayBuffer(1) },
    unrelated: { a: new ArrayBuffer(1) },
  });
  assert.deepEqual(await deleteOtherWorldVersions(next, other), [mine]);
  assert.deepEqual([...other.stores.keys()].sort(), [worldDataCacheName(next), `${WORLD_SHELL_PREFIX}abc`, "shanhai-travel-v2", "unrelated"].sort());
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

const origin = "https://trip.example";
const html = (...assets) => new TextEncoder().encode(`<!doctype html><script type="module" src="/assets/${assets[0]}"></script><link rel="stylesheet" href="/assets/${assets[1]}">`).buffer;
const build = (tag) => ({
  "https://trip.example/": html(`${tag}.js`, `${tag}.css`),
  [`https://trip.example/assets/${tag}.js`]: new TextEncoder().encode(`js ${tag}`).buffer,
  [`https://trip.example/assets/${tag}.css`]: new TextEncoder().encode(`css ${tag}`).buffer,
});
const loadedOf = (files) => Object.keys(files).filter((url) => url !== "https://trip.example/").concat("https://cdn.example/x.js");
const worker = () => ({ registered: [], register(url) { this.registered.push(url); return Promise.resolve(); }, ready: Promise.resolve({}) });
const prepare = (files, caches, extra = {}) => {
  const sw = worker();
  const net = network(files);
  return prepareWorldOffline({ baseUrl: "/", origin, loaded: loadedOf(files), catalog, caches, serviceWorker: sw, fetchImpl: net.fetchImpl, ...extra }).then((result) => ({ result, sw, net }));
};
const text = (buffer) => new TextDecoder().decode(buffer);
/** Every cache with every entry's text: two equal snapshots are two equal CacheStorages. */
const snapshot = (caches, only = () => true) =>
  JSON.stringify([...caches.stores].filter(([name]) => only(name)).map(([name, entries]) => [name, [...entries].map(([url, body]) => [url, text(body)])]));
const shellNames = (caches) => [...caches.stores.keys()].filter((name) => name.startsWith(WORLD_SHELL_PREFIX));

test("preparing the offline cache registers the worker and writes one complete shell cache, same-origin files only", async () => {
  const caches = memoryCaches({ unrelated: { a: new ArrayBuffer(1) }, [worldDataCacheName(catalog)]: { [chinaUrl]: bytes(china) } });
  const notShell = (name) => !name.startsWith(WORLD_SHELL_PREFIX);
  const before = snapshot(caches, notShell);
  const files = build("a");
  const { result, sw, net } = await prepare(files, caches);
  assert.equal(result, "ready");
  assert.deepEqual(sw.registered, ["/sw.js"]);
  assert.deepEqual(net.calls.sort(), Object.keys(files).sort(), "only same-origin files were requested");
  const [name, ...rest] = shellNames(caches);
  assert.equal(rest.length, 0, "one shell cache");
  assert.match(name, /^shanhai-world-shell-[0-9a-f]{16}$/);
  const shell = caches.stores.get(name);
  const manifestUrl = shellManifestUrl(origin, "/");
  assert.deepEqual([...shell.keys()].sort(), [...Object.keys(files), manifestUrl].sort());
  assert.deepEqual(JSON.parse(text(shell.get(manifestUrl))), {
    files: Object.keys(files).sort(),
    dataCache: worldDataCacheName(catalog),
    chinaSha256: catalog.china.sha256,
  });
  assert.equal([...caches.puts].at(-1)[1], manifestUrl, "the manifest is written last");
  assert.equal(snapshot(caches, notShell), before, "no other cache was touched");
  // No Cache Storage or no worker support: nothing is attempted, and it is reported.
  assert.equal((await prepare(files, undefined)).result, "unsupported");
  assert.equal((await prepare(files, caches, { serviceWorker: undefined })).result, "unsupported");
});

test("preparing again for the same build writes nothing; a new build replaces the old shell, and nothing else", async () => {
  const caches = memoryCaches({ unrelated: { a: new ArrayBuffer(1) }, "shanhai-world-shell": { "https://trip.example/": new ArrayBuffer(1) } });
  assert.equal((await prepare(build("a"), caches)).result, "ready");
  const [first] = shellNames(caches);
  assert.equal("shanhai-world-shell" in Object.fromEntries(caches.stores), false, "the earlier single-cache shell is removed");
  const putsAfterFirst = caches.puts.length;
  assert.equal((await prepare(build("a"), caches)).result, "ready");
  assert.equal(caches.puts.length, putsAfterFirst, "an identical build is not rewritten");
  assert.equal((await prepare(build("b"), caches)).result, "ready");
  const names = shellNames(caches);
  assert.equal(names.length, 1);
  assert.notEqual(names[0], first, "the shell of the new build");
  assert.ok(caches.stores.has("unrelated"));
  assert.deepEqual([...caches.stores.get(names[0]).keys()].filter((u) => u.includes("/assets/")).sort(), ["https://trip.example/assets/b.css", "https://trip.example/assets/b.js"]);
});

test("a download or cache write that fails leaves the previous shell, index.html and files, exactly as it was", async () => {
  const fresh = async () => {
    const caches = memoryCaches();
    assert.equal((await prepare(build("a"), caches)).result, "ready");
    return caches;
  };
  const b = build("b");
  const scenarios = {
    "a file answers 404": { files: { ...b, "https://trip.example/assets/b.js": undefined } },
    "the page itself cannot be fetched": { fetchImpl: async (url) => (url === "https://trip.example/" ? Promise.reject(new TypeError("Failed to fetch")) : new Response("x")) },
    "the second file cannot be written": { fail: { put: (_, url) => url.endsWith("/assets/b.css") } },
    "the first file cannot be written (quota)": { fail: { put: (_, url) => url === "https://trip.example/" } },
    "the manifest cannot be written": { fail: { put: (_, url) => url.endsWith("shell-manifest.json") } },
    "a new deployment's page names files this page never loaded": {
      files: { ...b, "https://trip.example/": html("b.js", "c.css") },
    },
  };
  for (const [label, { files = b, fetchImpl, fail }] of Object.entries(scenarios)) {
    const caches = await fresh();
    const [old] = shellNames(caches);
    const was = snapshot(caches);
    const faulty = fail ? memoryCaches(Object.fromEntries([...caches.stores].map(([n, e]) => [n, Object.fromEntries(e)])), fail) : caches;
    const { result } = await prepare(files, faulty, fetchImpl ? { fetchImpl } : {});
    assert.equal(result, "failed", label);
    assert.equal(snapshot(faulty), was, `${label}: the earlier shell and nothing else is there`);
    assert.deepEqual(shellNames(faulty), [old], label);
  }
});

test("a failed first preparation leaves no shell cache that could be served, and a retry succeeds", async () => {
  const caches = memoryCaches({}, { put: (_, url) => url.endsWith("/assets/a.css") });
  assert.equal((await prepare(build("a"), caches)).result, "failed");
  assert.deepEqual(shellNames(caches), []);
  assert.equal((await prepare(build("a"), memoryCaches())).result, "ready");
});

test("failing to remove the old shell afterwards is not a failure: the new shell is complete", async () => {
  const caches = memoryCaches();
  assert.equal((await prepare(build("a"), caches)).result, "ready");
  const [old] = shellNames(caches);
  const stubborn = memoryCaches(Object.fromEntries([...caches.stores].map(([n, e]) => [n, Object.fromEntries(e)])), { delete: (name) => name === old });
  assert.equal((await prepare(build("b"), stubborn)).result, "ready");
  assert.equal(shellNames(stubborn).length, 2, "both stay, the newer one is created last");
});

test("the files an index.html points at are those under assets/ on this origin", () => {
  const page = '<script type="module" crossorigin src="/assets/index-a.js?x=1"></script><link rel=stylesheet href=\'/assets/a.css\'><link href="https://cdn.example/assets/evil.js"><link href="/favicon.svg"><a href="/assets/../etc">x</a>';
  assert.deepEqual(indexAssets(page, origin, "/"), ["https://trip.example/assets/a.css", "https://trip.example/assets/index-a.js"]);
  assert.deepEqual(indexAssets('<script src="/app/assets/z.js"></script><script src="/assets/y.js"></script>', origin, "/app/"), ["https://trip.example/app/assets/z.js"]);
});
