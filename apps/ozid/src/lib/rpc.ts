import { createConnectTransport } from "@connectrpc/connect-web";
import { createClient } from "@connectrpc/connect";
import { createAccountApi } from "@kongken/account/api";
import { parseApiBase } from "@kongken/account/navigation";
import { AuthService } from "@kongken/api-client/bff/v1/auth_pb";

/** The stable app ID of the user center in the BFF's navigation registry. Not a host name. */
export const APP_ID = "ozid";

const parsed = parseApiBase(import.meta.env.VITE_API_BASE_URL as string | undefined);

/** Set when the public build setting is missing or malformed; the app then renders only this. */
export const configError = parsed.ok ? null : parsed.error;

/** The BFF's canonical public RPC base. OzID never talks to its own origin for the API. */
export const apiBase = parsed.ok ? parsed.value : "https://invalid.invalid";

let csrf: string | undefined;

// The Cookie belongs to the API host and is HttpOnly: the page never reads it and never
// sees a session token. CSRF is the BFF's double submit: the token comes in a response
// body and goes back in a header; the Cookie half is sent by the browser on its own.
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

/** The shared account client. OzID only uses sign-in, Me, sign-out, navigation and reauthentication. */
export const account = createAccountApi({ transport, appId: APP_ID, ensureCsrf });
