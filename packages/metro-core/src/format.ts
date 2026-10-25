/** Pure formatting helpers (no generated code, so they run under plain node in tests). */
export function formatKm(km: number): string {
  return `${(Math.round(km * 100) / 100).toLocaleString("zh-CN", { maximumFractionDigits: 2 })} km`;
}

/** Splits a textarea into non-empty trimmed lines. */
export const lines = (s: string) => s.split("\n").map((l) => l.trim()).filter(Boolean);
