import { createAccountApi, type AccountApi } from "@kongken/account/api";
import { refreshCSRF } from "./csrf";
import { transport } from "./transport";

/** The account client of this app; null in local mode (no `VITE_API_BASE_URL`). */
export const account: AccountApi | null = transport
  ? createAccountApi({
      transport,
      appId: "trip-map",
      ensureCsrf: async () => {
        await refreshCSRF();
      },
    })
  : null;

export function requireAccount(): AccountApi {
  if (!account) throw new Error("backend not configured");
  return account;
}
