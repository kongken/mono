import { geoContains, geoNaturalEarth1, geoPath } from "d3-geo";
import type { Feature, FeatureCollection, MultiPolygon, Polygon } from "geojson";
import { normalizeMap, type MapData } from "./maps.ts";
import type { WorldCatalog } from "./world-catalog.ts";

// The world map (docs/trip-map/world-spec.md section 10). `CN` is the unmodified
// national map public/maps/100000.json; every other country/region comes from
// public/maps/world.json. Both are same-origin static files: no third-party
// request, no key.
export const WORLD_MAP_FILE = "maps/world.json";
export const CHINA_MAP_FILE = "maps/100000.json";
/** Removed from the world catalog and the world map: they are part of CN there. */
export const MERGED_INTO_CN = ["HK", "MO", "TW"] as const;

type Shape = Feature<Polygon | MultiPolygon, { code: string }>;
export type WorldShape = {
  /** ISO 3166-1 alpha-2 (or XK). Every province of the national map is `CN`. */
  code: string;
  feature: Shape;
  /** Planar [minLon, minLat, maxLon, maxLat]; polygons never cross the antimeridian. */
  bbox: [number, number, number, number];
};
export type WorldMap = {
  /** Every country/region except CN, in file order. */
  countries: WorldShape[];
  /** The 34 province-level features of the national map, all with code `CN`. */
  china: WorldShape[];
  /** The unnamed sea-boundary line `100000_JD`; drawn thin, never hit-tested. */
  chinaLines: Feature[];
};

export class WorldMapError extends Error {}

function bboxOf(feature: Feature): [number, number, number, number] {
  const box: [number, number, number, number] = [Infinity, Infinity, -Infinity, -Infinity];
  const walk = (value: unknown): void => {
    if (typeof (value as number[])[0] === "number") {
      const [x, y] = value as number[];
      box[0] = Math.min(box[0], x);
      box[1] = Math.min(box[1], y);
      box[2] = Math.max(box[2], x);
      box[3] = Math.max(box[3], y);
    } else for (const item of value as unknown[]) walk(item);
  };
  const g = feature.geometry;
  if (g.type === "Polygon" || g.type === "MultiPolygon") walk(g.coordinates);
  return box;
}

const wind = (collection: unknown) => normalizeMap(collection as MapData) as unknown as FeatureCollection;

/** Validates both files against the catalog and the 34 province IDs, then indexes
 * them. Throws instead of returning a partial map. */
export function buildWorldMap(
  worldFile: unknown,
  chinaFile: unknown,
  catalog: Pick<WorldCatalog, "countries">,
  provinceCodes: readonly string[],
): WorldMap {
  const world = wind(worldFile);
  const china = wind(chinaFile);
  const codes = world.features.map((f) => String((f.properties as { code?: string } | null)?.code));
  const expected = catalog.countries.filter((c) => c.hasShape && c.code !== "CN").map((c) => c.code);
  if (new Set(codes).size !== codes.length) throw new WorldMapError("世界地图包含重复的国家/地区");
  if (codes.some((c) => c === "CN" || (MERGED_INTO_CN as readonly string[]).includes(c)))
    throw new WorldMapError("世界地图不应单独包含中国、香港、澳门或台湾");
  if ([...codes].sort().join() !== [...expected].sort().join()) throw new WorldMapError("世界地图与国家/地区目录不一致");

  const provinces = china.features.filter((f) => (f.properties as { name?: string }).name);
  const provinceIds = provinces.map((f) => String((f.properties as { adcode: unknown }).adcode));
  if ([...provinceIds].sort().join() !== [...provinceCodes].sort().join()) throw new WorldMapError("中国地图与省级区域索引不一致");
  const lines = china.features.filter((f) => !(f.properties as { name?: string }).name);
  if (lines.length !== 1 || lines[0].geometry.type !== "MultiPolygon" || lines[0].geometry.coordinates.length !== 10)
    throw new WorldMapError("中国地图缺少海上边界线");

  const shape = (code: string) => (f: Feature): WorldShape => {
    const feature = { ...f, properties: { code } } as Shape;
    return { code, feature, bbox: bboxOf(feature) };
  };
  return {
    countries: world.features.map((f, i) => shape(codes[i])(f)),
    china: provinces.map(shape("CN")),
    chinaLines: lines,
  };
}

export async function loadWorldMap(
  baseUrl: string,
  catalog: Pick<WorldCatalog, "countries">,
  provinceCodes: readonly string[],
  signal?: AbortSignal,
  fetchJson: typeof fetch = fetch,
): Promise<WorldMap> {
  const get = async (file: string) => {
    const response = await fetchJson(`${baseUrl}${file}`, { signal });
    if (!response.ok) throw new WorldMapError(`Failed to load ${file}`);
    return response.json();
  };
  const [world, china] = await Promise.all([get(WORLD_MAP_FILE), get(CHINA_MAP_FILE)]);
  return buildWorldMap(world, china, catalog, provinceCodes);
}

/** Draw order: `CN` last so it covers neighbours where the two sources overlap. */
export function drawOrder(map: WorldMap): WorldShape[] {
  return [...map.countries, ...map.china];
}

/** Which country/region a [lon, lat] position belongs to, or null at sea. `CN` wins:
 * a position inside any province-level area is `CN`, whatever the other source says. */
export function hitTestWorld(map: WorldMap, lon: number, lat: number): string | null {
  const inside = (s: WorldShape) =>
    lon >= s.bbox[0] && lon <= s.bbox[2] && lat >= s.bbox[1] && lat <= s.bbox[3] && geoContains(s.feature, [lon, lat]);
  if (map.china.some(inside)) return "CN";
  return map.countries.find(inside)?.code ?? null;
}

/** Natural Earth projection of the whole sphere, independent of the China camera in
 * `projection.ts`. */
export function createWorldProjection(width: number, height: number) {
  return geoNaturalEarth1().fitExtent(
    [
      [0, 0],
      [width, height],
    ],
    { type: "Sphere" },
  );
}
export const worldPath = (width: number, height: number) => geoPath(createWorldProjection(width, height));

/**
 * The place under a point of the map's own box (VIEW_W x VIEW_H units, after the viewport's
 * pan and zoom), as WGS-84 degrees rounded to 4 decimals (about 11 m). Null outside the
 * outline of the world. The inverse of how the map is drawn, so what was clicked is what is stored.
 */
export function pickLocation(
  projection: ReturnType<typeof createWorldProjection>,
  view: { k: number; x: number; y: number },
  boxX: number,
  boxY: number,
): { lat: number; lng: number } | null {
  const at: [number, number] = [(boxX - view.x) / view.k, (boxY - view.y) / view.k];
  const found = projection.invert?.(at);
  if (!found || !Number.isFinite(found[0]) || !Number.isFinite(found[1])) return null;
  const [lng, lat] = found;
  if (Math.abs(lat) > 90 || Math.abs(lng) > 180) return null;
  const back = projection([lng, lat]);
  if (!back || Math.hypot(back[0] - at[0], back[1] - at[1]) > 0.5) return null;
  const round = (n: number) => Math.round(n * 1e4) / 1e4 + 0;
  return { lat: round(lat), lng: round(lng) };
}
