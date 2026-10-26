import { colors } from "./travel.ts";
import { isValidTripDate, newTripId, TRIP_ID_PATTERN, TRIP_NOTE_LIMIT } from "./trips.ts";

// World footprints (docs/trip-map/world-spec.md section 4): the data model, its strict
// validation and canonical form, and the pure edits the world view will use. Nothing
// here touches storage or the China footprints.

export type WorldTrip = { id: string; date: string; note: string };
export type WorldPlace = {
  id: string;
  name: string;
  /** ISO 3166-1 alpha-2 (or XK) from the world catalog; never CN. */
  country: string;
  /** WGS-84 degrees with at most 6 decimals. */
  lat: number;
  lng: number;
  trips: WorldTrip[];
};
/** An independent country/region mark: only what the user clicked, never a derived light. */
export type WorldMark = { code: string; color: string };
export type WorldDocument = {
  kind: "world";
  version: 1;
  countries: WorldMark[];
  places: WorldPlace[];
};

export const WORLD_VERSION = 1;
export const WORLD_MAX_PLACES = 5000;
export const WORLD_MAX_TRIPS = 10_000;
/** Sum of the UTF-8 bytes of every string field (id, name, country, code, color, date, note). */
export const WORLD_MAX_STRING_BYTES = 2_500_000;
/** The largest world JSON file an import reads, in bytes. */
export const WORLD_MAX_IMPORT_BYTES = 4_000_000;
export const WORLD_NAME_LIMIT = 60;
export const WORLD_NOTE_LIMIT = TRIP_NOTE_LIMIT;
/** The country code the world data never stores: CN is derived from the China footprints. */
export const DERIVED_COUNTRY = "CN";

export type WorldErrorKind =
  | "format"
  | "kind"
  | "version"
  | "field"
  | "country"
  | "color"
  | "duplicate"
  | "id"
  | "name"
  | "coordinate"
  | "date"
  | "note"
  | "limit";

/** A rejected document or edit. `path` names the record, e.g. `places[2](p123).lat`. */
export class WorldValidationError extends Error {
  readonly kind: WorldErrorKind;
  readonly path: string;
  constructor(kind: WorldErrorKind, path: string, message: string) {
    super(path ? `${path}：${message}` : message);
    this.kind = kind;
    this.path = path;
  }
}

export const emptyWorld = (): WorldDocument => ({ kind: "world", version: 1, countries: [], places: [] });

const isObject = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);
const compare = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

// --- Names -----------------------------------------------------------------

/** The characters the name rule strips from both ends: Unicode White_Space plus U+FEFF, never the language's default trim. */
const NAME_EDGE_CHAR = /^[\p{White_Space}\uFEFF]$/u;
/**
 * Strips them with two linear scans, one from each end. (A single `^…+|…+$` regular
 * expression backtracks quadratically on a long run of inner whitespace.) Every such
 * character is a single UTF-16 unit, so scanning units is exact.
 */
export function trimWorldName(name: string): string {
  let start = 0;
  let end = name.length;
  while (start < end && NAME_EDGE_CHAR.test(name[start])) start++;
  while (end > start && NAME_EDGE_CHAR.test(name[end - 1])) end--;
  return name.slice(start, end);
}
const CONTROL = /\p{Cc}/u;
const hasLoneSurrogate = (text: string) => /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/.test(text);

// --- Coordinates -----------------------------------------------------------

/** The canonical form of a coordinate: whole micro-degrees, rounded away from zero on ties. */
export const toMicroDegrees = (degrees: number): number => {
  const e6 = Math.sign(degrees) * Math.round(Math.abs(degrees) * 1e6);
  return e6 + 0; // never -0
};
export const fromMicroDegrees = (e6: number): number => e6 / 1e6 + 0;
/** What the interface writes: any input normalised to 6 decimals (half away from zero). */
export const normalizeCoordinate = (degrees: number): number => fromMicroDegrees(toMicroDegrees(degrees));
/** True for a value with at most 6 decimals (the tolerance rule of the spec). */
export const hasAtMostSixDecimals = (x: number): boolean => Math.abs(x * 1e6 - Math.round(x * 1e6)) < 1e-6;

