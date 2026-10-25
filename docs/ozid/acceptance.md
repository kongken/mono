# OzID 与统一管理控制台：整体验收（T12）

[规格](spec.md) §14 的 26 项验收场景如何落到实现与测试，最终的来源策略，旧邮件过渡清单，以及哪些验证是自动化做过的、哪些还要运维做。只使用保留示例名称（`example.com`、`*.test`、`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`、`2001:db8::/32`、回环地址）：真实域名、来源 IP 和凭据由私有部署配置提供，不写入 Issue、PR、文档或示例。

## 真实网关与浏览器的验收场景（`TestGatewayDomainSets`）

`internal/e2e/gateway_*_test.go` 与 `e2e/gateway/` 把整套栈（OzID、控制台、trip-map、metro 的构建产物，真实 BFF、usersrv、metro、tripmap，PostgreSQL、Redis）放在**真实网关**之后，对**两组完全不同的保留同站点域名**各跑一遍（`alpha-e2e.test` 的 `id/api/trip/metro/admin`，`bravo-e2e.test` 的 `accounts/edge/footprints/rails/ops`；没有任何源码、模板或示例依赖其中一组的名字）：

- **Caddy**：运行 `deploy/reverse-proxy/Caddyfile.example` 本身。只做文本替换（站点名、上游地址与监听端口、静态文件根目录、一个本机无法自带的“网关地址”：Caddy 用 `127.0.0.2` 拨上游，客户端是 `127.0.0.1`）；每处替换在目标不在示例里时让测试失败，所以改动示例不会悄悄变成“测的已不是示例”。
- **Nginx**：运行 `deploy/ozid-web`、`deploy/admin-web` 的模板，用真实 `envsubst` 与各 Dockerfile 的 `NGINX_ENVSUBST_FILTER` 渲染，并执行镜像自己的入口脚本（`19-require-api-origin.sh`、`19-admin-access.sh`，所以空白名单、重叠的受信代理在这里同样是启动失败）。产品镜像的模板（`trip-map-web`、`metro-web`）也在裸端口上单独探测。
- **三个 Caddy 实例**：只放行测试内网客户端的白名单实例；示例原样的实例（`203.0.113.0/24`，不含任何真实网络，所以控制台对谁都关闭）；去掉网关自己的控制台规则的实例（让容器和 BFF 各自独立证明拒绝）。
- **三类来源**：`127.0.0.1` 是白名单内的浏览器，`127.0.0.4` 是 MCP 机器，`127.0.0.9` 是外部；网关是 BFF 与控制台容器的受信代理（`127.0.0.2/32`），不在任何白名单内。
- **浏览器**：Chromium（桌面与手机视口各 15 个场景，名单外浏览器 3 个，每组 25 个）经 `--host-resolver-rules` 把整个保留站点解析到网关端口，只忽略测试用自签证书的错误，其他不放宽。

Go 探测证明的是**哪一层**拒绝：网关拒绝的请求不得出现在 BFF 的 HTTP 边缘（观察器计数为 0）；去掉网关规则后由 BFF 拒绝的请求只能是 403/404，并且 metro 管理服务的调用计数不变；MCP 被拒的请求不改城市数据、不使用令牌。

