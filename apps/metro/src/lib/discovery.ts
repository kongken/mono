import type { Line } from "@kongken/api-client/bff/metro/v1/catalog_pb";

export const formations = [
  { value: "all", label: "全部编组" },
  { value: "6ab", label: "6A/6B" },
  { value: "8a", label: "8A" },
  { value: "special", label: "特种/胶轮" },
  { value: "unknown", label: "未分类" },
] as const;
export type Formation = typeof formations[number]["value"];

export function matchesFormation(line: Line, formation: Formation): boolean {
  const hasCars = (n: number) => line.cars === n || line.carsAddition === n;
  switch (formation) {
    case "all": return true;
    case "6ab": return (line.vehicleType === "a" || line.vehicleType === "b") && hasCars(6);
    case "8a": return line.vehicleType === "a" && hasCars(8);
    case "special": return ["l", "rubber", "other"].includes(line.vehicleType);
    case "unknown": return !line.vehicleType;
  }
}

/** Name, code and all supplied station names are searchable, without interpreting model codes as classes. */
export function matchesLine(line: Line, query: string): boolean {
  const q = query.trim().toLocaleLowerCase();
  return !q || [line.name, line.code, ...line.stationNames].some((s) => s.toLocaleLowerCase().includes(q));
}
