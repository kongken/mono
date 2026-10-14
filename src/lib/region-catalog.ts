import bundledRegions from "../regions.json" with { type: "json" };
import metadata from "../region-catalog.json" with { type: "json" };

export type RegionMapping = { fromCode: string; fromName: string; fromProvince: string; toCode: string };
export type CatalogMetadata = { version: string; supportedVersions: string[]; mappings: RegionMapping[] };
export type RegionList = readonly { code: string; cities: readonly { code: string; name: string }[] }[];
export const REGION_VERSION = metadata.version;
export const REGION_HEADER = "X-Tripmap-Regions";
export class RegionCompatibilityError extends Error {}

/** A small seam for current IDs, exact historical tuples and collision safety.
 * A migration is one-to-one at the record level; it never combines user content.
 */
export function createRegionCatalog(regions: RegionList, catalog: CatalogMetadata = metadata) {
  const cities = new Map(regions.flatMap(p => p.cities.map(c => [c.code, { code: c.code, name: c.name, province: p.code }])));
  const historical = new Map<string, string>();
  const aliases = new Map<string, string>();
  const tuple = (code: unknown, name: unknown, province: unknown) => JSON.stringify([code, name, province]);
  for (const m of catalog.mappings) {
    if (!cities.has(m.toCode)) continue; // A test/subset index cannot accept foreign destinations.
    const key = tuple(m.fromCode, m.fromName, m.fromProvince);
    if (historical.has(key) || (cities.has(m.fromCode) && m.fromCode !== m.toCode) || (aliases.has(m.fromCode) && aliases.get(m.fromCode) !== m.toCode)) throw new RegionCompatibilityError("区域迁移表包含重复或歧义映射");
    historical.set(key, m.toCode);
    aliases.set(m.fromCode, m.toCode);
  }
  return {
    assertVersion(version: unknown) {
      if (version === undefined || version === "" || version === "legacy" || (typeof version === "string" && (version === catalog.version || catalog.supportedVersions.includes(version)))) return;
      throw new RegionCompatibilityError("数据使用未知或更新的区域索引版本。原数据已保留，请升级页面或联系维护者，不要覆盖数据。");
    },
    city(code: unknown, name: unknown, province: unknown) {
      if (typeof code !== "string" || typeof name !== "string" || typeof province !== "string") return null;
      const current = cities.get(code);
      if (current?.name === name && current.province === province) return current;
      return cities.get(historical.get(tuple(code, name, province)) ?? "") ?? null;
    },
    code(code: string) { return cities.has(code) ? code : aliases.get(code) ?? null; },
  };
}
export const regionCatalog = createRegionCatalog(bundledRegions);
/** Persist/export an independent region version without changing the F5 format. */
export function stampRegions<T extends object>(saved: T): T & { regionVersion: string } {
  return { ...saved, regionVersion: REGION_VERSION };
}
