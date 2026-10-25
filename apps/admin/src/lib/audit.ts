import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { AuditChannel, type SecurityAuditEntry, type SecurityAuditOrigin } from "@kongken/api-client/bff/v1/user_admin_pb";
import type { AuditFilters } from "./routes.ts";
import { userAdmin } from "./rpc";

export type Channel = "web" | "cli" | "self" | "system" | "unknown";

/** The original change a replayed row points at. */
export type AuditOriginRow = { id: string; createdAt: Date | null; action: string; result: string; beforeStatus: string; afterStatus: string; beforeRoles: string[]; afterRoles: string[] };

/** A row of the account audit as the console shows it: identifiers, classifications and the stated reason only. */
export type AuditRow = {
  id: string;
  createdAt: Date | null;
  channel: Channel;
  /** The account behind a web change or read (or the holder of a self-service event); "" for the CLI and the system. */
  operatorId: string;
  /** The controlled operator name of a CLI change. Never shown as a web identity. */
  operatorLabel: string;
  userId: string;
  action: string;
  result: string;
  reason: string;
  requestId: string;
  mutationId: string;
  beforeStatus: string;
  afterStatus: string;
  beforeRoles: string[];
  afterRoles: string[];
  originId: string;
  origin: AuditOriginRow | null;
};

const CHANNEL_OUT: Record<string, AuditChannel> = { web: AuditChannel.WEB, cli: AuditChannel.CLI, self: AuditChannel.SELF, system: AuditChannel.SYSTEM };

function channelOf(c: AuditChannel): Channel {
  return c === AuditChannel.WEB ? "web" : c === AuditChannel.CLI ? "cli" : c === AuditChannel.SELF ? "self" : c === AuditChannel.SYSTEM ? "system" : "unknown";
}

const when = (t: Timestamp | undefined) => (t ? timestampDate(t) : null);

const originOf = (o: SecurityAuditOrigin | undefined): AuditOriginRow | null =>
  o ? { id: o.id, createdAt: when(o.createdAt), action: o.action, result: o.result, beforeStatus: o.beforeStatus, afterStatus: o.afterStatus, beforeRoles: o.beforeRoles, afterRoles: o.afterRoles } : null;

const rowOf = (e: SecurityAuditEntry): AuditRow => ({
  id: e.id,
  createdAt: when(e.createdAt),
  channel: channelOf(e.channel),
  operatorId: e.operatorUserId,
  operatorLabel: e.operatorLabel,
  userId: e.userId,
  action: e.action,
  result: e.result,
  reason: e.reason,
  requestId: e.requestId,
  mutationId: e.mutationId,
  beforeStatus: e.beforeStatus,
  afterStatus: e.afterStatus,
  beforeRoles: e.beforeRoles,
  afterRoles: e.afterRoles,
  originId: e.originId,
  origin: originOf(e.origin),
});

export type AuditPage = { rows: AuditRow[]; next: string };

/** One page of the account audit. The server filters, orders and pages; `token` is the opaque position it gave. */
export async function listAudit(filters: AuditFilters, token: string, signal?: AbortSignal): Promise<AuditPage> {
  const date = (v: string) => (v ? timestampFromDate(new Date(v)) : undefined);
  const res = await userAdmin.listSecurityAudit(
    {
      userId: filters.userId.trim(),
      operatorUserId: filters.operatorId.trim(),
      channel: CHANNEL_OUT[filters.channel] ?? AuditChannel.UNSPECIFIED,
      action: filters.action.trim(),
      result: filters.result,
      mutationId: filters.mutationId.trim(),
      from: date(filters.from),
      to: date(filters.to),
      pageToken: token,
    },
    { signal },
  );
  return { rows: res.entries.map(rowOf), next: res.nextPageToken };
}

export const CHANNEL_LABEL: Record<Channel, string> = { web: "网页控制台", cli: "命令行（运维）", self: "账号本人", system: "系统", unknown: "未知" };

const ACTION_LABEL: Record<string, string> = {
  "admin.account.disable": "禁用账号",
  "admin.account.enable": "启用账号",
  "admin.role.grant:metro-admin": "授予地铁目录管理员",
  "admin.role.revoke:metro-admin": "收回地铁目录管理员",
  "admin.users.list": "查询用户列表",
  "admin.users.get": "查看账号详情",
  "admin.audit.list": "查询账号审计",
  "role.grant:metro-admin": "授予地铁目录管理员",
  "role.revoke:metro-admin": "收回地铁目录管理员",
  "role.grant:ozid-admin": "授予平台管理员",
  "role.revoke:ozid-admin": "收回平台管理员",
  "account.create": "创建账号",
  "account.disable": "禁用账号",
  "account.enable": "启用账号",
  login: "登录",
  "password.change": "修改密码",
  "password.verify": "验证密码",
  "password.reset": "重置密码",
  "password_reset.request": "申请重置密码",
  "deletion.begin": "开始注销",
};

/** A readable name for an action, with the stored classification kept as the fallback (never hidden). */
export const actionLabel = (a: string) => ACTION_LABEL[a] ?? a;

export const RESULT_LABEL: Record<string, string> = {
  ok: "已完成",
  unchanged: "无变化（已是目标状态）",
  replayed: "重复提交（返回当时的结果）",
  refused_forbidden: "被拒绝：没有权限",
  refused_version_conflict: "被拒绝：账号已被改动",
  refused_target_protected: "被拒绝：受保护的账号",
  refused_not_active: "被拒绝：账号不是正常状态",
  refused_mutation_reuse: "被拒绝：操作编号重复使用",
  refused_platform_admin: "被拒绝：平台管理员",
};

export const resultLabel = (r: string) => RESULT_LABEL[r] ?? r;

/** The results the filter offers: the ones an administrator's work produces. */
export const RESULT_FILTERS = ["ok", "unchanged", "replayed", "refused_forbidden", "refused_version_conflict", "refused_target_protected", "refused_not_active", "refused_mutation_reuse"];

export const STATUS_NAME: Record<string, string> = { active: "正常", disabled: "已禁用", deleting: "注销中" };
