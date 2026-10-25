import { Code, ConnectError, type Interceptor } from "@connectrpc/connect";

/**
 * The BFF's CSRF check is a double submit: the X-CSRF-Token header must equal the `kongken_csrf` Cookie, which
 * belongs to the API host and is shared by every tab and every application of the browser. Signing in at OzID (or
 * any other page asking for a token) replaces that Cookie, so the token this page fetched at start can be stale
 * by the time it writes, and the BFF answers "request rejected" (permission denied) before anything runs.
 *
 * On that answer, ask for a fresh token once and repeat the call. Repeating is safe: every write of the product
 * carries a mutation ID the server deduplicates, and a call refused before it ran changed nothing. A second
 * refusal, or a token that cannot be fetched, is the original error: nothing here widens what is allowed.
 * Place it before the interceptor that adds the header, so the repeat picks up the new token.
 */
export function csrfRetry(refresh: () => Promise<unknown>): Interceptor {
  return (next) => async (req) => {
    try {
      return await next(req);
    } catch (error) {
      if (req.stream || ConnectError.from(error).code !== Code.PermissionDenied) throw error;
      try {
        await refresh();
      } catch {
        throw error;
      }
      return next(req);
    }
  };
}
