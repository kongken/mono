import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import catalog from "@kongken/geo-data/world-catalog.json" with { type: "json" };
import searchIndex from "@kongken/geo-data/world-search-index.json" with { type: "json" };
import regions from "@kongken/geo-data/regions.json" with { type: "json" };
import { buildWorld, SOURCE, verifyInput } from "./prepare-world-map.mjs";
import { assertChinaBaseline, checkWorldMap, CHINA_BASELINE_SHA256, WORLD_BUDGET_BYTES } from "./check-world-map.mjs";
import { searchIndexText } from "./prepare-world-map.mjs";
import { searchWorldCountries, loadWorldCatalog } from "../src/lib/world-catalog.ts";
import {
  buildWorldMap,
  drawOrder,
  hitTestWorld,
  loadWorldMap,
  WorldMapError,
} from "../src/lib/world-map.ts";
import {
  WORLD_NOTICE_CHINA,
  WORLD_NOTICE_DISCLAIMER,
  WORLD_NOTICE_LIMITS,
  worldNoticeSource,
} from "../src/lib/world-notice.ts";

const read = (path) => JSON.parse(readFileSync(path, "utf8"));
const worldFile = read("public/maps/world.json");
const chinaFile = read("public/maps/100000.json");
const provinceCodes = regions.map((r) => r.code);
const map = buildWorldMap(worldFile, chinaFile, catalog, provinceCodes);
const find = (query) => searchWorldCountries(catalog.countries, searchIndex, query).map((c) => c.code);

test("the committed world data passes the offline checks and fits the size budget", async () => {
  const result = await checkWorldMap();
  assert.equal(result.countries, 247);
  assert.ok(result.gzipBytes <= WORLD_BUDGET_BYTES);
});

const bytes = (value) => Buffer.from(typeof value === "string" ? value : JSON.stringify(value));
const sha = (value) => createHash("sha256").update(value).digest("hex");
const catalogText = readFileSync("../../packages/geo-data/data/world-catalog.json", "utf8");
const indexText = readFileSync("../../packages/geo-data/data/world-search-index.json", "utf8");

test("a tampered search index is rejected by the offline check", async () => {
  const wrongJp = JSON.stringify({ ...searchIndex, JP: ["zzz", "z"] }, null, 2) + "\n";
  await assert.rejects(checkWorldMap({ indexBytes: bytes(wrongJp) }), /differs from the catalog/);
  const missing = { ...searchIndex };
  delete missing.JP;
  await assert.rejects(checkWorldMap({ indexBytes: bytes(JSON.stringify(missing, null, 2) + "\n") }), /differs from the catalog|misses entries/);
  // Forging the catalog's recorded SHA-256 as well is not enough: the content digest
  // over the catalog breaks, and so does the offline regeneration from its names.
  const forged = JSON.parse(catalogText);
  forged.searchIndex.sha256 = sha(wrongJp);
  await assert.rejects(checkWorldMap({ indexBytes: bytes(wrongJp), catalogBytes: bytes(JSON.stringify(forged)) }), /digest is stale/);
  const { version: _version, ...body } = forged;
  const reDigested = { version: "sha256:" + sha(JSON.stringify(body)), ...body };
  await assert.rejects(checkWorldMap({ indexBytes: bytes(wrongJp), catalogBytes: bytes(JSON.stringify(reDigested)) }), /search index is stale/);
});

test("the search index is exactly what the catalog's names and aliases generate", () => {
  assert.equal(indexText, searchIndexText(catalog.countries));
  assert.equal(catalog.searchIndex.sha256, sha(indexText));
  assert.equal(catalog.searchIndex.file, "world-search-index.json");
});

test("CN stays the unmodified national map: the baseline digest is fixed in the check", async () => {
  const original = readFileSync("public/maps/100000.json");
  assert.equal(sha(original), CHINA_BASELINE_SHA256);
  assert.doesNotThrow(() => assertChinaBaseline(original));
  assert.throws(() => assertChinaBaseline(Buffer.concat([original, Buffer.from("\n")])), /CN baseline/);
  await assert.rejects(checkWorldMap({ chinaBytes: Buffer.concat([original, Buffer.from(" ")]) }), /CN baseline/);
});

