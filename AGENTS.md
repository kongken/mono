# 项目协作说明

本仓库（`kongken/mono`）是多产品 monorepo：每个产品有自己的前端应用 `apps/<产品>`、业务服务 `internal/<产品>`、浏览器契约 `proto/bff/<产品>/v1` 与产品文档 `docs/<产品>/`；账号、会话与公网入口（BFF）由各产品共用。当前有两个产品：「山海足迹」（目录名 `trip-map`，后端业务服务名 `tripmap`）和「地铁足迹」（`metro`，可选）；另有跨产品的用户中心 OzID（`apps/ozid`，没有自己的业务服务，只调用 BFF，见 [OzID 功能描述](docs/ozid/features.md)）和统一管理控制台（`apps/admin`，只调用 BFF，只在来源白名单内提供页面，见 [控制台功能描述](docs/admin/features.md)）。

山海足迹是中国旅行地图 WebApp，前端使用 React、Vite、TypeScript 和 shadcn/ui；后端沿用 neo-box 的 Go、Butterfly、Gin、ConnectRPC、Ent，基础设施使用 PostgreSQL、Redis 和 S3。保持中文旅行手账风格，以省份探索和城市足迹记录为核心。

## 按任务读取文档

- 修改用户可见功能或交互前，阅读 [功能描述](docs/trip-map/features.md)，确认现有规则和验收场景。
- 修改存储、备份、行政区域或地图绘制前，阅读 [数据与地图说明](docs/trip-map/data-and-maps.md)，确认兼容性和边界数据限制。
- 修改后端启动、接口、数据库 schema、Redis 或 S3 前，阅读 [后端初始化说明](docs/backend.md)。
- 查找文档入口和维护方式时，阅读 [文档索引](docs/README.md)。运行命令以 `package.json` 为准，启动说明见根目录 `README.md`。

## 实现约定

- 复用 `packages/ui/src/` 中的 shadcn 组件（在 `apps/trip-map` 内引用为 `@kongken/ui/button` 等），图标使用 `@phosphor-icons/react`，沿用现有浅色、绿色主题和响应式布局。
- 全国地图、省内地图与目的地列表使用一致的区域 ID；台湾补充区域使用 `tw-` 前缀。区域名称用于展示，ID 用于识别。
- 省份独立标记与城市足迹分别存储。统计中的已探索省份取两者并集；移除一类记录时保留另一类记录。
- 地图使用随项目分发的 GeoJSON；默认保持运行时无需第三方地图请求或 API Key。联网底图是显式启用的可选能力（OSM 或服务端代理的 Mapbox，见 [数据与地图说明](docs/trip-map/data-and-maps.md)），Mapbox token 只留在 tripmap 配置，不进入前端。
- 更改存储结构或区域索引时，处理已有足迹的兼容或迁移，避免将旧数据直接当作空数据覆盖。

- 前端是 npm workspaces monorepo，细节见 [前后端 monorepo 布局](docs/monorepo.md)：`apps/<应用>` 是可独立构建部署的前端应用（当前有 `apps/trip-map`、`apps/metro`、`apps/ozid` 与 `apps/admin`，包名 `@kongken/trip-map`、`@kongken/metro`、`@kongken/ozid`、`@kongken/admin`），`packages/` 放跨应用或前后端共用的代码和数据（`ui`、`api-client`、`geo-data`、`fixtures`、`account`）。应用之间不互相 import，共用部分抽到 `packages/`；只被一个应用使用的代码留在该应用内，出现第二个使用方时再抽出。
- `packages/api-client/src/` 是 `make buf` 的生成目录，`packages/geo-data/data/` 的区域索引与 `internal/tripmap/regions/`、`internal/tripmap/itinerary/` 里的后端副本必须一致，`packages/fixtures/` 是前后端共同验证的夹具；它们的更新方式见 [数据与地图说明](docs/trip-map/data-and-maps.md)。

## 后端约定

