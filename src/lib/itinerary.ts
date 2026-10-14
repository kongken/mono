import rawCoords from "../city-coords.json" with { type: "json" };
import regions from "../regions.json" with { type: "json" };
import { colors } from "./travel.ts";
import { createRegionCatalog, REGION_VERSION, regionCatalog } from "./region-catalog.ts";
import { TRIP_ID_PATTERN, isRealDate } from "./trips.ts";

export type TransportMode = "train" | "car" | "flight" | "ship" | "other";

export const TRANSPORT_MODES: readonly { value: TransportMode; label: string }[] = [
  { value: "train", label: "高铁 / 铁路" },
  { value: "car", label: "自驾 / 公路" },
  { value: "flight", label: "飞机 / 航线" },
  { value: "ship", label: "轮渡 / 水路" },
  { value: "other", label: "其他 / 综合" },
];

export type ItineraryStopInput = {
  id: string;
  regionCode: string;
  date: string;
  note: string;
  transport: TransportMode;
  tripId?: string;
};

export type ItineraryStop = {
  id: string;
  order: number;
  regionCode: string;
  regionName: string;
  provinceCode: string;
  provinceName: string;
  date: string;
  note: string;
  transport: TransportMode;
  tripId: string;
  longitude: number;
  latitude: number;
  wgsLongitude: number;
  wgsLatitude: number;
};

export type ItineraryLeg = {
  fromOrder: number;
  toOrder: number;
  fromRegionCode: string;
  fromRegionName: string;
  toRegionCode: string;
  toRegionName: string;
  transport: TransportMode;
  straightDistanceKm: number;
  estimatedDistanceKm: number;
  estimatedDurationMinutes: number;
  fromCache: boolean;
};

export type ItineraryEstimationPolicy = {
  provider: string;
  providerLabel: string;
  externalApiEnabled: boolean;
  costPolicy: string;
  cacheStrategy: string;
  disclaimer: string;
};

export type ItineraryRecord = {
  id: string;
  title: string;
  note: string;
  color: string;
  version: number;
  stops: ItineraryStopInput[];
  createdAt?: string;
  updatedAt?: string;
};

export type ItineraryInfo = {
  id: string;
  title: string;
  note: string;
  color: string;
  version: number;
  stops: ItineraryStop[];
  legs: ItineraryLeg[];
  totalStraightDistanceKm: number;
  totalEstimatedDistanceKm: number;
  totalEstimatedDurationMinutes: number;
  cityCount: number;
  provinceCount: number;
  fromCache: boolean;
  createdAt?: string;
  updatedAt?: string;
};

export const DEFAULT_ESTIMATION_POLICY: ItineraryEstimationPolicy = {
  provider: "offline_great_circle",
  providerLabel: "内置城市中心点球面与交通系数估算",
  externalApiEnabled: false,
  costPolicy: "free_builtin",
  cacheStrategy: "redis_leg_24h_and_local_deterministic",
  disclaimer:
    "基于随项目分发的城市中心点球面距离与交通方式系数离线估算，不发起第三方地图请求，不产生外部 API 费用；仅供参考，不作为实时导航依据",
};

export const ITINERARY_TITLE_LIMIT = 80;
export const ITINERARY_NOTE_LIMIT = 1000;
export const STOP_NOTE_LIMIT = 200;
export const MIN_ITINERARY_STOPS = 2;
export const MAX_ITINERARY_STOPS = 100;

export const LOCAL_ITINERARIES_KEY = "shanhai-itineraries-v1";

const EARTH_RADIUS_KM = 6371.0;
const SINGLE_CITY_PROVINCES = new Set(["110000", "120000", "310000", "500000", "810000", "820000"]);
const coordsMap = rawCoords as unknown as Record<string, [number, number, number, number]>;
const cityMetaByCode = new Map(
  regions.flatMap((p) =>
    p.cities.map((c) => [
      c.code,
      { code: c.code, name: c.name, provinceCode: p.code, provinceName: p.name },
    ]),
  ),
);

export function validTransport(mode: unknown): boolean {
  return (
    mode === "" ||
    mode === "train" ||
    mode === "car" ||
    mode === "flight" ||
    mode === "ship" ||
    mode === "other"
  );
}

