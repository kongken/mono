import {
  emptyWorld,
  validateWorld,
  WORLD_MAX_IMPORT_BYTES,
  WORLD_MAX_PLACES,
  WORLD_MAX_TRIPS,
  WORLD_VERSION,
  WorldValidationError,
  type WorldCodes,
  type WorldDocument,
  type WorldMark,
  type WorldPlace,
  type WorldTrip,
} from "./world.ts";

// Local storage and JSON backups of the world footprints (docs/trip-map/world-spec.md
// section 7). The rules are the ones of the China footprints (F0): read once, write only
// after an edit/import/resolution, never over text this page has not seen, and never
// lose or overwrite what cannot be read. Only keys starting with `shanhai-world-v1` are
// touched; the China keys (`shanhai-travel-*`, `shanhai-cloud-*`) are never read or written.

export const WORLD_KEY = "shanhai-world-v1";
const copyKey = (kind: "conflict" | "unreadable", now: Date) => `${WORLD_KEY}-${kind}-${now.getTime()}`;

// --- Reading ---------------------------------------------------------------

export type WorldRecovery = {
  document: WorldDocument;
  discardedCountries: number;
  discardedPlaces: number;
  discardedTrips: number;
};

export type WorldLoad =
  | { status: "ok"; document: WorldDocument; raw: string }
  /** Nothing stored (or an empty string): first use, not an error. */
  | { status: "empty"; document: WorldDocument; raw: null }
  | { status: "unavailable"; document: WorldDocument }
  /** The stored text is kept as is; saving stays paused until the user chooses. */
  | { status: "unreadable"; document: WorldDocument; raw: string; reason: string; recovery: WorldRecovery | null };

function reasonOf(error: unknown): string {
  if (error instanceof SyntaxError) return "内容不是有效的 JSON，可能已损坏或被截断";
  // The reason alone: the user is told what is wrong, not the internal path of the record (`countries[1].code`).
  if (error instanceof WorldValidationError) return error.detail;
  return error instanceof Error ? error.message : "无法识别的数据";
}

/**
 * Reads the world footprints of this device. Never writes. A text that cannot be parsed
 * or validated (including an unknown version) comes back as `unreadable` with the raw
 * string, never as an empty document that a later save would write over it.
 */
export function loadWorldLocal(storage: Pick<Storage, "getItem">, codes: WorldCodes): WorldLoad {
  let raw: string | null;
  try {
    raw = storage.getItem(WORLD_KEY);
  } catch {
    return { status: "unavailable", document: emptyWorld() };
  }
  if (raw === null || raw === "") return { status: "empty", document: emptyWorld(), raw: null };
  try {
    return { status: "ok", document: validateWorld(JSON.parse(raw), codes), raw };
  } catch (error) {
    return {
      status: "unreadable",
      document: emptyWorld(),
      raw,
      reason: reasonOf(error),
      recovery: recoverWorld(raw, codes),
    };
  }
}

/**
 * The most records (marks, cities and trips together) a recovery scans. The largest valid
 * document has 5000 cities, 10000 trips and fewer than 250 marks, so this is about twice
 * that; a damaged text with more is not worth scanning (each record costs microseconds,
 * and a crafted 2 MB text of tiny records would otherwise stall the page for seconds).
 */
export const WORLD_RECOVERY_MAX_RECORDS = 30_000;

/**
 * Salvages the recognisable records of a version 1 world document that failed strict
 * validation: valid marks, valid cities, and their valid trips. Returns null for text
 * that is not a version 1 world document at all (an unknown or newer version is never
 * salvaged), when nothing is usable, when what is usable is over a limit (nothing is
 * truncated to fit), or when the text holds more than WORLD_RECOVERY_MAX_RECORDS records
 * (checked before any record is validated; the original text stays as it is and can be
 * exported or replaced as for any unreadable text). Nothing is repaired: invalid records
 * are dropped and counted. The work is linear and bounded by that budget.
 */
