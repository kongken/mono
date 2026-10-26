/**
 * The mailed links carry their one-time token in the URL fragment (`#token=...`), never in
 * the path or query, so it is not sent to any server, proxy log or Referer. The landing
 * page reads it into memory once and removes it from the address bar at once. It is
 * never written to localStorage or sessionStorage.
 */

import { landingKind, MAIL_TOKEN, type LandingKind } from "./paths.ts";

export { landingKind, RESET_PASSWORD_PATH, VERIFY_EMAIL_PATH, type LandingKind } from "./paths.ts";

/** The token of a `#token=...` fragment, or null when it is absent or not URL-safe base64. */
export function parseFragmentToken(hash: string): string | null {
  const raw = hash.startsWith("#") ? hash.slice(1) : hash;
  for (const pair of raw.split("&")) {
    const eq = pair.indexOf("=");
    if (eq < 0 || pair.slice(0, eq) !== "token") continue;
    const value = pair.slice(eq + 1);
    return MAIL_TOKEN.test(value) ? value : null;
  }
  return null;
}

type LocationLike = { pathname: string; search: string; hash: string };
type HistoryLike = { replaceState(state: unknown, unused: string, url?: string | URL | null): void };
type DocumentLike = {
  head: { appendChild(node: unknown): unknown };
  createElement(tag: string): { name: string; content: string };
  querySelector(selector: string): unknown;
};

type Captured = { kind: LandingKind; token: string | null };
let captured: Captured | null = null;

/**
 * Reads the token of a landing page and strips the fragment. Call it before anything else
 * runs (main.tsx): the first call reads and clears, later calls return the same in-memory
 * result, so React's double render in development cannot lose the token. Off the landing
 * paths nothing is touched.
 */
export function captureLandingToken(
  env: { location: LocationLike; history: HistoryLike; document: DocumentLike; base?: string } = {
    location: window.location,
    history: window.history,
    document,
    base: import.meta.env?.BASE_URL,
  },
): Captured | null {
  if (captured) return captured;
  const kind = landingKind(env.location.pathname, env.base ?? "/");
  if (!kind) return null;
  const token = parseFragmentToken(env.location.hash);
  // Remove the whole fragment, whatever it held, before any other script can read it.
  env.history.replaceState(null, "", env.location.pathname + env.location.search);
  // Requests made from here on carry no Referer.
  if (!env.document.querySelector('meta[name="referrer"]')) {
    const meta = env.document.createElement("meta");
    meta.name = "referrer";
    meta.content = "no-referrer";
    env.document.head.appendChild(meta);
  }
  captured = { kind, token };
  return captured;
}

/**
 * A second mail link opened in the same tab only changes the fragment (no reload): read it
 * the same way. `onToken` runs after the new token replaced the held one.
 */
export function watchLandingFragment(
  onToken: () => void,
  env: { location: LocationLike; history: HistoryLike; addEventListener: (type: "hashchange", fn: () => void) => void; removeEventListener: (type: "hashchange", fn: () => void) => void } = {
    location: window.location,
    history: window.history,
    addEventListener: (t, f) => window.addEventListener(t, f),
    removeEventListener: (t, f) => window.removeEventListener(t, f),
  },
): () => void {
  const listener = () => {
    if (!captured || !env.location.hash) return;
    const token = parseFragmentToken(env.location.hash);
    env.history.replaceState(null, "", env.location.pathname + env.location.search);
    captured = { ...captured, token };
    onToken();
  };
  env.addEventListener("hashchange", listener);
  return () => env.removeEventListener("hashchange", listener);
}

/** The token held in memory for the landing page; null if the link had none or it was used. */
export function heldToken(): string | null {
  return captured?.token ?? null;
}

/** Forgets the token after a successful (or definitively refused) submission. */
export function releaseToken() {
  if (captured) captured = { ...captured, token: null };
}

/** For tests. */
export function resetCapturedForTest() {
  captured = null;
}
