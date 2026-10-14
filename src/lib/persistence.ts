import { colors, type Saved, type Trip, type Visit } from "./travel.ts";
import { createRegionCatalog, REGION_VERSION, RegionCompatibilityError, stampRegions } from "./region-catalog.ts";
import {
  TRIP_ID_PATTERN,
  TRIP_NOTE_LIMIT,
  canonicalTrips,
  isRealDate,
  legacyTripId,
} from "./trips.ts";

export type RegionIndex = readonly {
  code: string;
  cities: readonly { code: string; name: string }[];
}[];

export const NOTE_LIMIT = TRIP_NOTE_LIMIT;
/** Backups hold every trip of every city, so the limit is far above the size of any real file. */
export const MAX_BACKUP_BYTES = 32_000_000;

const isObject = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);
const validColor = (color: unknown) =>
  typeof color === "string" && colors.some((c) => c.value === color);
const DATE_SHAPE = /^\d{4}-\d{2}-\d{2}$/;

/**
 * A date shaped like YYYY-MM-DD that is not on the calendar, such as 2026-02-30.
 * Older versions accepted these. They are never changed or dropped silently:
 * callers must be told and let the user decide.
 */
export class InvalidDatesError extends Error {
  readonly count: number;
  constructor(count: number) {
    super(
      `有 ${count} 个旅行日期不是真实存在的日历日期（例如 2 月 30 日）。为避免改动你的记录，没有自动处理。`,
    );
    this.count = count;
  }
}

type DateMode = { fix: boolean; found: number };

function parseTrip(t: unknown, mode: DateMode): { trip: Trip; badDate: boolean } | null {
  if (
    !isObject(t) ||
    typeof t.id !== "string" ||
    !TRIP_ID_PATTERN.test(t.id) ||
    typeof t.note !== "string" ||
    t.note.length > NOTE_LIMIT ||
    typeof t.date !== "string"
  )
    return null;
  let date = t.date;
  let badDate = false;
  if (date !== "" && !isRealDate(date)) {
    if (!DATE_SHAPE.test(date)) return null;
    badDate = true;
    mode.found++;
    if (mode.fix) date = "";
  }
  return { trip: { id: t.id, date, note: t.note }, badDate };
}

function parseCity(v: Record<string, unknown>, catalog: ReturnType<typeof createRegionCatalog>) {
  const city = catalog.city(v.code, v.name, v.province);
  if (!city || !validColor(v.color)) return null;
  return { ...city, color: v.color as string };
}

function assertNoAliasCollision(visits: unknown[], catalog: ReturnType<typeof createRegionCatalog>) {
  const sources = new Map<string, string>();
  for (const raw of visits) {
    if (!isObject(raw)) continue;
    const city = catalog.city(raw.code, raw.name, raw.province);
    if (!city) continue;
    const previous = sources.get(city.code);
    if (previous !== undefined && previous !== raw.code) throw new RegionCompatibilityError("旧区域映射后发生 ID 碰撞，整份原数据已保留；不能自动合并旅行记录");
    sources.set(city.code, raw.code as string);
  }
}

const isValidProvinceMark = (code: string, color: unknown, regions: RegionIndex) =>
  regions.some((p) => p.code === code) && validColor(color);

/** The trips of one raw visit: version 1 carries one date and note, version 2 a list. */
function rawTrips(v: Record<string, unknown>, version: 1 | 2, code: string): unknown[] | null {
  if (version === 1) {
    if (typeof v.date !== "string" || typeof v.note !== "string") return null;
    return [{ id: legacyTripId(code), date: v.date, note: v.note }];
  }
  return Array.isArray(v.trips) ? v.trips : null;
}

/**
 * Strict validation shared by local storage reads and JSON imports. Version 1
 * files become one trip per city (kept even when empty); version 2 files carry
 * their trips. Dates that are not real calendar dates throw InvalidDatesError
 * unless the caller opted into clearing them.
 */
