import { createClient } from "@connectrpc/connect";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { AuthService } from "@/gen/tripmap/v1/auth_pb";
import type { User } from "@/gen/tripmap/v1/auth_pb";
import type { StoredSession } from "@/lib/session";
import { transport } from "./transport";

const raw = transport ? createClient(AuthService, transport) : null;
// Only reachable once a session exists, which requires a configured backend.
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

type SessionReply = {
  token: string;
  user?: User;
  expiresAt?: Parameters<typeof timestampDate>[0];
};

/** Turn a login-like reply into what the browser stores. */
export function toStoredSession(reply: SessionReply): StoredSession {
  if (!reply.token || !reply.user) throw new Error("incomplete session reply");
  return {
    token: reply.token,
    expiresAt: reply.expiresAt ? timestampDate(reply.expiresAt).getTime() : 0,
    user: {
      id: reply.user.id,
      username: reply.user.username,
      displayName: reply.user.displayName,
      avatarUrl: reply.user.avatarUrl,
      ...(reply.user.deletionPending ? { deletionPending: true } : {}),
    },
  };
}

export const authApi = {
  register: async (username: string, password: string, displayName: string) =>
    toStoredSession(
      await client().register({ username, password, displayName }),
    ),
  login: async (username: string, password: string) =>
    toStoredSession(await client().login({ username, password })),
  logout: () => client().logout({}),
  deleteAccount: (password: string) =>
    client().deleteAccount({ confirmation: "DELETE", password }),
  me: async () => (await client().me({})).user,
  listSessions: async () =>
    (await client().listSessions({})).sessions.map((s) => ({
      id: s.id,
      current: s.current,
      createdAt: s.createdAt ? timestampDate(s.createdAt) : null,
      lastUsedAt: s.lastUsedAt ? timestampDate(s.lastUsedAt) : null,
    })),
  revokeSession: (id: string) => client().revokeSession({ id }),
  listProviders: async () =>
    (await client().listOAuthProviders({})).providers.map((p) => ({
      name: p.name,
      displayName: p.displayName || p.name,
    })),
  beginOAuth: (provider: string) => client().beginOAuthFlow({ provider }),
  completeOAuth: async (provider: string, code: string, state: string) =>
    toStoredSession(
      await client().completeOAuthFlow({ provider, code, state }),
    ),
};
export type SessionInfo = Awaited<
  ReturnType<typeof authApi.listSessions>
>[number];
export type ProviderInfo = Awaited<
  ReturnType<typeof authApi.listProviders>
>[number];
