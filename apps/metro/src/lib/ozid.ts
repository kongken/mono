import { confirmedReturn, ozidUrl, parseApiBase, parseOrigin } from "@kongken/account/navigation";
import { matchRoute } from "./route.ts";

/**
 * Every account entry of the product (sign in, sign in again, manage the account, close it) is a page of
 * OzID, the shared user center. This module only builds those addresses and decides which page a sign-in
 * returns to; it holds no session and no secret.
 *
 * The browser never chooses where a sign-in returns to. The product names itself (`APP_ID`, a stable
 * identifier of the BFF's navigation registry, not a host name) and a canonical pathname; the BFF decides
 * whether that pair is registered. Nothing here is a login ticket: knowing these parameters yields no identity.
 */

/** The product's stable app ID in the BFF's navigation registry (and the id usersrv knows it by). */
export const APP_ID = "metro";

/** The start page: where a return goes when nothing safer can be said. */
export const START_PATH = "/";

export type OzidTargets =
  | { enabled: false; reason: string }
  | {
      enabled: true;
      origin: string;
      /** OzID's sign-in page, returning to the start page. */
      login: string;
      /** The sign-in page that returns to `returnPath`: a pathname from `returnPathFor`, confirmed by the BFF. */
      loginAt: (returnPath: string) => string;
      /**
       * The sign-in page for a return context the BFF itself validated (an old mail's landing page answers
       * with one); without one, the start page. Never built from anything in the URL.
       */
      loginFor: (back: { appId: string; returnPath: string } | null) => string;
      /** OzID's own account pages (profile, password, devices); no return context. */
      home: string;
      /** Where an unfinished account closure is retried. */
      closeAccount: string;
    };

/** The addresses for a build. A build without OzID's origin has no way to sign anybody in. */
export function resolveOzid(env: { VITE_OZID_ORIGIN?: string }): OzidTargets {
  const parsed = parseOrigin(env.VITE_OZID_ORIGIN, "VITE_OZID_ORIGIN");
  if (!parsed.ok) return { enabled: false, reason: parsed.error };
  return {
    enabled: true,
    origin: parsed.value,
    login: ozidUrl(parsed.value, "login", { appId: APP_ID, returnPath: START_PATH }),
    loginAt: (path) => ozidUrl(parsed.value, "login", { appId: APP_ID, returnPath: path }),
    loginFor: (back) => ozidUrl(parsed.value, "login", back ?? { appId: APP_ID, returnPath: START_PATH }),
    home: parsed.value,
    closeAccount: `${parsed.value}/close-account`,
  };
}

/**
 * The BFF's RPC base. A build names it as an absolute address (the session Cookie belongs to the API host, so
 * no same-origin `/api` proxy of the product can carry the shared session). Only the development server may fall
 * back to its own `/api` proxy, which has no shared session either and is for local work.
 */
export function resolveApiBase(env: { VITE_API_BASE_URL?: string }, dev: boolean): { ok: true; base: string } | { ok: false; error: string } {
  if ((env.VITE_API_BASE_URL ?? "").trim() === "" && dev) return { ok: true, base: "/api" };
  const parsed = parseApiBase(env.VITE_API_BASE_URL);
  return parsed.ok ? { ok: true, base: parsed.value } : { ok: false, error: parsed.error };
}

/** A segment that can appear in a canonical return pathname (the BFF's rule: ASCII letters, digits and -._~). */
const SEGMENT = /^[A-Za-z0-9._~-]{1,64}$/;
const usable = (segment: string) => SEGMENT.test(segment) && segment !== "." && segment !== "..";

/**
 * The pathname a sign-in started on `pathname` returns to: the page itself when it is one of the product's
 * public or personal pages and its path can be written canonically, otherwise the start page (the explicit safe
 * fallback). Only the pathname is ever looked at, so a query or fragment cannot reach the result; the old
 * account addresses (`/login`, `/account`), mail landing pages and unknown paths fall back to the start page.
 * Each page other than the start page needs a matching rule in the BFF's registry
 * (`navigation.apps.metro.paths`); the BFF, not this function, decides what is allowed.
 */
export function returnPathFor(pathname: string): string {
  const route = matchRoute(pathname);
  switch (route.name) {
    case "topics":
      return "/topics";
    case "city":
      return usable(route.slug) ? `/cities/${route.slug}` : START_PATH;
    case "topic":
      return usable(route.slug) ? `/topics/${route.slug}` : START_PATH;
    case "profile":
      return usable(route.handle) ? `/u/${route.handle}` : START_PATH;
    case "me":
      return "/me";
    default:
      return START_PATH;
  }
}

/**
 * Chooses the return path of a sign-in started on `pathname`: the page itself only after the BFF confirmed that
 * exact pathname (see `confirmedReturn`), otherwise the start page.
 */
export function chooseReturn(pathname: string, confirm: (path: string) => Promise<boolean>, timeoutMs = 3000): Promise<string> {
  return confirmedReturn(returnPathFor(pathname), START_PATH, confirm, timeoutMs);
}
