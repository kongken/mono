# 后端初始化

## 当前状态与技术栈

后端基础设施已初始化，参考 `neo-box` 的技术栈和分层：Go 1.26.5+、Butterfly、Gin、ConnectRPC、Ent、PostgreSQL、Redis、AWS SDK for Go v2（S3）。依赖版本以 `go.mod` 为准，RPC 契约使用 Buf 生成 Go 与 TypeScript 代码。

当前已实现服务启动、依赖检查、Ent 建表、S3 对象存储封装与基础客户端，以及 F1：账号与会话（`AuthService`）、足迹读写（`FootprintService`，含服务端校验、用户隔离、revision 与限流），前端登录与云端读写闭环也已接入（配置 `VITE_API_BASE_URL` 后出现登录入口，见 [功能描述](features.md)）。前端持久化离线修改、重连三方合并及同记录冲突选择已实现。已补齐账号注销、增量变更日志与批量同步；云端快照与恢复（F3，`SnapshotService`）已实现；F4 照片与附件已实现，分享仍待实现，真实 OAuth 授权验收待部署。未登录的城市足迹继续保存在浏览器；本地足迹迁移（F2）完全由前端经批量同步接口发起，后端没有专门的迁移接口，也不会自行读取或覆盖本地记录；云端已有足迹时，前端会在迁移前调用 `CreateSnapshot` 保存一份快照。

Redis 客户端已接入，后续用于会话、缓存或限流；本次没有把城市足迹存入 Redis。对象内容使用 S3，PostgreSQL 只存业务记录和媒体元数据。

## 项目结构

| 路径 | 职责 |
| --- | --- |
| `cmd/tripmap/` | Butterfly 服务入口，配置加载、启动、退出资源清理 |
| `internal/app/` | Bootstrap、依赖注入、Gin 路由 |
| `internal/config/` | 业务配置及必填存储名称校验 |
| `internal/application/` | ConnectRPC 服务实现：`SystemService`、`AuthService`、`FootprintService`、`SnapshotService`（含定时快照与保留维护）及认证拦截器 |
| `internal/auth/` | 会话令牌、Redis 限流与 OAuth state 存储；`provider/` 为 GitHub、Google OAuth 适配 |
| `internal/regions/` | 嵌入的行政区域索引，供服务端校验区域 ID、名称与归属 |
| `internal/testutil/` | 带独立 schema 和 Redis key 前缀的集成测试辅助 |
| `internal/health/` | 有超时的依赖就绪检查 |
| `internal/blobstore/` | S3 Put/Get/Delete 与 bucket 检查，不依赖具体供应方 |
| `internal/ent/schema/` | 数据库 schema 源文件 |
| `internal/ent/` | 生成的 Ent 客户端与迁移结构 |
| `proto/tripmap/v1/` | Protobuf 接口定义 |
| `pkg/proto/`、`src/gen/` | 生成的 Go / TypeScript 契约 |
| `src/api/system.ts` | 前端 Connect-Web 健康状态客户端 |
| `cmd/storage-init/` | 显式初始化配置中指定的 S3 bucket |
| `internal/integration/` | 可选的实际 PostgreSQL、Redis、S3 集成测试 |

## 媒体接口（F4，`tripmap.v1.MediaService`）

全部方法要求会话，只取当前用户 ID。媒体使用 `region_code` 关联目的地，和足迹 revision、快照独立。

