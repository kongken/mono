import { APP_ID, chooseReturn, resolveOzid } from "./ozid.ts";
import { account } from "./rpc.ts";

/** Where the product's account pages are. Disabled (with the reason) when the build has no OzID origin. */
export const ozid = resolveOzid({ VITE_OZID_ORIGIN: import.meta.env.VITE_OZID_ORIGIN as string | undefined });

/**
 * The address to send the browser to for signing in from the page it is on: OzID's sign-in page, returning to
 * this page when the BFF confirms its pathname is registered, and to the start page otherwise. Throws when the
 * product has no account center (check `ozid.enabled` first).
 */
export async function signInAddress(pathname: string = window.location.pathname): Promise<string> {
  if (!ozid.enabled) throw new Error("no account center");
  const path = await chooseReturn(pathname, async (candidate) => {
    const target = await account.resolveNavigation(APP_ID, candidate);
    // The BFF's own canonical answer for the pathname must be the one asked for.
    return target.appId === APP_ID && target.returnPath === candidate;
  });
  return ozid.loginAt(path);
}
