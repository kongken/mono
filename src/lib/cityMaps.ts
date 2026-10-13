import { normalizeMap, type MapData } from "./maps";

export type CityMaps = Record<string, MapData>;

// Loads every province's local city/district boundaries so the national map can
// be filled per city. Rejects if any file fails, so a partial map is never shown.
export async function loadCityMaps(
  baseUrl: string,
  provinceCodes: string[],
  signal?: AbortSignal,
): Promise<CityMaps> {
  const entries = await Promise.all(
    provinceCodes.map(async (code) => {
      const response = await fetch(`${baseUrl}maps/${code}.json`, { signal });
      if (!response.ok) throw new Error(`Failed to load ${code}`);
      return [code, normalizeMap(await response.json())] as const;
    }),
  );
  return Object.fromEntries(entries);
}