| 探测 | 内容 |
| --- | --- |
| 公开边缘 | OzID 的 CSP/落地页头与 `ozid-web` 模板逐字一致；产品站点的旧入口 `/auth/*` 与同源 `/api/*`（含全部管理、账号写、开放过程）一律 404 且不到 BFF；`/ping` 仍转发；产品落地页头；每个构建产物只含本组的 API origin，不含另一组的站点名，也没有管理代码 |
| 控制台三层 | 白名单内浏览器可用；名单外：网关 403 空体；去掉网关规则：容器 403 `forbidden`；再绕过容器：BFF 403。示例原样的网关对内外一律 403 |
| 绕过 | 13 种伪造转发头（XFF 各种链、X-Real-IP、Forwarded、X-Forwarded-Host…）× 多种路径写法（`//`、编码点段、`%00`…）；OPTIONS/POST/PUT/DELETE/HEAD/PATCH；API 主机不服务控制台、MCP 与静态文件；CORS 预检只回应四个登记 origin，未登记兄弟、外站、http 降级、端口、`null`、另一组域名都得不到任何 CORS 头；未知 Host、另一组（旧）域名、Host 与 SNI 不一致都没有内容也到不了 BFF；控制台容器与 BFF 的裸端口在外部来源 + 伪造头下仍 403，Host 不符时容器直接断开 |
| 来源策略 | 产品 Origin 与未登记兄弟调用集中账号写（登录、注册、重置、改密、设备、注销…）一律 403；Me、CSRF、退出对四个登记应用仍然可用；管理过程换成任何非控制台 Origin 都 403 |
| MCP | 令牌经网关由控制台会话创建；机器网段走通整套协议；浏览器地址、外部地址、伪造 XFF、Forwarded、X-Real-IP、会话 Cookie 一律 403 `request rejected`，缺/错令牌 401，Cookie 不能代替令牌，其他主机不承载 MCP |

浏览器场景：`flows.spec.ts`（metro 匿名标记 → 用户中心登录 → 回到原页 → 标记保存，另一台设备读回；trip-map 登录 → 添加足迹 → 另一浏览器从服务端读回；一次登录两个产品同时可见、在用户中心退出后两个产品都失效；邮件落到用户中心并在另一浏览器完成，令牌不进请求 URL 与 Referer；旧邮件落到产品页并在过渡期内完成；换域名后上一组的会话在新 API 主机无效）、`console.spec.ts`（登录与角色三种身份、用户中心页面不能调用管理过程）、`outside.spec.ts`（名单外的浏览器：控制台全部 403 空体，用户中心和两个产品照常）。

- **变异检查**：把外部来源地址加进 BFF 的控制台白名单，探测失败（被抓到）。把白名单放宽到覆盖受信代理的网段则会被配置校验直接拒绝启动，这本身也是一道保护。
- **CI**：`gateway-browser` 任务（`.github/workflows/ci.yml`）用 `-v` 运行，要求所有子测试 `--- PASS`、没有任何 `--- SKIP`、两组各自的 Playwright 报告恰好是 `25 passed` 且没有 failed/skipped/flaky/interrupted/did not run，日志与失败时的 trace、截图、视频作为制品保留。缺 nginx、caddy、envsubst 或 Chromium 是失败，不是跳过。
- **本地的限制**：本机没有 Docker，镜像构建与 `scripts/*-web-smoke.sh` 只在 CI 的 `*-web-image` 任务里运行；本机用 nginx 1.28.1 与 Caddy v2.11.7 实测，CI 用发行版的 nginx 包，版本可能不同（入口脚本、`real_ip` 与 `allow/deny` 的行为稳定，但这一差别没有在本机验证）。

## #77 验收项 → 实现与测试