test("a hand-edited world.json is rejected", async () => {
  const edited = JSON.parse(readFileSync("public/maps/world.json", "utf8"));
  edited.features[0].geometry.coordinates[0][0][0][0] += 0.01;
  await assert.rejects(checkWorldMap({ geometryBytes: bytes(edited) }), /differs from the catalog/);
});

test("the catalog is ISO 3166-1 with HK, MO and TW merged into CN, plus XK", () => {
  const codes = catalog.countries.map((c) => c.code);
  assert.equal(codes.length, 247);
  assert.equal(new Set(codes).size, 247);
  for (const code of ["HK", "MO", "TW"]) assert.ok(!codes.includes(code), code);
  for (const code of ["CN", "XK", "JP", "EH", "PS"]) assert.ok(codes.includes(code), code);
  assert.equal(catalog.countries.filter((c) => !c.hasShape).length, 13);
  assert.ok(catalog.countries.find((c) => c.code === "CN").hasShape);
});

test("world.json has one shape per catalog entry with a shape, and never CN, HK, MO or TW", () => {
  const ids = worldFile.features.map((f) => f.properties.code);
  assert.equal(ids.length, 233);
  assert.deepEqual([...ids].sort(), catalog.countries.filter((c) => c.hasShape && c.code !== "CN").map((c) => c.code).sort());
  for (const code of ["CN", "HK", "MO", "TW"]) assert.ok(!ids.includes(code), code);
  for (const code of ["SO", "CY", "AU"]) assert.ok(ids.includes(code));
});

test("CN is the unmodified national map: 34 provinces plus the 10-part sea line", () => {
  assert.equal(map.china.length, 34);
  assert.ok(map.china.every((s) => s.code === "CN"));
  assert.equal(map.chinaLines.length, 1);
  assert.equal(map.chinaLines[0].geometry.coordinates.length, 10);
  assert.equal(catalog.china.sha256, createHash("sha256").update(readFileSync("public/maps/100000.json")).digest("hex"));
  // Taiwan, Hong Kong and Macao are provinces of the national map, hence CN.
  assert.deepEqual(
    ["710000", "810000", "820000"].filter((id) => !provinceCodes.includes(id)),
    [],
  );
  assert.equal(drawOrder(map).at(-1).code, "CN", "CN is drawn last");
});

test("buildWorldMap refuses a map that does not match the catalog or the province IDs", () => {
  assert.throws(() => buildWorldMap({ ...worldFile, features: worldFile.features.slice(1) }, chinaFile, catalog, provinceCodes), WorldMapError);
  const withCN = { ...worldFile, features: [...worldFile.features, { ...worldFile.features[0], properties: { code: "CN" } }] };
  assert.throws(() => buildWorldMap(withCN, chinaFile, catalog, provinceCodes), WorldMapError);
  const withTW = { ...worldFile, features: [...worldFile.features, { ...worldFile.features[0], properties: { code: "TW" } }] };
  assert.throws(() => buildWorldMap(withTW, chinaFile, catalog, provinceCodes), WorldMapError);
  assert.throws(() => buildWorldMap(worldFile, chinaFile, catalog, provinceCodes.slice(1)), WorldMapError);
  const fewerProvinces = { ...chinaFile, features: chinaFile.features.slice(1) };
  assert.throws(() => buildWorldMap(worldFile, fewerProvinces, catalog, provinceCodes), WorldMapError);
});

test("hit testing puts CN first and falls back to the source polygons", () => {
  assert.equal(hitTestWorld(map, 104.07, 30.67), "CN"); // Chengdu
  assert.equal(hitTestWorld(map, 121.5, 25.04), "CN"); // Taipei
  assert.equal(hitTestWorld(map, 114.17, 22.32), "CN"); // Hong Kong
  assert.equal(hitTestWorld(map, 94.7, 28.2), "CN"); // contested border land: the national map wins
  assert.equal(hitTestWorld(map, 139.69, 35.69), "JP");
  assert.equal(hitTestWorld(map, -0.12, 51.5), "GB");
  assert.equal(hitTestWorld(map, 0, 0), null);
  assert.equal(hitTestWorld(map, -53, 4), "FR", "French Guiana lies inside the France polygon");
});

