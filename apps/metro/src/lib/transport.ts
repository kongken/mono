import type { Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { csrfRetry } from "@kongken/account/csrf-retry";

/**
 * The product's Connect transport without anything that reads the build environment or the page, so the exact
 * request structure (headers, body, order of interceptors) can be asserted by a plain Node test.
 */
export type ApiTransportParts = {
  baseUrl: string;
  fetch: typeof fetch;
  /** The CSRF token this page holds; the BFF compares it with the shared Cookie. */
  csrf: () => string | undefined;
  /** Asks the BFF for a fresh token (and Cookie). */
  refresh: () => Promise<unknown>;
  /** The account this page believes it is; narrows the shared Cookie's identity (see `apiHeaders`). */
  account: () => string | undefined;
};

/**
 * Sets what every request carries. The account guard: the session Cookie is shared by every tab and every
 * product, so another tab may have signed in as someone else since this page last asked Me. The page names the
 * account it believes it is acting for, and the BFF refuses an authenticated call whose Cookie is somebody else's,
 * instead of writing the first account's change into the second account's data. The header never authenticates
 * or selects an account; `Me` is how the page learns who it is, so it does not carry it. It is set once on the
 * request and stays, so a repeat after a refused token carries the same account whatever happened meanwhile.
 */
export function apiHeaders(parts: Pick<ApiTransportParts, "csrf" | "account">): Interceptor {
  return (next) => (req) => {
    const account = parts.account();
    if (account && !req.url.endsWith("/Me") && !req.header.has("X-Kongken-User")) req.header.set("X-Kongken-User", account);
    const csrf = parts.csrf();
    if (csrf) req.header.set("X-CSRF-Token", csrf);
    return next(req);
  };
}

export function createApiTransport(parts: ApiTransportParts) {
  return createConnectTransport({
    baseUrl: parts.baseUrl,
    // The replay wraps the header interceptor: a repeat after a stale token picks up the fresh one.
    interceptors: [csrfRetry(parts.refresh), apiHeaders(parts)],
    fetch: parts.fetch,
  });
}