| # | 实现 | 测试（自动化） |
| --- | --- | --- |
| 1 两组域名、构建参数真的生效 | 各 Dockerfile 的 build args、`navigation`/`cors_allowed_origins`/usersrv `auth.apps.ozid`、`deploy/*` 模板与示例；无域名写入源码 | `TestGatewayDomainSets`（两组、真实网关）；`TestPublicFilesUseOnlyReservedNamesAndNoCredentials`；镜像 `*-web-smoke.sh`（CI） |
| 2 Cookie/HTTPS/无令牌 | `session` 的 host-only Cookie；产品与 OzID 直连 API origin | `TestGatewayDomainSets`（Cookie 属性与存储）、`TestOzidBrowserChain`、`TestTripMapBrowserChain`、`TestMetroBrowserChain`、`session_test` |
| 3 A 发起 OzID 完成，B 看到同一身份 | 导航注册表、`Me`、共享 Cookie | `TestGatewayDomainSets`（一次登录两个产品）、三个产品的真实栈 Playwright |
| 4 未登记兄弟/外站/伪造来源与返回地址 | 精确 CORS、`navigation` 校验、Origin/CSRF | `TestChainOzidRefusesUnregisteredSameSiteOrigins`、`navigation_test`、网关的预检探测、`TestChainCSRFAndOriginRejected` |
| 5 直接访问 OzID、无循环 | OzID 路由与重新登录页 | `ozid.spec.ts`（模拟 BFF）、`TestChainOzidSignInAndSafeReturn`、OzID 真实栈 |
| 6 邮件只落 OzID，另一浏览器完成 | usersrv `auth.apps.ozid`、fragment 令牌、落地页头 | `TestChainOzidRegistrationMailLoop`、`TestChainOzidPasswordResetMailLoop`、网关 `flows.spec.ts` 邮件两个场景（头来自真实 Nginx 经真实 Caddy） |
| 7 换 API 主机重新登录、旧邮件 TTL、配置错配 | 本文“旧邮件过渡清单”、`auth.legacy_mail_transition`、启动校验 | `TestChainLegacyMailTransitionIsNarrowAndEnds`、`account_origin_test`、`config_test`、网关“换域名”场景、`TestRealMailStackConfigsAgree` |
| 8 产品无账号表单、产品 Origin 不能调账号写 | `account_origin.go`（来源策略）、产品只留入口 | `TestNoOldAccountFormsOrManagementUIInProducts`、`TestChainAccountProceduresTakeOnlyTheUserCenterOrigin`、`TestChainRegisteredApplicationsKeepIdentityAndSignOut`、`TestEveryBrowserServiceHasASourceRule`、网关来源策略探测；产品各自的账号入口 Playwright |
| 9 退出与退出其他设备 | usersrv 会话撤销、`RevokeOtherSessions` | `device_sessions_chain_test.go`、`TestChainLoginAfterLogout`、网关“退出后两个产品都失效” |
| 10 改密/重置结束全部会话与令牌 | `auth_version` | `TestChainChangePasswordEndsOnlyThisAccountsAccess`、`TestChainPasswordResetEndsEverySession`、`TestChainMCPTokenIsRevalidatedOnEveryCall` |
| 11 trip-map 未同步修改、A/B 切换 | 足迹引擎队列与 `X-Kongken-User` | trip-map 真实栈 Playwright、`TestTripMapBrowserChain`；网关场景只验证登录后跨设备读回 |
| 12 账号注销与恢复 | 注销工作流 | `ozid_close_account*_test.go`、`TestOzidBrowserChain` |
| 13 资料与设备页 | OzID 页面 | `ozid-account.spec.ts`、`device_sessions_chain_test.go`、OzID 真实栈 |
| 14 名单外：控制台与管理 API 拒绝，公共功能可用 | `adminguard`、`admin-web` 白名单、空名单关闭 | 网关“控制台三层”探测与 `outside.spec.ts`、`TestChainUserAdminSourcePolicy`、`TestAdminBrowserChain` |
| 15 绕过尝试 | 同上，受信代理链 | 网关“绕过”探测、`adminguard` 单测、`TestChainMetroAdminPerimeterPreflightAndPaths` |
| 16 浏览器与 MCP 名单独立 | `mcp.*` 独立周界 | 网关 MCP 探测、`TestChainMCPSourceAllowlist`、`TestChainMCPAndBrowserAllowlistsAreIndependent`、`TestChainMCPAllowedAddressStillNeedsAValidTokenAndScope` |
| 17 角色矩阵 | `required_role` 双层校验 | `TestChainUserAdminRoleMatrix`、`TestChainMetroAdminRolesAndReauthentication`、控制台真实栈、网关 `console.spec.ts` |
| 18 管理只接受控制台 Origin | `adminguard` 的 Origin 规则 | `TestChainUserAdminDemandsTheConsoleOrigin`、`TestChainMetroAdminPerimeterDemandsTheConsoleOrigin`、网关 Origin 探测与“用户中心页面不能调用管理过程” |
| 19–22 管理写入保护、认证时效、审计 | usersrv `ApplyAdminChange`、15 分钟密码认证 | `user_admin_write_chain_test.go`、`user_admin_audit_chain_test.go`、`TestChainUserAdminNeedsARecentPassword` |
| 23–25 metro 管理迁入、令牌、旧 /admin 退役 | 控制台 metro 分区 | `metro_admin_*_chain_test.go`、`metro_mcp_*_chain_test.go`、`apps/admin/tests/metro.spec.ts`、`TestNoOldAccountFormsOrManagementUIInProducts`、网关构建产物扫描（产品与 OzID 的产物不含管理代码） |
| 26 真实网关、静态回退、安全头、默认关闭 | `Caddyfile.example`、各 Nginx 模板 | `TestGatewayDomainSets`（示例原样的实例：名单为文档网段，对谁都 403）；`scripts/admin-web-smoke.sh`（CI）；`19-admin-access.sh` 拒绝空白名单 |

