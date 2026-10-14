import type { Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { resolveApiConfig } from "@/lib/backend";
import { getToken } from "./token";
import { REGION_HEADER, REGION_VERSION } from "@/lib/region-catalog";

// The backend is optional: without VITE_API_BASE_URL the app stays local-only
// and no transport or client is created, so no request can reach a missing server.
export const apiConfig = resolveApiConfig({
  VITE_API_BASE_URL: import.meta.env.VITE_API_BASE_URL as string | undefined,
});
export const backendEnabled = apiConfig.enabled;

const bearer: Interceptor = (next) => (req) => {
  const token = getToken();
  if (token) req.header.set("Authorization", `Bearer ${token}`);
  // Declares that this client understands several trips per city. A backend that
  // knows the header refuses clients without it once an account holds trips, so an
  // older page can never overwrite them; one that does not know it is detected by
  // the schema fields missing from its replies.
  req.header.set("X-Tripmap-Schema", "2");
  req.header.set(REGION_HEADER, REGION_VERSION);
  return next(req);
};

export const transport = apiConfig.enabled
  ? createConnectTransport({ baseUrl: apiConfig.baseUrl, interceptors: [bearer] })
  : null;
