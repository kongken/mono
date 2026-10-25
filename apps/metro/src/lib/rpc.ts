import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { createAccountApi } from "@kongken/account/api";
import { AuthService } from "@kongken/api-client/bff/v1/auth_pb";
import { CatalogService } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { RiderService } from "@kongken/api-client/bff/metro/v1/rider_pb";
import { APP_ID, resolveApiBase } from "./ozid.ts";
import { createApiTransport } from "./transport.ts";

export { APP_ID };

const resolved = resolveApiBase({ VITE_API_BASE_URL: import.meta.env.VITE_API_BASE_URL as string | undefined }, import.meta.env.DEV);
if (!resolved.ok) throw new Error(`@kongken/metro cannot start: ${resolved.error}`);

/** The BFF's canonical public RPC base: the session Cookie belongs to that host, which is how the session is shared with OzID. */
export const apiBase = resolved.base;

const withCredentials = (input: RequestInfo | URL, init?: RequestInit) => fetch(input, { ...init, credentials: "include" });

let csrf: string | undefined;
let expectedAccount: string | undefined;

/** Names the account this page acts for on every authenticated call (see `apiHeaders`); undefined when signed out. */
export function setTransportAccount(userId?: string) {
  expectedAccount = userId;
}

// Token issuance goes around the interceptors: it is the repair the replay uses, so it cannot be replayed itself.
const anonymous = createClient(AuthService, createConnectTransport({ baseUrl: apiBase, fetch: withCredentials }));

async function ensureCsrf() {
  csrf = (await anonymous.issueCSRFToken({})).token;
}

const transport = createApiTransport({
  baseUrl: apiBase,
  fetch: withCredentials,
  csrf: () => csrf,
  refresh: ensureCsrf,
  account: () => expectedAccount,
});

export const auth = createClient(AuthService, transport);

/** The shared account client. This product only reads Me, ends its own session and asks the BFF about return pages; every other account step is OzID's. */
export const account = createAccountApi({ transport, appId: APP_ID, ensureCsrf });
export const catalog = createClient(CatalogService, transport);
export const rider = createClient(RiderService, transport);

/**
 * Runs a state-changing call with a fresh CSRF token (the BFF double-submit check). Reads never need it. A token
 * that another tab or OzID replaced in between is repaired once by the transport (see `csrfRetry`).
 */
export async function write<T>(call: () => Promise<T>): Promise<T> {
  await ensureCsrf();
  return call();
}
