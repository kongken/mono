import regions from "../regions.json" with { type: "json" };
import type { Saved } from "./travel.ts";
import { diffSaved, applyOp, type PendingBatch } from "./sync.ts";

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
export type CachedCloud = {
  saved: Saved;
  revision: number;
  confirmed?: Saved;
  pendingBatch?: PendingBatch;
};

type Reader = Pick<Storage, "getItem">;
type Writer = Pick<Storage, "setItem">;
type Remover = Pick<Storage, "removeItem">;
type Enumerable = Pick<Storage, "length" | "key">;

export const SESSION_KEY = "shanhai-session-v1";
export const CACHE_PREFIX = "shanhai-cloud-v1:";
export const cacheKey = (userId: string) => CACHE_PREFIX + userId;

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

/**
 * Old {saved, revision} caches remain readable. New caches also keep the
 * confirmed ancestor so unsent edits can be rebased after a reload.
 * Never discard unreadable data: it may contain the only copy of offline edits.
 */
export function loadCloudCache(
  storage: Reader & Remover,
  userId: string,
  validate: (data: unknown) => Saved,
): CachedCloud | null {
  try {
    const raw = storage.getItem(cacheKey(userId));
    if (!raw) return null;
    const data = JSON.parse(raw) as {
      saved?: unknown;
      revision?: unknown;
      confirmed?: unknown;
      pendingBatch?: PendingBatch;
    };
    if (!Number.isSafeInteger(data.revision) || (data.revision as number) < 0)
      throw new Error("bad revision");
    if (data.pendingBatch) {
      const batch = data.pendingBatch;
      if (
        typeof batch.id !== "string" ||
        !/^[A-Za-z0-9_-]{1,128}$/.test(batch.id) ||
        batch.revision !== data.revision ||
        !Array.isArray(batch.ops) ||
        batch.ops.length < 1 ||
        batch.ops.length > 100
      )
        throw new Error("bad pending batch");
      let result = validate(data.confirmed);
      for (const op of batch.ops) {
        if (
          !["upsertVisit", "deleteVisit", "upsertMark", "deleteMark"].includes(
            op.type,
          )
        )
          throw new Error("bad mutation");
        if (
          op.type === "deleteVisit" &&
          !regions.some((p) => p.cities.some((c) => c.code === op.code))
        )
          throw new Error("unknown deleted city");
        if (
          op.type === "deleteMark" &&
          !regions.some((p) => p.code === op.province)
        )
          throw new Error("unknown deleted province");
        result = applyOp(result, op);
      }
      validate(result);
    }
    return {
      ...(data.pendingBatch ? { pendingBatch: data.pendingBatch } : {}),
      saved: validate(data.saved),
      revision: data.revision as number,
      ...(data.confirmed === undefined
        ? {}
        : { confirmed: validate(data.confirmed) }),
    };
  } catch {
    throw new Error(
      "账号缓存无法读取，已保留原始数据。请先导出原始缓存，不要清除网站存储。",
    );
  }
}

export function saveCloudCache(
  storage: Writer,
  userId: string,
  cache: CachedCloud,
): boolean {
  try {
    storage.setItem(cacheKey(userId), JSON.stringify(cache));
    return true;
  } catch {
    return false;
  }
}

function cacheKeys(storage: Enumerable) {
  const keys: string[] = [];
  for (let i = 0; i < storage.length; i++) {
    const key = storage.key(i);
    if (key?.startsWith(CACHE_PREFIX)) keys.push(key);
  }
  return keys;
}

/** Clean copies may be discarded; another account's unsent/invalid data may not. */
export function clearOtherCaches(
  storage: Enumerable & Remover & Reader,
  keepUserId?: string,
  validate?: (data: unknown) => Saved,
) {
  for (const key of cacheKeys(storage)) {
    if (key === (keepUserId ? cacheKey(keepUserId) : "")) continue;
    try {
      const cache = JSON.parse(storage.getItem(key) ?? "null") as CachedCloud;
      if (!cache) continue;
      if (validate) {
        validate(cache.saved);
        if (cache.confirmed) validate(cache.confirmed);
      }
      if (
        cache.pendingBatch ||
        (cache.confirmed && diffSaved(cache.confirmed, cache.saved).length)
      )
        continue;
      storage.removeItem(key);
    } catch {
      /* Preserve unrecognisable data for its owner's recovery. */
    }
  }
}

/**
 * Sign-out cleanup: the session and explicitly discarded account cache;
 * other accounts' pending/unreadable caches stay intact. Footprints saved in
 * local mode before signing in are not account data and are left alone.
 */
export function clearAccountData(
  storage: Enumerable & Remover & Reader,
  currentUserId?: string,
  validate?: (data: unknown) => Saved,
) {
  if (currentUserId) storage.removeItem(cacheKey(currentUserId));
  clearOtherCaches(storage, undefined, validate);
  storage.removeItem(SESSION_KEY);
}
