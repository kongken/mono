export type ChromeExtension = {
  /** Chrome Web Store extension ID, also the stable key for this list. */
  id: string;
  name: string;
  tagline: string;
  category: string;
  storeUrl: string;
  icon: string;
  screenshot: string;
  version: string;
  updated: string;
  size: string;
  languages: string;
  developer: string;
  features: string[];
  permissions: string[];
  privacy: string[];
};

/**
 * Current listings published by Oz Studio on the Chrome Web Store.
 *
 * Values mirror each store listing (name, version, update date, size, feature
 * list, privacy notes). Update them here when a listing changes, and add one
 * entry per new extension.
 */
export const extensions: ChromeExtension[] = [
  {
    id: "hcoagokijkcgkjaikennhcbceofgodih",
    name: "RequestLoom — Headers & Redirects",
    tagline: "用可切换的配置档案管理请求头、响应头与域名重定向。",
    category: "开发者工具",
    storeUrl: "https://chromewebstore.google.com/detail/hcoagokijkcgkjaikennhcbceofgodih",
    icon: "/extensions/requestloom-icon.jpg",
    screenshot: "/extensions/requestloom-screenshot.jpg",
    version: "0.1.1",
    updated: "2026-10-19",
    size: "90.43 KiB",
    languages: "英文界面",
    developer: "OzLabs",
    features: [
      "设置或移除请求头与响应头。",
      "把来源域名的请求重定向到其他主机、端口或 HTTP(S) 协议，同时保留路径与查询参数。",
      "用命名档案组织规则，可同时启用多个档案。",
      "头部规则可限定到精确域名或通配子域名；域名列表留空则对所有站点生效。",
      "拖动档案调整优先级：列表中越靠下的档案在冲突时胜出。",
      "在弹窗里开关档案，或一键暂停全部规则而不丢失设置。",
      "单个档案或完整配置可导出为 JSON；导入时不会覆盖已有档案。",
      "配置通过 Chrome Sync 同步。",
    ],
    permissions: [
      "declarativeNetRequest：由 Chrome 的网络请求规则引擎执行改写与重定向。",
      "storage：保存档案、规则与偏好设置（经 chrome.storage.sync）。",
      "站点访问：声明访问所有站点，实际是否改写取决于你启用的规则。",
    ],
    privacy: [
      "没有开发者运营的后端，也没有广告、分析或遥测。",
      "配置（包括你填写的头部值）存放在 chrome.storage.sync，按你的 Chrome 设置可能经 Google 账号同步。",
      "导出的 JSON 含已配置的值，分享前请自行检查。",
      "只需在你要测试的站点上启用规则，不需要额外注册账号。",
    ],
  },
];
