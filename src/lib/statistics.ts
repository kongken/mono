import regions from "../regions.json";

export const totalCities = regions.reduce((total, region) => total + region.cities.length, 0);
export const percentage = (count: number, total: number) => total ? `${(count / total * 100).toFixed(1)}%` : "0.0%";

