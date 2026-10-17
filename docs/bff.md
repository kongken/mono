# BFF（公网入口）

一个仓库承载多个业务时，BFF（`cmd/bff`）是唯一的公网入口，统一处理跨域、客户端地址、请求生命周期和追踪脱敏。业务服务只在内网监听。当前只有 tripmap 一个业务。

## 当前状态

目标架构见 [Issue #26](https://github.com/kongken/mono/issues/26)：浏览器 →（Connect over HTTPS）→ BFF →（原生 gRPC over TLS）→ 业务服务。它按纵向切片落地，**当前只迁移了一条链路**：匿名状态查询（[Issue #29](https://github.com/kongken/mono/issues/29)）。

**当前已实现**

- `bff.v1.StatusService/GetStatus`：匿名的公网 Connect 入口，BFF 以生成的 grpc-go 客户端调用 `tripmap.v1.SystemService/GetStatus`（见下文“公网 Connect 入口”“内部 gRPC 与凭证”）。前端 `/status` 页面和 `getBackendStatus()` 使用它。
- tripmap 自管 TLS gRPC 监听（`grpc.listen`），只提供 `SystemService`；每个调用必须带 BFF 签发的 60 秒 Ed25519 凭证。tripmap 的 Connect 版 `SystemService` 已移除，`/api/tripmap.v1.SystemService/GetStatus` 不再有任何服务应答。

**当前限制（尚未迁移的部分）**

- 其他业务路径仍走透明网关：BFF 原样转发 HTTP（含 Connect 过程 `/api/tripmap.v1.*`），tripmap 的 Connect 处理器仍由原 Bearer 会话拦截器保护；账号、会话与 OAuth 仍在 tripmap 数据库。这是尚未迁移的现状，不是兼容能力，会随后续切片逐个改为 `bff.v1` + 内部 gRPC，最后移除通用转发。
- BFF 还没有会话认证：`bff.v1` 的受保护方法目前对所有调用者返回 `UNAUTHENTICATED`（受保护是默认）。公网只有匿名的 `GetStatus`。
- 没有 CSRF/Origin 校验、Cookie 会话和边缘限流（均属待实现）。

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
- **期限与取消**：普通过程上限 10 秒；上传等重任务上限 25 秒，需在 `ingress.Policy.Heavy` 中显式登记（当前为空）。实际期限取客户端剩余期限（`Connect-Timeout-Ms`）与上限的较小值，在 BFF 设置一次并通过 gRPC 期限传给业务服务；客户端取消会传播到服务，服务端处理随之停止。不做任何自动重试。
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
- **metadata 白名单**：BFF 每次调用都重建出站 metadata，只写入 `authorization`、`x-request-id`、`traceparent`、`x-bff-client-ip`；服务端只读取这四项，其中请求 ID 与客户端地址在凭证验证通过后才采信，地址不是合法 IP 时丢弃。其他头（如伪造的 `x-user-id`）不会被读取。`traceparent` 取自当前追踪上下文，未启用追踪时由 BFF 生成。
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
| `GET /readyz` | 并发请求每个服务的 `/readyz`（各 2 秒超时）：全部 200 时返回 200，否则 503；`dependencies` 按服务名列出 `ready` |

BFF 启动时不要求业务服务已经可用，只在 `/readyz` 中反映。

## 配置

复制 `config.bff.example.yaml` 为 `config.bff.yaml`（已被 Git 忽略）。与 tripmap 一样由 Butterfly 文件模式读取，`${VAR}` 不展开。

- `upstreams.<服务>`：路由表中每个服务的内网基础地址，如 `http://tripmap:18081`，用于 `/readyz` 探测和尚未迁移的 HTTP 路由。缺少某个服务、写了路由表中没有的服务名，或地址不是不带路径、查询和账号的 http(s) 地址时，BFF 拒绝启动。
- `grpc.ca_file`、`grpc.services.<服务>.address`（可选 `server_name`，证书名称与地址主机不同时使用）、`grpc.max_message_bytes`（默认 20 MiB）：路由表中标记 `GRPC: true` 的服务必须配置；缺失、写了未标记的服务名或证书文件无效时拒绝启动。
- `internal_auth.issuer`、`kid`、`private_key_file`：内部凭证的签发设置（见上文），三项必填。
- `client_ip_header`：BFF 前面可信反向代理写入的客户端地址头。位于前端镜像 Nginx 之后设为 `X-Real-IP`，位于 Cloudflare 之后设为 `CF-Connecting-IP`；客户端可直连 BFF 时不要设置。
- `cors_allowed_origins`：允许跨站调用 API 的浏览器来源，规则与 tripmap 原有配置相同；同源部署无需配置。

## 本地开发与部署

首次运行 `make dev-secrets` 生成本地证书与密钥。`make dev` 同时启动 BFF（`18081`，Vite 代理目标不变）和 tripmap（`18082` 为 HTTP，`18083` 为 gRPC over TLS）；也可分别运行 `make dev-bff` 和 `make dev-tripmap`。

镜像 `ghcr.io/kongken/trip-map-bff` 由 `deploy/bff/Dockerfile` 构建，容器内端口 18081，配置挂载在 `/config/config.yaml`，证书与密钥作为 Secret 挂载。tripmap 镜像额外监听 gRPC 端口 18083，只在内网开放；部署时为 tripmap 提供 `grpc.cert_file`/`key_file` 与 BFF 公钥，为 BFF 提供签发私钥与 `grpc.ca_file`。从“前端直连 tripmap”切换到 BFF 的顺序：

1. 部署 BFF，`upstreams.tripmap` 指向 tripmap 的内网地址；把 tripmap 原来的 `auth.client_ip_header` 和 `auth.cors_allowed_origins` 分别移到 BFF 的 `client_ip_header` 和 `cors_allowed_origins`。
2. tripmap 设置 `auth.client_ip_header: X-Real-IP`，并只在内网暴露。
3. 前端容器的 `BACKEND_UPSTREAM` 改为 BFF 地址。

回退时把 `BACKEND_UPSTREAM` 改回 tripmap 即可：前端 Nginx 同样会写 `X-Real-IP`，tripmap 自己的跨域处理也仍然保留。

## 新增业务

1. 新建 `proto/<业务>/v1/`、`cmd/<业务>/`、`internal/<业务>/`，需要数据库时使用 `migrations/<业务>/`。服务提供 `/ping` 与 `/readyz`，只在内网监听；业务 RPC 用 `grpcx` + `rpcauth` 提供 gRPC over TLS（参考 `internal/tripmap/app/grpc.go`），匿名方法在 proto 中用 `platform.v1.anonymous` 声明。
2. 在 `internal/bff/gateway/services.go` 登记服务（`GRPC: true`），并在 BFF 增加对应的 `bff.v1` 契约和适配；仍走 HTTP 的路径登记 `Packages`、`Routes`、`Headers`，路径或查询串带凭据时填写 `Trace`。
3. 在每份 BFF 配置的 `upstreams` 和 `grpc.services` 中加上服务地址（缺少时 BFF 拒绝启动），并给服务配置 `internal_auth`。
4. 新增 `deploy/<业务>/Dockerfile`，只复制本服务与 `internal/platform`，并在 `.github/workflows/docker-publish.yml` 的矩阵中登记。

各服务镜像只复制本服务与 `internal/platform` 的代码，因此业务之间互相 import，或 `internal/platform` 依赖某个业务时，对应镜像会构建失败。

## 验证

- `internal/bff/ingress`：Connect-only 门禁、匿名/受保护默认、期限上限与客户端更短期限、请求 ID 与客户端地址规则。
- `internal/bff/app`：BFF → 真实 TLS gRPC（受凭证拦截器保护的 tripmap 替身）：下传的 metadata、期限、取消、无自动重试、错误脱敏、协议与路径限制、跨域。
- `internal/e2e`（仅测试包，是唯一同时 import 两个服务的地方）：真实 BFF → TLS gRPC → 真实 tripmap，覆盖状态与依赖故障，以及直连 gRPC 的无凭证、坏签名、未知 `kid`、错误 `iss`/`aud`/方法、过期与未生效凭证、匿名凭证打不开受保护方法、伪造 metadata 无效、不受信 TLS 身份和公钥轮换。设置 `TRIPMAP_TEST_POSTGRES_DSN` 与 `TRIPMAP_TEST_REDIS_ADDR`（`make test-integration`）时，依赖探针使用随机 PostgreSQL schema 与 Redis key 前缀，测试结束自动清理；未设置时使用内存探针，此时数据库与 Redis 的实际连通不在验证范围内。
- 浏览器：`apps/web/tests/status.spec.ts` 在桌面与手机尺寸验证 `/status` 页面（模拟 BFF 应答，不能代替上述真实链路）。

## 当前限制

- 认证与账号仍在 tripmap（见上文）。
- 没有聚合接口：一个页面需要多个业务的数据时，前端分别调用。
- BFF 自身没有限流，限流仍由各服务完成。
- Butterfly 把指标端口固定为 `:2223`。本地 `make dev` 在同一台机器上运行两个进程时，后启动的进程会记录一条 `error serving metrics` 后继续运行，但没有指标；容器部署各有独立端口，不受影响。

## 待实现

以下为已确定方向，尚未实现。

**认证迁入 BFF**

- 账号、密码哈希、OAuth 绑定、会话和注销凭据迁入 BFF 自己的数据库（同一 PostgreSQL 实例中的独立库）。用户 ID 保持不变；是否保留开发数据另行决定，不默认清空。
- 账号、会话与 OAuth 改由 BFF 以 `bff.v1` 契约提供，会话使用同源 HttpOnly Cookie 并校验 Origin 与 CSRF Token；服务尚未上线，不保留旧过程名（`tripmap.v1.AuthService/*`）、Bearer 认证或旧前端兼容。
- BFF 校验会话后，把用户 ID 放入内部凭证的 `sub`（已实现的凭证机制，见上文）；默认不下传会话 ID。无有效会话的调用只能访问 proto 声明为匿名的方法。
- tripmap 保留 `users` 作为业务档案：revision、schema_version、好友字段，以及用户名、昵称、头像的副本，按身份声明补建或更新；现有级联删除继续在 tripmap 内部生效。

**注销编排**

由 BFF 标记注销，再逐个调用业务服务的内部接口 `DeleteUserData`（可重复调用，不经 BFF 对外暴露），最后删除账号与会话；完成后移除 tripmap 中的密码哈希等身份字段。
