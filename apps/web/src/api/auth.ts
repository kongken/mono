import { createClient } from "@connectrpc/connect";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { AuthService } from "@trip-map/api-client/bff/v1/auth_pb";
import type { User } from "@trip-map/api-client/bff/v1/auth_pb";
import type { StoredSession } from "@/lib/session";
import { refreshCSRF } from "./csrf";
import { transport } from "./transport";

const raw = transport ? createClient(AuthService, transport) : null;
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

function toUser(u: User | undefined): StoredSession["user"] {
  if (!u?.id) throw new Error("incomplete user");
  return {
    id: u.id,
    username: u.username,
    displayName: u.displayName,
    avatarUrl: u.avatarUrl,
    ...(u.deletionPending ? { deletionPending: true } : {}),
  };
}

export function toStoredSession(reply: {
  user?: User;
  expiresAt?: Parameters<typeof timestampDate>[0];
}): StoredSession {
  return {
    user: toUser(reply.user),
    expiresAt: reply.expiresAt
      ? timestampDate(reply.expiresAt).getTime()
      : undefined,
  };
}

export const authApi = {
  register: async (username: string, password: string, displayName: string) => {
    await refreshCSRF();
    return toStoredSession(
      await client().register({ username, password, displayName }),
    );
  },
  login: async (username: string, password: string) => {
    await refreshCSRF();
    return toStoredSession(await client().login({ username, password }));
  },
  logout: async () => {
    await refreshCSRF();
    await client().logout({});
  },
  deleteAccount: (_password: string) => {
    throw new Error("deleteAccount not migrated to bff.v1 yet");
  },
  me: async () => toUser((await client().me({})).user),
  listSessions: async () => {
    const res = await client().listSessions({});
    return (res.sessions ?? []).map((s) => ({
      id: s.id,
      current: s.current,
      createdAt: s.createdAt ? timestampDate(s.createdAt) : null,
      lastUsedAt: s.lastUsedAt ? timestampDate(s.lastUsedAt) : null,
    }));
  },
  revokeSession: async (id: string) => {
    await refreshCSRF();
    await client().revokeSession({ id });
  },
  listProviders: (): Promise<ProviderInfo[]> =>
    Promise.reject(new Error("OAuth not migrated to bff.v1 yet (#32)")),
  beginOAuth: (_provider: string): Promise<{ authorizeUrl: string; state: string }> =>
    Promise.reject(new Error("OAuth not migrated to bff.v1 yet (#32)")),
  completeOAuth: (_provider: string, _code: string, _state: string): Promise<StoredSession> =>
    Promise.reject(new Error("OAuth not migrated to bff.v1 yet (#32)")),
};

export type SessionInfo = {
  id: string;
  current: boolean;
  createdAt: Date | null;
  lastUsedAt: Date | null;
};
export type ProviderInfo = { name: string; displayName: string };
