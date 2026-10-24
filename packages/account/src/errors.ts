import { Code, ConnectError } from "@connectrpc/connect";

/** What the user was doing; the same server code reads differently in each. */
export type AccountAction =
  | "login"
  | "register-request"
  | "register-complete"
  | "reset-request"
  | "reset-complete"
  | "reauthenticate"
  | "change-password"
  | "profile"
  | "delete"
  | "other";

export type AccountErrorKind =
  /** The server could not be reached at all. */
  | "network"
  /** Reached, but usersrv, storage or Redis could not answer; nothing was done. */
  | "unavailable"
  | "rate_limited"
  /** Login failed: one text for wrong email, wrong password and an unusable account. */
  | "credentials"
  | "wrong_password"
  /** The mailed link is expired, used, for another purpose or no longer valid. */
  | "link_invalid"
  | "registration_closed"
  | "reauth_required"
  | "not_signed_in"
  | "deletion_in_progress"
  | "validation"
  | "other";

export type AccountField = "email" | "password" | "newPassword" | "displayName" | "avatarUrl";

export type AccountFailure = {
  kind: AccountErrorKind;
  /** Chinese text for the user. Never says whether an email is registered. */
  message: string;
  /** The form field the server pointed at, when it did. */
  field?: AccountField;
};

export const LOGIN_FAILURE = "邮箱或密码错误，或账号不可用";
export const LINK_INVALID = "这个链接已失效、已使用或已过期，请重新申请一封新的邮件";

const FIELDS: Record<string, { field: AccountField; message: string }> = {
  email: { field: "email", message: "邮箱地址格式不正确" },
  password: { field: "password", message: "密码需要 12 到 128 个字符" },
  new_password: { field: "newPassword", message: "新密码需要 12 到 128 个字符" },
  current_password: { field: "password", message: "请输入当前密码" },
  display_name: { field: "displayName", message: "昵称需要 1 到 40 个字符，且不能包含控制字符" },
  avatar_url: { field: "avatarUrl", message: "头像需要是 https 地址，且不含账号信息（最长 500 个字符）" },
};

/**
 * Turns any thrown value of an account call into text and a kind the form can act on.
 * The BFF and usersrv answer with short fixed English texts; the prefixes below are what we match.
 */
export function classifyAccountError(error: unknown, action: AccountAction = "other"): AccountFailure {
  const failure = classify(error, action);
  // A deletion that did not answer may well be under way: never tell the user nothing happened.
  if (action === "delete" && (failure.kind === "network" || failure.kind === "unavailable"))
    return { kind: "unavailable", message: "注销尚未确认完成：账号可能已经关闭，数据清理会继续。请稍后点击“重试注销”，进度不会丢失" };
  return failure;
}

function classify(error: unknown, action: AccountAction): AccountFailure {
  const err = ConnectError.from(error);
  const raw = err.rawMessage;
  switch (err.code) {
    case Code.ResourceExhausted:
      return { kind: "rate_limited", message: "操作太频繁，请过几分钟再试" };
    case Code.Unauthenticated:
      if (action === "login") return { kind: "credentials", message: LOGIN_FAILURE };
      if (raw.startsWith("current password is incorrect") && (action === "reauthenticate" || action === "change-password" || action === "delete"))
        return { kind: "wrong_password", message: "当前密码不正确" };
      return { kind: "not_signed_in", message: "登录已失效，请重新登录" };
    case Code.FailedPrecondition:
      if (raw.startsWith("link is not usable")) return { kind: "link_invalid", message: LINK_INVALID };
      if (raw.startsWith("registration is closed")) return { kind: "registration_closed", message: "目前暂未开放新用户注册" };
      if (raw.startsWith("enter your password again")) return { kind: "reauth_required", message: "为了安全，请先输入当前密码再继续" };
      if (raw.startsWith("another account deletion")) return { kind: "deletion_in_progress", message: "这个账号已有一次注销正在进行，请等待它完成" };
      if (action === "register-complete" || action === "reset-complete") return { kind: "link_invalid", message: LINK_INVALID };
      return { kind: "other", message: "现在无法执行此操作" };
    case Code.AlreadyExists:
      // A second registration for the same mailbox won the race: shown as an unusable link, as the spec asks.
      if (action === "register-complete") return { kind: "link_invalid", message: LINK_INVALID };
      return { kind: "other", message: "该内容已存在" };
    case Code.InvalidArgument: {
      const field = /^([a-z_]+):/.exec(raw)?.[1];
      if (field === "token") return { kind: "link_invalid", message: LINK_INVALID };
      if (field === "app_id") return { kind: "other", message: "当前应用暂未开放账号功能" };
      const known = field ? FIELDS[field] : undefined;
      if (known) return { kind: "validation", ...known };
      return { kind: "validation", message: "提交的内容不符合要求，请检查后再试" };
    }
    case Code.PermissionDenied:
      return { kind: "other", message: "当前无法执行此操作" };
    case Code.NotFound:
      return { kind: "other", message: "没有找到对应的内容，可能已经处理完成" };
    case Code.Unknown:
    case Code.Canceled:
      return { kind: "network", message: "无法连接服务器，请检查网络后重试" };
    case Code.DeadlineExceeded:
      return { kind: "network", message: "服务器响应超时，请检查网络后重试；这次请求不一定已经生效，可以稍后再确认" };
    case Code.Unavailable:
    case Code.Internal:
    default:
      if (/failed to fetch|networkerror|load failed/i.test(raw)) return { kind: "network", message: "无法连接服务器，请检查网络后重试" };
      return { kind: "unavailable", message: "服务暂时不可用，请稍后再试；这次请求没有生效" };
  }
}

export const describeAccountError = (error: unknown, action: AccountAction = "other") => classifyAccountError(error, action).message;
