import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { classifyAccountError } from "@kongken/account/errors";
import type { AccountProfile } from "@kongken/account/api";
import { account } from "./rpc";

export type SessionState =
  | { status: "loading" }
  | { status: "signed-in"; profile: AccountProfile }
  /** No valid session: never signed in, signed out, expired or revoked. */
  | { status: "anonymous" }
  /** The BFF or usersrv could not answer. Not a sign-out: nothing may be concluded from it. */
  | { status: "unavailable"; message: string };

type Ctx = {
  session: SessionState;
  /** Re-reads the account. `quiet` keeps the current screen while it runs. */
  refresh: (quiet?: boolean) => Promise<void>;
  /** Ends the Cookie session of this browser (every app using the same BFF sees it on its next check). */
  signOut: () => Promise<void>;
};

const SessionContext = createContext<Ctx | null>(null);

export const useSession = () => {
  const v = useContext(SessionContext);
  if (!v) throw new Error("SessionProvider missing");
  return v;
};

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionState>({ status: "loading" });

  const refresh = useCallback(async (quiet = false) => {
    if (!quiet) setSession({ status: "loading" });
    try {
      setSession({ status: "signed-in", profile: await account.me() });
    } catch (e) {
      if (ConnectError.from(e).code === Code.Unauthenticated) setSession({ status: "anonymous" });
      // A failed check leaves a signed-in screen where it is; only a first load shows the failure.
      else setSession((s) => (quiet && s.status !== "loading" ? s : { status: "unavailable", message: classifyAccountError(e, "other").message }));
    }
  }, []);

  useEffect(() => {
    void refresh();
    // Another app (or tab) may have signed this browser in or out: look again when the page comes back.
    const onVisible = () => {
      if (document.visibilityState === "visible") void refresh(true);
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => document.removeEventListener("visibilitychange", onVisible);
  }, [refresh]);

  const value = useMemo<Ctx>(
    () => ({
      session,
      refresh,
      signOut: async () => {
        await account.logout();
        setSession({ status: "anonymous" });
      },
    }),
    [session, refresh],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}
