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
//   the on-demand world catalog) lives in `shanhai-world-shell-<id>`, where the id is the
//   SHA-256 of that index.html: one cache is one build, complete or not at all. Only
//   public/sw.js reads it, and only when the network cannot answer. It is filled once the
//   world view has loaded online, because a service worker only sees the requests made after
//   it took control. Everything is downloaded first and written into a cache of its own; a
//   manifest naming the files, the data cache and the China map's SHA-256 is written last, and
//   only a cache with a manifest is ever served. So a download or a write that fails leaves
//   the previous complete shell exactly as it was, and an offline start never mixes the
//   index.html of one build with the files of another. Older shell caches are removed after
//   the new one is complete.

export const WORLD_DATA_CACHE_PREFIX = "shanhai-world-data-";
export const WORLD_SHELL_PREFIX = "shanhai-world-shell-";
/** The shell cache of an earlier build of this feature: a single cache that was rewritten in place. */
const LEGACY_SHELL_CACHE = "shanhai-world-shell";
export const shellManifestUrl = (origin: string, baseUrl: string) => new URL(`${baseUrl}shell-manifest.json`, origin).href;

/** What marks a shell cache as complete, and ties it to the map data of the same build. */
export type ShellManifest = { files: string[]; dataCache: string; chinaSha256: string };
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
  catalog: Wanted;
  caches?: CacheStore;
  serviceWorker?: Pick<ServiceWorkerContainer, "register" | "ready">;
  fetchImpl?: typeof fetch;
};
const READY_TIMEOUT_MS = 10_000;
export type OfflineResult = "ready" | "unsupported" | "failed";

/** The hashed files an index.html points at (`src` and `href`), as absolute same-origin URLs without query. */
export function indexAssets(html: string, origin: string, baseUrl: string): string[] {
  const found = new Set<string>();
  for (const match of html.matchAll(/\b(?:src|href)\s*=\s*["']([^"']+)["']/g)) {
    let url: URL;
    try {
      url = new URL(match[1], new URL(baseUrl, origin));
    } catch {
      continue;
    }
    if (url.origin !== origin || !url.pathname.startsWith(`${baseUrl}assets/`)) continue;
    url.search = "";
    url.hash = "";
    found.add(url.href);
  }
  return [...found].sort();
}

type Downloaded = { url: string; bytes: ArrayBuffer; type: string | null };
const asResponse = ({ bytes, type }: Downloaded) =>
  new Response(bytes.slice(0), { status: 200, headers: type ? { "content-type": type } : undefined });

async function isComplete(caches: CacheStore, name: string, manifestUrl: string, manifest: string): Promise<boolean> {
  if (!(await caches.keys()).includes(name)) return false;
  const hit = await (await caches.open(name)).match(manifestUrl);
  return !!hit && (await hit.text()) === manifest;
}

/**
 * Registers the service worker and keeps the shell cache current with what this page loaded.
 * Best effort: the world view works online without it, so a failure is reported, not thrown, and
 * leaves the shell that was complete before as it was.
 */
export async function prepareWorldOffline(env: OfflineEnv): Promise<OfflineResult> {
  if (!env.caches || !env.serviceWorker) return "unsupported";
  const caches = env.caches;
  const get = env.fetchImpl ?? ((input: RequestInfo | URL, init?: RequestInit) => fetch(input, init));
  let writing: string | undefined;
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

    // 1. Download everything before writing anything.
    const wanted = shellResources(env.loaded, env.origin, env.baseUrl);
    const files: Downloaded[] = await Promise.all(
      wanted.map(async (url) => {
        const response = await get(url, { credentials: "same-origin", cache: "reload" });
        if (!response.ok) throw new Error(`${url}: ${response.status}`);
        return { url, bytes: await response.arrayBuffer(), type: response.headers.get("content-type") };
      }),
    );
    // 2. The index.html downloaded now must be the one that goes with the files this page loaded: a
    //    deployment in between would pair a new index.html with the old build's files.
    const indexUrl = new URL(env.baseUrl, env.origin).href;
    const index = files.find((file) => file.url === indexUrl)!;
    const have = new Set(files.map((file) => file.url));
    if (indexAssets(new TextDecoder().decode(index.bytes), env.origin, env.baseUrl).some((url) => !have.has(url))) return "failed";

    // 3. One cache per build; the manifest, written last, is what makes it complete.
    const manifest: ShellManifest = {
      files: files.map((file) => file.url).sort(),
      dataCache: worldDataCacheName(env.catalog),
      chinaSha256: env.catalog.china.sha256,
    };
    const manifestText = JSON.stringify(manifest);
    const manifestUrl = shellManifestUrl(env.origin, env.baseUrl);
    const name = `${WORLD_SHELL_PREFIX}${(await sha256Hex(index.bytes)).slice(0, 16)}`;
    if (!(await isComplete(caches, name, manifestUrl, manifestText))) {
      writing = name;
      await caches.delete(name);
      const shell = await caches.open(name);
      for (const file of files) await shell.put(file.url, asResponse(file));
      await shell.put(manifestUrl, new Response(manifestText, { status: 200, headers: { "content-type": "application/json" } }));
    }
    // 4. Only now is anything older removed (a failure here only leaves more than needed).
    for (const other of await caches.keys()) {
      if (other !== name && (other.startsWith(WORLD_SHELL_PREFIX) || other === LEGACY_SHELL_CACHE)) await caches.delete(other).catch(() => false);
    }
    return "ready";
  } catch {
    // Whatever this run had begun to write is not a complete shell: drop it, the earlier one stays.
    if (writing) await caches.delete(writing).catch(() => false);
    return "failed";
  }
}
