# OzID 与统一管理控制台：整体验收（T12）

[规格](spec.md) §14 的 26 项验收场景（文中记作 S1–S26，序号即 §14 的编号）如何落到实现与测试，最终的来源策略，旧邮件交接与过渡清单，以及哪些验证是自动化做过的、哪些还要运维做。**不是每个场景都经过真实网关**：下文的映射表逐行分开写明三种证据各自覆盖什么，空白就是没有。只使用保留示例名称（`example.com`、`*.test`、`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`、`2001:db8::/32`、回环地址）：真实域名、来源 IP 和凭据由私有部署配置提供，不写入 Issue、PR、文档或示例。

## 真实网关与浏览器的验收场景（`TestGatewayDomainSets`）

`internal/e2e/gateway_*_test.go` 与 `e2e/gateway/` 把整套栈（OzID、控制台、trip-map、metro 的构建产物，真实 BFF、usersrv、metro、tripmap，PostgreSQL、Redis）放在**真实网关**之后，对**两组完全不同的保留同站点域名**各跑一遍（`alpha-e2e.test` 的 `id/api/trip/metro/admin`，`bravo-e2e.test` 的 `accounts/edge/footprints/rails/ops`；没有任何源码、模板或示例依赖其中一组的名字）：

- **Caddy**：运行 `deploy/reverse-proxy/Caddyfile.example` 本身。只做文本替换（站点名、上游地址与监听端口、静态文件根目录、一个本机无法自带的“网关地址”：Caddy 用 `127.0.0.2` 拨上游，客户端是 `127.0.0.1`）；每处替换在目标不在示例里时让测试失败，所以改动示例不会悄悄变成“测的已不是示例”。
- **Nginx**：运行 `deploy/ozid-web`、`deploy/admin-web` 的模板，用真实 `envsubst` 与各 Dockerfile 的 `NGINX_ENVSUBST_FILTER` 渲染，并执行镜像自己的入口脚本（`19-require-api-origin.sh`、`19-admin-access.sh`，所以空白名单、重叠的受信代理在这里同样是启动失败）。产品镜像的模板（`trip-map-web`、`metro-web`）也在裸端口上单独探测。
- **三个 Caddy 实例**：只放行测试内网客户端的白名单实例；示例原样的实例（`203.0.113.0/24`，不含任何真实网络，所以控制台对谁都关闭）；去掉网关自己的控制台规则的实例（让容器和 BFF 各自独立证明拒绝）。
- **三类来源**：`127.0.0.1` 是白名单内的浏览器，`127.0.0.4` 是 MCP 机器，`127.0.0.9` 是外部；网关是 BFF 与控制台容器的受信代理（`127.0.0.2/32`），不在任何白名单内。
- **浏览器**：Chromium（桌面与手机视口各 11 个场景：`flows.spec.ts` 7 个 + `console.spec.ts` 4 个；名单外浏览器 3 个；每组 11×2+3 = 25 个）经 `--host-resolver-rules` 把整个保留站点解析到网关端口，只忽略测试用自签证书的错误，其他不放宽。

Go 探测证明的是**哪一层**拒绝：网关拒绝的请求不得出现在 BFF 的 HTTP 边缘（观察器计数为 0）；去掉网关规则后由 BFF 拒绝的请求只能是 403/404，并且 metro 管理服务的调用计数不变；MCP 被拒的请求不改城市数据、不使用令牌。

