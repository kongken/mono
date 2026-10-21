# BFF（公网入口）

一个仓库承载多个业务时，BFF（`cmd/bff`）是唯一的公网入口，统一处理跨域、客户端地址、请求生命周期和追踪脱敏。业务服务只在内网监听。当前只有 tripmap 一个业务。

## 当前状态

目标架构见 [Issue #26](https://github.com/kongken/mono/issues/26)。[Issue #29](https://github.com/kongken/mono/issues/29) 迁移匿名状态；[Issue #30](https://github.com/kongken/mono/issues/30) 迁移密码账号与 Cookie 会话；[Issue #33](https://github.com/kongken/mono/issues/33) 迁移足迹读/sync 全链路。

**当前已实现**

- `bff.tripmap.v1.StatusService/GetStatus`（匿名）→ `tripmap.v1.SystemService/GetStatus`。
- `bff.v1.AuthService`：`Register`、`Login`、`Me`、`Logout`、`ListSessions`、`RevokeSession`、`IssueCSRFToken`，以及 OAuth `ListOAuthProviders` 与 HTTP 登录/回调（#54；旧 Begin/Complete RPC 返回 unimplemented）；账号、OAuth 绑定与会话在 BFF 独立 PostgreSQL（`migrations/bff/`、`internal/bff/ent`），OAuth state 在 BFF Redis（10 分钟、单次使用、绑定 provider/PKCE、app/落点、固定 redirect URI 和浏览器 Cookie 摘要）。会话 Cookie：`kongken_sid`（HttpOnly、Path `/`、SameSite=Lax；生产 `auth.cookie_secure: true`）。CSRF：Cookie `kongken_csrf` + 请求头 `X-CSRF-Token`；写操作（含 `RevokeSession`）校验 Origin。响应不返回会话令牌；会话列表与撤销只读写 BFF 存储，不向 tripmap 下传浏览器会话 ID。
- OAuth 提供方在 `config.bff.yaml` 的 `auth.oauth_providers`（GitHub、Google、Logto；Logto 带 PKCE），与旧 tripmap `auth.oauth_providers` 字段含义相同，配置迁到 BFF。
- `bff.tripmap.v1.FootprintService`（读、增量、批量 sync、单条写）→ tripmap gRPC `FootprintService` 同名方法；`X-Tripmap-Schema` / `X-Tripmap-Regions` 白名单下传。`SyncChanges` 登记为 25 秒重任务；Connect 读上限 `grpcx.LegitimatePayloadBytes`（16 MiB），内部 gRPC 默认 20 MiB。注册/登录后调用 `ProfileService/EnsureProfile` 同步业务档案（同一 `user_id`）。
- `bff.tripmap.v1.NotificationService`（全部提醒设置 RPC）→ tripmap gRPC `NotificationService`；浏览器经 Cookie 会话，业务仍只认 BFF 签发的内部凭证与用户 ID。
- 公开 HTTP：`GET/HEAD/POST /api/notifications/unsubscribe` 在 BFF 显式注册（`internal/bff/publichttp`），边缘限流与追踪路径模板 `/api/notifications/unsubscribe`（不含查询令牌），再调用 tripmap gRPC `NotificationPublicService/ProcessEmailUnsubscribe`（匿名内部凭证 + 退订签名令牌）；不使用 Cookie CSRF。
- `bff.tripmap.v1.ShareService`（#36：`GetShare`、`PreviewShare`、`SaveShare`、`RevokeShare`）→ tripmap gRPC `ShareService` 同名方法；浏览器经 Cookie 会话。
- 分享公开页：`GET|HEAD /s/<token>`、`/s/<token>/og.png`、`/api/share/<token>/data`、`/api/share/<token>/photos/<句柄>` 在 BFF `publichttp` 登记（边缘限流、追踪模板 `/s/{token}` / `/api/share/{token}`），再调用 tripmap gRPC `SharePublicService`（匿名内部凭证 + 分享令牌）；tripmap HTTP 不再挂载这些路径。
- `bff.tripmap.v1.FriendService`（#37）：好友概览、隐私设置、请求/解除与好友足迹对比 → tripmap gRPC `CollaborationService` 同名方法；好友码与展示字段在 tripmap 业务档案，不查 BFF 认证库。写操作校验 CSRF。
- `bff.tripmap.v1.SharedTripService`（#38）：共同旅行邀请/响应、更新、角色与成员管理、解散/退出及共享媒体列表/读取 → tripmap gRPC `CollaborationService` 对应方法；写操作校验 CSRF；`ReadSharedTripMedia` 登记 25 秒重任务。tripmap 公网 Connect 上的 `CollaborationService` 已移除。
- `bff.tripmap.v1.ItineraryService`（#41：`ListItineraries`、`GetItinerary`、`SaveItinerary`、`DeleteItinerary`）→ tripmap gRPC `ItineraryService` 同名方法；写操作校验 Origin/CSRF。tripmap 公网 Connect 上的 `ItineraryService` 已移除。
- `bff.tripmap.v1.SnapshotService`（#34：`ListSnapshots`、`CreateSnapshot`、`GetSnapshot`、`RestoreSnapshot`）→ tripmap gRPC `SnapshotService` 同名方法；`X-Tripmap-Schema` / `X-Tripmap-Regions` 白名单下传。`CreateSnapshot` / `RestoreSnapshot` 写操作校验 Origin/CSRF；`RestoreSnapshot` 登记为 25 秒重任务。tripmap 公网 Connect 上的 `SnapshotService` 已移除。
- `bff.tripmap.v1.MediaService`（#35：`PrepareUpload`、`CompleteUpload`、`ListMedia`、`ReadMedia`、`DeleteMedia`）→ tripmap gRPC `MediaService`；写操作校验 CSRF；`PrepareUpload`/`CompleteUpload` 登记 25 秒重任务；Connect 读上限 16 MiB。tripmap 公网 Connect 上的 `MediaService` 已移除。
- `bff.tripmap.v1.ReportService/GetReport`（#39）→ tripmap gRPC `ReportService/GetReport`；`bff.tripmap.v1.SearchService/SearchNotes` → tripmap gRPC `SearchService/SearchNotes`（Connect 读上限 4 KiB）。tripmap 公网 Connect 上的 `ReportService` / `SearchService` 已移除。
- `bff.tripmap.v1.RegionService`（#40：`GetRegionIndex`、`PreviewRegionUpgrade`、`ApplyRegionUpgrade`）→ tripmap gRPC `RegionService` 同名方法；`ApplyRegionUpgrade` 校验 Origin/CSRF；`X-Tripmap-Regions` 白名单下传。tripmap 公网 Connect 上的 `RegionService` 已移除。
- 可选 Mapbox 底图瓦片：`GET /api/tiles/{z}/{x}/{y}.png`（匿名）在 `gateway.Services` 登记为 tripmap 的 HTTP 路由，BFF 透明转发到 `upstreams.tripmap`（需要 tripmap 的 `mapbox_tiles.enabled`，见 [后端初始化](backend.md#mapbox-瓦片代理可选)）。BFF 不持有 Mapbox token、样式或限流配置；转发时只保留 `Accept`、`User-Agent` 与缓存验证头 `If-None-Match`、`If-Modified-Since`（查询串原样转发，由 tripmap 校验），丢弃 Cookie、Authorization、CSRF、Origin 等其余浏览器头，并覆盖写入 `X-Real-IP`；保留 tripmap 的图片、缓存与验证响应头及 304 状态，去掉 `Access-Control-*` 并按 `cors_allowed_origins` 应答；tripmap 不可达时返回不缓存的 502。
- 本地：`config.bff.example.yaml` 配置 `store.db`（建议库名 `bff`）、`store.redis`；Vite 代理 `/api` 至 BFF `:18081`（同源 Cookie）。

**开发数据保留（#30 / #44）**

不默认删除 tripmap 既有数据。`migrations/tripmap/20261017180000_retire_tripmap_sessions.sql` 只删除 tripmap 侧会话表；`users` 上的 legacy 凭据列保留到运维确认导入之后。顺序：**(1)** 应用 tripmap + BFF 版本化迁移；**(2)** `bin/bff-import-legacy-accounts -tripmap-dsn … -bff-dsn …`（先 `--dry-run` 看报告）；**(3)** 抽查同一 `user_id` 可登录；**(4)** 可选手动执行 `deploy/sql/tripmap/post_import_drop_legacy_auth_columns.sql`（不在 atlas 中）。新注册仍在 BFF 建 `auth_users` 并 `EnsureProfile` 同步 tripmap 业务列。

**当前限制**

- 浏览器 RPC 仅 `/api/bff.*`（共用的 `bff.v1`、各产品的 `bff.<产品>.v1`）；`/api/tripmap.v1.*` 透明转发已移除。tripmap HTTP 只保留 `/ping`、`/readyz` 与可选的 `/api/tiles/…`（分享/unsubscribe 由 BFF `publichttp` 提供）。
- 前端的业务 RPC（含分享 API）使用 `bff.tripmap.v1`，OAuth/会话管理使用 `bff.v1`；账号凭据在 BFF 独立库；tripmap `users` 仍可能含 legacy 凭据列直至 post-import 清理（Ent 不写入这些列）。
- 真实 GitHub/Google/Logto 端到端授权验收仍依赖部署凭据，CI 使用受控 fake 提供方与 `internal/e2e/oauth_chain_test.go`。

```
浏览器 → 前端 Nginx（BACKEND_UPSTREAM）→ BFF :18081 ─┬→ tripmap（内网）
                                                    └→ 以后的业务
```

## 路由

`internal/bff/gateway/services.go` 的 `Services` 登记**上游业务服务名**与是否经 gRPC 调用（当前 tripmap：`GRPC: true`），供 `upstreams` 健康探测与 `grpc.services` 配置对齐；**不再**按 proto 包名透明转发 HTTP；仅 `Service.Routes` 中显式登记的 HTTP 路径会转发（目前只有 `/api/tiles/`）。BFF 自有登录路径和凭据追踪模板另在同文件 `BFFOwnedRoutes` 登记，浏览器额外请求头由 `BrowserHeaders` 登记。

| 入口 | 登记位置 | 说明 |
| --- | --- | --- |
| `GET /auth/login`、`GET /auth/callback/:provider` | `app.go` → `auth.ServeOAuthHTTP` | BFF 自有登录路由，回调查询串追踪脱敏，无业务上游转发 |
| `/ping`、`/readyz` | `internal/bff/app/app.go` | BFF 自身与依赖就绪 |
| `/api/bff.v1.<Service>/<Method>` | `app.go` `publicAPI` + `bffv1connect.New*Handler` | 共用的账号与会话 Connect RPC；`NoRoute` 下前缀 `/api/bff.` 进入此 mux |
| `/api/bff.tripmap.v1.<Service>/<Method>` | `internal/bff/tripmap/tripmap.go` `MountStatus`、`MountRPC` | 山海足迹业务 Connect RPC，同一个 mux |
| `/s/…`、`/api/share/…`、`/api/notifications/unsubscribe` | `app.go` `mountPublicHTTP` + `internal/bff/publichttp` | 分享页/数据/预览图、邮件退订；凭分享或退订令牌，不用 Cookie CSRF |
| `/api/tiles/{z}/{x}/{y}.png` | `gateway/services.go` `Routes`（`Anonymous: true`）+ `gateway.Forward` | 登记路径透明转发到 tripmap，规则与限流在 tripmap；不规范路径（`..`、`//`）404 |
| 其他路径 | `NoRoute` | **404**（含旧 `/api/tripmap.v1.*`） |

- 不规范路径（含 `..`、`//`）在 BFF 层拒绝或 404。
- BFF Connect 读上限 16 MiB；边缘 Nginx 20 MiB；内部 gRPC 默认 20 MiB（见 [部署与发布](deployment.md#请求体大小)）。

## 公网 Connect 入口（`bff.v1`、`bff.tripmap.v1`）

`/api/bff.<包>.<服务>/<方法>` 由 BFF 自己应答，不转发；路由在 `internal/bff/app/app.go` 与各产品的 `internal/bff/<产品>/`，处理链在 `internal/bff/ingress/`。

- **只接受 Connect**：仅 `POST`，`Content-Type` 必须是 `application/json` 或 `application/proto`（可带 charset），否则返回 405 / 415（`Accept-Post` 列出可用类型）。gRPC、gRPC-Web 与 Connect 流式帧都在到达处理器前被拒绝。处理器还要求 `Connect-Protocol-Version: 1`，缺失或不是 `1` 返回 400。
- **路径**：未知方法或服务、多余路径段、含 `..` 或 `//` 的路径一律 404，不会落入通用转发。
- **匿名策略**：由 proto 方法选项 `platform.v1.anonymous` 声明（`proto/platform/v1/options.proto`）；**默认受保护**，未知过程也受保护。BFF 的拦截器和 tripmap 的拦截器各自独立执行同一规则。匿名方法不使用浏览器会话，调用内部服务时不带用户 ID。
- **写操作防护**：除 `internal/bff/ingress/write_policy.go` 列出的只读 RPC 外，所有 `bff.*` 过程在 ingress 统一校验 CSRF 双提交与同源（`Origin`、配置的 `cors_allowed_origins`、或与 `Host` 匹配的 Origin，或在缺少 `Origin` 时浏览器的 `Sec-Fetch-Site: same-origin`）。`IssueCSRFToken` 与各类 GET 式读 RPC 豁免；公开 HTTP 路由使用领域令牌而非 CSRF。
- **期限与取消**：普通过程上限 10 秒；上传等重任务上限 25 秒，需在 `ingress.Policy.Heavy` 中显式登记（已登记 `FootprintService/SyncChanges`）。实际期限取客户端剩余期限（`Connect-Timeout-Ms`）与上限的较小值，在 BFF 设置一次并通过 gRPC 期限传给业务服务；客户端取消会传播到服务，服务端处理随之停止。不做任何自动重试。
- **请求 ID**：读取 `X-Request-Id`，仅当匹配 `^[A-Za-z0-9._-]{1,64}$` 时沿用，否则替换为随机 32 位十六进制；响应（含错误）回写该头，并作为内部 metadata 下传。
- **客户端地址**：按 `client_ip_header`（受信反向代理写入的头）解析，未设置时用 TCP 对端；客户端自带的 `X-Bff-Client-Ip` 之类的头不会被读取。
- **错误**：业务服务的错误消息不原样返回；`NOT_FOUND`、`PERMISSION_DENIED`、`INVALID_ARGUMENT`、`RESOURCE_EXHAUSTED` 等用户可见域错误按同名 Connect 码返回；`UNAVAILABLE`、`DEADLINE_EXCEEDED`、`CANCELED` 同理。BFF 凭证被拒（tripmap 返回的 `UNAUTHENTICATED`）与 `INTERNAL` 等服务端故障统一为 `INTERNAL: internal error`。tripmap 不可达时返回 `UNAVAILABLE`。
- **跨域**：与 `/api/` 其他路径一样由 BFF 按 `cors_allowed_origins` 应答。

## 内部 gRPC 与凭证

BFF 以生成的 grpc-go 客户端（`pkg/proto/<包>/*_grpc.pb.go`）逐方法调用业务服务，两层契约独立演进：`bff.v1`（账号）与 `bff.tripmap.v1`（山海足迹业务）给浏览器，`tripmap.v1` 只给 BFF。

**传输**：业务服务用 `internal/platform/grpcx` 自管原生 gRPC 监听，默认 TLS（TLS 1.3），默认单消息上限 20 MiB（覆盖现有 16 MiB 合法请求并留出序列化开销；两端都显式设置，不依赖 grpc-go 的 4 MiB 默认值）、拦截器和优雅关闭（先排空进行中的调用，10 秒后强制停止）。业务服务的 HTTP 端口只保留 `/ping`、`/readyz` 以及尚未迁移的路由。

传输由配置显式选择，业务服务用 `grpc.transport`，BFF 对每个上游用 `grpc.services.<名称>.transport`，取值 `tls`（默认）或 `h2c`，其他值（含大小写不同）启动即失败；**不会**因证书缺失或握手失败而推测或降级：

| 模式 | 服务端 | BFF 上游 |
| --- | --- | --- |
| `tls`（默认） | 必须有 `cert_file`、`key_file` | 以 `grpc.ca_file` 校验服务证书与 `server_name`（默认取地址主机）；证书、CA 或名称不符则调用失败，没有明文或跳过校验的回退 |
| `h2c` | 不加载证书；仍配置 `cert_file`/`key_file` 视为冲突并拒绝启动 | 不读取 CA，不接受 `server_name`；配置了 `ca_file` 也不会加载 |

- 两端模式必须一致：TLS 客户端连 h2c 监听、h2c 客户端连 TLS 监听都会失败（浏览器看到 `UNAVAILABLE`），不会换另一种传输重试。
- 同一 BFF 可混用：每个上游独立选择；`grpc.ca_file` 仅在至少一个上游使用 `tls` 时必填并加载，全 `h2c` 时可省略。
- h2c 是 gRPC 的 HTTP/2 prior knowledge（明文 TCP，无 HTTP/1.1 Upgrade），仍是同一个原生 gRPC 监听，不经过 Gin 的 HTTP 端口，也不是 gRPC-Web 或浏览器直连。消息上限、拦截器、期限与取消、不自动重试、优雅关闭在两种模式下相同。
- **风险**：h2c 没有 TLS 的服务端身份验证、传输加密和完整性保护。同网络的被动监听可读取 RPC 内容，主动攻击者可以篡改，短期凭证在 60 秒窗口内可被重放（`jti` 不防重放）；Ed25519 凭证只证明调用来自持有 BFF 私钥者，不能替代这些能力，**不得**宣称与 TLS 等价。只在经显式批准的可信内部网络使用（网络隔离，或由其他层如服务网格 / 隧道提供加密，由部署方评估），业务 gRPC 端口不得经公网 Ingress、NodePort 或 LoadBalancer 暴露。项目不会自动修改集群网络策略，也不会自动把任何部署切到 h2c。
- 启动日志记录所选传输（h2c 监听另有警告）；日志与错误不含凭证、用户数据或密钥。

**凭证**（`internal/platform/servicetoken`）：紧凑 JWS，`alg=EdDSA`，header 带 `kid`。

| claim | 内容 |
| --- | --- |
| `iss` | 签发者，等于 BFF 的 `internal_auth.issuer`，服务端必须配置同一值 |
| `aud` | 目标服务名（如 `tripmap`），服务端校验与自己的 `internal_auth.audience` 一致 |
| `sub` | 用户 ID；匿名调用不含此字段 |
| `iat` / `nbf` / `exp` | 签发 / 生效 / 过期时间（秒），`exp - iat` 不得超过 60 秒 |
| `jti` | 随机凭证 ID，仅用于日志关联，**不提供单次使用或完整防重放保证** |
| `mth` | 目标 RPC 的完整方法名，如 `/tripmap.v1.SystemService/GetStatus`，凭证只对该方法有效 |

- **校验**：服务端检查签名、`kid`、`iss`、`aud`、`mth`、时间窗口和必需字段；允许的时钟偏差默认 10 秒（`internal_auth.clock_skew` 可调，0–1 分钟）。所有失败返回同一个 `UNAUTHENTICATED`，不说明失败原因。
- **匿名与受保护**：匿名方法同样要求有效 BFF 凭证；不含 `sub` 的凭证打不开受保护方法。业务只能从拦截器放入 context 的类型化信息（`rpcauth.CallFrom` / `UserIDFrom`）取得用户 ID，不读取裸 metadata。
- **metadata 白名单**：BFF 每次调用都重建出站 metadata，写入 `authorization`、`x-request-id`、`traceparent`、`x-bff-client-ip`，以及浏览器声明的 `x-tripmap-schema`、`x-tripmap-regions`（足迹读/sync 需要）；服务端只读取白名单项。
- **私钥与公钥**：私钥（PKCS#8 PEM）只在 BFF；服务只持受信公钥（PKIX PEM）。不依赖网络位置认证，不使用 mTLS（TLS 为单向，只由客户端校验服务端证书）。两种传输下都要求 BFF 凭证；h2c 只去掉传输层 TLS 材料，不去掉签名密钥。
- **密钥轮换**：在服务的 `internal_auth.trusted_keys` 加入新公钥（`kid` 不同，新旧并存）→ 把 BFF 的 `internal_auth.kid` 与 `private_key_file` 切到新密钥 → 等旧凭证的有效窗口与允许偏差过去（约 70 秒）后撤下旧公钥。仍用已撤销密钥签名的 BFF 会被拒绝，浏览器看到 `INTERNAL`。
- **日志**：凭证、Cookie、CSRF 令牌等不进入日志；上游错误只记录服务名和 gRPC 码。

本地开发用 `make dev-secrets` 生成 `dev-secrets/`（CA、tripmap 证书、签名密钥，`kid` 为 `dev-1`，已被 Git 忽略）。

## 公开 HTTP（`internal/bff/publichttp`）

**凭证型公开页**（退订、分享页与预览图）在 BFF 用 `publichttp.Registry`（含 `MountParam` 通配路径）逐条登记：HTTP 方法、路径、可选 Redis 边缘限流（按客户端 IP）、`TracePattern`（追踪与 span 不含路径令牌）。处理器内调用 tripmap gRPC，经 `rpcauth.WithCall` 下传 `ClientIP`。

## 统一处理的事项

- **客户端地址**（透明转发的 `/api/tiles/` 路径）：BFF 按自身的 `client_ip_header`（未设置时用 TCP 对端地址）算出客户端地址，转发时总是覆盖 `X-Real-IP`，客户端无法自选地址。业务服务配置 `auth.client_ip_header: X-Real-IP`，用于登录与分享限流。
- **跨域**：`/api/` 下的跨域请求由 BFF 按 `cors_allowed_origins` 应答，预检请求不转发；业务服务返回的 `Access-Control-*` 头会被去掉，避免出现两套。
- **追踪脱敏**：Butterfly 的 otel 中间件会记录原始路径；登记了 `Trace` 或 `publichttp.TracePattern` 的路径会在追踪里替换为模板（如 `/s/{token}`、`/api/notifications/unsubscribe`），查询串置空。

## 健康检查

| 路径 | 行为 |
| --- | --- |
| `GET /ping` | BFF 存活，返回 `{"message":"pong"}` |
| `GET /readyz` | upstream HTTP `/readyz`、`bff_postgres`、`bff_redis`、各 `{name}_grpc`（按该上游所选传输 tls / h2c，签名 `GetStatus`）；2 秒超时，全部就绪 200，否则 503 |

BFF 启动时不要求业务服务已经可用，只在 `/readyz` 中反映。

## 配置

复制 `config.bff.example.yaml` 为 `config.bff.yaml`（已被 Git 忽略）。与 tripmap 一样由 Butterfly 文件模式读取，`${VAR}` 不展开。

- `db_store` / `redis_store` / `key_prefix` / `auto_migrate`：BFF 认证库与 Redis 限流（见 example 内 `store` 段）。
- `auth.session_ttl`、`auth.cookie_secure`、`auth.registration_disabled`、`auth.csrf_token_ttl`。
- `upstreams.<服务>`、`grpc.*`、`internal_auth.*`、`client_ip_header`、`cors_allowed_origins`：同前。`grpc.services.<名称>.transport` 为 `tls`（默认）或 `h2c`，见 [内部 gRPC 与凭证](#内部-grpc-与凭证)。

配置示例（`grpc.ca_file` 仅 TLS 上游需要；`internal_auth` 在所有模式下必填）：

```yaml
# TLS（默认，不写 transport 与此相同）
grpc:
  ca_file: /secrets/ca.pem
  services:
    tripmap: {address: tripmap:18083, server_name: tripmap.internal}
---
# 全部 h2c：不需要 CA 文件
grpc:
  services:
    tripmap: {address: tripmap:18083, transport: h2c}
---
# 混合：每个上游独立；TLS 上游仍以 ca_file 校验
grpc:
  ca_file: /secrets/ca.pem
  services:
    tripmap: {address: tripmap:18083, transport: h2c}
    other: {address: other:18083, server_name: other.internal}
```

从 TLS 手动切到 h2c：先在业务服务配置 `grpc.transport: h2c` 并去掉 `cert_file`/`key_file`，同时在 BFF 对应上游加 `transport: h2c` 并去掉 `server_name`，两端一起滚动（模式不一致期间该上游返回 `UNAVAILABLE`，不会回退）。升级应用本身不改变任何现有部署的传输。

## 本地开发与部署

首次 `make dev-secrets`；创建库 `bff`。`make dev` 启动 BFF（18081）与 tripmap（18082/18083 内网）。生产见 [部署与发布](deployment.md)。

## 新增业务

BFF 共用账号、会话、CSRF 与入口策略（`bff.v1.AuthService`、`internal/bff/auth`、`session`、`ingress`），单点登录的账号库在 BFF；各产品只增加自己的适配层：

1. `proto/bff/<产品>/v1/` 定义浏览器契约（包名 `bff.<产品>.v1`，Go 包别名 `bff<产品>v1`），运行 `make buf`。
2. `internal/bff/<产品>/` 放各服务的适配处理器，并提供与 `internal/bff/tripmap/tripmap.go` 同形状的 `Service`、`HeavyProcedures`、`MountStatus`/`MountRPC`、`MountPublicHTTP`。产品包之间不互相 import。
3. 在 `internal/bff/app/app.go` 的 `publicAPI`、`heavyProcedures`、`mountPublicHTTP` 里调用新产品的 `Mount*`；只读过程登记到 `internal/bff/ingress/write_policy.go`，其余过程自动要求 Origin/CSRF。
4. 在 `internal/bff/gateway/services.go` 登记上游名与 HTTP 路由，在 BFF 配置的 `upstreams`、`grpc.services` 增加地址。
5. 前端应用只使用 `@kongken/api-client/bff/v1/auth_pb` 与自己的 `bff/<产品>/v1/*_pb`。

## 当前限制（多产品）

- 会话与 CSRF Cookie 是 BFF 的 host-only Cookie（无 `Domain`）。同一信任范围内的 HTTPS 同站点应用可以从各自 origin 直连同一个 BFF，使用 `credentials: include` 和精确 CORS 白名单；不承诺任意跨站点或第三方 Cookie 可用。各独立前端的 `/api` 代理会把 Cookie 留在各自前端主机，不能据此宣称共享会话。
- `bff.tripmap.v1.StatusService` 的响应带有山海足迹专有字段（`footprint_schema`、`share_enabled`、`region_version`），是产品级状态，不是通用健康检查。

## 验证

- `internal/bff/auth`：注册/登录/会话/CSRF（需 `TRIPMAP_TEST_*`）。
- `internal/bff/ingress`、`internal/bff/app`、`internal/e2e`：Connect 策略与内部 gRPC（TLS 与 h2c）信任。
- **`internal/e2e` `ChainHarness`**（见 [后端初始化](backend.md#生成构建与验证)）：Cookie 注册/登录、Me、Logout、`ListSessions`/`RevokeSession`（跨设备撤销与 CSRF）、A/B 足迹隔离、`SyncChanges` 幂等与 revision 冲突、行程增删改读、提醒设置、公开退订 HTTP、**#43** 注销重试/S3 故障/完成回执/跨用户隔离、错误 Origin/CSRF、过期/撤销/禁用/注销待完成会话、响应体不含会话令牌；`go test ./internal/e2e/ -count=1`。浏览器注销 UI 仍用 Playwright 模拟 RPC（`apps/trip-map/tests/account-deletion.spec.ts`），真实 Cookie 链路以 e2e 为准。
- **Playwright 真实栈**（默认跳过）：`apps/trip-map/tests/bff-cookie-real.spec.ts`；先 `make dev`（或等价 BFF+tripmap），再 `E2E_REAL_BFF=1 TRIPMAP_DEV_BACKEND=http://127.0.0.1:18081 npm run test:e2e -w @kongken/trip-map -- bff-cookie-real.spec.ts`。CI 与常规 `npm run test:e2e` 仍用 fake backend；本 spec 在本沙箱未对真实栈执行。
- 其余 UI：Playwright 模拟后端覆盖登录骨架与业务场景。

## 当前限制

- 见上文「当前限制」；Butterfly 指标端口 `:2223` 本地双进程冲突同前。

## 待实现

真实 OAuth/SMTP/Web Push/生产 S3 端到端验收依赖部署环境；Playwright 真实 Cookie 栈见「验证」，默认跳过。

## 多 Web App 登录与迁移（#54）

GitHub、Google、Logto 统一使用 BFF HTTP 回调；Logto 继续使用 S256 PKCE。浏览器顶层导航到 `/auth/login?app=trip-map&provider=logto`（provider 默认 logto），可选 `return_to` 只接受该 app 的精确允许路径。未知 app、绝对/协议相对地址、百分号编码、反斜线、非规范路径、查询串和片段均拒绝。客户端不能选择 issuer、callback 或 origin。

每次尝试在 Redis 保存随机 192-bit state（10 分钟、GETDEL 单次消费）、app、完整返回地址、固定 redirect URI、PKCE verifier 和独立随机浏览器 Cookie 的 SHA-256 摘要。每个 state 有独立短期 HttpOnly、Secure、SameSite=Lax、host-only Cookie，生产使用 `__Host-` 前缀；标签页并发不会覆盖绑定。回调先消费 state，再校验 provider、绑定和配置中的 redirect URI/返回白名单；成功才建会话并 303 返回干净的应用地址，清除该次 Cookie。取消、缺参、过期、重放、上游失败只显示中文错误，要求重新发起，不建立新会话、不回显上游错误/授权码。未完成尝试的 Cookie 自行在 10 分钟后过期。

生产示例（示意域名，secret 只放忽略配置或 Secret）：

```yaml
cors_allowed_origins: [https://app-a.example.com, https://app-b.example.com]
auth:
  cookie_secure: true
  oauth_apps:
    trip-map:
      origin: https://app-a.example.com
      default_path: /
      allowed_paths: [/]
    app-b:
      origin: https://app-b.example.com
      default_path: /map
      allowed_paths: [/map, /]
  oauth_providers:
    logto:
      issuer_url: https://tenant.logto.app/oidc
      client_id: example-client
      client_secret: replace-in-secret
      redirect_url: https://api.example.com/auth/callback/logto
```

迁移步骤：

1. 在 Logto Traditional web 应用登记唯一固定 BFF callback；GitHub/Google 同步登记各自 `/auth/callback/github`、`/auth/callback/google`。把 BFF 的 `redirect_url` 从 SPA 地址改到该入口，增加 `oauth_apps` 与精确 `cors_allowed_origins`。缺少 oauth_apps、或不符合路径/HTTPS 规则的旧配置在启动时拒绝；部署者仍须确认所填 callback URL 实际路由到 BFF（同源代理可保持前端主机地址）；不兼容升级前正在授权的 state，用户需重新点击登录。
2. 公网网关将 `/auth/login` 和 `/auth/callback/*` 转发给 BFF；所有层的访问日志、错误日志、追踪/APM 均排除 OAuth 查询串。BFF 回调响应 `Cache-Control: no-store`、`Referrer-Policy: no-referrer`；随仓库的 Nginx 示例对 `/auth/` 禁止访问日志并限制错误日志。只允许受信网关访问 BFF；由网关覆盖 `X-Real-IP` 后才配置 `client_ip_header`，直连则留空；不依赖请求 Host/Referer 生成返回地址。
3. 山海足迹构建 `VITE_API_BASE_URL=https://api.example.com/api`、`VITE_OAUTH_APP_ID=trip-map`（默认 trip-map）。其他应用使用各自 app ID，通过同一 BFF 顶层入口登录，返回后用 Me 恢复身份。前端不处理授权码、不持久化 state/token。旧 Begin/Complete RPC 保留契约占位但返回 unimplemented；不存在可用的平行认证链。旧 SPA 登录临时 sessionStorage 键在加载时清理。
4. 同源模式仍可构建 `VITE_API_BASE_URL=/api`，通过同一个前端网关代理 `/api` 和 `/auth/`，callback 也登记这个网关地址，使登录与 API Cookie 在同一主机；Vite 和 Nginx 示例都已代理 `/auth/`。共享 BFF 多前端模式则让所有应用直连同一个 API 主机，不能混用独立代理的 Cookie。前端挂在子路径时把服务端默认落点和允许路径同步设为该路径。
5. 本地开发可以显式设置 `auth.oauth_development: true`：仅为 loopback/localhost origin 和 callback 允许 HTTP，配合 `cookie_secure: false`。生产移除此开关并使用 HTTPS/Secure；配置错误在启动时拒绝。密码入口策略不变；`registration_disabled: true` 仍会阻止 OAuth 新用户创建，不能用于“仅 Logto”。

CORS 由统一中间件应答：精确允许 Origin 的正常、错误与 OPTIONS 响应均带 Allow-Origin、Allow-Credentials 与 Vary，预检包含 Connect/CSRF/区域头及 X-Kongken-User；不允许 `*` 或任意回显。读取授权不会免除写入的 Origin/CSRF 验证。CSRF token 从 IssueCSRFToken 响应体取得，并在凭据请求中放入 X-CSRF-Token（不要求前端读取 BFF Cookie）。退出只撤销当前 BFF 会话，不调用 Logto 全局退出；其他设备会话按原规则保留。共享这个 BFF Cookie 的应用会在下一次请求观察到退出。

受控真实栈验证：

```bash
# make deps-up 后，以下环境只指向独立本地开发依赖
TRIPMAP_TEST_POSTGRES_DSN='postgres://tripmap:tripmap-local@127.0.0.1:15432/tripmap?sslmode=disable' \
TRIPMAP_TEST_REDIS_ADDR=127.0.0.1:16379 TRIPMAP_TEST_REDIS_PASSWORD=tripmap-local \
E2E_OAUTH_BROWSER=1 go test -v ./internal/e2e -run TestOAuthHTTPSCrossOriginBrowser -count=1
```

需要 Node 和 Playwright Chromium（`npx playwright install chromium`）。测试创建独立 PostgreSQL schema、Redis 前缀和临时 HTTPS 监听器；Chromium 映射 `.example.test` 到 loopback，两个不同 app 子域名经同一 BFF 与固定 callback，真实跳转并校验 S256 verifier、Cookie/Me、凭据化预检、成功/错误响应、错误 CSRF、退出、未允许 Origin 和无浏览器 token。它不替代真实 Logto 验收：后者需已登记的 HTTPS callback、有效客户端凭据、受信代理/证书、两个同站点应用与允许 Origin，逐个完成登录、Me、写操作、退出与并发流程。

共享 Cookie 换账号保护：页面为已确定身份的请求携带 `X-Kongken-User`，足迹引擎把该值绑定到自身账号。BFF 在同一请求内解析 Cookie 后比较预期 ID，不相符即 unauthenticated，旧缓存不会写入新账号。该头只能收窄会话身份，不能代替认证或选择其他账号；Me 恢复时不发送它。未升级客户端不发送该头仍按原会话认证，但不能获得这一并发账号切换保护，运营方应同步升级各共享 Cookie 的应用。
