import { sha256Hex } from "./sha256.ts";
import { WorldMapError } from "./world-map.ts";
import type { WorldCatalog } from "./world-catalog.ts";

// Offline use of the world view (docs/trip-map/world-spec.md section 10, "离线"). Two caches,
// both same-origin only:
//
// - The map data (maps/world.json, maps/100000.json) lives in `shanhai-world-data-<catalog
//   version>`. This page alone reads and writes it, and it checks every file against the
//   SHA-256 the catalog records before using it, from the cache or from the network. A copy
//   that does not match is deleted and fetched again; a network copy that does not match is
//   refused and never cached, so an incomplete or stale map is never drawn. Another catalog
//   version has another cache name, so it is never read, and it is deleted once this version
//   has loaded.
// - The page shell (index.html and the hashed scripts, styles and chunks it uses, among them
//   the on-demand world catalog) lives in `shanhai-world-shell`. Only public/sw.js reads it,
//   and only when the network cannot answer. It is filled once the world view has loaded
//   online, because a service worker only sees the requests made after it took control.

export const WORLD_DATA_CACHE_PREFIX = "shanhai-world-data-";
export const WORLD_SHELL_CACHE = "shanhai-world-shell";
export const worldDataCacheName = (catalog: Pick<WorldCatalog, "version">) => `${WORLD_DATA_CACHE_PREFIX}${catalog.version}`;

export type CacheStore = Pick<CacheStorage, "open" | "keys" | "delete">;

type Wanted = Pick<WorldCatalog, "version" | "geometry" | "china">;

/** The SHA-256 the catalog records for a map file, or undefined for any other file. */
const expectedDigest = (catalog: Wanted, baseUrl: string, url: string) =>
  url === `${baseUrl}${catalog.geometry.file}` ? catalog.geometry.sha256 : url === `${baseUrl}${catalog.china.file}` ? catalog.china.sha256 : undefined;

const jsonResponse = (bytes: ArrayBuffer) => new Response(bytes, { status: 200, headers: { "content-type": "application/json" } });

/**
 * A `fetch` for the two map files that returns only bytes matching the catalog's SHA-256:
 * the cached copy of this catalog version if it matches, otherwise the network's. Throws
 * WorldMapError when neither is available (offline without a cache, or a file that does
 * not match). Any other URL is refused: it is never a request to anywhere else.
 */
export function createVerifiedFetch(opts: {
  catalog: Wanted;
  baseUrl: string;
  caches?: CacheStore;
  fetchImpl?: typeof fetch;
}): typeof fetch {
  const { catalog, baseUrl } = opts;
  const get = opts.fetchImpl ?? ((input: RequestInfo | URL, init?: RequestInit) => fetch(input, init));
  const store = async () => (opts.caches ? opts.caches.open(worldDataCacheName(catalog)).catch(() => null) : null);

  return async (input, init) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const digest = expectedDigest(catalog, baseUrl, url);
    if (!digest) throw new WorldMapError("不是世界地图文件");
    const cache = await store();
    if (cache) {
      try {
        const hit = await cache.match(url);
        if (hit) {
          const bytes = await hit.arrayBuffer();
          if ((await sha256Hex(bytes)) === digest) return jsonResponse(bytes);
          await cache.delete(url); // damaged or from another build: never used, fetched again
        }
      } catch {
        // an unreadable cache is a cache miss
      }
    }
    let response: Response;
    try {
      response = await get(url, { signal: init?.signal, credentials: "same-origin" });
    } catch {
      throw new WorldMapError("无法加载世界地图：网络不可用，也没有可用的离线缓存");
    }
    if (!response.ok) throw new WorldMapError(`无法加载世界地图（${response.status}）`);
    const bytes = await response.arrayBuffer();
    if ((await sha256Hex(bytes)) !== digest) throw new WorldMapError("世界地图文件与目录记录的校验值不一致，已拒绝使用");
    if (cache) await cache.put(url, jsonResponse(bytes.slice(0))).catch(() => undefined);
    return jsonResponse(bytes);
  };
}

/** Deletes the data caches of other catalog versions (and nothing else). */
export async function deleteOtherWorldVersions(catalog: Pick<WorldCatalog, "version">, caches: CacheStore): Promise<string[]> {
  const keep = worldDataCacheName(catalog);
  const old = (await caches.keys()).filter((name) => name.startsWith(WORLD_DATA_CACHE_PREFIX) && name !== keep);
  await Promise.all(old.map((name) => caches.delete(name)));
  return old;
}

/**
 * The same-origin URLs the page needs to start offline: the page itself and the hashed
 * files under assets/ (and the icon) that it has loaded. Maps and `/api/` requests are
 * never part of it.
 */
export function shellResources(loaded: readonly string[], origin: string, baseUrl: string): string[] {
  const urls = new Set<string>([new URL(baseUrl, origin).href]);
  for (const name of loaded) {
    let url: URL;
    try {
      url = new URL(name, origin);
    } catch {
      continue;
    }
    if (url.origin !== origin) continue;
    if (url.pathname.startsWith(`${baseUrl}assets/`) || url.pathname === `${baseUrl}favicon.svg`) {
      url.search = "";
      url.hash = "";
      urls.add(url.href);
    }
  }
  return [...urls].sort();
}

export type OfflineEnv = {
  baseUrl: string;
  origin: string;
  /** `performance.getEntriesByType("resource")` names. */
  loaded: readonly string[];
  caches?: CacheStore;
  serviceWorker?: Pick<ServiceWorkerContainer, "register" | "ready">;
  fetchImpl?: typeof fetch;
};
const READY_TIMEOUT_MS = 10_000;
export type OfflineResult = "ready" | "unsupported" | "failed";

/**
 * Registers the service worker and (re)fills the shell cache with what this page loaded.
 * Best effort: the world view works online without it, so a failure is reported, not thrown.
 */
export async function prepareWorldOffline(env: OfflineEnv): Promise<OfflineResult> {
  if (!env.caches || !env.serviceWorker) return "unsupported";
  const get = env.fetchImpl ?? ((input: RequestInfo | URL, init?: RequestInit) => fetch(input, init));
  try {
    await env.serviceWorker.register(`${env.baseUrl}sw.js`);
    // `ready` never settles where the worker cannot start (blocked, private mode): do not wait for it forever.
    let timer: ReturnType<typeof setTimeout> | undefined;
    const started = await Promise.race([
      env.serviceWorker.ready.then(() => true),
      new Promise<boolean>((resolve) => {
        timer = setTimeout(() => resolve(false), READY_TIMEOUT_MS);
      }),
    ]);
    clearTimeout(timer);
    if (!started) return "failed";
    const wanted = shellResources(env.loaded, env.origin, env.baseUrl);
    const shell = await env.caches.open(WORLD_SHELL_CACHE);
    const fresh = await Promise.all(
      wanted.map(async (url) => {
        const response = await get(url, { credentials: "same-origin" });
        if (!response.ok) throw new Error(`${url}: ${response.status}`);
        await shell.put(url, response);
        return url;
      }),
    );
    const keep = new Set(fresh);
    for (const request of await shell.keys()) if (!keep.has(request.url)) await shell.delete(request);
    return "ready";
  } catch {
    return "failed";
  }
}