| 探测 | 内容 |
| --- | --- |
| 公开边缘 | OzID 的 CSP/落地页头与 `ozid-web` 模板逐字一致；产品站点的旧入口 `/auth/*` 与同源 `/api/*`（含全部管理、账号写、开放过程）一律 404 且不到 BFF；`/ping` 仍转发；产品落地页头；每个构建产物只含本组的 API origin，不含另一组的站点名，也没有管理代码 |
| 控制台三层 | 白名单内浏览器可用；名单外：网关 403 空体；去掉网关规则：容器 403 `forbidden`；再绕过容器：BFF 403。示例原样的网关对内外一律 403 |
| 绕过 | 13 种伪造转发头（XFF 各种链、X-Real-IP、Forwarded、X-Forwarded-Host…）× 多种路径写法（`//`、编码点段、`%00`…）；OPTIONS/POST/PUT/DELETE/HEAD/PATCH；API 主机不服务控制台、MCP 与静态文件；CORS 预检只回应四个登记 origin，未登记兄弟、外站、http 降级、端口、`null`、另一组域名都得不到任何 CORS 头；未知 Host、另一组（旧）域名、Host 与 SNI 不一致都没有内容也到不了 BFF；控制台容器与 BFF 的裸端口在外部来源 + 伪造头下仍 403，Host 不符时容器直接断开 |
| 来源策略 | 产品 Origin 与未登记兄弟调用集中账号写（登录、注册、重置、改密、设备、注销…）一律 403；Me、CSRF、退出对四个登记应用仍然可用；管理过程换成任何非控制台 Origin 都 403 |
| MCP | 令牌经网关由控制台会话创建；机器网段走通整套协议；浏览器地址、外部地址、伪造 XFF、Forwarded、X-Real-IP、会话 Cookie 一律 403 `request rejected`，缺/错令牌 401，Cookie 不能代替令牌，其他主机不承载 MCP |

浏览器场景：`flows.spec.ts`（metro 匿名标记 → 用户中心登录 → 回到原页 → 标记保存，另一台设备读回；trip-map 登录 → 添加足迹 → 另一浏览器从服务端读回；一次登录两个产品同时可见、在用户中心退出后两个产品都失效；邮件落到用户中心并在另一浏览器完成，令牌不进请求 URL 与 Referer；旧邮件指向产品页时，产品页（经真实网关的落地页规则，带 `no-referrer`、`no-store`、`script-src 'self'`）只向用户中心交接，令牌从产品页地址栏消失、产品页没有任何带令牌的请求也不向 API 发请求，由用户中心完成注册，同一链接再用一次只显示“链接不可用”；换域名后上一组的会话在新 API 主机无效）、`console.spec.ts`（登录与角色三种身份、用户中心页面不能调用管理过程）、`outside.spec.ts`（名单外的浏览器：控制台全部 403 空体，用户中心和两个产品照常）。

- **变异检查**：把外部来源地址加进 BFF 的控制台白名单，探测失败（被抓到）。把白名单放宽到覆盖受信代理的网段则会被配置校验直接拒绝启动，这本身也是一道保护。
- **CI**：`gateway-browser` 任务（`.github/workflows/ci.yml`）用 `-v` 运行，要求所有子测试 `--- PASS`、没有任何 `--- SKIP`、两组各自的 Playwright 报告恰好是 `25 passed` 且没有 failed/skipped/flaky/interrupted/did not run，日志与失败时的 trace、截图、视频作为制品保留。缺 nginx、caddy、envsubst 或 Chromium 是失败，不是跳过。
- **本地的限制**：本机没有 Docker，镜像构建与 `scripts/*-web-smoke.sh` 只在 CI 的 `*-web-image` 任务里运行；本机用 nginx 1.28.1 与 Caddy v2.11.7 实测，CI 用发行版的 nginx 包，版本可能不同（入口脚本、`real_ip` 与 `allow/deny` 的行为稳定，但这一差别没有在本机验证）。

## #77 验收项 → 实现与测试

三类证据，每行分开写，互不替代：

- **网关**：`TestGatewayDomainSets`（真实 Caddy/Nginx、真实 Chromium、真实 BFF/usersrv/metro/tripmap/PostgreSQL/Redis；两组保留域名；对象存储是内存实现，不含 S3）。
- **链路**：不经网关的独立真实链路——真实 BFF、usersrv、PostgreSQL、Redis（`internal/e2e` 的 `TestChain*`），以及 `Test{Ozid,Admin,TripMap,Metro}BrowserChain`（真实浏览器经 HTTPS 保留子域名的 Cookie/CORS，但由测试内的本地代理承接，不是 Caddy/Nginx 网关）。这些证明会话、来源、业务规则，**不**证明网关规则。
- **模拟/单元**：模拟 BFF 的 Playwright 与 Go/TS 单元、静态扫描——只证明界面与局部逻辑，**不**证明共享会话或 IP 防绕过（规格 §14 的“测试边界”）。

