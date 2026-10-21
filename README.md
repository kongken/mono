# Kongken Monorepo

一个同时承载前端应用、后端服务与共享基础代码的仓库。前端使用 npm workspaces，后端使用根目录的单个 Go module；各应用和服务独立构建、部署。

当前业务是「山海足迹」中国旅行地图：以省份探索、城市足迹和旅行记录为核心，支持本地保存，以及可选的账号登录、云端同步和公开分享。地图使用随应用分发的 GeoJSON，无需地图 API Key；省内与全国地图可选开启联网底图（默认关闭，默认 OpenStreetMap；部署者也可经服务端代理启用 Mapbox，见 [数据与地图说明](docs/trip-map/data-and-maps.md)）。完整规则见 [功能描述](docs/trip-map/features.md)。

## 应用与服务

| 应用 / 服务 | 代码位置 | 职责 | 本地开发入口 |
| --- | --- | --- | --- |
| trip-map（山海足迹） | `apps/trip-map/` | React、Vite、TypeScript、Tailwind CSS、shadcn/ui 前端 | Vite 输出地址，通常为 `http://localhost:5173` |
| bff | `cmd/bff/`、`internal/bff/` | 唯一公网 API 入口；账号、OAuth、Cookie 会话、CSRF 和业务接口适配 | HTTP `127.0.0.1:18081` |
| tripmap | `cmd/tripmap/`、`internal/tripmap/` | 足迹、行程、媒体、分享等旅行领域业务 | 内网 HTTP `127.0.0.1:18082`；内部 gRPC `127.0.0.1:18083`（默认 TLS，可显式选 h2c） |

