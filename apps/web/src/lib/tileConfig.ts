import { describeTileSource, resolveTileConfig } from "@/lib/tiles";

/** Build-time raster tile source shared by the map and the basemap messages. */
export const TILES = resolveTileConfig({
  VITE_OSM_TILE_URL: import.meta.env.VITE_OSM_TILE_URL,
  VITE_OSM_TILE_MAX_ZOOM: import.meta.env.VITE_OSM_TILE_MAX_ZOOM,
  VITE_OSM_TILE_ATTRIBUTION: import.meta.env.VITE_OSM_TILE_ATTRIBUTION,
  VITE_OSM_TILE_PROVIDER: import.meta.env.VITE_OSM_TILE_PROVIDER,
});

export const TILE_SOURCE = describeTileSource(TILES);
