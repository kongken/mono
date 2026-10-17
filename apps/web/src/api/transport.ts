import type { Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { resolveApiConfig } from "@/lib/backend";
import { csrfHeader } from "./csrf";
import { REGION_HEADER, REGION_VERSION } from "@/lib/region-catalog";

export const apiConfig = resolveApiConfig({
  VITE_API_BASE_URL: import.meta.env.VITE_API_BASE_URL as string | undefined,
});
export const backendEnabled = apiConfig.enabled;

const withCredentials = (input: RequestInfo | URL, init?: RequestInit) =>
  fetch(input, { ...init, credentials: "include" });

const schemaHeaders: Interceptor = (next) => (req) => {
  req.header.set("X-Tripmap-Schema", "2");
  req.header.set(REGION_HEADER, REGION_VERSION);
  const csrf = csrfHeader();
  if (csrf) req.header.set("X-CSRF-Token", csrf);
  return next(req);
};

export const transport = apiConfig.enabled
  ? createConnectTransport({
      baseUrl: apiConfig.baseUrl,
      interceptors: [schemaHeaders],
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
