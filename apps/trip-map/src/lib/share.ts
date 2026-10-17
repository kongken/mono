import regions from "@kongken/geo-data/regions.json" with { type: "json" };

/** A share token is 32 random bytes in unpadded base64url. */
export const SHARE_TOKEN = /^[A-Za-z0-9_-]{43}$/;

export type ShareRoute =
  | { kind: "none" }
  | { kind: "share"; token: string }
  /** Something under /s/ that is not a share link. It must never fall back to a personal map. */
  | { kind: "invalid" };

/**
 * Decides what a page URL is. Everything at or below `<base>s` belongs to the
 * public share page; any other path is the normal application.
 */
export function parseSharePath(pathname: string, base = "/"): ShareRoute {
  const prefix = base.endsWith("/") ? base : `${base}/`;
  if (!pathname.startsWith(prefix)) return { kind: "none" };
  const rest = pathname.slice(prefix.length);
  if (rest !== "s" && !rest.startsWith("s/")) return { kind: "none" };
  const parts = rest.split("/").slice(1);
  if (parts.length && parts[parts.length - 1] === "") parts.pop();
  return parts.length === 1 && SHARE_TOKEN.test(parts[0])
    ? { kind: "share", token: parts[0] }
    : { kind: "invalid" };
}

export type SharePhoto = { id: string; w: number; h: number };
export type SharedTrip = { date?: string; note?: string; photos?: SharePhoto[] };
export type SharedCity = {
  code: string;
  name: string;
  province: string;
  color: string;
  trips: SharedTrip[];
  photos?: SharePhoto[];
};
export type ShareContent = {
  v: 1;
  published_on: string;
  fields: { cities: boolean; dates: boolean; notes: boolean; photos: boolean };
  stats: {
    explored_provinces: number;
    total_provinces: number;
    cities: number;
    total_cities: number;
    trips: number;
    by_province: { code: string; cities: number; total: number }[];
  };
  provinces: string[];
  cities: SharedCity[];
};

const record = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const count = (v: unknown) => typeof v === "number" && Number.isInteger(v) && v >= 0 && v < 1e7;
const text = (v: unknown, max: number) => typeof v === "string" && v.length <= max;
const list = (v: unknown): unknown[] => (v == null ? [] : Array.isArray(v) ? v : bad());
const bad = (): never => {
  throw new Error("invalid share content");
};

const provinceCodes = new Set(regions.map((p) => p.code));
const cityCodes = new Set(regions.flatMap((p) => p.cities.map((c) => c.code)));

function photos(value: unknown): SharePhoto[] {
  return list(value).map((p) => {
    if (!record(p) || typeof p.id !== "string" || !/^p[0-9A-Za-z_-]{8}$/.test(p.id) || !count(p.w) || !count(p.h)) return bad();
    return { id: p.id, w: p.w as number, h: p.h as number };
  });
}

/**
 * Reads the public JSON strictly: unknown regions, odd colours or wrong types are
 * refused instead of drawn. Only these fields are ever rendered.
 */
export function parseShareContent(data: unknown): ShareContent {
  if (!record(data) || data.v !== 1 || !text(data.published_on, 10)) return bad();
  const f = record(data.fields) ? data.fields : bad();
  const s = record(data.stats) ? data.stats : bad();
  if (![s.explored_provinces, s.total_provinces, s.cities, s.total_cities, s.trips].every(count)) bad();
  const provinces = list(data.provinces).map((c) => (typeof c === "string" && provinceCodes.has(c) ? c : bad()));
  const byProvince = list(s.by_province).map((p) => {
    if (!record(p) || typeof p.code !== "string" || !provinceCodes.has(p.code) || !count(p.cities) || !count(p.total)) return bad();
    return { code: p.code, cities: p.cities as number, total: p.total as number };
  });
  const cities = list(data.cities).map((c): SharedCity => {
    if (
      !record(c) ||
      typeof c.code !== "string" ||
      !cityCodes.has(c.code) ||
      typeof c.province !== "string" ||
      !provinceCodes.has(c.province) ||
      !text(c.name, 60) ||
      typeof c.color !== "string" ||
      !/^#[0-9a-fA-F]{6}$/.test(c.color)
    )
      return bad();
    return {
      code: c.code,
      name: c.name as string,
      province: c.province,
      color: c.color,
      photos: photos(c.photos),
      trips: list(c.trips).map((t): SharedTrip => {
        if (!record(t) || (t.date != null && !text(t.date, 10)) || (t.note != null && !text(t.note, 4000))) return bad();
        return { date: (t.date as string) || undefined, note: (t.note as string) || undefined, photos: photos(t.photos) };
      }),
    };
  });
  return {
    v: 1,
    published_on: data.published_on as string,
    fields: { cities: f.cities === true, dates: f.dates === true, notes: f.notes === true, photos: f.photos === true },
    stats: {
      explored_provinces: s.explored_provinces as number,
      total_provinces: s.total_provinces as number,
      cities: s.cities as number,
      total_cities: s.total_cities as number,
      trips: s.trips as number,
      by_province: byProvince,
    },
    provinces,
    cities,
  };
}
