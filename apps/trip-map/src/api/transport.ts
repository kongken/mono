import { createConnectTransport } from "@connectrpc/connect-web";
import { resolveApiConfig } from "@/lib/backend";
import { createApiTransport } from "./transport-core";
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

export const transport = apiConfig.enabled
  ? createApiTransport({
      baseUrl: apiConfig.baseUrl,
      fetch: withCredentials,
      csrf: csrfHeader,
      refresh: refreshCSRF,
      account: () => expectedAccount,
      fixed: () => ({ "X-Tripmap-Schema": "2", [REGION_HEADER]: REGION_VERSION }),
      origin: () => (typeof window !== "undefined" ? window.location.origin : undefined),
    })
  : null;

/** Anonymous transport (status, CSRF issuance) — still sends cookies when present. */
export const anonymousTransport = apiConfig.enabled
  ? createConnectTransport({
      baseUrl: apiConfig.baseUrl,
      fetch: withCredentials,
    })
  : null;
