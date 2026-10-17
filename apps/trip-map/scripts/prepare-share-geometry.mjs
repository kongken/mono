import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { geoPath } from "d3-geo";
import { normalizeMap } from "../src/lib/maps.ts";
import { mapProjection, SEA_INSET, seaInsetPaths } from "../src/lib/projection.ts";
import { nationalProvinceColors } from "../src/lib/travel.ts";

export const GEOMETRY_FILE = "../../internal/tripmap/share/assets/geometry.json";

// d3 emits "M x,y L x,y ... Z" for polygons; return one point list per ring.
function rings(path) {
  return (path || "")
    .split(/(?=M)/)
    .filter(Boolean)
    .map((part) => {
      const numbers = part.match(/-?\d+(?:\.\d+)?(?:e-?\d+)?/g) ?? [];
      const ring = [];
      for (let i = 0; i + 1 < numbers.length; i += 2) {
        const point = [Number(numbers[i]), Number(numbers[i + 1])];
        const last = ring[ring.length - 1];
        if (!last || last[0] !== point[0] || last[1] !== point[1]) ring.push(point);
      }
      return ring;
    })
    .filter((ring) => ring.length >= 3);
}

/**
 * The national map exactly as the app projects it, as polygon rings per province,
 * with the fixed palette the app assigns. The Go backend rasterizes the share
 * preview image from this file, so it needs no map library at run time.
 */
export function buildShareGeometry(nationalMap, regions) {
  const map = normalizeMap(nationalMap);
  const path = geoPath(mapProjection(map)).digits(1);
  const colors = nationalProvinceColors({
    version: 2,
    visits: [],
    provinces: Object.fromEntries(regions.map((r) => [r.code, "x"])),
  });
  const provinces = regions.map((region) => {
    const feature = map.features.find((f) => f.properties.name && String(f.properties.adcode) === region.code);
    if (!feature) throw new Error(`national map has no province ${region.code}`);
    return { code: region.code, name: region.name, short: region.short, color: colors[region.code], rings: rings(path(feature)) };
  });
  const inset = seaInsetPaths(map).flatMap((d) => rings(d.replace(/(\d+\.\d{1})\d+/g, "$1")));
  return { version: 1, provinces, inset: { ...SEA_INSET, rings: inset } };
}

export async function prepareShareGeometry() {
  const map = JSON.parse(await readFile("public/maps/100000.json", "utf8"));
  const regions = JSON.parse(await readFile("../../packages/geo-data/data/regions.json", "utf8"));
  const geometry = buildShareGeometry(map, regions);
  await writeFile(GEOMETRY_FILE, JSON.stringify(geometry) + "\n");
  console.log(`Prepared share preview geometry for ${geometry.provinces.length} provinces.`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) await prepareShareGeometry();