| 场景 | 实现 | 网关 | 链路 | 模拟/单元 |
| --- | --- | --- | --- | --- |
| S1 两组域名、构建参数生效 | 各 Dockerfile 的 build args、`navigation`/`cors_allowed_origins`/usersrv `auth.apps.ozid`、`deploy/*` 模板与示例；无域名写入源码 | 两组域名各自完整跑通登录、邮件、返回、管理跳转；每个构建产物只含本组的 API origin | — | `TestPublicFilesUseOnlyReservedNamesAndNoCredentials`（静态扫描）；镜像 `*-web-smoke.sh` 只在 CI 的 `*-web-image` 任务里构建真实镜像检查（本机无 Docker，不经网关） |
| S2 Cookie/HTTPS/无令牌 | `session` 的 host-only Cookie；产品与 OzID 直连 API origin | `flows.spec.ts` 断言 `kongken_sid` 为 host-only、HttpOnly、Secure、Lax、无 Domain | 三个产品与 OzID 的 `Test*BrowserChain`（Cookie 属性、存储里没有令牌） | `session_test` |
| S3 A 发起 OzID 完成，B 看到同一身份 | 导航注册表、`Me`、共享 Cookie | 一次登录两个产品同时可见 | 三个产品的真实栈 Playwright | — |
| S4 未登记兄弟/外站/伪造来源与返回地址 | 精确 CORS、`navigation` 校验、Origin/CSRF | CORS 预检探测：只回应四个登记 origin | `TestChainOzidRefusesUnregisteredSameSiteOrigins`、`TestChainCSRFAndOriginRejected` | `navigation_test`（返回地址形态） |
| S5 直接访问 OzID、无循环 | OzID 路由与重新登录页 | — | `TestChainOzidSignInAndSafeReturn`、OzID 真实栈 | `ozid.spec.ts`（模拟 BFF） |
| S6 邮件只落 OzID，另一浏览器完成 | usersrv `auth.apps.ozid`、fragment 令牌、落地页头；产品旧链接只向 OzID 交接 | `flows.spec.ts` 的两个邮件场景（新邮件在另一浏览器完成；旧邮件由产品页交接、OzID 完成、重放不可用），头来自真实 Nginx 经真实 Caddy | `TestChainOzidRegistrationMailLoop`、`TestChainOzidPasswordResetMailLoop`（假收件器） | `apps/{ozid,trip-map,metro}/tests`（模拟 BFF，桌面与手机：落地页、交接页、恶意查询与返回地址、无令牌与不可用链接、响应头） |
| S7 换 API 主机重新登录、旧邮件 TTL、配置错配 | 本文“旧邮件交接与过渡清单”、`auth.legacy_mail_transition`、启动校验（含“提供账号服务必须有注册表与 OzID origin”） | “换域名”场景：上一组的会话在新 API 主机无效 | `TestChainLegacyMailTransitionIsNarrowAndEnds`、`TestChainAccountsNeedTheRegistryToStart`、`TestRealMailStackConfigsAgree` | `account_origin_test`、`config_test`（含缺注册表启动失败与 Secure Cookie 模式） |
| S8 产品无账号表单、产品 Origin 不能调账号写 | `account_origin.go`（来源策略）、产品只留入口 | 来源策略探测（产品 Origin 与未登记兄弟被拒；Me、CSRF、退出仍可用） | `TestChainAccountProceduresTakeOnlyTheUserCenterOrigin`、`TestChainRegisteredApplicationsKeepIdentityAndSignOut` | `TestNoOldAccountFormsOrManagementUIInProducts`、`TestEveryBrowserServiceHasASourceRule`；两产品 `account.spec.ts`（模拟 BFF） |
| S9 退出与退出其他设备 | usersrv 会话撤销、`RevokeOtherSessions` | 在用户中心退出后两个产品都失效（只有“退出当前会话”） | `device_sessions_chain_test.go`、`TestChainLoginAfterLogout`（含退出其他设备与幂等） | `ozid-account.spec.ts`（模拟 BFF） |
| S10 改密/重置结束全部会话与令牌 | `auth_version` | — | `TestChainChangePasswordEndsOnlyThisAccountsAccess`、`TestChainPasswordResetEndsEverySession`、`TestChainMCPTokenIsRevalidatedOnEveryCall` | — |
| S11 trip-map 未同步修改、A/B 切换 | 足迹引擎队列与 `X-Kongken-User` | 只验证登录后另一浏览器从服务端读回足迹 | `TestTripMapBrowserChain`、trip-map 真实栈 Playwright | trip-map `account.spec.ts`（模拟 BFF：队列、祖先副本、迟到响应、存储失败） |
| S12 账号注销与恢复 | 注销工作流 | —（网关场景不含注销，对象存储为内存实现） | `ozid_close_account*_test.go`、`TestOzidBrowserChain`；CI 里真实 S3 兼容服务的“必须模式”已执行（见下文“真实 S3”） | `ozid-close-account.spec.ts`（模拟 BFF，只证明界面行为） |
| S13 资料与设备页 | OzID 页面 | — | `device_sessions_chain_test.go`、OzID 真实栈 | `ozid-account.spec.ts`（模拟 BFF） |
| S14 名单外：控制台与管理 API 拒绝，公共功能可用 | `adminguard`、`admin-web` 白名单、空名单关闭 | 控制台三层探测与 `outside.spec.ts`；示例原样的网关对内外一律 403 | `TestChainUserAdminSourcePolicy`、`TestAdminBrowserChain`（不经网关，只证明 BFF 层） | — |
| S15 绕过尝试 | 同上，受信代理链 | “绕过”探测（13 种伪造转发头 × 路径写法 × 方法；裸端口） | `TestChainMetroAdminPerimeterPreflightAndPaths` | `adminguard` 单测（CIDR、IPv4/IPv6/mapped） |
| S16 浏览器与 MCP 名单独立 | `mcp.*` 独立周界 | MCP 探测（机器网段、浏览器地址、伪造头、缺/错令牌） | `TestChainMCPSourceAllowlist`、`TestChainMCPAndBrowserAllowlistsAreIndependent`、`TestChainMCPAllowedAddressStillNeedsAValidTokenAndScope` | — |
| S17 角色矩阵 | `required_role` 双层校验 | `console.spec.ts`：无角色、仅 metro 管理员、运营者三种身份（不是四种全覆盖） | `TestChainUserAdminRoleMatrix`、`TestChainMetroAdminRolesAndReauthentication`、`TestAdminBrowserChain` | `apps/admin/tests`（模拟 BFF） |
| S18 管理只接受控制台 Origin | `adminguard` 的 Origin 规则 | Origin 探测与“用户中心页面不能调用管理过程” | `TestChainUserAdminDemandsTheConsoleOrigin`、`TestChainMetroAdminPerimeterDemandsTheConsoleOrigin` | — |
| S19–S22 管理写入保护、认证时效、审计 | usersrv `ApplyAdminChange`、15 分钟密码认证 | — | `user_admin_write_chain_test.go`、`user_admin_audit_chain_test.go`、`TestChainUserAdminNeedsARecentPassword` | `apps/admin/tests`（模拟 BFF：页面行为） |
| S23–S25 metro 管理迁入、令牌、旧 /admin 退役 | 控制台 metro 分区 | 构建产物扫描（产品与 OzID 的产物不含管理代码）；产品镜像对 `/admin`、`/api/` 的 404 | `metro_admin_*_chain_test.go`、`metro_mcp_*_chain_test.go`、`TestAdminBrowserChain` | `apps/admin/tests/metro.spec.ts`（模拟 BFF）、`TestNoOldAccountFormsOrManagementUIInProducts` |
| S26 真实网关、静态回退、安全头、默认关闭 | `Caddyfile.example`、各 Nginx 模板 | 全部在此：示例原样的实例（名单为文档网段，对谁都 403）、静态回退、邮件 `no-store`/`no-referrer`、安全头 | — | `scripts/admin-web-smoke.sh`（CI 真实镜像，不经 Caddy）；`19-admin-access.sh` 拒绝空白名单 |

