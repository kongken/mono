# 后端初始化

## 当前状态与技术栈

后端基础设施已初始化，参考 `neo-box` 的技术栈和分层：Go 1.26.5+、Butterfly、Gin、ConnectRPC、Ent、PostgreSQL、Redis、AWS SDK for Go v2（S3）。依赖版本以 `go.mod` 为准，RPC 契约使用 Buf 生成 Go 与 TypeScript 代码。

当前已实现服务启动、依赖检查、Ent 建表、S3 对象存储封装与基础客户端。浏览器身份统一由 Logto 提供，BFF（`bff.v1.AuthService`）只保存 `(issuer, sub)` 映射、资料缓存和 Cookie/CSRF 会话；tripmap 只保留业务 profile 与内部 gRPC，不再挂载浏览器 Connect。旧密码账号 RPC 仅作为 deprecated 的 unimplemented 兼容契约保留，不再创建或验证密码账号。BFF 身份与会话迁移见 `migrations/bff/20261023095053_logto_identity_mapping.sql`，旧测试表的手动清理见 `deploy/sql/bff/retire_password_accounts.sql`。足迹读写经 `bff.tripmap.v1.FootprintService` → tripmap gRPC（服务端校验、用户隔离、revision 与限流），见 [功能描述](trip-map/features.md) 与 [BFF](bff.md)。前端持久化离线修改、重连三方合并及同记录冲突选择已实现。已补齐账号数据删除、增量变更日志与批量同步；云端快照与恢复（F3，`SnapshotService`）已实现；F4 照片与附件已实现；F6 公开分享页与预览图已实现（默认关闭，见 [公开分享页与预览图](trip-map/sharing.md)）；F12 提醒与通知已实现（邮件与浏览器推送默认关闭，见下文 F12）；真实 Logto 授权验收仍待部署。未登录的城市足迹继续保存在浏览器；本地足迹迁移（F2）完全由前端经批量同步接口发起，后端没有专门的迁移接口，也不会自行读取或覆盖本地记录；云端已有足迹时，前端会在迁移前调用 `CreateSnapshot` 保存一份快照。

Redis 客户端已接入，后续用于会话、缓存或限流；本次没有把城市足迹存入 Redis。对象内容使用 S3，PostgreSQL 只存业务记录和媒体元数据。

## 项目结构

仓库按服务组织：`cmd/<服务>/` 为入口，业务代码在 `internal/<业务>/`，跨服务共享代码在 `internal/platform/`，版本化迁移在 `migrations/<业务>/`。当前只有 tripmap 一个业务；业务包之间不互相 import，`internal/platform/` 不依赖业务包。公网请求经 BFF（`cmd/bff`）转发到业务服务，见 [BFF（公网入口）](bff.md)。

