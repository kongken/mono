/** Pure formatting helpers (no generated code, so they run under plain node in tests). */
export function formatKm(km: number): string {
  return `${(Math.round(km * 100) / 100).toLocaleString("zh-CN", { maximumFractionDigits: 2 })} km`;
}

/** The year of a YYYY-MM-DD date, or "" when the date is missing. */
export function yearOf(date: string): string {
  return /^\d{4}-\d{2}-\d{2}$/.test(date) ? date.slice(0, 4) : "";
}

/**
 * Only https addresses are ever used for images and links, whoever supplied them.
 * Anything else (http:, javascript:, data:, relative) is dropped.
 */
export function httpsOnly(url: string | undefined): string | undefined {
  if (!url) return undefined;
  try {
    const u = new URL(url);
    return u.protocol === "https:" && u.host !== "" ? u.toString() : undefined;
  } catch {
    return undefined;
  }
}

const socialBase: Record<string, string> = {
  twitter: "https://x.com/",
  instagram: "https://www.instagram.com/",
  weibo: "https://weibo.com/n/",
  facebook: "https://www.facebook.com/",
  github: "https://github.com/",
};

/** A user name or an https link to the profile on `platform`; undefined when neither. */
export function socialLink(platform: string, value: string): string | undefined {
  const v = value.trim();
  if (!v) return undefined;
  if (/^[a-z][a-z0-9+.-]*:/i.test(v)) return httpsOnly(v);
  const base = socialBase[platform];
  if (!base || /[/\s?#]/.test(v)) return undefined;
  return base + encodeURIComponent(v.replace(/^@/, ""));
}

/** Parses "2019" style input; "" is unknown (undefined), anything else invalid is NaN. */
export function parseYear(input: string): number | undefined {
  const t = input.trim();
  if (t === "") return undefined;
  const n = Number(t);
  return Number.isInteger(n) && n >= 1900 && n <= new Date().getFullYear() ? n : NaN;
}

/** Splits a textarea into non-empty trimmed lines. */
export const lines = (s: string) => s.split("\n").map((l) => l.trim()).filter(Boolean);
