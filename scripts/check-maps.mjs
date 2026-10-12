import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { geoArea, geoMercator, geoPath } from "d3-geo";
import { normalizeMap } from "../src/lib/maps.ts";
const regions = JSON.parse(await readFile("src/regions.json", "utf8"));
assert.equal(regions.length, 34);
const national = normalizeMap(JSON.parse(await readFile('public/maps/100000.json', 'utf8')));
assert.equal(national.features.filter(f => f.properties.name).length, 34);
for (const f of national.features) assert.ok(geoArea(f) < 2 * Math.PI, `National map: ${f.properties.name} has inverted winding`);
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
console.log(
  `Map checks passed: 34 provinces, ${total} city/district regions, valid projections and unique identifiers.`,
);