每张票原有的测试都保留；本阶段的全量运行见下文“验证结果”。

## 最终来源策略

| 过程 | 允许的来源 |
| --- | --- |
| `AuthService`：`Me`、`IssueCSRFToken`、`ResolveNavigation`、`Logout` | 任一已登记应用（精确 CORS 列表）；这四个是产品需要的全部账号能力，`TestAccountOpenProceduresAreTheMinimalFour` 守护 |
| `AuthService`、`UserService` 的其余过程（登录、注册、重置、重新认证、改密、资料、设备与会话、注销及其状态查询，含读方法） | **只有** `navigation.apps.ozid.origin`（`Origin` 必须恰好一个且相等）。例外：`CompleteRegistration`、`ResetPassword` 在 `auth.legacy_mail_transition.origins`（已登记的产品 origin）内、`until` 之前也接受。没有注册表时策略关闭（启动警告） |
| `UserAdminService`、`AccessTokenService`、metro `AdminService` | 控制台 origin + `admin.allowed_cidrs`（经 `admin.trusted_proxies` 解析）+ 角色；空名单关闭 |
| `bff.tripmap.*`、`bff.metro.*`（除 `AdminService`） | 已登记产品的 origin（CORS）+ 会话与 CSRF |
| `POST /mcp/metro` | Bearer 令牌 + `mcp.allowed_cidrs`（经 `mcp.trusted_proxies`）；不设 CORS；空名单关闭 |
| 公开 HTTP（`/s/*`、`/api/share/*`、`/api/notifications/*`、`/api/tiles/*`、`/ping`、`/readyz`） | 无会话；产品站点只代理这几类，其余同源 `/api` 一律 404 |

`TestEveryBrowserServiceHasASourceRule` 枚举注册表里的所有浏览器服务：新增的服务不在账号、管理、产品三份列表之一就失败。

## 旧邮件 TTL 过渡清单

新邮件始终链接到中央 OzID（usersrv `auth.apps.ozid.origin`）。切换前已发出或已排队的邮件仍指向产品自己的 `/account/verify-email`、`/account/reset-password`（产品里保留落地页，只服务这些旧邮件），令牌有效期为 usersrv 的 `registration_ttl`（默认 30m）与 `reset_ttl`（默认 15m）。

1. **切换日之前**：usersrv `auth.apps.ozid.origin` 与 BFF `navigation.apps.ozid.origin` 是同一个 OzID origin（配置错配只会让邮件链接落到别处，不会放行任何来源）。
2. **切换日 T0**：BFF 配置 `auth.legacy_mail_transition`：`origins` 填仍在发旧邮件的产品 origin，`until` 填 `T0 + max(registration_ttl, reset_ttl) + 队列与重试余量`（RFC 3339，加载时最多 7 天，更长则 BFF 拒绝启动）。usersrv 的产品条目（`auth.apps.trip-map`、`auth.apps.metro`）保留到同一时刻。边缘对产品落地页的三条规则（`no-referrer`、`no-store`、`script-src 'self'`，Caddy 示例与 Nginx 模板都有）保持不变。
3. **T0 到 `until`**：旧邮件在产品页完成注册/重置（只有这两个过程被接受），完成后“去登录”指向 OzID。BFF 启动时写一条警告说明过渡仍在进行。
4. **`until` 之后**：BFF 自动不再接受这些 origin（仍能启动，写警告提示删除该配置段），旧邮件的落地页打开后完成会被拒绝，用户重新申请（忘记密码/注册）。随后删除 `legacy_mail_transition` 段与 usersrv 里的产品邮件条目并重启。没有通配 CORS，也没有泄露 fragment 的重定向器。
5. **检查**：`TestChainLegacyMailTransitionIsNarrowAndEnds` 在真实链路上证明：控制台与未登记兄弟被拒，产品 origin 只能调这两个过程，`until` 之后被拒；`account_origin_test`、`config_test` 覆盖加载校验与过期告警。

