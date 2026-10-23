import type { City, RollingStockTopic } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { catalog } from "./rpc";

// The public catalogue rarely changes, so the city list is kept in memory and
// revalidated with the catalogue version: an unchanged version comes back as
// not_modified without the payload. Nothing is persisted.
let cities: { version: bigint; list: City[] } | undefined;

export async function loadCities(): Promise<City[]> {
  const res = await catalog.listCities({ knownVersion: cities?.version ?? 0n });
  if (res.notModified && cities) return cities.list;
  cities = { version: res.version, list: res.cities };
  return res.cities;
}

export async function loadTopics(): Promise<RollingStockTopic[]> {
  const res = await catalog.listRollingStockTopics({});
  return res.topics;
}

/** Forget what is cached, after an administrator changed the catalogue. */
export function invalidateCatalog() {
  cities = undefined;
}
