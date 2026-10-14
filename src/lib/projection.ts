import { geoConicConformal, geoMercator, geoPath, type GeoProjection } from "d3-geo";
import type { MapData } from "./maps.ts";

/**
 * Camera shared by the app, the public share page and the script that bakes the
 * server-side preview geometry (scripts/prepare-share-geometry.mjs), so the three
 * always draw the same map.
 */
export function mapProjection(map: MapData, provinceCode?: string): GeoProjection {
  const shapes = map.features.filter(
    (f) =>
      f.properties.name &&
      (provinceCode !== "460000" || String(f.properties.adcode) !== "460300"),
  );
  // Mainland bounds define the national camera. Maritime lines have their own inset.
  const bounds = provinceCode
    ? { ...map, features: shapes }
    : {
        type: "FeatureCollection" as const,
        features: shapes.map((f) => {
          if (f.geometry.type !== "MultiPolygon") return f;
          return {
            ...f,
            geometry: {
              ...f.geometry,
              coordinates: f.geometry.coordinates.filter((p) =>
                p[0].some((point) => point[1] > 17.5),
              ),
            },
          };
        }),
      };
  return (
    provinceCode
      ? geoMercator()
      : geoConicConformal().parallels([25, 47]).rotate([-105, 0])
  ).fitExtent(
    [
      [60, 35],
      [provinceCode === "460000" ? 640 : 790, 565],
    ],
    bounds,
  );
}

/** Where the South China Sea inset sits on the national map, in map units. */
export const SEA_INSET = { x: 736, y: 458, width: 83, height: 109 } as const;

/** Outline paths of the maritime features drawn in the inset (relative to its origin). */
export function seaInsetPaths(map: MapData): string[] {
  return map.features
    .filter((f) => !f.properties.name)
    .map(
      (f) =>
        geoPath(
          geoMercator().fitExtent(
            [
              [9, 7],
              [74, 87],
            ],
            f,
          ),
        )(f) || "",
    );
}
