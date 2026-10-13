import { Code, ConnectError } from "@connectrpc/connect";

export function isUnauthenticated(error: unknown) {
  return ConnectError.from(error).code === Code.Unauthenticated;
}

// Server messages are English and meant for developers; these are the ones a
// person can act on, matched by prefix so wording details can change.
const KNOWN_MESSAGES: [string, string][] = [
  ["maximum 20 files", "每座城市最多 20 个文件，请先删除媒体或未完成的上传"],
  ["media upload limit", "上传操作太频繁，每小时最多 60 次，请稍后再试"],
  ["media not found", "文件不存在或已经删除"],
  ["media not ready", "文件尚未确认完成或已经删除"],
  ["upload expired", "上传已过期，请放弃本次上传后重新选择文件"],
  ["upload missing", "尚未收到完整文件，请重试上传；已上传文件可重新确认"],
  ["invalid image", "照片格式或尺寸无效，最多 2400 万像素、最长边 8000 像素"],
  ["corrupt image", "照片内容已损坏，请选择其他文件"],
  ["invalid PDF", "PDF 内容无效，请选择其他文件"],
  ["uploaded size", "上传内容与文件大小不一致，请重新选择文件"],
  ["access revoked", "文件访问已关闭，对象清理暂未完成，请重试删除"],
  ["username must be", "用户名需为 3–32 位小写字母、数字或下划线"],
  ["password must be", "密码需为 8–72 位"],
  ["display name is too long", "昵称太长，最多 40 个字"],
  ["username and password are required", "请输入用户名和密码"],
  ["username is taken", "该用户名已被使用"],
  ["registration is closed", "当前未开放注册"],
  ["invalid username or password", "用户名或密码不正确"],
  ["account is disabled", "该账号已被停用"],
  ["invalid or expired state", "登录请求无效或已过期，请重新发起登录"],
  ["oauth sign-in failed", "第三方登录失败，请重试"],
  ["snapshot not found", "该快照已过期或不存在"],
  ["snapshot contains", "该快照包含当前行政区划不再支持的区域，无法恢复"],
];

/** User-facing Chinese text for a failed API call. */
export function describeApiError(error: unknown): string {
  const err = ConnectError.from(error);
  const known = KNOWN_MESSAGES.find(([prefix]) => err.rawMessage.startsWith(prefix));
  if (known) return known[1];
  switch (err.code) {
    case Code.Unavailable:
    case Code.DeadlineExceeded:
    case Code.Unknown:
      return "无法连接服务器，请检查网络后重试";
    case Code.Unauthenticated:
      return "登录已失效或凭据不正确，请重新登录";
    case Code.ResourceExhausted:
      return "操作太频繁，请稍后再试";
    case Code.AlreadyExists:
      return "该内容已存在";
    case Code.PermissionDenied:
      return "当前无法执行此操作";
    case Code.InvalidArgument:
    case Code.FailedPrecondition:
      return "提交的内容不符合要求";
    case Code.Aborted:
      return "云端数据已在别处更新，请刷新后重试";
    default:
      return "服务暂时不可用，请稍后重试";
  }
}