export function normalizeTransport(mode?: string): TransportMode {
  switch (mode) {
    case "car":
    case "flight":
    case "ship":
    case "other":
      return mode;
    default:
      return "train";
  }
}

export function transportLabel(mode?: string): string {
  const m = normalizeTransport(mode);
  return TRANSPORT_MODES.find((item) => item.value === m)?.label ?? "高铁 / 铁路";
}

export function formatDistanceKm(km: number): string {
  if (!Number.isFinite(km) || km <= 0) return "0 公里";
  return `${Number.isInteger(km) ? km : km.toFixed(1)} 公里`;
}

export function formatDurationMinutes(minutes: number): string {
  if (!Number.isFinite(minutes) || minutes <= 0) return "0 分钟";
  const rounded = Math.round(minutes);
  const hours = Math.floor(rounded / 60);
  const mins = rounded % 60;
  if (hours === 0) return `${mins} 分钟`;
  if (mins === 0) return `${hours} 小时`;
  return `${hours} 小时 ${mins} 分`;
}

export function cityCoords(regionCode: string): [number, number, number, number] | null {
  return coordsMap[regionCode] ?? null;
}

function paramsForMode(mode: TransportMode): { detourPct: number; speedKmh: number; bufferMin: number } {
  switch (mode) {
    case "flight":
      return { detourPct: 105, speedKmh: 700, bufferMin: 50 };
    case "ship":
      return { detourPct: 115, speedKmh: 35, bufferMin: 25 };
    case "car":
      return { detourPct: 130, speedKmh: 80, bufferMin: 15 };
    case "other":
      return { detourPct: 120, speedKmh: 100, bufferMin: 15 };
    default:
      return { detourPct: 120, speedKmh: 220, bufferMin: 20 };
  }
}

export type LegEstimate = {
  transport: TransportMode;
  straightTenthsKm: number;
  estTenthsKm: number;
  straightDistanceKm: number;
  estimatedDistanceKm: number;
  estimatedDurationMinutes: number;
};

export function computeLeg(fromCode: string, toCode: string, transport?: string): LegEstimate {
  const mode = normalizeTransport(transport);
  if (fromCode === toCode) {
    return {
      transport: mode,
      straightTenthsKm: 0,
      estTenthsKm: 0,
      straightDistanceKm: 0,
      estimatedDistanceKm: 0,
      estimatedDurationMinutes: 0,
    };
  }
  const c1 = coordsMap[fromCode];
  const c2 = coordsMap[toCode];
  if (!c1 || !c2) {
    return {
      transport: mode,
      straightTenthsKm: 0,
      estTenthsKm: 0,
      straightDistanceKm: 0,
      estimatedDistanceKm: 0,
      estimatedDurationMinutes: 0,
    };
  }
  const [, , lng1, lat1] = c1;
  const [, , lng2, lat2] = c2;
  const dLat = ((lat2 - lat1) * Math.PI) / 180;
  const dLng = ((lng2 - lng1) * Math.PI) / 180;
  const rLat1 = (lat1 * Math.PI) / 180;
  const rLat2 = (lat2 * Math.PI) / 180;
  const a =
    Math.sin(dLat / 2) * Math.sin(dLat / 2) +
    Math.cos(rLat1) * Math.cos(rLat2) * Math.sin(dLng / 2) * Math.sin(dLng / 2);
  const c = 2 * Math.atan2(Math.sqrt(a), Math.sqrt(Math.max(0, 1 - a)));
  const rawKm = EARTH_RADIUS_KM * c;
  const straightTenthsKm = Math.max(1, Math.round(rawKm * 10));
  const mp = paramsForMode(mode);
  const estTenthsKm = Math.max(
    straightTenthsKm,
    Math.floor((straightTenthsKm * mp.detourPct + 50) / 100),
  );
  const travelMin = Math.max(
    1,
    Math.floor((estTenthsKm * 6 + Math.floor(mp.speedKmh / 2)) / mp.speedKmh),
  );
  const estimatedDurationMinutes = travelMin + mp.bufferMin;
  return {
    transport: mode,
    straightTenthsKm,
    estTenthsKm,
    straightDistanceKm: straightTenthsKm / 10,
    estimatedDistanceKm: estTenthsKm / 10,
    estimatedDurationMinutes,
  };
}

