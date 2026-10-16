export type TileConfig = {
  url: string;
  maxZoom: number;
  /** HTML shown in the map attribution; always credits OpenStreetMap. */
  attribution: string;
};

export const DEFAULT_TILE_URL = "https://tile.openstreetmap.org/{z}/{x}/{y}.png";
const DEFAULT_MAX_ZOOM = 19;
const OSM_CREDIT =
  '&copy; <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noreferrer">OpenStreetMap</a> contributors';

type TileEnv = {
  VITE_OSM_TILE_URL?: string;
  VITE_OSM_TILE_MAX_ZOOM?: string;
  VITE_OSM_TILE_ATTRIBUTION?: string;
};

/**
 * Resolve the raster tile source from build-time env. Invalid values fall back to
 * the OSM defaults so a typo never leaves the basemap blank.
 */
export function resolveTileConfig(env: TileEnv = {}): TileConfig {
  const rawUrl = env.VITE_OSM_TILE_URL?.trim();
  const url =
    rawUrl && ["{z}", "{x}", "{y}"].every((token) => rawUrl.includes(token))
      ? rawUrl
      : DEFAULT_TILE_URL;
  const zoom = Number(env.VITE_OSM_TILE_MAX_ZOOM);
  const maxZoom =
    Number.isInteger(zoom) && zoom >= 1 && zoom <= 22
      ? zoom
      : url === DEFAULT_TILE_URL
        ? DEFAULT_MAX_ZOOM
        : 18;
  const extra = env.VITE_OSM_TILE_ATTRIBUTION?.trim();
  return {
    url,
    maxZoom,
    attribution: extra ? `${OSM_CREDIT}, ${extra}` : OSM_CREDIT,
  };
}