每张票原有的测试都保留；本阶段的全量运行见下文“验证结果”。

## 最终来源策略

| 过程 | 允许的来源 |
| --- | --- |
| `AuthService`：`Me`、`IssueCSRFToken`、`ResolveNavigation`、`Logout` | 任一已登记应用（精确 CORS 列表）；这四个是产品需要的全部账号能力，`TestAccountOpenProceduresAreTheMinimalFour` 守护 |
| `AuthService`、`UserService` 的其余过程（登录、注册、重置、重新认证、改密、资料、设备与会话、注销及其状态查询，含读方法） | **只有** `navigation.apps.ozid.origin`（`Origin` 必须恰好一个且相等）。例外：`CompleteRegistration`、`ResetPassword` 在 `auth.legacy_mail_transition.origins`（已登记的产品 origin）内、`until` 之前也接受（只给升级前构建的产品镜像；新构建的产品页不调用它们）。**BFF 提供账号服务（配置了 `upstreams.usersrv`）时必须有导航注册表与 OzID origin，缺失则拒绝启动，没有绕过开关**；不提供账号服务的 BFF 不需要。纯本地前端不使用 BFF，不受影响 |
| `UserAdminService`、`AccessTokenService`、metro `AdminService` | 控制台 origin + `admin.allowed_cidrs`（经 `admin.trusted_proxies` 解析）+ 角色；空名单关闭 |
| `bff.tripmap.*`、`bff.metro.*`（除 `AdminService`） | 已登记产品的 origin（CORS）+ 会话与 CSRF |
| `POST /mcp/metro` | Bearer 令牌 + `mcp.allowed_cidrs`（经 `mcp.trusted_proxies`）；不设 CORS；空名单关闭 |
| 公开 HTTP（`/s/*`、`/api/share/*`、`/api/notifications/*`、`/api/tiles/*`、`/ping`、`/readyz`） | 无会话；产品站点只代理这几类，其余同源 `/api` 一律 404 |