function statsCityCode(regionCode: string, provinceCode: string): string {
  if (SINGLE_CITY_PROVINCES.has(provinceCode)) return provinceCode;
  return regionCode;
}

export function buildItineraryInfo(
  record: ItineraryRecord,
  catalog: ReturnType<typeof createRegionCatalog> = regionCatalog,
): ItineraryInfo {
  const stops: ItineraryStop[] = [];
  const citySet = new Set<string>();
  const provSet = new Set<string>();

  for (let i = 0; i < record.stops.length; i++) {
    const raw = record.stops[i];
    const resolvedCode = catalog.code(raw.regionCode) ?? raw.regionCode;
    const cityRef = cityMetaByCode.get(resolvedCode);
    const regionName = cityRef?.name ?? resolvedCode;
    const provinceCode = cityRef?.provinceCode ?? "";
    const provinceName = cityRef?.provinceName ?? provinceCode;
    const coord = coordsMap[resolvedCode] ?? [0, 0, 0, 0];
    const transport = normalizeTransport(raw.transport);
    stops.push({
      id: raw.id || `stop-${i + 1}`,
      order: i + 1,
      regionCode: resolvedCode,
      regionName,
      provinceCode,
      provinceName,
      date: raw.date || "",
      note: raw.note || "",
      transport,
      tripId: raw.tripId || "",
      longitude: coord[0],
      latitude: coord[1],
      wgsLongitude: coord[2],
      wgsLatitude: coord[3],
    });
    if (resolvedCode) {
      citySet.add(statsCityCode(resolvedCode, provinceCode));
    }
    if (provinceCode) {
      provSet.add(provinceCode);
    }
  }

  const legs: ItineraryLeg[] = [];
  let totalStraightTenths = 0;
  let totalEstTenths = 0;
  let totalDurationMin = 0;

  for (let i = 1; i < stops.length; i++) {
    const prev = stops[i - 1];
    const curr = stops[i];
    const est = computeLeg(prev.regionCode, curr.regionCode, curr.transport);
    totalStraightTenths += est.straightTenthsKm;
    totalEstTenths += est.estTenthsKm;
    totalDurationMin += est.estimatedDurationMinutes;
    legs.push({
      fromOrder: prev.order,
      toOrder: curr.order,
      fromRegionCode: prev.regionCode,
      fromRegionName: prev.regionName,
      toRegionCode: curr.regionCode,
      toRegionName: curr.regionName,
      transport: est.transport,
      straightDistanceKm: est.straightDistanceKm,
      estimatedDistanceKm: est.estimatedDistanceKm,
      estimatedDurationMinutes: est.estimatedDurationMinutes,
      fromCache: false,
    });
  }

  return {
    id: record.id,
    title: record.title,
    note: record.note,
    color: record.color,
    version: record.version || 1,
    stops,
    legs,
    totalStraightDistanceKm: totalStraightTenths / 10,
    totalEstimatedDistanceKm: totalEstTenths / 10,
    totalEstimatedDurationMinutes: totalDurationMin,
    cityCount: citySet.size,
    provinceCount: provSet.size,
    fromCache: false,
    ...(record.createdAt ? { createdAt: record.createdAt } : {}),
    ...(record.updatedAt ? { updatedAt: record.updatedAt } : {}),
  };
}

export function toItineraryRecord(info: ItineraryInfo): ItineraryRecord {
  return {
    id: info.id,
    title: info.title,
    note: info.note,
    color: info.color,
    version: info.version || 1,
    stops: info.stops.map((s) => ({
      id: s.id,
      regionCode: s.regionCode,
      date: s.date,
      note: s.note,
      transport: normalizeTransport(s.transport),
      ...(s.tripId ? { tripId: s.tripId } : {}),
    })),
    ...(info.createdAt ? { createdAt: info.createdAt } : {}),
    ...(info.updatedAt ? { updatedAt: info.updatedAt } : {}),
  };
}

