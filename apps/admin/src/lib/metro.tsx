import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { createEpoch } from "@kongken/account/latest";
import { isAbsent } from "./metro-errors";
import { metroAdmin } from "./rpc";
import { useSession } from "./session";

/**
 * Whether metro is there to be administered, for an account that holds the metro role:
 * "checking" until the first answer; "available" when the catalogue answers; "absent" when this BFF has
 * no metro behind it (the service is not mounted); "unknown" for any other answer (the metro upstream
 * is down, a password check is due, the network refuses): the section stays, because it may well
 * work, and each page shows exactly why it does not. An absent metro offers no section at all.
 */
export type MetroState = "checking" | "available" | "absent" | "unknown";

const MetroContext = createContext<MetroState>("checking");

export const useMetro = () => useContext(MetroContext);

export function MetroProvider({ children }: { children: ReactNode }) {
  const { session } = useSession();
  const userId = session.status === "signed-in" && session.profile.roles.includes("metro-admin") ? session.profile.user.id : "";
  const [state, setState] = useState<MetroState>("checking");
  const [reads] = useState(createEpoch);

  useEffect(() => {
    if (userId === "") return;
    setState("checking");
    const current = reads.begin();
    metroAdmin.listAdminCities({ pageSize: 1 }).then(
      () => current() && setState("available"),
      (e: unknown) => current() && setState(isAbsent(e) ? "absent" : "unknown"),
    );
    return () => reads.invalidate();
  }, [userId, reads]);

  return <MetroContext.Provider value={userId === "" ? "checking" : state}>{children}</MetroContext.Provider>;
}

/** Whether the menu and the home page may offer the metro section. */
export const metroOffered = (state: MetroState) => state === "available" || state === "unknown";