`TestEveryBrowserServiceHasASourceRule` 枚举注册表里的所有浏览器服务：新增的服务不在账号、管理、产品三份列表之一就失败。启动校验由 `config_test`（`TestValidateAccountsNeedTheRegistry`：缺 `navigation`、缺 OzID 应用、缺 `api_origin`、Secure Cookie 模式均失败）与 `TestChainAccountsNeedTheRegistryToStart`（真实启动路径）守护；正常配置下的最终来源矩阵由 `account_origin_test` 与 `TestChainAccountProceduresTakeOnlyTheUserCenterOrigin` 回归。

## 旧邮件交接与 TTL 过渡清单

新邮件始终链接到中央 OzID（usersrv `auth.apps.ozid.origin`）。切换前已发出或已排队的邮件仍指向产品自己的 `/account/verify-email`、`/account/reset-password`，令牌有效期为 usersrv 的 `registration_ttl`（默认 30m）与 `reset_ttl`（默认 15m）。

**产品页不再完成这些链接，只向中央 OzID 交接**（T12）：trip-map 与 metro 在这两个精确路径上渲染 `MailHandOff`，没有表单、不发账号请求、不加载业务或分析脚本。令牌读入内存并先清本页地址栏，再用 `location.replace` 跳到构建参数 `VITE_OZID_ORIGIN` 同名路径，令牌只在 fragment 里（不进 query、日志或存储）；目的地只来自构建配置，不接受任意 URL，query 与“返回地址”参数被忽略；令牌缺失或形态不对时不带令牌，只显示“链接不可用，请重新申请”。OzID 的落地页完成注册/重置，用过或过期的令牌显示“链接不可用”。所以不需要日后再发一次镜像来删除旧产品表单。

