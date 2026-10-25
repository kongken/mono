# 部署与发布

本文描述**当前已实现**的同源代理与 HTTPS 同站点多 Web App/BFF 拓扑、密钥与 TLS 布局、健康探针和运维轮换步骤。真实邮件厂商（Mailgun、SendGrid、SMTP、SMTP2GO）、Web Push、生产 S3 策略与社交平台抓取在本仓库 CI 中**未**端到端验收，没有对任何真实厂商实测；浏览器真实栈 Playwright 见 [BFF 验证](bff.md#验证)（默认跳过，需本地 `make dev` 与 `E2E_REAL_BFF=1`）。

## 拓扑

```
浏览器 ──HTTPS──► 边缘（Nginx 前端镜像 / Caddy 等）
                      ├─ 静态 SPA（/、/assets、/maps）
                      └─ /api（含 /api/tiles/）、/ping、/readyz、/s/、/api/share/、/api/notifications/ ──► BFF :18081
                                                                                              │
                                                                                              ├─ PostgreSQL（BFF 会话库）
                                                                                              ├─ Redis（限流、CSRF）
                                                                                              ├─ TLS gRPC ──► usersrv :18087（内网，账号与密码）
                                                                                              │     ├─ PostgreSQL（usersrv 独占库）
                                                                                              │     ├─ Redis（限流，key 前缀 usersrv:）
                                                                                              │     └─ 邮件厂商（出站 HTTPS / SMTP）
                                                                                              ├─ TLS gRPC ──► tripmap :18083（内网）
                                                                                              │     ├─ PostgreSQL（业务）
                                                                                              │     ├─ Redis
                                                                                              │     └─ S3
                                                                                              └─ TLS gRPC ──► metro :18085（可选）
```

- **公网**只暴露边缘 HTTPS 与 BFF 的 HTTP 端口（经边缘反代）；**不得**把 tripmap 的 `18082`（HTTP `/ping`/`/readyz`）或 `18083`（gRPC）直接映射到公网。
- 前端构建需 `--build-arg VITE_API_BASE_URL=/api`，使浏览器与 BFF 同源；纯静态托管不设置该变量时仍为本地模式。
- 示例文件：`deploy/trip-map-web/default.conf.template`、`deploy/metro-web/default.conf.template`（前端 Nginx）、`deploy/reverse-proxy/Caddyfile.example`（单站点 Caddy）、`deploy/compose.stack.example.yaml`（内网隔离的 Compose 栈，含 usersrv）、`deploy/postgres/init-databases.sql`（创建 `bff`、`usersrv`、`metro` 三个库的初始化脚本）、`deploy/k8s/secrets.layout.example.yaml`（Secret 键名示意）。
- 边缘对 `/auth/` 前缀一律返回 404（Nginx 模板与 Caddy 示例都有），不让旧的 `/auth/login`、OAuth 回调落入 SPA 回退。

## 配置来源

BFF、tripmap、usersrv 镜像及 Compose 示例默认使用文件模式。使用 Consul KV 时，覆盖各服务的 `BUTTERFLY_CONFIG_TYPE=consul`，设置进程可访问的 `BUTTERFLY_CONFIG_CONSUL_ADDRESS`，并按需设置 `BUTTERFLY_NAMESPACE`（默认 `apps`）。例如前缀为 `kongken/prod` 时，分别在 `kongken/prod/bff`、`kongken/prod/tripmap` 保存完整 YAML 配置；无需 `.yaml` 后缀或按字段拆 key。

`BUTTERFLY_CONFIG_CONSUL_NAMESPACE` 是 Consul API Namespace，不控制 KV 路径前缀。Consul 模式忽略 `BUTTERFLY_CONFIG_FILE_PATH`，但证书和签名密钥文件仍需挂载；配置变化后重启对应服务。环境变量、示例与工具限制见 [Consul 配置与路径前缀](backend.md#consul-配置与路径前缀)。

## 受信代理与客户端地址

- 边缘反代必须设置 **`X-Real-IP`**（客户端地址）和 **`X-Forwarded-Proto`**（`https`）；BFF 按 `client_ip_header`（生产建议 `X-Real-IP`）解析地址，并**覆盖**转发给 tripmap 的 `X-Real-IP`，客户端无法自选地址。
- tripmap `auth.client_ip_header: X-Real-IP`（见 `config.tripmap.example.yaml`），用于分享/退订限流；BFF 之后的服务不要信任浏览器直连。
- 同源部署通常**不需要** `cors_allowed_origins`；跨站 API 时在 BFF 配置精确来源列表。
- **不要**在边缘剥离 `Connect-Protocol-Version`、`Content-Type`、`Cookie`、`X-CSRF-Token` 或 Connect 相关头。

## 请求体大小

合法业务载荷上限为 **16 MiB**（`grpcx.LegitimatePayloadBytes`）；BFF Connect 读上限与此一致，内部 gRPC 与边缘默认 **20 MiB**（含序列化与 framing 余量）。前端 Nginx 模板 `client_max_body_size 20m`；Caddy 示例 `max_size 20MB`。调小任一层都会导致批量同步或媒体上传失败。

## 安全响应头

前端 Nginx 模板已添加 `X-Content-Type-Options`、`X-Frame-Options`、`Referrer-Policy`、`Permissions-Policy`；`index.html` 与落地页的 `location` 因 Nginx `add_header` 不继承而重复同一组头并加 `Cache-Control: no-store`。Caddy 示例在站点级设置同类头（`Referrer-Policy` 用 `?` 前缀，落地页的 `no-referrer` 不被覆盖）。分享/退订路径仍不写访问日志（令牌在路径或查询中），见 [公开分享页](trip-map/sharing.md#配置与部署)。

## TLS 与内部凭证 Secret

| 材料 | 归属 | 用途 |
| --- | --- | --- |
| `tripmap.pem` / `tripmap.key` | tripmap Pod/容器 | gRPC TLS 服务端证书（`grpc.cert_file` / `grpc.key_file`）；仅 `tls` 模式需要，`h2c` 不挂载 |
| `ca.pem` | BFF | 校验 tripmap 证书（`grpc.ca_file`）；仅存在 `tls` 上游时需要，全 `h2c` 可不挂载 |
| `bff.key`（Ed25519 PKCS#8） | **仅 BFF** | 签发内部 JWS（`internal_auth.private_key_file`） |
| `bff.pub`（PKIX） | tripmap、usersrv、metro（及轮换期多副本） | `internal_auth.trusted_keys[].public_key_file` |
| `usersrv.pem` / `usersrv.key` | usersrv | gRPC TLS 服务端证书（`grpc.cert_file` / `grpc.key_file`）；SAN 含服务 DNS 名；**密码经这条链路传输，跨主机必须用 TLS** |
| `hmac.key`、`mail-encryption-1.key`、邮件厂商凭据 | usersrv | `auth.hmac_key_file`、`mail.encryption.keys[].key_file`、`config.usersrv.yaml` 的 `mail.<provider>`；见[usersrv 的部署](#usersrv-的部署) |

Ed25519 私钥 / 公钥在 `tls` 与 `h2c` 两种内部传输下**都必需**：h2c 只去掉传输层的证书 / CA，不去掉应用层签名。上表展示文件方式；也可将 PEM 分别放入 BFF `internal_auth.private_key`、tripmap `internal_auth.trusted_keys[].public_key`，替换对应 `*_key_file`，不再要求独立 key 文件或签名密钥挂载。来源必须二选一，完整格式与保密要求见 [配置内联签名密钥](bff.md#配置内联签名密钥)。含私钥的配置必须按 Secret 管理，不可放入公开 ConfigMap；现有 Deployment 的文件挂载不会自动移除，需由部署方另行同步修改。

### 内部 gRPC 传输（tls / h2c）

默认且推荐 `tls`（TLS 1.3 单向，BFF 校验服务证书；不是 mTLS）。可信内网可在业务服务设置 `grpc.transport: h2c`、BFF 对应上游设置 `grpc.services.<名称>.transport: h2c` 以免证书依赖；配置示例、两端必须一致与 TLS → h2c 手动切换步骤见 [BFF 内部 gRPC 与凭证](bff.md#内部-grpc-与凭证)和[配置](bff.md#配置)。

- h2c 没有加密、服务端身份验证和完整性保护，被动监听、内容篡改与 60 秒窗口内的凭证重放仍可能发生，**不与 TLS 等价**；只用于经显式批准的可信内部网络，是否需要网络隔离或由代理 / 网格承担加密由部署方评估。
- 业务 gRPC 端口（tripmap `18083`）不得通过公网 Ingress、NodePort 或 LoadBalancer 暴露；本项目不自动修改集群网络策略，也不会因升级而改变现有部署的传输（未配置即 `tls`）。
- 两端模式不一致时，BFF `/readyz` 的 `{name}_grpc` 为未就绪，浏览器看到 `UNAVAILABLE`；不会自动回退到另一种传输。

本地示例显式使用 loopback h2c：`make dev-secrets` 仅生成签名密钥对，写入被 Git 忽略的 `dev-secrets/`（`kid` 为 `dev-1`）。本地 TLS 另用 `go run ./cmd/dev-secrets -tls` 并同步修改两端配置；已有配置不会自动切换。生产 TLS 用 CA 签发 tripmap 证书（SAN 含服务 DNS 名），签名私钥与公钥分别通过 Secret 文件或各自的 Secret 配置提供；文件布局示例见 `deploy/k8s/secrets.layout.example.yaml`。

### Ed25519 签名密钥轮换

1. 生成新密钥对，在**每个**业务服务（tripmap、usersrv、metro）的 `internal_auth.trusted_keys` **追加**新 `kid` 与 `bff.pub`（新旧公钥并存）。
2. 滚动重启这些服务，确认各自 `/readyz` 与 BFF `/readyz` 中 `tripmap_grpc`、`usersrv_grpc`（及 `metro_grpc`）正常。
3. 更新 BFF Secret 中的私钥，并把 BFF `internal_auth.kid` 与私钥来源（`private_key` 或 `private_key_file`，二选一）切到新密钥；滚动重启 BFF。
4. 等待旧凭证最长有效期（**60 秒**）加允许时钟偏差（默认 **10 秒**，`internal_auth.clock_skew`）后再从 `trusted_keys` 移除旧公钥。
5. 仍用已撤销私钥签名的调用会被业务服务拒绝，浏览器侧表现为 `INTERNAL`（usersrv 不可用时登录失败）。

## Mapbox 底图（可选）

默认关闭，未配置的部署继续使用纯本地地图与现有的自定义瓦片源。启用后浏览器 → 边缘 → BFF → tripmap → `api.mapbox.com`；token 只在 tripmap。规则、配置字段与错误码见 [后端初始化](backend.md#mapbox-瓦片代理可选)，前端署名见 [数据与地图说明](trip-map/data-and-maps.md#mapbox-代理底图可选)。

1. **token**：在 Mapbox 账号创建只含 `styles:tiles` 范围的 `pk.` 公共令牌，设置用量告警；tripmap 拒绝 `sk.` secret token。不要给令牌配 URL 限制后再伪造来源头（tripmap 的请求没有 `Referer`），需要限制时在账号侧评估可行方式。
2. **tripmap**：在 `config.tripmap.yaml`（或挂载的 Secret 文件）加入 `mapbox_tiles`（`enabled: true`、`token`、`username`、`style_id`，可调 `style_version`、`max_zoom`、`rate_per_minute`、`upstream_timeout`、`max_response_bytes`），重启。配置不合法会启动失败，错误不含 token。**轮换 token**：只改配置里的 `token` 并重启 tripmap，前端无需重建。
3. **网络**：tripmap 容器需要能出站访问 `https://api.mapbox.com`（443，遵循 `HTTPS_PROXY` 等标准环境变量）；BFF 到 tripmap 的 HTTP 端口（`upstreams.tripmap`）需要可达。tripmap 的 `18082` 仍不得暴露到公网。
4. **边缘与 BFF**：现有 Nginx/Caddy 对 `/api/` 的转发已覆盖 `/api/tiles/`，无需新增规则；边缘必须设置 `X-Real-IP`，限流按它计数。tripmap 的 `auth.client_ip_header` 需为 `X-Real-IP`，否则所有请求会被算作 BFF 的地址并共享同一个限流额度。
5. **前端**：重新构建并传入 `VITE_OSM_TILE_URL=/api/tiles/{z}/{x}/{y}.png`（同源）和 `VITE_OSM_TILE_PROVIDER=mapbox`，Docker 镜像用 `--build-arg`；前后端跨源时 URL 用 BFF 绝对地址，并在 BFF 配置 `cors_allowed_origins`。构建产物与浏览器请求中不应出现 token。可选再传 `VITE_OSM_TILE_VERSION`（与 `style_version` 相同）。当前 CI 工作流未传递这些变量。
6. **验证**：打开全国与省内地图的底图开关，网络面板中只有 `/api/tiles/…` 请求，响应正常；临时把 `enabled` 设为 `false` 并重启，确认请求得到 404、页面提示底图失败且 SVG 地图可用。

7. **切换样式**：同时修改 tripmap 的 `style_id`（或 `username`）与 `style_version`，重启 tripmap，并用新的 `VITE_OSM_TILE_VERSION` 重新构建前端。两者更新之间有短暂的不一致窗口：服务端先更新时，没有 `?v=` 的旧前端地址仍可用（每次重新验证，不会被旧缓存遮住），带旧版本号的地址得到 400；前端先更新时，带新版本号的地址在服务端重启前得到 400。建议同一次发布内完成。切换后在网络面板确认瓦片请求带新的 `?v=`，重复访问得到 304 或命中缓存。token 轮换不需要这些步骤。

费用与条款：瓦片请求计入 Mapbox 账号用量，256 像素瓦片按请求计费，数量约为 512 像素的 4 倍；代理限流只保护服务，不替代 Mapbox 的额度与条款，使用前请阅读 Mapbox 当前的服务条款和缓存规则。代理不做持久缓存、预取或离线下载；浏览器和符合条款的共享缓存按 Mapbox 的 `Cache-Control`（Static Tiles 默认 `max-age=43200`，即最长约 12 小时）缓存，代理不延长。

## 健康检查

| 探针 | 位置 | 依赖 |
| --- | --- | --- |
| `GET /ping` | BFF、tripmap、usersrv、metro | 进程存活 |
| `GET /readyz` | tripmap | `postgres`、`redis`、`s3`（各 2 秒超时） |
| `GET /readyz` | usersrv | `postgres`、`redis`、`worker`（后台清理的本地状态；不探测邮件厂商） |
| `GET /readyz` | BFF | 各 upstream HTTP `/readyz`（如 `tripmap`）、`bff_postgres`、`bff_redis`、各服务 **`{name}_grpc`**（按该上游的传输 tls / h2c，用签名凭证调用 `SystemService/GetStatus`） |

BFF 启动**不要求**下游已就绪（usersrv 未就绪时登录、注册与每个需要登录的请求失败）；编排系统应使用 BFF `/readyz` 作为对外就绪门。前端 `/healthz` 仅表示静态文件服务。

## 独立镜像与 import 边界

tripmap、BFF、metro、usersrv 与各前端为独立镜像（见 [Docker 镜像](docker-images.md)）。Dockerfile 只 `COPY` 本服务业务目录与 `internal/platform/`；`go test ./internal/platform/moduleboundary`（`make check-import-boundaries`）断言 `cmd/bff` 不依赖业务包、业务服务（tripmap、metro、usersrv）互不依赖也不依赖 `internal/bff`。

## 地铁足迹（metro，可选）

示例见 `deploy/compose.stack.example.yaml`（`metro`、`metro-web` 服务）与 `deploy/reverse-proxy/Caddyfile.example`（`metro.example.com` 站点）。步骤：

1. 在同一个 PostgreSQL 实例创建独立数据库 `metro`，复制 `config.metro.example.yaml` 为 `config.metro.yaml`，生产环境关闭 `auto_migrate` 并按文件名顺序执行 `migrations/metro/`；BFF 库另需执行新的 `migrations/bff/*_access_tokens.sql`。
2. `config.bff.yaml` 加 `upstreams.metro`、`grpc.services.metro`，`auth.account_cleanup` 列入 `metro`，按需调整 `mcp.rate_limit_per_minute`。不部署 metro 时去掉这些项，BFF 不会挂载 metro 的任何路由。
3. 管理员角色：用 `usersrv-admin` 给管理员账号授予 `metro-admin`；BFF 每次请求读取当前角色，上线前先让一个管理员登录并确认 `Me` 返回 `metro-admin`。
4. 边缘网关对 `/mcp/metro` 不记录请求头与访问日志（Bearer 令牌），并保证 `client_ip_header` 由网关覆盖（MCP 失败认证按该地址限流）。MCP 令牌由管理员在 `/admin/tokens` 创建，最长 90 天，明文只显示一次。

## usersrv 的部署

usersrv 是账号服务，BFF 没有它就无法登录或注册，因此是必需的。它只被 BFF 经内部 gRPC 调用，不得暴露到公网。镜像 `ghcr.io/kongken/mono-usersrv`（`deploy/usersrv/Dockerfile`，入口 `/usersrv`，运维命令 `/usersrv-admin`，容器内 HTTP 18086、gRPC 18087，配置挂载为 `/config/config.yaml`）；资源建议见 [后端初始化](backend.md#usersrv-服务账号核心p1)（内存限制不低于 512 MiB）。

1. **独立数据库与 Redis。** 在同一 PostgreSQL 实例创建独立数据库 `usersrv`（Compose 示例用 `deploy/postgres/init-databases.sql` 在空数据目录首次启动时创建 `bff`、`usersrv`、`metro`；已有数据目录不会重跑，须手动创建）。usersrv 不使用 S3。Redis 必需，限流在 Redis 不可用时拒绝而不是放行；key 前缀默认 `usersrv:`，与 BFF 共用实例时保持前缀不同。
2. **配置。** 复制 `config.usersrv.example.yaml` 为 `config.usersrv.yaml`（或放入 Consul / Secret），**逐项替换示例值**：`db_store`/`store`、`auth.hmac_key`（≥32 字节随机，`head -c 32 /dev/urandom | base64`）、`mail.encryption`（32 字节随机，与 hmac 不同）、`auth.apps.ozid.origin`（必填：OzID 的精确 https 来源，所有新的注册验证与重置邮件都落到这里，必须与 BFF `navigation.apps.ozid.origin` 一致并在 `cors_allowed_origins` 里；**生产关闭** `auth.allow_loopback_http`；升级前发出的邮件仍指向产品页面，保留 `trip-map`、`metro` 条目到这些邮件过期后再删除）、`mail.from_email` 与一个 provider（**生产关闭** `mail.allow_insecure_loopback`，SMTP 用 `implicit_tls` 或 `starttls`）。`auto_migrate: false`。示例里的 hmac 与加密 key 是公开的本地样例，直接用于生产等于没有保护。密钥和厂商凭据优先用 `key_file` / `hmac_key_file` 或整份配置放 Secret（布局见 `deploy/k8s/secrets.layout.example.yaml` 的 `usersrv-*`）。
3. **迁移。** 在发布前按文件名顺序对 `usersrv` 库执行 `migrations/usersrv/`；之后每个版本先应用新迁移再滚动 usersrv。BFF 库同样先应用 `migrations/bff/`（见[下一节](#自建用户系统的数据迁移)）。
4. **传输与信任。** 生产使用 `grpc.transport: tls`：CA 签发的 `usersrv.pem`/`usersrv.key`（SAN 含 usersrv 的 DNS 名）挂到 usersrv，BFF 的 `grpc.services.usersrv` 设 `transport: tls` 并挂 `ca.pem`。`internal_auth.trusted_keys` 只放 BFF 的公钥 `bff.pub`（`audience: usersrv`）。h2c 不加密，而这条链路携带明文密码，仅限单机本地开发或经批准的加密隧道。
5. **BFF 配置。** `upstreams.usersrv`（HTTP `/readyz`）与 `grpc.services.usersrv`；旧配置里的 `auth.oauth_apps`、`auth.oauth_providers`、`auth.oauth_development` 可删除（不再读取）；构建前端时不再需要 `VITE_OAUTH_APP_ID`。不部署 metro 时，BFF 的 `auth.account_cleanup`、`upstreams`、`grpc.services` 同步去掉 metro 的条目（usersrv 的 `auth.apps.metro` 若为过渡期保留，过期后也删除）。
6. **邮件。** Compose 中 usersrv 同时接入 `internal` 和 `mail-egress`：前者隔离内部服务，后者提供到邮件厂商的出站路由，不发布 usersrv 端口。自建网络策略需允许所选厂商的 HTTPS / SMTP 出站连接。usersrv 启动时不连接厂商，也不发探测邮件，配置错误只在启动校验或第一次发信时暴露。四家厂商都没有在本仓库实测，上线前按 [真实邮件手动冒烟](usersrv/mail-smoke-test.md)逐家验证，并为发信域名配置 SPF/DKIM/DMARC。切换厂商时递增 `mail.generation` 并先排空队列。
7. **首个管理员。** 没有“第一个注册用户自动成为管理员”。先用普通流程注册并验证邮箱，再在能访问 usersrv 数据库的位置执行（`USERSRV_DATABASE_URL` 优先于命令行参数，避免密码出现在进程列表）：

```bash
docker run --rm --network <内部网络> -e USERSRV_DATABASE_URL='postgres://…/usersrv?sslmode=…' \
  --entrypoint /usersrv-admin ghcr.io/kongken/mono-usersrv:main \
  grant-role -email admin@example.com -role metro-admin -operator alice -reason "ticket 42"
```

8. **上线顺序与回滚。** usersrv（含迁移）→ BFF（含迁移与配置）→ tripmap / metro → 前端。涉及 OzID 邮件的版本（T2）有更严格的顺序：迁移 → `ozid-web`（落地页就绪）→ usersrv（邮件由此开始链接到 OzID）→ BFF（必须配置 `navigation`）→ 其余前端；新版 BFF 配旧版 usersrv 是**不支持的组合**（空 `app_id` 会被旧 usersrv 拒绝），回滚时先 BFF 后 usersrv，细节与已测试/未测试范围见 [OzID 功能描述](ozid/features.md#升级顺序回滚与兼容边界t2)。OzID 的资料/密码/设备页（T3）没有迁移也不改 usersrv：先 BFF（新增 `RevokeOtherSessions`）后 `ozid-web`，见 [同一文档](ozid/features.md#账号资料密码与设备t3)。回滚镜像时不要回退 usersrv 的迁移，先确认旧镜像兼容当前表结构；一旦用户已在 usersrv 注册，回滚到 Logto 版本不是受支持的路径，需要恢复备份并接受账号丢失。
9. **验收。** BFF `/readyz` 中 `usersrv` 与 `usersrv_grpc` 就绪；走一遍注册 → 收邮件 → 设密码 → 登录 → 业务写入 → 找回密码 → 重新登录 → 注销（`make test-integration` 在隔离的真实 PostgreSQL / Redis / S3 上自动走这条链，不含真实邮件）。

旧 Logto 的外部资源与旧表的清理见 [Logto 退役与手动清理](usersrv/retirement.md)；服务启动与迁移不会自动删除任何东西。

## 自建用户系统的数据迁移

本项目不迁移旧的 Logto 账号、测试云端数据或会话。备份数据库并暂停登录与业务写入后，按以下顺序操作；所有清理 SQL 均由运维手动执行，不由服务启动调用。每个脚本的目标库、范围、前置检查与回滚见 [Logto 退役与手动清理](usersrv/retirement.md)：

1. 部署 usersrv（`config.usersrv.example.yaml`，独立数据库 `usersrv`，生产关闭 `auto_migrate` 并按文件名顺序执行 `migrations/usersrv/`）；在 BFF 配置 `upstreams.usersrv`、`grpc.services.usersrv`（见 [BFF](bff.md#配置)）。
2. **BFF 数据库**：按版本顺序应用迁移，包括 `migrations/bff/20261024161800_usersrv_sessions.sql`（会话增加 `auth_version`、新建 `bff_account_deletions`）。旧 Cookie 会话在第一次使用时被撤销，用户须重新登录。BFF 与 tripmap 的所有副本升级完成后，旧版本副本才不会再读旧表。
3. **tripmap 业务数据库（可选，且必须在开放新登录前）**：若整库业务数据均可丢弃，手动执行 `deploy/sql/tripmap/reset_test_profiles.sql`，删除旧测试 profile 及其级联业务数据。它不会清理 S3 实体对象；开放新登录后不要运行此脚本。
4. **tripmap 业务数据库**：手动执行 `deploy/sql/tripmap/post_import_drop_legacy_auth_columns.sql`，删除认证遗留列及旧用户名唯一索引；保留业务 `users` 表及所有业务结构。
5. 验证注册、登录、`Me`、业务读写、退出、会话撤销和注销后，**BFF 数据库**可手动执行（建议 `psql -v ON_ERROR_STOP=1 -f …`） `deploy/sql/bff/retire_logto_identities.sql`，删除旧的 `auth_identities`、`account_deletion_receipts`（以及更早的 `auth_sessions`、`auth_users`）。该脚本会先检查新的 `bff_account_deletions` 已存在、没有未完成的旧注销回执依赖。
6. 恢复流量。清理后不能直接回滚至旧版本；需要恢复数据库备份。

退出仅撤销当前 BFF 会话。会话在每个请求都和 usersrv 的账号状态、`auth_version`、角色核对：禁用账号、改密码、撤销角色在下一个请求生效。浏览器登录、注册与邮件落地页已在 trip-map 与 metro 实现（P5）；产品入口尚未切换到 OzID，但自 T2 起新邮件都链接到 OzID 的落地页（`ozid-web`），产品自己的落地页只服务升级前发出的旧邮件；usersrv 的发布见上一节，真实邮件厂商需要上线前手动验证。落地页 `/account/verify-email`、`/account/reset-password` 在 Nginx 与 Caddy 示例里（`ozid-web` 的模板为这两个路径单独声明，并限制 `script-src 'self'`）带 `Referrer-Policy: no-referrer`、`Cache-Control: no-store` 并回退到 `index.html`，自建边缘需保持这三点。

## 验证清单

- `make dev-secrets`、`make deps-up`、`make storage-init`、`make dev` 本地 smoke。
- `make build`、`make test`、`make check-import-boundaries`、`buf lint`、`npm run build`。
- 有隔离存储时 `make test-integration`（需要真实 Redis；设置 `USERSRV_TEST_*`，否则账号链整体跳过）与 `make test-integration-usersrv`。
- `npm test`（含两个前端的 Playwright 验收，模拟 BFF，不发真实邮件）。
- 上线前手动：[真实邮件冒烟](usersrv/mail-smoke-test.md)，以及用真实的 Nginx / Caddy 加载示例并检查响应头（见 [usersrv 规格的验证状态](usersrv/spec.md#验证状态)）。
- 文档链接：`make check-docs`。

## 多前端共用 BFF 的部署

公网 API 域名提供 `/api/bff.*` 与 `/mcp/metro`；不再有 `/auth/login` 或 `/auth/callback/*`。多个 HTTPS 同站点应用分别构建 `VITE_API_BASE_URL=https://api.example.com/api`；BFF 配置精确的 `cors_allowed_origins`，生产 `cookie_secure: true`。邮件里的链接来源由 usersrv 的 `auth.apps.ozid.origin` 决定，落地页 `/account/verify-email`、`/account/reset-password` 由 `ozid-web` 提供（响应头见 [OzID 功能描述](ozid/features.md#邮件流程t2)）。API 主机保存 host-only Cookie，应用用 credentials include 和 CSRF 响应体 token 调用；不要扩大 Domain。任意跨站点及第三方 Cookie 限制不属于支持承诺。

同源 `/api` 模式把 `/api` 代理到 BFF。独立前端代理会得到各自的 Cookie，不能与直连共享 BFF 主机会话混为一谈。`client_ip_header` 只信任已覆盖客户端同名头的网关；BFF 不按 Host/Referer 推导可信来源。

## OzID 用户中心的部署

OzID（`apps/ozid`，镜像 `ozid-web`）是纯静态站点，没有同源 `/api`：浏览器直接以 `credentials: include` 调用 BFF 的规范公开 origin，Cookie 只属于该 API 主机（host-only）。三个主机——OzID、API、各产品——须在同一 HTTPS 站点内，示例 `id.example.com`、`api.example.com`、`trip.example.com`、`metro.example.com`（均为保留示例，真实值由私有构建与部署配置注入）。

1. **前端重建**：OzID 以 `--build-arg VITE_API_BASE_URL=https://api.example.com/api` 构建（无默认值，缺失或非 https 即构建失败）；运行时设置 `API_ORIGIN=https://api.example.com`（用于 CSP，缺失则容器拒绝启动）。产品前端要与同一 API origin 对接，需同样改成绝对地址（见上文“多前端共用 BFF 的部署”）。
2. **BFF 配置**：`cors_allowed_origins` 列出所有前端的精确 origin；`navigation` 登记 `api_origin` 与各应用 origin/允许返回路径；生产 `auth.cookie_secure: true`。配置不一致时 BFF 启动失败并指出字段，不靠通配 CORS 补救。
3. **反代与 DNS/TLS**：见 `deploy/reverse-proxy/Caddyfile.example` 的 `api.example.com` 与 `id.example.com` 站点；API 主机必须覆盖并传递 `X-Real-IP`、`Host`、`X-Forwarded-Proto`（参见“受信代理与客户端地址”）。
4. 更换 API 主机或域名后旧 Cookie 不会转移，用户需重新登录；这是配置变更，不改源码。

运维仍须自行确认：真实 DNS、证书、TLS 终止、IP 允许名单和邮件厂商；自动化只覆盖受控的保留测试域名。镜像发布与 CI 不会自动部署，也不清理数据。运行示例见 `deploy/compose.stack.example.yaml` 的 `ozid-web`。

## 统一管理控制台的部署

控制台（`apps/admin`，镜像 `admin-web`）是纯静态站点，也没有同源 `/api`，并且**只在来源白名单之后**提供页面。它与 BFF 的 `admin` 周界是两道各自独立的防线，白名单与受信代理要一起维护：

1. **前端重建**：`--build-arg VITE_API_BASE_URL=https://api.example.com/api --build-arg VITE_OZID_ORIGIN=https://id.example.com`（都无默认值，缺失或不合法构建失败）；运行时 `API_ORIGIN`、`ADMIN_HOST`、`ADMIN_ALLOWED_CIDRS` 必填，空值或全开的白名单使容器拒绝启动；`ADMIN_TRUSTED_PROXIES`（前置代理网段）可选，且不得与 `ADMIN_ALLOWED_CIDRS` 重叠（BFF 的 `admin.trusted_proxies` 与 `admin.allowed_cidrs` 同理），重叠的组合启动即被拒绝。
2. **BFF 配置**：`admin.allowed_cidrs`（空 = 全部拒绝）、`admin.trusted_proxies`；`navigation.apps.admin`（`origin`、`paths: ["/users", "/users/*", "/audit/users"]`）；控制台 origin 加入 `cors_allowed_origins`，且与 API 同站点。
3. **反代与 DNS/TLS**：见 `deploy/reverse-proxy/Caddyfile.example` 的 `admin.example.com` 站点：代理是第一个看到真实客户端的跳点，用对端地址**覆盖** `X-Forwarded-For`（不透传客户端的值），并列入后两层的受信代理。前面如果还有 CDN/负载均衡，它们的网段也属于受信链，必须同时配置，否则真实来源会被误判。
4. **授予平台角色**：`usersrv-admin grant-role -role ozid-admin ...`（先于上线）。usersrv 迁移 `20261025120000_platform_admin_role.sql` 随 usersrv 上线。
5. **上线顺序与回滚**：usersrv → BFF → 网关与 `admin-web`；回滚先下线控制台，再回滚 BFF；回滚迁移前先撤销所有 `ozid-admin`。

镜像发布与 CI 不会自动部署、不会自动开放白名单，也不清理数据；运维仍须自行确认真实 DNS、证书与 TLS、真实来源地址与白名单取值、代理/CDN 链路和邮件厂商。详见 [控制台功能描述](admin/features.md)。
