import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { createEpoch } from "@kongken/account/latest";
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
  /** The current session is no longer accepted by a call: look again, which decides sign-in or retry. */
  sessionLost: () => void;
};

const SessionContext = createContext<Ctx | null>(null);

export const useSession = () => {
  const v = useContext(SessionContext);
  if (!v) throw new Error("SessionProvider missing");
  return v;
};

/** The roles of the signed-in account, or none. What a screen shows is derived from these; the server decides again on every call. */
export const useRoles = (): string[] => {
  const { session } = useSession();
  return session.status === "signed-in" ? session.profile.roles : [];
};

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionState>({ status: "loading" });
  // Only the newest read of the account counts: an old success cannot undo a newer "not signed in",
  // and an old failure cannot hide a newer valid identity.
  const [reads] = useState(createEpoch);

  const refresh = useCallback(
    async (quiet = false) => {
      const current = reads.begin();
      if (!quiet) setSession({ status: "loading" });
      try {
        const profile = await account.me();
        if (current()) setSession({ status: "signed-in", profile });
      } catch (e) {
        if (!current()) return;
        if (ConnectError.from(e).code === Code.Unauthenticated) setSession({ status: "anonymous" });
        // A failed check leaves a signed-in screen where it is; only a first load shows the failure.
        else setSession((s) => (quiet && s.status !== "loading" ? s : { status: "unavailable", message: classifyAccountError(e, "other").message }));
      }
    },
    [reads],
  );

  useEffect(() => {
    void refresh();
    // Another app (or tab) may have signed this browser in or out: look again when the page comes back.
    const onVisible = () => {
      if (document.visibilityState === "visible") void refresh(true);
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      document.removeEventListener("visibilitychange", onVisible);
      reads.invalidate();
    };
  }, [refresh, reads]);

  const value = useMemo<Ctx>(
    () => ({
      session,
      refresh,
      sessionLost: () => void refresh(true),
      signOut: async () => {
        await account.logout();
        // Reads that began before the sign-out ended may still say "signed in": drop them.
        reads.invalidate();
        setSession({ status: "anonymous" });
      },
    }),
    [session, refresh, reads],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}
