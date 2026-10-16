import { createClient } from "@connectrpc/connect";
import { SystemService } from "@trip-map/api-client/tripmap/v1/system_pb";
import { transport } from "./transport";

export { apiConfig, backendEnabled } from "./transport";

const systemClient = transport ? createClient(SystemService, transport) : null;

/** Returns null when no backend is configured. */
export const getBackendStatus = () => systemClient?.getStatus({}) ?? null;
