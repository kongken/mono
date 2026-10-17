import type { Geometry } from "geojson";

// DataV GeoAtlas boundaries use GCJ-02, while OpenStreetMap tiles use WGS-84.
// Converting before drawing on top of OSM avoids a visible offset (up to ~500 m).
const A = 6378245.0;
const EE = 0.006693421622965943;

const outOfChina = (lng: number, lat: number) =>
  lng < 72.004 || lng > 137.8347 || lat < 0.8293 || lat > 55.8271;

function transformLat(x: number, y: number) {
  let r =
    -100 +
    2 * x +
    3 * y +
    0.2 * y * y +
    0.1 * x * y +
    0.2 * Math.sqrt(Math.abs(x));
  r +=
    ((20 * Math.sin(6 * x * Math.PI) + 20 * Math.sin(2 * x * Math.PI)) * 2) / 3;
  r += ((20 * Math.sin(y * Math.PI) + 40 * Math.sin((y / 3) * Math.PI)) * 2) / 3;
  r +=
    ((160 * Math.sin((y / 12) * Math.PI) + 320 * Math.sin((y * Math.PI) / 30)) *
      2) /
    3;
  return r;
}

function transformLng(x: number, y: number) {
  let r =
    300 + x + 2 * y + 0.1 * x * x + 0.1 * x * y + 0.1 * Math.sqrt(Math.abs(x));
  r +=
    ((20 * Math.sin(6 * x * Math.PI) + 20 * Math.sin(2 * x * Math.PI)) * 2) / 3;
  r += ((20 * Math.sin(x * Math.PI) + 40 * Math.sin((x / 3) * Math.PI)) * 2) / 3;
  r +=
    ((150 * Math.sin((x / 12) * Math.PI) + 300 * Math.sin((x / 30) * Math.PI)) *
      2) /
    3;
  return r;
}

function delta(lng: number, lat: number): [number, number] {
  const radLat = (lat / 180) * Math.PI;
  let magic = Math.sin(radLat);
  magic = 1 - EE * magic * magic;
  const sqrtMagic = Math.sqrt(magic);
  const dLat =
    (transformLat(lng - 105, lat - 35) * 180) /
    (((A * (1 - EE)) / (magic * sqrtMagic)) * Math.PI);
  const dLng =
    (transformLng(lng - 105, lat - 35) * 180) /
    ((A / sqrtMagic) * Math.cos(radLat) * Math.PI);
  return [dLng, dLat];
}

export function wgs84ToGcj02(lng: number, lat: number): [number, number] {
  if (outOfChina(lng, lat)) return [lng, lat];
  const [dLng, dLat] = delta(lng, lat);
  return [lng + dLng, lat + dLat];
}

export function gcj02ToWgs84(lng: number, lat: number): [number, number] {
  if (outOfChina(lng, lat)) return [lng, lat];
  const [dLng, dLat] = delta(lng, lat);
  return [lng - dLng, lat - dLat];
}

type Position = number[];

function mapPositions(geometry: Geometry, fn: (p: Position) => Position) {
  const ring = (r: Position[]) => r.map(fn);
  switch (geometry.type) {
    case "Polygon":
      return { ...geometry, coordinates: geometry.coordinates.map(ring) };
    case "MultiPolygon":
      return {
        ...geometry,
        coordinates: geometry.coordinates.map((p) => p.map(ring)),
      };
    default:
      return geometry;
  }
}

export function geometryGcj02ToWgs84(geometry: Geometry): Geometry {
  return mapPositions(geometry, ([lng, lat]) => gcj02ToWgs84(lng, lat));
}
