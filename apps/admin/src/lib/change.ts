// What the console may do to an ordinary account, as pure data and rules so node:test can cover them.
// The server decides every one of these again; this only keeps the screen honest.

export type ChangeKind = "disable" | "enable" | "grant" | "revoke";

/** The slice of an account the rules need. */
export type Target = { status: "active" | "disabled" | "deleting" | "unknown"; roles: string[] };

export const METRO_ADMIN = "metro-admin";
export const PLATFORM_ADMIN = "ozid-admin";

export type ChangeInfo = {
  kind: ChangeKind;
  /** The button and the dialog title. */
  label: string;
  /** What the account becomes, for the "after" line. */
  after: (t: Target) => string;
  /** What happens to the account and to the person behind it. Shown before the confirmation. */
  impact: string[];
  confirm: string;
  /** The result when it did change something. */
  done: string;
};

const SIGN_OUT = "该账号已登录的设备会退出，已创建的管理令牌失效；它需要重新登录，令牌需要重新创建。";

export const CHANGES: Record<ChangeKind, ChangeInfo> = {
  disable: {
    kind: "disable",
    label: "禁用账号",
    after: (t) => `状态：已禁用；角色：${roleText(t.roles)}`,
    impact: ["该账号马上不能再登录或使用任何产品。", SIGN_OUT, "之后重新启用账号，也不会恢复原来的登录状态和令牌。"],
    confirm: "确认禁用",
    done: "账号已禁用，它的登录状态和管理令牌已失效。",
  },
  enable: {
    kind: "enable",
    label: "启用账号",
    after: (t) => `状态：正常；角色：${roleText(t.roles)}`,
    impact: ["该账号可以重新登录。", "禁用期间失效的登录状态和管理令牌不会恢复，需要重新登录、重新创建。"],
    confirm: "确认启用",
    done: "账号已启用。它需要重新登录。",
  },
  grant: {
    kind: "grant",
    label: "授予地铁目录管理员",
    after: (t) => `状态：${statusText(t.status)}；角色：${roleText([...t.roles, METRO_ADMIN])}`,
    impact: ["该账号可以在地铁目录后台修改城市、线路、车型和专题。", SIGN_OUT],
    confirm: "确认授予",
    done: "已授予地铁目录管理员。该账号需要重新登录后才会看到它。",
  },
  revoke: {
    kind: "revoke",
    label: "收回地铁目录管理员",
    after: (t) => `状态：${statusText(t.status)}；角色：${roleText(t.roles.filter((r) => r !== METRO_ADMIN))}`,
    impact: ["该账号不能再使用地铁目录后台。", SIGN_OUT, "之后再次授予，也不会恢复原来的令牌。"],
    confirm: "确认收回",
    done: "已收回地铁目录管理员，该账号的登录状态和管理令牌已失效。",
  },
};

const ROLE_NAME: Record<string, string> = { [PLATFORM_ADMIN]: "平台管理员", [METRO_ADMIN]: "地铁目录管理员" };

export function roleText(roles: string[]): string {
  return roles.length === 0 ? "无" : roles.map((r) => ROLE_NAME[r] ?? r).join("、");
}

export function statusText(s: Target["status"]): string {
  return s === "active" ? "正常" : s === "disabled" ? "已禁用" : s === "deleting" ? "注销中" : "未知";
}

/**
 * What the console offers for an account, and why nothing when nothing:
 * - a platform administrator is never changed here (that is an operator's job, on the command line);
 * - a closing account is read-only, and "enable" would not cancel the closing;
 * - a role can only be granted to an account in good standing.
 */
export type Offer =
  | { kind: "protected" }
  | { kind: "closing" }
  | { kind: "unknown" }
  | { kind: "actions"; status: ChangeKind; role: ChangeKind; roleBlocked: string | null };

export function offerFor(t: Target): Offer {
  if (t.roles.includes(PLATFORM_ADMIN)) return { kind: "protected" };
  if (t.status === "deleting") return { kind: "closing" };
  if (t.status === "unknown") return { kind: "unknown" };
  const holds = t.roles.includes(METRO_ADMIN);
  return {
    kind: "actions",
    status: t.status === "active" ? "disable" : "enable",
    role: holds ? "revoke" : "grant",
    roleBlocked: !holds && t.status !== "active" ? "账号已禁用，需要先启用才能授予角色" : null,
  };
}

const EMAIL_LIKE = /[^\s@]+@[^\s@]+\.[^\s@]+/;
const TOKEN_LIKE = /[A-Za-z0-9_+/=-]{32,}/;
// eslint-disable-next-line no-control-regex
const CONTROL = /[\u0000-\u001f\u007f-\u009f]/;

/** The reason's rules, the same as the server's: 1-200 characters, no controls, nothing that looks like a secret. */
export function checkReason(raw: string): string | null {
  const reason = raw.trim();
  const length = [...reason].length;
  if (length === 0) return "请填写原因，它会记入审计记录";
  if (length > 200) return "原因最多 200 个字符";
  if (CONTROL.test(reason)) return "原因不能包含控制字符";
  if (EMAIL_LIKE.test(reason) || TOKEN_LIKE.test(reason) || reason.includes("mcp_")) return "原因里不要写邮箱、密码或令牌";
  return null;
}

/** A new operation ID: 1-64 characters of A-Z a-z 0-9 . _ - (the server's alphabet). */
export function newMutationId(random: () => string = () => crypto.randomUUID()): string {
  return random().replace(/[^A-Za-z0-9._-]/g, "").slice(0, 64);
}
