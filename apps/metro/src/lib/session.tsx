import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { createEpoch } from "@kongken/account/latest";
import { account } from "./rpc";
import { isUnauthenticated } from "./errors";
import { navigate } from "./router";
import { loginPath } from "./next";

export type Account = { id: string; displayName: string; avatarUrl: string; roles: string[] };
export type SessionState = { status: "loading" } | { status: "anonymous" } | { status: "signed-in"; account: Account };

type Ctx = {
  session: SessionState;
  isAdmin: boolean;
  /** Sends the browser to the sign-in page and back to the current page afterwards. */
  signIn: () => void;
  signOut: () => Promise<void>;
  refresh: () => Promise<void>;
  /** The signed-in account's profile changed (nickname, avatar). */
  updateAccount: (patch: Partial<Pick<Account, "displayName" | "avatarUrl">>) => void;
  /** An account deletion was started in this browser and has not been confirmed finished. */
  deletionPending: boolean;
  setDeletionPending: (pending: boolean) => void;
};

const SessionContext = createContext<Ctx | null>(null);

export const useSession = () => {
  const v = useContext(SessionContext);
  if (!v) throw new Error("SessionProvider missing");
  return v;
};

/**
 * A deletion whose end was not confirmed leaves the account closed ("deleting") and the
 * session unusable, so the page cannot learn about it from Me. This flag is the only
 * thing kept in the browser: it makes the retry offer survive a reload. It holds no
 * identity and is cleared once the deletion is confirmed.
 */
const PENDING_KEY = "metro-account-deletion-pending";
const readPending = () => {
  try {
    return localStorage.getItem(PENDING_KEY) === "1";
  } catch {
    return false;
  }
};

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionState>({ status: "loading" });
  const [deletionPending, setPending] = useState(readPending);

  const setDeletionPending = useCallback((pending: boolean) => {
    try {
      if (pending) localStorage.setItem(PENDING_KEY, "1");
      else localStorage.removeItem(PENDING_KEY);
    } catch {
      /* Without storage the offer simply does not survive a reload. */
    }
    setPending(pending);
  }, []);

  // Only the newest read of the account counts: a read overtaken by a newer one, or begun
  // before this browser signed out, must not overwrite the newer answer.
  const [reads] = useState(createEpoch);

  const refresh = useCallback(async () => {
    const current = reads.begin();
    try {
      const me = await account.me();
      if (!current()) return;
      setSession({
        status: "signed-in",
        account: { id: me.user.id, displayName: me.user.displayName, avatarUrl: me.user.avatarUrl, roles: me.roles },
      });
    } catch (e) {
      if (!current()) return;
      if (!isUnauthenticated(e)) console.warn("me failed", e);
      setSession({ status: "anonymous" });
    }
  }, [reads]);

  useEffect(() => {
    void refresh();
    return () => reads.invalidate();
  }, [refresh, reads]);

  const value = useMemo<Ctx>(
    () => ({
      session,
      isAdmin: session.status === "signed-in" && session.account.roles.includes("metro-admin"),
      signIn: () => navigate(loginPath(window.location.pathname + window.location.search)),
      signOut: async () => {
        await account.logout();
        reads.invalidate();
        setSession({ status: "anonymous" });
        navigate("/");
      },
      refresh,
      updateAccount: (patch) =>
        setSession((s) => (s.status === "signed-in" ? { status: "signed-in", account: { ...s.account, ...patch } } : s)),
      deletionPending,
      setDeletionPending,
    }),
    [session, refresh, reads, deletionPending, setDeletionPending],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}
