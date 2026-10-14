import regions from "../regions.json" with { type: "json" };
import type { Saved } from "./travel.ts";
import { diffSaved, applyOp, MAX_BATCH_OPS, type Op, type PendingBatch } from "./sync.ts";
import { TRIP_ID_PATTERN, TRIP_NOTE_LIMIT, isValidTripDate } from "./trips.ts";
import { digestText, preserveRegionOriginal, regionOriginalPrefix } from "./persistence.ts";
import { regionCatalog, REGION_VERSION, stampRegions } from "./region-catalog.ts";
import { projectRegionOps } from "./region-operations.ts";

export type StoredUser = {
  id: string;
  username: string;
  displayName: string;
  avatarUrl: string;
  deletionPending?: boolean;
};
export type StoredSession = {
  token: string;
  /** Milliseconds since the epoch. */
  expiresAt: number;
  user: StoredUser;
};
/** The cache layout this version writes; caches without the field are version 1 (one date and note per city). */
export const CACHE_SCHEMA = 2;
export type CachedCloud = {
  schema?: typeof CACHE_SCHEMA;
  saved: Saved;
  revision: number;
  confirmed?: Saved;
  pendingBatch?: PendingBatch;
  /** The unsent change must reach the server as one batch (see SyncCache). */
  atomic?: true;
};
/** Data an older page wrote to its own cache key after this version took the account over. */
export type LegacyCacheChange = { raw: string; saved: Saved | null };
/**
 * A loaded cache. `legacyDigest` is what the legacy key held when read (recorded on
 * the first save); `legacyChange` is set when the legacy key holds changes that
 * this version has not accounted for.
 */
export type LoadedCloud = CachedCloud & {
  legacyDigest: string;
  legacyChange?: LegacyCacheChange;
};

type Reader = Pick<Storage, "getItem">;
type Writer = Pick<Storage, "setItem"> & Partial<Pick<Storage, "removeItem">>;
type Remover = Pick<Storage, "removeItem">;
type Enumerable = Pick<Storage, "length" | "key">;

export const SESSION_KEY = "shanhai-session-v1";
/** The canonical account cache of this version; only this version writes it. */
export const CACHE_PREFIX = "shanhai-cloud-v2:";
export const cacheKey = (userId: string) => CACHE_PREFIX + userId;
/**
 * The cache key earlier versions wrote. Pages of those versions may still be open,
 * so this version only reads it (to take over an unsent batch with its request ID)
 * and never writes it.
 */
export const LEGACY_CACHE_PREFIX = "shanhai-cloud-v1:";
export const legacyCacheKey = (userId: string) => LEGACY_CACHE_PREFIX + userId;
/** Digest of the legacy cache this version has already accounted for. Must not start with CACHE_PREFIX. */
export const LEGACY_CACHE_SEEN_PREFIX = "shanhai-cloud-v2-legacy-seen:";
export const legacyCacheSeenKey = (userId: string) => LEGACY_CACHE_SEEN_PREFIX + userId;

const isString = (v: unknown): v is string => typeof v === "string";

/** Returns the stored session, or null if absent, malformed or expired. */
export function loadSession(
  storage: Reader & Remover,
  now = Date.now(),
): StoredSession | null {
  try {
    const raw = storage.getItem(SESSION_KEY);
    if (!raw) return null;
    const data = JSON.parse(raw) as Partial<StoredSession>;
    const user = data.user as Partial<StoredUser> | undefined;
    if (
      !isString(data.token) ||
      !data.token ||
      typeof data.expiresAt !== "number" ||
      data.expiresAt <= now ||
      !user ||
      !isString(user.id) ||
      !user.id
    ) {
      storage.removeItem(SESSION_KEY);
      return null;
    }
    return {
      token: data.token,
      expiresAt: data.expiresAt,
      user: {
        id: user.id,
        username: isString(user.username) ? user.username : "",
        displayName: isString(user.displayName) ? user.displayName : "",
        avatarUrl: isString(user.avatarUrl) ? user.avatarUrl : "",
        ...(user.deletionPending === true ? { deletionPending: true } : {}),
      },
    };
  } catch {
    return null;
  }
}

export function saveSession(storage: Writer, session: StoredSession): boolean {
  try {
    storage.setItem(SESSION_KEY, JSON.stringify(session));
    return true;
  } catch {
    return false;
  }
}

const MUTATION_TYPES = ["upsertVisit", "deleteVisit", "upsertTrip", "deleteTrip", "upsertMark", "deleteMark", "legacyUpsertVisit"];
const isRecord = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const knownCity = (code: unknown) => typeof code === "string" && !!regionCatalog.code(code);