1. **切换日之前**：usersrv `auth.apps.ozid.origin` 与 BFF `navigation.apps.ozid.origin` 是同一个 OzID origin（BFF 提供账号服务时缺少它直接拒绝启动；配置错配只会让邮件链接落到别处，不会放行任何来源）。
2. **切换日 T0**：部署带交接页的产品镜像（交接页生效后，旧邮件由中央页面完成）。仍有**升级前构建**的产品镜像在自己页面完成链接时，才需要 BFF 配置 `auth.legacy_mail_transition`：`origins` 填这些产品 origin，`until` 填 `T0 + max(registration_ttl, reset_ttl) + 队列与重试余量`（RFC 3339，加载时最多 7 天，更长则 BFF 拒绝启动）；usersrv 的产品条目（`auth.apps.trip-map`、`auth.apps.metro`）保留到同一时刻。边缘对产品落地路径的三条规则（`no-referrer`、`no-store`、`script-src 'self'`，Caddy 示例与 Nginx 模板都有）保持不变。
3. **T0 到 `until`**：旧邮件在产品页被交接到 OzID 并完成（只有 `CompleteRegistration` 与 `ResetPassword` 对旧 origin 开放，用于旧镜像）；完成后“去登录”在 OzID。BFF 启动时写一条警告说明过渡仍在进行。
4. **`until` 之后**：BFF 不再接受旧 origin 的 API 调用（仍能启动，写警告提示删除该配置段）。新产品页的交接不受影响，OzID 对仍有效的令牌照常完成，过期的提示重新申请。删除 `legacy_mail_transition` 段与 usersrv 里的产品邮件条目并重启。没有通配 CORS，也没有通用重定向器。
5. **检查**：`TestChainLegacyMailTransitionIsNarrowAndEnds` 在真实链路上证明：控制台与未登记兄弟被拒，产品 origin 只能调这两个过程，`until` 之后被拒；`account_origin_test`、`config_test` 覆盖加载校验与过期告警；`apps/{trip-map,metro}/tests/account.spec.ts` 的“mailed links from before OzID”（桌面与手机，各 10 项）与网关 `flows.spec.ts` 的旧邮件场景覆盖交接本身（见上文 S6）；`TestNoOldAccountFormsOrManagementUIInProducts` 扫描产品源码不得再引用完成链接的落地页。

## 验证结果

本阶段在本机（隔离的 PostgreSQL 与 Redis，无 Docker）实际运行，最终代码上：

- `go vet`、`gofmt`（本阶段改动的文件）、`make check-docs`、`git diff --check`；
- `go test ./...` 全部通过（含 `internal/e2e` 的链路测试与新增的启动校验）；
- 四个浏览器链 `OZID_BROWSER_E2E`、`ADMIN_BROWSER_E2E`、`TRIPMAP_BROWSER_E2E`、`METRO_BROWSER_E2E`：`TestOzidBrowserChain` 54、`TestAdminBrowserChain` 50、`TestTripMapBrowserChain` 22、`TestMetroBrowserChain` 20 项 Playwright 全部通过；
- `GATEWAY_E2E=1 go test ./internal/e2e -run TestGatewayDomainSets`：两组各 25 个浏览器场景全部通过、无跳过，三个 Go 探测子测试全部通过；
- 四个应用各自的模拟 BFF Playwright（桌面与手机）：trip-map 250 通过（4 项为既有的条件跳过）、metro 80、OzID 166、控制台 166 全部通过；`npm test` 单元全部通过；`npx oxlint` 无错误（既有的警告不变）；四个应用的构建（带必需的构建参数）成功。

### 已定位并修复的历史失败

