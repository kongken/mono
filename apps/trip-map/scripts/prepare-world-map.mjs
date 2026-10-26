// Builds the world-view data (docs/trip-map/world-spec.md sections 3 and 10) from a
// pinned Natural Earth input:
//   apps/trip-map/public/maps/world.json            country/region polygons, CN excluded
//   packages/geo-data/data/world-catalog.json       catalog, source/licence, digests
//   packages/geo-data/data/world-search-index.json  pinyin keys for the Chinese names
//
//   npm run prepare:world                 download the input, verify SHA-256, write outputs
//   npm run prepare:world -- --input F    same, reading a local copy of the input (still verified)
//   npm run prepare:world -- --verify     regenerate in memory and compare with the committed files
//
// `npm run check:maps` only checks the committed outputs offline; it never downloads.
import fs from "node:fs";
import { createHash } from "node:crypto";
import { pathToFileURL } from "node:url";
import { pinyin } from "pinyin-pro";
import {
  ALIASES,
  CONTINENTS,
  EN_NAMES,
  NO_SHAPE,
  OPEN_OCEAN_CONTINENT,
  SOURCE_CONTINENT,
  ZH_NAMES,
} from "./world-names.mjs";
import { ISO_3166_1_ALPHA2 } from "./world-iso-codes.mjs";

export const SOURCE = {
  name: "Natural Earth",
  dataset: "Admin 0 - Countries, 1:50m",
  version: "v5.1.2",
  commit: "f1890d9f152c896d250a77557a5751a93d494776",
  url: "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/f1890d9f152c896d250a77557a5751a93d494776/geojson/ne_50m_admin_0_countries.geojson",
  inputSha256: "3e458fc036ad0a66411f2c1e6cac49c5d7bfb81cb1123bc513b22511a2b7fdeb",
  inputBytes: 3083490,
  license: "公有领域（public domain）",
  licenseUrl: "https://www.naturalearthdata.com/about/terms-of-use/",
  licenseText:
    "All versions of Natural Earth raster + vector map data found on this website are in the public domain. You may use the maps in any manner, including modifying the content and design, electronic dissemination, and offset printing.",
};

// Source features that do not become catalog entries of their own.
const DROP_CODES = new Set(["CN", "HK", "MO", "TW"]); // CN comes from public/maps/100000.json; HK/MO/TW are part of CN
const DROP_ADM0 = { KAS: "Siachen Glacier" }; // no ISO code and claimed by two states: not assigned to either
const MERGE_ADM0 = { SOL: "SO", CYN: "CY" }; // no ISO code: merged into the state that is generally recognised
const AUX_ADM0 = new Set([...Object.keys(MERGE_ADM0), "IOA", "ATC"]); // never the "main" feature of an entry
const PRECISION = 2; // decimal degrees, about 1.1 km

const GEOMETRY_FILE = new URL("../public/maps/world.json", import.meta.url);
const CATALOG_FILE = new URL("../../../packages/geo-data/data/world-catalog.json", import.meta.url);
const INDEX_FILE = new URL("../../../packages/geo-data/data/world-search-index.json", import.meta.url);
const CHINA_FILE = new URL("../public/maps/100000.json", import.meta.url);

export const sha256 = (data) => createHash("sha256").update(data).digest("hex");

const round = (v) => Math.round(v * 10 ** PRECISION) / 10 ** PRECISION + 0;
const same = (a, b) => a[0] === b[0] && a[1] === b[1];
function area(ring) {
  let sum = 0;
  for (let i = 0; i < ring.length - 1; i++) sum += ring[i][0] * ring[i + 1][1] - ring[i + 1][0] * ring[i][1];
  return sum / 2;
}
// Rounding to a fixed grid keeps vertices shared between neighbours identical, so
// borders stay watertight; only collapsed rings are removed.
function cleanRing(ring) {
  const out = [];
  for (const [x, y] of ring.slice(0, -1)) {
    const p = [round(x), round(y)];
    if (!out.length || !same(out[out.length - 1], p)) out.push(p);
  }
  while (out.length > 1 && same(out[0], out[out.length - 1])) out.pop();
  if (out.length < 3) return null;
  const closed = [...out, out[0]];
  return area(closed) === 0 ? null : closed;
}
function cleanPolygon(rings) {
  const outer = cleanRing(rings[0]);
  if (!outer) return null;
  return [outer, ...rings.slice(1).map(cleanRing).filter(Boolean)];
}

