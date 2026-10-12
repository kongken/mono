import { useCallback, useEffect, useRef, useState } from "react";
import { authApi, type ProviderInfo } from "@/api/auth";
import { footprintApi } from "@/api/footprints";
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
} from "@/lib/session";
import { SyncEngine, type CloudSnapshot, type SyncStatus } from "@/lib/sync";
import type { Saved } from "@/lib/travel";

export type CloudBoot = { session: StoredSession | null; cache: CachedCloud | null };

/** Reads the stored session and its cached copy; both are synchronous. */
export function readBoot(validate: (data: unknown) => Saved): CloudBoot {
  if (!backendEnabled) return { session: null, cache: null };
  try {
    const session = loadSession(localStorage);
    return {
      session,
      cache: session ? loadCloudCache(localStorage, session.user.id, validate) : null,
    };
  } catch {
    return { session: null, cache: null };
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

  const [session, setSessionState] = useState<StoredSession | null>(() => {
    setToken(options.boot.session?.token ?? null);
    return options.boot.session;
  });
  const sessionRef = useRef(session);
  const [hasData, setHasData] = useState(!!options.boot.cache);
  const [loaded, setLoaded] = useState<"pending" | "ok" | "failed">("pending");
  const [loadError, setLoadError] = useState("");
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
      clearAccountData(localStorage);
    } catch {
      // The in-memory state below is cleared regardless.
    }
    engineRef.current = null;
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

  const adopt = (s: StoredSession, snapshot: CloudSnapshot, fresh: boolean) => {
    const cacheWrite = (c: CloudSnapshot) => {
      if (sessionRef.current?.token === s.token)
        saveCloudCache(localStorage, s.user.id, c);
    };
    if (!engineRef.current) {
      engineRef.current = new SyncEngine(footprintApi, snapshot, {
        onStatus: setSync,
        onConfirmed: cacheWrite,
        onError: (error) => {
          if (sessionRef.current?.token !== s.token) return;
          if (isUnauthenticated(error)) {
            leaveAccount("登录已过期，请重新登录");
            return;
          }
          const engine = engineRef.current;
          if (!engine) return;
          // Show only what the server confirmed, and drop the unsent changes.
          const confirmed = engine.confirmed;
          engine.reset(confirmed);
          opts.current.onCloudData(confirmed.saved);
          // Read-only until a refetch succeeds, so the indicator stays honest.
          setLoaded("failed");
          setLoadError(describeApiError(error));
          opts.current.notify(
            `同步失败：${describeApiError(error)}。已还原为云端最新确认的数据`,
          );
          void refresh(true);
        },
      });
    } else {
      engineRef.current.reset(snapshot);
    }
    cacheWrite(snapshot);
    if (fresh) opts.current.onCloudData(snapshot.saved);
    setHasData(true);
    setLoaded("ok");
    setLoadError("");
  };

  const refresh = useCallback(async (force = false) => {
    const s = sessionRef.current;
    if (!s) return;
    const now = Date.now();
    if (!force && now - lastRefresh.current < REFRESH_MIN_MS) return;
    lastRefresh.current = now;
    const engine = engineRef.current;
    if (engine && (engine.pending || engine.status === "syncing")) return;
    const seq = editSeq.current;
    let fetched: CloudSnapshot;
    try {
      fetched = await footprintApi.get();
    } catch (error) {
      if (sessionRef.current?.token !== s.token) return;
      if (isUnauthenticated(error)) {
        leaveAccount("登录已过期，请重新登录");
        return;
      }
      setLoaded("failed");
      setLoadError(describeApiError(error));
      return;
    }
    let snapshot: CloudSnapshot;
    try {
      snapshot = { revision: fetched.revision, saved: opts.current.validate(fetched.saved) };
    } catch {
      if (sessionRef.current?.token === s.token) {
        setLoaded("failed");
        setLoadError(SERVER_DATA_ERROR);
      }
      return;
    }
    // Drop the result if the account changed or an edit happened meanwhile.
    if (sessionRef.current?.token !== s.token || editSeq.current !== seq) return;
    const current = engineRef.current;
    if (current && current.confirmed.revision === snapshot.revision) {
      setLoaded("ok");
      return;
    }
    adopt(s, snapshot, true);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const enter = (s: StoredSession) => {
    saveSession(localStorage, s);
    try {
      clearOtherCaches(localStorage, s.user.id);
    } catch {
      // A stale cache of another account is only an optimisation lost.
    }
    const cache = loadCloudCache(localStorage, s.user.id, opts.current.validate);
    engineRef.current = null;
    setSession(s);
    setLoaded("pending");
    setLoadError("");
    setSync("idle");
    setHasData(!!cache);
    opts.current.onAccountChange(cache?.saved ?? emptySaved);
    lastRefresh.current = 0;
    void refresh(true);
  };

  // First load for a restored session.
  useEffect(() => {
    if (sessionRef.current) void refresh(true);
  }, [refresh]);

  // Pick up changes from other devices when the page comes back.
  useEffect(() => {
    if (!session) return;
    const onVisible = () => {
      if (document.visibilityState === "visible") void refresh();
    };
    const onOnline = () => void refresh(true);
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("online", onOnline);
    return () => {
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("online", onOnline);
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
    /** Edits are accepted only against data confirmed by the server. */
    canWrite: !!session && loaded === "ok",
    sync,
    busy,
    providers,
    retry: () => refresh(true),
    /** Hand a user edit to the sync engine; call only in cloud mode. */
    push: (next: Saved) => {
      editSeq.current++;
      engineRef.current?.push(next);
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
    logout: async () => {
      const s = sessionRef.current;
      if (!s) return;
      // Let in-flight writes land first so nothing is lost by signing out.
      await engineRef.current?.drain();
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
