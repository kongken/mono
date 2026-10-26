// Informational report for reviewers (docs/trip-map/world-spec.md section 10): how much
// the neighbours' source polygons overlap the national map (`CN`) and how much
// land-adjacent gap lies between the two sources. A 0.25-degree grid, cell centres
// tested with geoContains, area weighted by cos(latitude). Not a pass/fail check.
//
//   npm run report:world-overlap
import { readFile } from "node:fs/promises";
import { geoContains } from "d3-geo";
import { buildWorldMap } from "../src/lib/world-map.ts";

const root = new URL("../", import.meta.url);
const data = new URL("../../../packages/geo-data/data/", import.meta.url);
const catalog = JSON.parse(await readFile(new URL("world-catalog.json", data), "utf8"));
const regions = JSON.parse(await readFile(new URL("regions.json", data), "utf8"));
const map = buildWorldMap(
  JSON.parse(await readFile(new URL("public/maps/world.json", root), "utf8")),
  JSON.parse(await readFile(new URL("public/maps/100000.json", root), "utf8")),
  catalog,
  regions.map((r) => r.code),
);

const STEP = 0.25;
const R = 6371.0088; // km
const cellArea = (lat) => (((R * STEP * Math.PI) / 180) ** 2) * Math.cos((lat * Math.PI) / 180);
const inBox = (s, lon, lat) => lon >= s.bbox[0] && lon <= s.bbox[2] && lat >= s.bbox[1] && lat <= s.bbox[3];
const cnAt = (lon, lat) => map.china.some((s) => inBox(s, lon, lat) && geoContains(s.feature, [lon, lat]));
const neighbourAt = (lon, lat) => map.countries.find((s) => inBox(s, lon, lat) && geoContains(s.feature, [lon, lat]))?.code;

// Cells within one degree of the national map's extent.
const box = map.china.reduce((b, s) => [Math.min(b[0], s.bbox[0]), Math.min(b[1], s.bbox[1]), Math.max(b[2], s.bbox[2]), Math.max(b[3], s.bbox[3])], [Infinity, Infinity, -Infinity, -Infinity]);
const lons = [];
for (let x = Math.floor(box[0]) - 1 + STEP / 2; x < box[2] + 1; x += STEP) lons.push(x);
const lats = [];
for (let y = Math.floor(box[1]) - 1 + STEP / 2; y < box[3] + 1; y += STEP) lats.push(y);

const cn = lats.map((lat) => lons.map((lon) => cnAt(lon, lat)));
const other = lats.map((lat, j) => lons.map((lon, i) => (cn[j][i] ? undefined : neighbourAt(lon, lat))));
const overlap = new Map();
let overlapTotal = 0;
let holeTotal = 0;
lats.forEach((lat, j) =>
  lons.forEach((lon, i) => {
    const n = neighbourAt(lon, lat);
    if (cn[j][i] && n) {
      overlap.set(n, (overlap.get(n) ?? 0) + cellArea(lat));
      overlapTotal += cellArea(lat);
    }
    if (!cn[j][i] && !other[j][i]) {
      let nearCn = false;
      let nearOther = false;
      for (let dj = -1; dj <= 1; dj++)
        for (let di = -1; di <= 1; di++) {
          nearCn ||= Boolean(cn[j + dj]?.[i + di]);
          nearOther ||= Boolean(other[j + dj]?.[i + di]);
        }
      if (nearCn && nearOther) holeTotal += cellArea(lat);
    }
  }),
);
const km2 = (v) => `${Math.round(v).toLocaleString("en-US")} km2`;
console.log(`Grid ${STEP} deg, ${lons.length}x${lats.length} cells around the national map.`);
console.log(`Overlap (neighbour polygon inside CN, CN drawn on top): ${km2(overlapTotal)}`);
for (const [code, v] of [...overlap].sort((a, b) => b[1] - a[1])) console.log(`  ${code}: ${km2(v)}`);
console.log(`Gap (in neither source, adjacent to both): ${km2(holeTotal)}`);
