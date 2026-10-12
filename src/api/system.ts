import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { SystemService } from "@/gen/tripmap/v1/system_pb";

// Vite proxies /api to the local Go service; production uses a same-origin reverse proxy.
export const systemClient = createClient(
  SystemService,
  createConnectTransport({ baseUrl: "/api" }),
);
export const getBackendStatus = () => systemClient.getStatus({});
