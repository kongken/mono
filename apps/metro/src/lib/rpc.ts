import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { createAccountApi } from "@kongken/account/api";
import { AuthService } from "@kongken/api-client/bff/v1/auth_pb";
import { CatalogService } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { RiderService } from "@kongken/api-client/bff/metro/v1/rider_pb";

/** The BFF's RPC base: a same-origin path in production, set at build time otherwise. */
export const apiBase = ((import.meta.env.VITE_API_BASE_URL as string | undefined)?.trim().replace(/\/+$/, "")) || "/api";

let csrf: string | undefined;

const transport = createConnectTransport({
  baseUrl: apiBase,
  fetch: (input, init) => fetch(input, { ...init, credentials: "include" }),
  interceptors: [
    (next) => (req) => {
      if (csrf) req.header.set("X-CSRF-Token", csrf);
      return next(req);
    },
  ],
});

export const auth = createClient(AuthService, transport);

/** The id usersrv knows this app by; it picks the origin of the links in account mails. */
export const APP_ID = "metro";

async function ensureCsrf() {
  csrf = (await auth.issueCSRFToken({})).token;
}

/** The shared account client: sign-in, registration, password reset, profile, devices, deletion. */
export const account = createAccountApi({ transport, appId: APP_ID, ensureCsrf });
export const catalog = createClient(CatalogService, transport);
export const rider = createClient(RiderService, transport);

/**
 * Runs a state-changing call with a fresh CSRF token (the BFF double-submit
 * check). Reads never need it.
 */
export async function write<T>(call: () => Promise<T>): Promise<T> {
  await ensureCsrf();
  return call();
}