export function recoverWorld(raw: string, codes: WorldCodes): WorldRecovery | null {
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return null;
  }
  const record = data as { kind?: unknown; version?: unknown; countries?: unknown; places?: unknown } | null;
  if (!record || typeof record !== "object" || record.kind !== "world" || record.version !== WORLD_VERSION) return null;
  if (!Array.isArray(record.countries) || !Array.isArray(record.places)) return null;
  let records = record.countries.length + record.places.length;
  for (const place of record.places as Record<string, unknown>[]) {
    if (Array.isArray(place?.trips)) records += place.trips.length;
    if (records > WORLD_RECOVERY_MAX_RECORDS) return null;
  }
  if (records > WORLD_RECOVERY_MAX_RECORDS) return null;
  /** The canonical form of one record put alone into a document, or null if it is not valid. */
  const alone = (countries: unknown[], places: unknown[]): WorldDocument | null => {
    try {
      return validateWorld({ kind: "world", version: WORLD_VERSION, countries, places }, codes);
    } catch {
      return null;
    }
  };

  const countries: WorldMark[] = [];
  const markCodes = new Set<unknown>();
  let discardedCountries = 0;
  for (const mark of record.countries) {
    const valid = alone([mark], [])?.countries[0];
    if (valid && !markCodes.has(valid.code)) {
      markCodes.add(valid.code);
      countries.push(valid);
    } else discardedCountries++;
  }

  const places: WorldPlace[] = [];
  const placeIds = new Set<unknown>();
  const tripIds = new Set<string>();
  let discardedPlaces = 0;
  let discardedTrips = 0;
  for (const place of record.places as Record<string, unknown>[]) {
    const trips = Array.isArray(place?.trips) ? (place.trips as unknown[]) : [];
    const bare = place && typeof place === "object" ? { ...place, trips: [] } : place;
    const kept = alone([], [bare])?.places[0];
    if (!kept || placeIds.has(kept.id)) {
      discardedPlaces++;
      discardedTrips += trips.length;
      continue;
    }
    placeIds.add(kept.id);
    const list: WorldTrip[] = [];
    for (const trip of trips) {
      const valid = alone([], [{ ...bare, trips: [trip] }])?.places[0].trips[0];
      if (!valid || tripIds.has(valid.id)) discardedTrips++;
      else {
        tripIds.add(valid.id);
        list.push(valid);
      }
    }
    places.push({ ...kept, trips: list });
    if (places.length > WORLD_MAX_PLACES || tripIds.size > WORLD_MAX_TRIPS) return null; // can never fit
  }
  if (!countries.length && !places.length) return null;
  const document = alone(countries, places);
  return document && { document, discardedCountries, discardedPlaces, discardedTrips };
}

// --- Writing ---------------------------------------------------------------

export type WorldWrite =
  | { status: "ok" }
  /** Another tab changed the stored text since this page last read or wrote it. */
  | { status: "conflict"; other: string | null }
  /** The browser refused (storage unavailable or full); the caller keeps its in-memory data. */
  | { status: "error" };

/**
 * The only way the app writes the local world footprints, and it only ever writes
 * WORLD_KEY. Before each write it checks that the stored text is still the one this page
 * last read or wrote; if not it writes nothing and reports the other text.
 */
export function createWorldWriter(storage: Pick<Storage, "getItem" | "setItem">, initialRaw: string | null) {
  let last = initialRaw === "" ? null : initialRaw;
  return {
    write(world: WorldDocument): WorldWrite {
      let current: string | null;
      try {
        current = storage.getItem(WORLD_KEY);
      } catch {
        return { status: "error" };
      }
      if (current === "") current = null;
      if (current !== last) return { status: "conflict", other: current };
      const text = JSON.stringify(world);
      try {
        storage.setItem(WORLD_KEY, text);
      } catch {
        return { status: "error" };
      }
      last = text;
      return { status: "ok" };
    },
    /** Accept what storage holds as the new baseline, e.g. after the user resolved a conflict. */
    adopt(raw: string | null) {
      last = raw === "" ? null : raw;
    },
  };
}
export type WorldWriter = ReturnType<typeof createWorldWriter>;

function keepCopy(storage: Pick<Storage, "setItem">, key: string, raw: string): boolean {
  try {
    storage.setItem(key, raw);
    return true;
  } catch {
    return false;
  }
}
/** Keeps another tab's text (`-conflict-<time>`) before it is overwritten; false if the browser refused. */
export const preserveWorldConflict = (storage: Pick<Storage, "setItem">, raw: string, now = new Date()) =>
  keepCopy(storage, copyKey("conflict", now), raw);
/** Keeps unreadable text (`-unreadable-<time>`) before recovering or starting empty; false if the browser refused. */
export const preserveWorldUnreadable = (storage: Pick<Storage, "setItem">, raw: string, now = new Date()) =>
  keepCopy(storage, copyKey("unreadable", now), raw);

export type Resolution =
  | { status: "ok"; document: WorldDocument; writer: WorldWriter }
  /** The original could not be copied: it must be exported before anything is replaced. */
  | { status: "copy-failed" };

/**
 * The user chose to recover the recognisable records or to start empty after an unreadable
 * read. The original text is copied first; only then is a writer returned, whose first
 * write replaces it. Nothing is written here.
 */
