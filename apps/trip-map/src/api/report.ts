import { createClient } from "@connectrpc/connect";
import {
  ReportService,
  type AchievementStatus,
  type AnnualReport,
  type GetReportResponse,
  type ReportOverview as ProtoReportOverview,
  type ReportProvinceProgress as ProtoProvinceProgress,
  type ReportRecordRef as ProtoRecordRef,
} from "@kongken/api-client/bff/tripmap/v1/report_pb";
import type {
  AchievementData,
  AnnualReportData,
  ReportOverview,
  ReportProvinceProgress,
  ReportRecordRef,
  TravelReportData,
} from "@/lib/report";
import { transport } from "./transport";

const raw = transport ? createClient(ReportService, transport) : null;
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

function mapRef(r: ProtoRecordRef | undefined): ReportRecordRef | undefined {
  if (!r) return undefined;
  const kind = r.kind === "province" || r.kind === "city" ? r.kind : "trip";
  return {
    kind,
    tripId: r.tripId,
    regionCode: r.regionCode,
    regionName: r.regionName,
    provinceCode: r.provinceCode,
    provinceName: r.provinceName,
    date: r.date,
    noteLength: r.noteLength,
    notePreview: r.notePreview,
    tripCount: r.tripCount,
  };
}

function mapProvinceProgress(p: ProtoProvinceProgress): ReportProvinceProgress {
  return {
    provinceCode: p.provinceCode,
    provinceName: p.provinceName,
    visitedCities: p.visitedCities,
    totalCities: p.totalCities,
    markedOnly: p.markedOnly,
    visitedRegions: p.visitedRegions.map(r => mapRef(r)!),
  };
}

function mapOverview(o: ProtoReportOverview | undefined): ReportOverview {
  return {
    exploredProvinces: o?.exploredProvinces ?? 0,
    totalProvinces: o?.totalProvinces ?? 34,
    visitedCities: o?.visitedCities ?? 0,
    totalCities: o?.totalCities ?? 391,
    visitedRegions: o?.visitedRegions ?? 0,
    totalRegions: o?.totalRegions ?? 497,
    totalTrips: o?.totalTrips ?? 0,
    datedTrips: o?.datedTrips ?? 0,
    undatedTrips: o?.undatedTrips ?? 0,
    provinceProgress: (o?.provinceProgress ?? []).map(mapProvinceProgress),
  };
}

function mapAnnual(a: AnnualReport): AnnualReportData {
  return {
    year: a.year,
    tripCount: a.tripCount,
    cityCount: a.cityCount,
    regionCount: a.regionCount,
    newCityCount: a.newCityCount,
    newRegionCount: a.newRegionCount,
    provinceCount: a.provinceCount,
    newProvinceCount: a.newProvinceCount,
    spanDays: a.spanDays,
    busiestMonth: a.busiestMonth,
    busiestMonthTrips: a.busiestMonthTrips,
    earliestTrip: mapRef(a.earliestTrip),
    latestTrip: mapRef(a.latestTrip),
    longestNoteTrip: mapRef(a.longestNoteTrip),
    mostVisitedCity: mapRef(a.mostVisitedCity),
    newRegions: a.newRegions.map(r => mapRef(r)!),
    provinces: a.provinces.map(r => mapRef(r)!),
    trips: a.trips.map(r => mapRef(r)!),
  };
}

function mapAchievement(a: AchievementStatus): AchievementData {
  return {
    id: a.id,
    title: a.title,
    description: a.description,
    unlocked: a.unlocked,
    current: a.current,
    target: a.target,
    unlockedAt: a.unlockedAt,
    evidence: a.evidence.map(r => mapRef(r)!),
  };
}

export function mapReportResponse(res: GetReportResponse): TravelReportData {
  return {
    revision: Number(res.revision),
    timezone: res.timezone,
    generatedOn: res.generatedOn,
    currentYear: res.currentYear,
    selectedYear: res.selectedYear,
    availableYears: [...res.availableYears],
    overview: mapOverview(res.overview),
    annualReports: res.annualReports.map(mapAnnual),
    achievements: res.achievements.map(mapAchievement),
    fromCache: res.fromCache,
  };
}

export const reportApi = {
  getReport: async (timezone?: string, year?: number): Promise<TravelReportData> => {
    const res = await client().getReport({
      timezone: timezone ?? "",
      year: year ?? 0,
    });
    return mapReportResponse(res);
  },
};
