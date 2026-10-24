import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { auth, write } from "./rpc";
import { isUnauthenticated } from "./errors";
import { navigate } from "./router";

export type Account = { id: string; displayName: string; roles: string[] };
export type SessionState = { status: "loading" } | { status: "anonymous" } | { status: "signed-in"; account: Account };

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

/**
 * The email and password forms arrive with the front-end stage of the account
 * system; until then there is no browser sign-in to start.
 */
export function signIn() {}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionState>({ status: "loading" });

  const refresh = useCallback(async () => {
    try {
      const me = await auth.me({});
      if (!me.user) throw new Error("incomplete user");
      setSession({
        status: "signed-in",
        account: { id: me.user.id, displayName: me.user.displayName, roles: me.roles },
      });
    } catch (e) {
      if (!isUnauthenticated(e)) console.warn("me failed", e);
      setSession({ status: "anonymous" });
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

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