浏览器经 BFF 的 Connect 接口访问后端（账号与会话是共用的 `bff.v1`，山海足迹业务是 `bff.tripmap.v1`）；BFF 使用内部凭证，通过内部 gRPC（默认 TLS；可信内网可显式配置 h2c，见 [BFF](docs/bff.md#内部-grpc-与凭证)）调用 tripmap。tripmap 的 HTTP 端口提供 `/ping`、`/readyz` 健康检查；启用 Mapbox 底图时还提供匿名的 `GET /api/tiles/…`，由 BFF 转发。账号与会话保存在 BFF 的独立数据库，旅行数据由 tripmap 管理。当前有两个后端服务，其中 tripmap 是唯一的业务服务，详见 [BFF](docs/bff.md)。

## 仓库结构

```text
apps/trip-map/        山海足迹前端应用
packages/
  ui/                 共享 shadcn/ui 组件
  api-client/         Buf 生成的 TypeScript RPC 契约
  geo-data/           区域索引、坐标与地图辅助数据
  fixtures/           前后端共同验证的测试夹具
cmd/
  bff/                BFF 服务入口
  tripmap/            旅行业务服务入口
  ...                 数据迁移、存储初始化与密钥生成工具
internal/
  bff/                BFF 认证、接口适配与 Ent schema
  tripmap/            旅行业务、存储与 Ent schema
  platform/           跨服务共享基础代码
  e2e/                BFF → tripmap 端到端测试
proto/                bff、tripmap 与 platform 的 Protobuf 契约
pkg/proto/            生成的 Go RPC 代码
migrations/           按 bff、tripmap 分开的版本化数据库迁移
deploy/               镜像构建、反向代理与部署示例
docs/                 功能、数据、开发与部署文档
```

前端应用之间不互相 import，共用部分放入 `packages/`；后端业务包之间不互相 import，共享基础代码放入 `internal/platform/`，且不反向依赖业务包。目录约定和新增前端应用步骤见 [前后端 monorepo 布局](docs/monorepo.md)。

## 本地开发

以下命令均在仓库根目录执行。前端推荐 Node.js 24；启动后端另需 Go 1.26.5+ 与 Docker Compose，重新生成 RPC 代码需安装 Buf。

### 仅运行前端

```bash
npm install
npm run dev:trip-map
```

默认无需启动后端，未登录的足迹保存在当前浏览器，可通过 JSON 导入导出备份。根目录 npm 脚本按应用命名（`dev:trip-map`、`build:trip-map` 等）；`build`、`test` 对所有应用运行；地图数据脚本（`check:maps`、`prepare:*`、`download:maps`）目前转发到 `@kongken/trip-map`。

### 前后端联调

首次准备本地配置与依赖：

```bash
cp config.tripmap.example.yaml config.tripmap.yaml
cp config.bff.example.yaml config.bff.yaml
make deps-up
# Compose 默认只创建 tripmap 数据库；首次使用时另建 BFF 认证库。
docker compose exec postgres createdb -U tripmap bff
make storage-init
make dev-secrets
```

`make deps-up` 启动 PostgreSQL、Redis 与 S3 开发依赖；`make storage-init` 初始化 S3 bucket；`make dev-secrets` 生成本地 CA、tripmap TLS 证书与 BFF 签名密钥。配置文件和 `dev-secrets/` 均已被 Git 忽略。已有本地环境时保留配置、数据库和密钥，跳过对应的首次初始化步骤。

启动两个后端服务：

```bash
make dev
```

也可在不同终端分别运行 `make dev-bff` 和 `make dev-tripmap`。另开终端启用前端后端连接：

```bash
npm install
VITE_API_BASE_URL=/api npm run dev:trip-map
```

Vite 将 `/api`、`/s/` 和健康检查请求代理到 BFF `http://127.0.0.1:18081`。BFF 使用其他地址时，通过 `TRIPMAP_DEV_BACKEND` 指定代理目标。生产构建同样需要在构建时设置 `VITE_API_BASE_URL`；配置、依赖端口与启动限制见 [后端初始化](docs/backend.md) 和 [BFF](docs/bff.md)。

停止开发依赖使用 `make deps-down`，数据卷会保留。

## 构建与验证

| 范围 | 命令 | 说明 |
| --- | --- | --- |
| 前端 | `npm run build` | 构建所有前端应用；单个应用用 `npm run build:trip-map`，山海足迹产物在 `apps/trip-map/dist/` |
| 前端 | `npm run preview:trip-map` | 预览已构建的山海足迹 |
| 前端 | `npm test` | 运行所有前端应用的脚本与业务逻辑测试（`npm run test:trip-map` 只跑山海足迹） |
| 前端 | `npm run test:e2e:trip-map` | 运行 Playwright 浏览器测试 |
| 地图与数据 | `npm run check:maps` | 检查地图、区域索引及前后端数据副本 |
| 后端 | `make build` | 构建 BFF、tripmap 与 legacy 账号导入工具至 `bin/` |
| 后端 | `make test` | 运行 Go 测试，未配置存储连接时相关集成测试跳过 |
| 后端 | `make test-integration` | 在独立的 Compose 开发依赖上验证存储与服务链路 |
| 服务边界 | `make check-import-boundaries` | 检查后端模块依赖边界 |
| 文档 | `make check-docs` | 检查 Markdown 相对链接的目标路径 |

修改 `proto/` 后运行 `make buf`；修改 tripmap / BFF Ent schema 后分别运行 `make ent` / `make ent-bff`，生成文件随变更一起提交。生产关闭两个服务的 `auto_migrate`，使用 `migrations/tripmap/` 与 `migrations/bff/` 中评审过的迁移，详见 [版本化数据库迁移](docs/backend.md#版本化数据库迁移)。

地图文件位于 `apps/trip-map/public/maps/`，区域、坐标、搜索索引与分享预览几何的更新命令和兼容规则见 [数据与地图说明](docs/trip-map/data-and-maps.md)。

## 部署

trip-map-web、BFF 与 tripmap 分别构建镜像，构建上下文均为仓库根目录：

| 部署单元 | Dockerfile |
| --- | --- |
| trip-map-web | `deploy/trip-map-web/Dockerfile` |
| BFF | `deploy/bff/Dockerfile` |
| tripmap | `deploy/tripmap/Dockerfile` |

GitHub Actions 按矩阵构建并发布三个镜像，镜像名由当前 GitHub 仓库名加组件后缀生成（`-tripmap`、`-trip-map-web`、`-bff`）；PR 仅构建。前端容器提供静态资源并把 API 与分享页请求转发到 BFF，业务服务只在内网暴露。部署拓扑、配置挂载与发布流程见 [部署与发布](docs/deployment.md) 和 [Docker 镜像与 Actions](docs/docker-images.md)。

已有 tripmap 账号升级到 BFF 认证时，先执行两个服务的版本化迁移，再使用 `bin/bff-import-legacy-accounts` 导入（先 `--dry-run`），抽查登录后再按需清理旧凭据列，详见 [Legacy 账号导入](docs/deployment.md#legacy-账号导入-44)。

照片直传、公开分享、OAuth 与通知需要额外配置，相关说明见下方文档。

## 项目文档

- [文档索引](docs/README.md)：全部专题文档入口。
- [前后端 monorepo 布局](docs/monorepo.md)：workspace、共享包与新增应用。
- [功能描述](docs/trip-map/features.md)：山海足迹的功能规则、验收场景与当前限制。
- [数据与地图说明](docs/trip-map/data-and-maps.md)：存储、备份、区域版本和地图维护。
- [后端初始化](docs/backend.md)：依赖、配置、媒体直传、通知、模型与迁移。
- [BFF](docs/bff.md)：认证、浏览器契约、内部 gRPC 与服务边界。
- [公开分享页与预览图](docs/trip-map/sharing.md)：分享模型、隐私与部署配置。
- [部署与发布](docs/deployment.md)：部署拓扑、Secret 与升级顺序。
- [项目协作说明](AGENTS.md)：实现约定、文档同步与验证要求。
