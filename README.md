# Kongken Monorepo

一个同时承载前端应用、后端服务与共享基础代码的仓库。前端使用 npm workspaces，后端使用根目录的单个 Go module；各应用和服务独立构建、部署。

当前业务是「山海足迹」中国旅行地图：以省份探索、城市足迹和旅行记录为核心，支持本地保存，以及可选的账号登录、云端同步和公开分享。地图使用随应用分发的 GeoJSON，无需地图 API Key；省内与全国地图可选开启联网底图（默认关闭，默认 OpenStreetMap；部署者也可经服务端代理启用 Mapbox，见 [数据与地图说明](docs/trip-map/data-and-maps.md)）。完整规则见 [功能描述](docs/trip-map/features.md)。

## 应用与服务

| 应用 / 服务 | 代码位置 | 职责 | 本地开发入口 |
| --- | --- | --- | --- |
| trip-map（山海足迹） | `apps/trip-map/` | React、Vite、TypeScript、Tailwind CSS、shadcn/ui 前端 | Vite 输出地址，通常为 `http://localhost:5173` |
| ozid（OzID 用户中心，T1、T2 已实现） | `apps/ozid/` | 独立的统一登录、注册与找回密码、邮件落地页（注册验证、密码重置）、账号概览与密码重新认证；登录后只返回服务端校验过的应用与路径（见 [OzID 功能描述](docs/ozid/features.md)） | Vite `http://localhost:5175`（需 BFF 配置 `navigation`） |
| admin（统一管理控制台，T4–T6、T8 已实现） | `apps/admin/` | 独立的管理控制台：只在来源白名单内提供页面，匿名跳 OzID 登录，无权限不循环，按真实角色显示分区，用户查询、普通账号状态/`metro-admin` 的受限修改（T5）、账号审计（T6）与 metro 目录管理（T8，已从 metro 公开站迁入）；详见 [控制台功能描述](docs/admin/features.md) | Vite 5176（本机开发） |
| bff | `cmd/bff/`、`internal/bff/` | 唯一公网 API 入口；账号接口（经 usersrv）、Cookie 会话、CSRF、账号注销工作流和业务接口适配 | HTTP `127.0.0.1:18081` |
| tripmap | `cmd/tripmap/`、`internal/tripmap/` | 足迹、行程、媒体、分享等旅行领域业务 | 内网 HTTP `127.0.0.1:18082`；内部 gRPC `127.0.0.1:18083`（默认 TLS，可显式选 h2c） |
| usersrv（自建用户系统，P1–P6 已实现） | `cmd/usersrv/`、`internal/usersrv/`、`cmd/usersrv-admin/` | 邮箱＋密码账号、注册验证与找回密码流程、角色与账号安全状态、邮件发送模块（四种 adapter）；只有内部 gRPC，由 BFF 调用；登录、注册、找回密码与账号设置都在 OzID（P5 最初在 metro 实现，共用代码在 `packages/account`；T2 起新邮件统一落到 OzID，T10、T11 起 trip-map 与 metro 没有账号表单，只保留 OzID 入口和旧邮件的落地页），有镜像 `mono-usersrv` 与部署示例（[部署](docs/deployment.md#usersrv-的部署)）；真实邮件厂商没有实测，开放前须手动冒烟（见 [规格](docs/usersrv/spec.md#验证状态)） | 仅内网：HTTP `127.0.0.1:18086`、gRPC `127.0.0.1:18087`（需自己的数据库与 Redis） |
| metro（地铁足迹，第二个产品，可选） | `apps/metro/`、`cmd/metro/`、`internal/metro/` | 城市、线路、车型目录与骑行者记录；管理后台；BFF 上的 MCP 端点（见 [功能描述](docs/metro/features.md)） | 前端 `http://localhost:5174`；内网 HTTP `127.0.0.1:18084`、gRPC `127.0.0.1:18085` |

浏览器经 BFF 的 Connect 接口访问后端（账号与会话是共用的 `bff.v1`，山海足迹业务是 `bff.tripmap.v1`）；BFF 使用内部凭证，通过内部 gRPC（默认 TLS；可信内网可显式配置 h2c，见 [BFF](docs/bff.md#内部-grpc-与凭证)）调用 tripmap。tripmap 的 HTTP 端口提供 `/ping`、`/readyz` 健康检查；启用 Mapbox 底图时还提供匿名的 `GET /api/tiles/…`，由 BFF 转发。账号与会话保存在 BFF 的独立数据库，旅行数据由 tripmap 管理。当前有两个后端服务，其中 tripmap 是唯一的业务服务，详见 [BFF](docs/bff.md)。

## 仓库结构

```text
apps/trip-map/        山海足迹前端应用
apps/metro/           地铁足迹前端应用（含管理后台）
apps/ozid/            OzID 用户中心前端应用
apps/admin/           统一管理控制台前端应用（受控入口，T4；T8 起含 metro 目录管理）
packages/
  ui/                 共享 shadcn/ui 组件
  api-client/         Buf 生成的 TypeScript RPC 契约
  geo-data/           区域索引、坐标与地图辅助数据
  fixtures/           前后端共同验证的测试夹具
  metro-core/         metro 公开站与统一控制台共用的纯函数（线路状态、里程格式）
  account/            trip-map、metro 与 OzID 共用的邮箱账号流程（RPC 客户端、表单、CSRF 重放与返回确认；表单只由 OzID 使用，trip-map 与 metro 只保留邮件落地页以服务旧邮件）
cmd/
  bff/                BFF 服务入口
  tripmap/            旅行业务服务入口
  metro/              地铁足迹业务服务入口
  usersrv/            用户系统账号核心（由 BFF 经内部 gRPC 调用）
  usersrv-admin/      usersrv 的角色与账号状态运维命令
  ...                 数据迁移、存储初始化与密钥生成工具
internal/
  bff/                BFF 认证、接口适配与 Ent schema
  tripmap/            旅行业务、存储与 Ent schema
  platform/           跨服务共享基础代码
  e2e/                BFF → tripmap 端到端测试
proto/                bff、tripmap 与 platform 的 Protobuf 契约
pkg/proto/            生成的 Go RPC 代码
migrations/           按 bff、tripmap、metro、usersrv 分开的版本化数据库迁移
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

默认无需启动后端，未登录的足迹保存在当前浏览器，可通过 JSON 导入导出备份。根目录 npm 脚本按应用命名（`dev:trip-map`、`build:trip-map` 等）；`test` 对所有应用运行，`build` 只构建 trip-map（不带后端变量时是纯本地站点）；metro、OzID 与控制台没有本地模式，必须带绝对 API 地址单独构建（`build:metro`、`build:ozid`、`build:admin`，metro 与控制台还要 `VITE_OZID_ORIGIN`），trip-map 带后端时同样须同时给绝对 API 地址与 `VITE_OZID_ORIGIN`；地图数据脚本（`check:maps`、`prepare:*`、`download:maps`）目前转发到 `@kongken/trip-map`。

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

`make deps-up` 启动 PostgreSQL、Redis 与 S3 开发依赖；`make storage-init` 初始化 S3 bucket；`make dev-secrets` 仅生成 BFF 的 Ed25519 签名密钥对。本地示例显式使用 h2c，不需要 TLS 证书；也可在配置中填写配套的 `private_key` / `public_key` PEM（替换对应 `*_key_file`），无需独立密钥文件，见 [BFF 配置](docs/bff.md#配置内联签名密钥)。TLS 开发另用 `go run ./cmd/dev-secrets -tls` 并同步配置两端。配置文件和 `dev-secrets/` 均已被 Git 忽略。已有本地环境时保留配置、数据库和密钥，跳过对应的首次初始化步骤；升级不会自动修改已有配置。

启动后端服务（BFF、tripmap 与可选的 metro；metro 需要自己的数据库和 `config.metro.yaml`，见 [后端初始化](docs/backend.md#metro-服务地铁足迹)）：

```bash
make dev
```

也可在不同终端分别运行 `make dev-bff`、`make dev-tripmap` 和 `make dev-metro`。地铁足迹的前端另开终端运行 `make dev-metro-web`（等价 `npm run dev:metro`，端口 5174；开发服务器把 `/api` 代理到 BFF 以浏览目录，要登录需在 `apps/metro/.env.local` 给出 `VITE_API_BASE_URL`（BFF 的公开 API 地址，如 `http://localhost:18081/api`）与 `VITE_OZID_ORIGIN`（OzID 的 origin，如 `http://localhost:5175`），没有则登录提示“账号中心尚未配置”；账号页面都在 OzID，目录管理在统一控制台，见 [部署](docs/deployment.md#地铁足迹metro可选)）。OzID 用户中心另开终端运行 `npm run dev:ozid`（端口 5175，直接调用 `.env.development` 里的回环示例 API 地址 `http://localhost:18081/api`；`config.bff.example.yaml` 已含对应的 `navigation` 与 CORS 示例，生产构建需给出绝对 `VITE_API_BASE_URL`，见 [OzID 功能描述](docs/ozid/features.md#构建镜像与运行)；本地可用 `npm run test:e2e:ozid`，真实浏览器链路 `OZID_BROWSER_E2E=1 go test ./internal/e2e -run TestOzidBrowserChain`）。另开终端启用前端后端连接：

```bash
npm install
VITE_API_BASE_URL=/api VITE_OZID_ORIGIN=http://localhost:5175 npm run dev:trip-map
```

Vite 将 `/api`、`/s/` 和健康检查请求代理到 BFF `http://127.0.0.1:18081`。BFF 使用其他地址时，通过 `TRIPMAP_DEV_BACKEND` 指定代理目标。山海足迹没有自己的登录、注册和账号设置：所有账号入口都跳转到 OzID，所以配置了后端时还要给出 `VITE_OZID_ORIGIN`（开发用上面的 OzID 地址）。生产构建必须同时给出绝对的 `VITE_API_BASE_URL`（会话 Cookie 属于 API 主机，同源 `/api` 代理无法共享）与 `VITE_OZID_ORIGIN`，缺失或不合法时构建失败；不设置 `VITE_API_BASE_URL` 则是纯本地的独立站点，没有账号入口；配置、依赖端口与启动限制见 [后端初始化](docs/backend.md) 和 [BFF](docs/bff.md)。

停止开发依赖使用 `make deps-down`，数据卷会保留。

## 构建与验证

| 范围 | 命令 | 说明 |
| --- | --- | --- |
| 前端 | `npm run build` | 构建所有前端应用；单个应用用 `npm run build:trip-map`，山海足迹产物在 `apps/trip-map/dist/` |
| 前端 | `npm run preview:trip-map` | 预览已构建的山海足迹 |
| 前端 | `npm test` | 运行所有前端应用的脚本与业务逻辑测试（`npm run test:trip-map` 只跑山海足迹） |
| 前端 | `npm run test:e2e:trip-map` | 运行 Playwright 浏览器测试 |
| 前端 | `npm run build:metro`、`npm run test:metro`、`npm run test:e2e:metro` | 构建（需 `VITE_API_BASE_URL` 与 `VITE_OZID_ORIGIN`）、单元测试与 Playwright 测试地铁足迹（浏览器测试使用模拟 BFF；真实跨 origin 链路 `METRO_BROWSER_E2E=1 go test ./internal/e2e -run TestMetroBrowserChain`） |
| 地图与数据 | `npm run check:maps` | 检查地图、区域索引及前后端数据副本 |
| 后端 | `make build` | 构建 BFF、tripmap、metro、usersrv 与 usersrv-admin 至 `bin/` |
| 后端 | `make test-integration-usersrv` | 用本地 PostgreSQL 与 Redis 运行 usersrv 的全部测试（独立 schema 与 key 前缀） |
| 后端 | `make test` | 运行 Go 测试，未配置存储连接时相关集成测试跳过 |
| 后端 | `make test-integration` | 在独立的 Compose 开发依赖上验证存储与服务链路 |
| 服务边界 | `make check-import-boundaries` | 检查后端模块依赖边界 |
| 文档 | `make check-docs` | 检查 Markdown 相对链接的目标路径 |

修改 `proto/` 后运行 `make buf`；修改 tripmap / BFF / metro / usersrv Ent schema 后分别运行 `make ent` / `make ent-bff` / `make ent-metro` / `make ent-usersrv`，生成文件随变更一起提交。生产关闭各服务的 `auto_migrate`，使用 `migrations/<服务>/` 中评审过的迁移，详见 [版本化数据库迁移](docs/backend.md#版本化数据库迁移)。

地图文件位于 `apps/trip-map/public/maps/`，区域、坐标、搜索索引与分享预览几何的更新命令和兼容规则见 [数据与地图说明](docs/trip-map/data-and-maps.md)。

## 部署

trip-map-web、BFF、usersrv、tripmap 与可选的 metro、metro-web 分别构建镜像，构建上下文均为仓库根目录：

| 部署单元 | Dockerfile |
| --- | --- |
| trip-map-web | `deploy/trip-map-web/Dockerfile` |
| BFF | `deploy/bff/Dockerfile` |
| usersrv | `deploy/usersrv/Dockerfile` |
| tripmap | `deploy/tripmap/Dockerfile` |
| metro | `deploy/metro/Dockerfile` |
| metro-web | `deploy/metro-web/Dockerfile` |

GitHub Actions 按矩阵构建并发布各镜像，镜像名由当前 GitHub 仓库名加组件后缀生成（`-tripmap`、`-trip-map-web`、`-bff`、`-usersrv`、`-metro`、`-metro-web`）；PR 仅构建。前端容器提供静态资源并把 API 与分享页请求转发到 BFF，业务服务只在内网暴露。部署拓扑、配置挂载与发布流程见 [部署与发布](docs/deployment.md) 和 [Docker 镜像与 Actions](docs/docker-images.md)。

usersrv 是唯一账号来源，运行时已没有 Logto，旧的 Logto 账号与测试账号不迁移。手动清理的对象、目标库、执行顺序及限制见 [Logto 退役与手动清理](docs/usersrv/retirement.md) 与 [自建用户系统的数据迁移](docs/deployment.md#自建用户系统的数据迁移)；脚本不会自动执行。

照片直传、公开分享、邮件与通知需要额外配置，相关说明见下方文档。

## 项目文档

- [文档索引](docs/README.md)：全部专题文档入口。
- [前后端 monorepo 布局](docs/monorepo.md)：workspace、共享包与新增应用。
- [功能描述](docs/trip-map/features.md)：山海足迹的功能规则、验收场景与当前限制。
- [数据与地图说明](docs/trip-map/data-and-maps.md)：存储、备份、区域版本和地图维护。
- [地铁足迹功能描述](docs/metro/features.md)：第二个产品的页面、管理后台、管理令牌与 MCP。
- [后端初始化](docs/backend.md)：依赖、配置、媒体直传、通知、模型与迁移。
- [BFF](docs/bff.md)：认证、浏览器契约、内部 gRPC 与服务边界。
- [usersrv 规格](docs/usersrv/spec.md)：自建用户系统的目标、分阶段交付与各阶段实现状态（P1–P6 已实现）。
- [公开分享页与预览图](docs/trip-map/sharing.md)：分享模型、隐私与部署配置。
- [部署与发布](docs/deployment.md)：部署拓扑、Secret 与升级顺序。
- [项目协作说明](AGENTS.md)：实现约定、文档同步与验证要求。