function coordinate(value: unknown, limit: number, path: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) throw new WorldValidationError("coordinate", path, "坐标必须是有限数字");
  if (Math.abs(value) > limit) throw new WorldValidationError("coordinate", path, `坐标超出范围（±${limit}）`);
  if (!hasAtMostSixDecimals(value)) throw new WorldValidationError("coordinate", path, "坐标最多 6 位小数，请先规范后再导入");
  return normalizeCoordinate(value);
}

// --- Size ------------------------------------------------------------------

const encoder = new TextEncoder();
const utf8 = (text: string) => encoder.encode(text).length;

/** The measure the size limit is defined on: UTF-8 bytes of every string field, no serialisation involved. */
export function worldStringBytes(world: WorldDocument): number {
  let total = 0;
  for (const mark of world.countries) total += utf8(mark.code) + utf8(mark.color);
  for (const place of world.places) {
    total += utf8(place.id) + utf8(place.name) + utf8(place.country);
    for (const trip of place.trips) total += utf8(trip.id) + utf8(trip.date) + utf8(trip.note);
  }
  return total;
}
export const worldTripCount = (world: WorldDocument): number => world.places.reduce((n, p) => n + p.trips.length, 0);

// --- Validation ------------------------------------------------------------

export type WorldCodes = ReadonlySet<string>;
export const worldCodeSet = (catalog: { countries: readonly { code: string }[] }): WorldCodes =>
  new Set(catalog.countries.map((c) => c.code));

function exact(value: Record<string, unknown>, allowed: readonly string[], path: string) {
  for (const key of Object.keys(value))
    if (!allowed.includes(key)) throw new WorldValidationError("field", path, `出现未定义字段“${key}”`);
}
const validColor = (color: unknown): color is string => typeof color === "string" && colors.some((c) => c.value === color);

function countryCode(value: unknown, codes: WorldCodes, path: string): string {
  if (typeof value !== "string" || !codes.has(value)) throw new WorldValidationError("country", path, "国家/地区码不在目录中");
  if (value === DERIVED_COUNTRY) throw new WorldValidationError("country", path, "中国由中国足迹派生，不能在世界数据中标记或作为海外城市的所属地");
  return value;
}

/**
 * Strict validation shared by local reads, imports and edits. Returns the canonical
 * form (sorted, names trimmed, coordinates in whole micro-degrees); throws a
 * WorldValidationError that names the record for anything it will not accept. It
 * never drops or repairs a field silently.
 */