/**
 * A batch saved by a version before several trips per city holds schema 1
 * operations: an upsertVisit carried the date and note. It is replayed as the
 * same request, so its receipt on the server still identifies it.
 */
function legacyOps(ops: Op[]): Op[] {
  return ops.map((op) => {
    const o = op as { type?: unknown; visit?: unknown };
    return o.type === "upsertVisit" && isRecord(o.visit)
      ? ({ type: "legacyUpsertVisit", visit: o.visit } as unknown as Op)
      : op;
  });
}

function checkOp(op: Op) {
  if (!isRecord(op) || !MUTATION_TYPES.includes(op.type)) throw new Error("bad mutation");
  switch (op.type) {
    case "deleteVisit":
      if (!knownCity(op.code)) throw new Error("unknown deleted city");
      break;
    case "upsertTrip":
      if (
        !knownCity(op.code) ||
        !isRecord(op.trip) ||
        typeof op.trip.id !== "string" ||
        !TRIP_ID_PATTERN.test(op.trip.id) ||
        !isValidTripDate(op.trip.date) ||
        typeof op.trip.note !== "string" ||
        op.trip.note.length > TRIP_NOTE_LIMIT
      )
        throw new Error("bad trip");
      break;
    case "deleteTrip":
      if (!knownCity(op.code) || typeof op.id !== "string" || !TRIP_ID_PATTERN.test(op.id))
        throw new Error("bad deleted trip");
      break;
    case "deleteMark":
      if (!regions.some((p) => p.code === op.province)) throw new Error("unknown deleted province");
      break;
  }
}

type ParsedCache = Omit<CachedCloud, "schema"> & { schema: typeof CACHE_SCHEMA };

/**
 * Validates one stored cache text. A text without a schema field is a version 1
 * cache: its footprints convert to one trip per city and its unsent batch is kept
 * as it was written (same ID, same operations on the wire).
 */
function parseCache(raw: string, validate: (data: unknown) => Saved): ParsedCache {
  const data = JSON.parse(raw) as {
    schema?: unknown;
    regionVersion?: unknown;
    saved?: unknown;
    revision?: unknown;
    confirmed?: unknown;
    pendingBatch?: PendingBatch;
    atomic?: unknown;
  };
  regionCatalog.assertVersion(data.regionVersion);
  regionCatalog.assertVersion((data.saved as { regionVersion?: unknown } | null)?.regionVersion);
  regionCatalog.assertVersion((data.confirmed as { regionVersion?: unknown } | null)?.regionVersion);
  const legacy = data.schema === undefined;
  if (data.atomic !== undefined && (legacy || data.atomic !== true)) throw new Error("bad atomic flag");
  if (!legacy && data.schema !== CACHE_SCHEMA) throw new Error("unknown cache schema");
  if (!Number.isSafeInteger(data.revision) || (data.revision as number) < 0)
    throw new Error("bad revision");
  let pendingBatch: PendingBatch | undefined;
  if (data.pendingBatch !== undefined) {
    const batch = data.pendingBatch;
    if (
      typeof batch.id !== "string" ||
      !/^[A-Za-z0-9_-]{1,128}$/.test(batch.id) ||
      batch.revision !== data.revision ||
      !Array.isArray(batch.ops) ||
      batch.ops.length < 1 ||
      batch.ops.length > MAX_BATCH_OPS
    )
      throw new Error("bad pending batch");
    const ops = legacy ? legacyOps(batch.ops) : batch.ops;
    let result = validate(data.confirmed);
    for (const op of ops) {
      checkOp(op);
      for (const projected of projectRegionOps([op])) result = applyOp(result, projected);
    }
    validate(result);
    pendingBatch = { id: batch.id, revision: batch.revision, ops };
  }
  const saved = validate(data.saved);
  const confirmed = data.confirmed === undefined ? undefined : validate(data.confirmed);
  return {
    schema: CACHE_SCHEMA,
    ...(pendingBatch ? { pendingBatch } : {}),
    saved,
    revision: data.revision as number,
    ...(confirmed ? { confirmed } : {}),
    ...(data.atomic === true ? { atomic: true as const } : {}),
  };
}

/**
 * Reads the account's cached copy. The canonical key of this version wins; when
 * it does not exist yet, the legacy key is read and left exactly as it is, so
 * a version 1 cache converts without its original bytes ever being rewritten,
 * and its unsent batch keeps its ID and operations. When both exist and the
 * legacy key changed since this version last accounted for it, the change is
 * reported (`legacyChange`) rather than merged or dropped silently.
 * Never discard unreadable data: it may contain the only copy of offline edits.
 */
