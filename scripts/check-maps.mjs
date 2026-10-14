import assert from "node:assert/strict";
import { provinceNeighbors } from "./prepare-province-neighbors.mjs";
import { readFile } from "node:fs/promises";
import { geoArea, geoMercator, geoPath } from "d3-geo";
import { normalizeMap } from "../src/lib/maps.ts";
import { buildShareGeometry, GEOMETRY_FILE } from "./prepare-share-geometry.mjs";
const regions = JSON.parse(await readFile("src/regions.json", "utf8"));
assert.equal(regions.length, 34);
const national = normalizeMap(JSON.parse(await readFile('public/maps/100000.json', 'utf8')));
assert.equal(national.features.filter(f => f.properties.name).length, 34);
for (const f of national.features) assert.ok(geoArea(f) < 2 * Math.PI, `National map: ${f.properties.name} has inverted winding`);
const storedNeighbors = JSON.parse(await readFile("src/province-neighbors.json", "utf8"));
assert.deepEqual(storedNeighbors, provinceNeighbors(national), "Province adjacency is stale: run npm run prepare:map-colors");
const codes = new Set();
let total = 0;
for (const province of regions) {
  const raw = JSON.parse(
    await readFile(`public/maps/${province.code}.json`, "utf8"),
  );
  const map = normalizeMap(raw);
  const projection = geoMercator().fitExtent(
    [
      [0, 0],
      [850, 620],
    ],
    map,
  );
  const path = geoPath(projection);
  assert.ok(map.features.length > 0, province.name);
  assert.equal(
    map.features.filter((f) => f.properties.name).length,
    province.cities.length,
  );
  for (const f of map.features) {
    assert.ok(
      geoArea(f) < 2 * Math.PI,
      `${province.name}/${f.properties.name} has inverted spherical winding`,
    );
    const drawing = path(f);
    assert.ok(
      drawing && !drawing.includes("NaN"),
      `${province.name}/${f.properties.name} cannot render`,
    );
    if (!f.properties.name) continue;
    const code = String(f.properties.adcode);
    assert.ok(!codes.has(code), `${code} is duplicated`);
    codes.add(code);
    assert.ok(
      province.cities.some(
        (c) => c.code === code && c.name === f.properties.name,
      ),
    );
    total++;
  }
}
// The Go backend draws the share preview from baked geometry; it must be exactly what
// the app would project from the shipped national map and its fixed palette.
const shareGeometry = JSON.parse(await readFile(GEOMETRY_FILE, "utf8"));
assert.deepEqual(
  shareGeometry,
  JSON.parse(JSON.stringify(buildShareGeometry(JSON.parse(await readFile("public/maps/100000.json", "utf8")), regions))),
  `Share preview geometry is stale: run npm run prepare:share-geometry`,
);
assert.deepEqual(
  shareGeometry.provinces.map((p) => p.code),
  regions.map((r) => r.code),
  "Share geometry province IDs differ from regions.json",
);
for (const p of shareGeometry.provinces) assert.ok(p.rings.length > 0 && /^#[0-9a-f]{6}$/i.test(p.color), `${p.code} has no usable share geometry`);
console.log(
  `Map checks passed: 34 provinces, ${total} city/district regions, valid projections and unique identifiers.`,
);
