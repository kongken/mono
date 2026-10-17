export type TileProvider = "osm" | "mapbox";

export type TileConfig = {
  url: string;
  maxZoom: number;
  /** HTML shown in the map attribution; always credits OpenStreetMap. */
  attribution: string;
  /**
   * "mapbox" means url is this site's own tile proxy (the Mapbox token stays on the
   * server), so the Mapbox logo and credits are required next to the map.
   */
  provider: TileProvider;
};

export const DEFAULT_TILE_URL = "https://tile.openstreetmap.org/{z}/{x}/{y}.png";
const DEFAULT_MAX_ZOOM = 19;

const OSM_CREDIT =
  '&copy; <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noreferrer">OpenStreetMap</a> contributors';

// Mapbox requires the text credits "© Mapbox", "© OpenStreetMap" and "Improve this
// map"; the logo is drawn by the map component. The feedback link's coordinates are
// filled in from the current view (see mapboxFeedbackUrl).
const MAPBOX_FEEDBACK = "https://apps.mapbox.com/feedback/";
const MAPBOX_CREDIT =
  '&copy; <a href="https://www.mapbox.com/about/maps/" target="_blank" rel="noreferrer">Mapbox</a>';
const MAPBOX_IMPROVE = `<strong><a data-mapbox-feedback href="${MAPBOX_FEEDBACK}" target="_blank" rel="noreferrer">Improve this map</a></strong>`;

type TileEnv = {
  VITE_OSM_TILE_URL?: string;
  VITE_OSM_TILE_MAX_ZOOM?: string;
  VITE_OSM_TILE_ATTRIBUTION?: string;
  VITE_OSM_TILE_PROVIDER?: string;
};

const hasTileTokens = (url: string) =>
  ["{z}", "{x}", "{y}"].every((token) => url.includes(token));

/** Same-origin path ("/api/tiles/...") or an absolute http(s) address, never protocol-relative. */
const isProxyUrl = (url: string) =>
  (/^\/(?!\/)/.test(url) || /^https?:\/\//i.test(url)) &&
  !/access_token/i.test(url);

/**
 * Resolve the raster tile source from build-time env. Invalid values fall back to
 * the OSM defaults so a typo never leaves the basemap blank.
 *
 * VITE_OSM_TILE_PROVIDER=mapbox declares that VITE_OSM_TILE_URL is this site's own
 * Mapbox tile proxy (a relative path for same-origin deployments, or the BFF's
 * absolute address). It only takes effect with such a URL, so Mapbox credits are
 * never shown on other tiles, and a URL carrying an access token is refused: the
 * token belongs to the server.
 */
export function resolveTileConfig(env: TileEnv = {}): TileConfig {
  const rawUrl = env.VITE_OSM_TILE_URL?.trim();
  const wantsMapbox = env.VITE_OSM_TILE_PROVIDER?.trim().toLowerCase() === "mapbox";
  const validUrl = rawUrl && hasTileTokens(rawUrl) ? rawUrl : undefined;
  const mapbox = Boolean(wantsMapbox && validUrl && isProxyUrl(validUrl));
  // A Mapbox declaration that does not hold up (no URL, or a URL with a token) goes
  // back to OSM entirely rather than keeping a half-trusted custom address.
  const url = mapbox ? validUrl! : wantsMapbox ? DEFAULT_TILE_URL : (validUrl ?? DEFAULT_TILE_URL);
  const zoom = Number(env.VITE_OSM_TILE_MAX_ZOOM);
  const maxZoom =
    Number.isInteger(zoom) && zoom >= 1 && zoom <= 22
      ? zoom
      : url === DEFAULT_TILE_URL
        ? DEFAULT_MAX_ZOOM
        : 18;
  const extra = env.VITE_OSM_TILE_ATTRIBUTION?.trim();
  const credits = mapbox
    ? [MAPBOX_CREDIT, OSM_CREDIT, MAPBOX_IMPROVE]
    : [OSM_CREDIT];
  const attribution = extra
    ? mapbox
      ? `${credits.join(" ")}, ${extra}`
      : `${OSM_CREDIT}, ${extra}`
    : credits.join(" ");
  return { url, maxZoom, attribution, provider: mapbox ? "mapbox" : "osm" };
}

/** Mapbox's "Improve this map" address for the current view. */
export function mapboxFeedbackUrl(view: {
  lng: number;
  lat: number;
  zoom: number;
}): string {
  const round = (n: number, digits: number) => Number(n.toFixed(digits));
  return `${MAPBOX_FEEDBACK}#/${round(view.lng, 4)}/${round(view.lat, 4)}/${round(view.zoom, 2)}`;
}

export type TileSourceInfo = {
  /** Provider shown in messages. */
  name: string;
  /** Label for the map and for load failures, such as "Mapbox 底图". */
  label: string;
  /** Text of the basemap toggle. */
  toggle: string;
  /** Tooltip telling people where the browser really fetches tiles from. */
  hint: string;
};

/** Describe where tiles come from without claiming OSM's servers when they are not used. */
export function describeTileSource(config: TileConfig): TileSourceInfo {
  const sameOrigin = config.url.startsWith("/");
  let host = "本站服务";
  if (!sameOrigin) {
    try {
      host = new URL(config.url).hostname;
    } catch {
      host = "自定义瓦片服务";
    }
  }
  if (config.provider === "mapbox") {
    return {
      name: "Mapbox",
      label: "Mapbox 底图",
      toggle: "联网底图",
      hint: `开启后会通过${host}加载 Mapbox 地图瓦片`,
    };
  }
  if (config.url === DEFAULT_TILE_URL) {
    return {
      name: "OpenStreetMap",
      label: "OpenStreetMap 底图",
      toggle: "OSM 底图",
      hint: "开启后会向 openstreetmap.org 请求地图瓦片",
    };
  }
  return {
    name: "自定义瓦片服务",
    label: "底图",
    toggle: "联网底图",
    hint: `开启后会向 ${host} 请求地图瓦片`,
  };
}
