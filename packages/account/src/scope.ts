/**
 * What closing the shared account removes. Both apps show the whole list, not just their
 * own part, because the account is shared and every enabled product's data goes with it.
 */
export const ACCOUNT_SHARED_SCOPE = "共用账号：登录邮箱、密码、昵称与头像、已授予的角色，以及全部已登录设备和管理令牌";

export const ACCOUNT_PRODUCT_SCOPE: { id: string; name: string; data: string }[] = [
  { id: "tripmap", name: "山海足迹", data: "云端足迹与历史快照、照片与附件、好友关系与共同旅行、分享页面、提醒订阅和同步记录" },
  { id: "metro", name: "地铁足迹", data: "乘坐记录、骑行者资料与公开主页" },
];

/** The word the user types to confirm; the request itself always carries "DELETE". */
export const DELETE_CONFIRM_WORD = "注销";