test("search finds a country by Chinese name, pinyin, initials, English name and ISO code", () => {
  for (const q of ["日本", "riben", "RIBEN", "rb", "Japan", "japan", "JP", "jp", "  ＪＡＰＡＮ "]) assert.equal(find(q)[0], "JP", q);
  assert.equal(find("united states")[0], "US");
  assert.equal(find("meiguo")[0], "US");
  assert.equal(find("hanguo")[0], "KR");
  assert.equal(find("Korea").slice(0, 2).sort().join(), "KP,KR");
  assert.deepEqual(find("刚果").sort(), ["CD", "CG"]);
  assert.equal(find("Japn")[0], "JP", "one typo still finds it when nothing else matches");
  assert.deepEqual(find("zzzzzz"), []);
  assert.equal(find("").length, 247);
});

test("HK, MO and TW names lead to CN, and entries without a shape stay searchable", () => {
  for (const q of ["台湾", "Taiwan", "香港", "澳门", "Macau", "xianggang"]) assert.equal(find(q)[0], "CN", q);
  assert.equal(find("圣诞岛")[0], "CX");
  assert.equal(find("French Guiana")[0], "GF");
  assert.equal(find("北塞浦路斯")[0], "CY");
  assert.equal(find("索马里兰")[0], "SO");
});

test("the catalog loader returns the same data as the JSON files", async () => {
  const loaded = await loadWorldCatalog();
  assert.equal(loaded.catalog.version, catalog.version);
  assert.equal(Object.keys(loaded.index).length, Object.keys(searchIndex).length);
});