export function validateSaved(
  data: unknown,
  regions: RegionIndex,
  options: { correctDates?: boolean; catalog?: ReturnType<typeof createRegionCatalog> } = {},
): Saved {
  if (!isObject(data)) throw new Error("文件格式不正确");
  if (
    (data.version !== 1 && data.version !== 2) ||
    !Array.isArray(data.visits) ||
    !isObject(data.provinces)
  )
    throw new Error("请选择山海足迹导出的 JSON 文件");
  const catalog = options.catalog ?? createRegionCatalog(regions);
  catalog.assertVersion(data.regionVersion);
  assertNoAliasCollision(data.visits, catalog);
  const version = data.version;
  const mode: DateMode = { fix: !!options.correctDates, found: 0 };
  const codes = new Set<string>();
  const tripIds = new Set<string>();
  const visits: Visit[] = [];
  for (const raw of data.visits) {
    const city = isObject(raw) ? parseCity(raw, catalog) : null;
    if (!city || codes.has(city.code)) throw new Error("足迹数据无效或重复");
    codes.add(city.code);
    const list = rawTrips(raw as Record<string, unknown>, version, (raw as Record<string, unknown>).code as string);
    if (!list) throw new Error("足迹数据无效或重复");
    const trips: Trip[] = [];
    for (const item of list) {
      const parsed = parseTrip(item, mode);
      if (!parsed || tripIds.has(parsed.trip.id)) throw new Error("旅行记录无效或重复");
      tripIds.add(parsed.trip.id);
      trips.push(parsed.trip);
    }
    visits.push({ ...city, trips: canonicalTrips(trips) });
  }
  for (const [code, color] of Object.entries(data.provinces))
    if (!isValidProvinceMark(code, color, regions)) throw new Error("省份标记数据无效");
  if (mode.found && !mode.fix) throw new InvalidDatesError(mode.found);
  return { version: 2, visits, provinces: data.provinces as Record<string, string> };
}

export type Recovery = {
  saved: Saved;
  discardedVisits: number;
  discardedTrips: number;
  discardedProvinces: number;
  /** Trip dates cleared because they were not real calendar dates. */
  correctedDates: number;
};

/**
 * Salvages recognisable records from a version 1 or 2 payload that failed
 * strict validation. Records with impossible dates are dropped, unless
 * correctDates clears just those dates and keeps the rest of the record.
 * Returns null when nothing in the payload is usable.
 */
export function recoverSaved(
  raw: string,
  regions: RegionIndex,
  options: { correctDates?: boolean; catalog?: ReturnType<typeof createRegionCatalog> } = {},
): Recovery | null {
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return null;
  }
  if (
    !isObject(data) ||
    (data.version !== 1 && data.version !== 2) ||
    !Array.isArray(data.visits) ||
    !isObject(data.provinces)
  )
    return null;
  const catalog = options.catalog ?? createRegionCatalog(regions);
  try { catalog.assertVersion(data.regionVersion); assertNoAliasCollision(data.visits, catalog); } catch { return null; }
  const version = data.version;
  const fix = !!options.correctDates;
  const codes = new Set<string>();
  const tripIds = new Set<string>();
  const visits: Visit[] = [];
  let discardedTrips = 0;
  let correctedDates = 0;
  for (const rawVisit of data.visits) {
    const city = isObject(rawVisit) ? parseCity(rawVisit, catalog) : null;
    // Whatever trips a dropped city carried are dropped with it, and counted.
    const carried = isObject(rawVisit) ? (version === 1 ? 1 : Array.isArray(rawVisit.trips) ? rawVisit.trips.length : 0) : 0;
    if (!city || codes.has(city.code)) {
      discardedTrips += carried;
      continue;
    }
    const list = rawTrips(rawVisit as Record<string, unknown>, version, (rawVisit as Record<string, unknown>).code as string);
    if (!list) {
      discardedTrips += carried;
      continue;
    }
    const trips: Trip[] = [];
    let lost = 0;
    for (const item of list) {
      const mode: DateMode = { fix, found: 0 };
      const parsed = parseTrip(item, mode);
      if (!parsed || (parsed.badDate && !fix) || tripIds.has(parsed.trip.id)) {
        lost++;
        continue;
      }
      if (parsed.badDate) correctedDates++;
      tripIds.add(parsed.trip.id);
      trips.push(parsed.trip);
    }
    // A version 1 record is one trip: when it cannot be kept, the city goes with it.
    if (version === 1 && lost) {
      discardedTrips += lost;
      continue;
    }
    codes.add(city.code);
    discardedTrips += lost;
    visits.push({ ...city, trips: canonicalTrips(trips) });
  }
  const provinces: Record<string, string> = {};
  const marks = Object.entries(data.provinces);
  for (const [code, color] of marks)
    if (isValidProvinceMark(code, color, regions)) provinces[code] = color as string;
  if (!visits.length && !Object.keys(provinces).length) return null;
  return {
    saved: { version: 2, visits, provinces },
    discardedVisits: data.visits.length - visits.length,
    discardedTrips,
    discardedProvinces: marks.length - Object.keys(provinces).length,
    correctedDates,
  };
}

