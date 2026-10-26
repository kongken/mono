// Navigation between the products and OzID (the user center), and the public build
// configuration both sides need. Pure functions only (no DOM types), so build tooling
// and node:test can import them.
//
// The browser never decides where a sign-in returns to. A product sends the browser to
// OzID with a stable app ID and a canonical pathname; OzID asks the BFF
// (`ResolveNavigation`) whether that pair is registered and only ever navigates to the
// URL the BFF answers with. Nothing here is a login ticket: knowing or forging these
// parameters yields no identity.

import { MAIL_TOKEN, RESET_PASSWORD_PATH, VERIFY_EMAIL_PATH, type LandingKind } from "./paths.ts";

export const NAV_APP_PARAM = "app_id";
export const NAV_PATH_PARAM = "return_path";

const MAX_APP_ID = 32;
const MAX_PATH = 256;
const APP_ID = /^[a-z][a-z0-9-]*$/;

/** What a page of OzID was asked for: nothing, a pair to hand to the BFF, or garbage. */
export type NavContext = { kind: "none" } | { kind: "context"; appId: string; returnPath: string } | { kind: "invalid" };

/**
 * Reads `app_id` and `return_path` from a query string. It only rejects what could not
 * be a registered pair at all (repeated, oversized or malformed values); whether a pair
 * is allowed is the BFF's decision, never this function's.
 */
export function readNavContext(search: string): NavContext {
  const params = new URLSearchParams(search);
  const apps = params.getAll(NAV_APP_PARAM);
  const paths = params.getAll(NAV_PATH_PARAM);
  if (apps.length === 0 && paths.length === 0) return { kind: "none" };
  if (apps.length !== 1 || paths.length > 1) return { kind: "invalid" };
  const appId = apps[0]!;
  const returnPath = paths[0] ?? "";
  if (appId.length > MAX_APP_ID || !APP_ID.test(appId) || returnPath.length > MAX_PATH) return { kind: "invalid" };
  return { kind: "context", appId, returnPath };
}

/** The query string that carries a context to an OzID page ("" without one). */
export function navQuery(ctx: { appId: string; returnPath?: string }): string {
  const q = new URLSearchParams({ [NAV_APP_PARAM]: ctx.appId });
  if (ctx.returnPath) q.set(NAV_PATH_PARAM, ctx.returnPath);
  return `?${q.toString()}`;
}

export type OzidPage = "login" | "security";

/**
 * The OzID address a product sends the browser to: "/login" to sign in, "/security" to
 * confirm the password again. `ozidOrigin` is the public build setting of the product.
 */
export function ozidUrl(ozidOrigin: string, page: OzidPage, ctx: { appId: string; returnPath?: string }): string {
  return `${ozidOrigin.replace(/\/+$/, "")}/${page}${navQuery(ctx)}`;
}

const loopback = (host: string) => host === "localhost" || host.endsWith(".localhost") || host === "127.0.0.1" || host === "[::1]";

type Parsed<T> = { ok: true; value: T } | { ok: false; error: string };

function parseUrl(name: string, raw: string | undefined): Parsed<URL> {
  const value = raw?.trim();
  if (!value) return { ok: false, error: `${name} is required` };
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return { ok: false, error: `${name} must be an absolute URL such as https://api.example.com/api` };
  }
  if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback(url.hostname)))
    return { ok: false, error: `${name} must be https (plain http is only accepted for localhost)` };
  if (url.username || url.password || url.search || url.hash)
    return { ok: false, error: `${name} must not contain credentials, a query or a fragment` };
  return { ok: true, value: url };
}

/**
 * The BFF RPC base of a front end that has no same-origin proxy: an absolute URL, with an
 * optional path such as /api. This is a public build setting; it holds no secret.
 */
export function parseApiBase(raw: string | undefined, name = "VITE_API_BASE_URL", opts: { allowSameOrigin?: boolean } = {}): Parsed<string> {
  // A front end served from the API's own origin (behind a gateway that routes the RPC paths to the BFF)
  // may use exactly "/": requests go to the page's origin. Any other relative value stays refused.
  if (opts.allowSameOrigin && raw?.trim() === "/") return { ok: true, value: "/" };
  const parsed = parseUrl(name, raw);
  if (!parsed.ok) return parsed;
  return { ok: true, value: parsed.value.href.replace(/\/+$/, "") };
}

/** An origin setting: scheme, host and optional port only. */
export function parseOrigin(raw: string | undefined, name: string): Parsed<string> {
  const parsed = parseUrl(name, raw);
  if (!parsed.ok) return parsed;
  if (parsed.value.pathname !== "/") return { ok: false, error: `${name} must be an origin without a path` };
  return { ok: true, value: parsed.value.origin };
}

/** True for an address the browser may be navigated to: http(s) only. */
export function isNavigableUrl(raw: string): boolean {
  try {
    const u = new URL(raw);
    return (u.protocol === "https:" || u.protocol === "http:") && !u.username && !u.password;
  } catch {
    return false;
  }
}

/**
 * Chooses the return path of a sign-in. `wanted` is the canonical pathname of the page the sign-in started on
 * (the product decided that it is a page worth returning to), `start` its start page. A page other than the
 * start page is used only after the BFF itself confirmed that exact pathname (`confirm` asks it): a deployment
 * that has not registered the page, an outage, a refusal or a slow answer all end the same way, at the start
 * page, so a sign-in never ends in OzID's "this address is not allowed" state because of a page the product
 * offered.
 */
export async function confirmedReturn(
  wanted: string,
  start: string,
  confirm: (path: string) => Promise<boolean>,
  timeoutMs = 3000,
): Promise<string> {
  if (wanted === start) return start;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const answer = await Promise.race([
    confirm(wanted).catch(() => false),
    new Promise<false>((resolve) => {
      timer = setTimeout(() => resolve(false), timeoutMs);
    }),
  ]);
  clearTimeout(timer);
  return answer ? wanted : start;
}

/**
 * Where a product's old mail landing page hands the browser over: the same fixed path on the user center
 * (`/account/verify-email` or `/account/reset-password`), with the one-time token in the fragment, as the
 * mail itself carries it. The address is built from the build's OzID origin, the landing kind (one of two
 * constants) and a token of the issued shape; nothing from the page's own URL (query, referrer, other
 * fragment parts) can change where it leads, and a token that does not have the issued shape is not carried.
 */
export function mailHandOffUrl(ozidOrigin: string, kind: LandingKind, token: string | null): string {
  const path = kind === "verify-email" ? VERIFY_EMAIL_PATH : RESET_PASSWORD_PATH;
  return `${ozidOrigin.replace(/\/+$/, "")}${path}${token !== null && MAIL_TOKEN.test(token) ? `#token=${token}` : ""}`;
}
