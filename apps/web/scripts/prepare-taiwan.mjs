import { geoArea, geoCentroid } from "d3-geo";
export function prepareTaiwan(data) {
  const translation = {
    臺: "台",
    東: "东",
    縣: "县",
    蓮: "莲",
    雲: "云",
    義: "义",
    蘭: "兰",
    園: "园",
    門: "门",
    連: "连",
    澎: "澎",
  };
  const normalize = (rings) =>
    geoArea({ type: "Polygon", coordinates: rings }) > Math.PI * 2
      ? rings.map((ring) => [...ring].reverse())
      : rings;
  return {
    type: "FeatureCollection",
    features: data.features.map((f) => {
      const name = [...f.properties.county]
        .map((c) => translation[c] || c)
        .join("")
        .replace("桃园县", "桃园市");
      const geometry =
        f.geometry.type === "Polygon"
          ? { ...f.geometry, coordinates: normalize(f.geometry.coordinates) }
          : {
              ...f.geometry,
              coordinates: f.geometry.coordinates.map(normalize),
            };
      return {
        type: "Feature",
        geometry,
        properties: {
          name,
          adcode: `tw-${f.properties.county_id}`,
          centroid: geoCentroid(geometry),
        },
      };
    }),
  };
}
