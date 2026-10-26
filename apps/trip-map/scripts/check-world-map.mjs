// Offline checks of the committed world-view data (docs/trip-map/world-spec.md
// section 10). Uses only d3-geo and Node; it never downloads anything. A fresh
// regeneration from the pinned input is `npm run prepare:world -- --verify`.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { gzipSync } from "node:zlib";
import { geoArea, geoPath } from "d3-geo";
import { buildWorldMap, createWorldProjection, hitTestWorld, MERGED_INTO_CN } from "../src/lib/world-map.ts";
import { ISO_3166_1_ALPHA2 } from "./world-iso-codes.mjs";
import { searchIndexText } from "./prepare-world-map.mjs";

export const WORLD_BUDGET_BYTES = 600 * 1024;
// SHA-256 of apps/trip-map/public/maps/100000.json as the world view uses it: CN is that
// file unmodified. Changing the national map on purpose means changing this value in the
// same commit (and re-running `npm run prepare:world`); a stray edit fails the check.
export const CHINA_BASELINE_SHA256 = "99adfeded5223848bbe37a0a12f8023e11ee12161c7800521c27db42fdeac275";
const root = new URL("../", import.meta.url);
const data = new URL("../../../packages/geo-data/data/", import.meta.url);
const sha = (buffer) => createHash("sha256").update(buffer).digest("hex");

export function assertChinaBaseline(chinaBytes) {
  assert.equal(
    sha(chinaBytes),
    CHINA_BASELINE_SHA256,
    "public/maps/100000.json differs from the CN baseline of the world view (CHINA_BASELINE_SHA256 in check-world-map.mjs)",
  );
}

/** `override` replaces files read from disk; the regression tests use it to prove that a
 * tampered file is rejected. */
export async function checkWorldMap(override = {}) {
  const geometryBytes = override.geometryBytes ?? (await readFile(new URL("public/maps/world.json", root)));
  const chinaBytes = override.chinaBytes ?? (await readFile(new URL("public/maps/100000.json", root)));
  const catalogBytes = override.catalogBytes ?? (await readFile(new URL("world-catalog.json", data)));
  const indexBytes = override.indexBytes ?? (await readFile(new URL("world-search-index.json", data)));
  const catalog = JSON.parse(catalogBytes);
  const index = JSON.parse(indexBytes);
  const regions = JSON.parse(await readFile(new URL("regions.json", data), "utf8"));

  // Provenance and digests: the catalog pins the input, the licence, and both outputs.
  const { source } = catalog;
  assert.match(source.inputSha256, /^[0-9a-f]{64}$/, "world source input SHA-256 is missing");
  assert.ok(source.url.includes(source.commit) && /^[0-9a-f]{40}$/.test(source.commit), "world source URL is not pinned to a commit");
  assert.ok(source.license && source.licenseUrl.startsWith("https://") && source.licenseText, "world source licence is not recorded");
  assert.equal(catalog.geometry.sha256, sha(geometryBytes), "world.json differs from the catalog: npm run prepare:world");
  assert.equal(catalog.geometry.bytes, geometryBytes.length);
  assertChinaBaseline(chinaBytes);
  assert.equal(catalog.china.sha256, sha(chinaBytes), "100000.json changed after the world data was generated: npm run prepare:world");
  assert.equal(catalog.searchIndex.sha256, sha(indexBytes), "world-search-index.json differs from the catalog: npm run prepare:world");
  const { version, ...body } = catalog;
  assert.equal(version, "sha256:" + sha(JSON.stringify(body)), "world catalog digest is stale: npm run prepare:world");

  // Catalog: every ISO 3166-1 entry, HK/MO/TW merged into CN, plus XK.
  const codes = catalog.countries.map((c) => c.code);
  const expected = [...ISO_3166_1_ALPHA2.filter((c) => !MERGED_INTO_CN.includes(c)), "XK"].sort();
  assert.equal(expected.length, 247);
  assert.deepEqual(codes, expected, "world catalog differs from ISO 3166-1 (minus HK/MO/TW, plus XK)");
  const seenNames = new Set();
  for (const c of catalog.countries) {
    assert.ok(c.name && c.en && catalog.continents[c.continent], `${c.code} is incomplete`);
    assert.equal(typeof c.hasShape, "boolean");
    assert.ok(Array.isArray(c.point) && c.point.length === 2 && Math.abs(c.point[0]) <= 180 && Math.abs(c.point[1]) <= 90, `${c.code} has no label point`);
    assert.ok(!seenNames.has(c.name), `${c.name} is duplicated`);
    seenNames.add(c.name);
  }
  assert.equal(indexBytes.toString("utf8"), searchIndexText(catalog.countries), "world search index is stale: npm run prepare:world");
  assert.deepEqual(Object.keys(index).filter((c) => !codes.includes(c)), [], "search index has unknown codes");
  assert.deepEqual(codes.filter((c) => /\p{Script=Han}/u.test(catalog.countries.find((x) => x.code === c).name) && !index[c]), [], "search index misses entries");

  // The backend validates country codes with its own copy of the catalog (internal/tripmap/world).
  const backendCatalogBytes = override.backendCatalogBytes ?? (await readFile(new URL("../../../internal/tripmap/world/catalog.json", import.meta.url)));
  assert.ok(Buffer.compare(backendCatalogBytes, catalogBytes) === 0, "internal/tripmap/world/catalog.json differs from world-catalog.json: npm run prepare:world");

  // Geometry: one shape per hasShape entry (CN comes from the national map), CN is the
  // 34 provinces plus the sea-boundary line, and everything renders.
  const world = JSON.parse(geometryBytes);
  const map = buildWorldMap(world, JSON.parse(chinaBytes), catalog, regions.map((r) => r.code));
  assert.equal(map.countries.length, catalog.geometry.features);
  assert.equal(map.china.length, 34);
  const path = geoPath(createWorldProjection(1000, 500));
  for (const s of [...map.countries, ...map.china]) {
    assert.ok(geoArea(s.feature) < 2 * Math.PI, `${s.code} has inverted spherical winding`);
    const d = path(s.feature);
    assert.ok(d && !d.includes("NaN"), `${s.code} cannot render`);
  }
  for (const f of world.features) {
    for (const polygon of f.geometry.coordinates) {
      for (const ring of polygon) {
        assert.ok(ring.length >= 4, `${f.properties.code} has a degenerate ring`);
        for (const [x, y] of ring) {
          assert.ok(Math.abs(x) <= 180 && Math.abs(y) <= 90 && Math.round(x * 100) / 100 === x && Math.round(y * 100) / 100 === y, `${f.properties.code} has an off-grid coordinate`);
        }
      }
    }
  }
  // CN wins over the neighbours: Taiwan, Hong Kong and Macao are CN; Japan stays Japan.
  assert.equal(hitTestWorld(map, 121.5, 25.0), "CN");
  assert.equal(hitTestWorld(map, 114.17, 22.3), "CN");
  assert.equal(hitTestWorld(map, 139.7, 35.7), "JP");

  // Size: the world resources added by this view, gzip level 9. The unmodified national
  // map is reused and is not part of the budget.
  const gz = [geometryBytes, catalogBytes, indexBytes].map((b) => gzipSync(b, { level: 9 }).length);
  const total = gz.reduce((a, b) => a + b, 0);
  assert.ok(total <= WORLD_BUDGET_BYTES, `world resources are ${total} bytes gzip, over the ${WORLD_BUDGET_BYTES} budget`);
  return { countries: catalog.countries.length, shapes: map.countries.length, gzipBytes: total };
}