- Go module 位于根目录；前端是 npm workspaces（见下节“前端约定”）。一个仓库承载多个后端服务：`cmd/<服务>/` 为入口，业务代码放在 `internal/<业务>/`（tripmap 的 `app/` 装配、`application/` RPC 服务、存储模块和 Ent schema 都在 `internal/tripmap/`），跨服务共享代码放在 `internal/platform/`。
- 业务包之间不互相 import，`internal/platform/` 不依赖任何业务包；只被一个业务使用的代码留在该业务目录，出现第二个使用方时再移入 `internal/platform/`。各服务镜像只复制本服务与 `internal/platform`，越界 import 会使镜像构建失败。
- BFF（`cmd/bff`、`internal/bff/`）是唯一公网入口，统一处理跨域、客户端地址（覆盖写入 `X-Real-IP`）和追踪脱敏；业务服务只在内网监听，提供 `/ping` 与 `/readyz`。账号、会话、CSRF 与入口策略是共用的（`bff.v1`、`internal/bff/auth` 等）；每个产品的浏览器契约放在 `proto/bff/<产品>/v1`（包名 `bff.<产品>.v1`），BFF 适配代码放在 `internal/bff/<产品>/`，并由该包的 `Mount*` 函数在 `internal/bff/app` 登记，产品之间不互相 import。新增业务或公开路径时在 `internal/bff/gateway/services.go` 登记 proto 包、HTTP 路径和浏览器请求头，携带凭据的路径填写 `Trace`，并在 BFF 配置的 `upstreams` 中增加地址。
- 修改 `proto/` 后运行 `make buf`，修改 `internal/<业务>/ent/schema/` 后运行 `make ent`，将生成的 Go / TypeScript / Ent 文件一起提交；生成文件只通过生成器更新。版本化迁移按业务放在 `migrations/<业务>/`。
- 私有业务接口先实现认证与用户隔离，当前公共接口除健康状态外，只有可选的匿名瓦片代理 `GET /api/tiles/{z}/{x}/{y}.png`（默认关闭，见 [后端初始化](docs/backend.md)）；城市区域 ID 规则与前端一致。
- 配置示例使用本地开发值，真实凭据放在忽略的 `config.tripmap.yaml` 或挂载 Secret 中。
- `usersrv`（自建用户系统，阶段状态见 [规格](docs/usersrv/spec.md)）是内部账号服务：`internal/usersrv` 不 import 其他业务，BFF 不 import `internal/usersrv`，只经 `usersrv.v1` 内部 gRPC 调用（`internal/bff/usersrv` 只放错误映射）；目前实现 P1（账号核心）、P2（邮件 adapter 与加密 outbox）、P3（注册验证、邮箱密码登录、找回密码）、P4（BFF 会话、每请求 `GetAuthState`、注销工作流、产品鉴权）P5（trip-map 与 metro 的登录、注册、找回密码、账号设置和邮件落地页，共用代码在 `packages/account`；OzID 复用其中的表单、邮件落地页与 API 客户端：T2 起注册、找回密码和邮件落地页集中在 OzID，新邮件的链接都指向 `auth.apps.ozid`（必填），申请时校验过的来源应用与路径随挑战保存，旧产品落地页只服务升级前发出的邮件，见 [OzID 功能描述](docs/ozid/features.md)）与 P6（镜像、部署示例与 Logto 退役文档，见 [退役说明](docs/usersrv/retirement.md)）。BFF 不保存密码，运行时没有 Logto 或 OAuth 代码。邮件的真实厂商只做手动冒烟验证（至今没有对任何真实厂商实测），自动化测试只连本地假服务。跑 `make test-integration` 需要真实 Redis 并设置 `USERSRV_TEST_*`，否则账号链整体跳过。修改其 Ent schema 用 `make ent-usersrv`，迁移用 `make usersrv-migration`，测试用 `make test-integration-usersrv`。

## 文档同步

- 新增、调整或删除用户可见行为时，在同一次变更中更新 `docs/trip-map/features.md` 的对应功能规则和验收场景。
- 更改数据字段、版本、备份校验、地图来源或区域映射时，同步更新 `docs/trip-map/data-and-maps.md`。
- 后端配置、模型或接口改变时同步更新 `docs/backend.md`，BFF 路由、配置或部署拓扑改变时同步更新 `docs/bff.md`；业务功能实际实现后更新功能描述及调研状态。
- 新建专题文档后，在 `docs/README.md` 增加入口；启动或常用命令改变时，更新根目录 `README.md`。
- 文档区分“当前已实现”与“当前限制”。尚未实现的想法如需记录，单独标明待实现，避免写成现有功能。
- 详细功能规则以 `docs` 为主，根目录 `README.md` 保留项目简介、运行方式和文档入口，减少重复维护。

## 按变更验证

- 应用代码或构建配置改变后运行 `npm run build`；标记或自动配色逻辑改变后另运行 `npm test`。
- 后端改变后运行 `make test` 和 `make build`；存储连接、模型或对象操作改变后，另在独立开发环境运行 `make test-integration`；契约变化后检查 `buf lint` 和前端构建。
- 地图数据、区域索引、投影或归一化逻辑改变后，另运行 `npm run check:maps`；数据更新涉及的文件一起提交。
- 用户交互改变后，在浏览器验证对应功能场景；布局改变后检查桌面及手机尺寸，清理自行创建的测试足迹。
- 仅文档变更时检查路径、链接和描述与实现是否一致，无需为此重新构建应用。
- 完成时说明功能与文档的变化、验证结果及仍存在的限制。

## Agent skills

### Issue tracker

需求、任务和规格使用 GitHub Issues；操作前阅读 [Issue tracker](docs/agents/issue-tracker.md)。

### Triage labels

分流使用五类标准标签；操作前阅读 [Triage labels](docs/agents/triage-labels.md)。

### Domain docs

采用 single-context。探索代码、命名领域概念或调整架构前，阅读 [Domain docs](docs/agents/domain.md)。