## 验证结果

本阶段在本机（隔离的 PostgreSQL 与 Redis，无 Docker）实际运行：

- `go vet`、`gofmt`、`go test ./internal/e2e ./internal/bff/...`（含 `OZID_BROWSER_E2E`、`ADMIN_BROWSER_E2E`、`TRIPMAP_BROWSER_E2E`、`METRO_BROWSER_E2E` 四个浏览器链）；
- `GATEWAY_E2E=1 go test ./internal/e2e -run TestGatewayDomainSets`：两组各 25 个浏览器场景全部通过、无跳过，三个 Go 探测子测试全部通过；
- `npx oxlint`、`make check-docs`。

### 已定位并修复的历史失败

- T11 的 CI 失败 `395b73e`（按钮缺 `type=button`，`dc0512c` 修复）、`cf3e6d7`（断言过早，`8238040` 修复）和 `fb` 竞态（`376437d` 修复）。
- 本阶段：启用来源策略后许多链路测试得到 `permission_denied: request rejected`（测试以产品或默认 Origin 调账号写；已按真实调用者改为 OzID Origin，或明确走产品 Origin 以验证被拒）；`TestLegitimatePayloadLimitsAreConsistent` 在产品镜像不再代理 `/api/` 后失败（原先它检查产品 Nginx 的 `client_max_body_size`，现改为检查唯一承载同步批量的 API 站点的 `max_size ≥ 16 MiB`，并检查产品镜像对 `/api/` 返回 404）；网关场景首次运行的失败都是断言写错或页面状态理解有误（`/auth` 无尾斜线本就落到 SPA、Caddy 对不匹配的 Host 返回空 200、浏览器对无 CORS 头的 403 只报告请求失败），已改成与真实行为一致且不放宽拒绝的断言。

### 现存的本地失败

无（上述全量运行中没有失败项）。T11 期间有一次本地偶发、没有日志的失败至今**没有定位**；本阶段的运行中没有再出现，但不能据此说它已修复。

### 仍需运维验证（自动化无法覆盖）

- 真实 DNS、公共可信证书与 TLS 终止、真实 CDN/负载均衡链路（其网段必须同时列入 `admin.trusted_proxies`、`mcp.trusted_proxies`、`ADMIN_TRUSTED_PROXIES`，并让 Caddy 的 `trusted_proxies` 全局选项一致）；
- 真实客户端来源地址与 `admin.allowed_cidrs`、`mcp.allowed_cidrs`、`ADMIN_ALLOWED_CIDRS`、示例网关规则里的网段取值（示例只有文档网段，**没有配置真实网段时控制台与 MCP 都是关闭的**）；
- 容器网络隔离：裸端口（BFF、`admin-web`、`ozid-web`）只能被网关访问是网络策略，不是应用能自证的；自动化只证明直接拨它们时容器和 BFF 自己如何拒绝；
- 真实邮件厂商的投递与链接跟踪（链接是否被厂商改写）；
- 真实 S3 的注销清理（`TestChainAccountDeletionRemovesPhotosFromRealS3` 需要真实对象存储，未配置时跳过）；
- CI 的 `gateway-browser` 使用发行版的 nginx 包与 `go install` 的 Caddy，版本可能与本机不同；以 CI 实际结果为准，不用本机结果替代。

### 已知行为（不是缺陷）

- Caddy 对没有站点块的 Host（例如 SNI 是 API 主机而 `Host` 是别的名字）返回空 200：没有内容也到不了 BFF；浏览器遇到未知 SNI 则是 TLS 失败。示例没有为此增加兜底站点。
- Nginx 对畸形请求目标（`/%2e%2e/`、`%00`）在检查来源之前返回 400，没有内容。
- 本阶段不自动部署、不合并、不清理任何数据。