/** The canonical key of this version: schema 2 data, and only this version writes it. */
export const LOCAL_KEY = "shanhai-travel-v2";
/**
 * The key earlier versions wrote. Pages of those versions may still be open, so
 * this version never writes it: its first save goes to LOCAL_KEY and the old
 * bytes stay exactly as they were.
 */
export const LEGACY_LOCAL_KEY = "shanhai-travel-v1";
/** What this version has already accounted for of the legacy key, as a digest. */
export const LEGACY_SEEN_KEY = "shanhai-travel-v2-legacy-seen";

/** Short fingerprint of a stored text (cyrb53), so a changed legacy key is noticed without copying it. */
export function digestText(raw: string | null): string {
  if (raw === null) return "none";
  let h1 = 0xdeadbeef;
  let h2 = 0x41c6ce57;
  for (let i = 0; i < raw.length; i++) {
    const ch = raw.charCodeAt(i);
    h1 = Math.imul(h1 ^ ch, 2654435761);
    h2 = Math.imul(h2 ^ ch, 1597334677);
  }
  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909);
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909);
  return `${raw.length}:${(4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(36)}`;
}

/** Data an older page wrote to the legacy key after this version took over. */
export type LegacyChange = {
  raw: string;
  /** The footprints in it; null when they cannot be read (only the raw export is offered). */
  saved: Saved | null;
};

type LoadCommon = {
  /** What LOCAL_KEY held when read; the writer compares against it before every write. */
  canonicalRaw: string | null;
  /** Digest of the legacy key as read; recorded once this version first writes. */
  legacyDigest: string;
};
export type LocalLoad =
  | ({
      status: "ok";
      saved: Saved;
      /** The text the footprints were read from. */
      raw: string;
      source: "v2" | "legacy";
      /** Only when LOCAL_KEY is in use and the legacy key changed since it was last accounted for. */
      legacyChange?: LegacyChange;
    } & LoadCommon)
  | ({ status: "empty"; saved: Saved } & LoadCommon)
  | { status: "unavailable"; saved: Saved }
  | ({
      status: "unreadable";
      saved: Saved;
      raw: string;
      /** The key the unreadable text lives in; it is never overwritten. */
      sourceKey: string;
      source: "v2" | "legacy";
      reason: string;
      /** Salvage that drops what cannot be read, including records with impossible dates. */
      recovery: Recovery | null;
      /** Salvage that clears impossible dates but keeps the rest; only when such dates exist. */
      recoveryCorrected: Recovery | null;
    } & LoadCommon);

const emptySaved = (): Saved => ({ version: 2, visits: [], provinces: {} });

