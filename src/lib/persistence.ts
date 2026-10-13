import { colors, type Saved, type Visit } from "./travel.ts";
import { canonicalVisitProvince } from "./region-compatibility.ts";

export type RegionIndex = readonly {
  code: string;
  cities: readonly { code: string; name: string }[];
}[];

export const NOTE_LIMIT = 1000;

const isObject = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);
const validColor = (color: unknown) =>
  typeof color === "string" && colors.some((c) => c.value === color);

function isValidVisit(v: unknown, regions: RegionIndex): v is Visit {
  if (!isObject(v)) return false;
  const province = regions.find((p) => p.code === canonicalVisitProvince(v.code, v.province));
  return (
    !!province?.cities.some((c) => c.code === v.code && c.name === v.name) &&
    validColor(v.color) &&
    typeof v.note === "string" &&
    v.note.length <= NOTE_LIMIT &&
    typeof v.date === "string" &&
    (v.date === "" || /^\d{4}-\d{2}-\d{2}$/.test(v.date))
  );
}

const isValidProvinceMark = (
  code: string,
  color: unknown,
  regions: RegionIndex,
) => regions.some((p) => p.code === code) && validColor(color);

const cleanVisit = (v: Visit): Visit => ({
  code: v.code,
  name: v.name,
  province: canonicalVisitProvince(v.code, v.province),
  color: v.color,
  date: v.date,
  note: v.note,
});

/** Strict validation shared by local storage reads and JSON imports. */
export function validateSaved(data: unknown, regions: RegionIndex): Saved {
  if (!isObject(data)) throw new Error("文件格式不正确");
  if (
    data.version !== 1 ||
    !Array.isArray(data.visits) ||
    !isObject(data.provinces)
  )
    throw new Error("请选择山海足迹导出的 JSON 文件");
  const ids = new Set<string>();
  for (const v of data.visits) {
    if (!isValidVisit(v, regions) || ids.has(v.code))
      throw new Error("足迹数据无效或重复");
    ids.add(v.code);
  }
  for (const [code, color] of Object.entries(data.provinces))
    if (!isValidProvinceMark(code, color, regions))
      throw new Error("省份标记数据无效");
  return {
    version: 1,
    visits: (data.visits as Visit[]).map(cleanVisit),
    provinces: data.provinces as Record<string, string>,
  };
}

export type Recovery = {
  saved: Saved;
  discardedVisits: number;
  discardedProvinces: number;
};

/**
 * Salvages recognisable records from a version 1 payload that failed strict
 * validation. Returns null when the payload cannot be interpreted at all or
 * nothing in it is usable.
 */
export function recoverSaved(raw: string, regions: RegionIndex): Recovery | null {
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return null;
  }
  if (
    !isObject(data) ||
    data.version !== 1 ||
    !Array.isArray(data.visits) ||
    !isObject(data.provinces)
  )
    return null;
  const ids = new Set<string>();
  const visits: Visit[] = [];
  for (const v of data.visits) {
    if (!isValidVisit(v, regions) || ids.has(v.code)) continue;
    ids.add(v.code);
    visits.push(cleanVisit(v));
  }
  const provinces: Record<string, string> = {};
  const marks = Object.entries(data.provinces);
  for (const [code, color] of marks)
    if (isValidProvinceMark(code, color, regions))
      provinces[code] = color as string;
  if (!visits.length && !Object.keys(provinces).length) return null;
  return {
    saved: { version: 1, visits, provinces },
    discardedVisits: data.visits.length - visits.length,
    discardedProvinces: marks.length - Object.keys(provinces).length,
  };
}

export type LocalLoad =
  | { status: "ok" | "empty" | "unavailable"; saved: Saved }
  | {
      status: "unreadable";
      saved: Saved;
      raw: string;
      reason: string;
      recovery: Recovery | null;
    };

const emptySaved = (): Saved => ({ version: 1, visits: [], provinces: {} });

/**
 * Reads the cached footprints. A failed read never turns into a silent reset:
 * the raw string is returned so callers can keep it, export it, and refuse to
 * write over it until the user decides.
 */
export function loadLocalSaved(
  storage: Pick<Storage, "getItem">,
  key: string,
  regions: RegionIndex,
): LocalLoad {
  let raw: string | null;
  try {
    raw = storage.getItem(key);
  } catch {
    return { status: "unavailable", saved: emptySaved() };
  }
  if (raw === null || raw === "") return { status: "empty", saved: emptySaved() };
  try {
    return { status: "ok", saved: validateSaved(JSON.parse(raw), regions) };
  } catch (e) {
    return {
      status: "unreadable",
      saved: emptySaved(),
      raw,
      reason:
        e instanceof SyntaxError
          ? "内容不是有效的 JSON，可能已损坏或被截断"
          : e instanceof Error
            ? e.message
            : "无法识别的数据",
      recovery: recoverSaved(raw, regions),
    };
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
  try {
    storage.setItem(`${key}-unreadable-${now.getTime()}`, raw);
    return true;
  } catch {
    return false;
  }
}
