export const PENDING_KEY = "shanhai-oauth-pending-v1";
const PROVIDER = /^[a-z][a-z0-9_-]{0,31}$/;

export type OAuthCallback =
  | { provider: string; code: string; state: string }
  | { provider: string; error: string };

/**
 * Recognises /auth/callback/<provider> under the app's base path and returns
 * the provider's code and state, or its error. Anything else is not a callback.
 */
export function parseOAuthCallback(
  pathname: string,
  search: string,
  base = "/",
): OAuthCallback | null {
  const prefix = base.endsWith("/") ? base : base + "/";
  if (!pathname.startsWith(prefix + "auth/callback/")) return null;
  const provider = pathname.slice((prefix + "auth/callback/").length).replace(/\/$/, "");
  if (!PROVIDER.test(provider)) return null;
  const params = new URLSearchParams(search);
  const error = params.get("error");
  if (error) return { provider, error };
  const code = params.get("code");
  const state = params.get("state");
  if (!code || !state) return { provider, error: "missing_code" };
  return { provider, code, state };
}

type Store = Pick<Storage, "getItem" | "setItem" | "removeItem">;

/** Remember which authorization attempt this browser started. */
export function savePending(store: Store, provider: string, state: string) {
  store.setItem(PENDING_KEY, JSON.stringify({ provider, state }));
}

/**
 * True only if the callback matches the attempt this browser started; the
 * pending record is consumed either way so a state cannot be replayed. Without
 * this check an attacker could sign a victim in to the attacker's account.
 */
export function consumePending(
  store: Store,
  callback: { provider: string; state?: string },
): boolean {
  let pending: { provider?: unknown; state?: unknown } | null = null;
  try {
    pending = JSON.parse(store.getItem(PENDING_KEY) ?? "null");
  } catch {
    pending = null;
  }
  store.removeItem(PENDING_KEY);
  return (
    !!pending &&
    typeof pending.state === "string" &&
    pending.state !== "" &&
    pending.provider === callback.provider &&
    pending.state === callback.state
  );
}
