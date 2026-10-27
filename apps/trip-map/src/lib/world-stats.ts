import { colors, type Saved } from "./travel.ts";
import { DERIVED_COUNTRY, worldTripCount, type WorldDocument } from "./world.ts";

// Which countries/regions light up and the three world statistics
// (docs/trip-map/world-spec.md sections 5 and 6). Pure functions of the world document
// and a read-only summary of the China footprints; the China data is never written.

/** Colour of a country/region lit only by a city or by China footprints: the fixed 山野绿. */
export const DERIVED_COLOR = colors[0].value;

/** What the world view needs to know of the China footprints. */
export type ChinaSummary =
  | { status: "ok"; lit: boolean }
  /** The local China footprints are under read protection: CN is not lit and nothing is written. */
  | { status: "unreadable" };

/** CN is lit by at least one city footprint or one province mark (Hong Kong, Macao and Taiwan count). */
export const summarizeChina = (saved: Pick<Saved, "visits" | "provinces">): ChinaSummary => ({
  status: "ok",
  lit: saved.visits.length > 0 || Object.keys(saved.provinces).length > 0,
});
export const CHINA_UNREADABLE: ChinaSummary = { status: "unreadable" };

export type LitSource = "mark" | "place" | "china";
export type LitCountry = { code: string; color: string; sources: LitSource[] };

/**
 * The lit countries/regions, in code order: an independent mark, an overseas city, or
 * the derived CN. A marked country uses its mark's colour; every other one uses
 * DERIVED_COLOR. Unmarking a country does not remove one a city still lights.
 */
export function litCountries(world: WorldDocument, china: ChinaSummary): LitCountry[] {
  const lit = new Map<string, LitCountry>();
  const light = (code: string, source: LitSource, color?: string) => {
    const entry = lit.get(code) ?? { code, color: DERIVED_COLOR, sources: [] };
    if (!entry.sources.includes(source)) entry.sources.push(source);
    if (color) entry.color = color;
    lit.set(code, entry);
  };
  for (const mark of world.countries) light(mark.code, "mark", mark.color);
  for (const place of world.places) light(place.country, "place");
  if (china.status === "ok" && china.lit) light(DERIVED_COUNTRY, "china");
  return [...lit.values()].sort((a, b) => (a.code < b.code ? -1 : a.code > b.code ? 1 : 0));
}

export type WorldStats = { countries: number; places: number; trips: number };

/** Countries/regions (a lit China counts 1), overseas cities, and trips; shown apart from the China statistics. */
export function worldStats(world: WorldDocument, china: ChinaSummary): WorldStats {
  return {
    countries: litCountries(world, china).length,
    places: world.places.length,
    trips: worldTripCount(world),
  };
}
