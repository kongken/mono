// Keep source IDs stable: the prefix identifies the boundary dataset, not ownership.
export function canonicalVisitProvince<T>(code: unknown, province: T): T | "350000" {
  return province === "710000" && (code === "tw-09020" || code === "tw-09007")
    ? "350000"
    : province;
}
