import { geoArea } from "d3-geo";
import type { Feature, FeatureCollection, Geometry } from "geojson";

type MapFeature = Feature<
  Geometry,
  {
    name: string;
    adcode: number | string;
    center?: [number, number];
    centroid?: [number, number];
  }
>;
export type MapData = FeatureCollection<Geometry, MapFeature["properties"]>;

export function normalizeMap(data: MapData): MapData {
  // GeoAtlas uses RFC 7946 winding; d3's spherical polygons use the opposite winding.
  const normalizePolygon = (coordinates: number[][][]) =>
    geoArea({ type: "Polygon", coordinates }) > Math.PI * 2
      ? coordinates.map((ring) => [...ring].reverse())
      : coordinates;
  return {
    ...data,
    features: data.features.map((feature) => {
      const g = feature.geometry;
      if (g.type === "Polygon")
        return {
          ...feature,
          geometry: { ...g, coordinates: normalizePolygon(g.coordinates) },
        };
      if (g.type === "MultiPolygon")
        return {
          ...feature,
          geometry: { ...g, coordinates: g.coordinates.map(normalizePolygon) },
        };
      return feature;
    }),
  };
}
