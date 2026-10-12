import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { SystemService } from "@/gen/tripmap/v1/system_pb";
import { resolveApiConfig } from "@/lib/backend";

// The backend is optional: without VITE_API_BASE_URL the app stays local-only
// and no client is created, so no request can reach a missing server.
export const apiConfig = resolveApiConfig({
  VITE_API_BASE_URL: import.meta.env.VITE_API_BASE_URL as string | undefined,
});
export const backendEnabled = apiConfig.enabled;

const systemClient = apiConfig.enabled
  ? createClient(
      SystemService,
      createConnectTransport({ baseUrl: apiConfig.baseUrl }),
    )
  : null;

/** Returns null when no backend is configured. */
export const getBackendStatus = () => systemClient?.getStatus({}) ?? null;
