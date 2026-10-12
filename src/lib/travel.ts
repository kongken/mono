export type Visit = {
  code: string;
  name: string;
  province: string;
  color: string;
  date: string;
  note: string;
};
export type Saved = {
  version: 1;
  visits: Visit[];
  provinces: Record<string, string>;
};
export const colors = [
  { value: "#4b937d", name: "山野绿" },
  { value: "#e2af5c", name: "日落黄" },
  { value: "#d88370", name: "珊瑚橙" },
  { value: "#719fc6", name: "海岸蓝" },
  { value: "#a08cbd", name: "暮光紫" },
];

function leastUsedColor(used: string[]): string {
  const counts = colors.map((color) => used.filter((value) => value === color.value).length);
  return colors[counts.indexOf(Math.min(...counts))].value;
}

export function provinceColor(saved: Saved, province: string): string | undefined {
  return saved.provinces[province] || saved.visits.find((visit) => visit.province === province)?.color;
}

export function nationalProvinceColors(saved: Saved): Record<string, string> {
  // Derive display colors independently from stored city colors, including old backups.
  const explored = new Set([
    ...Object.keys(saved.provinces),
    ...saved.visits.map((visit) => visit.province),
  ]);
  return Object.fromEntries(
    [...explored].sort().map((code, index) => [code, colors[index % colors.length].value]),
  );
}

export function automaticProvinceColor(saved: Saved, province: string): string {
  const existing = provinceColor(saved, province);
  if (existing) return existing;
  const explored = new Set([...Object.keys(saved.provinces), ...saved.visits.map((visit) => visit.province)]);
  return leastUsedColor([...explored].map((code) => provinceColor(saved, code)!));
}

export function automaticColor(saved: Saved, province: string): string {
  const visits = saved.visits.filter((visit) => visit.province === province);
  return visits.length
    ? leastUsedColor(visits.map((visit) => visit.color))
    : automaticProvinceColor(saved, province);
}

export function toggleProvince(saved: Saved, province: string): Saved {
  const provinces = { ...saved.provinces };
  if (provinces[province]) delete provinces[province];
  else provinces[province] = automaticProvinceColor(saved, province);
  return { ...saved, provinces };
}

export function toggleVisit(
  saved: Saved,
  province: string,
  city: { code: string; name: string },
  date: string,
): Saved {
  if (saved.visits.some((visit) => visit.code === city.code)) {
    return {
      ...saved,
      visits: saved.visits.filter((visit) => visit.code !== city.code),
    };
  }
  return {
    ...saved,
    visits: [
      ...saved.visits,
      {
        code: city.code,
        name: city.name,
        province,
        color: automaticColor(saved, province),
        date,
        note: "",
      },
    ],
  };
}