| 方法 | 行为 |
| --- | --- |
| `PrepareUpload` | 接收区域、文件名、MIME、大小和 UUID `upload_id`；原子预留数量，返回媒体 ID、10 分钟上传 URL 与必须携带的头。相同 ID/内容重放返回原预留；内容不同返回 `already_exists`。已完成项不再签发上传 URL，过期预留需删除后重新创建 |
| `CompleteUpload` | 读取临时对象，限量读取并校验实际大小/图片格式、尺寸、解码或 PDF 头尾，生成处理后文件与缩略图，再标记 `ready`；响应丢失可重复确认，失败仍可重试，超过 24 小时拒绝确认 |
| `ListMedia` | 返回当前用户媒体元数据，可按区域筛选；包括 `pending`，隐藏 `deleting`，不泄露对象 key |
| `ReadMedia` | 只读取本人 `ready` 文件/缩略图，以 RPC bytes 返回，`Cache-Control: no-store`；他人、不存在或已删除项不提供内容，PDF 不提供缩略图 |
| `DeleteMedia` | 先关闭访问再清理三个对象 key，重复删除幂等；S3 失败返回 `unavailable`，保留清理任务供重试与维护任务处理 |

默认 JPEG/PNG/WebP/PDF，10 MiB，20 个/城市（含待确认），签发 60 次/用户/小时；图片最多 24 MP、最长边 8000 px，缩略图最长边 480 px。照片重新编码 JPEG 不复制 EXIF/GPS；PDF 只校验头尾、供下载，不做内容解析或杀毒。上传/确认通过用户行锁串行，与账号注销互斥；服务端并发图像处理最多两个，并在像素解码前检查尺寸。确认最多 30 秒。尚未支持 HEIC、多次旅行关联、媒体离线队列和分享。

