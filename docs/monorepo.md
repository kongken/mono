# 前后端 monorepo 布局

仓库同时承载多个前端应用和多个后端服务。前端是 npm workspaces，后端是根目录的 Go module，两边只通过 `proto/` 契约、区域索引和测试夹具共享内容。本文描述当前已实现的目录约定。

## 目录

```
apps/
  web/                  前端应用“山海足迹”：Vite + React，自己的 index.html、vite.config.ts、tsconfig、public/、tests/（Playwright）、scripts/
packages/
  ui/                   shadcn 组件与 cn()：@trip-map/ui/button、dialog、input、utils
  api-client/           buf 生成的 ConnectRPC TypeScript 契约：@trip-map/api-client/<包>/v1/<文件>_pb（`bff/v1` 为浏览器契约，`tripmap/v1` 为业务契约，`platform/v1` 为共享选项）
  geo-data/             区域索引、城市坐标、区域目录、搜索索引、省份邻接表：@trip-map/geo-data/<名称>.json（文件在 data/）
  fixtures/             前端与 Go 后端共同验证的 JSON 夹具
cmd/ internal/ pkg/ proto/ migrations/   Go 后端，规则见 AGENTS.md 与 backend.md、bff.md
deploy/                 Dockerfile、Nginx 与 Compose/K8s/反代示例（deployment.md）
```

## 规则

- 所有 npm 命令在仓库根目录执行，根 `package.json` 的脚本转发到 `@trip-map/web`（`npm run dev`、`build`、`test`、`test:e2e`、`check:maps` 等不变）。依赖由根目录统一安装与锁定（单个 `package-lock.json`）。
- `apps/*` 之间不互相 import；共用的代码放进 `packages/*`。包以源码形式发布（`exports` 指向 `.ts`、`.tsx`、`.json`），不单独构建，由使用它的应用的 Vite 与 `tsc` 一起编译。只被一个应用使用的代码留在该应用里，出现第二个使用方时再抽出。
- `packages/ui` 的 Tailwind 类名需要被扫描：应用的 CSS 入口用 `@source` 指向它（`apps/web/src/index.css`）。新增应用时同样要加。
- `packages/api-client/src/` 由 `make buf` 生成（`buf.gen.yaml`），不手改。
- `packages/geo-data/data/regions.json`、`region-catalog.json` 在后端各有一份必须逐字一致的副本（`internal/tripmap/regions/`、`internal/tripmap/itinerary/coords.json`），因为 Go 用 `embed` 内嵌；Go 测试和 `npm run check:maps` 会比对。更新流程见 [数据与地图说明](data-and-maps.md)。
- `packages/fixtures` 的夹具由前端 `npm test` 与 Go 测试同时读取。
- 地图 GeoJSON（`apps/web/public/maps/`）和生成区域数据的脚本（`apps/web/scripts/`）目前仍属于 `apps/web`。第二个前端应用需要同一份地图时，再把地图文件和脚本移入 `packages/geo-data`。

## 新增前端应用

1. 建 `apps/<名称>/`，带 `package.json`（`name` 取 `@trip-map/<名称>`，依赖用到的 `@trip-map/*` 包，版本写 `*`）。
2. 复制 `apps/web` 的 Vite、TypeScript 配置，在 CSS 入口加 `@source "…/packages/ui/src"`。
3. 在根 `package.json` 增加对应的转发脚本（或直接使用 `npm run <脚本> -w @trip-map/<名称>`）。
4. 新增 `deploy/<名称>/Dockerfile`，先复制各 workspace 的 `package.json` 再 `npm ci`，并在 `.github/workflows/docker-publish.yml` 的矩阵中登记；`.github/workflows/ci.yml` 增加对应测试。
5. 如需访问后端，在 BFF 路由表登记浏览器请求头（见 [BFF](bff.md)），应用只使用 `@trip-map/api-client`。

## Cloudflare Pages 构建设置

静态托管 `apps/web`。仓库根目录需要保持为构建根目录，因为 workspace 依赖和 `packages/` 在根目录之外的位置：

| 设置 | 值 |
| --- | --- |
| Root directory（根目录） | 留空（仓库根目录） |
| Build command | `npm run build` |
| Build output directory | `apps/web/dist` |
| Install command | 默认（`npm ci` 或 `npm install`，在根目录执行） |
| `NODE_VERSION` | `24` |

构建时变量沿用原有名称：`VITE_API_BASE_URL`、`VITE_OSM_TILE_URL`、`VITE_OSM_TILE_MAX_ZOOM`、`VITE_OSM_TILE_ATTRIBUTION`、`VITE_OSM_TILE_PROVIDER`。Pages 不会运行前端镜像的 Nginx，因此 `/api/`、`/s/` 的同源转发不会自动存在：纯本地模式无需处理；启用后端时，`VITE_API_BASE_URL` 填 BFF 的绝对地址（BFF 负责跨域），分享链接的 OG 标签需要另行把 `/s/` 转发到 BFF（当前仓库没有提供 Pages 的转发配置）。

## 当前限制

- CI 对前后端每次都全量运行，没有按路径过滤；应用增多后再考虑按路径触发或任务图工具。
- `packages/ui` 只含现在用到的三个组件；新增 shadcn 组件时通过官方 CLI 加到 `packages/ui/src/` 并在其 `exports` 中登记（`apps/web/components.json` 里的别名需要随之调整）。
