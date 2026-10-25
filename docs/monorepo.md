# 前后端 monorepo 布局

仓库同时承载多个前端应用和多个后端服务。前端是 npm workspaces，后端是根目录的 Go module，两边只通过 `proto/` 契约、区域索引和测试夹具共享内容。本文描述当前已实现的目录约定。

## 目录

```
apps/
  trip-map/             前端应用“山海足迹”：Vite + React，自己的 index.html、vite.config.ts、tsconfig、public/、tests/（Playwright）、scripts/
  metro/                前端应用“地铁足迹”：Vite + React，自己的路由与内存目录缓存，tests/（Playwright，模拟 BFF），不含地图资源脚本
  ozid/                 前端应用“OzID 用户中心”：Vite + React，登录、注册、找回密码、邮件落地页（注册验证、密码重置）、概览、密码重新认证，以及资料、改密码与设备会话页面（T3）；没有同源代理，构建必须给出绝对 `VITE_API_BASE_URL`；tests/ 有模拟 BFF 的 Playwright 与真实 BFF 的 real-stack 用例（由 internal/e2e 启动）
packages/
  ui/                   shadcn 组件与 cn()：@kongken/ui/button、dialog、input、utils
  api-client/           buf 生成的 ConnectRPC TypeScript 契约：@kongken/api-client/<包>/v1/<文件>_pb（`bff/v1` 为共用的账号契约，`bff/<产品>/v1` 如 `bff/tripmap/v1` 为各产品的浏览器契约，`tripmap/v1` 为业务服务契约，`platform/v1` 为共享选项）
  geo-data/             区域索引、城市坐标、区域目录、搜索索引、省份邻接表：@kongken/geo-data/<名称>.json（文件在 data/）
  fixtures/             前端与 Go 后端共同验证的 JSON 夹具
  account/              trip-map、metro 与 OzID 共用的邮箱账号流程（T2 起新邮件的落地页在 OzID，产品的落地页只服务旧邮件）：`createAccountApi`（经 `bff.v1` 的 Connect 客户端）、错误文案、登录/注册/资料/改密码/设备/注销表单、邮件落地页、落地页响应头的 Vite 插件，以及测试用的假账号服务与 SMTP 接收器（`@kongken/account/<模块>`）
cmd/ internal/ pkg/ proto/ migrations/   Go 后端，规则见 AGENTS.md 与 backend.md、bff.md
deploy/                 Dockerfile、Nginx 与 Compose/K8s/反代示例（deployment.md）
```

## 多产品命名约定

一个产品在各层使用同一个短名（山海足迹是 `trip-map` / `tripmap`，地铁足迹是 `metro`），新增产品按此对齐；地铁足迹是第二个产品，各层位置见表：


