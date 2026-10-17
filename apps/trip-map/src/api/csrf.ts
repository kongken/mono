import { createClient } from "@connectrpc/connect";
import { AuthService } from "@kongken/api-client/bff/v1/auth_pb";
import { anonymousTransport } from "./transport";

let token: string | null = null;

const client = () => {
  if (!anonymousTransport) throw new Error("backend not configured");
  return createClient(AuthService, anonymousTransport);
};

/** Fetches a CSRF token and cookie before a state-changing auth call. */
export async function refreshCSRF(): Promise<string> {
  const res = await client().issueCSRFToken({});
  token = res.token;
  return token;
}

export function csrfHeader(): string | undefined {
  return token ?? undefined;
}
