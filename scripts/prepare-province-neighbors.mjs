import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

// The bundled national map uses matching border segments. Round coordinates to
// four decimals to tolerate serialization noise, and ignore point-only contact.
export function provinceNeighbors(map) {
  const neighbors = {};
  const owners = new Map();
  for (const feature of map.features.filter(f => f.properties.name)) {
    const code = String(feature.properties.adcode);
    neighbors[code] = new Set();
    const geometry = feature.geometry;
    const polygons = geometry.type === "Polygon" ? [geometry.coordinates] : geometry.coordinates;
    for (const polygon of polygons) {
      for (const ring of polygon) {
        for (let i = 1; i < ring.length; i++) {
          const start = ring[i - 1].map(value => value.toFixed(4)).join(",");
          const end = ring[i].map(value => value.toFixed(4)).join(",");
          if (start === end) continue;
          const edge = [start, end].sort().join(";");
          if (!owners.has(edge)) owners.set(edge, new Set());
          owners.get(edge).add(code);
        }
      }
    }
  }
  for (const codes of owners.values()) {
    for (const code of codes) {
      for (const other of codes) if (code !== other) neighbors[code].add(other);
    }
  }
  return Object.fromEntries(Object.keys(neighbors).sort().map(code => [code, [...neighbors[code]].sort()]));
}

export async function prepareProvinceNeighbors() {
  const map = JSON.parse(await readFile("public/maps/100000.json", "utf8"));
  const neighbors = provinceNeighbors(map);
  await writeFile("src/province-neighbors.json", JSON.stringify(neighbors, null, 2) + "\n");
  console.log(`Prepared adjacency for ${Object.keys(neighbors).length} provinces.`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) await prepareProvinceNeighbors();
