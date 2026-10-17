# 山海足迹

使用 React、Vite、TypeScript、Tailwind CSS 和 shadcn/ui 开发的中国旅行地图，后端使用 Go、Butterfly、Gin、ConnectRPC、Ent 与 PostgreSQL/Redis/S3。地图使用 d3-geo 渲染本地 GeoJSON，无需地图 API Key；省内与全国地图可选开启联网底图（Leaflet，默认关闭，默认 OpenStreetMap；部署者也可经服务端代理启用 Mapbox，见 `docs/data-and-maps.md`）。

## 仓库结构

前后端同在一个 monorepo：前端是 npm workspaces（`apps/web` 加 `packages/{ui,api-client,geo-data,fixtures}`），后端是仓库根目录的 Go module（`cmd/`、`internal/`、`proto/`、`migrations/`）。目录约定和新增应用的步骤见 [前后端 monorepo 布局](docs/monorepo.md)。所有 npm 命令在仓库根目录执行。

## 运行

推荐 Node.js 24，最低版本以 Vite 的要求为准。

```bash
npm install
npm run dev
```

生产构建和预览：

```bash
npm test
npm run build
npm run preview
```

## 后端启动

```bash
cp config.example.yaml config.yaml
cp config.bff.example.yaml config.bff.yaml
make deps-up
make storage-init
make dev-secrets   # 本地 CA、tripmap TLS 证书与 BFF 签名密钥（dev-secrets/，已被 Git 忽略）
make dev
```

生产环境关闭 `auto_migrate`，用 `migrations/tripmap/` 里评审过的 SQL 升级数据库；修改 Ent schema 后用 `make ent` 与 `make migration NAME=… DEV_DSN=…` 生成（顺序与验证见 [后端初始化说明](docs/backend.md#版本化数据库迁移)）。

`make dev` 启动 BFF（18081）与 tripmap（18082/18083 内网），见 [BFF](docs/bff.md) 与 [部署与发布](docs/deployment.md)。

自 tripmap 迁出认证后，已有用户需按顺序：**版本化迁移**（`migrations/tripmap/` + `migrations/bff/`）→ **`make build` 后 `bin/bff-import-legacy-accounts`（先 `--dry-run`）** → 抽查登录 → 可选 `deploy/sql/tripmap/post_import_drop_legacy_auth_columns.sql`。详见 [部署与发布](docs/deployment.md#legacy-账号导入-44)。

前端无需后端即可独立运行；仅当构建时设置 `VITE_API_BASE_URL`（如 `/api`）才会连接后端，详见 [后端初始化说明](docs/backend.md)。配置、端口、数据库模型和验证方式也见该文档。未登录时足迹保存在浏览器本地；配置后端后可选登录（密码、GitHub、Google），足迹保存到云端并多设备同步。分享页默认关闭：后端配置 `share.public_base_url` 后才可用，开发时 Vite 开发服务器会把 `/s/` 代理到后端（见 `config.example.yaml`）。

## 功能

- 全国与省内地图探索，城市一键标记 / 取消并自动配色。
- 同一城市多次旅行（日期、备注、照片），目的地搜索、足迹收藏与探索统计。
- 浏览器本地自动保存、JSON 备份导入导出，以及手机适配。
- 可选账号登录与云端同步（需配置后端）。
- 可选回忆提醒与通知（邮件、浏览器推送，默认关闭，需配置后端 `notification`，见 [后端初始化说明](docs/backend.md#f12-提醒与通知tripmapv1notificationservice)）。
- 可选公开分享页与社交预览图（需配置后端 `share.public_base_url`，见 [公开分享页与预览图](docs/sharing.md)）。

完整交互规则和验收场景见 [功能描述](docs/features.md)。未登录时数据保存在当前浏览器，不会跨设备同步，建议定期导出备份；登录后保存在云端。

## 地图来源和边界说明

地图文件已包含在 `apps/web/public/maps/`，构建后随静态资源部署。来源、边界年份、区域 ID 及存储格式见 [数据与地图说明](docs/data-and-maps.md)。

```bash
npm run prepare:regions  # 区域索引变更后归档旧版本并重建版本/迁移表（详见数据说明）
npm run prepare:map-colors  # 更换全国地图后重建省份邻接表
npm run check:maps  # 检查区域版本副本、城市中心点坐标副本、地图完整性、投影、多边形方向、区域 ID 和分享预览几何
npm run prepare:share-geometry  # 更换全国地图后重建分享预览几何
npm run download:maps  # 联网更新省内地图与城市索引
```

shadcn 组件通过官方 CLI 添加，源文件在 `packages/ui/src/`；地图归一化逻辑在 `apps/web/src/lib/maps.ts`，主要业务界面在 `apps/web/src/App.tsx`。

## Docker 镜像

GitHub Actions 分别构建并发布 `ghcr.io/kongken/trip-map`（tripmap 后端）、`ghcr.io/kongken/trip-map-bff`（BFF 公网入口）和 `ghcr.io/kongken/trip-map-front`（前端）。main 分支使用 `main` 标签，版本 tag 发布对应版本；PR 仅构建。详情见 [Docker 镜像与 Actions](docs/docker-images.md)。

## 项目文档

- [文档索引](docs/README.md)
- [项目协作与文档同步规范](AGENTS.md)

照片与附件直传需要配置私有 S3 的浏览器 CORS 和临时目录生命周期，初始化命令及部署顺序见 [后端媒体说明](docs/backend.md#直传部署配置)。
