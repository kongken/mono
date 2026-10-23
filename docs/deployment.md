# 部署与发布

本文描述**当前已实现**的同源代理与 HTTPS 同站点多 Web App/BFF 拓扑、密钥与 TLS 布局、健康探针和运维轮换步骤。真实 Logto OAuth、SMTP、Web Push、生产 S3 策略与社交平台抓取在本仓库 CI 中**未**端到端验收；浏览器真实栈 Playwright 见 [BFF 验证](bff.md#验证)（默认跳过，需本地 `make dev` 与 `E2E_REAL_BFF=1`）。

## 拓扑

```
浏览器 ──HTTPS──► 边缘（Nginx 前端镜像 / Caddy 等）
                      ├─ 静态 SPA（/、/assets、/maps）
                      └─ /auth/、/api（含 /api/tiles/）、/ping、/readyz、/s/、/api/share/、/api/notifications/ ──► BFF :18081
                                                                                              │
                                                                                              ├─ PostgreSQL（BFF 认证库）
                                                                                              ├─ Redis（限流、OAuth state、CSRF）
                                                                                              └─ TLS gRPC ──► tripmap :18083（内网）
                                                                                                    ├─ PostgreSQL（业务）
                                                                                                    ├─ Redis
                                                                                                    └─ S3
```

- **公网**只暴露边缘 HTTPS 与 BFF 的 HTTP 端口（经边缘反代）；**不得**把 tripmap 的 `18082`（HTTP `/ping`/`/readyz`）或 `18083`（gRPC）直接映射到公网。
- 前端构建需 `--build-arg VITE_API_BASE_URL=/api`，使浏览器与 BFF 同源；纯静态托管不设置该变量时仍为本地模式。
- 示例文件：`deploy/trip-map-web/default.conf.template`（前端 Nginx）、`deploy/reverse-proxy/Caddyfile.example`（单站点 Caddy）、`deploy/compose.stack.example.yaml`（内网隔离的 Compose 栈）、`deploy/k8s/secrets.layout.example.yaml`（Secret 键名示意）。

## 受信代理与客户端地址

- 边缘反代必须设置 **`X-Real-IP`**（客户端地址）和 **`X-Forwarded-Proto`**（`https`）；BFF 按 `client_ip_header`（生产建议 `X-Real-IP`）解析地址，并**覆盖**转发给 tripmap 的 `X-Real-IP`，客户端无法自选地址。
- tripmap `auth.client_ip_header: X-Real-IP`（见 `config.tripmap.example.yaml`），用于分享/退订限流；BFF 之后的服务不要信任浏览器直连。
- 同源部署通常**不需要** `cors_allowed_origins`；跨站 API 时在 BFF 配置精确来源列表。
- **不要**在边缘剥离 `Connect-Protocol-Version`、`Content-Type`、`Cookie`、`X-CSRF-Token` 或 Connect 相关头。

## 请求体大小

合法业务载荷上限为 **16 MiB**（`grpcx.LegitimatePayloadBytes`）；BFF Connect 读上限与此一致，内部 gRPC 与边缘默认 **20 MiB**（含序列化与 framing 余量）。前端 Nginx 模板 `client_max_body_size 20m`；Caddy 示例 `max_size 20MB`。调小任一层都会导致批量同步或媒体上传失败。

## 安全响应头

前端 Nginx 模板已添加 `X-Content-Type-Options`、`X-Frame-Options`、`Referrer-Policy`、`Permissions-Policy`。Caddy 示例在站点级设置同类头。分享/退订路径仍不写访问日志（令牌在路径或查询中），见 [公开分享页](trip-map/sharing.md#配置与部署)。

## TLS 与内部凭证 Secret

| 材料 | 归属 | 用途 |
| --- | --- | --- |
| `tripmap.pem` / `tripmap.key` | tripmap Pod/容器 | gRPC TLS 服务端证书（`grpc.cert_file` / `grpc.key_file`）；仅 `tls` 模式需要，`h2c` 不挂载 |
| `ca.pem` | BFF | 校验 tripmap 证书（`grpc.ca_file`）；仅存在 `tls` 上游时需要，全 `h2c` 可不挂载 |
| `bff.key`（Ed25519 PKCS#8） | **仅 BFF** | 签发内部 JWS（`internal_auth.private_key_file`） |
| `bff.pub`（PKIX） | tripmap（及轮换期多副本） | `internal_auth.trusted_keys[].public_key_file` |

Ed25519 私钥 / 公钥在 `tls` 与 `h2c` 两种内部传输下**都必需**：h2c 只去掉传输层的证书 / CA，不去掉应用层签名。上表展示文件方式；也可将 PEM 分别放入 BFF `internal_auth.private_key`、tripmap `internal_auth.trusted_keys[].public_key`，替换对应 `*_key_file`，不再要求独立 key 文件或签名密钥挂载。来源必须二选一，完整格式与保密要求见 [配置内联签名密钥](bff.md#配置内联签名密钥)。含私钥的配置必须按 Secret 管理，不可放入公开 ConfigMap；现有 Deployment 的文件挂载不会自动移除，需由部署方另行同步修改。

### 内部 gRPC 传输（tls / h2c）

默认且推荐 `tls`（TLS 1.3 单向，BFF 校验服务证书；不是 mTLS）。可信内网可在业务服务设置 `grpc.transport: h2c`、BFF 对应上游设置 `grpc.services.<名称>.transport: h2c` 以免证书依赖；配置示例、两端必须一致与 TLS → h2c 手动切换步骤见 [BFF 内部 gRPC 与凭证](bff.md#内部-grpc-与凭证)和[配置](bff.md#配置)。

- h2c 没有加密、服务端身份验证和完整性保护，被动监听、内容篡改与 60 秒窗口内的凭证重放仍可能发生，**不与 TLS 等价**；只用于经显式批准的可信内部网络，是否需要网络隔离或由代理 / 网格承担加密由部署方评估。
- 业务 gRPC 端口（tripmap `18083`）不得通过公网 Ingress、NodePort 或 LoadBalancer 暴露；本项目不自动修改集群网络策略，也不会因升级而改变现有部署的传输（未配置即 `tls`）。
- 两端模式不一致时，BFF `/readyz` 的 `{name}_grpc` 为未就绪，浏览器看到 `UNAVAILABLE`；不会自动回退到另一种传输。

本地示例显式使用 loopback h2c：`make dev-secrets` 仅生成签名密钥对，写入被 Git 忽略的 `dev-secrets/`（`kid` 为 `dev-1`）。本地 TLS 另用 `go run ./cmd/dev-secrets -tls` 并同步修改两端配置；已有配置不会自动切换。生产 TLS 用 CA 签发 tripmap 证书（SAN 含服务 DNS 名），签名私钥与公钥分别通过 Secret 文件或各自的 Secret 配置提供；文件布局示例见 `deploy/k8s/secrets.layout.example.yaml`。

### Ed25519 签名密钥轮换

1. 生成新密钥对，在**每个** tripmap（及未来业务服务）的 `internal_auth.trusted_keys` **追加**新 `kid` 与 `bff.pub`（新旧公钥并存）。
2. 滚动重启 tripmap，确认 `/readyz` 与 BFF `/readyz` 中 `tripmap_grpc` 正常。
3. 更新 BFF Secret 中的私钥，并把 BFF `internal_auth.kid` 与私钥来源（`private_key` 或 `private_key_file`，二选一）切到新密钥；滚动重启 BFF。
4. 等待旧凭证最长有效期（**60 秒**）加允许时钟偏差（默认 **10 秒**，`internal_auth.clock_skew`）后再从 `trusted_keys` 移除旧公钥。
5. 仍用已撤销私钥签名的调用会被 tripmap 拒绝，浏览器侧表现为 `INTERNAL`。

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
| `GET /ping` | BFF、tripmap | 进程存活 |
| `GET /readyz` | tripmap | `postgres`、`redis`、`s3`（各 2 秒超时） |
| `GET /readyz` | BFF | 各 upstream HTTP `/readyz`（如 `tripmap`）、`bff_postgres`、`bff_redis`、各服务 **`{name}_grpc`**（按该上游的传输 tls / h2c，用签名凭证调用 `SystemService/GetStatus`） |

BFF 启动**不要求** tripmap 已就绪；编排系统应使用 BFF `/readyz` 作为对外就绪门。前端 `/healthz` 仅表示静态文件服务。

## 独立镜像与 import 边界

tripmap、BFF、前端为三个镜像（见 [Docker 镜像](docker-images.md)）。Dockerfile 只 `COPY` 本服务业务目录与 `internal/platform/`；`go test ./internal/platform/moduleboundary`（`make check-import-boundaries`）断言 `cmd/bff` 不依赖 `internal/tripmap`、`cmd/tripmap` 不依赖 `internal/bff`。

## Logto-only 身份迁移

本项目不迁移旧密码账号或测试云端数据。备份数据库并暂停登录与业务写入后，按以下顺序操作；所有清理 SQL 均由运维手动执行，不由服务启动调用：

1. **BFF 数据库**：按版本顺序应用尚未执行的迁移，包括 `migrations/bff/20261023095053_logto_identity_mapping.sql`。它只新增 `auth_identities`、`auth_browser_sessions`，不复制旧账号或会话；旧 Cookie 会话失效。
2. 部署所有 BFF、tripmap 与前端实例。移除旧 provider 配置及 `registration_disabled`，仅配置 Logto；确认没有旧版本副本后才清理列，旧 Ent 客户端依赖它们。
3. **tripmap 业务数据库（可选，且必须在开放新登录前）**：若整库业务数据均可丢弃，手动执行 `deploy/sql/tripmap/reset_test_profiles.sql`，删除旧测试 profile 及其级联业务数据。它不会清理 S3 实体对象；如需物理删除，应在删除元数据之前单独核对、备份并清理测试对象，不能影响其他应用对象。开放新登录后不要运行此脚本，否则新用户数据也会删除。
4. **tripmap 业务数据库**：手动执行 `deploy/sql/tripmap/post_import_drop_legacy_auth_columns.sql`，删除认证遗留列及旧用户名唯一索引。文件名仅为历史兼容，不需要账号导入。此步骤在开放新登录前完成，避免不同 Logto subject 的相同资料名发生冲突；保留业务 `users` 表及所有业务结构。
5. 验证 Logto 登录、`Me`、业务读写、退出、会话撤销和删除云端数据后，**BFF 数据库**可手动执行 `deploy/sql/bff/retire_password_accounts.sql`，仅删除旧 `auth_sessions` / `auth_users`。保留新身份映射、浏览器会话及 `account_deletion_receipts`。
6. 恢复流量。清理后不能直接回滚至旧版本；需要恢复数据库备份。以上操作不连接或修改 **Logto 数据库**，也不会删除 Logto 用户。

退出仅撤销当前 BFF 会话，不执行 Logto 全局退出。Logto 远端禁用、删除账号或修改资料尚不会实时撤销/刷新既有 BFF 会话；资料在再次登录时刷新，会话依本地过期和撤销规则处理。最近 15 分钟的 BFF 会话不代表 Logto 强制重新输入凭据（SSO 可能直接完成登录）。

## 验证清单

- `make dev-secrets`、`make deps-up`、`make storage-init`、`make dev` 本地 smoke。
- `make build`、`make test`、`make check-import-boundaries`、`buf lint`、`npm run build`。
- 有隔离存储时 `make test-integration`。
- 文档链接：`make check-docs`。

## 多前端共用 BFF 的部署（#54）

完整配置、回调迁移与验证命令见 [BFF 多 Web App 登录](bff.md#多-web-app-登录与迁移54)。公网 API 域名同时提供 `/api/bff.*`、`/auth/login` 与 `/auth/callback/*`；OAuth callback 只由 BFF 处理，不回退 SPA。所有网关禁止记录回调查询串；Nginx 示例已覆盖 `/auth/`。不更改 DNS、Ingress 或生产资源的自动配置。

多个 HTTPS 同站点应用分别构建 `VITE_API_BASE_URL=https://api.example.com/api` 与自身 `VITE_OAUTH_APP_ID`；BFF 增加两个精确 app origin/落点和 CORS origins，生产 `cookie_secure: true`，关闭开发例外。Logto 登记固定 `https://api.example.com/auth/callback/logto`；新增 app 不改变该 URI。API 主机保存 host-only Cookie，应用用 credentials include 和 CSRF 响应体 token 调用；不要扩大 Domain。任意跨站点及第三方 Cookie 限制不属于支持承诺。

同源 `/api` 模式仍须同时代理 `/auth/`，callback 和 API 的浏览器主机一致。独立前端代理会得到各自的 Cookie，不能与直连共享 BFF 主机会话混为一谈。`client_ip_header` 只信任已覆盖客户端同名头的网关；BFF 不按 Host/Referer 推导可信回调或落点。当前退出撤销 BFF 会话，不注销 Logto；删除云端数据也不删除 Logto 账号。旧测试账号和业务数据按迁移说明处理，不自动导入或清空。
