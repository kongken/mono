import assert from "node:assert/strict";
import { geoArea, geoCentroid, geoContains } from "d3-geo";
import { gcj02ToWgs84 } from "../src/lib/coords.ts";
import { normalizeMap } from "../src/lib/maps.ts";

const islandCodes = new Set(["tw-09020", "tw-09007"]);

/** Split the supplemental dataset by province, without changing its IDs or coordinates. */
export function splitFujianRegions(taiwan, fujian) {
  const islands = taiwan.features.filter(f => islandCodes.has(String(f.properties.adcode)));
  if (!islands.length) {
    assert.equal(fujian.features.filter(f => islandCodes.has(String(f.properties.adcode))).length, 2, "missing Fujian supplemental boundaries");
    return { taiwan, fujian };
  }
  assert.equal(islands.length, 2, "incomplete Fujian supplemental boundaries");
  const normalized = normalizeMap({ type: "FeatureCollection", features: islands }).features;
  const features = fujian.features.filter(f => !islandCodes.has(String(f.properties.adcode))).map(f => {
    if (!["350100", "350500"].includes(String(f.properties.adcode))) return f;
    const polygons = f.geometry.type === "Polygon" ? [f.geometry.coordinates] : f.geometry.coordinates;
    // DataV already includes some island outlines in Fuzhou/Quanzhou. Remove those
    // entire island components to avoid two independently clickable overlays.
    const remaining = polygons.filter(coordinates => {
      const polygon = normalizeMap({ type: "FeatureCollection", features: [{ type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates } }] }).features[0];
      const center = gcj02ToWgs84(...geoCentroid(polygon));
      return !normalized.some(island => geoArea(polygon) < geoArea(island) && geoContains(island, center));
    });
    assert.ok(remaining.length, `${f.properties.name}: cannot remove the mainland boundary`);
    return { ...f, geometry: { type: "MultiPolygon", coordinates: remaining } };
  });
  return {
    taiwan: { ...taiwan, features: taiwan.features.filter(f => !islandCodes.has(String(f.properties.adcode))) },
    fujian: { ...fujian, features: [...features, ...islands] },
  };
}