export function moveStop<T>(stops: readonly T[], index: number, direction: -1 | 1): T[] {
  const target = index + direction;
  if (index < 0 || index >= stops.length || target < 0 || target >= stops.length) {
    return [...stops];
  }
  const next = [...stops];
  const [item] = next.splice(index, 1);
  next.splice(target, 0, item);
  return next;
}

export function newItineraryId(): string {
  const rand =
    typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
      ? crypto.randomUUID().replace(/-/g, "").slice(0, 12)
      : Math.random().toString(36).slice(2, 14);
  return `itin_${rand}`;
}

export function newStopId(): string {
  const rand =
    typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
      ? crypto.randomUUID().replace(/-/g, "").slice(0, 10)
      : Math.random().toString(36).slice(2, 12);
  return `stop_${rand}`;
}

const isObject = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const validColor = (c: unknown): c is string =>
  typeof c === "string" && colors.some((item) => item.value === c);

export function validateItineraryInput(
  input: {
    id?: string;
    title: string;
    note?: string;
    color?: string;
    version?: number;
    stops: readonly {
      id?: string;
      regionCode: string;
      date?: string;
      note?: string;
      transport?: string;
      tripId?: string;
    }[];
  },
  catalog: ReturnType<typeof createRegionCatalog> = regionCatalog,
): ItineraryRecord {
  const id = (input.id ?? "").trim() || newItineraryId();
  if (!TRIP_ID_PATTERN.test(id)) {
    throw new Error("行程 ID 格式不正确");
  }
  const title = input.title.trim();
  if (!title) {
    throw new Error("请填写行程名称");
  }
  if (title.length > ITINERARY_TITLE_LIMIT) {
    throw new Error(`行程名称不能超过 ${ITINERARY_TITLE_LIMIT} 个字符`);
  }
  const note = input.note ?? "";
  if (note.length > ITINERARY_NOTE_LIMIT) {
    throw new Error(`行程备注不能超过 ${ITINERARY_NOTE_LIMIT} 个字符`);
  }
  const color = (input.color ?? "").trim() || colors[0].value;
  if (!validColor(color)) {
    throw new Error("不支持的行程配色");
  }
  if (!Array.isArray(input.stops) || input.stops.length < MIN_ITINERARY_STOPS) {
    throw new Error("一条行程至少需要包含 2 个途经城市站点");
  }
  if (input.stops.length > MAX_ITINERARY_STOPS) {
    throw new Error(`一条行程最多包含 ${MAX_ITINERARY_STOPS} 个站点`);
  }
  const seenStopIds = new Set<string>();
  const stops: ItineraryStopInput[] = [];
  for (let i = 0; i < input.stops.length; i++) {
    const raw = input.stops[i];
    const stopId = (raw.id ?? "").trim() || `stop-${i + 1}`;
    if (!TRIP_ID_PATTERN.test(stopId) || seenStopIds.has(stopId)) {
      throw new Error(`第 ${i + 1} 站 ID 无效或重复`);
    }
    seenStopIds.add(stopId);
    const resolved = catalog.code((raw.regionCode ?? "").trim());
    if (!resolved || !cityMetaByCode.has(resolved)) {
      throw new Error(`第 ${i + 1} 站城市区域无效`);
    }
    const date = (raw.date ?? "").trim();
    if (date !== "" && !isRealDate(date)) {
      throw new Error(`第 ${i + 1} 站日期不是有效的日历日期`);
    }
    const stopNote = raw.note ?? "";
    if (stopNote.length > STOP_NOTE_LIMIT) {
      throw new Error(`第 ${i + 1} 站备注不能超过 ${STOP_NOTE_LIMIT} 个字符`);
    }
    const transportRaw = (raw.transport ?? "").trim();
    if (!validTransport(transportRaw)) {
      throw new Error(`第 ${i + 1} 站交通方式无效`);
    }
    const tripId = (raw.tripId ?? "").trim();
    if (tripId !== "" && !TRIP_ID_PATTERN.test(tripId)) {
      throw new Error(`第 ${i + 1} 站关联记录 ID 无效`);
    }
    stops.push({
      id: stopId,
      regionCode: resolved,
      date,
      note: stopNote,
      transport: normalizeTransport(transportRaw),
      ...(tripId ? { tripId } : {}),
    });
  }
  return {
    id,
    title,
    note,
    color,
    version: input.version && input.version > 0 ? input.version : 1,
    stops,
  };
}

