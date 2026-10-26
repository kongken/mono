import { isValidTripDate, newTripId } from "./trips.ts";
import {
  parseCoordinateInput,
  trimWorldName,
  WORLD_NAME_LIMIT,
  WORLD_NOTE_LIMIT,
  WorldValidationError,
  type PlaceDraft,
  type WorldCodes,
  type WorldPlace,
  type WorldTrip,
} from "./world.ts";

// The city form of the world view: what the dialog holds while it is open (text exactly as
// typed), and the one place that turns it into a city or into a reason the user can act on.
// Nothing here touches storage; the world document is only changed by `savePlace`.

export type PlaceForm = {
  /** Present when an existing city is edited. */
  id?: string;
  name: string;
  /** The ISO code chosen, or "" while none is. */
  country: string;
  /** True once the user picked the country from the list: a later location no longer changes it. */
  countryChosen: boolean;
  lat: string;
  lng: string;
  trips: WorldTrip[];
};

/** Where the problem is: one of the fields, or one trip by its ID. */
export type FormField = "name" | "country" | "lat" | "lng" | { trip: string; part: "date" | "note" };
export type FormError = { field?: FormField; message: string };

export const emptyForm = (): PlaceForm => ({ name: "", country: "", countryChosen: false, lat: "", lng: "", trips: [] });

export const formFromPlace = (place: WorldPlace): PlaceForm => ({
  id: place.id,
  name: place.name,
  country: place.country,
  countryChosen: true,
  lat: String(place.lat),
  lng: String(place.lng),
  trips: place.trips.map((trip) => ({ ...trip })),
});

export const newTrip = (): WorldTrip => ({ id: newTripId(), date: "", note: "" });

/** The coordinates of a form when both are readable, else null (used to locate and to draw). */
export function formLocation(form: Pick<PlaceForm, "lat" | "lng">): { lat: number; lng: number } | null {
  try {
    return { lat: parseCoordinateInput(form.lat, "lat"), lng: parseCoordinateInput(form.lng, "lng") };
  } catch {
    return null;
  }
}

/** Degrees as a form shows them: the shortest text that is the same number. */
export const coordinateText = (degrees: number): string => String(Math.round(degrees * 1e6) / 1e6);

/** The refusal for a location inside China, here and in the hint under the coordinates. */
export const CHINA_LOCATION_MESSAGE = "这个位置在中国。中国的城市请在中国视图记录，世界视图只记录海外城市。";

/**
 * Reads the form strictly, field by field in the order the user sees them. The first problem
 * is returned with the field it belongs to; a form that passes can still be refused by
 * `savePlace` (the limits of the whole document), which reports in the same way.
 *
 * `inChina` says whether the coordinates fall inside China on the map the page has drawn. The data
 * layer has no geometry (a stored or imported city is checked only for its country code, which is
 * never `CN`), so the form is where a location is held against the map: the caller works it out
 * with `hitTestWorld` and passes it in, for typed, picked and edited coordinates alike.
 */
export function readForm(form: PlaceForm, codes: WorldCodes, inChina = false): { draft: PlaceDraft } | { error: FormError } {
  const name = trimWorldName(form.name);
  if (!name) return { error: { field: "name", message: "请填写城市名称" } };
  if (name.length > WORLD_NAME_LIMIT) return { error: { field: "name", message: `城市名称最多 ${WORLD_NAME_LIMIT} 个字符` } };
  let lat: number;
  let lng: number;
  try {
    lat = parseCoordinateInput(form.lat, "lat");
  } catch (error) {
    return { error: { field: "lat", message: error instanceof WorldValidationError ? error.detail : "纬度无法识别" } };
  }
  try {
    lng = parseCoordinateInput(form.lng, "lng");
  } catch (error) {
    return { error: { field: "lng", message: error instanceof WorldValidationError ? error.detail : "经度无法识别" } };
  }
  if (inChina) return { error: { field: "lat", message: CHINA_LOCATION_MESSAGE } };
  if (!form.country) return { error: { field: "country", message: "请选择城市所在的国家或地区" } };
  if (form.country === "CN") return { error: { field: "country", message: "中国的城市请在中国视图记录，世界视图只记录海外城市" } };
  if (!codes.has(form.country)) return { error: { field: "country", message: "所选国家或地区不在目录中" } };
  for (const [i, trip] of form.trips.entries()) {
    if (!isValidTripDate(trip.date))
      return { error: { field: { trip: trip.id, part: "date" }, message: `第 ${i + 1} 次旅行的日期不是真实存在的日期（格式 YYYY-MM-DD，或留空）` } };
    if (trip.note.length > WORLD_NOTE_LIMIT)
      return { error: { field: { trip: trip.id, part: "note" }, message: `第 ${i + 1} 次旅行的记忆最多 ${WORLD_NOTE_LIMIT} 个字符` } };
  }
  return { draft: { id: form.id, name, country: form.country, lat, lng, trips: form.trips } };
}
