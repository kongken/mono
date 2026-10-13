import { Code, ConnectError } from "@connectrpc/connect";
import { useCallback, useEffect, useRef, useState } from "react";
import { authApi, type ProviderInfo } from "@/api/auth";
import { footprintApi, createFootprintApi } from "@/api/footprints";
import { setToken } from "@/api/token";
import { backendEnabled } from "@/api/transport";
import { describeApiError, isUnauthenticated } from "@/lib/errors";
import { consumePending, parseOAuthCallback, savePending } from "@/lib/oauth";
import {
  clearAccountData,
  clearOtherCaches,
  loadCloudCache,
  loadSession,
  saveCloudCache,
  saveSession,
  type CachedCloud,
  type StoredSession,
  cacheKey,
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
  cache: CachedCloud | null;
  error?: string;
};

/** Reads the stored session and its cached copy; both are synchronous. */
export function readBoot(validate: (data: unknown) => Saved): CloudBoot {
  if (!backendEnabled) return { session: null, cache: null };
  const session = loadSession(localStorage);
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
const SERVER_DATA_ERROR = "云端数据格式无法识别";
const emptySaved: Saved = { version: 1, visits: [], provinces: {} };

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
  const [loaded, setLoaded] = useState<"pending" | "ok" | "failed">("pending");
  const [loadError, setLoadError] = useState(options.boot.error ?? "");
  const protectedCache = useRef(!!options.boot.error);
  const [lockedCache, setLockedCache] = useState(!!options.boot.error);
  const [cacheFailed, setCacheFailed] = useState(false);
  const [conflictRecords, setConflictRecords] = useState<Op[]>([]);
  const [pending, setPending] = useState(false);
  const [sync, setSync] = useState<SyncStatus>("idle");
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [busy, setBusy] = useState("");
  const engineRef = useRef<SyncEngine | null>(null);
  const editSeq = useRef(0);
  const lastRefresh = useRef(0);
  const callbackHandled = useRef(false);

  const setSession = (next: StoredSession | null) => {
    sessionRef.current = next;
    setToken(next?.token ?? null);
    setSessionState(next);
  };

  const leaveAccount = useCallback((message: string) => {
    try {
      clearAccountData(
        localStorage,
        sessionRef.current?.user.id,
        opts.current.validate,
      );
    } catch {
      // The in-memory state below is cleared regardless.
    }
    engineRef.current?.stop();
    engineRef.current = null;
    protectedCache.current = false;
    setLockedCache(false);
    setCacheFailed(false);
    setConflictRecords([]);
    setPending(false);
    setDeletionBlocked(false);
    sessionRef.current = null;
    setToken(null);
    setSessionState(null);
    setLoaded("pending");
    setLoadError("");
    setHasData(false);
    setSync("idle");
    opts.current.onAccountChange(null);
    if (message) opts.current.notify(message);
  }, []);

  const adopt = (
    s: StoredSession,
    snapshot: CloudSnapshot,
    desired = snapshot.saved,
    pendingBatch?: CachedCloud["pendingBatch"],
  ) => {
    engineRef.current?.stop();
    const scopedApi = createFootprintApi(snapshot);
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
          if (sessionRef.current?.token !== s.token) return;
          setSync(status);
          setConflictRecords(engineRef.current?.conflicts ?? []);
          setPending(engineRef.current?.pending ?? false);
          if (status === "idle") {
            setLoaded("ok");
            setLoadError("");
          }
        },
        onChange: (cache) => {
          if (sessionRef.current?.token !== s.token) return;
          setCacheFailed(!saveCloudCache(localStorage, s.user.id, cache));
          setPending(
            !!cache.pendingBatch ||
              diffSaved(cache.confirmed, cache.saved).length > 0,
          );
          opts.current.onCloudData(cache.saved);
        },
        onBeforeSend: (cache) => {
          if (
            sessionRef.current?.token !== s.token ||
            !saveCloudCache(localStorage, s.user.id, cache)
          ) {
            setCacheFailed(true);
            throw new Error("批量请求无法保存到本机，已暂停同步，请先导出备份");
          }
          setCacheFailed(false);
        },
        onError: (error) => {
          if (sessionRef.current?.token !== s.token) return;
          if (isUnauthenticated(error)) {
            // Do not clear unsent edits automatically when a session expires.
            setLoadError(
              "登录已过期，未同步修改仍保留，请先导出备份再退出并重新登录",
            );
          } else {
            setLoadError(describeApiError(error));
          }
          setLoaded("failed");
          opts.current.notify("同步未完成，修改已保留在本机，可重试或导出备份");
        },
      },
      desired,
      pendingBatch,
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
      setLoadError("账号注销未完成，请打开账号设置重试注销");
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
      fetched = await footprintApi.get();
    } catch (error) {
      if (sessionRef.current?.token !== s.token) return;
      if (isUnauthenticated(error)) {
        setLoadError("登录已过期，请先导出未同步修改，再退出并重新登录");
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
      if (sessionRef.current?.token === s.token) {
        setLoaded("failed");
        setLoadError(SERVER_DATA_ERROR);
      }
      return;
    }
    // Drop the result if the account changed or an edit happened meanwhile.
    if (sessionRef.current?.token !== s.token || editSeq.current !== seq)
      return;
    adopt(s, snapshot);
    engineRef.current?.retry();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const enter = (s: StoredSession) => {
    saveSession(localStorage, s);
    try {
      clearOtherCaches(localStorage, s.user.id, opts.current.validate);
    } catch {
      // A stale cache of another account is only an optimisation lost.
    }
    let cache: CachedCloud | null = null;
    try {
      cache = loadCloudCache(localStorage, s.user.id, opts.current.validate);
      protectedCache.current = false;
      setLockedCache(false);
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
    setDeletionBlocked(!!s.user.deletionPending);
    if (s.user.deletionPending) {
      setHasData(true);
      opts.current.onAccountChange(emptySaved);
      setLoaded("failed");
      setLoadError("账号注销未完成，请重试注销");
      return;
    }
    setLoaded("pending");
    setLoadError("");
    setSync("idle");
    setHasData(!!cache);
    opts.current.onAccountChange(cache?.saved ?? emptySaved);
    lastRefresh.current = 0;
    if (cache) {
      adopt(
        s,
        { saved: cache.confirmed ?? cache.saved, revision: cache.revision },
        cache.saved,
        cache.pendingBatch,
      );
    }
    void refresh(true);
  };

  // First load for a restored session.
  useEffect(() => {
    const s = sessionRef.current;
    const cache = opts.current.boot.cache;
    setToken(s?.token ?? null);
    if (s?.user.deletionPending) {
      setHasData(true);
      setLoaded("failed");
      setLoadError("账号注销未完成，请重试注销");
      return;
    }
    if (s && cache) {
      adopt(
        s,
        { saved: cache.confirmed ?? cache.saved, revision: cache.revision },
        cache.saved,
        cache.pendingBatch,
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

  // Finish an OAuth round trip when this page is the provider callback.
  useEffect(() => {
    if (!backendEnabled || callbackHandled.current) return;
    const callback = parseOAuthCallback(
      location.pathname,
      location.search,
      import.meta.env.BASE_URL,
    );
    if (!callback) return;
    callbackHandled.current = true;
    // Remove the one-time code from the address bar and history right away.
    history.replaceState(null, "", import.meta.env.BASE_URL);
    if ("error" in callback) {
      consumePending(sessionStorage, callback);
      opts.current.notify(
        callback.error === "access_denied"
          ? "你已取消授权，未登录"
          : "第三方登录未完成，请重试",
      );
      return;
    }
    if (!consumePending(sessionStorage, callback)) {
      opts.current.notify("登录请求无效或已过期，请重新发起登录");
      return;
    }
    setBusy("正在完成登录…");
    authApi
      .completeOAuth(callback.provider, callback.code, callback.state)
      .then((s) => {
        enter(s);
        opts.current.notify(`已登录：${s.user.displayName || s.user.username}`);
      })
      .catch((error) => opts.current.notify(describeApiError(error)))
      .finally(() => setBusy(""));
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const signIn = async (action: () => Promise<StoredSession>) => {
    const s = await action();
    enter(s);
    opts.current.notify(`已登录：${s.user.displayName || s.user.username}`);
  };

  const loadProviders = useCallback(async () => {
    try {
      setProviders(await authApi.listProviders());
    } catch {
      setProviders([]);
    }
  }, []);

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
    conflicts: conflictRecords.length,
    conflictRecords,
    pending,
    cacheFailed,
    resolveConflict: (choice: "local" | "cloud") =>
      engineRef.current?.resolve(choice),
    exportRawCache: () => {
      if (!session) return;
      const raw = localStorage.getItem(cacheKey(session.user.id));
      if (raw === null) return;
      const url = URL.createObjectURL(
        new Blob([raw], { type: "application/json" }),
      );
      const link = document.createElement("a");
      link.href = url;
      link.download = "山海足迹-账号原始缓存.json";
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    },
    busy,
    providers,
    retry: () => refresh(true),
    /** Hand a user edit to the sync engine; call only in cloud mode. */
    push: (next: Saved) => {
      const engine = engineRef.current;
      const s = sessionRef.current;
      if (!engine || !s || protectedCache.current) return false;
      // Persist first: refused writes must not leave a phantom edit on screen.
      if (
        !saveCloudCache(localStorage, s.user.id, {
          ...engine.cache,
          saved: next,
        })
      ) {
        setCacheFailed(true);
        opts.current.notify(
          "本机保存失败，未接受此次修改，请先导出备份并释放存储空间",
        );
        return false;
      }
      editSeq.current++;
      engine.push(next);
      return true;
    },
    login: (username: string, password: string) =>
      signIn(() => authApi.login(username, password)),
    register: (username: string, password: string, displayName: string) =>
      signIn(() => authApi.register(username, password, displayName)),
    loadProviders,
    startOAuth: async (provider: string) => {
      const r = await authApi.beginOAuth(provider);
      const url = new URL(r.authorizeUrl);
      if (url.protocol !== "https:" && url.protocol !== "http:")
        throw new Error("invalid authorize url");
      savePending(sessionStorage, provider, r.state);
      location.assign(url.toString());
    },
    deleteAccount: async (password: string) => {
      const s = sessionRef.current;
      if (!s) return;
      const engine = engineRef.current;
      await engine?.drain();
      engine?.stop();
      setDeletionBlocked(true);
      try {
        await authApi.deleteAccount(password);
        leaveAccount("账号及全部云端数据已注销；登录前的本地足迹保留");
      } catch (error) {
        if (
          !s.user.deletionPending &&
          error instanceof ConnectError &&
          [
            Code.PermissionDenied,
            Code.InvalidArgument,
            Code.FailedPrecondition,
          ].includes(error.code)
        ) {
          setDeletionBlocked(false);
          engine?.resume();
        } else {
          const pendingSession = {
            ...s,
            user: { ...s.user, deletionPending: true },
          };
          saveSession(localStorage, pendingSession);
          setSession(pendingSession);
          setLoadError(
            "注销尚未确认完成，请重新登录或打开账号设置重试；云端编辑已暂停",
          );
        }
        throw error;
      }
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
      try {
        await authApi.logout();
      } catch (error) {
        revoked = isUnauthenticated(error);
      }
      leaveAccount(
        revoked
          ? "已退出登录，本机的账号缓存已清除"
          : "已退出登录并清除本机缓存；未能通知服务器撤销此次登录，可在其他设备的会话列表中手动撤销",
      );
    },
  };
}

export type Cloud = ReturnType<typeof useCloud>;
