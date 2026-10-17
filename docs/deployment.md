# 部署与发布

本文描述**当前已实现**的同源 Web/BFF 拓扑、密钥与 TLS 布局、健康探针和运维轮换步骤。真实 GitHub/Google/Logto OAuth、SMTP、Web Push、生产 S3 策略与社交平台抓取在本仓库 CI 中**未**端到端验收；浏览器真实栈 Playwright 见 [BFF 验证](bff.md#验证)（默认跳过，需本地 `make dev` 与 `E2E_REAL_BFF=1`）。

## 拓扑

```
浏览器 ──HTTPS──► 边缘（Nginx 前端镜像 / Caddy 等）
                      ├─ 静态 SPA（/、/assets、/maps）
                      └─ /api、/ping、/readyz、/s/、/api/share/、/api/notifications/ ──► BFF :18081
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
- 示例文件：`deploy/frontend/default.conf.template`（前端 Nginx）、`deploy/reverse-proxy/Caddyfile.example`（单站点 Caddy）、`deploy/compose.stack.example.yaml`（内网隔离的 Compose 栈）、`deploy/k8s/secrets.layout.example.yaml`（Secret 键名示意）。

## 受信代理与客户端地址

- 边缘反代必须设置 **`X-Real-IP`**（客户端地址）和 **`X-Forwarded-Proto`**（`https`）；BFF 按 `client_ip_header`（生产建议 `X-Real-IP`）解析地址，并**覆盖**转发给 tripmap 的 `X-Real-IP`，客户端无法自选地址。
- tripmap `auth.client_ip_header: X-Real-IP`（见 `config.example.yaml`），用于分享/退订限流；BFF 之后的服务不要信任浏览器直连。
- 同源部署通常**不需要** `cors_allowed_origins`；跨站 API 时在 BFF 配置精确来源列表。
- **不要**在边缘剥离 `Connect-Protocol-Version`、`Content-Type`、`Cookie`、`X-CSRF-Token` 或 Connect 相关头。

## 请求体大小

合法业务载荷上限为 **16 MiB**（`grpcx.LegitimatePayloadBytes`）；BFF Connect 读上限与此一致，内部 gRPC 与边缘默认 **20 MiB**（含序列化与 framing 余量）。前端 Nginx 模板 `client_max_body_size 20m`；Caddy 示例 `max_size 20MB`。调小任一层都会导致批量同步或媒体上传失败。

## 安全响应头

前端 Nginx 模板已添加 `X-Content-Type-Options`、`X-Frame-Options`、`Referrer-Policy`、`Permissions-Policy`。Caddy 示例在站点级设置同类头。分享/退订路径仍不写访问日志（令牌在路径或查询中），见 [公开分享页](sharing.md#配置与部署)。

## TLS 与内部凭证 Secret

| 材料 | 归属 | 用途 |
| --- | --- | --- |
| `tripmap.pem` / `tripmap.key` | tripmap Pod/容器 | gRPC TLS 服务端证书（`grpc.cert_file` / `grpc.key_file`） |
| `ca.pem` | BFF | 校验 tripmap 证书（`grpc.ca_file`） |
| `bff.key`（Ed25519 PKCS#8） | **仅 BFF** | 签发内部 JWS（`internal_auth.private_key_file`） |
| `bff.pub`（PKIX） | tripmap（及轮换期多副本） | `internal_auth.trusted_keys[].public_key_file` |

本地开发：`make dev-secrets` 写入被 Git 忽略的 `dev-secrets/`（`kid` 为 `dev-1`，与示例 YAML 一致）。生产用 CA 签发 tripmap 证书（SAN 含服务 DNS 名），私钥与公钥分 Secret 挂载；示例键名见 `deploy/k8s/secrets.layout.example.yaml`。

### Ed25519 签名密钥轮换

1. 生成新密钥对，在**每个** tripmap（及未来业务服务）的 `internal_auth.trusted_keys` **追加**新 `kid` 与 `bff.pub`（新旧公钥并存）。
2. 滚动重启 tripmap，确认 `/readyz` 与 BFF `/readyz` 中 `tripmap_grpc` 正常。
3. 更新 BFF Secret 中的 `bff.key`，并把 BFF `internal_auth.kid` 与 `private_key_file` 指向新密钥；滚动重启 BFF。
4. 等待旧凭证最长有效期（**60 秒**）加允许时钟偏差（默认 **10 秒**，`internal_auth.clock_skew`）后再从 `trusted_keys` 移除旧公钥。
5. 仍用已撤销私钥签名的调用会被 tripmap 拒绝，浏览器侧表现为 `INTERNAL`。

## 健康检查

| 探针 | 位置 | 依赖 |
| --- | --- | --- |
| `GET /ping` | BFF、tripmap | 进程存活 |
| `GET /readyz` | tripmap | `postgres`、`redis`、`s3`（各 2 秒超时） |
| `GET /readyz` | BFF | 各 upstream HTTP `/readyz`（如 `tripmap`）、`bff_postgres`、`bff_redis`、各服务 **`{name}_grpc`**（经 TLS + 签名凭证调用 `SystemService/GetStatus`） |

BFF 启动**不要求** tripmap 已就绪；编排系统应使用 BFF `/readyz` 作为对外就绪门。前端 `/healthz` 仅表示静态文件服务。

## 独立镜像与 import 边界

tripmap、BFF、前端为三个镜像（见 [Docker 镜像](docker-images.md)）。Dockerfile 只 `COPY` 本服务业务目录与 `internal/platform/`；`go test ./internal/platform/moduleboundary`（`make check-import-boundaries`）断言 `cmd/bff` 不依赖 `internal/tripmap`、`cmd/tripmap` 不依赖 `internal/bff`。

## Legacy 账号导入（#44）

自 tripmap 迁出认证后，已有用户需导入 BFF 库（不默认删 tripmap 数据）：

1. 应用 `migrations/tripmap/` 与 `migrations/bff/` 版本化迁移（关闭生产 `auto_migrate`）。
2. `bin/bff-import-legacy-accounts -tripmap-dsn … -bff-dsn … --dry-run`，确认报告后去掉 `--dry-run` 执行。
3. 抽查同一 `user_id` 可密码/OAuth 登录，`EnsureProfile` 业务列完整。
4. 可选：确认无遗留登录依赖后，DBA 执行 `deploy/sql/tripmap/post_import_drop_legacy_auth_columns.sql`（**不在** atlas 迁移中）。

## 验证清单

- `make dev-secrets`、`make deps-up`、`make storage-init`、`make dev` 本地 smoke。
- `make build`、`make test`、`make check-import-boundaries`、`buf lint`、`npm run build`。
- 有隔离存储时 `make test-integration`。
- 文档链接：`make check-docs`。
