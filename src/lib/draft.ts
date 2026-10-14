import type { Trip, Visit } from "./travel.ts";
import { canonicalTrips } from "./trips.ts";

/**
 * Saving the city editor merges what the user changed with what the city looks
 * like when they press save, not with what it looked like when the editor was
 * opened. The editor remembers the city as it opened (the base); only the
 * difference between the base and the draft is the user's change, and that
 * change is applied to the live city. Changes that arrived meanwhile (a trip
 * added or edited elsewhere, a synced edit) are kept. When both sides touched
 * the same record the user is asked.
 */

/** The city as the editor opened it. A city that did not exist yet has no trips. */
export type DraftBase = {
  code: string;
  existed: boolean;
  color: string;
  trips: Trip[];
};

/** What the editor holds. */
export type DraftValue = {
  code: string;
  name: string;
  province: string;
  color: string;
  trips: Trip[];
};

export type DraftConflict =
  /** The city was cancelled meanwhile and the draft still changes it. */
  | { key: "city"; kind: "city-removed" }
  /** The color was changed on both sides to different values. */
  | { key: "color"; kind: "color"; mine: string; latest: string }
  /**
   * One trip was changed on both sides. `mine` / `latest` are null when that side
   * deleted the trip.
   */
  | { key: string; kind: "trip"; id: string; mine: Trip | null; latest: Trip | null };

/** The user's choice per conflict key. */
export type DraftChoices = Record<string, "mine" | "latest">;

export type DraftResult =
  | { kind: "noop"; conflicts: DraftConflict[] }
  | { kind: "set"; visit: Visit; conflicts: [] }
  /** Conflicts without a choice yet; nothing has been decided. */
  | { kind: "ask"; conflicts: DraftConflict[] };

const sameTrip = (a: Trip, b: Trip) => a.date === b.date && a.note === b.note;
export const tripKeyOf = (id: string) => `trip:${id}`;

/** True when the draft differs from the base in anything the user can edit. */
export function draftChanged(base: DraftBase, draft: DraftValue): boolean {
  if (!base.existed || draft.color !== base.color) return true;
  if (draft.trips.length !== base.trips.length) return true;
  const before = new Map(base.trips.map((t) => [t.id, t]));
  return draft.trips.some((t) => {
    const b = before.get(t.id);
    return !b || !sameTrip(b, t);
  });
}

/**
 * Applies the draft's changes onto the live city.
 * `live` is the city at the moment of saving (undefined when it was cancelled).
 */
export function mergeDraft(
  base: DraftBase,
  draft: DraftValue,
  live: Visit | undefined,
  choices: DraftChoices = {},
): DraftResult {
  const baseTrips = new Map(base.trips.map((t) => [t.id, t]));
  const draftTrips = new Map(draft.trips.map((t) => [t.id, t]));
  const colorEdited = draft.color !== base.color;

  // Nothing of the city is left to merge into.
  if (!live) {
    if (base.existed) {
      if (!draftChanged(base, draft)) return { kind: "noop", conflicts: [] };
      const conflict: DraftConflict = { key: "city", kind: "city-removed" };
      const choice = choices.city;
      if (!choice) return { kind: "ask", conflicts: [conflict] };
      if (choice === "latest") return { kind: "noop", conflicts: [] };
    }
    // A new city, or the user chose to bring the cancelled one back with the draft.
    return {
      kind: "set",
      visit: {
        code: draft.code,
        name: draft.name,
        province: draft.province,
        color: draft.color,
        trips: canonicalTrips(draft.trips),
      },
      conflicts: [],
    };
  }

  const conflicts: DraftConflict[] = [];
  let color = live.color;
  if (colorEdited && live.color !== draft.color) {
    if (live.color === base.color) color = draft.color;
    else {
      conflicts.push({ key: "color", kind: "color", mine: draft.color, latest: live.color });
      if (choices.color === "mine") color = draft.color;
    }
  }

  const trips = new Map(live.trips.map((t) => [t.id, t]));
  const touched = new Set([...baseTrips.keys(), ...draftTrips.keys()]);
  for (const id of touched) {
    const before = baseTrips.get(id);
    const mine = draftTrips.get(id);
    const now = trips.get(id);
    // The user did not change this trip: whatever the city has now stays.
    if (before && mine && sameTrip(before, mine)) continue;
    if (!before && !mine) continue;
    // Added by the user. IDs are unique, so nothing else can hold it, but if it
    // is already there with the same content there is nothing to do.
    if (!before && mine) {
      if (!now || !sameTrip(now, mine)) {
        if (now) conflicts.push({ key: tripKeyOf(id), kind: "trip", id, mine, latest: now });
        if (!now || choices[tripKeyOf(id)] === "mine") trips.set(id, mine);
      }
      continue;
    }
    // Deleted by the user.
    if (before && !mine) {
      if (!now) continue; // already gone
      if (sameTrip(now, before)) trips.delete(id);
      else {
        conflicts.push({ key: tripKeyOf(id), kind: "trip", id, mine: null, latest: now });
        if (choices[tripKeyOf(id)] === "mine") trips.delete(id);
      }
      continue;
    }
    // Edited by the user.
    if (before && mine) {
      if (!now) {
        conflicts.push({ key: tripKeyOf(id), kind: "trip", id, mine, latest: null });
        if (choices[tripKeyOf(id)] === "mine") trips.set(id, mine);
      } else if (sameTrip(now, mine)) continue;
      else if (sameTrip(now, before)) trips.set(id, mine);
      else {
        conflicts.push({ key: tripKeyOf(id), kind: "trip", id, mine, latest: now });
        if (choices[tripKeyOf(id)] === "mine") trips.set(id, mine);
      }
    }
  }
  const open = conflicts.filter((c) => !choices[c.key]);
  if (open.length) return { kind: "ask", conflicts: open };
  return {
    kind: "set",
    visit: { ...live, color, trips: canonicalTrips([...trips.values()]) },
    conflicts: [],
  };
}
