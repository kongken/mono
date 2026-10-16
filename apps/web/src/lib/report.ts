import regions from "@trip-map/geo-data/regions.json" with { type: "json" };
import { isDistrictDestination, totalCities } from "./statistics.ts";
import type { Saved } from "./travel.ts";

export const DEFAULT_TIMEZONE = "Asia/Shanghai";

export type ReportRecordRef = {
  kind: "trip" | "city" | "province";
  tripId: string;
  regionCode: string;
  regionName: string;
  provinceCode: string;
  provinceName: string;
  date: string;
  noteLength: number;
  notePreview: string;
  tripCount: number;
};

export type ReportProvinceProgress = {
  provinceCode: string;
  provinceName: string;
  visitedCities: number;
  totalCities: number;
  markedOnly: boolean;
  visitedRegions: ReportRecordRef[];
};

export type ReportOverview = {
  exploredProvinces: number;
  totalProvinces: number;
  visitedCities: number;
  totalCities: number;
  visitedRegions: number;
  totalRegions: number;
  totalTrips: number;
  datedTrips: number;
  undatedTrips: number;
  provinceProgress: ReportProvinceProgress[];
};

export type AnnualReportData = {
  year: number;
  tripCount: number;
  cityCount: number;
  regionCount: number;
  newCityCount: number;
  newRegionCount: number;
  provinceCount: number;
  newProvinceCount: number;
  spanDays: number;
  busiestMonth: number;
  busiestMonthTrips: number;
  earliestTrip?: ReportRecordRef;
  latestTrip?: ReportRecordRef;
  longestNoteTrip?: ReportRecordRef;
  mostVisitedCity?: ReportRecordRef;
  newRegions: ReportRecordRef[];
  provinces: ReportRecordRef[];
  trips: ReportRecordRef[];
};

export type AchievementData = {
  id: string;
  title: string;
  description: string;
  unlocked: boolean;
  current: number;
  target: number;
  unlockedAt: string;
  evidence: ReportRecordRef[];
};

export type TravelReportData = {
  revision: number;
  timezone: string;
  generatedOn: string;
  currentYear: number;
  selectedYear: number;
  availableYears: number[];
  overview: ReportOverview;
  annualReports: AnnualReportData[];
  achievements: AchievementData[];
  fromCache: boolean;
};

export type BuildReportOptions = {
  timezone?: string;
  year?: number;
  now?: Date;
  revision?: number;
};

const provinceByCode = new Map(regions.map(p => [p.code, p]));
const cityIndex = new Map<string, { name: string; provinceCode: string; provinceName: string }>();
let totalRecordableRegions = 0;
for (const p of regions) {
  totalRecordableRegions += p.cities.length;
  for (const c of p.cities) {
    cityIndex.set(c.code, { name: c.name, provinceCode: p.code, provinceName: p.name });
  }
}

export function detectBrowserTimezone(): string {
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    return tz && tz.trim() ? tz.trim() : DEFAULT_TIMEZONE;
  } catch {
    return DEFAULT_TIMEZONE;
  }
}

export function resolveLocalDateInTimezone(now: Date, tz?: string): { timezone: string; generatedOn: string; currentYear: number } {
  const candidate = tz?.trim() || DEFAULT_TIMEZONE;
  for (const zone of [candidate, DEFAULT_TIMEZONE, "UTC"]) {
    try {
      const parts = new Intl.DateTimeFormat("en-CA", {
        timeZone: zone,
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
      }).formatToParts(now);
      const y = parts.find(p => p.type === "year")?.value ?? "";
      const m = parts.find(p => p.type === "month")?.value ?? "";
      const d = parts.find(p => p.type === "day")?.value ?? "";
      if (y && m && d) {
        return {
          timezone: zone === "UTC" && candidate !== "UTC" ? DEFAULT_TIMEZONE : zone,
          generatedOn: `${y}-${m}-${d}`,
          currentYear: Number(y),
        };
      }
    } catch {
      // Fall back to default timezone
    }
  }
  const iso = now.toISOString().slice(0, 10);
  return { timezone: DEFAULT_TIMEZONE, generatedOn: iso, currentYear: Number(iso.slice(0, 4)) };
}

