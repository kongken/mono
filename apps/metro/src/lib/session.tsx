import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createEpoch } from "@kongken/account/latest";
import { account, setTransportAccount } from "./rpc";
import { ozid, signInAddress } from "./account-center";
import { isUnauthenticated } from "./errors";
import { navigate } from "./router";

export type Account = { id: string; displayName: string; avatarUrl: string; roles: string[] };

/**
 * What this page knows about the account behind the shared session Cookie.
 *
 * Not being signed in is a fact only the BFF's answer can establish (`Unauthenticated`). A check that could not
 * be made (the service or the network is down, a timeout) is not that answer: an account already known stays
 * known and is marked `unverified`, and a page that never learned anything is `unavailable`, which offers a retry
 * and never the sign-in page (nobody is sent to sign in again because a service blinked).
 */
export type SessionState =
  | { status: "loading" }
  | { status: "anonymous" }
  | { status: "signed-in"; account: Account; unverified: boolean }
  | { status: "unavailable" };

export type Notice = { kind: "error" | "info"; text: string };

type Ctx = {
  session: SessionState;
  /** Sends the browser to OzID's sign-in page and back to the page it is on (when the BFF confirms that page). */
  signIn: () => void;
  /** True from the click until the browser leaves for OzID. */
  signingIn: boolean;
  /** Ends this browser's shared session (the Cookie is the API host's, so OzID and the other products see it too). */
  signOut: () => Promise<void>;
  /** Asks the BFF who the Cookie belongs to. */
  refresh: () => Promise<void>;
  /** The last thing worth telling the person about their session (signed out or switched elsewhere, a failed attempt). */
  notice: Notice | undefined;
  dismissNotice: () => void;
  /** A deletion started before OzID took over the account pages and never confirmed finished (a flag left in this browser). */
  deletionPending: boolean;
  dismissDeletionPending: () => void;
};

const SessionContext = createContext<Ctx | null>(null);

export const useSession = () => {
  const v = useContext(SessionContext);
  if (!v) throw new Error("SessionProvider missing");
  return v;
};

/** The id of the signed-in account, which a page keys its personal state by. */
export const accountId = (s: SessionState) => (s.status === "signed-in" ? s.account.id : undefined);

/**
 * Until OzID took over the account pages, a deletion whose end was not confirmed left this flag behind so a retry
 * offer survived a reload. Nothing sets it any more; it only keeps that offer for browsers that still have it,
 * until the person says it is done. It holds no identity.
 */
const PENDING_KEY = "metro-account-deletion-pending";
const readPending = () => {
  try {
    return localStorage.getItem(PENDING_KEY) === "1";
  } catch {
    return false;
  }
};

/** How soon one silent re-check may follow another (focus, visibility and network events arrive in bursts). */
export const RECHECK_MIN_MS = 5000;

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionState>({ status: "loading" });
  const [notice, setNotice] = useState<Notice | undefined>();
  const [deletionPending, setPending] = useState(readPending);
  const [signingIn, setSigningIn] = useState(false);

  const dismissDeletionPending = useCallback(() => {
    try {
      localStorage.removeItem(PENDING_KEY);
    } catch {
      /* Without storage the offer simply stays until the next load. */
    }
    setPending(false);
  }, []);

  // Only the newest read of the account counts: a read overtaken by a newer one, or begun before this browser
  // signed out, must not overwrite the newer answer.
  const [reads] = useState(createEpoch);
  const known = useRef<SessionState>(session);
  const lastRead = useRef(0);

  const apply = useCallback((next: SessionState) => {
    known.current = next;
    setTransportAccount(accountId(next));
    setSession(next);
  }, []);

  const refresh = useCallback(async () => {
    const current = reads.begin();
    lastRead.current = Date.now();
    try {
      const me = await account.me();
      if (!current()) return;
      const before = known.current;
      const next: Account = { id: me.user.id, displayName: me.user.displayName, avatarUrl: me.user.avatarUrl, roles: me.roles };
      if (before.status === "signed-in" && before.account.id !== next.id) setNotice({ kind: "info", text: `账号已在其他页面切换为「${next.displayName}」，这里已同步。` });
      apply({ status: "signed-in", account: next, unverified: false });
    } catch (e) {
      if (!current()) return;
      if (isUnauthenticated(e)) {
        if (known.current.status === "signed-in") setNotice({ kind: "info", text: "你已在其他页面退出登录，或登录已失效。需要时请重新登录。" });
        apply({ status: "anonymous" });
        return;
      }
      // No answer is not "signed out": keep what is known, mark it, and let the person retry.
      console.warn("me failed", e);
      const before = known.current;
      if (before.status === "signed-in") apply({ ...before, unverified: true });
      else if (before.status === "loading") apply({ status: "unavailable" });
    }
  }, [reads, apply]);

  useEffect(() => {
    void refresh();
    return () => reads.invalidate();
  }, [refresh, reads]);

  // The shared session changes in other tabs and in OzID (sign-out, a password change, another account). The next
  // time this page is shown, focused or back online it asks again, so the change is seen without a reload.
  useEffect(() => {
    const check = () => {
      if (Date.now() - lastRead.current >= RECHECK_MIN_MS) void refresh();
    };
    const visible = () => {
      if (document.visibilityState === "visible") check();
    };
    // A page restored from the back-forward cache never ran its scripts again: it may still believe it is leaving.
    const shown = (e: PageTransitionEvent) => {
      if (e.persisted) {
        setSigningIn(false);
        check();
      }
    };
    window.addEventListener("focus", check);
    window.addEventListener("online", check);
    window.addEventListener("pageshow", shown);
    document.addEventListener("visibilitychange", visible);
    return () => {
      window.removeEventListener("focus", check);
      window.removeEventListener("online", check);
      window.removeEventListener("pageshow", shown);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [refresh]);

  const signIn = useCallback(() => {
    if (!ozid.enabled) {
      setNotice({ kind: "error", text: "账号中心尚未配置，暂时无法登录。" });
      return;
    }
    setSigningIn(true);
    setNotice(undefined);
    signInAddress().then(
      (address) => window.location.assign(address),
      () => {
        setSigningIn(false);
        setNotice({ kind: "error", text: "暂时无法前往登录页，请稍后重试。" });
      },
    );
  }, []);

  const signOut = useCallback(async () => {
    try {
      await account.logout();
    } catch (e) {
      if (isUnauthenticated(e)) {
        // The session this page believed in is not the Cookie's (another tab changed it): ask who it is now.
        await refresh();
        return;
      }
      setNotice({ kind: "error", text: "退出登录没有完成，请检查网络后重试。" });
      return;
    }
    reads.invalidate();
    setNotice(undefined);
    apply({ status: "anonymous" });
    navigate("/");
  }, [reads, apply, refresh]);

  const value = useMemo<Ctx>(
    () => ({
      session,
      signIn,
      signingIn,
      signOut,
      refresh,
      notice,
      dismissNotice: () => setNotice(undefined),
      deletionPending,
      dismissDeletionPending,
    }),
    [session, signIn, signingIn, signOut, refresh, notice, deletionPending, dismissDeletionPending],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}
