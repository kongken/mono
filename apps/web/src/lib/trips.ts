import type { Saved, Trip, Visit } from "./travel.ts";

/** Trip notes count UTF-16 code units, like the browser's string length. */
export const TRIP_NOTE_LIMIT = 1000;
export const TRIP_ID_PATTERN = /^[A-Za-z0-9_-]{1,64}$/;

/**
 * Accepts only dates that exist on the calendar, such as 2024-02-29 but not 2026-02-30.
 * Year 0000 is accepted, like on the backend (Go's time.Parse): the proleptic
 * Gregorian calendar has a year 0, and a value an earlier version stored must
 * never become unreadable. Both sides are checked against the same fixtures.
 */
export function isRealDate(date: string): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  if (!m) return false;
  const [year, month, day] = [Number(m[1]), Number(m[2]), Number(m[3])];
  if (month < 1 || month > 12 || day < 1) return false;
  const probe = new Date(Date.UTC(2000, month - 1, day));
  probe.setUTCFullYear(year);
  return (
    probe.getUTCFullYear() === year &&
    probe.getUTCMonth() === month - 1 &&
    probe.getUTCDate() === day
  );
}
/** A trip date is optional, but when present it must be a real calendar date. */
export const isValidTripDate = (date: unknown): date is string =>
  typeof date === "string" && (date === "" || isRealDate(date));

/** Schema 1 data becomes this trip; the backend derives the same ID. */
export const legacyTripId = (code: string) => `legacy-${code}`;

const base36 = (n: number, width: number) => n.toString(36).padStart(width, "0").slice(-width);
function randomPart(length: number): string {
  const bytes = new Uint8Array(length);
  if (globalThis.crypto?.getRandomValues) globalThis.crypto.getRandomValues(bytes);
  else for (let i = 0; i < length; i++) bytes[i] = Math.floor(Math.random() * 256);
  return [...bytes].map((b) => (b % 36).toString(36)).join("");
}
/**
 * Time-ordered random ID: later trips sort after earlier ones, and two devices
 * never pick the same ID in practice.
 */
export function newTripId(now = Date.now()): string {
  return `t${base36(now, 9)}${randomPart(8)}`;
}

/** Storage order: by ID ascending, so equal data always has an equal shape. */
export const byTripId = (a: Trip, b: Trip) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
export const canonicalTrips = (trips: readonly Trip[]): Trip[] => [...trips].sort(byTripId);

/** Display order: newest date first, trips without a date last, newest ID first among equals. */
export function displayTrips(trips: readonly Trip[]): Trip[] {
  return [...trips].sort(
    (a, b) =>
      Number(a.date === "") - Number(b.date === "") ||
      (a.date < b.date ? 1 : a.date > b.date ? -1 : 0) ||
      -byTripId(a, b),
  );
}
export const latestTrip = (visit: Visit): Trip | undefined => displayTrips(visit.trips)[0];
/** The latest real date among the city's trips, or "" when none has a date. */
export const cityLatestDate = (visit: Visit): string =>
  visit.trips.reduce((latest, trip) => (trip.date > latest ? trip.date : latest), "");

export const tripCount = (saved: Saved): number =>
  saved.visits.reduce((total, visit) => total + visit.trips.length, 0);

/**
 * Cities by the date of their latest trip, newest first. Cities without any
 * dated trip come last; ties break by region code so the order is stable.
 */
export function sortVisitsByLatest(visits: readonly Visit[]): Visit[] {
  return [...visits].sort((a, b) => {
    const da = cityLatestDate(a);
    const db = cityLatestDate(b);
    return Number(da === "") - Number(db === "") || (da < db ? 1 : da > db ? -1 : 0) || (a.code < b.code ? -1 : a.code > b.code ? 1 : 0);
  });
}

/**
 * Names one trip of a city so two trips never read alike: its position in the
 * editor's list ("第 2 次"), its date or "日期未记录", and the start of its note.
 * Used wherever a trip has to be picked or shown on its own.
 */
export function tripLabel(trips: readonly Trip[], id: string, noteChars = 12): string {
  const ordered = displayTrips(trips);
  const index = ordered.findIndex((t) => t.id === id);
  if (index < 0) return "";
  const { date, note } = ordered[index];
  const text = note.replace(/\s+/g, " ").trim();
  const clip = text.length > noteChars ? `${text.slice(0, noteChars)}…` : text;
  return `第 ${index + 1} 次 · ${date || "日期未记录"}${clip ? ` · ${clip}` : ""}`;
}
