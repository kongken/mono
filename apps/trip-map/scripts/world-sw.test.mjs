import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import vm from "node:vm";

// public/sw.js: the fetch handler added for the offline start of the world view. It must leave every
// request alone that the network can answer (except hashed files, which a cache may answer), and use the
// caches only when the network fails, for a short list of same-origin requests.

const ORIGIN = "https://app.example";
function loadWorker({ scope = `${ORIGIN}/`, online = true, shell = {}, data = {} } = {}) {
  const handlers = {};
  const lookups = [];
  const networkCalls = [];
  const stores = { "shanhai-world-shell": shell, "shanhai-world-data-sha256:v1": data, "shanhai-travel-v2": { [`${ORIGIN}/secret`]: "travel" } };
  const caches = {
    keys: async () => Object.keys(stores),
    match: async (request, options = {}) => {
      const url = typeof request === "string" ? new URL(request, ORIGIN).href : request.url;
      lookups.push({ url, ...options });
      const hit = options.cacheName ? stores[options.cacheName]?.[url] : undefined;
      return hit === undefined ? undefined : { cached: hit };
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
    caches,
    fetch,
    Response: { error: () => ({ error: true }) },
  });
  /** Returns undefined when the worker did not take the request, otherwise what it answered. */
  const send = async (url, { method = "GET", mode = "same-origin" } = {}) => {
    let answered;
    handlers.fetch({ request: { url, method, mode }, respondWith: (p) => (answered = Promise.resolve(p)) });
    return answered && (await answered);
  };
  return { send, lookups, networkCalls };
}

const shell = { [`${ORIGIN}/`]: "index", [`${ORIGIN}/assets/index-a.js`]: "js", [`${ORIGIN}/favicon.svg`]: "icon" };
const data = { [`${ORIGIN}/maps/100000.json`]: "china" };

test("online, the page is always the network's and the API, other origins and writes are not touched", async () => {
  const w = loadWorker({ shell, data });
  assert.deepEqual(await w.send(`${ORIGIN}/`, { mode: "navigate" }), { network: `${ORIGIN}/` });
  assert.deepEqual(await w.send(`${ORIGIN}/maps/100000.json`), { network: `${ORIGIN}/maps/100000.json` });
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
  const w = loadWorker({ online: false, shell, data });
  assert.deepEqual(await w.send(`${ORIGIN}/`, { mode: "navigate" }), { cached: "index" });
  assert.deepEqual(await w.send(`${ORIGIN}/?utm=1`, { mode: "navigate" }), { cached: "index" }, "a query string still opens the page");
  assert.deepEqual(await w.send(`${ORIGIN}/maps/100000.json`), { cached: "china" });
  assert.deepEqual(await w.send(`${ORIGIN}/assets/index-a.js`), { cached: "js" });
  assert.deepEqual(await w.send(`${ORIGIN}/favicon.svg`), { cached: "icon" });
  // A hashed file that was never cached has nowhere to come from, and the worker does not invent one.
  await assert.rejects(() => w.send(`${ORIGIN}/assets/other-b.js`), TypeError);
  // Caches it does not own (the travel data) are never looked into.
  assert.ok(w.lookups.every((l) => l.cacheName === "shanhai-world-shell" || l.cacheName?.startsWith("shanhai-world-data-")));
  assert.ok(w.lookups.every((l) => l.ignoreVary === true), "the page filled the caches with plain fetches");
});

test("hashed files are served from the cache when present and fetched otherwise; the national map is the network's first", async () => {
  const w = loadWorker({ shell, data });
  assert.deepEqual(await w.send(`${ORIGIN}/assets/index-a.js`), { cached: "js" });
  assert.deepEqual(w.networkCalls, []);
  assert.deepEqual(await w.send(`${ORIGIN}/assets/new-c.js`), { network: `${ORIGIN}/assets/new-c.js` });
  assert.deepEqual(await w.send(`${ORIGIN}/maps/100000.json`), { network: `${ORIGIN}/maps/100000.json` });
});

test("under a sub-path the worker only takes what is under its own scope", async () => {
  const w = loadWorker({ scope: `${ORIGIN}/app/`, online: false, shell: { [`${ORIGIN}/app/`]: "index", [`${ORIGIN}/app/assets/a.js`]: "js" } });
  assert.deepEqual(await w.send(`${ORIGIN}/app/`, { mode: "navigate" }), { cached: "index" });
  assert.deepEqual(await w.send(`${ORIGIN}/app/assets/a.js`), { cached: "js" });
  assert.equal(await w.send(`${ORIGIN}/`, { mode: "navigate" }), undefined);
  assert.equal(await w.send(`${ORIGIN}/assets/a.js`), undefined);
});
