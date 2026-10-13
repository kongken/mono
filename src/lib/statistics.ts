import regions from "../regions.json" with { type: "json" };
import type { Saved } from "./travel.ts";

const districtDestinations = new Set(["110000", "120000", "310000", "500000", "810000", "820000"]);
export const isDistrictDestination = (province: string) => districtDestinations.has(province);

// Districts remain independent records; only nationwide city metrics collapse them.
export const totalCities = regions.reduce((total, region) => total + (isDistrictDestination(region.code) ? 1 : region.cities.length), 0);
export function visitedCityCount(saved: Saved): number {
  return new Set(saved.visits.map(visit => isDistrictDestination(visit.province) ? visit.province : visit.code)).size;
}
export const percentage = (count: number, total: number) => total ? `${(count / total * 100).toFixed(1)}%` : "0.0%";
