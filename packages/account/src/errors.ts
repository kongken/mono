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
  /** An administrator's change of another account in the admin console. */
  | "admin-change"
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
  /** Another deletion cannot begin: this browser holds the recovery Cookie of one that has not finished. */
  | "deletion_pending_here"
  /** The browser did not keep the recovery Cookie the deletion needs; nothing was begun. */
  | "recovery_cookie_blocked"
  /** The account holds the platform role, which keeps it from the web self-service deletion. */
  | "platform_admin"
  /** An administrator change was refused because the account changed after it was read; nothing was done. */
  | "conflict"
  /** The target of an administrator change holds the platform role; the console never changes it. */
  | "target_protected"
  /** The target cannot be changed in its state (closing, or not active for a role grant). */
  | "target_state"
  /** The operation ID of an administrator change belongs to a different request. */
  | "mutation_reused"
  /** Signed in, but the account does not hold the role the management call needs. */
  | "forbidden"
  /** The management call came from a network or origin the perimeter does not admit; it never says which. */
  | "not_allowed"
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
  if (action === "admin-change") {
    // The administrator's changes have refusals of their own; usersrv's texts are fixed and matched by prefix.
    if (err.code === Code.Aborted && raw.startsWith("account changed since it was read"))
      return { kind: "conflict", message: "这个账号在你查看之后发生了变化，没有做任何修改。请刷新账号信息，确认后再操作" };
    if (err.code === Code.FailedPrecondition && raw.startsWith("platform administrators cannot be changed here"))
      return { kind: "target_protected", message: "这是平台管理员账号，网页上不能修改它的状态或角色。请联系运维使用命令行处理" };
    if (err.code === Code.FailedPrecondition && raw.startsWith("account is not active"))
      return { kind: "target_state", message: "账号当前的状态不允许这个操作（注销中的账号不能修改；授予角色需要账号处于正常状态）。没有做任何修改" };
    if (err.code === Code.AlreadyExists && raw.startsWith("mutation id used for a different request"))
      return { kind: "mutation_reused", message: "这个操作编号已被另一个操作使用。请关闭窗口后重新发起" };
    if (err.code === Code.NotFound) return { kind: "other", message: "没有找到这个账号，它可能已经注销" };
    if (err.code === Code.InvalidArgument && raw.startsWith("reason:"))
      return { kind: "validation", message: "原因需要 1 到 200 个字符，不含控制字符，也不要写邮箱、密码或令牌" };
    // No answer: the change may or may not have been made. Retrying with the same operation ID is safe.
    if ([Code.DeadlineExceeded, Code.Unavailable, Code.Unknown, Code.Canceled, Code.Internal].includes(err.code))
      return { kind: err.code === Code.Unavailable || err.code === Code.Internal ? "unavailable" : "network", message: "没有收到服务器的确认，这次修改不一定已经生效。可以用同一个操作编号重试：已经生效的修改不会被重复执行" };
  }
  switch (err.code) {
    case Code.ResourceExhausted:
      return { kind: "rate_limited", message: "操作太频繁，请过几分钟再试" };
    case Code.Unauthenticated:
      if (action === "login") return { kind: "credentials", message: LOGIN_FAILURE };
      if (raw.startsWith("current password is incorrect") && (action === "reauthenticate" || action === "change-password" || action === "delete"))
        return { kind: "wrong_password", message: "当前密码不正确" };
      // The management calls ask for a recent password check; the session itself is still valid.
      if (raw.startsWith("sign in again to use this feature")) return { kind: "reauth_required", message: "为了安全，请先重新验证密码再继续" };
      return { kind: "not_signed_in", message: "登录已失效，请重新登录" };
    case Code.FailedPrecondition:
      if (raw.startsWith("link is not usable")) return { kind: "link_invalid", message: LINK_INVALID };
      if (raw.startsWith("registration is closed")) return { kind: "registration_closed", message: "目前暂未开放新用户注册" };
      if (raw.startsWith("navigation is not configured")) return { kind: "other", message: "账号入口尚未配置完整，暂时无法申请，请联系管理员" };
      if (raw.startsWith("enter your password again")) return { kind: "reauth_required", message: "为了安全，请先输入当前密码再继续" };
      if (raw.startsWith("an account deletion started in this browser"))
        return { kind: "deletion_pending_here", message: "这个浏览器里有一次尚未完成的账号注销，完成之前不能开始另一次。请先在“注销账号”页查看进度并重试，直到它完成；登录和退出不受影响" };
      if (raw.startsWith("account deletion recovery cookie"))
        return { kind: "recovery_cookie_blocked", message: "浏览器没有保存注销所需的凭据（Cookie），没有开始注销，账号仍然有效。请确认没有禁用或拦截本站 Cookie，然后重试" };
      if (raw.startsWith("another account deletion")) return { kind: "deletion_in_progress", message: "这个账号已有一次注销正在进行，请等待它完成" };
      if (raw.startsWith("platform administrator accounts cannot be closed"))
        return { kind: "platform_admin", message: "这个账号是平台管理员，不能在网页上注销。请联系运维先回收管理员角色，再重新注销" };
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
      if (raw.startsWith("required role missing") || raw.startsWith("permission denied"))
        return { kind: "forbidden", message: "当前账号没有使用这个功能的权限" };
      // The perimeter's answer is the same whatever it checked (network, origin); so is this text.
      if (raw.startsWith("request rejected") || raw.startsWith("403"))
        return { kind: "not_allowed", message: "当前网络或访问来源不允许使用管理功能" };
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