type NormTrip = {
  id: string;
  date: string;
  year: number;
  month: number;
  validDate: boolean;
  note: string;
  noteLength: number;
  notePreview: string;
  regionCode: string;
  regionName: string;
  provinceCode: string;
  provinceName: string;
};

type NormCity = {
  code: string;
  name: string;
  provinceCode: string;
  provinceName: string;
  destKey: string;
  earliestDate: string;
  earliestTrip: string;
  trips: NormTrip[];
};

function parseValidDate(raw: string): { date: string; year: number; month: number; valid: boolean } {
  const d = raw.trim();
  if (!/^\d{4}-\d{2}-\d{2}$/.test(d)) {
    return { date: "", year: 0, month: 0, valid: false };
  }
  const y = Number(d.slice(0, 4));
  const m = Number(d.slice(5, 7));
  const day = Number(d.slice(8, 10));
  if (y < 1900 || y > 9999 || m < 1 || m > 12 || day < 1 || day > 31) {
    return { date: "", year: 0, month: 0, valid: false };
  }
  const dt = new Date(Date.UTC(y, m - 1, day));
  if (dt.getUTCFullYear() !== y || dt.getUTCMonth() + 1 !== m || dt.getUTCDate() !== day) {
    return { date: "", year: 0, month: 0, valid: false };
  }
  return { date: d, year: y, month: m, valid: true };
}

function makeNotePreview(s: string): string {
  const clean = s.trim().replace(/\s+/g, " ");
  if (clean.length <= 40) return clean;
  return `${clean.slice(0, 40)}…`;
}

