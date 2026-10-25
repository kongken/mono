import { Code, ConnectError } from "@connectrpc/connect";
import { AtomicBatchTooLarge, AtomicBatchUnavailable } from "./sync.ts";
import { RegionCompatibilityError } from "./region-catalog.ts";

/** The backend predates several trips per city; writing to it would flatten or drop trips. */
export class BackendNotUpgraded extends Error {
  constructor() {
    super("backend does not support several trips per city");
  }
}

export function isUnauthenticated(error: unknown) {
  return ConnectError.from(error).code === Code.Unauthenticated;
}

// Server messages are English and meant for developers; these are the ones a
// person can act on, matched by prefix so wording details can change.
const KNOWN_MESSAGES: [string, string][] = [
  ["region index version mismatch", "前后端区域索引版本不一致；同步已暂停，缓存和足迹已保留。请刷新页面或升级服务器"],
  ["region upgrade required before writes", "云端含待迁移或无法映射的旧区域，修改尚未提交。原请求和缓存已保留；请先导出备份，再在账号的“区域数据与兼容”中处理"],
  ["region upgrade blocked", "存在无法映射、发生碰撞或需要共同旅行协调的区域；没有修改任何数据。请先导出备份并联系维护者"],
  ["footprints changed since region preview", "云端数据已在预览后更新，没有迁移任何记录。请重新预览"],
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
  ["account is disabled", "该账号已被停用"],
  ["snapshot not found", "该快照已过期或不存在"],
  ["snapshot contains", "该快照包含当前行政区划不再支持的区域，无法恢复"],
  ["client upgrade required", "账号已使用“同城多次旅行”，当前页面版本过旧。请刷新页面（必要时清除缓存）后再试"],
  ["sharing is not enabled", "此服务器尚未开启分享"],
  ["share content is too large", "分享内容太大，请隐藏备注或减少城市后再试"],
  ["a chosen photo is no longer available", "选中的照片已不可用，请重新选择照片"],
  ["too many share requests", "分享操作太频繁，请稍后再试"],
  ["mutation_id was already used", "这次操作已经提交过，请关闭窗口重新打开后再试"],
  ["trip not found in this city", "这次旅行还没有同步到云端，请等待同步完成后再邀请好友"],
  ["trip not found", "这次旅行还没有同步到云端，请等待同步完成后再添加附件"],
  ["trip city does not exist", "旅行所属的城市在云端不存在，请先处理同步冲突或刷新"],
  ["trip belongs to another city", "旅行记录与城市不匹配，请刷新后重试"],
  ["ambiguous username", "有多个同名用户，请使用对方的好友邀请码"],
  ["user not found", "未找到该用户，请检查用户名或好友邀请码"],
  ["cannot add yourself", "不能添加自己为好友"],
  ["friend request not found", "好友申请不存在或已失效"],
  ["friendship not found", "好友关系不存在或已解除"],
  ["friend not found", "未找到该好友"],
  ["shared trip not found", "共同旅行不存在或已解散"],
  ["too many collaboration requests", "协作操作太频繁，请稍后再试"],
  ["email notifications are not available", "服务器尚未配置邮件发送"],
  ["browser push is not available", "服务器尚未配置浏览器推送"],
  ["verify an email address first", "请先验证邮箱地址，再开启邮件提醒"],
  ["enter a valid email address", "请输入有效的邮箱地址"],
  ["enter the six-digit code", "请输入邮件中的 6 位数字验证码"],
  ["the code expired or was used up", "验证码已过期或已失效，请重新获取"],
  ["the code is not correct", "验证码不正确"],
  ["could not send the verification email", "验证邮件发送失败，请稍后再试"],
  ["could not send the test email", "测试邮件发送失败，请检查服务器的邮件配置"],
  ["no browser is subscribed yet", "还没有订阅浏览器推送，请先在此设备启用"],
  ["could not reach any subscribed browser", "无法送达已订阅的浏览器，请在此设备重新启用推送"],
  ["trip owner cannot leave", "创建者无法直接退出，请选择解散共同旅行"],
];

/** User-facing Chinese text for a failed API call. */
export function describeApiError(error: unknown): string {
  if (error instanceof RegionCompatibilityError) return error.message;
  if (error instanceof BackendNotUpgraded)
    return "云端服务尚未升级到支持“同城多次旅行”的版本。为避免旅行记录丢失，已暂停同步；修改保留在本机，请待服务升级后重试";
  if (error instanceof AtomicBatchTooLarge)
    return `这次迁移需要 ${error.count} 项变更，超过一次原子迁移的上限 2000 项。为避免只迁移一半，已暂停同步，没有发送任何内容；未同步的内容仍完整保留在本机。请先导出备份，再减少旅行记录使总数不超过上限后重试`;
  if (error instanceof AtomicBatchUnavailable)
    return "这次迁移必须作为一个整体提交，但当前连接不支持批量提交。已暂停同步，没有发送任何内容；未同步的内容仍完整保留在本机。请先导出备份，刷新页面或升级后重试";
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
