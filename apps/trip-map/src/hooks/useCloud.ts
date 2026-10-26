import { useCallback, useEffect, useRef, useState } from "react";
import { requireAccount } from "@/api/auth";
import { detachThisDevice } from "@/api/notification";
import { createFootprintApi } from "@/api/footprints";
import { backendEnabled, setTransportAccount } from "@/api/transport";
import { refreshCSRF } from "@/api/csrf";
import { describeApiError, isUnauthenticated } from "@/lib/errors";
import {
  acknowledgeLegacyCache,
  clearAccountData,
  clearOtherCaches,
  loadCloudCache,
  loadSession,
  readLegacyCacheChange,
  saveCloudCache,
  saveSession,
  type CachedCloud,
  type LoadedCloud,
  type StoredSession,
  cacheKey,
  legacyCacheKey,
} from "@/lib/session";
import {
  SyncEngine,
  diffSaved,
  type Op,
  type CloudSnapshot,
  type SyncStatus,
} from "@/lib/sync";
import type { Saved } from "@/lib/travel";

export type CloudBoot = {
  session: StoredSession | null;
  cache: LoadedCloud | null;
  error?: string;
};

/** Unsynced data an older page left in its own cache key; offered to the user, never dropped or merged silently. */
export type LegacyCacheNotice = { raw: string; saved: Saved | null; digest: string };

/** Reads the stored session and its cached copy; both are synchronous. */
export function readBoot(validate: (data: unknown) => Saved): CloudBoot {
  if (!backendEnabled) return { session: null, cache: null };
  const session = loadSession(localStorage);
  setTransportAccount(session?.user.id);
  try {
    return {
      session,
      cache: session
        ? loadCloudCache(localStorage, session.user.id, validate)
        : null,
    };
  } catch (error) {
    return {
      session,
      cache: null,
      error: error instanceof Error ? error.message : "账号缓存无法读取",
    };
  }
}

type Options = {
  boot: CloudBoot;
  validate: (data: unknown) => Saved;
  /** The signed-in account changed: data is the new account's (or null for local mode). */
  onAccountChange: (data: Saved | null) => void;
  /** Fresh server data to show for the current account. */
  onCloudData: (data: Saved) => void;
  notify: (message: string) => void;
};

const REFRESH_MIN_MS = 15_000;
/** Me is asked at most this often by focus and visibility events (the start always asks). */
const ME_MIN_MS = 5_000;
const SESSION_EXPIRED_NOTICE =
  "登录已失效。请前往 OzID 重新登录；未同步修改仍保留在本机，也可先导出备份";
const SERVER_DATA_ERROR = "云端数据格式无法识别";
const emptySaved: Saved = { version: 2, visits: [], provinces: {} };