type Salvage = {
  reason: string;
  recovery: Recovery | null;
  recoveryCorrected: Recovery | null;
};
type Parsed = { ok: true; saved: Saved } | ({ ok: false } & Salvage);

function parseStored(raw: string, regions: RegionIndex): Parsed {
  try {
    return { ok: true, saved: validateSaved(JSON.parse(raw), regions) };
  } catch (e) {
    const recovery = e instanceof RegionCompatibilityError ? null : recoverSaved(raw, regions);
    const corrected = e instanceof InvalidDatesError ? recoverSaved(raw, regions, { correctDates: true }) : null;
    return {
      ok: false,
      reason:
        e instanceof SyntaxError
          ? "内容不是有效的 JSON，可能已损坏或被截断"
          : e instanceof Error
            ? e.message
            : "无法识别的数据",
      recovery,
      recoveryCorrected: corrected && corrected.correctedDates > 0 ? corrected : null,
    };
  }
}

/**
 * Reads the footprints of this device. LOCAL_KEY wins; when it does not exist
 * yet the legacy key is read (version 1 or 2 text) and left untouched, and the
 * first save creates LOCAL_KEY. A failed read never turns into a silent reset:
 * the raw string is returned so callers can keep it, export it, and refuse to
 * write over it until the user decides.
 */
export function loadLocalSaved(
  storage: Pick<Storage, "getItem">,
  regions: RegionIndex,
): LocalLoad {
  let canonical: string | null;
  let legacy: string | null;
  let seen: string | null;
  try {
    canonical = storage.getItem(LOCAL_KEY);
    legacy = storage.getItem(LEGACY_LOCAL_KEY);
    seen = storage.getItem(LEGACY_SEEN_KEY);
  } catch {
    return { status: "unavailable", saved: emptySaved() };
  }
  const canonicalRaw = canonical === "" ? null : canonical;
  const legacyRaw = legacy === "" ? null : legacy;
  const common: LoadCommon = { canonicalRaw: canonical, legacyDigest: digestText(legacyRaw) };
  const fromKey = (raw: string, source: "v2" | "legacy", sourceKey: string): LocalLoad => {
    const parsed = parseStored(raw, regions);
    if (parsed.ok) return { status: "ok", saved: parsed.saved, raw, source, ...common };
    const { ok: _ok, ...salvage } = parsed;
    return { status: "unreadable", saved: emptySaved(), raw, source, sourceKey, ...salvage, ...common };
  };
  if (canonicalRaw !== null) {
    const load = fromKey(canonicalRaw, "v2", LOCAL_KEY);
    if (load.status === "ok" && legacyRaw !== null && digestText(legacyRaw) !== seen) {
      const parsed = parseStored(legacyRaw, regions);
      return { ...load, legacyChange: { raw: legacyRaw, saved: parsed.ok ? parsed.saved : null } };
    }
    return load;
  }
  if (legacyRaw !== null) return fromKey(legacyRaw, "legacy", LEGACY_LOCAL_KEY);
  return { status: "empty", saved: emptySaved(), ...common };
}

/** Records that the legacy key, in its current state, has been dealt with. */
export function acknowledgeLegacy(storage: Pick<Storage, "setItem">, digest: string): boolean {
  return preserveCopy(storage, LEGACY_SEEN_KEY, digest);
}

/** Re-reads the legacy key after another page changed it. */
export function readLegacyChange(
  storage: Pick<Storage, "getItem">,
  regions: RegionIndex,
): (LegacyChange & { digest: string }) | null {
  try {
    const raw = storage.getItem(LEGACY_LOCAL_KEY);
    if (raw === null || raw === "") return null;
    const digest = digestText(raw);
    if (digest === storage.getItem(LEGACY_SEEN_KEY)) return null;
    const parsed = parseStored(raw, regions);
    return { raw, digest, saved: parsed.ok ? parsed.saved : null };
  } catch {
    return null;
  }
}

