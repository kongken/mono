import { ozidUrl, type OzidPage } from "@kongken/account/navigation";
import { APP_ID, ozidOrigin } from "./rpc";
import { returnPathOf } from "./routes.ts";

/**
 * Where OzID is opened for sign-in ("login") or password confirmation ("security") and
 * where it should send the browser back to. The BFF decides whether that pair is registered;
 * a refusal leaves the user at OzID, never at an unlisted address.
 */
export function ozidLink(page: OzidPage, pathname: string): string {
  return ozidUrl(ozidOrigin, page, { appId: APP_ID, returnPath: returnPathOf(pathname) });
}

/** OzID's own account pages (no return context): "manage the OzID account". */
export const ozidHome = () => ozidOrigin;

/** A full navigation to OzID: it is another origin. */
export function goToOzid(page: OzidPage, pathname = window.location.pathname) {
  window.location.assign(ozidLink(page, pathname));
}