export function resolveUnreadable(
  storage: Pick<Storage, "getItem" | "setItem">,
  load: Extract<WorldLoad, { status: "unreadable" }>,
  choice: "recover" | "empty",
  now = new Date(),
): Resolution {
  if (!preserveWorldUnreadable(storage, load.raw, now)) return { status: "copy-failed" };
  const document = choice === "recover" && load.recovery ? load.recovery.document : emptyWorld();
  return { status: "ok", document, writer: createWorldWriter(storage, load.raw) };
}

/**
 * The user chose to keep this page's data over another tab's. The other text is copied
 * first; only then is it adopted as the baseline and this page's document written.
 */
export function overwriteAfterConflict(
  storage: Pick<Storage, "getItem" | "setItem">,
  writer: WorldWriter,
  world: WorldDocument,
  other: string | null,
  now = new Date(),
): WorldWrite | { status: "copy-failed" } {
  if (other !== null && !preserveWorldConflict(storage, other, now)) return { status: "copy-failed" };
  writer.adopt(other);
  return writer.write(world);
}

// --- Backup ----------------------------------------------------------------

/** Filename prefix that tells a world backup from the China backup (`山海足迹-<date>.json`). */
export const WORLD_BACKUP_PREFIX = "山海足迹-世界-";

export function exportWorldBackup(world: WorldDocument, date: string): { filename: string; text: string } {
  return { filename: `${WORLD_BACKUP_PREFIX}${date}.json`, text: `${JSON.stringify(world, null, 2)}\n` };
}

export type BackupKind = "world" | "china" | "unknown";
/** Which of the two backup formats a parsed JSON value is; the China backup has no `kind`. */
export function classifyBackup(data: unknown): BackupKind {
  if (!data || typeof data !== "object" || Array.isArray(data)) return "unknown";
  const record = data as Record<string, unknown>;
  if (record.kind === "world") return "world";
  if ((record.version === 1 || record.version === 2) && Array.isArray(record.visits) && record.provinces && typeof record.provinces === "object") return "china";
  return "unknown";
}

export type ImportDelta = {
  added: number;
  /** In both documents with different content: replaced by the imported one. */
  overwritten: number;
  unchanged: number;
  /** In the current document only: lost by the replacement. */
  discarded: number;
};
export type WorldImportPreview = {
  /** The canonical imported document; writing it replaces the current one as a whole. */
  document: WorldDocument;
  countries: ImportDelta;
  places: ImportDelta;
  trips: ImportDelta;
};

function delta<T>(current: Map<string, T>, incoming: Map<string, T>): ImportDelta {
  let added = 0;
  let overwritten = 0;
  let unchanged = 0;
  for (const [key, value] of incoming) {
    const before = current.get(key);
    if (before === undefined) added++;
    else if (JSON.stringify(before) === JSON.stringify(value)) unchanged++;
    else overwritten++;
  }
  return { added, overwritten, unchanged, discarded: [...current.keys()].filter((key) => !incoming.has(key)).length };
}
const tripMap = (world: WorldDocument) => new Map(world.places.flatMap((p) => p.trips.map((t) => [t.id, { ...t, place: p.id }] as const)));

/**
 * Validates an imported file and counts what replacing the current document with it would
 * add, overwrite and discard. It reads nothing from storage and writes nothing: the caller
 * shows the preview and, only after the user confirms, writes `preview.document`. A China
 * backup, a file over WORLD_MAX_IMPORT_BYTES and any invalid document are rejected with a reason.
 */
export function previewWorldImport(text: string, current: WorldDocument, codes: WorldCodes): WorldImportPreview {
  if (new TextEncoder().encode(text).length > WORLD_MAX_IMPORT_BYTES)
    throw new WorldValidationError("limit", "", `文件超过 ${WORLD_MAX_IMPORT_BYTES} 字节，不能导入`);
  let data: unknown;
  try {
    data = JSON.parse(text);
  } catch {
    throw new WorldValidationError("format", "", "文件不是有效的 JSON");
  }
  const kind = classifyBackup(data);
  if (kind === "china") throw new WorldValidationError("kind", "", "这是中国足迹备份，请在中国视图导入；世界视图只接受世界备份");
  if (kind === "unknown") throw new WorldValidationError("kind", "", "请选择山海足迹导出的世界备份（JSON 文件）");
  const document = validateWorld(data, codes);
  return {
    document,
    countries: delta(new Map(current.countries.map((m) => [m.code, m])), new Map(document.countries.map((m) => [m.code, m]))),
    places: delta(
      new Map(current.places.map((p) => [p.id, { ...p, trips: undefined }])),
      new Map(document.places.map((p) => [p.id, { ...p, trips: undefined }])),
    ),
    trips: delta(tripMap(current), tripMap(document)),
  };
}