export function useCloud(options: Options) {
  const opts = useRef(options);
  opts.current = options;

  const [session, setSessionState] = useState<StoredSession | null>(
    options.boot.session,
  );
  const sessionRef = useRef(session);
  const [hasData, setHasData] = useState(
    !!options.boot.cache || !!options.boot.session?.user.deletionPending,
  );
  const [deletionBlocked, setDeletionBlocked] = useState(
    !!options.boot.session?.user.deletionPending,
  );
  // The session cookie no longer works (expired, revoked, password changed). The cache and
  // its unsynced edits stay; only signing in again (or exporting them) goes on from here.
  const [sessionExpired, setSessionExpiredState] = useState(false);
  const sessionExpiredRef = useRef(false);
  const setSessionExpired = (expired: boolean) => {
    sessionExpiredRef.current = expired;
    setSessionExpiredState(expired);
  };
  const [loaded, setLoaded] = useState<"pending" | "ok" | "failed">("pending");
  const [loadError, setLoadError] = useState(options.boot.error ?? "");
  const protectedCache = useRef(!!options.boot.error);
  const [lockedCache, setLockedCache] = useState(!!options.boot.error);
  const [cacheFailed, setCacheFailed] = useState(false);
  const [legacyNotice, setLegacyNotice] = useState<LegacyCacheNotice | null>(
    options.boot.cache?.legacyChange
      ? { ...options.boot.cache.legacyChange, digest: options.boot.cache.legacyDigest }
      : null,
  );
  // The legacy cache this page took over; recorded with the first canonical write.
  const legacyDigest = useRef<string | undefined>(options.boot.cache?.legacyDigest);
  const [conflictRecords, setConflictRecords] = useState<Op[]>([]);
  const [pending, setPending] = useState(false);
  const [sync, setSync] = useState<SyncStatus>("idle");
  // True once this session has confirmed the cloud state, not just the cache.
  const [fresh, setFresh] = useState(false);
  const sawSync = useRef(false);
  const [busy, setBusy] = useState("");
  const engineRef = useRef<SyncEngine | null>(null);
  const editSeq = useRef(0);
  const lastRefresh = useRef(0);
  const restoreStarted = useRef(false);

  const setSession = (next: StoredSession | null) => {
    setTransportAccount(next?.user.id);
    sessionRef.current = next;
    setSessionState(next);
  };

  const leaveAccount = useCallback((message: string, accountDeleted = false) => {
    try {
      clearAccountData(
        localStorage,
        sessionRef.current?.user.id,
        opts.current.validate,
        accountDeleted,
      );
    } catch {
      // The in-memory state below is cleared regardless.
    }
    engineRef.current?.stop();
    engineRef.current = null;
    protectedCache.current = false;
    setLockedCache(false);
    setCacheFailed(false);
    setLegacyNotice(null);
    legacyDigest.current = undefined;
    setConflictRecords([]);
    setPending(false);
    setDeletionBlocked(false);
    setSessionExpired(false);
    setTransportAccount();
    sessionRef.current = null;
    setSessionState(null);
    setLoaded("pending");
    setLoadError("");
    setHasData(false);
    setSync("idle");
    setFresh(false);
    sawSync.current = false;
    opts.current.onAccountChange(null);
    if (message) opts.current.notify(message);
  }, []);

  const adopt = (
    s: StoredSession,
    snapshot: CloudSnapshot,
    desired = snapshot.saved,
    pendingBatch?: CachedCloud["pendingBatch"],
    atomic = false,
  ) => {
    engineRef.current?.stop();
    const scopedApi = createFootprintApi(snapshot, s.user.id);
    const api = {
      ...scopedApi,
      get: async () => {
        const result = await scopedApi.get();
        return {
          revision: result.revision,
          saved: opts.current.validate(result.saved),
        };
      },
    };
    engineRef.current = new SyncEngine(
      api,
      snapshot,
      {
        onStatus: (status) => {
          if (sessionRef.current?.user.id !== s.user.id) return;
          setSync(status);
          if (status === "syncing") sawSync.current = true;
          else if (status === "idle" && sawSync.current) setFresh(true);
          setConflictRecords(engineRef.current?.conflicts ?? []);
          setPending(engineRef.current?.pending ?? false);
          if (status === "idle") {
            // A completed sync proves the session works again.
            setSessionExpired(false);
            setLoaded("ok");
            setLoadError("");
          }
        },
        onChange: (cache) => {
          if (sessionRef.current?.user.id !== s.user.id) return;
          setCacheFailed(
            !saveCloudCache(localStorage, s.user.id, cache, legacyDigest.current),
          );
          setPending(
            !!cache.pendingBatch ||
              diffSaved(cache.confirmed, cache.saved).length > 0,
          );
          opts.current.onCloudData(cache.saved);
        },
        onBeforeSend: (cache) => {
          if (
            sessionRef.current?.user.id !== s.user.id ||
            !saveCloudCache(localStorage, s.user.id, cache, legacyDigest.current)
          ) {
            setCacheFailed(true);
            throw new Error("批量请求无法保存到本机，已暂停同步，请先导出备份");
          }
          setCacheFailed(false);
        },
        onError: (error) => {
          if (sessionRef.current?.user.id !== s.user.id) return;
          if (isUnauthenticated(error)) {
            // Do not clear unsent edits automatically when a session expires.
            setSessionExpired(true);
            setLoadError(SESSION_EXPIRED_NOTICE);
          } else {
            setLoadError(describeApiError(error));
          }
          setLoaded("failed");
          opts.current.notify("同步未完成，修改已保留在本机，可重试或导出备份");
        },
      },
      desired,
      pendingBatch,
      atomic,
    );
    opts.current.onCloudData(desired);
    setHasData(true);
    setLoaded("ok");
    setLoadError("");
  };

  const refresh = useCallback(async (force = false) => {
    const s = sessionRef.current;
    if (!s) return;
    if (s.user.deletionPending) {
      setLoaded("failed");
      setLoadError("云端数据删除未完成，请前往 OzID 重试注销");
      return;
    }
    if (protectedCache.current) {
      setLoaded("failed");
      return;
    }
    const now = Date.now();
    if (!force && now - lastRefresh.current < REFRESH_MIN_MS) return;
    lastRefresh.current = now;
    const engine = engineRef.current;
    if (engine) {
      engine.retry();
      return;
    }
    const seq = editSeq.current;
    let fetched: CloudSnapshot;
    try {
      fetched = await createFootprintApi(undefined, s.user.id).get();
    } catch (error) {
      if (sessionRef.current?.user.id !== s.user.id) return;
      if (isUnauthenticated(error)) {
        setSessionExpired(true);
        setLoadError(SESSION_EXPIRED_NOTICE);
        setLoaded("failed");
        return;
      }
      setLoaded("failed");
      setLoadError(describeApiError(error));
      return;
    }
    let snapshot: CloudSnapshot;
    try {
      snapshot = {
        revision: fetched.revision,
        saved: opts.current.validate(fetched.saved),
      };
    } catch {
      if (sessionRef.current?.user.id === s.user.id) {
        setLoaded("failed");
        setLoadError(SERVER_DATA_ERROR);
      }
      return;
    }
    // Drop the result if the account changed or an edit happened meanwhile.
    if (sessionRef.current?.user.id !== s.user.id || editSeq.current !== seq)
      return;
    adopt(s, snapshot);
    setFresh(true);
    engineRef.current?.retry();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const enter = (s: StoredSession) => {
    saveSession(localStorage, s);
    try {
      clearOtherCaches(localStorage, s.user.id, opts.current.validate);
    } catch {
      // A stale cache of another account is only an optimisation lost.
    }
    let cache: LoadedCloud | null = null;
    try {
      cache = loadCloudCache(localStorage, s.user.id, opts.current.validate);
      protectedCache.current = false;
      setLockedCache(false);
      legacyDigest.current = cache?.legacyDigest;
      setLegacyNotice(
        cache?.legacyChange
          ? { ...cache.legacyChange, digest: cache.legacyDigest }
          : null,
      );
    } catch (error) {
      protectedCache.current = true;
      setLockedCache(true);
      opts.current.notify(
        error instanceof Error ? error.message : "账号缓存无法读取",
      );
    }
    engineRef.current?.stop();
    engineRef.current = null;
    setSession(s);
    setSessionExpired(false);
    setDeletionBlocked(!!s.user.deletionPending);
    if (s.user.deletionPending) {
      setHasData(true);
      opts.current.onAccountChange(emptySaved);
      setLoaded("failed");
      setLoadError("云端数据删除未完成，请前往 OzID 重试注销");
      return;
    }
    setLoaded("pending");
    setLoadError("");
    setSync("idle");
    setFresh(false);
    sawSync.current = false;
    setHasData(!!cache);
    opts.current.onAccountChange(cache?.saved ?? emptySaved);
    lastRefresh.current = 0;
    if (cache) {
      adopt(
        s,
        { saved: cache.confirmed ?? cache.saved, revision: cache.revision },
        cache.saved,
        cache.pendingBatch,
        cache.atomic === true,
      );
    }
    void refresh(true);
  };

  // First load for a restored session.
  useEffect(() => {
    const s = sessionRef.current;
    const cache = opts.current.boot.cache;
    if (backendEnabled) void refreshCSRF().catch(() => {});
    if (s?.user.deletionPending) {
      setHasData(true);
      setLoaded("failed");
      setLoadError("云端数据删除未完成，请前往 OzID 重试注销");
      return;
    }
    if (s && cache) {
      adopt(
        s,
        { saved: cache.confirmed ?? cache.saved, revision: cache.revision },
        cache.saved,
        cache.pendingBatch,
        cache.atomic === true,
      );
    }
    if (s) void refresh(true);
    return () => {
      engineRef.current?.stop();
      engineRef.current = null;
    };
  }, [refresh]);

  // Pick up changes from other devices when the page comes back.
  useEffect(() => {
    if (!session) return;
    const onVisible = () => {
      if (document.visibilityState === "visible") void refresh();
    };
    const onOnline = () => void refresh(true);
    const onStorage = (event: StorageEvent) => {
      if (event.key === legacyCacheKey(session.user.id) || event.key === null) {
        // An older page wrote its own key: the canonical cache is untouched, so
        // editing continues; the change is offered to the user.
        const change = readLegacyCacheChange(
          localStorage,
          session.user.id,
          opts.current.validate,
        );
        if (change) setLegacyNotice(change);
        if (event.key !== null) return;
      }
      if (
        event.key !== cacheKey(session.user.id) &&
        event.key !== "shanhai-session-v1"
      )
        return;
      engineRef.current?.stop();
      protectedCache.current = true;
      setLockedCache(true);
      setLoaded("failed");
      setCacheFailed(true);
      setLoadError(
        "另一个页面修改了账号数据，请先导出本页备份再刷新；暂停编辑以避免覆盖",
      );
    };
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("online", onOnline);
    window.addEventListener("storage", onStorage);
    return () => {
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("online", onOnline);
      window.removeEventListener("storage", onStorage);
    };
  }, [session, refresh]);

  // Ask Me who is signed in: the HttpOnly session Cookie is authoritative, never a stored name or an URL. At
  // start, when the page becomes visible or focused again and when the network is back, so that a sign-in,
  // sign-out, password change or account switch made in OzID (another origin, no shared events) is noticed.
  // Repeated asks are coalesced. Only a real "not signed in" answer ends the session; a network or server
  // failure is not a sign-out and changes nothing. In every case the cache stays: a different account's
  // sign-in switches to that account's own cache, and the previous one's unsent edits are kept for it.
  const meInFlight = useRef<Promise<void> | null>(null);
  const lastMe = useRef(0);
  const verifySession = useCallback((force = false): Promise<void> => {
    if (!backendEnabled) return Promise.resolve();
    if (meInFlight.current) return meInFlight.current;
    if (!force && Date.now() - lastMe.current < ME_MIN_MS) return Promise.resolve();
    lastMe.current = Date.now();
    const startedAs = sessionRef.current?.user.id;
    const run = requireAccount()
      .me()
      .then(({ user }) => {
        // A late answer must not act on a session that changed meanwhile (sign-out here, another switch).
        if (sessionRef.current?.user.id !== startedAs) return;
        const current = sessionRef.current;
        if (!current || current.user.id !== user.id) {
          if (current) {
            opts.current.notify(
              `账号已切换为“${user.displayName}”；上一个账号未同步的修改仍保留在本机，重新登录该账号后继续同步`,
            );
          }
          enter({ user });
          return;
        }
        // The same account: pick up a profile edited in OzID, and resume if the session had been lost.
        if (current.user.displayName !== user.displayName || current.user.avatarUrl !== user.avatarUrl) {
          const next = { ...current, user: { ...current.user, displayName: user.displayName, avatarUrl: user.avatarUrl } };
          saveSession(localStorage, next);
          setSession(next);
        }
        if (sessionExpiredRef.current) {
          setSessionExpired(false);
          setLoadError("");
          void refresh(true);
        }
      })
      .catch((error) => {
        if (sessionRef.current?.user.id !== startedAs) return;
        if (isUnauthenticated(error)) {
          if (sessionRef.current?.user.deletionPending) {
            // The closure that was left half done in OzID has finished: the account no longer exists.
            leaveAccount("账号已注销，账号和所有产品的数据都已删除；登录前的本机足迹保留", true);
          } else if (sessionRef.current) {
            setSessionExpired(true);
            setLoadError(SESSION_EXPIRED_NOTICE);
            setLoaded("failed");
          }
          return;
        }
        // Not a sign-out: nothing is cleared. Only the first look tells the person.
        if (force && !sessionRef.current) opts.current.notify(describeApiError(error));
      })
      .finally(() => {
        meInFlight.current = null;
      });
    meInFlight.current = run;
    return run;
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!backendEnabled || restoreStarted.current) return;
    restoreStarted.current = true;
    // Discard material left by a pre-migration SPA.
    sessionStorage.removeItem("shanhai-oauth-pending-v1");
    setBusy("正在恢复登录…");
    void verifySession(true).finally(() => setBusy(""));
    const check = () => void verifySession();
    const onVisibleAgain = () => {
      if (document.visibilityState === "visible") check();
    };
    document.addEventListener("visibilitychange", onVisibleAgain);
    window.addEventListener("focus", check);
    window.addEventListener("online", check);
    return () => {
      document.removeEventListener("visibilitychange", onVisibleAgain);
      window.removeEventListener("focus", check);
      window.removeEventListener("online", check);
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  /** Server-confirmed revision of the loaded data, or null before it is loaded. */
  const revision = useCallback(
    () => engineRef.current?.confirmed.revision ?? null,
    [],
  );

  /** The cloud-confirmed data; media may only link to trips that are in it. */
  const confirmedSaved = useCallback(
    () => engineRef.current?.confirmed.saved ?? null,
    [],
  );

  return {
    enabled: backendEnabled,
    session,
    user: session?.user ?? null,
    mode: session ? ("cloud" as const) : ("local" as const),
    /** Nothing to show yet for this account. */
    waiting: !!session && !hasData,
    loadFailed: loaded === "failed",
    loadError,
    /** Cached confirmed ancestors allow durable edits while offline. */
    deletionBlocked,
    canWrite:
      !deletionBlocked &&
      !!session &&
      hasData &&
      !lockedCache &&
      !cacheFailed &&
      sync !== "conflict",
    sync,
    fresh,
    conflicts: conflictRecords.length,
    conflictRecords,
    pending,
    cacheFailed,
    revision,
    confirmedSaved,
    resolveConflict: (choice: "local" | "cloud") =>
      engineRef.current?.resolve(choice),
    legacyNotice,
    /** The older page's cache, as stored, for export. */
    exportLegacyRaw: () => {
      if (!legacyNotice) return;
      const url = URL.createObjectURL(
        new Blob([legacyNotice.raw], { type: "application/json" }),
      );
      const link = document.createElement("a");
      link.href = url;
      link.download = "山海足迹-旧版页面账号缓存.json";
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    },
    /** The user dealt with the older page's data (merged or kept aside); stop offering it. */
    acknowledgeLegacy: () => {
      const s = sessionRef.current;
      if (!s || !legacyNotice) return false;
      if (!acknowledgeLegacyCache(localStorage, s.user.id, legacyNotice.digest)) {
        opts.current.notify("无法记录处理结果，浏览器存储不可用");
        return false;
      }
      setLegacyNotice(null);
      return true;
    },
    exportRawCache: () => {
      if (!session) return;
      const download = (raw: string, name: string) => {
        const url = URL.createObjectURL(
          new Blob([raw], { type: "application/json" }),
        );
        const link = document.createElement("a");
        link.href = url;
        link.download = name;
        link.click();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      };
      const raw = localStorage.getItem(cacheKey(session.user.id));
      if (raw !== null) download(raw, "山海足迹-账号原始缓存.json");
      // The cache of earlier versions is never rewritten, so it still holds its original text.
      const old = localStorage.getItem(legacyCacheKey(session.user.id));
      if (old !== null) download(old, "山海足迹-旧版账号缓存.json");
    },
    busy,
    retry: () => refresh(true),
    /** Hand a user edit to the sync engine; call only in cloud mode. */
    push: (next: Saved, atomic = false) => {
      const engine = engineRef.current;
      const s = sessionRef.current;
      if (!engine || !s || protectedCache.current) return false;
      // An atomic change (the migration) is refused rather than mixed with queued edits.
      if (atomic && (engine.pending || engine.status === "conflict")) return false;
      // Persist first: refused writes must not leave a phantom edit on screen.
      if (
        !saveCloudCache(
          localStorage,
          s.user.id,
          {
            ...engine.cache,
            saved: next,
            ...(atomic ? { atomic: true as const } : {}),
          },
          legacyDigest.current,
        )
      ) {
        setCacheFailed(true);
        opts.current.notify(
          "本机保存失败，未接受此次修改，请先导出备份并释放存储空间",
        );
        return false;
      }
      editSeq.current++;
      if (atomic) engine.pushAtomic(next);
      else engine.push(next);
      return true;
    },
    sessionExpired,
    /**
     * Called right before the browser leaves for OzID (sign in again). The account cache is written on every
     * change, so there is normally nothing left to save; what must not happen is leaving while the last write
     * failed. "unsafe" means the edits held by this page are not on this device, so the caller must offer the
     * export (or staying) instead of going. A cache that another page changed is never overwritten here.
     */
    prepareLeave: (): "ok" | "unsafe" => {
      const s = sessionRef.current;
      const engine = engineRef.current;
      if (!s || !engine) return "ok";
      if (!cacheFailed && !lockedCache) return "ok";
      if (lockedCache) return "unsafe";
      const saved = saveCloudCache(localStorage, s.user.id, engine.cache, legacyDigest.current);
      setCacheFailed(!saved);
      return saved ? "ok" : "unsafe";
    },
    logout: async (discardPending = false) => {
      const s = sessionRef.current;
      if (!s) return;
      // Let in-flight writes land first so nothing is lost by signing out.
      await engineRef.current?.drain();
      if (
        !discardPending &&
        (engineRef.current?.pending || protectedCache.current)
      )
        throw new Error(
          "仍有未同步修改或无法读取的缓存，请先导出备份；联网同步完成后再退出",
        );
      let revoked = true;
      // Stop this browser receiving the account's reminders once it is signed out.
      await detachThisDevice();
      try {
        await requireAccount().logout();
      } catch (error) {
        revoked = isUnauthenticated(error);
      }
      leaveAccount(
        revoked
          ? "已退出登录，本机的账号缓存已清除"
          : "已退出登录并清除本机缓存；未能通知服务器撤销此次登录，可在 OzID 的登录设备中手动撤销",
      );
    },
  };
}

export type Cloud = ReturnType<typeof useCloud>;