function compareStrings(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function compareDatesEmptyLast(a: string, b: string): number {
  if ((a === "") !== (b === "")) {
    return a === "" ? 1 : -1;
  }
  return compareStrings(a, b);
}

function daysBetweenInclusive(startDate: string, endDate: string): number {
  const t1 = Date.parse(`${startDate}T00:00:00Z`);
  const t2 = Date.parse(`${endDate}T00:00:00Z`);
  if (Number.isNaN(t1) || Number.isNaN(t2) || t2 < t1) return 1;
  return Math.floor((t2 - t1) / 86_400_000) + 1;
}

function tripToRef(tr: NormTrip): ReportRecordRef {
  return {
    kind: "trip",
    tripId: tr.id,
    regionCode: tr.regionCode,
    regionName: tr.regionName,
    provinceCode: tr.provinceCode,
    provinceName: tr.provinceName,
    date: tr.date,
    noteLength: tr.noteLength,
    notePreview: tr.notePreview,
    tripCount: 0,
  };
}

export function buildReport(saved: Saved, opts: BuildReportOptions = {}): TravelReportData {
  const now = opts.now ?? new Date();
  const { timezone, generatedOn, currentYear } = resolveLocalDateInTimezone(now, opts.timezone);

  const exploredSet = new Set<string>();
  const markedSet = new Set<string>();
  for (const pCode of Object.keys(saved.provinces ?? {})) {
    if (provinceByCode.has(pCode)) {
      exploredSet.add(pCode);
      markedSet.add(pCode);
    }
  }

  const cityByCode = new Map<string, NormCity>();
  for (const visit of saved.visits ?? []) {
    const info = cityIndex.get(visit.code);
    if (!info) continue;
    exploredSet.add(info.provinceCode);
    let nc = cityByCode.get(visit.code);
    if (!nc) {
      nc = {
        code: visit.code,
        name: info.name,
        provinceCode: info.provinceCode,
        provinceName: info.provinceName,
        destKey: isDistrictDestination(info.provinceCode) ? info.provinceCode : visit.code,
        earliestDate: "",
        earliestTrip: "",
        trips: [],
      };
      cityByCode.set(visit.code, nc);
    }
    const rawTrips = visit.trips ?? [];
    for (let i = 0; i < rawTrips.length; i++) {
      const tr = rawTrips[i];
      const tid = tr.id?.trim() || `${visit.code}-${i + 1}`;
      const parsed = parseValidDate(tr.date ?? "");
      const note = (tr.note ?? "").trim();
      nc.trips.push({
        id: tid,
        date: parsed.date,
        year: parsed.year,
        month: parsed.month,
        validDate: parsed.valid,
        note,
        noteLength: note.length,
        notePreview: makeNotePreview(note),
        regionCode: visit.code,
        regionName: info.name,
        provinceCode: info.provinceCode,
        provinceName: info.provinceName,
      });
    }
  }

  const cities = Array.from(cityByCode.values());
  for (const nc of cities) {
    nc.trips.sort((a, b) => compareDatesEmptyLast(a.date, b.date) || compareStrings(a.id, b.id));
    for (const tr of nc.trips) {
      if (tr.validDate) {
        nc.earliestDate = tr.date;
        nc.earliestTrip = tr.id;
        break;
      }
    }
  }
  cities.sort((a, b) => compareStrings(a.code, b.code));

  const destSet = new Set<string>();
  const citiesByProvince = new Map<string, NormCity[]>();
  const firstRegionYear = new Map<string, number>();
  const firstDestDate = new Map<string, string>();
  const firstDestYear = new Map<string, number>();
  const firstProvinceDate = new Map<string, string>();
  const firstProvinceYear = new Map<string, number>();
  const yearSet = new Set<number>([currentYear]);
  const allDatedTrips: NormTrip[] = [];
  let totalTrips = 0;
  let datedTrips = 0;

  for (const nc of cities) {
    destSet.add(nc.destKey);
    const list = citiesByProvince.get(nc.provinceCode) ?? [];
    list.push(nc);
    citiesByProvince.set(nc.provinceCode, list);
    totalTrips += nc.trips.length;
    for (const tr of nc.trips) {
      if (!tr.validDate) continue;
      datedTrips++;
      yearSet.add(tr.year);
      allDatedTrips.push(tr);
      const prevRegY = firstRegionYear.get(nc.code);
      if (prevRegY === undefined || tr.year < prevRegY) {
        firstRegionYear.set(nc.code, tr.year);
      }
      const prevDestD = firstDestDate.get(nc.destKey);
      if (prevDestD === undefined || tr.date < prevDestD) {
        firstDestDate.set(nc.destKey, tr.date);
        firstDestYear.set(nc.destKey, tr.year);
      }
      const prevProvD = firstProvinceDate.get(nc.provinceCode);
      if (prevProvD === undefined || tr.date < prevProvD) {
        firstProvinceDate.set(nc.provinceCode, tr.date);
        firstProvinceYear.set(nc.provinceCode, tr.year);
      }
    }
  }

  const provinceProgress: ReportProvinceProgress[] = [];
  for (const pCode of exploredSet) {
    const p = provinceByCode.get(pCode);
    if (!p) continue;
    const pCities = citiesByProvince.get(pCode) ?? [];
    provinceProgress.push({
      provinceCode: pCode,
      provinceName: p.name,
      visitedCities: pCities.length,
      totalCities: p.cities.length,
      markedOnly: pCities.length === 0 && markedSet.has(pCode),
      visitedRegions: pCities.map(nc => ({
        kind: "city",
        tripId: nc.earliestTrip,
        regionCode: nc.code,
        regionName: nc.name,
        provinceCode: nc.provinceCode,
        provinceName: nc.provinceName,
        date: nc.earliestDate,
        noteLength: 0,
        notePreview: "",
        tripCount: nc.trips.length,
      })),
    });
  }
  provinceProgress.sort((a, b) => {
    const lhs = a.visitedCities * b.totalCities;
    const rhs = b.visitedCities * a.totalCities;
    if (lhs !== rhs) return rhs - lhs;
    if (a.visitedCities !== b.visitedCities) return b.visitedCities - a.visitedCities;
    return compareStrings(a.provinceCode, b.provinceCode);
  });

  const overview: ReportOverview = {
    exploredProvinces: exploredSet.size,
    totalProvinces: regions.length,
    visitedCities: destSet.size,
    totalCities,
    visitedRegions: cities.length,
    totalRegions: totalRecordableRegions,
    totalTrips,
    datedTrips,
    undatedTrips: totalTrips - datedTrips,
    provinceProgress,
  };

  const availableYears = Array.from(yearSet).sort((a, b) => b - a);
  const annualReports = availableYears.map(y =>
    buildAnnualReport(y, allDatedTrips, cityByCode, firstRegionYear, firstDestYear, firstProvinceYear),
  );

  let selectedYear = currentYear;
  if (opts.year && availableYears.includes(opts.year)) {
    selectedYear = opts.year;
  } else {
    const withTrips = annualReports.find(ar => ar.tripCount > 0);
    if (withTrips) selectedYear = withTrips.year;
  }

  const achievements = buildAchievements(cities, exploredSet);

  return {
    revision: opts.revision ?? 0,
    timezone,
    generatedOn,
    currentYear,
    selectedYear,
    availableYears,
    overview,
    annualReports,
    achievements,
    fromCache: false,
  };
}

function buildAnnualReport(
  year: number,
  allDatedTrips: NormTrip[],
  cityByCode: Map<string, NormCity>,
  firstRegionYear: Map<string, number>,
  firstDestYear: Map<string, number>,
  firstProvinceYear: Map<string, number>,
): AnnualReportData {
  const yearTrips = allDatedTrips
    .filter(tr => tr.year === year)
    .sort((a, b) => compareStrings(a.date, b.date) || compareStrings(a.regionCode, b.regionCode) || compareStrings(a.id, b.id));

  if (yearTrips.length === 0) {
    return {
      year,
      tripCount: 0,
      cityCount: 0,
      regionCount: 0,
      newCityCount: 0,
      newRegionCount: 0,
      provinceCount: 0,
      newProvinceCount: 0,
      spanDays: 0,
      busiestMonth: 0,
      busiestMonthTrips: 0,
      newRegions: [],
      provinces: [],
      trips: [],
    };
  }

  const destInYear = new Set<string>();
  const newDestInYear = new Set<string>();
  const regionCountInYear = new Map<string, number>();
  const regionFirstTripInYear = new Map<string, NormTrip>();
  const provinceCountInYear = new Map<string, number>();
  const provinceFirstTripInYear = new Map<string, NormTrip>();
  const newProvinceInYear = new Set<string>();
  const monthCounts = new Map<number, number>();

  const tripRefs = yearTrips.map(tr => {
    const nc = cityByCode.get(tr.regionCode)!;
    destInYear.add(nc.destKey);
    if (firstDestYear.get(nc.destKey) === year) {
      newDestInYear.add(nc.destKey);
    }
    regionCountInYear.set(tr.regionCode, (regionCountInYear.get(tr.regionCode) ?? 0) + 1);
    if (!regionFirstTripInYear.has(tr.regionCode)) {
      regionFirstTripInYear.set(tr.regionCode, tr);
    }
    provinceCountInYear.set(tr.provinceCode, (provinceCountInYear.get(tr.provinceCode) ?? 0) + 1);
    if (!provinceFirstTripInYear.has(tr.provinceCode)) {
      provinceFirstTripInYear.set(tr.provinceCode, tr);
    }
    if (firstProvinceYear.get(tr.provinceCode) === year) {
      newProvinceInYear.add(tr.provinceCode);
    }
    monthCounts.set(tr.month, (monthCounts.get(tr.month) ?? 0) + 1);
    return tripToRef(tr);
  });

  const newRegions: ReportRecordRef[] = [];
  let mostVisitedCity: ReportRecordRef | undefined;
  for (const [code, count] of regionCountInYear.entries()) {
    const firstTr = regionFirstTripInYear.get(code)!;
    const nc = cityByCode.get(code)!;
    if (firstRegionYear.get(code) === year) {
      newRegions.push({
        kind: "city",
        tripId: nc.earliestTrip,
        regionCode: code,
        regionName: nc.name,
        provinceCode: nc.provinceCode,
        provinceName: nc.provinceName,
        date: nc.earliestDate,
        noteLength: 0,
        notePreview: "",
        tripCount: count,
      });
    }
    const candidate: ReportRecordRef = {
      kind: "city",
      tripId: firstTr.id,
      regionCode: code,
      regionName: nc.name,
      provinceCode: nc.provinceCode,
      provinceName: nc.provinceName,
      date: firstTr.date,
      noteLength: 0,
      notePreview: "",
      tripCount: count,
    };
    if (
      !mostVisitedCity ||
      candidate.tripCount > mostVisitedCity.tripCount ||
      (candidate.tripCount === mostVisitedCity.tripCount && candidate.date < mostVisitedCity.date) ||
      (candidate.tripCount === mostVisitedCity.tripCount &&
        candidate.date === mostVisitedCity.date &&
        candidate.regionCode < mostVisitedCity.regionCode)
    ) {
      mostVisitedCity = candidate;
    }
  }
  newRegions.sort((a, b) => compareStrings(a.date, b.date) || compareStrings(a.regionCode, b.regionCode));

  const provinces: ReportRecordRef[] = [];
  for (const [pCode, count] of provinceCountInYear.entries()) {
    const firstTr = provinceFirstTripInYear.get(pCode)!;
    provinces.push({
      kind: "province",
      tripId: firstTr.id,
      regionCode: firstTr.regionCode,
      regionName: firstTr.regionName,
      provinceCode: pCode,
      provinceName: firstTr.provinceName,
      date: firstTr.date,
      noteLength: 0,
      notePreview: "",
      tripCount: count,
    });
  }
  provinces.sort((a, b) => b.tripCount - a.tripCount || compareStrings(a.provinceCode, b.provinceCode));

  let bestNoteTrip = yearTrips[0];
  for (let i = 1; i < yearTrips.length; i++) {
    const tr = yearTrips[i];
    if (
      tr.noteLength > bestNoteTrip.noteLength ||
      (tr.noteLength === bestNoteTrip.noteLength && tr.date > bestNoteTrip.date) ||
      (tr.noteLength === bestNoteTrip.noteLength && tr.date === bestNoteTrip.date && tr.regionCode < bestNoteTrip.regionCode) ||
      (tr.noteLength === bestNoteTrip.noteLength &&
        tr.date === bestNoteTrip.date &&
        tr.regionCode === bestNoteTrip.regionCode &&
        tr.id < bestNoteTrip.id)
    ) {
      bestNoteTrip = tr;
    }
  }

  let busiestMonth = 0;
  let busiestMonthTrips = 0;
  for (let m = 1; m <= 12; m++) {
    const c = monthCounts.get(m) ?? 0;
    if (c > busiestMonthTrips) {
      busiestMonth = m;
      busiestMonthTrips = c;
    }
  }

  const earliestTrip = tripRefs[0];
  const latestTrip = tripRefs[tripRefs.length - 1];

  return {
    year,
    tripCount: yearTrips.length,
    cityCount: destInYear.size,
    regionCount: regionCountInYear.size,
    newCityCount: newDestInYear.size,
    newRegionCount: newRegions.length,
    provinceCount: provinces.length,
    newProvinceCount: newProvinceInYear.size,
    spanDays: daysBetweenInclusive(earliestTrip.date, latestTrip.date),
    busiestMonth,
    busiestMonthTrips,
    earliestTrip,
    latestTrip,
    longestNoteTrip: tripToRef(bestNoteTrip),
    mostVisitedCity,
    newRegions,
    provinces,
    trips: tripRefs,
  };
}

function buildMilestoneAchievement(
  id: string,
  title: string,
  description: string,
  target: number,
  items: ReportRecordRef[],
): AchievementData {
  const current = items.length;
  const unlocked = current >= target;
  let unlockedAt = "";
  let evidence: ReportRecordRef[] = [];
  if (unlocked) {
    unlockedAt = items[target - 1].date;
    evidence = target <= 5 ? items.slice(0, target) : [items[0], items[1], items[2], items[3], items[target - 1]];
  } else if (items.length > 0) {
    evidence = items.slice(0, Math.min(items.length, 5));
  }
  return {
    id,
    title,
    description,
    unlocked,
    current,
    target,
    unlockedAt,
    evidence,
  };
}

function buildAchievements(cities: NormCity[], exploredSet: Set<string>): AchievementData[] {
  const citiesByProvince = new Map<string, NormCity[]>();
  const destBest = new Map<string, ReportRecordRef>();
  const richNoteTrips: ReportRecordRef[] = [];

  for (const nc of cities) {
    const pList = citiesByProvince.get(nc.provinceCode) ?? [];
    pList.push(nc);
    citiesByProvince.set(nc.provinceCode, pList);

    const cityRef: ReportRecordRef = {
      kind: "city",
      tripId: nc.earliestTrip,
      regionCode: nc.code,
      regionName: nc.name,
      provinceCode: nc.provinceCode,
      provinceName: nc.provinceName,
      date: nc.earliestDate,
      noteLength: 0,
      notePreview: "",
      tripCount: nc.trips.length,
    };
    const prev = destBest.get(nc.destKey);
    if (!prev) {
      destBest.set(nc.destKey, cityRef);
    } else {
      prev.tripCount += cityRef.tripCount;
      if (
        compareDatesEmptyLast(cityRef.date, prev.date) < 0 ||
        (cityRef.date === prev.date && cityRef.regionCode < prev.regionCode)
      ) {
        prev.tripId = cityRef.tripId;
        prev.regionCode = cityRef.regionCode;
        prev.regionName = cityRef.regionName;
        prev.date = cityRef.date;
      }
    }
    for (const tr of nc.trips) {
      if (tr.noteLength >= 10) {
        richNoteTrips.push(tripToRef(tr));
      }
    }
  }

  const sortedProvinces: ReportRecordRef[] = [];
  const completedProvinces: ReportRecordRef[] = [];
  for (const pCode of exploredSet) {
    const p = provinceByCode.get(pCode);
    if (!p) continue;
    const pCities = citiesByProvince.get(pCode) ?? [];
    let earliestDate = "";
    let earliestTrip = "";
    let earliestRegionCode = "";
    let earliestRegionName = "";
    let latestCityFirstDate = "";
    let totalProvinceTrips = 0;
    for (const nc of pCities) {
      totalProvinceTrips += nc.trips.length;
      if (nc.earliestDate !== "") {
        if (
          earliestDate === "" ||
          nc.earliestDate < earliestDate ||
          (nc.earliestDate === earliestDate && nc.code < earliestRegionCode)
        ) {
          earliestDate = nc.earliestDate;
          earliestTrip = nc.earliestTrip;
          earliestRegionCode = nc.code;
          earliestRegionName = nc.name;
        }
        if (nc.earliestDate > latestCityFirstDate) {
          latestCityFirstDate = nc.earliestDate;
        }
      }
    }
    sortedProvinces.push({
      kind: "province",
      tripId: earliestTrip,
      regionCode: earliestRegionCode,
      regionName: earliestRegionName,
      provinceCode: pCode,
      provinceName: p.name,
      date: earliestDate,
      noteLength: 0,
      notePreview: "",
      tripCount: totalProvinceTrips,
    });
    if (p.cities.length > 0 && pCities.length === p.cities.length) {
      completedProvinces.push({
        kind: "province",
        tripId: earliestTrip,
        regionCode: earliestRegionCode,
        regionName: earliestRegionName,
        provinceCode: pCode,
        provinceName: p.name,
        date: latestCityFirstDate,
        noteLength: 0,
        notePreview: "",
        tripCount: pCities.length,
      });
    }
  }
  sortedProvinces.sort((a, b) => compareDatesEmptyLast(a.date, b.date) || compareStrings(a.provinceCode, b.provinceCode));
  completedProvinces.sort((a, b) => compareDatesEmptyLast(a.date, b.date) || compareStrings(a.provinceCode, b.provinceCode));

  const sortedDestinations = Array.from(destBest.values()).sort(
    (a, b) => compareDatesEmptyLast(a.date, b.date) || compareStrings(a.regionCode, b.regionCode),
  );
  richNoteTrips.sort(
    (a, b) =>
      compareDatesEmptyLast(a.date, b.date) || compareStrings(a.regionCode, b.regionCode) || compareStrings(a.tripId, b.tripId),
  );

  let bestRepeatCity: NormCity | undefined;
  for (const nc of cities) {
    if (nc.trips.length === 0) continue;
    if (!bestRepeatCity || nc.trips.length > bestRepeatCity.trips.length) {
      bestRepeatCity = nc;
      continue;
    }
    if (nc.trips.length === bestRepeatCity.trips.length) {
      const idxCmp = Math.min(nc.trips.length, 3) - 1;
      const cmp = compareDatesEmptyLast(nc.trips[idxCmp].date, bestRepeatCity.trips[idxCmp].date);
      if (cmp < 0 || (cmp === 0 && nc.code < bestRepeatCity.code)) {
        bestRepeatCity = nc;
      }
    }
  }

  const multiTripAch: AchievementData = {
    id: "multi_trip_city",
    title: "故地重游",
    description: "在同一座城市或区县记录至少 3 次旅行",
    unlocked: bestRepeatCity ? bestRepeatCity.trips.length >= 3 : false,
    current: bestRepeatCity ? bestRepeatCity.trips.length : 0,
    target: 3,
    unlockedAt: bestRepeatCity && bestRepeatCity.trips.length >= 3 ? bestRepeatCity.trips[2].date : "",
    evidence: bestRepeatCity ? bestRepeatCity.trips.slice(0, 5).map(tripToRef) : [],
  };

  return [
    buildMilestoneAchievement("first_footprint", "初踏山海", "点亮第 1 个省级区域（含城市打卡或省份独立标记）", 1, sortedProvinces),
    buildMilestoneAchievement("ten_cities", "十城足迹", "按全国城市口径累计打卡达到 10 座城市", 10, sortedDestinations),
    buildMilestoneAchievement("fifty_cities", "半百风物", "按全国城市口径累计打卡达到 50 座城市", 50, sortedDestinations),
    buildMilestoneAchievement("half_china", "半壁山河", "已探索省级区域达到 17 个（全国 34 个省级区域的一半）", 17, sortedProvinces),
    buildMilestoneAchievement("all_china", "九州同游", "点亮全国全部 34 个省级区域", 34, sortedProvinces),
    buildMilestoneAchievement(
      "province_master",
      "全省制霸",
      "在任意一个省级区域打卡其全部可记录城市或区县（不含仅省份标记）",
      1,
      completedProvinces,
    ),
    multiTripAch,
    buildMilestoneAchievement("chronicler", "山海执笔人", "累计撰写 5 条不少于 10 字的旅行记忆", 5, richNoteTrips),
  ];
}