export function loadCloudCache(
  storage: Reader & Remover,
  userId: string,
  validate: (data: unknown) => Saved,
): LoadedCloud | null {
  try {
    const canonical = storage.getItem(cacheKey(userId));
    const legacyRaw = storage.getItem(legacyCacheKey(userId));
    const legacy = legacyRaw === "" ? null : legacyRaw;
    const legacyDigest = digestText(legacy);
    if (canonical) {
      const cache = parseCache(canonical, validate);
      const seen = storage.getItem(legacyCacheSeenKey(userId));
      if (legacy !== null && legacyDigest !== seen) {
        let saved: Saved | null = null;
        try {
          saved = parseCache(legacy, validate).saved;
        } catch {
          /* Unreadable legacy data is offered for export only. */
        }
        return { ...cache, legacyDigest, legacyChange: { raw: legacy, saved } };
      }
      return { ...cache, legacyDigest };
    }
    if (!legacy) return null;
    return { ...parseCache(legacy, validate), legacyDigest };
  } catch {
    throw new Error(
      "账号缓存无法读取，已保留原始数据。请先导出原始缓存，不要清除网站存储。",
    );
  }
}

/** Re-reads another page's change to the legacy cache; null when there is nothing new. */
export function readLegacyCacheChange(
  storage: Reader,
  userId: string,
  validate: (data: unknown) => Saved,
): (LegacyCacheChange & { digest: string }) | null {
  try {
    const raw = storage.getItem(legacyCacheKey(userId));
    if (!raw) return null;
    const digest = digestText(raw);
    if (digest === storage.getItem(legacyCacheSeenKey(userId))) return null;
    let saved: Saved | null = null;
    try {
      saved = parseCache(raw, validate).saved;
    } catch {
      /* export only */
    }
    return { raw, digest, saved };
  } catch {
    return null;
  }
}

export function acknowledgeLegacyCache(storage: Writer, userId: string, digest: string): boolean {
  try {
    storage.setItem(legacyCacheSeenKey(userId), digest);
    return true;
  } catch {
    return false;
  }
}

/**
 * Writes the canonical cache. The first write also records the digest of the
 * legacy key that was taken over, before the canonical text exists, so a failed
 * write can never leave the legacy key looking unaccounted for.
 */
export function saveCloudCache(
  storage: Writer & Partial<Reader>,
  userId: string,
  cache: CachedCloud,
  legacyDigest?: string,
): boolean {
  try {
    if (storage.getItem && storage.getItem(legacyCacheSeenKey(userId)) === null)
      storage.setItem(legacyCacheSeenKey(userId), legacyDigest ?? "none");
    const old = storage.getItem?.(cacheKey(userId));
    if (old && storage.getItem && !preserveRegionOriginal(storage as Reader & Writer, cacheKey(userId), old)) return false;
    storage.setItem(cacheKey(userId), JSON.stringify({ ...cache, regionVersion: REGION_VERSION, schema: CACHE_SCHEMA, saved: stampRegions(cache.saved), ...(cache.confirmed ? { confirmed: stampRegions(cache.confirmed) } : {}) }));
    return true;
  } catch {
    return false;
  }
}

function cacheKeys(storage: Enumerable) {
  const keys: { key: string; userId: string }[] = [];
  for (let i = 0; i < storage.length; i++) {
    const key = storage.key(i);
    if (key?.startsWith(CACHE_PREFIX)) keys.push({ key, userId: key.slice(CACHE_PREFIX.length) });
    else if (key?.startsWith(LEGACY_CACHE_PREFIX)) keys.push({ key, userId: key.slice(LEGACY_CACHE_PREFIX.length) });
  }
  return keys;
}

/** True when a stored cache holds nothing that is not on the server yet; throws if unreadable. */
function isClean(raw: string | null, validate?: (data: unknown) => Saved): boolean {
  const cache = JSON.parse(raw ?? "null") as CachedCloud | null;
  if (!cache) return true;
  // Validation also converts a version 1 cache, so both layouts compare alike.
  const saved = validate ? validate(cache.saved) : cache.saved;
  const confirmed = cache.confirmed && validate ? validate(cache.confirmed) : cache.confirmed;
  return !(Object.hasOwn(cache, "pendingBatch") || (confirmed && diffSaved(confirmed, saved).length));
}

