import { timestampDate } from "@bufbuild/protobuf/wkt";
import { UserStatus, type AdminUser } from "@kongken/api-client/bff/v1/user_admin_pb";
import type { UserFilters } from "./routes.ts";
import type { ChangeKind } from "./change.ts";
import { freshCsrf, userAdmin } from "./rpc";

export type AccountStatus = "active" | "disabled" | "deleting" | "unknown";

/** An account as the console shows it: nothing but what the server chose to send. */
export type UserRow = {
  id: string;
  email: string;
  displayName: string;
  status: AccountStatus;
  roles: string[];
  createdAt: Date | null;
  /** The version of the account this row is of: a change names it, and is refused if the account moved on. */
  authVersion: bigint;
};

const STATUS_OUT: Record<AccountStatus, UserStatus> = {
  active: UserStatus.ACTIVE,
  disabled: UserStatus.DISABLED,
  deleting: UserStatus.DELETING,
  unknown: UserStatus.UNSPECIFIED,
};

function statusOf(s: UserStatus): AccountStatus {
  return s === UserStatus.ACTIVE ? "active" : s === UserStatus.DISABLED ? "disabled" : s === UserStatus.DELETING ? "deleting" : "unknown";
}

const row = (u: AdminUser | undefined): UserRow => {
  if (!u?.userId) throw new Error("incomplete user");
  return { id: u.userId, email: u.email, displayName: u.displayName, status: statusOf(u.status), roles: u.roles, createdAt: u.createdAt ? timestampDate(u.createdAt) : null, authVersion: u.authVersion };
};

export type UserPage = { users: UserRow[]; next: string };

/** One page of the list. The server pages and orders; `token` is the opaque position it gave. */
export async function listUsers(filters: UserFilters, token: string, signal?: AbortSignal): Promise<UserPage> {
  const res = await userAdmin.listUsers(
    {
      email: filters.email.trim(),
      userId: filters.userId.trim(),
      nickname: filters.nickname.trim(),
      status: STATUS_OUT[(filters.status || "unknown") as AccountStatus],
      role: filters.role,
      pageToken: token,
    },
    { signal },
  );
  return { users: res.users.map(row), next: res.nextPageToken };
}

export async function getUser(id: string, signal?: AbortSignal): Promise<UserRow> {
  return row((await userAdmin.getUser({ userId: id }, { signal })).user);
}

/** What the server answered to a change. */
export type ChangeOutcome = { status: AccountStatus; roles: string[]; authVersion: bigint; changed: boolean; replayed: boolean };

/**
 * One change to one account. `key` is the operation ID of this intended change: the same key goes with every
 * retry of the same change (a lost response gets the first answer back), a new one with every new intent.
 * The call carries no operator: the server takes it from the session.
 */
export async function changeUser(kind: ChangeKind, user: UserRow, key: string, reason: string): Promise<ChangeOutcome> {
  await freshCsrf();
  const msg = { userId: user.id, expectedAuthVersion: user.authVersion, mutationId: key, reason };
  const res =
    kind === "disable" ? await userAdmin.disableUser(msg) : kind === "enable" ? await userAdmin.enableUser(msg) : kind === "grant" ? await userAdmin.grantMetroAdmin(msg) : await userAdmin.revokeMetroAdmin(msg);
  return { status: statusOf(res.status), roles: res.roles, authVersion: res.authVersion, changed: res.changed, replayed: res.replayed };
}

export const STATUS_LABEL: Record<AccountStatus, string> = { active: "正常", disabled: "已禁用", deleting: "注销中", unknown: "未知" };
export const ROLE_LABEL: Record<string, string> = { "ozid-admin": "平台管理员", "metro-admin": "地铁目录管理员" };