| 路径 | 职责 |
| --- | --- |
| `cmd/bff/`、`internal/bff/`（共用的账号与会话；各产品的领域适配在 `internal/bff/<产品>/`，tripmap 的在 `internal/bff/tripmap/`） | BFF：公网唯一浏览器 RPC（`bff.v1` 账号、`bff.<产品>.v1` 业务，Connect、Cookie/CSRF），经内部 gRPC（TLS / h2c）调 tripmap（[说明](bff.md)） |
| `cmd/tripmap/` | Butterfly 服务入口，配置加载、启动、退出资源清理 |
| `internal/tripmap/app/` | Bootstrap、依赖注入、Gin 路由、内部 gRPC 服务装配（`grpc.go`） |
| `internal/tripmap/config/` | 业务配置及必填存储名称校验 |
| `internal/tripmap/tiles/` | 可选的 Mapbox 瓦片代理：`GET /api/tiles/{z}/{x}/{y}.png` 的配置校验、请求校验、限流与上游访问（[说明](#mapbox-瓦片代理可选)） |
| `internal/tripmap/application/` | 内部 gRPC 与测试用 Connect harness：`FootprintService`、`SnapshotService`、`MediaService`、`CollaborationService`、`ReportService`、`SearchService`、`NotificationService`、`AccountCleanupService` 等；HTTP 仅 `/ping`、`/readyz` 与可选的 `/api/tiles/…`（分享/退订公开页在 BFF） |
| `internal/bff/auth/`、`internal/bff/oauth/` | BFF 会话、Logto OAuth state（Redis）及 Logto/PKCE provider |
| `internal/tripmap/share/` | F6：公开内容白名单与快照构建、统计、预览图渲染（纯 Go）、HTML 元数据注入、受信任 index.html 获取；`assets/` 含预览几何和 Noto Sans SC 子集 |
| `internal/tripmap/report/` | F8：统计总览、按时区计算的年度报告、8 项旅行成就及可追溯记录引用计算（与前端 `apps/trip-map/src/lib/report.ts` 共用 `packages/fixtures/report-stats.json` 测试夹具） |
| `internal/tripmap/notification/` | F12：提醒内容规则（回忆匹配、年度窗口、文案）、退订令牌签名、SMTP 发送、Web Push 发送与 SSRF 防护（纯 Go，可单独测试） |
| `cmd/vapid-keygen/` | `make vapid-keys`：生成 Web Push 的 VAPID 密钥对 |
| `internal/tripmap/regions/` | 嵌入的行政区域索引，供服务端校验区域 ID、名称与归属 |
| `internal/tripmap/testutil/` | 带独立 schema 和 Redis key 前缀的集成测试辅助 |
| `internal/tripmap/blobstore/` | S3 Put/Get/Delete 与 bucket 检查，不依赖具体供应方 |
| `internal/tripmap/ent/schema/` | 数据库 schema 源文件 |
| `internal/tripmap/ent/` | 生成的 Ent 客户端与迁移结构 |
| `proto/tripmap/v1/` | Protobuf 接口定义 |
| `pkg/proto/`、`packages/api-client/src/` | 生成的 Go / TypeScript 契约 |
| `apps/trip-map/src/api/system.ts` | 前端 Connect-Web 匿名状态客户端（`bff.tripmap.v1.StatusService`，无 Authorization、无 Cookie） |
| `apps/trip-map/src/components/StatusPage.tsx` | 公开的 `/status` 服务状态页 |
| `cmd/storage-init/` | 显式初始化配置中指定的 S3 bucket |
| `internal/tripmap/integration/` | 可选的实际 PostgreSQL、Redis、S3 集成测试 |
| `internal/platform/health/` | 跨服务共享：有超时的依赖就绪检查 |
| `internal/platform/httpx/` | 跨服务共享：客户端地址、跨域应答、来源校验与追踪脱敏 |
| `internal/platform/servicetoken/` | 跨服务共享：BFF 与业务服务之间 60 秒 Ed25519 内部凭证的签发、校验与密钥加载 |
| `internal/platform/rpcauth/` | 跨服务共享：gRPC 客户端/服务端拦截器、proto 匿名选项读取、metadata 白名单与类型化调用者信息 |
| `internal/platform/grpcx/` | 跨服务共享：自管 gRPC 监听（默认 TLS，显式 h2c）、消息大小限制与优雅关闭 |
| `internal/platform/testkit/` | 跨服务共享：测试用临时 CA、证书与签名密钥 |
| `internal/e2e/` | 仅测试：真实 BFF → 内部 gRPC（TLS 与 h2c）→ 真实 tripmap 的端到端测试（[说明](bff.md#验证)） |
| `cmd/dev-secrets/` | `make dev-secrets`：生成本地开发签名密钥对；显式 `-tls` 才生成 CA 与证书 |

## 媒体接口（F4，`bff.tripmap.v1.MediaService` → `tripmap.v1.MediaService`）

浏览器经 BFF `bff.tripmap.v1.MediaService`（Cookie 会话，写操作 CSRF）；BFF 适配 tripmap gRPC `MediaService`（内部 Ed25519 凭证 + 用户 ID）。全部方法只取当前用户 ID；旅行附件写权限入口为 `AuthorizeTripMediaWrite`（共同旅行 active 的 owner/editor 可上传，viewer 拒绝；共享列表/读取走 `SharedTripService`）。媒体使用 `region_code` 关联目的地，和足迹 revision、快照独立。tripmap HTTP 不再挂载 `MediaService` Connect。

| 方法 | 行为 |
| --- | --- |
| `PrepareUpload` | 接收区域、可选 `trip_id`、文件名、MIME、大小和 UUID `upload_id`；`trip_id` 非空时该旅行必须属于同一用户和该区域（版本 1 账号只接受 `legacy-<区域 ID>`），否则 `invalid_argument`；原子预留数量，返回媒体 ID、10 分钟上传 URL 与必须携带的头。相同 ID/内容重放返回原预留；内容不同返回 `already_exists`。已完成项不再签发上传 URL，过期预留需删除后重新创建 |
| `CompleteUpload` | 读取临时对象，限量读取并校验实际大小/图片格式、尺寸、解码或 PDF 头尾，生成处理后文件与缩略图，再标记 `ready`；响应丢失可重复确认，失败仍可重试，超过 24 小时拒绝确认 |
| `ListMedia` | 返回当前用户媒体元数据，可按区域和 `trip_id` 筛选，元数据含 `trip_id`；包括 `pending`，隐藏 `deleting`，不泄露对象 key |
| `ReadMedia` | 只读取本人 `ready` 文件/缩略图，默认以 RPC bytes 返回；配置 `storage.cdn_domain` 时返回公开 `url`（`data` 为空），`Cache-Control: no-store`；他人、不存在或已删除项不提供内容，PDF 不提供缩略图 |
| `DeleteMedia` | 先关闭访问再清理三个对象 key，重复删除幂等；S3 失败返回 `unavailable`，保留清理任务供重试与维护任务处理 |

默认 JPEG/PNG/WebP/PDF，10 MiB，20 个/城市（含待确认），签发 60 次/用户/小时；图片最多 24 MP、最长边 8000 px，缩略图最长边 480 px。照片重新编码 JPEG 不复制 EXIF/GPS；PDF 只校验头尾、供下载，不做内容解析或杀毒。上传/确认通过用户行锁串行，与云端数据删除互斥；服务端并发图像处理最多两个，并在像素解码前检查尺寸。确认最多 30 秒。`trip_id` 是软引用（无外键）：删除旅行、取消城市、快照恢复都不改写或删除媒体；媒体接口不受 F5 请求头门控，旧客户端仍可上传和读取。尚未支持 HEIC、媒体离线队列和分享。

临时 URL 仅签发随机 staging key；确认后的 canonical key 不可用该 URL 改写。[S3 签名 URL 可在有效期内重复使用](https://docs.aws.amazon.com/AmazonS3/latest/userguide/PresignedUrlUploadObject.html)，因此删除账号时仍保留不含身份的临时对象清理队列。每小时处理删除失败与 24 小时未完成的上传；队列 24 小时后再次删除临时对象。超长在途上传需由 staging 生命周期兜底；该规则不会删除处理后照片。

### 直传部署配置

先升级后端 schema（新增媒体字段和 `media_cleanups`），再部署前端。S3 endpoint 必须同时被后端和浏览器访问，正式站点使用 HTTPS；不可把容器内网地址签发给公网浏览器。Bucket 应为本项目私有、非版本化存储，不配置公共读取策略。

可选 `storage.cdn_domain: https://cdn.example.com` 为媒体启用**公开 CDN 读取**，留空保持原有私有读取。填写带协议的 HTTP(S) origin（正式站点使用 HTTPS），不含路径、凭据、查询串或片段。个人 `ReadMedia` 和共同旅行 `ReadSharedTripMedia` 仍先校验权限和 `ready` 状态，再返回 `url`、MIME 和文件名；`data` 为空。前端无 Cookie、Authorization 或 Referer 地请求 CDN，失败显示下载错误，不回退读取源站。未配置时仍返回原 RPC bytes。

URL 规则为 `<cdn_domain>/<storage.key_prefix>/<对象 key>`，前缀为空则省略，不自动添加 bucket 名。例如 `key_prefix: tripmap` 对应 `https://cdn.example.com/tripmap/media/files/<媒体ID>`，缩略图为 `tripmap/media/thumbnails/<媒体ID>`。CDN 需将此路径映射到 S3 bucket 内相同 key，可用私有源站授权，无需把整个 bucket 设为公开；仅开放处理后的 `media/files/` 和 `media/thumbnails/` 前缀，禁止公开 `media/staging/`。CDN 还需允许前端 origin 跨域 GET（CORS）；若部署配置了 CSP，需在 `connect-src` 允许 CDN origin。上传签名、确认、维护与删除仍使用 `store.s3` 客户端和原 endpoint。

**启用后的隐私和缓存限制：** 持有 CDN URL 者无需登录即可读取，没有 URL 签名或到期时间；退出共同旅行、解除好友或关闭本配置，只阻止 API 再次提供 URL，不使已知链接失效。删除会清理 S3 对象，但已缓存内容需通过 CDN TTL 或运维刷新失效；服务不自动刷新 CDN。公开分享页继续使用原有令牌路由，不改写为 CDN 地址。应先部署支持 `url` 字段的新前端和 BFF，再启用本配置；旧客户端只读取 `data`，无法显示 CDN 模式的文件。不涉及数据库或对象 key 迁移。

浏览器直传需要 S3 CORS（和 API 的 `auth.cors_allowed_origins` 分开配置）。`storage-init` 提供显式选项，保留其他规则，仅替换 ID 为 `tripmap-media-upload` / `tripmap-media-staging` 的规则；不会在服务启动时更改 bucket：

```bash
go run ./cmd/storage-init -config config.tripmap.yaml \
  -media-cors-origins 'http://localhost:5173,http://127.0.0.1:5173' \
  -media-staging-lifecycle
```

正式环境换成实际前端来源，不允许 `*`；CORS 允许 PUT 与签名所需的请求头。生命周期仅对配置前缀下的 `media/staging/` 设置 2 天过期，用于清理极长在途或重放上传。初始化权限另需读写 bucket CORS / Lifecycle；运行服务只需已有对象读写删除权限。兼容供应方须支持这两类规则；不支持时由供应方配置等效临时目录清理任务。

CI 增加真实 S3 签名 PUT→校验/缩略图→私有读取→删除链路，真实 PostgreSQL/Redis 验证跨用户隔离、并发配额、请求重放、删除失败拒绝读取及删除云端数据后的临时清理；浏览器测试使用模拟 RPC，覆盖桌面/手机上传、丢失响应重试、下载、删除与格式拒绝，不表示正式部署已验收。

## 前端独立运行

后端是可选的。前端通过构建时环境变量 `VITE_API_BASE_URL` 决定是否连接后端：

- 未设置、为空或格式无效：纯本地模式，不创建 API 客户端，不向 `/api`、`/ping`、`/readyz` 发请求；全部功能使用浏览器本地存储。当前所有用户可见功能都属于这种情况，`npm run dev:trip-map`、`npm run build` 以及任意静态托管（含前端镜像默认构建）都无需后端。
- `/api` 这类同源路径，或 `https://api.example.com` 这类绝对 http(s) 地址：启用后端能力。`/api` 配合 Vite 开发代理或前端镜像的 Nginx 转发使用。

变量在构建时写入，修改后需重新构建；前端镜像通过 `--build-arg VITE_API_BASE_URL=/api` 传入，镜像发布工作流未传递，因此发布的前端镜像默认纯本地模式；CI 界面测试构建单独传入 /api。解析逻辑见 `apps/trip-map/src/lib/backend.ts`，传输层见 `apps/trip-map/src/api/transport.ts`（未启用时不创建，登录入口也不渲染），业务客户端见 `apps/trip-map/src/api/`。登录与云同步必须通过该开关判断；后端不可用时，未登录用户不受影响，有有效账号缓存的已登录用户可保留离线修改并重试同步；云端数据删除待完成或缓存异常时暂停编辑，也可退出登录回到本地模式，不会阻塞本地足迹的读写。前端同步与会话逻辑在 `apps/trip-map/src/lib/sync.ts`、`session.ts`、`oauth.ts` 和 `apps/trip-map/src/hooks/useCloud.ts`。

前端是 npm workspaces（`apps/trip-map` 加 `packages/*`，见 [前后端 monorepo 布局](monorepo.md)），后端 Go module 位于仓库根目录；前后端分别构建，共用 `proto/` 契约、区域索引和测试夹具。

## 本地开发

需要 Go 1.26.5+、Docker Compose、Node.js/npm。重新生成 RPC 代码另需 Buf，生成插件由 `buf.gen.yaml` 指定。

```bash
cp config.tripmap.example.yaml config.tripmap.yaml
cp config.bff.example.yaml config.bff.yaml
make deps-up
make storage-init
make dev-secrets
make dev
```

`make dev` 同时启动 BFF 和 tripmap；也可分别运行 `make dev-bff` 与 `make dev-tripmap`。本地示例在 loopback 上显式使用 h2c，`make dev-secrets` 仅生成签名密钥对，无需 TLS 证书。若在配置内填写配套 PEM（[内联签名密钥](bff.md#配置内联签名密钥)），可跳过该生成步骤；原有文件方式仍可用。已有配置不会自动切换传输。

另开终端启动前端：

```bash
npm install
npm run dev:trip-map
```

联调后端时在 `.env.local` 设置 `VITE_API_BASE_URL=/api` 并重启 Vite；不设置则前端保持纯本地模式。开发代理默认转发到 BFF `http://127.0.0.1:18081`，BFF 在其他端口时用环境变量 `TRIPMAP_DEV_BACKEND=http://127.0.0.1:端口` 启动 Vite。OAuth 登录由 BFF HTTP 入口持有：提供方登记的 callback 与 `config.bff.yaml` 的 `redirect_url` 一致，形如 `http://localhost:5173/auth/callback/logto`（本地 Vite `/auth/` 代理到 BFF；必须显式 `auth.oauth_development: true`）。生产使用固定 HTTPS BFF callback、受控 `oauth_apps` 与精确 CORS origins，配置与迁移见 [多 Web App 登录](bff.md#多-web-app-登录与迁移54)。前端不再接收授权码或执行 CompleteOAuthFlow。

Compose 只启动本项目的开发依赖，使用命名卷保存数据。S3 开发实例使用 [RustFS 官方容器](https://docs.rustfs.com/en/installation/container/docker)，提供持久化 S3 兼容接口；生产环境可配置 AWS S3 或其他兼容供应方，服务代码无需更换。

| 服务 | 本地地址 |
| --- | --- |
| PostgreSQL | `127.0.0.1:15432`，数据库 `tripmap` |
| Redis | `127.0.0.1:16379` |
| S3 API | `http://127.0.0.1:19000` |
| S3 开发控制台 | `http://127.0.0.1:19001` |
| BFF（公网入口） | `http://127.0.0.1:18081` |
| tripmap 服务 HTTP（仅供 BFF 访问） | `http://127.0.0.1:18082` |
| tripmap 服务 gRPC（本地示例 h2c，仅供 BFF 访问） | `127.0.0.1:18083` |
| Vite 前端 | 启动时输出地址，通常为 `http://localhost:5173` |

端口避开了现有 `neo-box` 开发服务。Vite 代理 `/api`、`/ping` 和 `/readyz` 到 BFF 的 `18081`；若改变 BFF 端口，同时更新代理配置。`make deps-down` 仅停止并移除本项目容器，保留数据卷。

## 配置与启动规则

`config.tripmap.example.yaml`（tripmap）和 `config.bff.example.yaml`（BFF，见 [BFF 配置](bff.md#配置)）提供本地示例。复制后的 `config.tripmap.yaml`、`config.bff.yaml` 和 `.env` 已被 Git 忽略，实际部署凭据只放在未跟踪配置或挂载的 Secret 中。

Butterfly 从 `BUTTERFLY_CONFIG_FILE_PATH` 读取同一个 YAML，`BUTTERFLY_CONFIG_TYPE=file` 启用文件模式。与 `neo-box` 一致，YAML 中的 `${VAR}` 不会自动展开。`.env.example` 提供文件模式与端口示例；`make dev` 直接设置这些变量，无需自动加载 `.env`。

### Consul 配置与路径前缀

BFF 和 tripmap 均可通过 Consul KV 读取配置。下面的变量在进程启动前设置，不写入业务 YAML；`make dev` 会显式启用文件模式，使用 Consul 时直接运行已构建的服务或配置容器环境变量。

| 环境变量 | 用途与默认行为 |
| --- | --- |
| `BUTTERFLY_CONFIG_TYPE` | `file` 读取本地文件；Consul 模式显式设为 `consul`。当前 Butterfly 对任何非 `file` 值都选择 Consul，因此应避免拼写错误 |
| `BUTTERFLY_CONFIG_CONSUL_ADDRESS` | Consul HTTP API 地址，例如 `http://127.0.0.1:8500`；部署时填写服务进程可访问的地址 |
| `BUTTERFLY_NAMESPACE` | 应用配置的 KV 路径前缀；未设置或空字符串时为 `apps`，首尾 `/` 会被去掉 |
| `BUTTERFLY_CONFIG_CONSUL_NAMESPACE` | 可选，传给 Consul API 客户端的 Namespace；只在 Consul 部署支持并使用该能力时设置，与 KV 路径前缀独立 |

配置 key 按 `<BUTTERFLY_NAMESPACE>/<服务名>` 拼接，服务名固定为 `tripmap` 和 `bff`，不添加 `.yaml` 后缀。默认 key 分别是 `apps/tripmap`、`apps/bff`；例如前缀设为 `kongken/prod` 时，读取 `kongken/prod/tripmap`、`kongken/prod/bff`。`BUTTERFLY_CONFIG_CONSUL_NAMESPACE` 不会拼进这些 key，不能用它替代路径前缀。

每个 key 的 value 保存对应服务的**完整 YAML 内容**（业务配置及 `log`、`store` 等框架配置），结构与 `config.tripmap.example.yaml` 或 `config.bff.example.yaml` 一致；不将 YAML 字段拆成多个 KV key。Consul 模式不使用 `BUTTERFLY_CONFIG_FILE_PATH`，文件模式忽略 KV key；两种模式都不会展开 YAML 中的 `${VAR}`。

例如先在 Consul 写入 `kongken/prod/tripmap` 的完整 YAML，再启动 tripmap：

```bash
export BUTTERFLY_CONFIG_TYPE=consul
export BUTTERFLY_CONFIG_CONSUL_ADDRESS=http://127.0.0.1:8500
export BUTTERFLY_NAMESPACE=kongken/prod
# 仅在需要 Consul API Namespace 时另外设置：
# export BUTTERFLY_CONFIG_CONSUL_NAMESPACE=team-a
PORT=18082 ./bin/tripmap
```

BFF 设置相同环境变量，在其 `kongken/prod/bff` key 写入 BFF 配置后，于另一个终端启动 `PORT=18081 ./bin/bff`。示例假定已运行 `make build`；端口与监听仍遵循各服务配置。证书、公私钥等文件路径仍须在对应进程或容器内可读，Consul 不负责分发这些文件。

现有 Docker 镜像和 `deploy/compose.stack.example.yaml` 默认 `BUTTERFLY_CONFIG_TYPE=file`；切换时必须覆盖为 `consul`，并提供地址和所需路径前缀。Consul 配置在启动时读取，当前没有热更新监听；修改后需重启对应服务。`storage-init` 是独立工具，仍从本地 YAML 读取配置，不随服务的 Consul 模式切换。

### 业务配置与启动检查

- `db_store` 指向 `store.db` 的命名连接，必须为 PostgreSQL。
- `redis_store` 指向 `store.redis` 的命名连接。
- `storage.s3_store` 指向 `store.s3` 的命名客户端，必须配置 bucket。
- `storage.key_prefix` 设置对象 key 前缀，示例为 `tripmap`。
- `auth.client_ip_header` 用于分享/退订限流；生产在 BFF 之后设为 `X-Real-IP`。账号会话/OAuth/CSRF 在 `config.bff.yaml`（[BFF 配置](bff.md#配置)）。
- **OAuth（#32 已迁至 BFF）**：仅在 `config.bff.yaml` 的 `auth.oauth_providers.logto` 配置 `issuer_url`、`client_id`、`client_secret`、固定 `redirect_url`、可选 `scopes` 与 `display_name`。tripmap `config.tripmap.yaml` 不配置浏览器身份提供方。
- `grpc.listen` 必填，`grpc.transport` 为 `tls`（默认）或 `h2c`：tripmap 自管的原生 gRPC 监听（业务 RPC 只在这里提供，BFF 调用）。`tls` 要求 `grpc.cert_file`、`grpc.key_file`；`h2c`（明文 HTTP/2，仅限经批准的可信内网）不加载证书，仍配置 cert/key 则启动失败；未知值启动失败，也不会按证书缺失推测为 h2c。`grpc.max_message_bytes` 默认 20 MiB。`internal_auth`（下一条）在两种模式下都必填，风险与切换见 [BFF 内部 gRPC 与凭证](bff.md#内部-grpc-与凭证)。
- `internal_auth.issuer`、`audience`、`trusted_keys[]` 必填；每项需要 `kid`，以及 `public_key`（内联 PKIX Ed25519 PEM）或 `public_key_file` **二选一**。缺失、同时设置、格式错误或非 Ed25519 均启动失败，不会回退另一来源。可选 `clock_skew`（默认 10 秒）：只接受这些 BFF 公钥签名的内部凭证，规则见 [内部 gRPC 与凭证](bff.md#内部-grpc-与凭证)。本地可用 `make dev-secrets` 生成文件或[在配置内填写 PEM](bff.md#配置内联签名密钥)。
- `mapbox_tiles.enabled` 开启 Mapbox 瓦片代理（默认关闭），`token`、`username`、`style_id` 等见下文“Mapbox 瓦片代理（可选）”。
- `share.public_base_url` 开启公开分享（为空即关闭）；`share.app_index_url`、`share.html_csp` 与限流项见下文 F6 一节。
- `auto_migrate: true` 在启动时通过 Ent 创建或补充表结构。本地示例开启；生产环境应设为 `false`，改由下文“版本化数据库迁移”中评审过的 SQL 文件升级，再启动新版本后端。

启动时检查 PostgreSQL、Redis 与 S3 bucket，缺失或不可连接则启动失败。后端不会在启动时自动创建 bucket；`make storage-init` 是独立显式操作，bucket 已存在且可访问时直接成功，不会配置公共访问策略。

S3 兼容服务可设置 endpoint 和 `use_path_style: true`；AWS S3 可省略 endpoint 并按供应方配置 region 和访问凭据。该 Butterfly 版本使用配置文件中的静态凭据。PostgreSQL 密码在框架内直接拼接 DSN，需避免 `@ : / ? # %` 等未转义字符。

## 已实现接口

下表是 tripmap 自身接口。BFF `/readyz` 含 `bff_postgres`、`bff_redis`、`tripmap_grpc` 等，见 [BFF 健康检查](bff.md#健康检查)与 [部署与发布](deployment.md#健康检查)。

| 接口 | 行为 |
| --- | --- |
| `GET /ping` | 存活检查，返回 `{"message":"pong"}` |
| `GET /readyz` | 检查 PostgreSQL、Redis 和 S3 bucket；全部可用返回 200，否则 503 |
| gRPC `tripmap.v1.SystemService/GetStatus` | 仅在内部 gRPC 端口（TLS / h2c），匿名方法但仍需 BFF 凭证；返回 `ready`、各依赖的 `name` / `ready`、`footprint_schema`、`share_enabled`、`region_version` |
| `POST /api/bff.tripmap.v1.StatusService/GetStatus`（BFF） | 浏览器的匿名状态查询，内容与上一行相同，由 BFF 经内部 gRPC 获取 |

就绪检查对每个依赖设置 2 秒超时并发执行，只返回状态，不返回原始驱动错误、连接字符串或凭据。RPC 请求成功时 HTTP 状态表示 RPC 调用结果，依赖可用性应读取响应中的 `ready`；运维探针使用 `/readyz`。tripmap 原来的 Connect 版 `tripmap.v1.SystemService` 已移除，不保留旧过程名。

```bash
curl --fail http://127.0.0.1:18082/ping
curl --fail http://127.0.0.1:18082/readyz
# 经 BFF（先 make dev-secrets，BFF 与 tripmap 都已启动）
curl --fail http://127.0.0.1:18081/api/bff.tripmap.v1.StatusService/GetStatus \
  -H 'Content-Type: application/json' \
  -H 'Connect-Protocol-Version: 1' \
  -d '{}'
```

状态接口无用户数据，无需登录。前端 `getBackendStatus()` 使用独立的匿名客户端（不带 Authorization 与 Cookie）：同步前的能力检查（`footprint_schema`、`region_version`）、账号面板的分享入口和公开的 `/status` 页面都使用它；没有配置 `VITE_API_BASE_URL` 时前端根本不会发请求。

### 账号与会话（浏览器：`bff.v1.AuthService`）

浏览器通过 BFF 使用 `bff.v1.AuthService`（Cookie `kongken_sid`、CSRF）。Logto 是唯一身份来源：账号、密码、注册、找回密码、社交连接和 Passkey 均在 Logto 管理；BFF 只按 `(issuer, sub)` 建立本地身份映射，缓存展示名/头像/登录名，并签发自己的 HttpOnly Cookie 会话。`Register`、`Login` 和旧 SPA OAuth RPC 保留 deprecated 契约但返回 `unimplemented`，不会写入密码或接受应用内密码。

- BFF 会话令牌使用 `bf_` 前缀，库内只存 SHA-256 哈希；Logto 令牌不会返回浏览器。首次 Logto 登录创建本地 `(issuer, sub)` 映射；每次登录调用 `ProfileService/EnsureProfile` 刷新业务档案（包括清空已移除的资料），失败不签发新 BFF 会话，可重新发起登录。删除待完成的映射不重新创建业务资料，只允许继续清理。
- PKCE：`code_verifier` 随 state 存入 Redis（同样 10 分钟、单次消费），只在后端换取令牌时使用，不返回给浏览器；换取失败后 state 已被消耗，需重新发起登录。Logto 令牌端点使用 `client_secret_basic` 认证，用户信息取自 `<issuer_url>/me`，不校验 ID Token；身份键只取受信 Logto 用户信息端点的 `sub` 与配置的 issuer，不使用邮箱或用户名关联。
- BFF 用独立 HttpOnly Cookie 将每次授权 state 绑定到发起浏览器；回调先消费 Redis state，再校验绑定与固定返回地址。前端仅顶层导航到 BFF，不保存 state、不处理授权码。升级前尚未完成且缺少浏览器绑定的旧尝试会被拒绝，需重新登录。
- 限流（Redis 固定窗口）：Logto OAuth 按客户端地址每 15 分钟最多 60 次；单条足迹写入每用户 600 次/分钟；批量同步按项一次性扣减，每用户每分钟共 6000 项（一个满批 2000 项，一次迁移可连续发送数批）；已有回执的批量重放不消耗额度。超限返回 `resource_exhausted`，Redis 不可用时返回 `unavailable`（不放行）。
- Logto 的注册、找回密码、邮箱验证和社交连接由 Logto 管理；本项目不保存密码，也不提供应用内密码找回。

### 足迹（`tripmap.v1.FootprintService`）

所有方法都要求登录，用户 ID 只取自会话，请求中没有用户字段。

| 方法 | 行为 |
| --- | --- |
| `GetFootprints` | 在同一事务快照中返回全部城市足迹、旅行、省份标记、`revision`、`schema_version` 与 `region_version`（城市按区域 ID、省份按省份 ID、旅行按 ID 排序）。城市的 `date` / `note` 为空；版本 1 账号读取时为每个城市合成一次 `legacy-<区域 ID>` 旅行，不修改数据 |
| `UpsertVisit` / `DeleteVisit` | 旧接口：保存或取消一个城市足迹；`UpsertVisit` 等于“城市加 `legacy-<区域 ID>` 旅行”，`DeleteVisit` 连同旅行一起删除 |
| `UpsertProvinceMark` / `DeleteProvinceMark` | 保存或取消独立省份标记；与城市足迹互不影响 |

- 服务端用嵌入的区域索引校验：区域 ID 存在，名称、所属省份与索引一致，颜色是五种之一，旅行日期为空或真实日历日期，备注不超过 1000 个 UTF-16 码元，旅行 ID 匹配 `^[A-Za-z0-9_-]{1,64}$`。校验失败返回 `invalid_argument`。
- 每次写入在持有用户行锁的事务中执行，同一用户的写入串行；只有实际改变数据才使 `revision` +1。内容相同的重复保存、删除不存在的记录都成功且不增加 revision，因此重试幂等，响应中的 `changed` / `deleted` 表示是否发生变化。
- `base_revision` 保持旧客户端兼容：未设置 `require_revision` 时，0 表示不检查，非 0 表示检查。新增 `require_revision: true` 会检查任何 base_revision（包含 0）。检查版本与当前 revision 不同、且写入会改变数据时返回 `aborted`，附带 `RevisionConflict.current_revision`；已生效请求的同内容重放仍幂等成功。新前端读取完整快照后逐记录精确版本写入，版本变化时重新三方合并，同记录冲突需要用户选择。部署须先更新支持 `require_revision` 的后端，再部署新前端（旧后端忽略该字段，无法保护初始 revision=0）。增量变更日志、删除 tombstone 和批量接口见下文 F1 新接口。
- 区域索引的后端副本是 `internal/tripmap/regions/regions.json`，必须与 `packages/geo-data/data/regions.json` 一致；`npm run download:maps` 会同时写入两份，Go 测试会比对。金门 `tw-09020`、连江县（马祖）`tw-09007` 归福建 `350000`，ID 和名称保持不变。
- 这两项旧云端记录若仍存为台湾 `710000`，快照和写入响应会返回规范化的福建归属，不修改原行、不提升 revision；旧客户端提交台湾归属时也先规范化再严格校验。与既有记录相同的规范化重放仍幂等，实际修改日期、颜色或备注后才按原有用户锁与 revision 机制写回福建归属。其他错误归属仍拒绝；没有数据库 schema 变化；F10 另提供用户确认的批量区域迁移。独立台湾省份标记保留。
- 此归属修正需先部署后端，再部署前端，以免新前端向尚未更新区域索引的旧后端提交福建记录被拒绝。

### 快照（`bff.tripmap.v1.SnapshotService` → `tripmap.v1.SnapshotService`，F3）

浏览器经 BFF Connect（Cookie + CSRF；读/list 带 schema/区域头）调用 `bff.tripmap.v1.SnapshotService`（`internal/bff/tripmap/snapshot/` → tripmap gRPC）。所有方法都要求登录，只访问当前用户自己的快照；快照内容是 `SnapshotData`（城市足迹和省份标记），保存为 gzip 压缩的 Protobuf，不可修改。tripmap HTTP 不再挂载 `SnapshotService` Connect。

| 方法 | 行为 |
| --- | --- |
| `ListSnapshots` | 按时间倒序返回元数据（ID、来源 `reason`、时间、城市、旅行与省份标记数量、快照时的 `revision`、`schema_version`），不读取内容 |
| `CreateSnapshot` | 在持有用户行锁的事务中保存当前足迹。客户端只能请求 `manual` 或 `before_migration`，其他值返回 `invalid_argument`；该 `revision` 已有快照时返回已有的（`created: false`），不重复保存。每用户每小时最多 30 次 |
| `GetSnapshot` | 返回元数据与全部记录（含旅行），供预览与比较；他人或不存在的 ID 返回 `not_found`。升级前保存的版本 1 快照在读取时转为版本 2（城市的日期与备注成为 `legacy-<区域 ID>` 旅行），已存储的载荷不被改写 |
| `RestoreSnapshot` | 参数 `snapshot_id`、`base_revision`、`mutation_id`。同一事务内：校验 `base_revision` 等于当前 revision（含 0，不同则 `aborted` 并附 `RevisionConflict`），保存 `before_restore` 快照，按最小差异（先删除、再新增或改写）把足迹改成与快照一致（先删旅行与城市，再新增或改写城市，最后新增或改写旅行；旅行换了所属城市时先删后建），每条变更照常写入变更日志并推进 revision。每用户每分钟最多 6 次 |

- 恢复的变更与普通编辑一样进入 `footprint_changes`，其他设备通过 `GetChanges` 增量同步；全部成功才提交，失败不留下部分结果，也不留下 `before_restore` 快照。
- `mutation_id` 与请求内容（快照 ID 和 `base_revision`）的哈希存入 `sync_receipts`（同一张表，`snapshot_id` 记录撤销用快照）：同 ID 同内容的重试不再次执行，返回原 revision、变更数和撤销快照（`replayed: true`；该快照已被保留规则清理时为空）；同 ID 不同内容返回 `already_exists`。回执检查先于 revision 检查，因此丢失响应后的重试不会误报冲突。
- 快照载荷在预览/恢复时按 F10 历史元组映射临时投影，旧载荷不改写；未知 `region_version`、未映射记录或目标 ID 碰撞返回 `failed_precondition`，云端不变。schema 1 的旅行 ID 按原区域 ID 生成，再映射城市归属。
- `reason` 取值：`manual`、`scheduled`、`before_migration`、`before_restore`。Protobuf 中用字符串而非 enum，是因为前端 `erasableSyntaxOnly` 禁止生成的 TypeScript enum。

**保留与定时（默认值，常量在 `internal/tripmap/application/snapshot_service.go`）。** 保留 30 天、始终保留最新 10 个、每账号最多 50 个，每次保存快照时对该账号执行，恢复期间不会清理正在恢复的快照；快照间隔 24 小时。`app.go` 的每小时维护任务在 Redis 锁 `tripmap:snapshot-maintenance`（50 分钟）保护下调用 `RunSnapshotMaintenance`：为“有足迹、自最新快照后有改动、且 24 小时内没有任何快照”的账号保存 `scheduled` 快照（在用户行锁内再次判断，多实例或重复运行不会产生重复），并对不再变化账号的过期快照执行保留规则。删除云端数据通过外键级联删除全部快照。

登录需要浏览器顶层导航到 `/auth/login?app=trip-map`，回调由 BFF 完成。空 Cookie 罐不能直接调用受保护接口；命令行测试须使用已登录的 BFF Cookie，并为写操作获取 CSRF token。不要向旧 Register/Login RPC 提交密码。

## F6 公开分享（`bff.tripmap.v1.ShareService` / `tripmap.v1.ShareService` gRPC）

用户可见规则见 [功能描述](trip-map/features.md#公开分享页与预览图f6)，行为细节、部署与限制见 [公开分享页与预览图](trip-map/sharing.md)。

**所有者接口（需要登录，账号只取自会话）。**

| 方法 | 行为 |
| --- | --- |
| `GetShare` | 返回已保存的设置、版本、完整链接（仅已发布时）、发布时的足迹 revision 与当前 revision；`enabled=false` 表示服务端未配置分享 |
| `PreviewShare` | 为候选设置构造将公开的内容并原样返回（`content_json`），同时返回当前足迹 revision 和照片句柄映射，不写任何数据；每用户每分钟 30 次 |
| `SaveShare` | 以 `expected_version` 条件保存。`mutation_id` 与**完整请求哈希**（期望版本、预览 revision、设置、选中照片、是否刷新）绑定，复用 `sync_receipts` 作为幂等回执：相同请求重放原结果，同 ID 不同内容返回 `already_exists`，过期请求不会因设置相同而被当作重放。新增内容（首次发布、新增字段或照片、`refresh_content`）在用户行锁内读取当前足迹，要求 `preview_revision` 等于当前 revision，否则 `aborted`（带 revision 冲突细节）；只收回字段时不读取足迹。重新发布生成新令牌，旧令牌从不复用。每用户每分钟 20 次写入 |
| `RevokeShare` | 无条件、立即删除令牌和快照，保留设置草稿；以 `mutation_id` 幂等 |

保存在单个事务内取得用户行的最小更新锁，与足迹同步、云端数据删除串行。快照 gzip 后上限 4 MiB，超过返回 `failed_precondition`（不截断）。`shares` 的变化不影响 OAuth、会话、快照或云同步。

**公开路由（无浏览器会话，分享令牌为凭证）：** `GET|HEAD /s/<token>`、`/s/<token>/og.png`、`/api/share/<token>/data`、`/api/share/<token>/photos/<句柄>`（见 [公开接口](trip-map/sharing.md#公开接口)）由 **BFF** 终止 HTTP 并调用 `tripmap.v1.SharePublicService`；领域逻辑在 `internal/tripmap/application/share_public.go`。tripmap HTTP 不再挂载 `/s/` 与 `/api/share/`（#36）。

**配置。**

| 字段 | 含义 |
| --- | --- |
| `share.public_base_url` | 站点地址，分享链接 = 它 + `/s/<令牌>`；为空则分享关闭，`GetStatus.share_enabled=false` |
| `share.app_index_url` | 受信任的前端 `index.html` 地址，用于注入 OG 标签；可空（使用最小页面）；不得包含 `/s/`、`/api/`，不受用户输入控制 |
| `share.html_csp` | 覆盖分享 HTML 的 CSP，默认严格同源；Vite 开发服务器需要放宽 |
| `share.page_rate_per_minute` / `image_…` / `miss_…` / `photo_…` | 每客户端地址每分钟限额，默认 120 / 30 / 30 / 300 |

**部署顺序与反向代理。** 先应用 `migrations/tripmap/20261014022913_f6_shares.sql`，再部署后端，最后部署前端。代理（BFF 与前端 Nginx）必须转发 `/s/` 与 `/api/share/` 到后端，且这些路径不写访问日志（令牌）；位于代理之后设置 `auth.client_ip_header`。镜像内的 Nginx 配置已处理；Vite 开发服务器代理 `/s/`。后端镜像构建需要 `internal/` 下的预览资源、`packages/fixtures` 和 `packages/geo-data/data` 中的区域索引与城市坐标；前端镜像构建需要 `internal/tripmap/share/assets/geometry.json`（`check:maps` 校验）。后端 `CGO_ENABLED=0` 构建通过。

**生成。** 修改 `proto/` 后 `make buf`（同时生成 Connect、grpc-go 与 TypeScript 代码），修改 `internal/tripmap/ent/schema/` 后 `make ent`，迁移文件用 `make migration NAME=… DEV_DSN=…` 生成；生成文件不手改。

## F7 共同旅行与好友

**公网（#37–#38）：** 浏览器经 BFF `bff.tripmap.v1.FriendService` 调用好友 RPC、`bff.tripmap.v1.SharedTripService` 调用共同旅行与共享媒体 RPC（`internal/bff/tripmap/friends/`、`internal/bff/tripmap/sharedtrip/` → tripmap gRPC `CollaborationService`）。**内网：** 完整 `CollaborationService` 仅在 tripmap gRPC 注册（`CollaborationGRPC`）；tripmap HTTP 不再挂载 `CollaborationService` Connect。

所有方法均要求登录；tripmap 仅从 BFF 签发的内部凭证取得用户 ID（`rpcauth.UserIDFrom`）。浏览器经 BFF Cookie 会话调用 `bff.tripmap.v1.FriendService` / `bff.tripmap.v1.SharedTripService`。用户可见规则与验收场景见 [功能描述](trip-map/features.md#共同旅行与好友f7)。

| 方法 | 行为 |
| --- | --- |
| `GetCollaborationOverview` | 返回当前用户的唯一好友邀请码（`fc_` 前缀，首次调用自动生成并持久化）、好友可见范围设置、全部好友关系（`accepted` 与 `pending`，带 `incoming`/`outgoing` 方向）以及相关的共同旅行列表 |
| `UpdateFriendSettings` | 更新对已确认好友公开的范围（`show_cities`、`show_dates`、`show_notes`；当 `show_cities=false` 时服务端强制将 `show_dates` 和 `show_notes` 置为 `false`） |
| `SendFriendRequest` / `RespondFriendRequest` / `RemoveFriend` | 通过对方用户名或 `fc_` 邀请码发起好友申请；接受或拒绝/撤回申请；解除好友关系（同一事务内清理双方未接受的共同旅行邀请，并将双方在同一共同旅行中的成员关联解绑，各自已有旅行记录保留为独立个人足迹） |
| `GetFriendFootprints` | 仅限双方处于 `accepted` 好友状态时调用。按好友的 `friend_show_*` 设置过滤并返回好友公开的省份、统计与可选城市/日期/备注，同时计算双方省份对比（`common_provinces`、`only_me_provinces`、`only_friend_provinces`）；仅当双方均开启 `friend_show_cities` 时返回 `common_cities` 与各自独占城市数 |
| `InviteToSharedTrip` / `RespondSharedTripInvite` | 创建者选择自己已存在的 `(region_code, trip_id)` 邀请已确认好友加入（角色 `editor` 或 `viewer`）。受邀者接受时，在按用户 ID 升序加行锁的单个事务内：若受邀者尚在 `schema_version=1` 则先原位升级到 `2`，若未打卡该城市则自动创建 `city_visits` 行，写入相同 `trip_id` 的 `city_trips` 行与 `footprint_changes` 变更日志并递增 `revision` |
| `UpdateSharedTrip` | `owner` 或 `active` 的 `editor` 修改共同旅行的日期或备注，在多用户行锁事务内同步更新所有 `active` 成员的 `city_trips`、变更日志与 `revision`；通过 `FootprintService.SyncChanges` 提交 `upsert_trip` 时同样自动触发跨成员同步，而 `viewer` 的修改或删除被拒绝（`permission_denied`） |
| `SetSharedTripMemberRole` / `RemoveSharedTripMember` / `LeaveSharedTrip` / `DeleteSharedTrip` | 创建者切换成员角色（`editor` / `viewer`）、移除成员、成员主动退出或创建者解散共同旅行。成员退出或被移除时，仅删除该成员在该城市的这一次 `city_trips` 子记录（写入 `delete_trip` 变更日志并推进 `revision`），保留该成员的 `city_visits` 打卡状态、其他旅行及个人媒体，不影响创建者或其他成员；创建者解散共同旅行时删除 `shared_trips` 与 `trip_members` 协作关系，所有已加入成员的 `city_trips` 保留为各自独立个人记录 |
| `ListSharedTripMedia` / `ReadSharedTripMedia` | 仅限该次共同旅行的 `active` 成员调用，列出或读取所有成员关联到该 `(region_code, trip_id)` 且状态为 `ready` 的照片与缩略图（标注上传者）；`MediaService.PrepareUpload` 同样允许共同旅行的 `owner` 与 `editor` 上传照片并拦截 `viewer`。成员退出、被移除或解除好友后立即失去访问他人上传照片的权限 |

- **并发锁序与隔离：** 涉及多个用户的写操作（接受邀请、共同编辑、移除成员、退出、解除好友）在同一数据库事务内按 `user.ID` 字典序升序执行 `SELECT … FOR UPDATE`（`lockUsers`），避免并发死锁，并与单用户的 `SyncChanges`、快照恢复、云端数据删除严格串行。
- **限流：** 协作写操作共用 Redis 固定窗口限流（每用户每分钟 60 次）。
- **好友定位：** 先匹配唯一好友邀请码，再按用户名查找；Logto 用户名仅为缓存，重名时返回 `invalid_argument` 并提示使用邀请码，不任意选取用户。
- **云端数据删除联动：** 内部 `AccountCleanupService` 第一阶段业务事务立即删除该用户的全部 `friendships`、`trip_members` 及创建的 `shared_trips`，其他成员各自的 `city_trips` 足迹记录不受影响。

## F8 统计、年度报告与成就（`bff.tripmap.v1.ReportService` → `tripmap.v1.ReportService`）

所有方法均要求登录，用户身份取自会话。用户可见口径、成就规则与验收场景见 [功能描述](trip-map/features.md#足迹统计年度报告与成就f8)。

| 方法 | 行为 |
| --- | --- |
| `GetReport` | 接收可选 IANA `timezone`（默认 `Asia/Shanghai`，无效时回退 `Asia/Shanghai`）与可选 `year`。在 `RepeatableRead` 只读事务快照中读取当前用户的 `revision`、城市足迹、旅行记录与省份独立标记，计算统计总览（`overview`）、全部年度报告（`annual_reports`）与 8 项旅行成就（`achievements`），每项指标与成就均附带可追溯的记录引用（`ReportRecordRef`） |

- **Redis 缓存与实时版本隔离：** 汇总结果以 Protobuf 编码缓存在 Redis 键 `<prefix>report:v2:<user_id>|<revision>|<timezone>|<local_date>|<region_version>` 中（TTL 24 小时）。由于缓存键绑定了用户当前的足迹 `revision`、区域版本与该时区下的本地日历日期 `local_date`，任何足迹或旅行的增删改推进 `revision` 后，下一次请求立即计算新版本并写入新键，不会返回过期统计。
- **定时汇总维护：** `internal/tripmap/app/app.go` 的每小时后台维护循环在 Redis 锁 `tripmap:report-maintenance`（50 分钟）保护下调用 `RunReportMaintenance`，为近期活跃且当前 `revision` 尚未缓存的账号预热默认时区（`Asia/Shanghai`）的汇总缓存。F8 无需新增 PostgreSQL 表。

## F9 私有备注搜索（`bff.tripmap.v1.SearchService` → `tripmap.v1.SearchService`）

`SearchNotes` 要求会话，身份只取当前用户，不能提交其他用户 ID；返回 `Cache-Control: no-store`。请求：`query`（≤100 UTF-16 码元、≤8 个空白分隔词）、可选 `province_code`、`limit`（默认 20，最大 50）、`offset`（最大 100000）及可选 `expected_revision`（offset > 0 时必填）。响应：城市/省份 ID、旅行 ID、日期、纯文本完整备注、`revision`、`next_offset`、`has_more`；最多多读 1 条判断下一页，不查询总数。

- 在 3 秒超时的 PostgreSQL `RepeatableRead` 只读事务中读取用户 schema/revision 与结果；分页 revision 不同返回 `aborted`，前端回退本机结果并允许重试。账号停用、令牌撤销/过期由认证拦截器拒绝，事务内再次检查用户停用状态。
- Ent 参数化、转义的 `NoteContainsFold` 在整条备注上执行所有词的 AND 匹配（中文片段、英文不区分大小写），`%`、`_`、引号按字面匹配，不接受查询表达式。这不是中文分词、相关度排序或语义搜索。
- schema 1 查询 `city_visits.note` 并返回 `legacy-<区域 ID>`；schema 2 仅查询 `city_trips.note`，限定旅行和城市都属于当前用户，不搜索冻结旧列。省份筛选按当前区域索引的城市 ID，兼容金门/马祖旧台湾归属；返回规范化名称与省份 ID。
- 每用户每分钟最多 60 次查询（Redis，不可用返回 `unavailable`，不放行）；处理器请求读取上限 4 KiB。结果不放 Redis、不创建备注副本或新增表/扩展，不需要数据库迁移。
- 当前利用已有用户索引缩小范围后执行字面包含扫描，无专用全文/GIN 索引；超大账号或深分页可能超时，需要后续测量后再决定专用索引/分词方案。前端失败时明确显示本机缓存结果，不把错误当成空结果。

区域拼音搜索由 `packages/geo-data/data/search-index.json` 在前端离线完成，不调用后端。部署新增 RPC 时先升级后端再部署前端；旧后端没有接口时备注搜索会提示并回退本机缓存。

## F10 区域数据服务（`bff.tripmap.v1.RegionService` → `tripmap.v1.RegionService`）

浏览器经 BFF 调用 `proto/bff/tripmap/v1/region.proto`；BFF 适配 tripmap 内部 gRPC（`internal/bff/tripmap/region/` → `internal/tripmap/application/region_grpc.go`）。业务契约仍为 `proto/tripmap/v1/regions.proto`，实现在 `internal/tripmap/application/region_service.go`。所有方法须认证（BFF Cookie + 内部凭证），只迁移当前会话用户的数据，区域索引本身不含私有足迹。使用与足迹校验同一 `internal/tripmap/regions.Index`，版本和映射由前后端共用生成流程维护，详见 [数据与地图说明](trip-map/data-and-maps.md#区域索引版本与维护f10)。不新增表、列或数据库迁移。

| 方法 | 行为 |
| --- | --- |
| `GetRegionIndex` | `known_version` 与当前一致时返回 `not_modified: true`，不重复传 `index_json`/`mappings`；否则返回完整公开 ID/名称索引、精确历史元组映射及支持版本；不包含 GeoJSON |
| `PreviewRegionUpgrade` | 3 秒超时、RepeatableRead 只读事务；返回当前 `version`、用户 `revision`、城市或 orphan-media 变化与 `blockers`；每用户 60 次/分钟；`Cache-Control: no-store` |
| `ApplyRegionUpgrade` | 必须提交当前 `version`、预览 `base_revision` 与唯一 `mutation_id`；每用户每分钟 6 个新尝试，既有回执不耗额度。用户行锁内重新计划、保存 `before_migration` 快照（同 revision 已有则复用），原位迁移并写日志和回执，同一事务提交 |

- 城市 ID 改号保留城市数据库 ID、颜色、用户内容及稳定旅行 ID；必要时先原位升级 schema 1，再原子替换不可普通编辑的旅行 region 字段，保留行 ID 和创建/更新时间。迁移媒体关联而不动 S3 key，包括已取消打卡的 orphan-media。媒体-only 迁移不增加足迹 revision。独立省份标记与已发布分享不更新。
- 同 ID 同请求返回原 revision/数量/快照 ID，跨 RPC 复用或不同内容返回 `already_exists`；回执先于当前版本/revision 检查。版本不一致/未映射/碰撞/共同旅行待协调返回 `failed_precondition`；预览 revision 变化返回 `aborted`；所有拒绝均不留下部分修改或迁移快照。
- `SystemService.GetStatus.region_version` 与 `GetFootprints` / `GetChanges.region_version` 广播当前摘要。前端统一发送 `X-Tripmap-Regions`，CORS 允许该头；新前端在同步前核对能力与版本。认证后区域相关 RPC 拒绝非空的不匹配头，Auth 和 Region 诊断/迁移不受头限制。缺失头继续按 legacy 客户端兼容，F5 的 schema 保护仍有效；`SyncChanges` 仅对已提交的精确回执绕过区域版本检查，新写入仍拒绝。
- 普通足迹写入、快照恢复和新媒体签发在退休/未映射城市 ID 尚未迁移时停止，避免在原旧行旁创建第二条当前城市。迁移表不猜测名称、不重用现存 ID，所有旧 ID 直接指向当前目标；拆分、多对一用户记录合并、省份 ID 改号与共同旅行跨账号 ID 改号未自动实现。真实 ID 改号与待发送旧批次并存时，应先导出缓存交由维护者处理；不会改写队列或悄悄丢弃未同步目标。

**部署。** 先部署带相同索引/映射摘要的后端，再部署前端。区域版本更新前归档历史版本并保留映射，重新生成 search/map/share 受影响资源及 Go/TS 契约，验证通过后发布；地图仍随页面静态分发，运行时不取第三方边界数据。不做启动时全库自动迁移或自动回滚行政数据。

## F11 行程服务（`bff.tripmap.v1.ItineraryService` → `tripmap.v1.ItineraryService`）

浏览器经 BFF 调用 `proto/bff/tripmap/v1/itinerary.proto`；BFF 适配 tripmap 内部 gRPC（`internal/bff/tripmap/itinerary/` → `internal/tripmap/application/itinerary_grpc.go`）。业务契约仍为 `proto/tripmap/v1/itinerary.proto`，实现在 `internal/tripmap/application/itinerary_service.go`，路程估算在 `internal/tripmap/itinerary`（内嵌 `coords.json`，由 `apps/trip-map/scripts/prepare-city-coords.mjs` 从 `apps/trip-map/public/maps` 生成并与 `packages/geo-data/data/city-coords.json` 逐字节一致，`npm run check:maps` 与 Go 测试都会核对）。所有方法须认证（BFF Cookie + 内部凭证），只读写当前用户的行程；不可见行程统一 `not_found`。

| 方法 | 行为 |
| --- | --- |
| `ListItineraries` | 返回自己的全部行程（含站点、路段、总计）和估算策略 `policy` |
| `GetItinerary` | 按 ID 读取，不存在或不属于自己返回 `not_found` |
| `SaveItinerary` | 创建或更新；新建时 `expected_version` 为 0；更新已有行程必须提交其当前版本，为 0 或不符返回 `aborted`；同 `mutation_id` 同内容返回原结果且 `replayed: true`，内容不同返回 `already_exists`；校验名称/备注/颜色、2–100 个站点、区域 ID（历史 ID 先按 F10 映射）、日期、备注、交通方式 |
| `DeleteItinerary` | 删除自己的行程；`expected_version` 大于 0 时须等于当前版本，否则 `aborted`；不存在（且未带版本）返回 `deleted: false` |

- 估算策略固定为 `offline_great_circle`、`external_api_enabled: false`、`free_builtin`、`redis_leg_24h_and_local_deterministic`。单段结果缓存在 Redis `<prefix>:itinerary:leg:v1:<from>:<to>:<mode>`（24 小时）；缓存不可用时直接计算，不影响结果。`from_cache` 仅表示整条行程的路段都来自缓存。
- 表 `itineraries`：所属用户（外键，级联删除）、`itinerary_id`（`user_id + itinerary_id` 唯一）、标题、备注、颜色、`version`、站点 JSON、创建/更新时间；迁移 `migrations/tripmap/20261014140617_f11_itineraries.sql`。
- 删除云端数据时删除行程；`RegionService` 迁移同时改写行程站点中的历史城市 ID（行程里出现未映射区域时作为阻止项）。其余部署与验证同上，先部署后端再部署前端。

## F12 提醒与通知（`bff.tripmap.v1.NotificationService` / `tripmap.v1.NotificationService`）

规则与验收见 [功能描述](trip-map/features.md#提醒与通知f12)。浏览器经 BFF `bff.tripmap.v1.NotificationService`（Connect + Cookie 会话）；BFF 适配 tripmap gRPC `NotificationService`（内部 Ed25519 凭证 + 用户 ID）。所有设置方法须认证，只读写当前会话用户的设置。契约：`proto/bff/tripmap/v1/notification.proto`、`proto/tripmap/v1/notification.proto`；实现在 `internal/bff/tripmap/notification/`（BFF 适配）、`internal/tripmap/application/notification_service.go`（业务 RPC）、`notification_maintenance.go`（发送）、`notification_public.go` / `notification_public_grpc.go`（退订领域逻辑），发送通道在 `internal/tripmap/notification`。tripmap HTTP 不再挂载 `NotificationService` Connect 或公开退订路由（#44 清理其余 Connect 代理）。

**配置**（`notification`，全部可选；示例见 `config.tripmap.example.yaml`，真实凭据放在被忽略的 `config.tripmap.yaml` 或挂载 Secret 中）：

| 字段 | 说明 |
| --- | --- |
| `public_base_url` | 站点对外地址；邮件链接和点击推送后打开的地址由它生成，不读取请求头。`/api/` 须能到达本后端（Vite 开发代理和前端镜像的 Nginx 已转发） |
| `secret` | 签名退订链接，至少 32 个字符；启用 SMTP 时必填。更换会使已发邮件里的退订链接失效 |
| `smtp.host/port/username/password/from/tls` | 发信服务器；`tls` 为 `starttls`（默认）、`tls`（隐式 TLS，常用 465）或 `none`（仅本地测试服务器） |
| `web_push.vapid_public_key/vapid_private_key/subject` | 浏览器推送；用 `make vapid-keys` 生成，`subject` 为 `mailto:` 或 `https:`；非 localhost 的站点须使用 HTTPS |

邮件渠道需要 `smtp.host`、`from`、`secret` 和 `public_base_url` 同时配置，推送渠道需要两把 VAPID 密钥；不完整的配置在启动校验时报错，未配置的渠道在接口里报告为不可用，开关无法开启。

| 方法 | 行为 |
| --- | --- |
| `GetNotificationSettings` | 返回每个类型/渠道的开关（缺失的行视为关闭）、时区、发送小时、是否显示地点、打码邮箱、待验证邮箱、已订阅设备数，以及 `email_available`、`push_available`、`vapid_public_key` |
| `UpdateNotificationSettings` | 更新时区（IANA 名称）、发送小时（0–23）、是否显示地点和所列开关；开启邮件需要服务器已配置且已有验证邮箱，开启推送需要服务器已配置，否则 `failed_precondition` |
| `RequestEmailVerification` | 向新地址发送 6 位验证码（SHA-256 哈希、绑定用户和地址，15 分钟有效）；每账号每小时 5 次、每地址每小时 3 次 |
| `ConfirmEmail` | 校验验证码，最多尝试 5 次；成功后地址才生效 |
| `RemoveEmail` | 删除地址并关闭所有邮件开关 |
| `RegisterPushSubscription` / `RemovePushSubscription` | 保存或删除当前浏览器的订阅；同一端点登记到另一账号时转移；每账号最多 10 个；端点和密钥只用于发送，不返回客户端 |
| `SendTestNotification` | 向一个渠道发送固定的通用测试消息（不含足迹数据）；推送全部失败时返回 `unavailable` |

写接口每用户每分钟 30 次，读接口 120 次；提供商返回的邮箱不会被存储（邮箱只来自用户输入并验证）。

**发送。** `internal/tripmap/app` 每小时在 Redis 锁 `tripmap:notification-maintenance` 下运行一次 `RunNotificationMaintenance`：按用户时区计算本地日期，到达发送小时后，为开启的类型/渠道在 `notification_deliveries` 里以唯一键（用户、类型、渠道、周期；周期为本地日期，年度为年份）先占位再发送，所以多实例或重复检查不会重复发送；失败最多尝试 3 次，状态为 `pending/sent/failed/skipped`，占位后进程崩溃的周期不再重发（最多一次）；发送记录保留 30 天后清理。回忆匹配规则：往年同月同日的旅行，非闰年的 2 月 28 日包含 2 月 29 日的旅行；旧版本 1 账号读取 `city_visits.travel_date`，版本 2 账号读取 `city_trips`；年度回顾在用户本地 1 月 1–7 日发送上一年的数量。

**邮件。** 标准库 `net/smtp`，UTF-8 quoted-printable，拒绝头注入；每封邮件带 `List-Unsubscribe` 与 `List-Unsubscribe-Post: List-Unsubscribe=One-Click`。退订链接 `GET/HEAD/POST /api/notifications/unsubscribe?t=<令牌>` 由 **BFF** 应答 HTML，经 gRPC 调用 `tripmap.v1.NotificationPublicService/ProcessEmailUnsubscribe`（匿名内部凭证 + 签名令牌）：令牌为 `userID|kind` 的 HMAC-SHA256（`kind` 为 `*` 表示全部邮件类型），无状态；GET/HEAD 只显示确认页，POST（含邮件客户端一键退订）才关闭对应开关；BFF 边缘与 tripmap 均按客户端 IP 限流，响应 `no-store`、`noindex`、严格 CSP，密钥未配置时不可用，访问日志与 Referer 不记录令牌。

**推送。** 使用 `github.com/SherClockHolmes/webpush-go`；发送前校验端点：必须是 https、域名（非 IP）、端口 443，拨号时再次检查解析出的 IP 不在内网/回环/链路本地等范围，不跟随重定向、不使用代理；返回 404/410 的订阅删除，连续失败 5 次的订阅删除。前端 Service Worker 在 `apps/trip-map/public/sw.js`，只显示服务端发来的标题和正文，点击只打开本站；前端镜像的 Nginx 对 `/sw.js` 设置 `no-cache`，并对 `/api/notifications/` 关闭访问日志、清除 Referer。

**数据与部署。** 新表 `notification_profiles`（每用户一行：时区、发送小时、是否显示地点、已验证邮箱、待验证邮箱及验证码哈希/过期时间/尝试次数）、`notification_settings`（`user_id + kind + channel` 唯一，缺失即关闭）、`push_subscriptions`（`endpoint_hash` 唯一）、`notification_deliveries`；均通过 `users` 外键级联删除，删除云端数据时一并清除。迁移 `migrations/tripmap/20261014165138_f12_notifications.sql` 为纯新增。**部署顺序：** 先执行该迁移，再升级后端，最后部署前端；未配置 `notification` 时不发送任何内容。

**验证与限制。** `internal/tripmap/notification` 的测试覆盖文案、令牌、SMTP（本地假服务器）、Web Push（本地 TLS 假推送服务和 SSRF 拒绝）；`internal/tripmap/application` 的集成测试（需要 PostgreSQL 与 Redis）覆盖设置、邮箱验证、订阅转移、发送时区与幂等、重试、退订、用户隔离和云端数据清理；前端有单元测试与桌面/手机 Playwright 验收。没有对真实 SMTP 服务商和真实浏览器推送服务做验证；投递语义为最多一次，没有送达回执；每小时一次的检查使实际发送可能晚最多约 1 小时。

## Mapbox 瓦片代理（可选）

规则与验收见 [功能描述](trip-map/features.md#全国与省内地图)，前端启用方式与署名见 [数据与地图说明](trip-map/data-and-maps.md#mapbox-代理底图可选)，部署见 [部署与发布](deployment.md#mapbox-底图可选)。实现在 `internal/tripmap/tiles/`，由 `internal/tripmap/app/app.go` 挂载到 tripmap 的内部 HTTP 端口；BFF 在 `internal/bff/gateway/services.go` 登记 `/api/tiles/` 并透明转发（见 [BFF](bff.md#路由)），BFF 不持有 Mapbox 配置。不新增 proto、数据库表、迁移或足迹字段。

**配置**（`mapbox_tiles`，示例见 `config.tripmap.example.yaml`；真实 token 只放在被忽略的 `config.tripmap.yaml` 或挂载 Secret 中）：

| 字段 | 说明 |
| --- | --- |
| `enabled` | 默认 `false`。关闭时 `/api/tiles/…` 对任何请求返回不缓存的 404，不访问 Mapbox、不使用 Redis；未配置此段的部署行为与之前完全相同 |
| `token` | Mapbox 公共访问令牌（`pk.` 开头；`sk.` 密钥令牌会被拒绝）。只在服务端使用，不进入响应、日志、错误和追踪；更换后重启 tripmap 即生效，前端无需重建 |
| `username`、`style_id` | 样式所属账号与样式 ID，1–64 位字母、数字、`-`、`_`；启用时三者都必填 |
| `style_version` | 可选，1–32 位字母、数字、`-`、`_` 的样式版本标签（如 `2026-10`）。设置后浏览器可见的瓦片地址为 `/api/tiles/{z}/{x}/{y}.png?v=<标签>`，只接受与配置完全相同的值；它不发给 Mapbox、不是机密，作用见下文“缓存与样式切换” |
| `max_zoom` | 1–22，默认 18；超过的层级返回 400 |
| `rate_per_minute` | 每个客户端地址每分钟请求数，默认 600，最大 100000 |
| `upstream_timeout` | 单次 Mapbox 请求（含读取响应）的上限，默认 5 秒，最大 30 秒 |
| `max_response_bytes` | 单个瓦片大小上限，默认 512 KiB，范围 1 KiB–4 MiB |

启用但配置不合法（缺字段、token 格式错误、名称含特殊字符、数值越界）时 tripmap 启动校验失败；错误信息只写字段名，不含 token。上游地址固定为 `https://api.mapbox.com` 的 Static Tiles API（`/styles/v1/{username}/{style_id}/tiles/256/{z}/{x}/{y}.png`），配置里没有可改的上游，客户端也不能指定 token、样式或上游。

**token 与账号建议**：只授予 `styles:tiles` 读取范围，只接受 `pk.` 公共令牌（高权限 `sk.` secret token 启动校验即失败）；在 Mapbox 账号设置用量告警。若 token 配了 URL 限制，tripmap 的服务端请求不带 `Referer`，可能被 Mapbox 以 403 拒绝——应改用不带 URL 限制的最小权限 token，**不要**通过伪造来源头绕过。

**请求规则**（任何一项不满足都不会访问 Mapbox，且响应都带 `Cache-Control: no-store`）：

| 条件 | 响应 |
| --- | --- |
| 代理未启用 | 404 |
| 方法不是 `GET`（含 `HEAD`） | 405，`Allow: GET` |
| 路径不是规范的 `/api/tiles/{z}/{x}/{y}.png`（多余段、`//`、`..`、编码斜杠、结尾 `/`、扩展名不是小写 `.png`） | 404 |
| 坐标不是无符号十进制整数（含前导零、正负号、小数）、`z` 超过 `max_zoom`、`x` 或 `y` 不小于 2^z | 400 |
| 带查询参数，但不是“已配置 `style_version` 且恰为 `?v=<该值>`”（含 `access_token`、`style`、重复或大小写不同的 `v`、值不匹配、未配置版本时带 `v`、空 `?`） | 400 |
| 超过 `rate_per_minute`（计数键含客户端地址，复用 Redis 固定窗口限流，键前缀 `tiles:`） | 429，`Retry-After: 60` |
| 限流存储不可用或尚未连接 | 502（不放行，避免无限制地消耗 Mapbox 额度） |

客户端地址按 `auth.client_ip_header`（生产 `X-Real-IP`，由 BFF 覆盖写入）解析；未设置时使用 TCP 对端。限流只复用现有 Redis 固定窗口操作，没有新增 Redis 命令。

**上游访问**：只发送 `Accept`、`User-Agent` 与 `Accept-Encoding: identity`（条件请求时另加下文的 `If-None-Match` 或 `If-Modified-Since`），不转发客户端的 `Authorization`、`Cookie`、`Referer`、`Origin` 或内部身份声明；不跟随重定向（任何 3xx 视为无效响应）；请求使用独立的 HTTP 传输，不经过会记录 URL 的出站追踪。成功须是 HTTP 200、不超过 `max_response_bytes`、PNG 且为 256×256 像素；其他情况一律为脱敏的 502（正文固定为 `Bad Gateway`，不透传上游状态码或错误正文，日志只记录原因类别且不含 token 或带凭据的 URL），超时为 504，浏览器已断开时终止上游请求且不再写响应。上游返回 429、401、403、404、5xx 对浏览器都是 502。

**响应**：成功返回 `image/png`，并只保留 Mapbox 的 `Cache-Control`、`Expires`、`Last-Modified` 与按样式限定范围的 `ETag`（上游没给缓存头时用 `Cache-Control: no-cache`）；不透传 `Set-Cookie`、CORS 或其他上游头。没有服务端瓦片缓存、持久瓦片库或预取，也不延长 Mapbox 给出的新鲜期。

**缓存与样式切换**（2026-10 按 [Static Tiles API 文档](https://docs.mapbox.com/api/maps/static-tiles/) 核对：该接口返回 `Cache-Control: max-age=43200, s-maxage=43200`，卫星源 `s-maxage=604800`，样式自带的缓存头会覆盖默认值；样式改动最长约 12 小时才会在 Mapbox 侧生效）：

- **条件请求**：浏览器带 `If-None-Match` 时，只有本样式签发的 ETag 会去掉范围标记后下传给 Mapbox（最多 8 个，头部超过 2 KiB、格式错误或为 `*` 时不下传，按无条件请求返回完整图片）；没有 `If-None-Match` 时才下传合法 HTTP 日期的 `If-Modified-Since`。Mapbox 返回 304 时，tripmap 返回无正文的 304，带与 200 相同口径的 `Cache-Control`、`Expires`、`Last-Modified`、`ETag`；Mapbox 的 304 缺少 ETag 且浏览器只给了一个本样式 ETag 时沿用该值。Mapbox 在没有下传验证器时返回 304 视为无效响应（502）。条件请求同样先过方法、路径、坐标、查询和限流校验，校验失败不会访问 Mapbox。
- **缓存身份**：返回给浏览器的 ETag 形如 `"<指纹>-<Mapbox 的 ETag>"`，指纹是 `username`、`style_id`、`style_version` 的 SHA-256 前 5 字节（十六进制），不含 token。换账号、样式或版本后，旧样式的 ETag 不会被下传，也不会得到 304。缓存不按用户区分，不保存或转发用户凭据；响应不带 `Vary`。
- **样式切换流程**：同时修改 tripmap 的 `style_id`（或 `username`）与 `style_version`，并用相同的 `VITE_OSM_TILE_VERSION` 重新构建前端；新页面的瓦片地址随之改变，浏览器不会复用旧样式的新鲜缓存。`style_version` 只是版本标签，不能选择样式、账号或上游。token 轮换仍只改 tripmap 配置，不涉及版本或前端。
- **兼容旧前端**：配置了 `style_version` 后，没有 `?v=` 的旧地址仍可用，但响应固定为 `Cache-Control: no-cache`（不带 `Expires`），浏览器每次用 ETag 重新验证，切换样式时不会被旧缓存遮住；值不匹配的 `?v=` 返回 400。未配置 `style_version` 时行为与以前相同，不接受 `?v=`。
- **限制**：未配置 `style_version` 时切换 `style_id`/`username`，浏览器地址不变，已缓存的旧样式瓦片仍会在 Mapbox 给出的新鲜期内（Static Tiles 默认最长 12 小时）被直接复用，之后才会因 ETag 范围变化得到完整 200，因此切换样式前应先配置版本；只有 `If-Modified-Since` 而没有 ETag 的客户端无法发现同一地址上的样式切换，应使用版本化地址；无效输入、关闭的代理、限流和上游错误一律 `no-store`，不会带验证器，也不会以图片成功状态返回。

**验证**：`internal/tripmap/app/tiles_routes_test.go` 通过真实 tripmap 路由和本地模拟 Mapbox（替换 HTTP 客户端的网络）覆盖成功、关闭、无效输入、限流、超时、过大和无效响应、重定向、取消、凭据隔离与日志脱敏，以及验证器下传、304、内容变化后 200、跨样式隔离、版本化与旧地址、条件请求不绕过校验和错误 `no-store`；`internal/tripmap/config/config_test.go` 覆盖配置校验及 token 不出现在错误和打印里；BFF 转发（含验证器与 304）见 `internal/bff/app/tiles_test.go`。自动化测试不使用真实 token，也不产生 Mapbox 请求。使用真实 token 的验收在独立环境完成。

## 数据库模型

| 表 | 关键字段与约束 |
| --- | --- |
| `users` | BFF 身份映射 ID、`schema_version`（足迹结构，默认 1，F5 升级后为 2）、可空且可重复的登录名、展示名/头像缓存、`friend_code`（F7 好友邀请码）、`friend_show_cities/dates/notes`（F7 对好友公开范围）、足迹 `revision`、创建/更新时间；不保存认证凭据 |
| `auth_identities` | Logto `(issuer, sub)` 映射、登录名/展示名/头像缓存、禁用和云端数据删除状态；唯一键为 `(issuer, external_id)` |
| `auth_browser_sessions` | BFF 浏览器会话的唯一令牌哈希、创建/过期/最近使用时间、撤销标记；用户删除时级联 |
| `city_visits` | 所属用户、区域 ID/名称、所属省份、颜色、日期字符串、备注；`user_id + region_code` 唯一。F5 起日期与备注列是版本 1 的冻结副本，不再更新 |
| `city_trips` | F5：所属用户、所属城市行（外键，级联删除）、旅行 ID（`user_id + trip_id` 唯一）、区域 ID、日期字符串、备注、创建/更新时间 |
| `province_marks` | 所属用户、省份 ID、颜色；`user_id + province_code` 唯一 |
| `media_objects` | 所属用户、唯一 object key、内容类型、非负字节大小、pending/ready/deleting 状态、区域 ID、可空软引用 `trip_id` 等 F4 字段 |
| `shares` | F6：所属用户（`user_id` 唯一，级联删除）、可空唯一 `token`（43 位 base64url，明文存储）、`visibility`（private / link / public，默认 private）、`show_cities/dates/notes/photos`、选中照片 ID（JSON）、`version`、`published_revision`、`published_at`、gzip 的发布快照 `payload`（公开内容 + 服务端独有的句柄映射）；撤销后令牌与载荷为空，保留设置草稿 |
| `friendships` | F7：规范化排序的 `user_low_id` 与 `user_high_id`（外键级联删除，`(user_low_id, user_high_id)` 唯一）、发起方 `requester_id`、`status`（`pending` / `accepted`）、创建/更新时间 |
| `shared_trips` | F7：创建者 `owner_id`（外键级联删除）、`trip_id`（`(owner_id, trip_id)` 唯一）、`region_code`、创建/更新时间 |
| `trip_members` | F7：所属共同旅行 `shared_trip_id`（外键级联删除）、成员 `user_id`（外键级联删除，`(shared_trip_id, user_id)` 唯一）、`role`（`owner` / `editor` / `viewer`）、`status`（`invited` / `active`）、加入时间与创建/更新时间 |
| `notification_profiles` | F12：所属用户（唯一，级联删除）、时区、发送小时、`show_places`、已验证邮箱与验证时间、待验证邮箱、验证码哈希/过期时间/尝试次数 |
| `notification_settings` | F12：所属用户、`kind`（memory / annual_report）、`channel`（email / push）、`enabled`；`(user_id, kind, channel)` 唯一，缺失行视为关闭 |
| `push_subscriptions` | F12：所属用户（可转移）、`endpoint_hash`（唯一）、端点、`p256dh`、`auth`（敏感，不入日志）、失败次数 |
| `notification_deliveries` | F12：所属用户、`kind`、`channel`、`period`、`status`（pending / sent / failed / skipped）、尝试次数；`(user_id, kind, channel, period)` 唯一，`created_at` 索引，30 天后清理 |
| `snapshots` | 所属用户、`reason`（manual / scheduled / before_migration / before_restore）、快照时的 `revision`、城市、旅行（`trip_count`）与省份标记数量、`schema_version`（旧行为 1）、gzip 压缩的 `SnapshotData` 载荷、创建时间；所有字段不可修改；`user_id + created_at`、`user_id + revision` 索引；用户删除时级联 |

城市足迹与省份独立标记分表。删除城市不影响省份独立标记；用户归属为必填外键，删除用户时数据库级联删除这些元数据。云端数据删除服务先清理 S3 实体对象，再级联删除元数据；对象清理失败保留暂停的本地身份映射以便重试，Logto 账号不受影响。

时间戳默认 UTC。旅行日期仍是 `YYYY-MM-DD` 字符串，允许空值，不做时区转换。Ent 验证日期是否为有效日历日期（年份 `0000` 与前端一致地接受，`packages/fixtures/trip-dates.json` 同时驱动前端测试和 `TestTripDatesMatchTheSharedFrontendFixtures`），并限制颜色为前端五种色值、备注按与前端一致的 UTF-16 长度限制为最多 1000。区域有效性、名称与归属由 `FootprintService` 在写入前校验。

一用户一城市一条城市足迹，城市下有任意多次旅行（F5）。现有浏览器备份格式为 `version: 2`（可导入版本 1），本地数据迁移由前端以批量同步完成；服务端只有账号从版本 1 到版本 2 的原位升级，见下文。

## 版本化数据库迁移

`migrations/tripmap/` 保存按时间戳排序的、只增不删的 PostgreSQL SQL 文件和 `atlas.sum` 校验和（Atlas 目录格式）。当前有下列文件：

| 文件 | 内容 |
| --- | --- |
| `20261014011442_baseline.sql` | F5 之前（`main`）的完整结构，用于全新数据库；已有数据库不执行 |
| `20261014011446_f5_multiple_trips.sql` | F5 的纯新增变更：`media_objects.trip_id`（默认空串）及索引、`snapshots.schema_version`（默认 1）与 `trip_count`（默认 0）、`users.schema_version`（默认 1）、新表 `city_trips`（外键级联、`(user_id, trip_id)` 唯一索引）。不删除、不改写任何已有列或行 |
| `20261014022913_f6_shares.sql` | F6 的纯新增变更：新表 `shares`（`token` 唯一索引、`user_id` 唯一索引、外键级联删除） |
| `20261014050044_f7_collaboration.sql` | F7 的纯新增变更：`users` 新增 `friend_code`（唯一索引）与 `friend_show_cities/dates/notes`（带默认值），新增 `friendships`、`shared_trips`、`trip_members` 三张表及唯一索引与外键级联删除 |
| `20261014140617_f11_itineraries.sql` | F11 的纯新增变更：新表 `itineraries`（`(user_id, itinerary_id)` 唯一索引、`users` 外键级联删除，站点为 JSON 列）；不改动既有表 |
| `20261014165138_f12_notifications.sql` | F12 的纯新增变更：新表 `notification_profiles`、`notification_settings`、`push_subscriptions`、`notification_deliveries`（唯一索引与 `users` 外键级联删除）；不改动既有表 |
| `20261017180000_retire_tripmap_sessions.sql` | 删除 tripmap 侧旧 `auth_sessions` / `account_deletion_receipts`；认证遗留列由手动 SQL 清理 |

BFF 的 `20261023095053_logto_identity_mapping.sql` 新增 Logto 身份映射和浏览器会话表，不导入旧测试账号；旧 BFF Cookie 会话随之失效。旧 BFF 表 `auth_users` / `auth_sessions` 与 tripmap 用户表中的认证遗留列不会被版本化迁移自动删除。升级与各脚本的目标库、执行顺序见 [部署说明](deployment.md#logto-only-身份迁移)。tripmap 手动清理也移除旧用户名唯一索引：用户名只是可重复的资料缓存。脚本不会由服务启动自动执行。

**生成。** 修改 `internal/tripmap/ent/schema/` 并运行 `make ent` 后，用 `make migration NAME=<变更名> DEV_DSN='postgres://…'` 生成下一个文件（`cmd/migrate-gen`）：它在 `DEV_DSN` 指向的服务器上临时创建一个随机命名的空数据库，重放现有 `migrations/tripmap/` 得到“当前结构”，与 Ent schema 比较后写出差异和新的 `atlas.sum`，然后删除该数据库；因此所用账号需要 `CREATEDB`，不会碰服务器上的其他库。生成器只写新增和修改类语句（不写 `DROP COLUMN` / `DROP INDEX`，也不生成会丢数据的回滚脚本）。迁移文件只通过生成器更新，手改会使 `atlas.sum` 校验失败。

**升级顺序（已有生产数据库，由 `auto_migrate` 建表）。**

1. 备份数据库（F5 对账号的升级是单向的，回退旧后端需要这份备份）。
2. 把后端配置改为 `auto_migrate: false`。
3. 对数据库依次执行 baseline 之后的文件，每个文件一个事务：`psql "$DSN" -v ON_ERROR_STOP=1 -1 -f migrations/tripmap/20261014011446_f5_multiple_trips.sql`，再依次执行 `20261014022913_f6_shares.sql`、`20261014050044_f7_collaboration.sql`、`20261014140617_f11_itineraries.sql` 和 `20261014165138_f12_notifications.sql`（已升级到前一版的数据库只执行尚未应用的文件）。已有数据库的结构等同于 baseline，所以 baseline 本身不执行；若使用带版本记录的迁移工具，把 baseline 版本标记为已执行。语句不带 `IF NOT EXISTS`：重复执行会报错并回滚，不会静默重复。
4. 部署新后端；已有账号在第一次被新前端写入时才升级到结构 2（见上文），旧行保持原样。
5. 全新数据库：按文件名顺序执行全部文件（含 baseline）。

**验证。** `internal/tripmap/integration/migrations_test.go`（随 `make test-integration` 和 CI 的 `go test ./...` 运行，需要 `TRIPMAP_TEST_POSTGRES_DSN`）在随机 schema 里先应用 baseline 并插入旧用户、城市足迹、快照和媒体行，再按上面的方式应用后续文件：检查旧行数据不变且新列取默认值、`city_trips` 为空，并要求迁移后的库与 `Schema.Create` 建出的库对 Ent 报告的待执行差异完全一致（Ent 对两条部分唯一索引的谓词文本有恒定的重建提示，两边同样出现，因此按“完全相同”比较）；另一个测试校验 `atlas.sum` 与文件一致。CI 与实际 PostgreSQL 一起运行这些测试。

**限制。** 迁移流水线只覆盖 PostgreSQL 结构；部署时仍需人工执行并记录已执行的版本（没有内置的迁移执行器或版本表）；回滚依赖数据库备份。

## 生成、构建与验证

```bash
make ent            # 修改 Ent schema 后生成客户端
make buf            # 修改 proto 后生成 Go / TypeScript 契约
buf lint
make test           # Go 单元测试；未配置连接时跳过实际存储集成测试
make build          # bin/tripmap 与 bin/bff
npm test
npm run build
```

实际存储集成测试在开发依赖已启动且 bucket 已初始化后运行：

```bash
make test-integration
```

该命令使用 Compose 示例的本地地址和开发凭据。使用其他测试环境时，设置 `TRIPMAP_TEST_POSTGRES_DSN`、`TRIPMAP_TEST_REDIS_ADDR`、`TRIPMAP_TEST_REDIS_PASSWORD`、`TRIPMAP_TEST_S3_ENDPOINT`、`TRIPMAP_TEST_S3_ACCESS_KEY`、`TRIPMAP_TEST_S3_SECRET_KEY`、`TRIPMAP_TEST_S3_BUCKET`，运行 `go test -v ./internal/tripmap/integration`。

PostgreSQL 测试创建独立随机 schema 并清理，Redis 测试使用随机 key 和 TTL，S3 测试仅操作随机测试对象并删除。测试覆盖城市唯一约束、非法日期/颜色、省份标记独立性、用户级联、Redis 读写和 S3 上传下载删除，以及版本化迁移的升级校验（见上文“版本化数据库迁移”）。

`internal/e2e/` 提供 **`ChainHarness`**（`chain_harness_test.go`）：在设置 `TRIPMAP_TEST_POSTGRES_DSN` 与 `TRIPMAP_TEST_REDIS_ADDR` 时启动真实 BFF（独立 schema + Redis 前缀）→ 内部 gRPC（默认 TLS，可选 h2c）→ 真实 tripmap（独立 schema），经 httptest 暴露 BFF 公网 URL。`FreshBrowser()` 返回带 cookie jar 的 **`Browser`**，自动处理 CSRF（`IssueCSRFToken` + `X-CSRF-Token`）与足迹读所需的 `X-Tripmap-Schema` / `X-Tripmap-Regions`；登录辅助方法走真实 BFF `/auth/login` 与回调，只替换远端 Logto 交换；`Me` / `Logout` / 足迹读与 sync（`GetFootprints`、`GetChanges`、`SyncChanges`）走 Connect 公共客户端；`region_chain_test.go` 覆盖 `bff.tripmap.v1.RegionService` 金门迁移、阻止项与 CSRF；`sessions_chain_test.go` 覆盖 `ListSessions`/`RevokeSession`；`notification_test.go` 覆盖 `bff.tripmap.v1.NotificationService` 与 BFF 公开退订 HTTP；`snapshot_chain_test.go` 覆盖快照创建、去重、恢复、幂等重放与用户隔离；`media_chain_test.go` 覆盖 `bff.tripmap.v1.MediaService` 预留/确认/读/删与用户隔离（内存 blob，非真实 S3）；`report_chain_test.go` 覆盖 `bff.tripmap.v1.ReportService` / `SearchService` 与用户隔离；`shared_trip_chain_test.go` 覆盖 `bff.tripmap.v1.SharedTripService` 邀请/viewer 写入拒绝、editor 足迹 sync、共享媒体与解除好友撤销。后续 ticket 只追加场景测试，复用该 harness。运行：`go test ./internal/e2e/ -count=1`（与 `make test-integration` 同一套 PG/Redis 环境；无需 S3，默认 noop blob，媒体场景可注入 `chainMediaBlobs`）。

`internal/bff/auth`、`internal/bff/session` 与 `internal/bff/oauth` 的测试覆盖 Logto 登录回调、issuer 与 subject 身份隔离、资料刷新、Cookie 会话、CSRF、会话撤销、云端数据删除和旧 RPC 的停用响应。`internal/e2e/` 覆盖受控 Logto 交换后的真实 BFF HTTP 回调及业务读写；真实 Logto 租户验收仍需部署凭据。

[Docker 镜像与 Actions](docker-images.md) 说明前后端两个镜像的构建发布。`deploy/tripmap/Dockerfile` 仅构建 tripmap 后端镜像，运行时挂载 `/config/config.yaml`；容器中连接 Compose 依赖时，配置地址使用服务名和容器端口，而不是宿主机的 `127.0.0.1`。

## 下一阶段

从 [后端功能调研](trip-map/backend-research.md) 中选择尚未实现的业务切片继续。F0（本地读取失败保护）以及 F1 的登录、前端云端读写、持久化离线修改与冲突选择已实现；云端数据删除与增量/批量同步已补齐；真实 OAuth 端到端验证按用户要求留待部署后完成，不能把 F1 全范围标为交付。F2（本地足迹迁移上云）、F3（云端快照与恢复）、F4（照片与附件）、F5（同城多次旅行）、F6（公开分享页与预览图）与 F7（共同旅行与好友）已实现；F8（统计、年度报告与成就）和 F9（区域搜索增强、私有备注搜索）已实现；F10 区域版本与兼容迁移已实现，限制见对应章节；F11 行程与路线连线和 F12 提醒与通知已实现（F12 未经真实邮件与推送服务验证）。

## F1 新接口与部署

- **浏览器删除云端数据（#43，BFF）：** `bff.v1.AuthService/DeleteAccount` 请求 `confirmation: "DELETE"`；首次删除要求最近 15 分钟内创建的 BFF 会话，重试使用删除回执与 Cookie 令牌哈希。BFF 暂停本地身份映射、撤销其他会话后调用内部 gRPC `tripmap.v1.AccountCleanupService/CleanupUserData`（不暴露为浏览器方法）清理 S3 与 tripmap 业务元数据，再删除本地业务 profile 并写入完成回执（24h）。此操作不会删除 Logto 账号。删除中 BFF 仅允许 Me、Logout、DeleteAccount；`User.deletion_pending` 在 Me 中返回。
- **浏览器身份与云端数据删除** 仅经 BFF `bff.v1.AuthService`；tripmap 不提供公网 Auth Connect，内部 `AccountCleanupService` 供 BFF 编排业务数据删除调用。
- `FootprintService.GetChanges`：参数 `since_revision` / `limit`，返回有序 `changes`、`next_revision`、`current_revision`、`has_more` 与 `resync_required`。
- `FootprintService.SyncChanges`：参数 `mutation_id`、`base_revision` 和 1–2000 条 oneof 变更（F5 前为 600；上限容纳一次完整迁移：497 个城市、每城市多次旅行与 34 个省份标记，更大的改动由客户端拆成连续批次）。变更为旧版 `upsert_visit`（城市加 `legacy-<区域 ID>` 旅行）、`delete_visit`（连同旅行）、`upsert_mark`、`delete_mark` 以及 F5 新增的 `upsert_city`（只改城市字段）、`upsert_trip`（城市必须已存在，在账号中或在同一批次更早的位置）、`delete_trip`。旅行的所属城市不可更改。全批原子处理，冲突返回 ABORTED；同一 ID 相同内容返回原结果，复用 ID 提交不同内容返回 ALREADY_EXISTS。日志、revision、回执与记录在同一事务提交。

当前 BFF 使用 `auth_identities.deletion_requested_at` 和无身份外键的短期 `account_deletion_receipts`；tripmap 保存 `footprint_changes`、`sync_receipts`，不再保存认证或删除状态列。F3 再新增 `snapshots` 表和 `sync_receipts.snapshot_id`（可空列），应在部署前审核 Ent 迁移并**先升级后端，再部署前端**。旧 RPC 保留原语义并自动记录变化。旧数据不会被清空，也不会被服务端自动迁移上云（由用户在前端主动迁移），缺失旧日志时走全量快照。

CI 现在启动 PostgreSQL、Redis 和 S3 兼容测试服务，执行真实处理器/race 与对象读写检查。前端 CI 构建启用 `/api`，自动执行桌面及手机删除云端数据界面验收（模拟 RPC）。真实 OAuth、生产凭据和生产数据不进入 CI；前端发布镜像仍保持默认纯本地模式，部署云端能力需设置构建参数。

F1 代码范围已覆盖云端数据删除与增量同步；F1 总项仍保留真实 OAuth 验收未完成的状态。F2 迁移不新增接口；F5 把批量上限提到 2000、批量限流预算提到每分钟 6000 项。

## F5 升级与部署顺序

**兼容机制。** 每个足迹、快照请求携带 `X-Tripmap-Schema: 2`（新前端的拦截器统一设置；CORS 的 `Access-Control-Allow-Headers` 已加入该头，跨站部署无需额外配置）。`SystemService.GetStatus` 返回 `footprint_schema`（当前 2），`GetFootprints` / `GetChanges` / 快照响应带 `schema_version`，新前端据此在首次同步前判断后端是否已升级。

- 账号为版本 1 且请求带头：在用户行锁内原位升级账号（见 [数据与地图说明](trip-map/data-and-maps.md#f5-服务端数据与升级)），再按版本 2 语义处理本次写入；读取不会升级账号，版本 1 账号读取时得到合成的 `legacy-` 旅行。
- 账号为版本 2 而请求没有头：足迹与快照 RPC 一律返回 `failed_precondition`（消息以 “client upgrade required” 开头）。媒体接口不门控。已有回执的批量同步重放先于门控检查，因此账号升级前发出、丢失响应的批次仍可得到原结果。
- 账号为版本 1 且请求没有头：行为与升级前完全相同，旧前端在升级发生前可继续使用。

**部署顺序。** 先审核并应用数据库变更（新增 `city_trips`、`users.schema_version`、`media_objects.trip_id`、`snapshots.schema_version`/`trip_count` 与索引；列均有默认值，可在旧后端仍运行时先加），再升级后端，最后部署前端。新前端自带能力检测：连到未升级的后端时拒绝同步并保留本机修改，不依赖这一顺序；旧前端连到已升级后端时，未升级账号继续可用，已升级账号被拒绝并提示刷新。

**回滚。** 账号升级是单向的：一旦某账号升到版本 2，旧后端只认识 `city_visits` 里冻结的日期与备注，会看不到升级之后新增的旅行，也看不到对已有旅行的修改，因此回滚到旧后端前必须备份数据库，并接受升级后旅行相关修改丢失；不要在升级过账号的数据库上回滚后继续让旧客户端写入：旧后端对 `city_visits` 的修改不会回流到 `city_trips`，账号的 `schema_version` 仍为 2。需要重新升级时，应从升级前的数据库备份恢复，而不是依赖自动回填。

**请求体与限流。** 一个满批（2000 项、每项备注 1000 个字符）按 JSON 最坏估计约 6–12 MB。Footprint 处理器 Connect 读上限为 16 MiB；前端镜像 Nginx `/api` 为 `client_max_body_size 20m`（`deploy/trip-map-web/default.conf.template`，含 framing 余量）；自行部署的边缘代理应 ≥20 MiB，见 [部署与发布](deployment.md#请求体大小)。批量限流按项计（每用户每分钟 6000 项），单条写入仍为每分钟 600 次。

**验证。** `make test` 在没有数据库连接时跳过存储相关测试；F5 的 Go 测试需要 `TRIPMAP_TEST_POSTGRES_DSN` 与 `TRIPMAP_TEST_REDIS_ADDR`（媒体测试使用内存对象存储，不需要 S3；真实 S3 测试另需 `make test-integration` 的环境）。

## 跨功能加固测试覆盖

以下用例在 `TRIPMAP_TEST_POSTGRES_DSN` 与 `TRIPMAP_TEST_REDIS_ADDR` 已配置时运行真实 Connect → BFF → TLS gRPC → tripmap 链（`internal/e2e/chain_harness_test.go`），未配置则跳过。

| 测试 | 规格要点 |
| --- | --- |
| `TestChainMaxLegitimateSyncBatchPersists` | 接近满批迁移同步成功并持久化；重任务下游期限约 25s（需 `Connect-Timeout-Ms`） |
| `TestChainSyncBatchCountOverLimitRejected` | 超过 2000 项批量返回 `invalid_argument`，无写入 |
| `TestChainConnectBodyOverReadMaxRejected` | 超过 Connect 16 MiB 读上限被拒绝，不挂起 |
| `TestChainGrpcOversizedMessageRejected` | 超过内部 gRPC 默认 20 MiB 消息被拒绝 |
| `TestLegitimatePayloadLimitsAreConsistent` | 公网 16 MiB、内部 20 MiB、Nginx `client_max_body_size` 一致 |
| `TestChainDeadlineBudgets` | 普通 10s / 重任务 25s 上限、更短 `Connect-Timeout-Ms`、不可抬高上限 |
| `TestChainClientCancellationPropagates` | 取消 Connect 请求后下游 gRPC 上下文取消 |
| `TestChainNoAutomaticRetryOnUnavailable` | 下游 `Unavailable` 仅调用一次 |
| `TestChainRequestIDAndTraceparent` | 非法 request-id 替换、有效 traceparent 下传 |
| `TestChainSensitiveValuesStayOutOfLogsAndResponses` | 会话/CSRF 等不出现在响应与捕获日志 |
| `TestChainConnectProtocolVersionRequired` / `TestChainRejectsGRPCAndGRPCWeb` | 仅 Connect；拒绝 gRPC/gRPC-Web |
| `TestChainAnonymousFootprintDenied` | 匿名默认拒绝受保护读 |
| `TestChainForgedClientIPIgnored` | 伪造客户端地址 metadata 不被采信 |
| `TestForgedMetadataChangesNothing`（`trust_test.go`） | 伪造身份 metadata 无效 |
| `TestTripmapRejectsEveryCallWithoutAValidBFFCredential`（`trust_test.go`） | 无凭证/匿名凭证不能访问受保护方法 |
| `TestPublicKeyRotation` / `TestTripmapOnlyTalksTLSToATrustedIdentity`（`trust_test.go`） | 密钥轮换与 TLS 身份 |
| `TestChainOverH2C`（`transport_chain_test.go`） | 同一条真实链路在 h2c 上复跑隔离、匿名拒绝、消息上限、期限、取消、不重试与伪造地址用例；设置 `TRIPMAP_TEST_GRPC_TRANSPORT=h2c` 可让整个链路套件在 h2c 上运行 |
| `TestH2CListenerIsNotReachableOverTLS` / `TestMismatchedTransportsFailInsteadOfFallingBack` / `TestBFFReadinessFollowsTheGRPCTransport`（`transport_test.go`） | h2c 不接受 TLS 客户端且仍需签名、两端模式不一致时失败而不回退、BFF `/readyz` 随传输做真实签名调用；`TestTripmapRejectsEveryCall…`、`TestForgedMetadataChangesNothing`、`TestPublicKeyRotation`、状态与依赖用例在 tls 与 h2c 上参数化 |
| `TestPublicEntryIsConnectOnlyAndUnknownPathsAreNotForwarded`（`bff/app/status_test.go`） | 公网协议与路由 |
| `TestCredentialPathsStayOutOfTraces`（`bff/app/app_test.go`） | 分享/退订路径 span 脱敏 |