/** True when this version took the legacy cache over in exactly this state, so the canonical cache holds its work. */
const accounted = (storage: Reader, userId: string, raw: string | null) =>
  raw !== null && digestText(raw) === storage.getItem(legacyCacheSeenKey(userId));

/**
 * Clean copies may be discarded; another account's unsent/invalid data may not.
 * Of a user's two keys each is judged on its own, so a legacy cache that still
 * holds unsent work survives while the canonical one is cleared.
 */
export function clearOtherCaches(
  storage: Enumerable & Remover & Reader,
  keepUserId?: string,
  validate?: (data: unknown) => Saved,
) {
  for (const { key, userId } of cacheKeys(storage)) {
    if (userId === keepUserId) continue;
    try {
      const raw = storage.getItem(key);
      const taken = key.startsWith(LEGACY_CACHE_PREFIX) && accounted(storage, userId, raw);
      if (!taken && !isClean(raw, validate)) continue;
      storage.removeItem(key);
      if (key.startsWith(CACHE_PREFIX)) {
        storage.removeItem(legacyCacheSeenKey(userId));
        storage.removeItem(cloudItinerariesKey(userId));
      }
    } catch {
      /* Preserve unrecognisable data for its owner's recovery. */
    }
  }
}

/**
 * Sign-out cleanup: the session and explicitly discarded account cache;
 * other accounts' pending/unreadable caches stay intact. Footprints saved in
 * local mode before signing in are not account data and are left alone. The
 * legacy cache of the account is removed only when this version took it over in
 * exactly its current state or it holds nothing unsent; changes an older page made
 * since stay in place. After a confirmed account deletion (`accountDeleted`) all of
 * the account's own keys are removed instead.
 */
export function clearAccountData(
  storage: Enumerable & Remover & Reader,
  currentUserId?: string,
  validate?: (data: unknown) => Saved,
  /** The account was deleted: its unsent or unreadable leftovers are discarded too. */
  accountDeleted = false,
) {
  if (currentUserId && accountDeleted) {
    // The user confirmed deleting the account together with everything unsent, so
    // every key it owns goes, whatever an older page changed or made unreadable.
    // Local-mode footprints and other accounts' caches are not its data.
    for (const key of [
      cacheKey(currentUserId),
      legacyCacheKey(currentUserId),
      legacyCacheSeenKey(currentUserId),
      migratedKey(currentUserId),
      cloudItinerariesKey(currentUserId),
    ])
      storage.removeItem(key);
  } else if (currentUserId) {
    storage.removeItem(cacheKey(currentUserId));
    storage.removeItem(cloudItinerariesKey(currentUserId));
    try {
      const raw = storage.getItem(legacyCacheKey(currentUserId));
      if (accounted(storage, currentUserId, raw) || isClean(raw, validate))
        storage.removeItem(legacyCacheKey(currentUserId));
    } catch {
      /* Keep what cannot be read. */
    }
    storage.removeItem(legacyCacheSeenKey(currentUserId));
  }
  if (currentUserId) {
    const prefixes = [regionOriginalPrefix(cacheKey(currentUserId)), regionOriginalPrefix(legacyCacheKey(currentUserId))];
    const originals = Array.from({ length: storage.length }, (_, i) => storage.key(i)).filter((key): key is string => !!key && prefixes.some(prefix => key.startsWith(prefix)));
    for (const key of originals) storage.removeItem(key);
  }
  clearOtherCaches(storage, undefined, validate);
  storage.removeItem(SESSION_KEY);
}

export const MIGRATED_PREFIX = "shanhai-migrated-v1:";
export const migratedKey = (userId: string) => MIGRATED_PREFIX + userId;
export const CLOUD_ITINERARIES_PREFIX = "shanhai-cloud-itineraries-v1:";
export const cloudItinerariesKey = (userId: string) => CLOUD_ITINERARIES_PREFIX + userId;

/**
 * Remembers which local data was already migrated for an account, so the
 * migration prompt is not repeated. Local footprints stay on the device, and
 * this record survives sign-out; only changed local data is offered again.
 */
export function isMigrated(
  storage: Pick<Storage, "getItem">,
  userId: string,
  fingerprint: string,
): boolean {
  try {
    return storage.getItem(migratedKey(userId)) === fingerprint;
  } catch {
    return false;
  }
}
export function markMigrated(
  storage: Pick<Storage, "setItem">,
  userId: string,
  fingerprint: string,
): boolean {
  try {
    storage.setItem(migratedKey(userId), fingerprint);
    return true;
  } catch {
    return false;
  }
}
