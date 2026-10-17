import fs from "node:fs";
import { createHash } from "node:crypto";

const source = new URL("../../../packages/geo-data/data/regions.json", import.meta.url);
const regions = JSON.parse(fs.readFileSync(source, "utf8"));
const history = JSON.parse(fs.readFileSync(new URL("../../../packages/geo-data/data/region-history.json", import.meta.url), "utf8"));
if (!Array.isArray(history.supportedVersions) || !history.supportedVersions.every(v => typeof v === "string" && v.length > 0) || !Array.isArray(history.mappings)) throw new Error("Invalid region history");
// Stable field order is shared with the Go catalog binder.
const mappings = history.mappings.map(m => ({ fromCode: m.fromCode, fromName: m.fromName, fromProvince: m.fromProvince, toCode: m.toCode }));
const cities = new Map(regions.flatMap(p => p.cities.map(c => [c.code, { ...c, province: p.code }])));
const keys = new Set();
const aliases = new Map();
for (const m of mappings) {
  if (!Object.values(m).every(v => typeof v === "string" && v.length > 0) || !cities.has(m.toCode)) throw new Error("Invalid region mapping");
  const key = JSON.stringify([m.fromCode, m.fromName, m.fromProvince]);
  if (keys.has(key) || (cities.has(m.fromCode) && m.fromCode !== m.toCode)) throw new Error("Duplicate or reused region ID in mapping");
  keys.add(key);
  if (aliases.has(m.fromCode) && aliases.get(m.fromCode) !== m.toCode) throw new Error("Ambiguous region alias");
  aliases.set(m.fromCode, m.toCode);
}
// Do not update only one of the two index copies while changing catalog version.
if (fs.readFileSync(source, "utf8") !== fs.readFileSync(new URL("../../../internal/tripmap/regions/regions.json", import.meta.url), "utf8")) throw new Error("Frontend/backend region indexes differ");
const version = "sha256:" + createHash("sha256").update(JSON.stringify({ regions, mappings })).digest("hex");
const catalog = { version, supportedVersions: [...new Set(["legacy", ...history.supportedVersions, version])], mappings };
const previousUrl = new URL("../../../packages/geo-data/data/region-catalog.json", import.meta.url);
if (fs.existsSync(previousUrl)) {
  const previous = JSON.parse(fs.readFileSync(previousUrl, "utf8"));
  if (previous.supportedVersions.some(v => !catalog.supportedVersions.includes(v))) throw new Error("Archive previous supported versions in packages/geo-data/data/region-history.json before updating");
  if (previous.mappings.some(m => !keys.has(JSON.stringify([m.fromCode, m.fromName, m.fromProvince])))) throw new Error("Keep historical mapping tuples; retarget them directly to a current ID, never delete them");
}
const output = JSON.stringify(catalog, null, 2) + "\n";
const targets = ["../../../packages/geo-data/data/region-catalog.json", "../../../internal/tripmap/regions/catalog.json"];
for (const target of targets) {
  const url = new URL(target, import.meta.url);
  if (process.argv.includes("--check")) {
    if (fs.readFileSync(url, "utf8") !== output) throw new Error("Region catalog is stale: npm run prepare:regions");
  } else fs.writeFileSync(url, output);
}