/**
 * Keeps a copy of unreadable data under a timestamped key before the main key
 * is overwritten. Returns false if the browser refused the write.
 */
export function preserveUnreadable(
  storage: Pick<Storage, "setItem">,
  key: string,
  raw: string,
  now = new Date(),
): boolean {
  return preserveCopy(storage, `${key}-unreadable-${now.getTime()}`, raw);
}

function preserveCopy(storage: Pick<Storage, "setItem">, key: string, raw: string): boolean {
  try {
    storage.setItem(key, raw);
    return true;
  } catch {
    return false;
  }
}

export const regionOriginalPrefix = (key: string) => `shanhai-regions-original:${key}:`;

/** Version upgrades keep an exact, stable-key copy before replacing any cache.
 * Unknown/corrupt versions are never eligible for automatic replacement.
 */
export function preserveRegionOriginal(storage: Pick<Storage, "getItem" | "setItem">, key: string, raw: string): boolean {
  try {
    let version: unknown;
    try { const data = JSON.parse(raw); version = data?.regionVersion ?? data?.saved?.regionVersion; } catch { /* Explicit recovery may replace corrupt bytes, but only after preserving them. */ }
    if (version === REGION_VERSION) return true;
    const copyKey = regionOriginalPrefix(key) + digestText(raw);
    if (storage.getItem(copyKey) === raw) return true;
    storage.setItem(copyKey, raw);
    return storage.getItem(copyKey) === raw;
  } catch { return false; }
}

export type LocalWrite =
  | { status: "ok" }
  | { status: "conflict"; other: string | null }
  | { status: "error" };

/**
 * The only way the app writes the local footprints, and it only ever writes
 * LOCAL_KEY. Before each write it checks that the stored text is still what this
 * page last read or wrote, so a second tab's changes are never overwritten
 * unnoticed. On the first write of an upgrade (no LOCAL_KEY yet) the digest of
 * the legacy key is recorded first, so later changes by an older page can be
 * told from the data this version took over.
 */
export function createLocalWriter(
  storage: Pick<Storage, "getItem" | "setItem">,
  initialRaw: string | null,
  legacyDigest?: string,
  /** Only for a user-confirmed recovery whose exact source was copied/exported. */
  originalAlreadySafe = false,
) {
  let originalSafe = originalAlreadySafe;
  let last = initialRaw === "" ? null : initialRaw;
  let pendingDigest = last === null ? legacyDigest : undefined;
  return {
    write(saved: Saved): LocalWrite {
      let current: string | null;
      try {
        current = storage.getItem(LOCAL_KEY);
      } catch {
        return { status: "error" };
      }
      if (current === "") current = null;
      if (current !== last) return { status: "conflict", other: current };
      if (pendingDigest !== undefined) {
        if (!preserveCopy(storage, LEGACY_SEEN_KEY, pendingDigest)) return { status: "error" };
        pendingDigest = undefined;
      }
      // Preserve the exact pre-F10 bytes before the first versioned write.
      if (!originalSafe && last !== null && !preserveRegionOriginal(storage, LOCAL_KEY, last)) return { status: "error" };
      const text = JSON.stringify(stampRegions(saved));
      try {
        storage.setItem(LOCAL_KEY, text);
      } catch {
        return { status: "error" };
      }
      last = text;
      originalSafe = false;
      return { status: "ok" };
    },
    /** Accept whatever storage holds now as the new baseline, e.g. after the user chose how to resolve a conflict. */
    adopt(raw: string | null, alreadySafe = false) {
      last = raw === "" ? null : raw;
      pendingDigest = undefined;
      originalSafe = alreadySafe;
    },
  };
}

/** Keeps another tab's text before overwriting it. */
export const preserveConflict = (
  storage: Pick<Storage, "setItem">,
  raw: string,
  now = new Date(),
) => preserveCopy(storage, `${LOCAL_KEY}-conflict-${now.getTime()}`, raw);
