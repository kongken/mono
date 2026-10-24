import { createClient } from "@connectrpc/connect";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { AuthService } from "@kongken/api-client/bff/v1/auth_pb";
import type { User } from "@kongken/api-client/bff/v1/auth_pb";
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
    displayName: u.displayName,
    avatarUrl: u.avatarUrl,
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
  logout: async () => {
    await refreshCSRF();
    await client().logout({});
  },
  deleteAccount: async () => {
    await refreshCSRF();
    await client().deleteAccount({ confirmation: "DELETE" });
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
};

export type SessionInfo = {
  id: string;
  current: boolean;
  createdAt: Date | null;
  lastUsedAt: Date | null;
};
