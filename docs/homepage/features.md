# Oz Studio 站点功能描述

`apps/homepage` 是 Oz Digital Studio 的静态站点：只做产品介绍与入口跳转，没有账号、接口或后端依赖，可独立部署到 Cloudflare Pages，也可用 `deploy/homepage/` 的镜像托管。本文描述当前已实现的页面与维护方式。

## 页面

| 路径 | 页面 | 内容 |
| --- | --- | --- |
| `/` | 首页 | 工作室介绍、两个 Web 产品（山海足迹、地铁足迹）卡片、OzID 说明与设计理念 |
| `/extensions/` | Chrome 插件列表 | Oz Studio 在 Chrome 网上应用店发布的插件：安装入口、功能、权限、数据与隐私说明、商店截图和版本信息 |

两个页面共用 `src/site-chrome.tsx` 里的页头与页脚：页头导航为「探索产品 / Chrome 插件 / 关于 Oz」与 OzID 入口，`current` 决定当前页的 `aria-current`；页脚指向产品锚点、插件列表与 OzID。新增页面时先在这里加导航项，再传入新的 `current`。

站点是 Vite 多页构建：`index.html` 与 `extensions/index.html` 各自有入口脚本和标题/描述，产物为 `dist/index.html` 与 `dist/extensions/index.html`。插件列表不需要 SPA 兜底路由，`/extensions/` 由静态目录索引直接提供（Nginx 模板已有 `try_files $uri $uri/ /index.html`，Cloudflare Pages 默认按目录索引返回）。

## 插件列表

- 列表数据在 `apps/homepage/src/extensions.ts`，每个条目对应一个商店上架插件，字段包括商店 ID、名称、一句话说明、分类、版本、更新时间、体积、语言、开发者、功能清单、权限说明、隐私说明以及图标和截图路径。
- 展示值以 Chrome 网上应用店页面为准；商店页面变化时同步更新这里。页面上不写商店没有给出的数字（例如安装量、评分）。
- 图标与截图下载到 `apps/homepage/public/extensions/`，随站点一起发布，运行时不请求第三方资源。图标用 128px 方图，截图转为体积更小的 JPEG。
- 安装按钮直接跳转 `https://chromewebstore.example/detail/<扩展 ID>`（部署时替换为实际 Chrome 网上应用店地址），使用 `target="_blank"` 与 `rel="noopener noreferrer"`。
- 目前有两个上架插件，开发者都是 OzLabs。**RequestLoom — Headers & Redirects** 的功能、权限（`declarativeNetRequest`、`storage`、站点访问 `<all_urls>`）与「没有开发者后端、广告、分析或遥测」的隐私说明来自商店的描述与 manifest。**OzTab** 是新标签页插件：权限只有 `storage` 与 `www.bing.com`、`api.open-meteo.com`、`geocoding-api.open-meteo.com`、`api.linear.app`、`www.google.com/s2` 五个主机，可选接入 Linear 用自己的 API 密钥、只读且有本地存储，数据不进开发者后端；它的功能、权限与商店声明的信息类型（个人身份信息、身份验证信息、位置、网站内容）同样来自商店的描述与 manifest。

### 新增一个插件

1. 打开商店页面，记录名称、简介、版本、更新时间、体积、语言、功能、权限与隐私说明。
2. 下载 128px 图标与该插件的首张商店截图，放到 `apps/homepage/public/extensions/`，文件名用小写产品名加 `-icon` / `-screenshot`。
3. 在 `src/extensions.ts` 追加一个条目，`id` 用商店扩展 ID（同时作为列表 key），图标与截图写成 `/extensions/<文件名>`。
4. 重新构建并在浏览器检查新条目。

## 验证

- 构建：仓库根目录 `npm run build:homepage`，产物同时包含 `/` 与 `/extensions/`。
- 浏览器检查：桌面与手机宽度下两个页面都没有横向溢出，图标和截图正常加载，导航与页脚的 `/extensions/` 链接可互相进入，商店链接在新标签打开。
- 只改文案或数据时，仍需构建一次以确认 TypeScript 与多页入口没有回归。

## 限制

- 插件数据是手工维护的静态列表，不会自动读取商店；版本与说明可能滞后于商店页面，页面上已注明以商店为准。
- 站点没有后台、搜索或分页，插件数量增加很多时需要重新考虑列表布局。
- 页面语言为中文，插件名称与商店内容保持英文原文。
