import { Code, ConnectError } from "@connectrpc/connect";

/**
 * One sentence for a failed call, in the words the person can act on. The server's own message is passed through
 * when it has one: it is written for a reader, and swallowing it would hide what to do next.
 */
/** Signed out, as opposed to broken: the page draws a 请先登录 card for the first and an error for the second. */
export function isUnauthenticated(error: unknown): boolean {
  return ConnectError.from(error).code === Code.Unauthenticated;
}

export function describeError(error: unknown, fallback: string): string {
  const e = ConnectError.from(error);
  switch (e.code) {
    case Code.Unauthenticated:
      return "登录已失效，请重新登录后再试。";
    case Code.PermissionDenied:
      return "请求被拒绝：可能是登录状态或来源校验过期，刷新页面后重试。";
    case Code.Aborted:
      return "这条记录刚在别处改过，刷新后再试。";
    case Code.NotFound:
      return "这条记录已经不在了。";
    default:
      return e.rawMessage === "" ? fallback : e.rawMessage;
  }
}