test("the map loader requests only the two same-origin static files", async () => {
  const urls = [];
  const fetchJson = async (url) => {
    urls.push(String(url));
    return { ok: true, json: async () => (String(url).endsWith("world.json") ? worldFile : chinaFile) };
  };
  const loaded = await loadWorldMap("/app/", catalog, provinceCodes, undefined, fetchJson);
  assert.deepEqual(urls.sort(), ["/app/maps/100000.json", "/app/maps/world.json"]);
  assert.equal(loaded.countries.length, 233);
  await assert.rejects(loadWorldMap("/", catalog, provinceCodes, undefined, async () => ({ ok: false })), WorldMapError);
  for (const file of ["world-map.ts", "world-catalog.ts", "world-notice.ts"]) {
    assert.doesNotMatch(readFileSync(`src/lib/${file}`, "utf8"), /https?:\/\//, `${file} must not reference a remote URL`);
  }
});

test("the source notice names the data, its licence and every merge and exception", () => {
  assert.equal(SOURCE.name, "Natural Earth");
  assert.equal(catalog.source.licenseUrl, "https://www.naturalearthdata.com/about/terms-of-use/");
  assert.match(catalog.source.licenseText, /public domain/);
  const line = worldNoticeSource(catalog.source, catalog.transform.precisionDecimals);
  for (const part of ["Natural Earth", catalog.source.version, "公有领域", "2"]) assert.ok(line.includes(part), part);
  assert.match(WORLD_NOTICE_DISCLAIMER, /官方地图/);
  assert.match(WORLD_NOTICE_DISCLAIMER, /主权/);
  assert.match(WORLD_NOTICE_CHINA, /港澳台|香港、澳门、台湾/);
  const limits = WORLD_NOTICE_LIMITS.join("\n");
  assert.deepEqual(catalog.transform.merged.map((m) => `${m.adm0}>${m.into}`), ["ATC>AU", "CYN>CY", "IOA>AU", "SOL>SO"]);
  for (const part of ["北塞浦路斯", "索马里兰", "印度洋领地", "阿什莫尔和卡捷群岛", "锡亚琴冰川", "缝隙", "列表"]) assert.ok(limits.includes(part), part);
  assert.deepEqual(catalog.transform.dropped.map((d) => d.adm0), ["KAS"]);
  assert.deepEqual(catalog.transform.droppedForCN, ["CHN", "HKG", "MAC", "TWN"]);
});

// A source feature in the shape of Natural Earth's, for the generator's unit tests.
const square = (x, y, size = 1) => [[[x, y], [x + size, y], [x + size, y + size], [x, y + size], [x, y]]];
const feature = (props, rings) => ({
  type: "Feature",
  properties: { NAME: props.NAME ?? props.ADM0_A3, NAME_ZH: props.NAME_ZH ?? "测试", NAME_LONG: props.NAME_LONG ?? props.ADM0_A3, CONTINENT: "Asia", LABEL_X: 1.234, LABEL_Y: 2.346, ...props },
  geometry: { type: "Polygon", coordinates: rings },
});
const fixture = () =>
  JSON.stringify({
    type: "FeatureCollection",
    features: [
      feature({ ADM0_A3: "JPN", ISO_A2_EH: "JP", NAME_ZH: "日本" }, [[[130.123, 31.456], [140.789, 31.456], [140.789, 45.001], [130.123, 45.001], [130.123, 31.456]]]),
      feature({ ADM0_A3: "CHN", ISO_A2_EH: "CN" }, square(75, 20, 50)),
      feature({ ADM0_A3: "TWN", ISO_A2_EH: "TW" }, square(120, 22)),
      feature({ ADM0_A3: "HKG", ISO_A2_EH: "HK" }, square(114, 22)),
      feature({ ADM0_A3: "MAC", ISO_A2_EH: "MO" }, square(113, 22)),
      feature({ ADM0_A3: "SOM", ISO_A2_EH: "SO" }, square(40, 0)),
      feature({ ADM0_A3: "SOL", ISO_A2_EH: "-99", NAME: "Somaliland" }, square(45, 8)),
      feature({ ADM0_A3: "KAS", ISO_A2_EH: "-99" }, square(77, 35)),
      {
        ...feature({ ADM0_A3: "ISL", ISO_A2_EH: "IS" }, []),
        geometry: { type: "MultiPolygon", coordinates: [square(-20, 64), [[[0.001, 0.001], [0.004, 0.001], [0.004, 0.004], [0.001, 0.001]]]] },
      },
    ],
  });
const build = () => buildWorld(fixture(), "0".repeat(64), { partial: true });

test("the generator is deterministic and applies the merge, drop and rounding rules", () => {
  const a = build();
  const b = build();
  assert.equal(a.geometryText, b.geometryText);
  assert.equal(a.catalogText, b.catalogText);
  assert.equal(a.indexText, b.indexText);
  const world = JSON.parse(a.geometryText);
  const polygons = (code) => world.features.find((f) => f.properties.code === code)?.geometry.coordinates;
  assert.deepEqual(world.features.map((f) => f.properties.code), ["IS", "JP", "SO"], "sorted by code, without CN, HK, MO, TW and the unassigned glacier");
  assert.equal(polygons("SO").length, 2, "Somaliland is merged into Somalia");
  assert.deepEqual(polygons("JP")[0][0][0], [130.12, 31.46], "coordinates are rounded to 2 decimals");
  assert.equal(polygons("IS").length, 1, "a polygon that collapses after rounding is dropped");
  assert.equal(a.stats.droppedPolygons.length, 1);
  const out = JSON.parse(a.catalogText);
  assert.deepEqual(out.countries.map((c) => c.code), ["CN", "IS", "JP", "SO"]);
  assert.ok(out.countries.find((c) => c.code === "SO").aliases.includes("Somaliland"));
  assert.equal(out.geometry.features, 3);
  assert.match(out.version, /^sha256:[0-9a-f]{64}$/);
  assert.deepEqual(out.transform.dropped.map((d) => d.adm0), ["KAS"]);
  assert.equal(JSON.parse(a.indexText).JP[0], "riben");
});

test("the generator refuses an unexpected source feature and an incomplete catalog", () => {
  const bad = JSON.parse(fixture());
  bad.features.push(feature({ ADM0_A3: "XXX", ISO_A2_EH: "-99" }, square(0, 0)));
  assert.throws(() => buildWorld(JSON.stringify(bad), "0".repeat(64), { partial: true }), /without an ISO code/);
  assert.throws(() => buildWorld(fixture(), "0".repeat(64)), /differ from ISO 3166-1/);
});

test("the pinned input is verified before anything is generated", () => {
  assert.throws(() => verifyInput(Buffer.from("{}")), /refusing to generate/);
  assert.match(SOURCE.url, /f1890d9f152c896d250a77557a5751a93d494776/);
  assert.match(SOURCE.inputSha256, /^[0-9a-f]{64}$/);
});