export function validateWorld(data: unknown, codes: WorldCodes): WorldDocument {
  if (!isObject(data)) throw new WorldValidationError("format", "", "文件格式不正确");
  if (data.kind !== "world") throw new WorldValidationError("kind", "kind", "不是山海足迹的世界备份");
  if (data.version !== WORLD_VERSION)
    throw new WorldValidationError("version", "version", "世界数据的版本未知或比当前页面新，请升级页面；原数据已保留");
  exact(data, ["kind", "version", "countries", "places"], "");
  if (!Array.isArray(data.countries)) throw new WorldValidationError("format", "countries", "应为数组");
  if (!Array.isArray(data.places)) throw new WorldValidationError("format", "places", "应为数组");
  if (data.places.length > WORLD_MAX_PLACES)
    throw new WorldValidationError("limit", "places", `海外城市最多 ${WORLD_MAX_PLACES} 个，当前 ${data.places.length} 个`);

  const markCodes = new Set<string>();
  const countries: WorldMark[] = data.countries.map((raw, i) => {
    const path = `countries[${i}]`;
    if (!isObject(raw)) throw new WorldValidationError("format", path, "应为对象");
    exact(raw, ["code", "color"], path);
    const code = countryCode(raw.code, codes, `${path}.code`);
    if (markCodes.has(code)) throw new WorldValidationError("duplicate", path, `国家/地区 ${code} 的标记重复`);
    markCodes.add(code);
    if (!validColor(raw.color)) throw new WorldValidationError("color", `${path}(${code}).color`, "不是足迹色板中的颜色");
    return { code, color: raw.color };
  });

  const placeIds = new Set<string>();
  const tripIds = new Set<string>();
  let trips = 0;
  const places: WorldPlace[] = data.places.map((raw, i) => {
    const at = `places[${i}]`;
    if (!isObject(raw)) throw new WorldValidationError("format", at, "应为对象");
    exact(raw, ["id", "name", "country", "lat", "lng", "trips"], at);
    if (typeof raw.id !== "string" || !TRIP_ID_PATTERN.test(raw.id))
      throw new WorldValidationError("id", `${at}.id`, "城市 ID 只能含字母、数字、下划线和连字符，1–64 个字符");
    const path = `${at}(${raw.id})`;
    if (placeIds.has(raw.id)) throw new WorldValidationError("duplicate", path, `城市 ID ${raw.id} 重复`);
    placeIds.add(raw.id);
    if (typeof raw.name !== "string") throw new WorldValidationError("name", `${path}.name`, "名称应为文字");
    const name = trimWorldName(raw.name);
    if (!name) throw new WorldValidationError("name", `${path}.name`, "名称不能为空");
    if (name.length > WORLD_NAME_LIMIT) throw new WorldValidationError("name", `${path}.name`, `名称最多 ${WORLD_NAME_LIMIT} 个字符`);
    if (CONTROL.test(name)) throw new WorldValidationError("name", `${path}.name`, "名称不能含控制字符");
    if (hasLoneSurrogate(name)) throw new WorldValidationError("name", `${path}.name`, "名称含无效的字符编码（孤立代理项）");
    const country = countryCode(raw.country, codes, `${path}.country`);
    const lat = coordinate(raw.lat, 90, `${path}.lat`);
    const lng = coordinate(raw.lng, 180, `${path}.lng`);
    if (!Array.isArray(raw.trips)) throw new WorldValidationError("format", `${path}.trips`, "应为数组");
    trips += raw.trips.length;
    if (trips > WORLD_MAX_TRIPS) throw new WorldValidationError("limit", `${path}.trips`, `旅行最多 ${WORLD_MAX_TRIPS} 次`);
    const list: WorldTrip[] = raw.trips.map((t, j) => {
      const tp = `${path}.trips[${j}]`;
      if (!isObject(t)) throw new WorldValidationError("format", tp, "应为对象");
      exact(t, ["id", "date", "note"], tp);
      if (typeof t.id !== "string" || !TRIP_ID_PATTERN.test(t.id))
        throw new WorldValidationError("id", `${tp}.id`, "旅行 ID 只能含字母、数字、下划线和连字符，1–64 个字符");
      if (tripIds.has(t.id)) throw new WorldValidationError("duplicate", `${tp}(${t.id})`, `旅行 ID ${t.id} 重复`);
      tripIds.add(t.id);
      if (!isValidTripDate(t.date)) throw new WorldValidationError("date", `${tp}(${t.id}).date`, "日期不是真实存在的日历日期（格式 YYYY-MM-DD，或留空）");
      if (typeof t.note !== "string" || t.note.length > WORLD_NOTE_LIMIT)
        throw new WorldValidationError("note", `${tp}(${t.id}).note`, `备注最多 ${WORLD_NOTE_LIMIT} 个字符`);
      return { id: t.id, date: t.date, note: t.note };
    });
    return { id: raw.id, name, country, lat, lng, trips: list.sort((a, b) => compare(a.id, b.id)) };
  });

  const world: WorldDocument = {
    kind: "world",
    version: 1,
    countries: countries.sort((a, b) => compare(a.code, b.code)),
    places: places.sort((a, b) => compare(a.id, b.id)),
  };
  const bytes = worldStringBytes(world);
  if (bytes > WORLD_MAX_STRING_BYTES)
    throw new WorldValidationError("limit", "", `文字内容共 ${bytes} 字节，超过上限 ${WORLD_MAX_STRING_BYTES} 字节`);
  return world;
}

