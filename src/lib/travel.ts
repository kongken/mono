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

export function automaticColor(saved: Saved, province: string): string {
  const counts = colors.map(
    (color) =>
      saved.visits.filter(
        (visit) => visit.province === province && visit.color === color.value,
      ).length,
  );
  return colors[counts.indexOf(Math.min(...counts))].value;
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
