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
  {
    id: "chidhljgdodbfohlcmohmojkpnefbocf",
    name: "OzTab",
    tagline: "把新标签页变成一个安静的起点：快捷方式、天气、日历、Linear 议题与 Bing 每日壁纸。",
    category: "效率工具",
    storeUrl: "https://chromewebstore.google.com/detail/chidhljgdodbfohlcmohmojkpnefbocf",
    icon: "/extensions/oztab-icon.jpg",
    screenshot: "/extensions/oztab-screenshot.jpg",
    version: "0.1.0",
    updated: "2026-11-04",
    size: "362KiB",
    languages: "英文界面",
    developer: "OzLabs",
    features: [
      "时钟、问候语与每天的 Bing 壁纸。",
      "用 Google、Bing 或 DuckDuckGo 搜索。",
      "自定义快捷方式，可添加、编辑与重新排序。",
      "显示你选择的城市的天气。",
      "本地任务清单与月历。",
      "可选：只读接入分配给你的 Linear 议题。",
    ],
    permissions: [
      "storage：保存设置、快捷方式与任务。",
      "站点访问：www.bing.com（每日壁纸）、api.open-meteo.com 与 geocoding-api.open-meteo.com（天气）、api.linear.app（可选议题）、www.google.com/s2（快捷方式图标）。",
    ],
    privacy: [
      "设置、快捷方式与任务保存在浏览器配置里，缓存的壁纸与天气可离线使用。",
      "没有 OzTab 账号，也没有开发者运营的后端。",
      "Linear 是可选项：用自己的个人 API 密钥连接，建议只授予读取权限；密钥存在本地并直接发往 Linear 的 API，断开连接会删除保存的连接数据。",
      "天气、壁纸、快捷方式图标与搜索分别使用各自的第三方服务。",
      "商店声明处理的信息：个人身份信息、身份验证信息、位置与网站内容；不会出售给第三方，也不用于与插件核心功能无关的用途。",
    ],
  },
];
