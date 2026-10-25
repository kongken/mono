import { Code, ConnectError, type Interceptor } from "@connectrpc/connect";

/**
 * The response header with which the BFF marks the one refusal a fresh token repairs: the double-submit CSRF
 * check failed before anything ran (`X-Bff-Rejection: csrf`, see internal/bff/ingress). The BFF sends it on
 * nothing else: an Origin, perimeter, role, session or business refusal looks the same (permission denied,
 * "request rejected") but has no marker, and reads are never CSRF-checked, so they never get one.
 */
export const REJECTION_HEADER = "x-bff-rejection";
export const REJECTION_CSRF = "csrf";

/**
 * The BFF's CSRF check is a double submit: the X-CSRF-Token header must equal the `kongken_csrf` Cookie, which
 * belongs to the API host and is shared by every tab and every application of the browser. Signing in at OzID (or
 * any other page asking for a token) replaces that Cookie, so the token this page fetched at start can be stale
 * by the time it writes, and the BFF refuses the write before anything runs.
 *
 * Only that marked refusal of a unary call is repeated, and only once: ask for a fresh token and send the same
 * request again. It is the same request object, so the repeat keeps its body (and so its mutation ID, which the
 * server deduplicates) and the account header it already carries; only the token is replaced. A refusal without
 * the marker, a second marked refusal, a token that cannot be fetched, a stream, or any other error is the
 * original error: nothing here widens what is allowed or repeats what was not refused for this reason.
 * Place it before the interceptor that adds the token, so the repeat picks up the new one.
 */
export function csrfRetry(refresh: () => Promise<unknown>): Interceptor {
  return (next) => async (req) => {
    try {
      return await next(req);
    } catch (error) {
      if (req.stream || !isCsrfRefusal(error)) throw error;
      try {
        await refresh();
      } catch {
        throw error;
      }
      return next(req);
    }
  };
}

/** True for a permission-denied answer that carries the BFF's CSRF marker. */
export function isCsrfRefusal(error: unknown): boolean {
  const e = ConnectError.from(error);
  return e.code === Code.PermissionDenied && e.metadata.get(REJECTION_HEADER) === REJECTION_CSRF;
}