const prop = (value) => (value === null || value === undefined || value === "null" ? undefined : String(value));

function zhKeys(texts) {
  const keys = [];
  for (const text of texts) {
    const han = text.replace(/[^\p{Script=Han}]/gu, "");
    if (!han) continue;
    const reading = pinyin(han, { toneType: "none", type: "array", v: true });
    keys.push(reading.join(""), reading.map((s) => s[0]).join(""));
  }
  return [...new Set(keys)];
}

function assertIsoCoverage(countries) {
  const expected = [...ISO_3166_1_ALPHA2.filter((c) => !DROP_CODES.has(c) || c === "CN"), "XK"].sort();
  const actual = countries.map((c) => c.code);
  if (JSON.stringify(expected) !== JSON.stringify(actual)) {
    throw new Error(`Catalog codes differ from ISO 3166-1 (minus HK/MO/TW, plus XK): missing ${expected.filter((c) => !actual.includes(c))}, extra ${actual.filter((c) => !expected.includes(c))}`);
  }
}

// `partial` is for unit tests with a small fixture: it skips the full-catalog (ISO 3166-1) check.
export function buildWorld(inputText, chinaSha256, { partial = false } = {}) {
  const source = JSON.parse(inputText);
  const groups = new Map();
  const dropped = [];
  let chinaFeature;
  for (const feature of source.features) {
    const p = feature.properties;
    const adm0 = p.ADM0_A3;
    if (DROP_ADM0[adm0]) {
      dropped.push({ adm0, name: DROP_ADM0[adm0], reason: "no ISO code, claimed by more than one state" });
      continue;
    }
    const code = MERGE_ADM0[adm0] ?? p.ISO_A2_EH;
    if (!code || code === "-99") throw new Error(`Unexpected source feature without an ISO code: ${adm0}`);
    if (code === "CN") chinaFeature = feature;
    if (DROP_CODES.has(code)) {
      dropped.push({ adm0, name: p.NAME, reason: code === "CN" ? "replaced by public/maps/100000.json" : "part of CN" });
      continue;
    }
    if (!groups.has(code)) groups.set(code, []);
    groups.get(code).push(feature);
  }
  if (!chinaFeature) throw new Error("The source has no CN feature to take names and label position from");
  const norm = (s) => s.normalize("NFKC").toLowerCase().replace(/[\s'’_-]/gu, "");
  const entry = (code, main, aux, point) => {
    const name = ZH_NAMES[code] ?? main.NAME_ZH;
    const en = EN_NAMES[code] ?? main.NAME_LONG;
    const continent = SOURCE_CONTINENT[main.CONTINENT] ?? OPEN_OCEAN_CONTINENT[code];
    if (!continent) throw new Error(`${code}: no continent for ${main.CONTINENT}`);
    const names = [main.NAME, main.NAME_ZH, prop(main.FORMAL_EN), ...aux.flatMap((f) => [f.properties.NAME, f.properties.NAME_ZH]), ...(ALIASES[code] ?? [])];
    const aliases = [...new Set(names.filter(Boolean).map(String))]
      .filter((a) => norm(a) !== norm(name) && norm(a) !== norm(en))
      .sort();
    return { code, name, en, aliases, continent, hasShape: true, point };
  };

  const features = [];
  const countries = [];
  const droppedPolygons = [];
  const merged = [];
  for (const code of [...groups.keys()].sort()) {
    const group = groups.get(code);
    const mains = group.filter((f) => !AUX_ADM0.has(f.properties.ADM0_A3));
    if (mains.length !== 1) throw new Error(`${code}: expected one main source feature, found ${mains.length}`);
    const main = mains[0].properties;
    const aux = group.filter((f) => AUX_ADM0.has(f.properties.ADM0_A3)).sort((a, b) => (a.properties.ADM0_A3 < b.properties.ADM0_A3 ? -1 : 1));
    for (const f of aux) merged.push({ adm0: f.properties.ADM0_A3, name: f.properties.NAME, into: code });

    const polygons = [];
    for (const feature of [mains[0], ...aux]) {
      const g = feature.geometry;
      for (const rings of g.type === "Polygon" ? [g.coordinates] : g.coordinates) {
        const polygon = cleanPolygon(rings);
        if (polygon) polygons.push(polygon);
        else droppedPolygons.push({ code, adm0: feature.properties.ADM0_A3 });
      }
    }
    if (!polygons.length) throw new Error(`${code}: no polygon survived`);
    features.push({ type: "Feature", properties: { code }, geometry: { type: "MultiPolygon", coordinates: polygons } });
    countries.push(entry(code, main, aux, [round(main.LABEL_X), round(main.LABEL_Y)]));
  }
  // CN is drawn from public/maps/100000.json and is not part of world.json.
  countries.push(entry("CN", chinaFeature.properties, [], [round(chinaFeature.properties.LABEL_X), round(chinaFeature.properties.LABEL_Y)]));
  const worldFeatures = features;
  if (!partial) for (const e of NO_SHAPE) countries.push({ code: e.code, name: e.name, en: e.en, aliases: [...e.aliases].sort(), continent: e.continent, hasShape: false, point: e.point });
  countries.sort((a, b) => (a.code < b.code ? -1 : 1));
  if (!partial) assertIsoCoverage(countries);

  const geometryText = JSON.stringify({ type: "FeatureCollection", features: worldFeatures }) + "\n";
  const body = {
    source: { ...SOURCE },
    transform: {
      precisionDecimals: PRECISION,
      dropped: dropped.filter((d) => d.reason !== "part of CN" && !d.reason.startsWith("replaced")).map(({ adm0, name, reason }) => ({ adm0, name, reason })).sort((a, b) => (a.adm0 < b.adm0 ? -1 : 1)),
      droppedForCN: dropped.filter((d) => d.reason === "part of CN" || d.reason.startsWith("replaced")).map((d) => d.adm0).sort(),
      merged: merged.sort((a, b) => (a.adm0 < b.adm0 ? -1 : 1)),
      droppedPolygons: droppedPolygons.length,
    },
    china: { file: "maps/100000.json", sha256: chinaSha256 },
    geometry: {
      file: "maps/world.json",
      sha256: sha256(geometryText),
      bytes: Buffer.byteLength(geometryText),
      features: worldFeatures.length,
    },
    continents: CONTINENTS,
    countries,
  };
  const version = "sha256:" + sha256(JSON.stringify(body));
  const head = JSON.stringify({ version, ...body, countries: undefined }, null, 2);
  // Keep the catalog diff-friendly: one entry per line.
  const catalogText = `${head.slice(0, -2)},\n  "countries": [\n${countries.map((c) => "    " + JSON.stringify(c)).join(",\n")}\n  ]\n}\n`;
  const index = {};
  for (const c of countries) {
    const keys = zhKeys([c.name, ...c.aliases]);
    if (keys.length) index[c.code] = keys;
  }
  const indexText = JSON.stringify(index, null, 2) + "\n";
  return { geometryText, catalogText, indexText, stats: { droppedPolygons, merged, dropped } };
}

/** The input must be byte-for-byte the pinned file, whether downloaded or local. */
export function verifyInput(bytes) {
  if (sha256(bytes) !== SOURCE.inputSha256) throw new Error(`Input SHA-256 is ${sha256(bytes)}, expected ${SOURCE.inputSha256}; refusing to generate`);
  if (bytes.length !== SOURCE.inputBytes) throw new Error("Input size differs from the pinned size");
  return bytes.toString("utf8");
}

async function readInput(args) {
  const at = args.indexOf("--input");
  if (at >= 0) return verifyInput(fs.readFileSync(args[at + 1]));
  const response = await fetch(SOURCE.url);
  if (!response.ok) throw new Error(`Download failed: ${response.status}`);
  return verifyInput(Buffer.from(await response.arrayBuffer()));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const args = process.argv.slice(2);
  const out = buildWorld(await readInput(args), sha256(fs.readFileSync(CHINA_FILE)));
  const targets = [
    [GEOMETRY_FILE, out.geometryText],
    [CATALOG_FILE, out.catalogText],
    [INDEX_FILE, out.indexText],
  ];
  if (args.includes("--verify")) {
    for (const [url, text] of targets) {
      if (fs.readFileSync(url, "utf8") !== text) throw new Error(`${url.pathname} differs from a fresh regeneration: npm run prepare:world`);
    }
    console.log(`World data matches a fresh regeneration from input ${SOURCE.inputSha256}`);
  } else {
    for (const [url, text] of targets) fs.writeFileSync(url, text);
    console.log(`Wrote ${targets.map(([u]) => u.pathname.split("/").pop()).join(", ")}; dropped polygons: ${out.stats.droppedPolygons.length}`);
  }
}