| 层 | 位置 |
| --- | --- |
| 前端应用 | `apps/<产品>`，包名 `@kongken/<产品>`，镜像 `…-<产品>-web`，部署文件 `deploy/<产品>-web/`（山海足迹的目录名是 `trip-map`，地铁足迹是 `metro`） |
| 业务服务 | `cmd/<产品>`、`internal/<产品>`、`proto/<产品>/v1`、`migrations/<产品>/`，镜像 `…-<产品>` |
| 浏览器契约与 BFF 适配 | `proto/bff/<产品>/v1`、`internal/bff/<产品>/`（见 [BFF](bff.md#新增业务)） |
| 产品文档 | `docs/<产品>/` |

共用部分不带产品名：`packages/*`、`internal/platform`、`proto/platform`、`bff.v1`（账号与会话）、BFF 的 `auth`、`session`、`ingress`、`gateway`。

账号服务 usersrv 不属于任何一个产品，而是两个产品共用的服务：`cmd/usersrv`、`cmd/usersrv-admin`（运维命令）、`internal/usersrv`、`proto/usersrv/v1`、`migrations/usersrv/`、`deploy/usersrv/`，镜像 `…-usersrv`（见 [Docker 镜像](docker-images.md)、[部署](deployment.md#usersrv-的部署)）。它与业务服务一样不被其他业务 import，BFF 只经 `usersrv.v1` 内部 gRPC 调用它。

## 规则

- 所有 npm 命令在仓库根目录执行，根 `package.json` 的脚本转发到各应用（应用生命周期脚本按应用命名：`dev:trip-map`、`build:trip-map`、`test:trip-map`、`preview:trip-map`、`test:e2e:trip-map`，metro、ozid 同理（`dev:metro`、`build:ozid`、`test:e2e:ozid` 等）；`test` 对所有应用运行，`build` 只构建使用同源 `/api` 的 trip-map 与 metro，OzID 必须带绝对 `VITE_API_BASE_URL` 单独构建（`build:ozid`）；地图数据脚本 `check:maps`、`prepare:*`、`download:maps` 因属于 `packages/geo-data` 的数据流程，名称不带应用前缀，目前转发到 `@kongken/trip-map`）。依赖由根目录统一安装与锁定（单个 `package-lock.json`）。
- `apps/*` 之间不互相 import；共用的代码放进 `packages/*`。包以源码形式发布（`exports` 指向 `.ts`、`.tsx`、`.json`），不单独构建，由使用它的应用的 Vite 与 `tsc` 一起编译。只被一个应用使用的代码留在该应用里，出现第二个使用方时再抽出。
- `packages/ui` 与 `packages/account` 的 Tailwind 类名需要被扫描：应用的 CSS 入口用 `@source` 指向它们（`apps/trip-map/src/index.css`、`apps/metro/src/index.css`）。新增应用时同样要加。`packages/account` 的落地页响应头插件 `accountLandingHeaders()` 也要加进应用的 `vite.config.ts`，应用入口 `main.tsx` 在渲染前先调用 `captureLandingToken()`。OzID（`apps/ozid`）已按此接入（[#80 / T2](https://github.com/kongken/mono/issues/80)，T1 时的临时例外已消除）：`vite.config.ts` 用 `accountLandingHeaders()`，`main.tsx` 在渲染前调用 `captureLandingToken()`，`deploy/ozid-web/default.conf.template` 为两个落地页路径单独设置 `Referrer-Policy: no-referrer`、`Cache-Control: no-store` 与同源脚本 CSP。邮件安全要求不变：令牌只在 URL fragment、页面立即清除、不缓存、不外泄 Referer、只允许同源脚本。今后再有应用提供这些落地页时同样必须接入这两个钩子和响应头。
- `packages/api-client/src/` 由 `make buf` 生成（`buf.gen.yaml`），不手改。
- `packages/geo-data/data/regions.json`、`region-catalog.json` 在后端各有一份必须逐字一致的副本（`internal/tripmap/regions/`、`internal/tripmap/itinerary/coords.json`），因为 Go 用 `embed` 内嵌；Go 测试和 `npm run check:maps` 会比对。更新流程见 [数据与地图说明](trip-map/data-and-maps.md)。
- `packages/fixtures` 的夹具由前端 `npm test` 与 Go 测试同时读取。
- 地图 GeoJSON（`apps/trip-map/public/maps/`）和生成区域数据的脚本（`apps/trip-map/scripts/`）目前仍属于 `apps/trip-map`。第二个前端应用需要同一份地图时，再把地图文件和脚本移入 `packages/geo-data`。

## 新增前端应用

1. 建 `apps/<名称>/`，带 `package.json`（`name` 取 `@kongken/<名称>`，依赖用到的 `@kongken/*` 包，版本写 `*`）。
2. 复制 `apps/trip-map` 的 Vite、TypeScript 配置，在 CSS 入口加 `@source "…/packages/ui/src"`。
3. 在根 `package.json` 增加按应用命名的转发脚本（`dev:<名称>`、`build:<名称>`、`test:<名称>` 等），或直接使用 `npm run <脚本> -w @kongken/<名称>`；根 `test` 会自动覆盖所有应用；根 `build` 只构建使用同源 `/api` 的 trip-map 与 metro，OzID 必须带绝对 API 地址单独构建（`npm run build:ozid`）。
4. 新增 `deploy/<名称>-web/Dockerfile`（镜像后缀 `-<名称>-web`），先复制各 workspace 的 `package.json` 再 `npm ci`，并在 `.github/workflows/docker-publish.yml` 的矩阵中登记；`.github/workflows/ci.yml` 增加对应测试。
5. 如需访问后端，在 BFF 路由表登记浏览器请求头（见 [BFF](bff.md)），应用只使用 `@kongken/api-client`。

## Cloudflare Pages 构建设置

静态托管 `apps/trip-map`。仓库根目录需要保持为构建根目录，因为 workspace 依赖和 `packages/` 在根目录之外的位置：

| 设置 | 值 |
| --- | --- |
| Root directory（根目录） | 留空（仓库根目录） |
| Build command | `npm run build:trip-map` |
| Build output directory | `apps/trip-map/dist` |
| Install command | 默认（`npm ci` 或 `npm install`，在根目录执行） |
| `NODE_VERSION` | `24` |

构建时变量沿用原有名称：`VITE_API_BASE_URL`、`VITE_OSM_TILE_URL`、`VITE_OSM_TILE_MAX_ZOOM`、`VITE_OSM_TILE_ATTRIBUTION`、`VITE_OSM_TILE_PROVIDER`。Pages 不会运行前端镜像的 Nginx，因此 `/api/`、`/s/` 的同源转发不会自动存在：纯本地模式无需处理；启用后端时，`VITE_API_BASE_URL` 填 BFF 的绝对地址（BFF 负责跨域），分享链接的 OG 标签需要另行把 `/s/` 转发到 BFF（当前仓库没有提供 Pages 的转发配置）。

## 当前限制

- 每个前端镜像的 Dockerfile 都要复制**所有** workspace 的 `package.json`（`npm ci` 按锁文件校验各 workspace）；新增应用时同步改其他应用的 Dockerfile。
- CI 对前后端每次都全量运行，没有按路径过滤；应用增多后再考虑按路径触发或任务图工具。
- `packages/ui` 只含现在用到的三个组件；新增 shadcn 组件时通过官方 CLI 加到 `packages/ui/src/` 并在其 `exports` 中登记（`apps/trip-map/components.json` 里的别名需要随之调整）。