- **T8（[#86](https://github.com/kongken/mono/issues/86) 控制台迁入 metro 管理）的 CI 失败**：`395b73e` 之后，按钮缺 `type=button` 让表单被误提交，`dc0512c` 修复；`cf3e6d7` 之后，一个真实栈断言过早读取公共目录，`8238040` 修复。
- **T11（[#89](https://github.com/kongken/mono/issues/89) metro 入口）的 CI 失败**：`dc3f121` 上，真实栈用例给 `ResolveNavigation` 加的失败规则是页面级的，页面跳到 OzID 后也拦住了 OzID 自己的同名请求，`376437d` 改为只让来自 metro origin 的那次请求失败。
- 本阶段：启用来源策略后许多链路测试得到 `permission_denied: request rejected`（测试以产品或默认 Origin 调账号写；已按真实调用者改为 OzID Origin，或明确走产品 Origin 以验证被拒）；`TestLegitimatePayloadLimitsAreConsistent` 在产品镜像不再代理 `/api/` 后失败（原先它检查产品 Nginx 的 `client_max_body_size`，现改为检查唯一承载同步批量的 API 站点的 `max_size ≥ 16 MiB`，并检查产品镜像对 `/api/` 返回 404）；网关场景首次运行的失败都是断言写错或页面状态理解有误（`/auth` 无尾斜线本就落到 SPA、Caddy 对不匹配的 Host 返回空 200、浏览器对无 CORS 头的 403 只报告请求失败），已改成与真实行为一致且不放宽拒绝的断言。
- 要求账号服务必须有注册表后，原先不带注册表的测试装配会启动失败，已改为默认带注册表（`withAccountRegistry` 与链路装配的默认注册表）。

### 未定位的偶发失败（没有当作已修复）

- **`TestWindowExpires`**（`internal/usersrv/ratelimit`，150ms 窗口、等待 250ms）：本阶段早先有一次本地失败，原因**没有定位**。在当前 HEAD 上，全量 `go test ./...`、该用例单独连跑 20 次、整个包连跑 5 次都**未复现**，CI 的 `-race` 运行也通过；这只说明现在没有复现，**历史原因未定位**，不能据此说它已修复。该测试依赖真实时钟的短窗口，机器繁忙时的抖动是可能但未经证实的解释。
- **T11 期间一次本地浏览器偶发**：没有日志，至今**没有定位**；之后的运行没有再出现，同样不能说已修复。继续保留为已知未定位项。

### 真实 S3（CI 已执行的必须模式）

Backend 任务设置 `E2E_REQUIRE_REAL_S3=1` 并启动 S3 兼容服务（MinIO），再有单独步骤“Account deletion against real S3 (required, never skipped)”只运行 `TestChainAccountDeletionRemovesPhotosFromRealS3` 与 `TestChainCloseAccountRecoversFromAnObjectStoreOutage`：缺任何 `TRIPMAP_TEST_S3_*` 变量时两者直接失败，不跳过也不回退到内存；步骤检查两者都 `PASS`、日志里有 `object store: real S3`、没有 `SKIP`。T12 的 CI 运行里两者都以真实 S3 通过（已执行）。**不需要也没有**把全部网关场景再乘一遍 S3：网关场景用内存对象存储，不含注销；S3 的证据只来自上面两个用例。本机没有配置 S3 变量时前者跳过、后者用内存，这样的运行只证明逻辑，不算 S3 证据。

### 仍需运维验证（自动化无法覆盖）

- 真实 DNS、公共可信证书与 TLS 终止、真实 CDN/负载均衡链路（其网段必须同时列入 `admin.trusted_proxies`、`mcp.trusted_proxies`、`ADMIN_TRUSTED_PROXIES`，并让 Caddy 的 `trusted_proxies` 全局选项一致）；
- 真实客户端来源地址与 `admin.allowed_cidrs`、`mcp.allowed_cidrs`、`ADMIN_ALLOWED_CIDRS`、示例网关规则里的网段取值（示例只有文档网段，**没有配置真实网段时控制台与 MCP 都是关闭的**）；
- 容器网络隔离：裸端口（BFF、`admin-web`、`ozid-web`）只能被网关访问是网络策略，不是应用能自证的；自动化只证明直接拨它们时容器和 BFF 自己如何拒绝；
- 真实邮件厂商的投递与链接跟踪（链接是否被厂商改写）；
- **生产对象存储**：厂商侧的删除权限、最终一致性、生命周期规则与跨区域行为；CI 里的真实 S3 兼容服务（MinIO，见上）证明的是协议层的删除逻辑，不替代厂商验收；
- CI 的 `gateway-browser` 使用发行版的 nginx 包与 `go install` 的 Caddy，版本可能与本机不同；以 CI 实际结果为准，不用本机结果替代。

### 已知行为（不是缺陷）

- Caddy 对没有站点块的 Host（例如 SNI 是 API 主机而 `Host` 是别的名字）返回空 200：没有内容也到不了 BFF；浏览器遇到未知 SNI 则是 TLS 失败。示例没有为此增加兜底站点。
- Nginx 对畸形请求目标（`/%2e%2e/`、`%00`）在检查来源之前返回 400，没有内容。
- 本阶段不自动部署、不合并、不清理任何数据。
