/** Optional catalog classifications, shared by the public site and the console. */
export const regions = [
  { value: "east", label: "华东" },
  { value: "north", label: "华北" },
  { value: "south", label: "华南" },
  { value: "southwest", label: "西南" },
  { value: "central", label: "华中" },
  { value: "northeast", label: "东北" },
  { value: "northwest", label: "西北" },
] as const;

export const vehicleTypes = [
  { value: "a", label: "A 型" },
  { value: "b", label: "B 型" },
  { value: "l", label: "L 型" },
  { value: "rubber", label: "胶轮" },
  { value: "other", label: "其他特种车型" },
] as const;

export const regionLabel = (value: string) => regions.find((r) => r.value === value)?.label ?? "区域未分类";