export function validateItineraryRecord(
  raw: unknown,
  catalog: ReturnType<typeof createRegionCatalog> = regionCatalog,
): ItineraryRecord | null {
  if (!isObject(raw)) return null;
  try {
    const rec = validateItineraryInput(
      {
        id: typeof raw.id === "string" ? raw.id : "",
        title: typeof raw.title === "string" ? raw.title : "",
        note: typeof raw.note === "string" ? raw.note : "",
        color: typeof raw.color === "string" ? raw.color : colors[0].value,
        version: typeof raw.version === "number" ? raw.version : 1,
        stops: Array.isArray(raw.stops)
          ? raw.stops.map((s) =>
              isObject(s)
                ? {
                    id: typeof s.id === "string" ? s.id : undefined,
                    regionCode: typeof s.regionCode === "string" ? s.regionCode : "",
                    date: typeof s.date === "string" ? s.date : "",
                    note: typeof s.note === "string" ? s.note : "",
                    transport: typeof s.transport === "string" ? s.transport : "train",
                    tripId: typeof s.tripId === "string" ? s.tripId : undefined,
                  }
                : { regionCode: "" },
            )
          : [],
      },
      catalog,
    );
    return {
      ...rec,
      ...(typeof raw.createdAt === "string" ? { createdAt: raw.createdAt } : {}),
      ...(typeof raw.updatedAt === "string" ? { updatedAt: raw.updatedAt } : {}),
    };
  } catch {
    return null;
  }
}

export type StoredItinerariesState = {
  activeId: string;
  itineraries: ItineraryRecord[];
};

export function loadStoredItineraries(
  storage: Pick<Storage, "getItem">,
  key: string = LOCAL_ITINERARIES_KEY,
  catalog: ReturnType<typeof createRegionCatalog> = regionCatalog,
): StoredItinerariesState {
  let raw: string | null = null;
  try {
    raw = storage.getItem(key);
  } catch {
    return { activeId: "", itineraries: [] };
  }
  if (!raw) return { activeId: "", itineraries: [] };
  try {
    const parsed = JSON.parse(raw);
    if (!isObject(parsed) || !Array.isArray(parsed.itineraries)) {
      return { activeId: "", itineraries: [] };
    }
    const seenIds = new Set<string>();
    const itineraries: ItineraryRecord[] = [];
    for (const item of parsed.itineraries) {
      const valid = validateItineraryRecord(item, catalog);
      if (valid && !seenIds.has(valid.id)) {
        seenIds.add(valid.id);
        itineraries.push(valid);
      }
    }
    const activeId =
      typeof parsed.activeId === "string" && seenIds.has(parsed.activeId)
        ? parsed.activeId
        : "";
    return { activeId, itineraries };
  } catch {
    return { activeId: "", itineraries: [] };
  }
}

export function saveStoredItineraries(
  storage: Pick<Storage, "setItem" | "removeItem">,
  state: StoredItinerariesState,
  key: string = LOCAL_ITINERARIES_KEY,
): boolean {
  try {
    if (state.itineraries.length === 0 && !state.activeId) {
      storage.removeItem(key);
      return true;
    }
    const validIds = new Set(state.itineraries.map((i) => i.id));
    const activeId = validIds.has(state.activeId) ? state.activeId : "";
    storage.setItem(
      key,
      JSON.stringify({
        version: 1,
        regionVersion: REGION_VERSION,
        activeId,
        itineraries: state.itineraries,
      }),
    );
    return true;
  } catch {
    return false;
  }
}

export function parseBackupItineraries(
  raw: unknown,
  catalog: ReturnType<typeof createRegionCatalog> = regionCatalog,
): ItineraryRecord[] {
  if (!Array.isArray(raw)) return [];
  const seenIds = new Set<string>();
  const out: ItineraryRecord[] = [];
  for (const item of raw) {
    const valid = validateItineraryRecord(item, catalog);
    if (!valid || seenIds.has(valid.id)) {
      throw new Error("备份文件中的行程记录格式无效或包含重复 ID");
    }
    seenIds.add(valid.id);
    out.push(valid);
  }
  return out;
}

