import indexData from "@trip-map/geo-data/search-index.json" with { type: "json" };
import type { Saved } from "./travel.ts";

const index: Record<string, string[]> = indexData;
type Area = { code: string; name: string; short?: string };
export type NoteMatch = {
  regionCode: string;
  regionName: string;
  provinceCode: string;
  tripId: string;
  date: string;
  note: string;
};

/** Names and romanization ignore case, tones and separators, not region IDs. */
export function normalizeRegionQuery(query: string): string {
  return query
    .normalize("NFKC")
    .toLowerCase()
    .normalize("NFD")
    .replace(/u\u0308/gu, "v")
    .replace(/\p{M}/gu, "")
    .replace(/[\s'’_-]/gu, "");
}

// One insertion, deletion, substitution or adjacent transposition. Never used
// for short initialisms: fuzzing "hz" would match almost the whole country.
export function oneEditAway(a: string, b: string): boolean {
  const x = [...a],
    y = [...b];
  if (Math.abs(x.length - y.length) > 1) return false;
  let i = 0;
  while (i < x.length && x[i] === y[i]) i++;
  if (i === x.length && i === y.length) return true;
  if (x.length === y.length) {
    if (x.slice(i + 1).join("") === y.slice(i + 1).join("")) return true;
    return (
      x[i] === y[i + 1] &&
      x[i + 1] === y[i] &&
      x.slice(i + 2).join("") === y.slice(i + 2).join("")
    );
  }
  return x.length > y.length
    ? x.slice(i + 1).join("") === y.slice(i).join("")
    : x.slice(i).join("") === y.slice(i + 1).join("");
}

/** Prefer exact/substring name, then pinyin/initials. Fuzzy only if none match. */
export function searchAreas<T extends Area>(
  areas: readonly T[],
  query: string,
): T[] {
  const q = normalizeRegionQuery(query.trim().slice(0, 100));
  if (!q) return [...areas];
  const candidates = areas.map((area) => ({
    area,
    names: [area.name, area.short]
      .filter(Boolean)
      .map((n) => normalizeRegionQuery(n!)),
    keys: index[area.code] ?? [],
  }));
  const direct = candidates
    .map((c) => ({
      ...c,
      rank: c.names.some((n) => n === q)
        ? 0
        : c.names.some((n) => n.includes(q))
          ? 1
          : c.keys.some((k) => k.includes(q))
            ? 2
            : 3,
    }))
    .filter((c) => c.rank < 3);
  if (direct.length)
    return direct.sort((a, b) => a.rank - b.rank).map((c) => c.area);
  const fuzzy = /^[a-z]+$/.test(q)
    ? q.length >= 4
    : /^\p{Script=Han}{2,}$/u.test(q);
  return fuzzy
    ? candidates
        .filter((c) =>
          [
            ...c.names,
            ...c.names.map((n) => n.replace(/[省市区县]$/u, "")),
            ...c.keys,
          ].some((k) => oneEditAway(q, k)),
        )
        .map((c) => c.area)
    : [];
}

export function searchProvinces<T extends Area & { cities: Area[] }>(
  provinces: readonly T[],
  query: string,
): T[] {
  if (!query.trim()) return [...provinces];
  // Search all destinations together so a fuzzy province cannot displace an
  // exact city match. National results still navigate to the owning province.
  const matches = new Set(
    searchAreas(
      provinces.flatMap((p) => [p, ...p.cities]),
      query,
    ).map((a) => a.code),
  );
  return provinces.filter(
    (p) => matches.has(p.code) || p.cities.some((c) => matches.has(c.code)),
  );
}

/** Literal, case-insensitive AND search across the complete note. No query DSL. */
export function noteTerms(query: string): string[] {
  return query.trim().toLowerCase().split(/\s+/u).filter(Boolean);
}
export function searchNotes(
  saved: Saved,
  query: string,
  provinceCode = "",
): NoteMatch[] {
  const terms = noteTerms(query);
  if (!terms.length || query.length > 100 || terms.length > 8) return [];
  return saved.visits
    .filter((v) => !provinceCode || v.province === provinceCode)
    .flatMap((v) =>
      v.trips
        .filter((t) =>
          terms.every((term) => t.note.toLowerCase().includes(term)),
        )
        .map((t) => ({
          regionCode: v.code,
          regionName: v.name,
          provinceCode: v.province,
          tripId: t.id,
          date: t.date,
          note: t.note,
        })),
    )
    .sort(
      (a, b) =>
        b.date.localeCompare(a.date) ||
        a.regionCode.localeCompare(b.regionCode) ||
        a.tripId.localeCompare(b.tripId),
    );
}

/** Keep the matched part visible, even if it occurs near the end of a note. */
export function noteExcerpt(note: string, query: string): string {
  const term = noteTerms(query)[0] ?? "";
  const position = note.toLowerCase().indexOf(term);
  const start = Math.max(0, position - 24);
  const end = Math.min(
    note.length,
    Math.max(start + 120, position + term.length),
  );
  return (
    (start ? "…" : "") + note.slice(start, end) + (end < note.length ? "…" : "")
  );
}
