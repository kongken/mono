import type { Saved } from "./travel.ts";

export type StoredUser = {
  id: string;
  username: string;
  displayName: string;
  avatarUrl: string;
};
export type StoredSession = {
  token: string;
  /** Milliseconds since the epoch. */
  expiresAt: number;
  user: StoredUser;
};
export type CachedCloud = { saved: Saved; revision: number };

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
 * The cloud cache only speeds up startup. Anything unreadable is dropped and
 * refetched, since the server holds the truth.
 */
export function loadCloudCache(
  storage: Reader & Remover,
  userId: string,
  validate: (data: unknown) => Saved,
): CachedCloud | null {
  try {
    const raw = storage.getItem(cacheKey(userId));
    if (!raw) return null;
    const data = JSON.parse(raw) as { saved?: unknown; revision?: unknown };
    if (typeof data.revision !== "number") throw new Error("bad revision");
    return { saved: validate(data.saved), revision: data.revision };
  } catch {
    try {
      storage.removeItem(cacheKey(userId));
    } catch {
      // Nothing more to do; the next fetch replaces the cache.
    }
    return null;
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

/** Removes every cached cloud copy except the one for `keepUserId`. */
export function clearOtherCaches(
  storage: Enumerable & Remover,
  keepUserId?: string,
) {
  for (const key of cacheKeys(storage))
    if (key !== (keepUserId ? cacheKey(keepUserId) : "")) storage.removeItem(key);
}

/**
 * Sign-out cleanup: the session and all account caches. Footprints saved in
 * local mode before signing in are not account data and are left alone.
 */
export function clearAccountData(storage: Enumerable & Remover) {
  clearOtherCaches(storage);
  storage.removeItem(SESSION_KEY);
}