临时 URL 仅签发随机 staging key；确认后的 canonical key 不可用该 URL 改写。[S3 签名 URL 可在有效期内重复使用](https://docs.aws.amazon.com/AmazonS3/latest/userguide/PresignedUrlUploadObject.html)，因此删除账号时仍保留不含身份的临时对象清理队列。每小时处理删除失败与 24 小时未完成的上传；队列 24 小时后再次删除临时对象。超长在途上传需由 staging 生命周期兜底；该规则不会删除处理后照片。

### 直传部署配置

先升级后端 schema（新增媒体字段和 `media_cleanups`），再部署前端。S3 endpoint 必须同时被后端和浏览器访问，正式站点使用 HTTPS；不可把容器内网地址签发给公网浏览器。Bucket 应为本项目私有、非版本化存储，不配置公共读取策略。

浏览器直传需要 S3 CORS（和 API 的 `auth.cors_allowed_origins` 分开配置）。`storage-init` 提供显式选项，保留其他规则，仅替换 ID 为 `tripmap-media-upload` / `tripmap-media-staging` 的规则；不会在服务启动时更改 bucket：

```bash
go run ./cmd/storage-init -config config.yaml \
  -media-cors-origins 'http://localhost:5173,http://127.0.0.1:5173' \
  -media-staging-lifecycle
```

正式环境换成实际前端来源，不允许 `*`；CORS 允许 PUT 与签名所需的请求头。生命周期仅对配置前缀下的 `media/staging/` 设置 2 天过期，用于清理极长在途或重放上传。初始化权限另需读写 bucket CORS / Lifecycle；运行服务只需已有对象读写删除权限。兼容供应方须支持这两类规则；不支持时由供应方配置等效临时目录清理任务。

CI 增加真实 S3 签名 PUT→校验/缩略图→私有读取→删除链路，真实 PostgreSQL/Redis 验证跨用户隔离、并发配额、请求重放、删除失败拒绝读取及注销后的临时清理；浏览器测试使用模拟 RPC，覆盖桌面/手机上传、丢失响应重试、下载、删除与格式拒绝，不表示正式部署已验收。

## 前端独立运行

后端是可选的。前端通过构建时环境变量 `VITE_API_BASE_URL` 决定是否连接后端：

- 未设置、为空或格式无效：纯本地模式，不创建 API 客户端，不向 `/api`、`/ping`、`/readyz` 发请求；全部功能使用浏览器本地存储。当前所有用户可见功能都属于这种情况，`npm run dev`、`npm run build` 以及任意静态托管（含前端镜像默认构建）都无需后端。
- `/api` 这类同源路径，或 `https://api.example.com` 这类绝对 http(s) 地址：启用后端能力。`/api` 配合 Vite 开发代理或前端镜像的 Nginx 转发使用。

变量在构建时写入，修改后需重新构建；前端镜像通过 `--build-arg VITE_API_BASE_URL=/api` 传入，镜像发布工作流未传递，因此发布的前端镜像默认纯本地模式；CI 界面测试构建单独传入 /api。解析逻辑见 `src/lib/backend.ts`，传输层见 `src/api/transport.ts`（未启用时不创建，登录入口也不渲染），业务客户端见 `src/api/`。登录与云同步必须通过该开关判断；后端不可用时，未登录用户不受影响，有有效账号缓存的已登录用户可保留离线修改并重试同步；注销待完成或缓存异常时暂停编辑，也可退出登录回到本地模式，不会阻塞本地足迹的读写。前端同步与会话逻辑在 `src/lib/sync.ts`、`session.ts`、`oauth.ts` 和 `src/hooks/useCloud.ts`。

前端仍位于根目录 `src/`，无需搬迁到 `front/`。后端 Go module 位于根目录；前后端分别构建。

## 本地开发

需要 Go 1.26.5+、Docker Compose、Node.js/npm。重新生成 RPC 代码另需 Buf，生成插件由 `buf.gen.yaml` 指定。

```bash
cp config.example.yaml config.yaml
make deps-up
make storage-init
make dev
```

另开终端启动前端：

```bash
npm install
npm run dev
```

联调后端时在 `.env.local` 设置 `VITE_API_BASE_URL=/api` 并重启 Vite；不设置则前端保持纯本地模式。开发代理默认转发到 `http://127.0.0.1:18081`，后端在其他端口时用环境变量 `TRIPMAP_DEV_BACKEND=http://127.0.0.1:端口` 启动 Vite。OAuth 登录需要在提供方登记的回调地址与 `config.yaml` 的 `redirect_url` 一致，形如 `http://localhost:5173/auth/callback/github`，该路径由前端页面处理（Vite 开发服务器与前端镜像的 Nginx 都回退到 `index.html`）。

Compose 只启动本项目的开发依赖，使用命名卷保存数据。S3 开发实例使用 [RustFS 官方容器](https://docs.rustfs.com/en/installation/container/docker)，提供持久化 S3 兼容接口；生产环境可配置 AWS S3 或其他兼容供应方，服务代码无需更换。

| 服务 | 本地地址 |
| --- | --- |
| PostgreSQL | `127.0.0.1:15432`，数据库 `tripmap` |
| Redis | `127.0.0.1:16379` |
| S3 API | `http://127.0.0.1:19000` |
| S3 开发控制台 | `http://127.0.0.1:19001` |
| Go 后端 | `http://127.0.0.1:18081` |
| Vite 前端 | 启动时输出地址，通常为 `http://localhost:5173` |

端口避开了现有 `neo-box` 开发服务。Vite 代理 `/api`、`/ping` 和 `/readyz` 到 `18081`；若改变后端端口，同时更新代理配置。`make deps-down` 仅停止并移除本项目容器，保留数据卷。

## 配置与启动规则

`config.example.yaml` 提供本地示例凭据。复制后的 `config.yaml` 和 `.env` 已被 Git 忽略，实际部署凭据只放在未跟踪配置或挂载的 Secret 中。

Butterfly 从 `BUTTERFLY_CONFIG_FILE_PATH` 读取同一个 YAML，`BUTTERFLY_CONFIG_TYPE=file` 启用文件模式。与 `neo-box` 一致，YAML 中的 `${VAR}` 不会自动展开。`.env.example` 提供文件模式与端口示例；`make dev` 直接设置这些变量，无需自动加载 `.env`。

- `db_store` 指向 `store.db` 的命名连接，必须为 PostgreSQL。
- `redis_store` 指向 `store.redis` 的命名连接。
- `storage.s3_store` 指向 `store.s3` 的命名客户端，必须配置 bucket。
- `storage.key_prefix` 设置对象 key 前缀，示例为 `tripmap`。
- `auth.session_ttl` 会话有效期，默认 `168h`（7 天）。
- `auth.registration_disabled` 为 `true` 时关闭注册（密码与 OAuth 新账号都被拒绝），默认开放注册。
- `auth.client_ip_header` 指向可信反向代理写入的客户端地址头（如 `CF-Connecting-IP`），用于登录限流；不设置则使用 TCP 对端地址。直接暴露在公网时不要设置，否则客户端可伪造该头绕过限流。
- `auth.cors_allowed_origins` 允许跨站调用 API 的浏览器来源（精确匹配，如 `https://trip.example.com`，不支持通配符）。同源部署（前端镜像的 Nginx 转发 `/api`）无需配置。
- `auth.oauth_providers.<github|google>` 配置 `client_id`、`client_secret`、`redirect_url`（OAuth 应用登记的前端回调页）、可选 `scopes` 与 `display_name`；缺少 `client_id` 或 `client_secret` 的提供方不会出现在 `ListOAuthProviders` 中。
- `auto_migrate: true` 在启动时通过 Ent 创建或补充表结构。本地示例开启；生产 schema 变更应先评审迁移，关闭自动迁移后仍需自行执行经过验证的迁移。本次尚无独立的版本化迁移流水线。

启动时检查 PostgreSQL、Redis 与 S3 bucket，缺失或不可连接则启动失败。后端不会在启动时自动创建 bucket；`make storage-init` 是独立显式操作，bucket 已存在且可访问时直接成功，不会配置公共访问策略。

S3 兼容服务可设置 endpoint 和 `use_path_style: true`；AWS S3 可省略 endpoint 并按供应方配置 region 和访问凭据。该 Butterfly 版本使用配置文件中的静态凭据。PostgreSQL 密码在框架内直接拼接 DSN，需避免 `@ : / ? # %` 等未转义字符。

## 已实现接口

| 接口 | 行为 |
| --- | --- |
| `GET /ping` | 存活检查，返回 `{"message":"pong"}` |
| `GET /readyz` | 检查 PostgreSQL、Redis 和 S3 bucket；全部可用返回 200，否则 503 |
| `POST /api/tripmap.v1.SystemService/GetStatus` | ConnectRPC 健康状态，返回 `ready` 和各依赖的 `name` / `ready` |

就绪检查对每个依赖设置 2 秒超时并发执行，只返回状态，不返回原始驱动错误、连接字符串或凭据。RPC 请求成功时 HTTP 状态表示 RPC 调用结果，依赖可用性应读取响应中的 `ready`；运维探针使用 `/readyz`。

```bash
curl --fail http://127.0.0.1:18081/ping
curl --fail http://127.0.0.1:18081/readyz
curl --fail http://127.0.0.1:18081/api/tripmap.v1.SystemService/GetStatus \
  -H 'Content-Type: application/json' \
  -H 'Connect-Protocol-Version: 1' \
  -d '{}'
```

健康接口无用户数据，无需登录。前端 `getBackendStatus()` 可调用生成客户端，界面不主动调用；没有配置 `VITE_API_BASE_URL` 时前端根本不会发请求。

### 账号与会话（`tripmap.v1.AuthService`）

除公共方法外，所有 RPC 都要求 `Authorization: Bearer <token>`，由 ConnectRPC 拦截器统一校验；未知、过期、已撤销令牌以及被禁用用户的会话返回相同的 `unauthenticated`。

| 方法 | 公共 | 行为 |
| --- | --- | --- |
| `Register` | 是 | 用户名（3–32 位 `a-z0-9_`，不区分大小写）和密码（8–72 字节）创建账号并登录；用户名冲突返回 `already_exists`；注册关闭返回 `permission_denied` |
| `Login` | 是 | 用户名 + 密码换取令牌；用户名不存在与密码错误返回同样的错误，并做等耗时的哈希比较 |
| `Logout` / `Me` | 否 | 撤销当前会话 / 返回当前用户 |
| `ListSessions` / `RevokeSession` | 否 | 列出自己未过期未撤销的会话（标记 `current`）/ 撤销自己的会话；他人的会话返回 `not_found` |
| `ListOAuthProviders` | 是 | 已配置的 OAuth 提供方，供客户端决定显示哪些登录按钮 |
| `BeginOAuthFlow` | 是 | 返回提供方授权地址和单次使用的 state（Redis，10 分钟） |
| `CompleteOAuthFlow` | 是 | 校验并消耗 state，用授权码换取身份，找到或创建账号并登录 |

- 令牌为 `tm_` 加 32 字节随机十六进制；数据库只保存 SHA-256，密码使用 bcrypt。
- OAuth 账号按 `(provider, external_id)` 识别，不按邮箱合并；不保存提供方邮箱。OAuth 账号没有用户名和密码。
- `state` 只能防止服务端重放；客户端必须在发起前保存 `state` 并在回调时比对，否则存在登录 CSRF。前端已实现：发起前把 provider 与 state 存入 `sessionStorage`，回调时比对并立即消耗，不一致则不调用 `CompleteOAuthFlow`。
- 限流（Redis 固定窗口）：登录每用户名 10 次 / 15 分钟、每客户端地址 40 次 / 15 分钟；注册每地址 10 次 / 小时；OAuth 每地址 60 次 / 15 分钟；足迹写入每用户 600 次 / 分钟（批量按项计，足够一次迁移全部足迹）；已有回执的批量重放不消耗额度。超限返回 `resource_exhausted`，Redis 不可用时返回 `unavailable`（不放行）。
- 当前没有找回密码、修改密码与邮箱验证；密码账号忘记密码无法自助恢复。

### 足迹（`tripmap.v1.FootprintService`）

所有方法都要求登录，用户 ID 只取自会话，请求中没有用户字段。

| 方法 | 行为 |
| --- | --- |
| `GetFootprints` | 在同一事务快照中返回全部城市足迹、省份标记和 `revision`（城市按区域 ID、省份按省份 ID 排序） |
| `UpsertVisit` / `DeleteVisit` | 保存或取消一个城市足迹 |
| `UpsertProvinceMark` / `DeleteProvinceMark` | 保存或取消独立省份标记；与城市足迹互不影响 |

- 服务端用嵌入的区域索引校验：区域 ID 存在，名称、所属省份与索引一致，颜色是五种之一，日期为空或真实日历日期，备注不超过 1000 个 UTF-16 码元。校验失败返回 `invalid_argument`。
- 每次写入在持有用户行锁的事务中执行，同一用户的写入串行；只有实际改变数据才使 `revision` +1。内容相同的重复保存、删除不存在的记录都成功且不增加 revision，因此重试幂等，响应中的 `changed` / `deleted` 表示是否发生变化。
- `base_revision` 保持旧客户端兼容：未设置 `require_revision` 时，0 表示不检查，非 0 表示检查。新增 `require_revision: true` 会检查任何 base_revision（包含 0）。检查版本与当前 revision 不同、且写入会改变数据时返回 `aborted`，附带 `RevisionConflict.current_revision`；已生效请求的同内容重放仍幂等成功。新前端读取完整快照后逐记录精确版本写入，版本变化时重新三方合并，同记录冲突需要用户选择。部署须先更新支持 `require_revision` 的后端，再部署新前端（旧后端忽略该字段，无法保护初始 revision=0）。尚无服务端增量变更日志、删除 tombstone 和 `SyncChanges` 接口。
- 区域索引的后端副本是 `internal/regions/regions.json`，必须与 `src/regions.json` 一致；`npm run download:maps` 会同时写入两份，Go 测试会比对。金门 `tw-09020`、连江县（马祖）`tw-09007` 归福建 `350000`，ID 和名称保持不变。
- 这两项旧云端记录若仍存为台湾 `710000`，快照和写入响应会返回规范化的福建归属，不修改原行、不提升 revision；旧客户端提交台湾归属时也先规范化再严格校验。与既有记录相同的规范化重放仍幂等，实际修改日期、颜色或备注后才按原有用户锁与 revision 机制写回福建归属。其他错误归属仍拒绝；没有数据库 schema 变化或批量数据迁移。独立台湾省份标记保留。
- 此归属修正需先部署后端，再部署前端，以免新前端向尚未更新区域索引的旧后端提交福建记录被拒绝。

### 快照（`tripmap.v1.SnapshotService`，F3）

所有方法都要求登录，只访问当前用户自己的快照；快照内容是 `SnapshotData`（城市足迹和省份标记），保存为 gzip 压缩的 Protobuf，不可修改。

| 方法 | 行为 |
| --- | --- |
| `ListSnapshots` | 按时间倒序返回元数据（ID、来源 `reason`、时间、城市与省份标记数量、快照时的 `revision`），不读取内容 |
| `CreateSnapshot` | 在持有用户行锁的事务中保存当前足迹。客户端只能请求 `manual` 或 `before_migration`，其他值返回 `invalid_argument`；该 `revision` 已有快照时返回已有的（`created: false`），不重复保存。每用户每小时最多 30 次 |
| `GetSnapshot` | 返回元数据与全部记录，供预览与比较；他人或不存在的 ID 返回 `not_found` |
| `RestoreSnapshot` | 参数 `snapshot_id`、`base_revision`、`mutation_id`。同一事务内：校验 `base_revision` 等于当前 revision（含 0，不同则 `aborted` 并附 `RevisionConflict`），保存 `before_restore` 快照，按最小差异（先删除、再新增或改写）把足迹改成与快照一致，每条变更照常写入变更日志并推进 revision。每用户每分钟最多 6 次 |

- 恢复的变更与普通编辑一样进入 `footprint_changes`，其他设备通过 `GetChanges` 增量同步；全部成功才提交，失败不留下部分结果，也不留下 `before_restore` 快照。
- `mutation_id` 与请求内容（快照 ID 和 `base_revision`）的哈希存入 `sync_receipts`（同一张表，`snapshot_id` 记录撤销用快照）：同 ID 同内容的重试不再次执行，返回原 revision、变更数和撤销快照（`replayed: true`；该快照已被保留规则清理时为空）；同 ID 不同内容返回 `already_exists`。回执检查先于 revision 检查，因此丢失响应后的重试不会误报冲突。
- 快照记录在恢复时用当前区域索引重新校验，名称、归属或区域 ID 已不被接受的返回 `failed_precondition`，云端不变。快照内的省份沿用既有规范化（金门、连江归福建）。
- `reason` 取值：`manual`、`scheduled`、`before_migration`、`before_restore`。Protobuf 中用字符串而非 enum，是因为前端 `erasableSyntaxOnly` 禁止生成的 TypeScript enum。

**保留与定时（默认值，常量在 `internal/application/snapshot_service.go`）。** 保留 30 天、始终保留最新 10 个、每账号最多 50 个，每次保存快照时对该账号执行，恢复期间不会清理正在恢复的快照；快照间隔 24 小时。`app.go` 的每小时维护任务在 Redis 锁 `tripmap:snapshot-maintenance`（50 分钟）保护下调用 `RunSnapshotMaintenance`：为“有足迹、自最新快照后有改动、且 24 小时内没有任何快照”的账号保存 `scheduled` 快照（在用户行锁内再次判断，多实例或重复运行不会产生重复），并对不再变化账号的过期快照执行保留规则。账号注销通过外键级联删除全部快照。

```bash
# 用 curl 体验（令牌来自 Register 或 Login 的响应）
curl -s http://127.0.0.1:18081/api/tripmap.v1.AuthService/Register \
  -H 'Content-Type: application/json' \
  -d '{"username":"demo","password":"correct horse battery"}'
curl -s http://127.0.0.1:18081/api/tripmap.v1.FootprintService/GetFootprints \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" -d '{}'
```

## 数据库模型

| 表 | 关键字段与约束 |
| --- | --- |
| `users` | UUID 字符串 ID、可空唯一用户名（密码账号）、`provider` + `external_id`（OAuth 账号，非空时唯一）、bcrypt 密码哈希、显示名、头像、禁用标记、足迹 `revision`、创建/更新时间；`email` 字段保留但目前不写入 |
| `auth_sessions` | 所属用户、唯一令牌哈希、创建/过期/最近使用时间、撤销标记；用户删除时级联 |
| `city_visits` | 所属用户、区域 ID/名称、所属省份、颜色、日期字符串、备注；`user_id + region_code` 唯一 |
| `province_marks` | 所属用户、省份 ID、颜色；`user_id + province_code` 唯一 |
| `media_objects` | 所属用户、唯一 object key、内容类型、非负字节大小、pending/ready 状态 |
| `snapshots` | 所属用户、`reason`（manual / scheduled / before_migration / before_restore）、快照时的 `revision`、城市与省份标记数量、gzip 压缩的 `SnapshotData` 载荷、创建时间；所有字段不可修改；`user_id + created_at`、`user_id + revision` 索引；用户删除时级联 |

城市足迹与省份独立标记分表。删除城市不影响省份独立标记；用户归属为必填外键，删除用户时数据库级联删除这些元数据。注销服务先清理 S3 实体对象，再级联删除元数据；对象清理失败保留已暂停账号以便重试。

时间戳默认 UTC。旅行日期仍是 `YYYY-MM-DD` 字符串，允许空值，不做时区转换。Ent 验证日期是否为有效日历日期，并限制颜色为前端五种色值、备注按与前端一致的 UTF-16 长度限制为最多 1000。区域有效性、名称与归属由 `FootprintService` 在写入前校验。

当前模型保留一用户一城市一足迹；`media_objects` 仍是预留表，不等于已实现附件上传。现有浏览器备份格式仍为 `version: 1`，本地数据迁移由前端以批量同步完成，没有服务端迁移逻辑。

## 生成、构建与验证

```bash
make ent            # 修改 Ent schema 后生成客户端
make buf            # 修改 proto 后生成 Go / TypeScript 契约
buf lint
make test           # Go 单元测试；未配置连接时跳过实际存储集成测试
make build          # bin/tripmap
npm test
npm run build
```

实际存储集成测试在开发依赖已启动且 bucket 已初始化后运行：

```bash
make test-integration
```

该命令使用 Compose 示例的本地地址和开发凭据。使用其他测试环境时，设置 `TRIPMAP_TEST_POSTGRES_DSN`、`TRIPMAP_TEST_REDIS_ADDR`、`TRIPMAP_TEST_REDIS_PASSWORD`、`TRIPMAP_TEST_S3_ENDPOINT`、`TRIPMAP_TEST_S3_ACCESS_KEY`、`TRIPMAP_TEST_S3_SECRET_KEY`、`TRIPMAP_TEST_S3_BUCKET`，运行 `go test -v ./internal/integration`。

PostgreSQL 测试创建独立随机 schema 并清理，Redis 测试使用随机 key 和 TTL，S3 测试仅操作随机测试对象并删除。测试覆盖城市唯一约束、非法日期/颜色、省份标记独立性、用户级联、Redis 读写和 S3 上传下载删除。

`internal/application` 的测试通过 HTTP 调用真实的处理器和拦截器，使用同样的隔离方式（随机 schema 与 Redis key 前缀），未设置 `TRIPMAP_TEST_POSTGRES_DSN` 和 `TRIPMAP_TEST_REDIS_ADDR` 时跳过。覆盖：快照的保存与去重、恢复的差异/撤销/revision 条件/幂等重放/原子性、用户隔离、保留与定时任务、注销清除快照；令牌与密码只存哈希、注册/登录校验与限流、会话过期/撤销/禁用、他人会话不可见、OAuth state 单次使用与注册关闭、用户 A/B 足迹隔离、非法数据、幂等、revision 冲突、省份标记与城市足迹互不影响，以及并发写入不丢失。`.github/workflows/ci.yml` 在 PostgreSQL 与 Redis 服务容器中运行 `go vet`、`go test -race ./...` 和前端测试、地图检查、构建。

[Docker 镜像与 Actions](docker-images.md) 说明前后端两个镜像的构建发布。根目录 `Dockerfile` 仅构建后端镜像，运行时挂载 `/config/config.yaml`；容器中连接 Compose 依赖时，配置地址使用服务名和容器端口，而不是宿主机的 `127.0.0.1`。

## 下一阶段

从 [后端功能调研](backend-research.md) 中选择业务切片继续实现：多次旅行（F5）、照片（F4）等。F0（本地读取失败保护）以及 F1 的登录、前端云端读写、持久化离线修改与冲突选择已实现；账号注销与增量/批量同步已补齐；真实 OAuth 端到端验证按用户要求留待部署后完成，不能把 F1 全范围标为交付。F2（本地足迹迁移上云）与 F3（云端快照与恢复）已实现；F4–F12 仍待完整实现。

## F1 新接口与部署

- `AuthService.DeleteAccount`：请求 `confirmation: "DELETE"` 与密码账号的 `password`。OAuth 账号要求最近 15 分钟内创建的会话。暂停账号后其他业务 RPC 拒绝，当前会话可重试清理；重新登录返回 `User.deletion_pending`，只开放 Me、Logout 和注销重试。完成后原注销令牌可在 24 小时内得到完成回执，其余接口仍未授权。
- `FootprintService.GetChanges`：参数 `since_revision` / `limit`，返回有序 `changes`、`next_revision`、`current_revision`、`has_more` 与 `resync_required`。
- `FootprintService.SyncChanges`：参数 `mutation_id`、`base_revision` 和 1–600 条 oneof 变更（上限由 F2 迁移需要决定：497 个城市加 34 个省份标记）。全批原子处理，冲突返回 ABORTED；同一 ID 相同内容返回原结果，复用 ID 提交不同内容返回 ALREADY_EXISTS。日志、revision、回执与记录在同一事务提交。

新增 `users.deletion_requested_at`、`footprint_changes`、`sync_receipts` 和无账号外键的短期 `account_deletion_receipts`；F3 再新增 `snapshots` 表和 `sync_receipts.snapshot_id`（可空列），应在部署前审核 Ent 迁移并**先升级后端，再部署前端**。旧 RPC 保留原语义并自动记录变化。旧数据不会被清空，也不会被服务端自动迁移上云（由用户在前端主动迁移），缺失旧日志时走全量快照。

CI 现在启动 PostgreSQL、Redis 和 S3 兼容测试服务，执行真实处理器/race 与对象读写检查。前端 CI 构建启用 `/api`，自动执行桌面及手机注销界面验收（模拟 RPC）。真实 OAuth、生产凭据和生产数据不进入 CI；前端发布镜像仍保持默认纯本地模式，部署云端能力需设置构建参数。

F1 代码范围已覆盖注销与增量同步；F1 总项仍保留真实 OAuth 验收未完成的状态。快照和多次旅行等后续功能不随本次实现；F2 迁移不新增接口，只依赖批量上限 600 与限流预算 600。
