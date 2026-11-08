import { ozidUrl, parseApiBase, parseOrigin } from "@kongken/account/navigation";

/**
 * Where this page finds the BFF and the shared user center. Nothing here holds a session or a secret: the
 * addresses are public build settings, and the session Cookie belongs to the API host.
 */

/** The product's stable app ID in the BFF's navigation registry (and the id usersrv knows it by). */
export const APP_ID = "timeline";

/**
 * The BFF's RPC base. A build names it as an absolute address (the session Cookie belongs to the API host, so no
 * same-origin `/api` proxy of the product can carry the shared session). Only the development server may fall
 * back to its own `/api` proxy, which is for local work.
 */
export function resolveApiBase(env: { VITE_API_BASE_URL?: string }, dev: boolean): { ok: true; base: string } | { ok: false; error: string } {
  if ((env.VITE_API_BASE_URL ?? "").trim() === "" && dev) return { ok: true, base: "/api" };
  const parsed = parseApiBase(env.VITE_API_BASE_URL);
  return parsed.ok ? { ok: true, base: parsed.value } : { ok: false, error: parsed.error };
}

export type OzidTargets = { ok: true; origin: string; login: string; home: string } | { ok: false; error: string };

/**
 * OzID's pages. Signing in is OzID's own page and it decides whether this product may bring the browser back to
 * its start page; the page only names itself and a path.
 */
export function resolveOzid(env: { VITE_OZID_ORIGIN?: string }): OzidTargets {
  const parsed = parseOrigin(env.VITE_OZID_ORIGIN, "VITE_OZID_ORIGIN");
  if (!parsed.ok) return { ok: false, error: parsed.error };
  return {
    ok: true,
    origin: parsed.value,
    login: ozidUrl(parsed.value, "login", { appId: APP_ID, returnPath: "/" }),
    home: parsed.value,
  };
}
