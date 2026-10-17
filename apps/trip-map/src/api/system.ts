import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { StatusService } from "@kongken/api-client/bff/tripmap/v1/status_pb";
import { resolveApiConfig } from "@/lib/backend";

// The status query is anonymous (bff.tripmap.v1.StatusService): its own transport sends no
// Authorization header and no cookies, and never touches the session or the account
// cache, so the /status page can use it without reading any user data.
export const statusApi = resolveApiConfig({
  VITE_API_BASE_URL: import.meta.env.VITE_API_BASE_URL as string | undefined,
});

const statusClient = statusApi.enabled
  ? createClient(StatusService, createConnectTransport({ baseUrl: statusApi.baseUrl, fetch: (input, init) => fetch(input, { ...init, credentials: "omit" }) }))
  : null;

/** Returns null when no backend is configured. */
export const getBackendStatus = (signal?: AbortSignal) => statusClient?.getStatus({}, { signal }) ?? null;
