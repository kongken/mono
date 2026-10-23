import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { auth, loginURL, write } from "./rpc";
import { isUnauthenticated } from "./errors";
import { loginReturnPaths } from "./route";
import { navigate } from "./router";

export type Account = { id: string; displayName: string; username: string; roles: string[] };
export type SessionState = { status: "loading" } | { status: "anonymous" } | { status: "signed-in"; account: Account };

const RETURN_KEY = "metro.return";

type Ctx = {
  session: SessionState;
  isAdmin: boolean;
  /** Sends the browser to the login page and back to the current page afterwards. */
  signIn: () => void;
  signOut: () => Promise<void>;
  refresh: () => Promise<void>;
};

const SessionContext = createContext<Ctx | null>(null);

export const useSession = () => {
  const v = useContext(SessionContext);
  if (!v) throw new Error("SessionProvider missing");
  return v;
};

/** The BFF only returns to a few fixed paths; the page the user came from is restored on arrival. */
export function signIn() {
  const here = window.location.pathname + window.location.search;
  const target = loginReturnPaths.includes(window.location.pathname) ? window.location.pathname : "/";
  try {
    if (here !== target) sessionStorage.setItem(RETURN_KEY, here);
  } catch {
    // Storage may be unavailable; the user simply lands on the fixed path.
  }
  window.location.assign(loginURL(target));
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionState>({ status: "loading" });

  const refresh = useCallback(async () => {
    try {
      const me = await auth.me({});
      if (!me.user) throw new Error("incomplete user");
      setSession({
        status: "signed-in",
        account: { id: me.user.id, displayName: me.user.displayName, username: me.user.username, roles: me.roles },
      });
    } catch (e) {
      if (!isUnauthenticated(e)) console.warn("me failed", e);
      setSession({ status: "anonymous" });
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Back from the login redirect: go on to the page the user started from.
  useEffect(() => {
    if (session.status !== "signed-in") return;
    try {
      const back = sessionStorage.getItem(RETURN_KEY);
      if (back) {
        sessionStorage.removeItem(RETURN_KEY);
        if (back.startsWith("/") && !back.startsWith("//")) navigate(back, true);
      }
    } catch {
      // ignore
    }
  }, [session.status]);

  const value = useMemo<Ctx>(
    () => ({
      session,
      isAdmin: session.status === "signed-in" && session.account.roles.includes("metro-admin"),
      signIn,
      signOut: async () => {
        await write(() => auth.logout({}));
        setSession({ status: "anonymous" });
        navigate("/");
      },
      refresh,
    }),
    [session, refresh],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}
