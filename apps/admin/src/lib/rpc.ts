import { createConnectTransport } from "@connectrpc/connect-web";
import { createClient } from "@connectrpc/connect";
import { createAccountApi } from "@kongken/account/api";
import { parseApiBase, parseOrigin } from "@kongken/account/navigation";
import { AuthService } from "@kongken/api-client/bff/v1/auth_pb";
import { UserAdminService } from "@kongken/api-client/bff/v1/user_admin_pb";

/** The stable app ID of the console in the BFF's navigation registry. Not a host name. */
export const APP_ID = "admin";

const api = parseApiBase(import.meta.env.VITE_API_BASE_URL as string | undefined);
const ozid = parseOrigin(import.meta.env.VITE_OZID_ORIGIN as string | undefined, "VITE_OZID_ORIGIN");

/** Set when a public build setting is missing or malformed; the app then renders only this. */
export const configError = !api.ok ? api.error : !ozid.ok ? ozid.error : null;

/** The BFF's canonical public RPC base. The console never talks to its own origin for the API. */
export const apiBase = api.ok ? api.value : "https://invalid.invalid";

/** OzID's origin: where sign-in and password confirmation happen. The console has no login form of its own. */
export const ozidOrigin = ozid.ok ? ozid.value : "https://invalid.invalid";

let csrf: string | undefined;

// The Cookie belongs to the API host and is HttpOnly: the page never reads it and never sees a
// session token. CSRF is the BFF's double submit: the token comes in a response body and goes
// back in a header; the Cookie half is sent by the browser on its own. The user queries are
// reads and need none; sign-out and every account change do.
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

const auth = createClient(AuthService, transport);

async function ensureCsrf() {
  csrf = (await auth.issueCSRFToken({})).token;
}

/** A fresh CSRF token for a write. Fetched at the moment of the write, so a token that expired never fails it. */
export const freshCsrf = ensureCsrf;

/** The shared account client. The console only uses Me, sign-out and navigation resolution. */
export const account = createAccountApi({ transport, appId: APP_ID, ensureCsrf });

/** The user management API: three reads (users, one user, the account audit) and four narrow changes (disable, enable, grant and revoke metro-admin). */
export const userAdmin = createClient(UserAdminService, transport);
