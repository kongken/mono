import fs from "node:fs";
import { gcj02ToWgs84 } from "../src/lib/coords.ts";

const WGS84_PROVINCES = new Set(["710000", "810000", "820000"]);
const regions = JSON.parse(
  fs.readFileSync(new URL("../../../packages/geo-data/data/regions.json", import.meta.url), "utf8"),
);

const round6 = (n) => Math.round(n * 1e6) / 1e6;

const coords = {};
for (const province of regions) {
  const map = JSON.parse(
    fs.readFileSync(
      new URL(`../public/maps/${province.code}.json`, import.meta.url),
      "utf8",
    ),
  );
  const isWgs84 = WGS84_PROVINCES.has(province.code);
  for (const city of province.cities) {
    const feature = map.features.find(
      (f) => String(f.properties?.adcode) === city.code,
    );
    const pt = feature?.properties?.centroid || feature?.properties?.center;
    if (!Array.isArray(pt) || pt.length !== 2) {
      throw new Error(`Missing center coordinate for ${city.code} (${city.name})`);
    }
    const gcjLng = round6(Number(pt[0]));
    const gcjLat = round6(Number(pt[1]));
    const [rawWgsLng, rawWgsLat] =
      isWgs84 || city.code.startsWith("tw-")
        ? [gcjLng, gcjLat]
        : gcj02ToWgs84(gcjLng, gcjLat);
    const wgsLng = round6(rawWgsLng);
    const wgsLat = round6(rawWgsLat);
    coords[city.code] = [gcjLng, gcjLat, wgsLng, wgsLat];
  }
}

const output = JSON.stringify(coords, null, 2) + "\n";
const frontTarget = new URL("../../../packages/geo-data/data/city-coords.json", import.meta.url);
const backDir = new URL("../../../internal/tripmap/itinerary", import.meta.url);
const backTarget = new URL("../../../internal/tripmap/itinerary/coords.json", import.meta.url);

if (process.argv.includes("--check")) {
  if (fs.readFileSync(frontTarget, "utf8") !== output) {
    throw new Error("City coordinates are stale: node scripts/prepare-city-coords.mjs");
  }
  if (fs.existsSync(backTarget) && fs.readFileSync(backTarget, "utf8") !== output) {
    throw new Error("Backend city coordinates are stale: node scripts/prepare-city-coords.mjs");
  }
} else {
  fs.mkdirSync(backDir, { recursive: true });
  fs.writeFileSync(frontTarget, output);
  fs.writeFileSync(backTarget, output);
}
