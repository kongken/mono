import { useCallback, useEffect, useMemo, useState } from "react";
import regions from "@kongken/geo-data/regions.json";
import { loadWorldCatalog, type WorldCatalog, type WorldSearchIndex } from "../lib/world-catalog.ts";
import { loadWorldMap, type WorldMap } from "../lib/world-map.ts";
import { createVerifiedFetch, deleteOtherWorldVersions, prepareWorldOffline, type OfflineResult } from "../lib/world-offline.ts";
import { worldCodeSet, type WorldCodes } from "../lib/world.ts";

// What the world view loads, and in what order: the catalog (a code chunk, needed to validate
// the stored world data), then the map files, then, once both are in, the offline cache. Each
// step fails on its own and can be retried; none of them touches the stored footprints.

export type CatalogState =
  | { status: "loading" }
  | { status: "failed" }
  | { status: "ready"; catalog: WorldCatalog; index: WorldSearchIndex; codes: WorldCodes };
export type MapState = { status: "loading" } | { status: "failed"; message: string } | { status: "ready"; map: WorldMap };
export type OfflineState = OfflineResult | "pending";

const provinceCodes = regions.map((region) => region.code);

export function useWorldAssets(active: boolean) {
  const [catalogTry, setCatalogTry] = useState(0);
  const [mapTry, setMapTry] = useState(0);
  const [catalog, setCatalog] = useState<CatalogState>({ status: "loading" });
  const [map, setMap] = useState<MapState>({ status: "loading" });
  const [offline, setOffline] = useState<OfflineState>("pending");

  useEffect(() => {
    if (!active) return;
    let current = true;
    setCatalog({ status: "loading" });
    loadWorldCatalog().then(
      ({ catalog: loaded, index }) => current && setCatalog({ status: "ready", catalog: loaded, index, codes: worldCodeSet(loaded) }),
      () => current && setCatalog({ status: "failed" }),
    );
    return () => {
      current = false;
    };
  }, [active, catalogTry]);

  const ready = catalog.status === "ready" ? catalog.catalog : null;
  useEffect(() => {
    if (!ready) return;
    const controller = new AbortController();
    setMap({ status: "loading" });
    const baseUrl = import.meta.env.BASE_URL;
    const caches = typeof window !== "undefined" && "caches" in window ? window.caches : undefined;
    loadWorldMap(baseUrl, ready, provinceCodes, controller.signal, createVerifiedFetch({ catalog: ready, baseUrl, caches })).then(
      (loaded) => {
        if (controller.signal.aborted) return;
        setMap({ status: "ready", map: loaded });
        // Only after a successful, verified load: fill the offline cache and drop other versions' caches.
        setOffline("pending");
        void (async () => {
          if (caches) await deleteOtherWorldVersions(ready, caches).catch(() => []);
          const result = await prepareWorldOffline({
            baseUrl,
            origin: location.origin,
            catalog: ready,
            loaded: performance.getEntriesByType("resource").map((entry) => entry.name),
            caches,
            serviceWorker: "serviceWorker" in navigator ? navigator.serviceWorker : undefined,
          });
          if (!controller.signal.aborted) setOffline(result);
        })();
      },
      (error: unknown) => {
        if (!controller.signal.aborted) setMap({ status: "failed", message: error instanceof Error ? error.message : "无法加载世界地图" });
      },
    );
    return () => controller.abort();
  }, [ready, mapTry]);

  return {
    catalog,
    map,
    offline,
    retryCatalog: useCallback(() => setCatalogTry((n) => n + 1), []),
    retryMap: useCallback(() => setMapTry((n) => n + 1), []),
    names: useMemo(
      () => (ready ? Object.fromEntries(ready.countries.map((country) => [country.code, country.name])) : {}),
      [ready],
    ),
  };
}
