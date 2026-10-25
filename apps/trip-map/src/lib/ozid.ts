import { ozidUrl, parseOrigin } from "@kongken/account/navigation";

/**
 * Every account entry of the product (sign in, sign in again, manage the account, close it) is a page of
 * OzID, the shared user center. This module only builds those addresses and decides whether they exist:
 * a product without a backend has no account center at all, and a backend without OzID's address
 * cannot send anybody anywhere (the build refuses that combination, see vite.config.ts).
 *
 * The browser never chooses where a sign-in returns to. The product names itself (`APP_ID`, a stable
 * identifier of the BFF's navigation registry, not a host name) and a canonical pathname; the BFF decides
 * whether that pair is registered. Nothing here is a login ticket: knowing these parameters yields no identity.
 */

/** The product's stable app ID in the BFF's navigation registry. */
export const APP_ID = "trip-map";

export type OzidTargets =
  | { enabled: false; reason: string }
  | {
      enabled: true;
      origin: string;
      /** OzID's sign-in page; the browser comes back to the product's start page. */
      login: string;
      /**
       * The sign-in page for a return context the BFF itself validated (an old mail's landing page answers
       * with one); without one, the product's own start page. Never built from anything in the URL.
       */
      loginFor: (back: { appId: string; returnPath: string } | null) => string;
      /** OzID's own account pages (profile, password, devices); no return context. */
      home: string;
      /** Where an unfinished account closure is retried. */
      closeAccount: string;
    };

/**
 * The addresses for a build. `backend` is whether the product talks to a backend at all; without
 * one there is no account center and nothing is built, so no address can ever be requested or shown.
 * `base` is the Vite base path the product is served under (where a sign-in returns to).
 */
export function resolveOzid(env: { VITE_OZID_ORIGIN?: string }, backend: boolean, base = "/"): OzidTargets {
  if (!backend) return { enabled: false, reason: "local mode" };
  const parsed = parseOrigin(env.VITE_OZID_ORIGIN, "VITE_OZID_ORIGIN");
  if (!parsed.ok) return { enabled: false, reason: parsed.error };
  const returnPath = base === "" ? "/" : base.startsWith("/") ? base : `/${base}`;
  return {
    enabled: true,
    origin: parsed.value,
    login: ozidUrl(parsed.value, "login", { appId: APP_ID, returnPath }),
    loginFor: (back) => ozidUrl(parsed.value, "login", back ?? { appId: APP_ID, returnPath }),
    home: parsed.value,
    closeAccount: `${parsed.value}/close-account`,
  };
}

/**
 * The pre-OzID entry points of the product: `/?signin=1` (the "Log in" link of the old mail pages),
 * `/login` and `/account`. They are not pages any more. They only open the account window, which
 * leads to OzID; the URL is cleaned up, and nothing else in it (no `next`, no address) is read or followed.
 * Returns the cleaned location, or null when the URL is not one of them.
 */
export function legacyAccountEntry(pathname: string, search: string, base = "/"): { path: string; search: string } | null {
  const prefix = base.replace(/\/+$/, "");
  const rest = pathname.startsWith(prefix) ? pathname.slice(prefix.length) : null;
  const named = rest !== null && /^\/(login|account)\/?$/.test(rest);
  const flagged = new URLSearchParams(search).get("signin") === "1";
  if (!named && !flagged) return null;
  // The whole query goes: an old link's parameters (a `next`, an address) were never read and are not kept.
  return { path: named ? `${prefix}/` : pathname, search: "" };
}
