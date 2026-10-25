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
      /** The sign-in page that returns to `returnPath`: a pathname from `returnPathFor`, confirmed by the BFF. */
      loginAt: (returnPath: string) => string;
      /** The product's start page: where a return goes when nothing safer can be said. */
      start: string;
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
    loginAt: (path) => ozidUrl(parsed.value, "login", { appId: APP_ID, returnPath: path }),
    start: returnPath,
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

/**
 * The pages of the product an account step may start from and come back to, as pathnames relative to the base
 * path. Each needs a matching rule in the BFF's registry (`navigation.apps.trip-map.paths`): the BFF, not
 * this list, decides what is allowed. Only pages that carry no capability belong here. The map itself is the
 * start page and needs no entry.
 */
export const RETURN_ROUTES = ["status"] as const;

/**
 * The pathname a sign-in started on `pathname` returns to: the page itself when it is the start page or a
 * listed one, otherwise the start page (the explicit safe fallback). Only the pathname is ever looked at, so a
 * query or fragment cannot reach the result, and a path that holds a capability (a share link `/s/<token>`, a
 * mail landing page, anything unknown) falls back to the start page instead of being carried to OzID.
 * Trailing slashes are normalised, so the result is always canonical (`/status`, never `/status/`).
 */
export function returnPathFor(pathname: string, base = "/"): string {
  const prefix = base === "" ? "/" : base.endsWith("/") ? base : `${base}/`;
  const start = prefix.startsWith("/") ? prefix : `/${prefix}`;
  const path = pathname.startsWith("/") ? pathname : `/${pathname}`;
  if (!path.startsWith(start)) return start;
  const rest = path.slice(start.length).replace(/\/+$/, "");
  return (RETURN_ROUTES as readonly string[]).includes(rest) ? `${start}${rest}` : start;
}

/**
 * Chooses the return path of a sign-in started on `pathname`. A page other than the start page is used only
 * after the BFF itself confirmed that exact pathname (`confirm` asks it): a deployment that has not registered
 * the page, an outage, a refusal or a slow answer all end the same way, at the start page, so a sign-in never
 * ends in OzID's "this address is not allowed" state because of a page the product offered.
 */
export async function chooseReturn(
  pathname: string,
  base: string,
  confirm: (path: string) => Promise<boolean>,
  timeoutMs = 3000,
): Promise<string> {
  const wanted = returnPathFor(pathname, base);
  const start = returnPathFor("/", base);
  if (wanted === start) return start;
  const answer = await Promise.race([
    confirm(wanted).catch(() => false),
    new Promise<false>((resolve) => setTimeout(() => resolve(false), timeoutMs)),
  ]);
  return answer ? wanted : start;
}
