import { APP_ID, chooseReturn, resolveOzid } from "@/lib/ozid";
import { requireAccount } from "./auth";
import { backendEnabled } from "./transport";

/**
 * Where the product's account pages are. Disabled without a backend (local mode has no account center) and, in
 * a development build that has a backend but no `VITE_OZID_ORIGIN`, with the reason shown instead of a link.
 */
export const ozid = resolveOzid(
  { VITE_OZID_ORIGIN: import.meta.env.VITE_OZID_ORIGIN as string | undefined },
  backendEnabled,
  import.meta.env.BASE_URL,
);

/**
 * The address to send the browser to for signing in from the page it is on: OzID's sign-in page, returning to
 * this page when the BFF confirms its pathname is registered, and to the start page otherwise. Throws when the
 * product has no account center (check `ozid.enabled` first).
 */
export async function signInAddress(pathname: string = window.location.pathname): Promise<string> {
  if (!ozid.enabled) throw new Error("no account center");
  const path = await chooseReturn(pathname, import.meta.env.BASE_URL, async (candidate) => {
    const target = await requireAccount().resolveNavigation(APP_ID, candidate);
    // The BFF's own canonical answer for the pathname must be the one asked for.
    return target.appId === APP_ID && target.returnPath === candidate;
  });
  return ozid.loginAt(path);
}
