import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import vm from "node:vm";

// public/sw.js: the fetch handler added for the offline start of the world view. It must leave every
// request alone that the network can answer (except hashed files, which a cache may answer), and use the
// caches only when the network fails, for a short list of same-origin requests, and only a shell cache
// whose manifest is there, with the national map the manifest vouches for.

const ORIGIN = "https://app.example";
const sha256 = (text) => createHash("sha256").update(text).digest("hex");

class Hit {
  constructor(cached) {
    this.cached = cached;
  }
  json() {
    return Promise.resolve(JSON.parse(this.cached));
  }
  clone() {
    return this;
  }
  arrayBuffer() {
    return Promise.resolve(new TextEncoder().encode(this.cached).buffer);
  }
}

function loadWorker({ scope = `${ORIGIN}/`, online = true, stores = {} } = {}) {
  const handlers = {};
  const opened = [];
  const networkCalls = [];
  const all = { "shanhai-travel-v2": { [`${ORIGIN}/secret`]: "travel" }, ...stores };
  const cacheOf = (name) => ({
    match: async (request, options = {}) => {
      assert.equal(options.ignoreVary, true, "the page filled the caches with plain fetches");
      const url = typeof request === "string" ? new URL(request, ORIGIN).href : request.url;
      const hit = all[name]?.[url];
      return hit === undefined ? undefined : new Hit(hit);
    },
  });
  const caches = {
    keys: async () => Object.keys(all),
    open: async (name) => {
      opened.push(name);
      assert.ok(name in all, "only caches that exist are opened (opening one would create it)");
      return cacheOf(name);
    },
  };
  const self = {
    location: { origin: ORIGIN },
    registration: { scope },
    addEventListener: (type, fn) => (handlers[type] = fn),
    skipWaiting() {},
    clients: { claim: async () => {} },
  };
  const fetch = async (request) => {
    networkCalls.push(request.url);
    if (!online) throw new TypeError("Failed to fetch");
    return { network: request.url };
  };
  vm.runInNewContext(readFileSync(new URL("../public/sw.js", import.meta.url), "utf8"), {
    self,
    URL,
    JSON,
    Uint8Array,
    Array,
    String,
    caches,
    crypto: globalThis.crypto,
    fetch,
    Response: { error: () => ({ error: true }) },
  });
  /** Returns undefined when the worker did not take the request, otherwise what it answered. */
  const send = async (url, { method = "GET", mode = "same-origin" } = {}) => {
    let answered;
    handlers.fetch({ request: { url, method, mode }, respondWith: (p) => (answered = Promise.resolve(p)) });
    const result = answered && (await answered);
    return result instanceof Hit ? { cached: result.cached } : result;
  };
  return { send, opened, networkCalls };
}

const manifestFor = (dataCache = "shanhai-world-data-sha256:v1", china = "china") =>
  JSON.stringify({ files: [], dataCache, chinaSha256: sha256(china) });
const shell = (root = ORIGIN, extra = {}) => ({
  [`${root}/`]: "index",
  [`${root}/assets/index-a.js`]: "js",
  [`${root}/favicon.svg`]: "icon",
  [`${root}/shell-manifest.json`]: manifestFor(),
  ...extra,
});
const data = { [`${ORIGIN}/maps/100000.json`]: "china" };
const complete = { "shanhai-world-shell-aaaa": shell(), "shanhai-world-data-sha256:v1": data };

test("online, the page is always the network's and the API, other origins and writes are not touched", async () => {
  const w = loadWorker({ stores: complete });
  assert.deepEqual(await w.send(`${ORIGIN}/`, { mode: "navigate" }), { network: `${ORIGIN}/` });
  assert.deepEqual(await w.send(`${ORIGIN}/maps/100000.json`), { network: `${ORIGIN}/maps/100000.json` });
  assert.deepEqual(w.opened, [], "no cache was looked into");
  for (const [url, options] of [
    [`${ORIGIN}/api/bff.v1.AuthService/Me`, {}],
    [`${ORIGIN}/maps/world.json`, {}],
    [`${ORIGIN}/maps/110000.json`, {}],
    ["https://tile.openstreetmap.org/1/2/3.png", {}],
    [`${ORIGIN}/assets/index-a.js`, { method: "POST" }],
    [`${ORIGIN}/s/token`, { mode: "navigate" }],
    [`${ORIGIN}/status`, { mode: "navigate" }],
    [`${ORIGIN}/other`, { mode: "navigate" }],
  ])
    assert.equal(await w.send(url, options), undefined, `${options.method ?? "GET"} ${url} is not the worker's`);
});

test("offline, the page, hashed files and the national map come from the world caches; nothing else does", async () => {
  const w = loadWorker({ online: false, stores: complete });
  assert.deepEqual(await w.send(`${ORIGIN}/`, { mode: "navigate" }), { cached: "index" });
  assert.deepEqual(await w.send(`${ORIGIN}/?utm=1`, { mode: "navigate" }), { cached: "index" }, "a query string still opens the page");
  assert.deepEqual(await w.send(`${ORIGIN}/maps/100000.json`), { cached: "china" });
  assert.deepEqual(await w.send(`${ORIGIN}/assets/index-a.js`), { cached: "js" });
  assert.deepEqual(await w.send(`${ORIGIN}/favicon.svg`), { cached: "icon" });
  // A hashed file that was never cached has nowhere to come from, and the worker does not invent one.
  await assert.rejects(() => w.send(`${ORIGIN}/assets/other-b.js`), TypeError);
  // Caches it does not own (the travel data) are never looked into.
  assert.ok(w.opened.every((name) => name.startsWith("shanhai-world-shell-") || name.startsWith("shanhai-world-data-")));
});