/** Structural equality of two valid documents (not byte equality of their serialisations). */
export const sameWorld = (a: WorldDocument, b: WorldDocument): boolean => JSON.stringify(a) === JSON.stringify(b);

// --- Edits -----------------------------------------------------------------
// Each edit builds the next document and validates it, so the limits and every rule
// apply to edits exactly as they do to files; a rejected edit leaves the input alone.

/** The least-used colour among the existing country marks; ties go by palette order. */
function nextMarkColor(world: WorldDocument): string {
  const counts = colors.map((c) => world.countries.filter((m) => m.color === c.value).length);
  return colors[counts.indexOf(Math.min(...counts))].value;
}

/** Marks or unmarks a country/region. Unmarking never deletes cities. */
export function toggleCountry(world: WorldDocument, code: string, codes: WorldCodes): WorldDocument {
  if (world.countries.some((m) => m.code === code))
    return validateWorld({ ...world, countries: world.countries.filter((m) => m.code !== code) }, codes);
  return validateWorld({ ...world, countries: [...world.countries, { code, color: nextMarkColor(world) }] }, codes);
}

export type NewPlace = { name: string; country: string; lat: number; lng: number };
/** A new overseas city with no trips yet; coordinates are normalised to 6 decimals first. */
export function addPlace(world: WorldDocument, input: NewPlace, codes: WorldCodes, id: string = newPlaceId()): WorldDocument {
  const place = {
    id,
    name: input.name,
    country: input.country,
    lat: Number.isFinite(input.lat) ? normalizeCoordinate(input.lat) : input.lat,
    lng: Number.isFinite(input.lng) ? normalizeCoordinate(input.lng) : input.lng,
    trips: [],
  };
  return validateWorld({ ...world, places: [...world.places, place] }, codes);
}

/** Deletes a city and its trips. Country marks are untouched. */
export function removePlace(world: WorldDocument, id: string, codes: WorldCodes): WorldDocument {
  if (!world.places.some((p) => p.id === id)) throw new WorldValidationError("id", `places(${id})`, "找不到这个城市");
  return validateWorld({ ...world, places: world.places.filter((p) => p.id !== id) }, codes);
}

function mapPlace(world: WorldDocument, id: string, change: (place: WorldPlace) => WorldPlace): WorldDocument["places"] {
  if (!world.places.some((p) => p.id === id)) throw new WorldValidationError("id", `places(${id})`, "找不到这个城市");
  return world.places.map((p) => (p.id === id ? change(p) : p));
}

export function addTrip(
  world: WorldDocument,
  placeId: string,
  trip: { date: string; note: string },
  codes: WorldCodes,
  id: string = newTripId(),
): WorldDocument {
  return validateWorld({ ...world, places: mapPlace(world, placeId, (p) => ({ ...p, trips: [...p.trips, { id, ...trip }] })) }, codes);
}
export function updateTrip(
  world: WorldDocument,
  placeId: string,
  tripId: string,
  change: { date?: string; note?: string },
  codes: WorldCodes,
): WorldDocument {
  const places = mapPlace(world, placeId, (p) => {
    if (!p.trips.some((t) => t.id === tripId)) throw new WorldValidationError("id", `places(${placeId}).trips(${tripId})`, "找不到这次旅行");
    return { ...p, trips: p.trips.map((t) => (t.id === tripId ? { ...t, ...change } : t)) };
  });
  return validateWorld({ ...world, places }, codes);
}
export function removeTrip(world: WorldDocument, placeId: string, tripId: string, codes: WorldCodes): WorldDocument {
  const places = mapPlace(world, placeId, (p) => {
    if (!p.trips.some((t) => t.id === tripId)) throw new WorldValidationError("id", `places(${placeId}).trips(${tripId})`, "找不到这次旅行");
    return { ...p, trips: p.trips.filter((t) => t.id !== tripId) };
  });
  return validateWorld({ ...world, places }, codes);
}

/** New city IDs are `p` + 9 base36 time digits + 8 random characters, like trips with their `t`. */
export const newPlaceId = (now = Date.now()): string => `p${newTripId(now).slice(1)}`;
