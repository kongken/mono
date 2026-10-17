# BFF（公网入口）

一个仓库承载多个业务时，BFF（`cmd/bff`）是唯一的公网入口，统一处理跨域、客户端地址、请求生命周期和追踪脱敏。业务服务只在内网监听。当前只有 tripmap 一个业务。

## 当前状态

目标架构见 [Issue #26](https://github.com/kongken/mono/issues/26)。[Issue #29](https://github.com/kongken/mono/issues/29) 迁移匿名状态；[Issue #30](https://github.com/kongken/mono/issues/30) 迁移密码账号与 Cookie 会话；[Issue #33](https://github.com/kongken/mono/issues/33) 迁移足迹读/sync 全链路。

**当前已实现**

- `bff.v1.StatusService/GetStatus`（匿名）→ `tripmap.v1.SystemService/GetStatus`。
- `bff.v1.AuthService`：`Register`、`Login`、`Me`、`Logout`、`IssueCSRFToken`；账号与会话在 BFF 独立 PostgreSQL（`migrations/bff/`、`internal/bff/ent`）。会话 Cookie：`shanhai_sid`（HttpOnly、Path `/`、SameSite=Lax；生产 `auth.cookie_secure: true`）。CSRF：Cookie `shanhai_csrf` + 请求头 `X-CSRF-Token`；写操作校验 Origin。响应不返回会话令牌。
- `bff.v1.FootprintService`（读、增量、批量 sync、单条写）→ tripmap gRPC `FootprintService` 同名方法；`X-Tripmap-Schema` / `X-Tripmap-Regions` 白名单下传。`SyncChanges` 登记为 25 秒重任务；Connect 读上限 `grpcx.LegitimatePayloadBytes`（16 MiB），内部 gRPC 默认 20 MiB。注册/登录后调用 `ProfileService/EnsureProfile` 同步业务档案（同一 `user_id`）。
- 本地：`config.bff.example.yaml` 配置 `store.db`（建议库名 `bff`）、`store.redis`；Vite 代理 `/api` 至 BFF `:18081`（同源 Cookie）。

**开发数据保留（#30）**

不默认删除 tripmap 既有数据。新注册在 BFF 建账号 ID 并同步 tripmap。已有 tripmap 密码账号保留同一 ID 需一次性导入 BFF `auth_users`（运维/脚本，非应用内双写）；未导入者需重新注册或等待迁移。

**当前限制**

- OAuth、ListSessions、快照、媒体、协作等仍经透明网关 `tripmap.v1` + Bearer（#31–#32、#34–#44）。
- tripmap HTTP 已不再挂载 `FootprintService` Connect；BFF 透明网关上的 `/api/tripmap.v1.FootprintService/*` 仍代理至 tripmap 直至 #44 移除整包转发。
- 前端足迹 API 已全部调用 `bff.v1`；OAuth UI 在 #32 前不可用。

```
浏览器 → 前端 Nginx（BACKEND_UPSTREAM）→ BFF :18081 ─┬→ tripmap（内网）
                                                    └→ 以后的业务
```

## 路由

路由表在 `internal/bff/gateway/services.go`，每个业务登记三类信息：

| 字段 | 含义 | tripmap |
| --- | --- | --- |
| `GRPC` | 该服务的已迁移过程经内部 gRPC 调用，需要 `grpc.services` 与 `internal_auth` 配置 | `true` |
| `Packages` | Connect 过程 `/api/<包名>.<服务>/<方法>` 按包名转发 | `tripmap.v1` |
| `Routes` | 其他 HTTP 路径：精确匹配，或以 `/` 结尾时按前缀匹配，最长者优先；携带凭据的路径填写 `Trace` | `/s`、`/s/`、`/api/share/`（分享令牌）、`/api/notifications/`（退订签名） |
| `Headers` | 浏览器客户端额外发送、需要跨域放行的请求头 | `X-Tripmap-Schema`、`X-Tripmap-Regions` |

- 未登记的路径和不规范路径（含 `..`、`//`）返回 404，不转发。
- 转发保留方法、路径、查询串、请求体（流式，不缓冲）、`Host` 及其余请求头；`Forwarded` / `X-Forwarded-*` 原样传递。
- 服务不可达返回 502。日志只记录服务名和连接错误，不记录可能含令牌的 URL。
- BFF 不设请求体上限：前端 Nginx 限制为 16 MiB，各服务按接口自行限制。

## 公网 Connect 入口（`bff.v1`）

`/api/bff.v1.<服务>/<方法>` 由 BFF 自己应答，不转发；路由在 `internal/bff/app/app.go`，处理链在 `internal/bff/ingress/`。

- **只接受 Connect**：仅 `POST`，`Content-Type` 必须是 `application/json` 或 `application/proto`（可带 charset），否则返回 405 / 415（`Accept-Post` 列出可用类型）。gRPC、gRPC-Web 与 Connect 流式帧都在到达处理器前被拒绝。处理器还要求 `Connect-Protocol-Version: 1`，缺失或不是 `1` 返回 400。
- **路径**：未知方法或服务、多余路径段、含 `..` 或 `//` 的路径一律 404，不会落入通用转发。
- **匿名策略**：由 proto 方法选项 `platform.v1.anonymous` 声明（`proto/platform/v1/options.proto`）；**默认受保护**，未知过程也受保护。BFF 的拦截器和 tripmap 的拦截器各自独立执行同一规则。匿名方法不使用浏览器会话，调用内部服务时不带用户 ID。
- **期限与取消**：普通过程上限 10 秒；上传等重任务上限 25 秒，需在 `ingress.Policy.Heavy` 中显式登记（已登记 `FootprintService/SyncChanges`）。实际期限取客户端剩余期限（`Connect-Timeout-Ms`）与上限的较小值，在 BFF 设置一次并通过 gRPC 期限传给业务服务；客户端取消会传播到服务，服务端处理随之停止。不做任何自动重试。
- **请求 ID**：读取 `X-Request-Id`，仅当匹配 `^[A-Za-z0-9._-]{1,64}$` 时沿用，否则替换为随机 32 位十六进制；响应（含错误）回写该头，并作为内部 metadata 下传。
- **客户端地址**：与透明网关相同，按 `client_ip_header`（受信反向代理写入的头）解析，未设置时用 TCP 对端；客户端自带的 `X-Bff-Client-Ip` 之类的头不会被读取。
- **错误**：业务服务的错误消息不原样返回；`UNAVAILABLE`、`DEADLINE_EXCEEDED`、`CANCELED` 映射为同名 Connect 码，BFF 凭证被拒（`UNAUTHENTICATED`）、`INTERNAL` 等服务端故障统一为 `INTERNAL: internal error`。tripmap 不可达时返回 `UNAVAILABLE`，不会返回过期或伪造的状态。
- **跨域**：与 `/api/` 其他路径一样由 BFF 按 `cors_allowed_origins` 应答。

## 内部 gRPC 与凭证

BFF 以生成的 grpc-go 客户端（`pkg/proto/<包>/*_grpc.pb.go`）逐方法调用业务服务，两层契约独立演进：`bff.v1` 给浏览器，`tripmap.v1` 只给 BFF。

**传输**：业务服务用 `internal/platform/grpcx` 自管 TLS 监听（TLS 1.3，默认单消息上限 20 MiB，覆盖现有 16 MiB 合法请求并留出序列化开销；两端都显式设置，不依赖 grpc-go 的 4 MiB 默认值）、拦截器和优雅关闭（先排空进行中的调用，10 秒后强制停止）。BFF 以 `grpc.ca_file` 校验服务证书，没有明文或跳过校验的回退。业务服务的 HTTP 端口只保留 `/ping`、`/readyz` 以及尚未迁移的路由。

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
- **私钥与公钥**：私钥（PKCS#8 PEM）只在 BFF；服务只持受信公钥（PKIX PEM）。不依赖网络位置认证，不使用 mTLS。
- **密钥轮换**：在服务的 `internal_auth.trusted_keys` 加入新公钥（`kid` 不同，新旧并存）→ 把 BFF 的 `internal_auth.kid` 与 `private_key_file` 切到新密钥 → 等旧凭证的有效窗口与允许偏差过去（约 70 秒）后撤下旧公钥。仍用已撤销密钥签名的 BFF 会被拒绝，浏览器看到 `INTERNAL`。
- **日志**：凭证、Cookie、CSRF 令牌等不进入日志；上游错误只记录服务名和 gRPC 码。

本地开发用 `make dev-secrets` 生成 `dev-secrets/`（CA、tripmap 证书、签名密钥，`kid` 为 `dev-1`，已被 Git 忽略）。

## 统一处理的事项

- **客户端地址**（透明转发的路径）：BFF 按自身的 `client_ip_header`（未设置时用 TCP 对端地址）算出客户端地址，转发时总是覆盖 `X-Real-IP`，客户端无法自选地址。业务服务配置 `auth.client_ip_header: X-Real-IP`，用于登录与分享限流。
- **跨域**：`/api/` 下的跨域请求由 BFF 按 `cors_allowed_origins` 应答，预检请求不转发；业务服务返回的 `Access-Control-*` 头会被去掉，避免出现两套。
- **追踪脱敏**：Butterfly 的 otel 中间件会记录原始路径；登记了 `Trace` 的路径会在追踪里替换为模板（如 `/s/{token}`），查询串置空。

## 健康检查

| 路径 | 行为 |
| --- | --- |
| `GET /ping` | BFF 存活，返回 `{"message":"pong"}` |
| `GET /readyz` | 并发请求每个服务的 `/readyz`（各 2 秒超时）：全部 200 时返回 200，否则 503；`dependencies` 按服务名列出 `ready`；配置 BFF 库时还检查 `bff_postgres` |

BFF 启动时不要求业务服务已经可用，只在 `/readyz` 中反映。

## 配置

复制 `config.bff.example.yaml` 为 `config.bff.yaml`（已被 Git 忽略）。与 tripmap 一样由 Butterfly 文件模式读取，`${VAR}` 不展开。

- `db_store` / `redis_store` / `key_prefix` / `auto_migrate`：BFF 认证库与 Redis 限流（见 example 内 `store` 段）。
- `auth.session_ttl`、`auth.cookie_secure`、`auth.registration_disabled`、`auth.csrf_token_ttl`。
- `upstreams.<服务>`、`grpc.*`、`internal_auth.*`、`client_ip_header`、`cors_allowed_origins`：同前。

## 本地开发与部署

首次运行 `make dev-secrets` 生成本地证书与密钥。创建 PostgreSQL 库 `bff`（与 tripmap 同实例）。`make dev` 启动 BFF（`18081`）与 tripmap（`18082` HTTP、`18083` gRPC）。Vite 将 `/api` 代理到 BFF。

## 新增业务

（同前，略）

## 验证

- `internal/bff/auth`：注册/登录/会话/CSRF（需 `TRIPMAP_TEST_*`）。
- `internal/bff/ingress`、`internal/bff/app`、`internal/e2e`：Connect 策略与 TLS gRPC 信任。
- **`internal/e2e` `ChainHarness`**（见 [后端初始化](backend.md#生成构建与验证)）：Cookie 注册/登录、Me、Logout、A/B 足迹隔离、`SyncChanges` 幂等与 revision 冲突、错误 Origin/CSRF、过期/撤销/禁用/注销待完成会话、响应体不含会话令牌；`go test ./internal/e2e/ -run Chain`。
- **Playwright 真实栈**（默认跳过）：`apps/web/tests/bff-cookie-real.spec.ts`；先 `make dev`（或等价 BFF+tripmap），再 `E2E_REAL_BFF=1 TRIPMAP_DEV_BACKEND=http://127.0.0.1:18081 npm run test:e2e -w @trip-map/web -- bff-cookie-real.spec.ts`。CI 与常规 `npm run test:e2e` 仍用 fake backend；本 spec 在本沙箱未对真实栈执行。
- 其余 UI：Playwright 模拟后端覆盖登录骨架与业务场景。

## 当前限制

- 见上文「当前限制」；Butterfly 指标端口 `:2223` 本地双进程冲突同前。

## 待实现

OAuth、会话管理、其余业务 RPC 经 `bff.v1` 迁移、移除透明网关与 tripmap Bearer（#31–#44）。
