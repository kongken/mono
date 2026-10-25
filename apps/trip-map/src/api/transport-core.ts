import type { Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { csrfRetry } from "../lib/csrf-retry.ts";

/**
 * The product's Connect transport without anything that reads the build environment or the page, so the
 * exact request structure (headers, body, order of interceptors) can be asserted by a plain Node test.
 */
export type ApiTransportParts = {
  baseUrl: string;
  fetch: typeof fetch;
  /** The CSRF token this page holds; the BFF compares it with the shared Cookie. */
  csrf: () => string | undefined;
  /** Asks the BFF for a fresh token (and Cookie). */
  refresh: () => Promise<unknown>;
  /** The account this page believes it is; narrows the shared Cookie's identity (see below). */
  account: () => string | undefined;
  /** Fixed headers every request carries (schema and region version). */
  fixed: () => Record<string, string>;
  /** The page's origin, sent explicitly because the BFF checks it on writes. */
  origin?: () => string | undefined;
};

/**
 * Sets what every request carries. The account guard: a request that already names its account (the footprint
 * client does, per call) keeps it; otherwise the page's current account is used. It is set once on the request
 * and stays, so a repeat after a refused token carries the same account whatever happened meanwhile.
 */
export function apiHeaders(parts: ApiTransportParts): Interceptor {
  return (next) => (req) => {
    for (const [name, value] of Object.entries(parts.fixed())) req.header.set(name, value);
    const account = parts.account();
    if (account && !req.url.endsWith("/Me") && !req.header.has("X-Kongken-User")) req.header.set("X-Kongken-User", account);
    const csrf = parts.csrf();
    if (csrf) req.header.set("X-CSRF-Token", csrf);
    const origin = parts.origin?.();
    if (origin) req.header.set("Origin", origin);
    return next(req);
  };
}

export function createApiTransport(parts: ApiTransportParts) {
  return createConnectTransport({
    baseUrl: parts.baseUrl,
    // The retry wraps the header interceptor: a repeat after a stale token picks up the fresh one.
    interceptors: [csrfRetry(parts.refresh), apiHeaders(parts)],
    fetch: parts.fetch,
  });
}