test("hashed files are served from the cache when present and fetched otherwise; the national map is the network's first", async () => {
  const w = loadWorker({ stores: complete });
  assert.deepEqual(await w.send(`${ORIGIN}/assets/index-a.js`), { cached: "js" });
  assert.deepEqual(w.networkCalls, []);
  assert.deepEqual(await w.send(`${ORIGIN}/assets/new-c.js`), { network: `${ORIGIN}/assets/new-c.js` });
  assert.deepEqual(await w.send(`${ORIGIN}/maps/100000.json`), { network: `${ORIGIN}/maps/100000.json` });
});

test("a shell cache without its manifest is never served: not the page, not its files", async () => {
  const half = shell();
  delete half[`${ORIGIN}/shell-manifest.json`];
  const w = loadWorker({ online: false, stores: { "shanhai-world-shell-bbbb": half, "shanhai-world-data-sha256:v1": data } });
  assert.deepEqual(await w.send(`${ORIGIN}/`, { mode: "navigate" }), { error: true });
  assert.deepEqual(await w.send(`${ORIGIN}/maps/100000.json`), { error: true });
  await assert.rejects(() => w.send(`${ORIGIN}/assets/index-a.js`), TypeError, "the half-written file is not used either");
  // Online the same files come from the network.
  const online = loadWorker({ stores: { "shanhai-world-shell-bbbb": half } });
  assert.deepEqual(await online.send(`${ORIGIN}/assets/index-a.js`), { network: `${ORIGIN}/assets/index-a.js` });
  // The single-cache shell of an earlier build of this feature is not a shell.
  const legacy = loadWorker({ online: false, stores: { "shanhai-world-shell": shell() } });
  assert.deepEqual(await legacy.send(`${ORIGIN}/`, { mode: "navigate" }), { error: true });
});

test("one complete shell serves both the page and its files: the newest complete one, never an unfinished newer one", async () => {
  const oldShell = shell(ORIGIN, { [`${ORIGIN}/`]: "index-old", [`${ORIGIN}/assets/index-a.js`]: "js-old" });
  const newShell = shell(ORIGIN, { [`${ORIGIN}/`]: "index-new", [`${ORIGIN}/assets/index-a.js`]: "js-new" });
  const unfinished = { [`${ORIGIN}/`]: "index-unfinished", [`${ORIGIN}/assets/index-a.js`]: "js-unfinished" };
  const stores = { "shanhai-world-shell-old": oldShell, "shanhai-world-shell-new": newShell, "shanhai-world-data-sha256:v1": data };
  const both = loadWorker({ online: false, stores });
  assert.deepEqual(await both.send(`${ORIGIN}/`, { mode: "navigate" }), { cached: "index-new" });
  assert.deepEqual(await both.send(`${ORIGIN}/assets/index-a.js`), { cached: "js-new" });
  const partial = loadWorker({ online: false, stores: { "shanhai-world-shell-old": oldShell, "shanhai-world-shell-new": unfinished, "shanhai-world-data-sha256:v1": data } });
  assert.deepEqual(await partial.send(`${ORIGIN}/`, { mode: "navigate" }), { cached: "index-old" });
  assert.deepEqual(await partial.send(`${ORIGIN}/assets/index-a.js`), { cached: "js-old" });
});

test("offline, the national map is served only if it is the one the shell's manifest names and its bytes match", async () => {
  const serve = (stores) => loadWorker({ online: false, stores }).send(`${ORIGIN}/maps/100000.json`);
  assert.deepEqual(await serve(complete), { cached: "china" });
  // The bytes are not the ones recorded: another version's map, or one that was cut short.
  assert.deepEqual(await serve({ ...complete, "shanhai-world-data-sha256:v1": { [`${ORIGIN}/maps/100000.json`]: "chin" } }), { error: true });
  // A data cache of another version than the manifest names is not read, even with the right bytes.
  assert.deepEqual(await serve({ "shanhai-world-shell-aaaa": shell(), "shanhai-world-data-sha256:other": data }), { error: true });
  // No data cache at all.
  assert.deepEqual(await serve({ "shanhai-world-shell-aaaa": shell() }), { error: true });
  // A manifest cannot point the worker at a cache that is not the world data's.
  const hostile = { "shanhai-world-shell-aaaa": shell(ORIGIN, { [`${ORIGIN}/shell-manifest.json`]: manifestFor("shanhai-travel-v2", "travel") }), "shanhai-travel-v2": { [`${ORIGIN}/maps/100000.json`]: "travel" } };
  assert.deepEqual(await serve(hostile), { error: true });
  // A manifest that is not JSON is no manifest.
  assert.deepEqual(await serve({ ...complete, "shanhai-world-shell-aaaa": shell(ORIGIN, { [`${ORIGIN}/shell-manifest.json`]: "{" }) }), { error: true });
});

test("under a sub-path the worker only takes what is under its own scope", async () => {
  const sub = `${ORIGIN}/app`;
  const w = loadWorker({
    scope: `${sub}/`,
    online: false,
    stores: { "shanhai-world-shell-aaaa": shell(sub), "shanhai-world-data-sha256:v1": { [`${sub}/maps/100000.json`]: "china" } },
  });
  assert.deepEqual(await w.send(`${sub}/`, { mode: "navigate" }), { cached: "index" });
  assert.deepEqual(await w.send(`${sub}/assets/index-a.js`), { cached: "js" });
  assert.deepEqual(await w.send(`${sub}/maps/100000.json`), { cached: "china" });
  assert.equal(await w.send(`${ORIGIN}/`, { mode: "navigate" }), undefined);
  assert.equal(await w.send(`${ORIGIN}/assets/a.js`), undefined);
});
