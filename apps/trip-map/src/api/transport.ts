import type { Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { resolveApiConfig } from "@/lib/backend";
import { csrfRetry } from "@/lib/csrf-retry";
import { csrfHeader, refreshCSRF } from "./csrf";
import { REGION_HEADER, REGION_VERSION } from "@/lib/region-catalog";

export const apiConfig = resolveApiConfig({
  VITE_API_BASE_URL: import.meta.env.VITE_API_BASE_URL as string | undefined,
});
export const backendEnabled = apiConfig.enabled;

const withCredentials = (input: RequestInfo | URL, init?: RequestInit) =>
  fetch(input, { ...init, credentials: "include" });

let expectedAccount: string | undefined;
/** Guard requests against another application's replacement of the shared Cookie. */
export function setTransportAccount(userId?: string) { expectedAccount = userId; }

const schemaHeaders: Interceptor = (next) => (req) => {
  req.header.set("X-Tripmap-Schema", "2");
  if (expectedAccount && !req.url.endsWith("/Me") && !req.header.has("X-Kongken-User"))
    req.header.set("X-Kongken-User", expectedAccount);
  req.header.set(REGION_HEADER, REGION_VERSION);
  const csrf = csrfHeader();
  if (csrf) req.header.set("X-CSRF-Token", csrf);
  if (typeof window !== "undefined") {
    req.header.set("Origin", window.location.origin);
  }
  return next(req);
};

export const transport = apiConfig.enabled
  ? createConnectTransport({
      baseUrl: apiConfig.baseUrl,
      // The retry wraps the header interceptor: a repeat after a stale token picks up the fresh one.
      interceptors: [csrfRetry(refreshCSRF), schemaHeaders],
      fetch: withCredentials,
    })
  : null;

/** Anonymous transport (status, CSRF issuance) — still sends cookies when present. */
export const anonymousTransport = apiConfig.enabled
  ? createConnectTransport({
      baseUrl: apiConfig.baseUrl,
      fetch: withCredentials,
    })
  : null;
