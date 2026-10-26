# BFF（公网入口）

> **后续方案：** [OzID 与统一管理控制台规格](ozid/spec.md) 规划统一用户中心、独立管理前端、配置化域名/导航、网页用户管理、集中账号写来源及浏览器/MCP 两类 IP 白名单。其中 T1（统一登录、重新认证与安全返回）、T2（统一注册验证与密码重置邮件）、T3（账号资料、密码与设备会话页面）、T4（独立管理控制台、`ozid-admin` 与只读用户查询、管理 IP 周界）、T5/T6（账号状态与角色修改、安全与审计查询）、T7（OzID 网页注销页与失败恢复）和 T8（metro 管理功能迁入控制台、metro 管理接口与管理令牌接口进入管理周界）已实现，见[导航注册表与 `ResolveNavigation`](#导航注册表与-resolvenavigation-ozid-t1)、[管理周界与用户查询](#管理周界与用户查询ozid-t4)、[OzID 功能描述](ozid/features.md) 和[控制台功能描述](admin/features.md)；其余（集中账号写来源、MCP 的独立 IP 白名单、产品入口收敛）仍待实现，不代表这些新防护或入口已生效。

一个仓库承载多个业务时，BFF（`cmd/bff`）是唯一的公网入口，统一处理跨域、客户端地址、请求生命周期和追踪脱敏。业务服务只在内网监听。当前有 tripmap（山海足迹）和 metro（地铁足迹，可选）两个业务。

## 当前状态

目标架构见 [Issue #26](https://github.com/kongken/mono/issues/26)。[Issue #29](https://github.com/kongken/mono/issues/29) 迁移匿名状态；[Issue #30](https://github.com/kongken/mono/issues/30) 迁移密码账号与 Cookie 会话；[Issue #33](https://github.com/kongken/mono/issues/33) 迁移足迹读/sync 全链路。

**当前已实现**

- `bff.tripmap.v1.StatusService/GetStatus`（匿名）→ `tripmap.v1.SystemService/GetStatus`。
- `bff.v1.AuthService`（账号与会话）与 `bff.v1.UserService`：BFF 不保存密码，也不再有任何第三方登录。注册、登录、找回/重置密码、改密码、资料与注销都经内部 gRPC 调用 usersrv（`usersrv.v1.AccountService`），BFF 自己只负责浏览器会话、CSRF、入口策略和注销工作流。详见下文「账号、会话与注销（P4）」。
- `bff.v1` 的 RPC：`RequestRegistration`、`CompleteRegistration`、`LoginWithEmail`、`RequestPasswordReset`、`ResetPassword`（匿名）；`Me`、`ListSessions`、`Logout`、`RevokeSession`、`RevokeOtherSessions`、`Reauthenticate`、`ChangePassword`、`UpdateMyProfile`、`DeleteAccount`、`GetAccountDeletion`、`RetryAccountDeletion`（登录后）。旧的 Register/Login/OAuth RPC 已从契约删除，字段编号用 protobuf `reserved` 保留。
- usersrv 已登记为上游服务（`gateway.Services`，`upstreams.usersrv`、`grpc.services.usersrv`），是必需的：不配置则启动失败。`/readyz` 增加 `usersrv_grpc`。
- `bff.tripmap.v1.FootprintService`（读、增量、批量 sync、单条写）→ tripmap gRPC `FootprintService` 同名方法；`X-Tripmap-Schema` / `X-Tripmap-Regions` 白名单下传。`SyncChanges` 登记为 25 秒重任务；Connect 读上限 `grpcx.LegitimatePayloadBytes`（16 MiB），内部 gRPC 默认 20 MiB。tripmap 的展示投影（用户 ID、昵称、头像）在首次调用 `bff.tripmap.v1.*` 时由 BFF 按需确保：读 usersrv 的账号后调用 `ProfileService/EnsureProfile`，并在 Redis 记下已投影的 `profile_version`（24 小时），昵称或头像变化后下一次调用重新投影；Redis 故障不致命，只多做一次幂等调用。投影只含用户 ID、昵称、头像，不含邮箱。
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
- **统一管理控制台与用户查询（T4）**：`bff.v1.UserAdminService/ListUsers`、`GetUser`、`ListSecurityAudit`（只读，`required_role = "ozid-admin"`；后者是 T6 的账号审计）→ 内部 `usersrv.v1.UserAdminService` 同名方法；浏览器入口前有管理周界（来源白名单、受信代理链、控制台 Origin），详见下文「管理周界与用户查询（OzID T4）」。
- **角色与管理员**：角色只存在于 usersrv（`user_roles`，当前有两个白名单角色：`metro-admin`（地铁足迹管理）与 `ozid-admin`（统一管理控制台，T4），都只能用 `usersrv-admin` 授予/撤销，互不继承），会话里不再保存角色快照。BFF 每个受保护请求都调用 usersrv `GetAuthState`（账号 active、`auth_version` 与会话相同、当前角色），`MeResponse.roles` 返回的就是当前角色。proto 方法选项 `platform.v1.required_role`（`proto/platform/v1/options.proto`）声明方法所需的角色：BFF ingress 在认证后检查（未登录 `UNAUTHENTICATED`，缺角色 `PERMISSION_DENIED`，会话的 `authenticated_at`（真实的密码验证时间）超过 `auth.admin_session_max_age`（默认 12h）`UNAUTHENTICATED`），业务服务的拦截器再从内部凭证独立检查一次（`rol` claim，只在声明了 `required_role` 的方法里签入）。角色撤销因此在下一次请求就生效。
- **账号注销多产品化**：注销工作流（见下文）依次调用 `auth.account_cleanup` 列出的各产品清理器（未设置时默认是 tripmap 加上已配置上游的 metro；示例配置为 `[tripmap, metro]`），每个都幂等；工作流创建时固定这份产品清单，之后配置移除了某个产品会报错而不是悄悄跳过。未部署的产品不能列入；已配置上游的 tripmap/metro 漏列则 BFF 启动校验失败，因为那会让注销后该产品的数据被留下。
- **可选上游**：`gateway.Service.Optional`（metro）使该产品在 `upstreams` 没有对应项时整体不启用：不校验、不挂路由、不探测、不清理；已配置则与 tripmap 一样校验 `grpc.services`。
- **metro（#64 P1–P3）**：`bff.metro.v1.CatalogService`（城市、线路、车型专题；全部匿名只读，`ListCities` 支持 `known_version`）→ `metro.v1.CatalogService`（按字段手写映射）；`bff.metro.v1.RiderService`（`GetPublicProfile` 匿名；`GetMyProfile`、`UpdateMyProfile`、`ListMyLines`、`MarkLine`、`UnmarkLine` 需登录）→ `metro.v1.RiderService`；`bff.metro.v1.AdminService`（城市/车型/线路/车型专题的增删改与列表、`ImportCatalog`、`ExportCatalog`、`ListAuditLogs`；全部 `required_role = "metro-admin"`）→ `metro.v1.AdminService`。管理读（`ListAdmin*`、`ExportCatalog`、`ListAuditLogs`）与登录读登记为只读过程，其余写过程校验 Origin/CSRF；`ImportCatalog`、`ExportCatalog` 登记为 25 秒重任务；管理接口 Connect 读上限 2 MiB（导入文档上限 1 MiB）。管理与骑行者契约与内部契约同形，由 `internal/bff/metro/relay` 经 protobuf 线格式转换，`TestContractsStayWireCompatible`（`internal/bff/metro/admin`、`rider`）在两边字段、编号、类型或方法不一致时失败；域错误（`INVALID_ARGUMENT` 字段错误、`FAILED_PRECONDITION` 的当前 revision 或引用数量、`ALREADY_EXISTS`）连同类型化 detail（`ValidationDetail`、`RevisionConflictDetail`、`ReferencedDetail`）原样返回浏览器。BFF 下传审计所需的 `x-bff-channel`（`web`）与（MCP 渠道的）`x-bff-token-id`，只在声明了 `required_role` 的方法上发送。
- **管理令牌与 MCP（#64 P4）**：共用契约 `bff.v1.AccessTokenService`（`CreateAccessToken`、`ListAccessTokens`、`RevokeAccessToken`）在 BFF 自己的库表 `bff_access_tokens`（`migrations/bff/*_access_tokens.sql`）里管理令牌：明文 `mcp_` + 64 位十六进制只在创建响应里出现一次，库里只存 SHA-256 摘要；字段有 `audience`（`metro`）、`scopes`（`metro:read`、`metro:write`，写隐含读）、创建时的 `roles` 快照与 `auth_version`、`expires_at`（必填，最长 90 天）、`last_used_at`、`revoked_at`；每个用户最多 10 个有效令牌；创建要求会话 + CSRF + `metro-admin`（且在 `admin_session_max_age` 内签发），并且会话的密码验证时间在 15 分钟内（否则 `FAILED_PRECONDITION`，先调 `Reauthenticate`），同一用户的创建用 advisory 锁串行以守住上限；创建者或任何管理员可吊销，每次使用都查库，吊销立即生效。**每个工具调用都会再向 usersrv 取 `GetAuthState`**：账号非 active、`auth_version` 与令牌创建时不同，工具返回 `UNAUTHENTICATED`；usersrv 不可用返回 `UNAVAILABLE` 且不转发；有效角色是令牌快照与当前角色的交集，缺 `metro-admin` 为 `PERMISSION_DENIED`。角色撤销后重新授予也不会让旧令牌复活，需重新创建。令牌的创建与吊销只写 BFF 的 `slog`（令牌 ID、用户、名称、作用域；不含明文），**不进入 metro 的 `admin_audit`**（审计表属于 metro，BFF 不写它）。MCP 端点 `POST /mcp/metro`（`internal/bff/metro/mcp`，在 `gateway.BFFOwnedRoutes` 登记为 `Method: "POST"` 的自有路由，仅当配置了 `upstreams.metro` 时挂载）是无状态的 MCP Streamable HTTP：只实现 `initialize`、`ping`、`tools/list`、`tools/call`（含批量；通知返回 202；协议版本接受 2025-06-18、2025-03-26、2024-11-05），不提供 SSE、resources、prompts。只认 `Authorization: Bearer`，**忽略 Cookie、不设 CORS**；失败认证按客户端地址限流（每分钟 30 次），成功请求按令牌在 Redis 限流（`mcp.rate_limit_per_minute`，默认 60）。20 个工具（读 7：`metro_list_cities`、`metro_list_lines`、`metro_list_rolling_stocks`、`metro_list_topics`、`metro_get_entity`、`metro_export_catalog`、`metro_list_audit_logs`；写 13：城市、车型、线路、专题各 create/update/delete，加 `metro_import_catalog`）直接调用同一个内部 `AdminService`，凭证里带令牌创建者的 `sub` 与有效角色，审计行记 `channel=mcp` 与令牌 ID。`dryRun` 默认 `true`，update/delete 必须带 `expectedRevision`，工具错误以 `isError` 结果返回（含字段级问题或当前 revision 的 JSON）。**MCP 协议层是手写的**（JSON-RPC 约 400 行，约束见上），没有引入 Go MCP 库：无状态 Streamable HTTP 只需这四个方法，官方库的会话与 SSE 模型对 BFF 的无状态路由是多余的依赖；将来需要 resources 或服务端推送时再评估（规格待决问题 7）。
- 本地：`config.bff.example.yaml` 配置 `store.db`（建议库名 `bff`）、`store.redis`；Vite 代理 `/api` 至 BFF `:18081`（同源 Cookie）。

**测试数据清理与升级**

不迁移旧的测试账号、旧的外部身份映射或云端足迹。BFF 的版本化迁移（`migrations/bff`）只做加法与必要的改表：会话表增加 `auth_version`，旧会话行（无版本或用户 ID 不是 UUID）在第一次使用时被撤销，须重新登录；`bff_account_deletions` 保存注销工作流。旧表 `auth_identities`、`account_deletion_receipts` 不会被迁移删除，验证新栈后由手动脚本 `deploy/sql/bff/retire_logto_identities.sql` 清理（对象、范围与顺序见 [Logto 退役与手动清理](usersrv/retirement.md)，部署步骤见[部署与发布](deployment.md#自建用户系统的数据迁移)）。这些脚本不会由启动流程自动调用。


**当前限制**

- 浏览器 RPC 仅 `/api/bff.*`（共用的 `bff.v1`、各产品的 `bff.<产品>.v1`）；`/api/tripmap.v1.*` 透明转发已移除。tripmap HTTP 只保留 `/ping`、`/readyz` 与可选的 `/api/tiles/…`（分享/unsubscribe 由 BFF `publichttp` 提供）。
- 前端的业务 RPC（含分享 API）使用 `bff.tripmap.v1`；账号与会话管理使用 `bff.v1`。BFF 会话和注销工作流在 BFF 独立库；tripmap `users` 只保存用户 UUID 对应的展示投影与产品数据，好友只能凭 `fc_` 邀请码添加（没有按用户名查找）。
- 浏览器登录、注册、找回密码与账号设置在 metro 与 OzID 实现（P5，见 [usersrv 规格](usersrv/spec.md#p5-已实现内容)；trip-map（T10）与 metro（T11）自此没有账号表单，所有账号入口都去 OzID，只保留升级前旧邮件的落地页）；发布与退役（P6）：usersrv 镜像与部署示例、Logto 运行时入口删除、手动退役文档已完成（[退役说明](usersrv/retirement.md)）；正式环境开放前仍需对真实邮件厂商做手动冒烟（[说明](usersrv/mail-smoke-test.md)，未对任何真实厂商实测）。
- **T2 之后的区别：** trip-map 的产品入口已在 T10 切换到 OzID（账号弹窗只有 OzID 入口，无表单）；metro 的产品入口也已在 T11 切换（没有账号表单，`/login`、`/account` 只是说明页）；新邮件已集中到 OzID（链接都指向 `auth.apps.ozid`），邮件落地页与完成后的“去登录”在 OzID，产品对升级前发出的旧邮件链接只做向 OzID 的交接（T12，没有表单、不调账号过程）。

```
浏览器 → 前端 Nginx（BACKEND_UPSTREAM）→ BFF :18081 ─┬→ tripmap（内网）
                                                    └→ 以后的业务
```

## 路由

`internal/bff/gateway/services.go` 的 `Services` 登记**上游业务服务名**与是否经 gRPC 调用（tripmap、metro、usersrv 均 `GRPC: true`），供 `upstreams` 健康探测与 `grpc.services` 配置对齐；**不再**按 proto 包名透明转发 HTTP；仅 `Service.Routes` 中显式登记的 HTTP 路径会转发（目前只有 `/api/tiles/`）。BFF 自己应答的 HTTP 路径（目前只有 MCP 端点）另在同文件 `BFFOwnedRoutes` 登记，浏览器额外请求头由 `BrowserHeaders` 登记。

| 入口 | 登记位置 | 说明 |
| --- | --- | --- |
| `/ping`、`/readyz` | `internal/bff/app/app.go` | BFF 自身与依赖就绪 |
| `/api/bff.v1.<Service>/<Method>` | `app.go` `publicAPI` + `bffv1connect.New*Handler` | 共用的账号与会话 Connect RPC；`NoRoute` 下前缀 `/api/bff.` 进入此 mux |
| `/api/bff.tripmap.v1.<Service>/<Method>` | `internal/bff/tripmap/tripmap.go` `MountStatus`、`MountRPC` | 山海足迹业务 Connect RPC，同一个 mux |
| `/api/bff.metro.v1.<Service>/<Method>` | `internal/bff/metro/metro.go` `MountRPC` | 地铁足迹业务 Connect RPC；仅当配置了 `upstreams.metro` 时挂载 |
| `POST /mcp/metro` | `app.go` `mountMCP` + `internal/bff/metro/mcp`；`gateway.BFFOwnedRoutes`（`Method: "POST"`） | 地铁足迹的 MCP 端点（Bearer 管理令牌）；`bff.v1.AccessTokenService` 挂在 `/api/bff.v1.AccessTokenService/…` |
| `/s/…`、`/api/share/…`、`/api/notifications/unsubscribe` | `app.go` `mountPublicHTTP` + `internal/bff/publichttp` | 分享页/数据/预览图、邮件退订；凭分享或退订令牌，不用 Cookie CSRF |
| `/api/tiles/{z}/{x}/{y}.png` | `gateway/services.go` `Routes`（`Anonymous: true`）+ `gateway.Forward` | 登记路径透明转发到 tripmap，规则与限流在 tripmap；不规范路径（`..`、`//`）404 |
| 其他路径 | `NoRoute` | **404**（含旧 `/api/tripmap.v1.*`） |

- 不规范路径（含 `..`、`//`）在 BFF 层拒绝或 404。
- BFF Connect 读上限 16 MiB；边缘 Nginx 20 MiB；内部 gRPC 默认 20 MiB（见 [部署与发布](deployment.md#请求体大小)）。

## 公网 Connect 入口（`bff.v1`、`bff.tripmap.v1`）

`/api/bff.<包>.<服务>/<方法>` 由 BFF 自己应答，不转发；路由在 `internal/bff/app/app.go` 与各产品的 `internal/bff/<产品>/`，处理链在 `internal/bff/ingress/`。

- **只接受 Connect**：仅 `POST`，`Content-Type` 必须是 `application/json` 或 `application/proto`（可带 charset），否则返回 405 / 415（`Accept-Post` 列出可用类型）。gRPC、gRPC-Web 与 Connect 流式帧都在到达处理器前被拒绝。处理器还要求 `Connect-Protocol-Version: 1`，缺失或不是 `1` 返回 400。
- **路径**：未知方法或服务、多余路径段、含 `..` 或 `//` 的路径一律 404，不会落入通用转发。
- **角色**：方法选项 `platform.v1.required_role` 声明所需角色（默认无）；ingress 先认证，再检查会话角色和 `admin_session_max_age`，通过后才把角色签进该调用的内部凭证。`rpcauth.Call.Roles` 不对没有此选项的方法生效。
- **匿名策略**：由 proto 方法选项 `platform.v1.anonymous` 声明（`proto/platform/v1/options.proto`）；**默认受保护**，未知过程也受保护。BFF 的拦截器和 tripmap 的拦截器各自独立执行同一规则。匿名方法不使用浏览器会话，调用内部服务时不带用户 ID。
- **写操作防护**：除 `internal/bff/ingress/write_policy.go` 列出的只读 RPC 外，所有 `bff.*` 过程在 ingress 统一校验 CSRF 双提交与同源（`Origin`、配置的 `cors_allowed_origins`、或与 `Host` 匹配的 Origin，或在缺少 `Origin` 时浏览器的 `Sec-Fetch-Site: same-origin`）。`IssueCSRFToken` 与各类 GET 式读 RPC 豁免；公开 HTTP 路由使用领域令牌而非 CSRF。**可重放的拒绝标记（T10）：** 写操作被拒绝时一律是 `PermissionDenied` 加同一句 `request rejected`（不泄露原因）；其中**只有 CSRF 双提交不匹配或缺失**（共用的令牌 Cookie 被另一个标签页或应用替换后，这是浏览器客户端自己能修复的唯一一种）会额外带响应头 `X-Bff-Rejection: csrf`（常量在 `internal/bff/ingress`，值固定，不含任何请求、会话或账号内容；该头在 `Access-Control-Expose-Headers` 里，跨 origin 的浏览器脚本能读到）。非法 Origin、周界、所需角色和业务拒绝，以及账号守卫（`X-Kongken-User` 与 Cookie 账号不符，返回 `Unauthenticated`）都不带这个标记。客户端只对带标记的一元写调用刷新令牌并重放一次，见[山海足迹功能描述](trip-map/features.md#账号与云端同步)；每次拒绝使用新建的错误值，避免标记残留到之后的拒绝上（测试：`internal/bff/ingress/rejection_marker_test.go`）。
- **期限与取消**：普通过程上限 10 秒；上传等重任务上限 25 秒，需在 `ingress.Policy.Heavy` 中显式登记（已登记 `FootprintService/SyncChanges` 与 metro 的 `ImportCatalog`、`ExportCatalog` 等）。实际期限取客户端剩余期限（`Connect-Timeout-Ms`）与上限的较小值，在 BFF 设置一次并通过 gRPC 期限传给业务服务；客户端取消会传播到服务，服务端处理随之停止。不做任何自动重试。
- **请求 ID**：读取 `X-Request-Id`，仅当匹配 `^[A-Za-z0-9._-]{1,64}$` 时沿用，否则替换为随机 32 位十六进制；响应（含错误）回写该头，并作为内部 metadata 下传。
- **客户端地址**：按 `client_ip_header`（受信反向代理写入的头）解析，未设置时用 TCP 对端；客户端自带的 `X-Bff-Client-Ip` 之类的头不会被读取。
- **错误**：业务服务的错误消息不原样返回；`NOT_FOUND`、`PERMISSION_DENIED`、`INVALID_ARGUMENT`、`RESOURCE_EXHAUSTED` 等用户可见域错误按同名 Connect 码返回；`UNAVAILABLE`、`DEADLINE_EXCEEDED`、`CANCELED` 同理。BFF 凭证被拒（tripmap 返回的 `UNAUTHENTICATED`）与 `INTERNAL` 等服务端故障统一为 `INTERNAL: internal error`。tripmap 不可达时返回 `UNAVAILABLE`。
- **跨域**：与 `/api/` 其他路径一样由 BFF 按 `cors_allowed_origins` 应答。

媒体 `ReadMedia`、`ReadSharedTripMedia` 响应支持可选 `url` 字段：tripmap 配置 `storage.cdn_domain` 后，BFF 透传公开 CDN URL（`data` 为空），前端无凭据请求 CDN；默认仍透传文件 bytes。鉴权继续在 BFF 和业务服务执行。CDN 配置仅在 tripmap，BFF 不增加路由或配置；部署和公开链接限制见 [后端初始化](backend.md)。

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
| `rol` | 可选，用户的角色名列表。只在目标方法声明了 `required_role` 时才签入，且必须有 `sub`；服务端据此独立执行角色要求 |

- **校验**：服务端检查签名、`kid`、`iss`、`aud`、`mth`、时间窗口和必需字段；允许的时钟偏差默认 10 秒（`internal_auth.clock_skew` 可调，0–1 分钟）。所有失败返回同一个 `UNAUTHENTICATED`，不说明失败原因。
- **匿名与受保护**：匿名方法同样要求有效 BFF 凭证；不含 `sub` 的凭证打不开受保护方法。业务只能从拦截器放入 context 的类型化信息（`rpcauth.CallFrom` / `UserIDFrom`）取得用户 ID，不读取裸 metadata。
- **metadata 白名单**：BFF 每次调用都重建出站 metadata，写入 `authorization`、`x-request-id`、`traceparent`、`x-bff-client-ip`，浏览器声明的 `x-tripmap-schema`、`x-tripmap-regions`（足迹读/sync 需要），以及仅发往声明了 `required_role` 的方法的 `x-bff-channel`（`web` 或 `mcp`）与 `x-bff-token-id`（审计用）；服务端只读取白名单项，并且只在凭证验证通过后相信它们。
- **私钥与公钥**：私钥（PKCS#8 PEM）只在 BFF；服务只持受信公钥（PKIX PEM）。不依赖网络位置认证，不使用 mTLS（TLS 为单向，只由客户端校验服务端证书）。两种传输下都要求 BFF 凭证；h2c 只去掉传输层 TLS 材料，不去掉签名密钥。
- **密钥轮换**：在服务的 `internal_auth.trusted_keys` 加入新公钥（`kid` 不同，新旧并存，文件和内联来源可混用）→ 把 BFF 的 `internal_auth.kid` 与私钥配置（`private_key` 或 `private_key_file`）切到新密钥 → 等旧凭证的有效窗口与允许偏差过去（约 70 秒）后撤下旧公钥。仍用已撤销密钥签名的 BFF 会被拒绝，浏览器看到 `INTERNAL`。
- **日志**：凭证、Cookie、CSRF 令牌等不进入日志；上游错误只记录服务名和 gRPC 码。

本地示例显式选用 loopback h2c，无需本地 TLS 证书。`make dev-secrets` 默认只生成 `dev-secrets/bff.key` / `bff.pub` 签名密钥对（`kid` 为 `dev-1`，目录已被 Git 忽略）；也可[在配置内填写密钥](#配置内联签名密钥)，不需要独立 key 文件。TLS 本地测试使用 `go run ./cmd/dev-secrets -tls` 额外生成 CA 与 tripmap 证书，同时将两端配置改为 TLS 并设置 CA/cert/key；已有文件不会覆盖，轮换需显式 `-force`。

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
| `GET /readyz` | upstream HTTP `/readyz`、`bff_postgres`、`bff_redis`、各 `{name}_grpc`（按该上游所选传输 tls / h2c，签名 `GetStatus`；`usersrv_grpc`、`tripmap_grpc`、`metro_grpc`）；2 秒超时，全部就绪 200，否则 503 |

BFF 启动时不要求业务服务已经可用，只在 `/readyz` 中反映。

## 配置

复制 `config.bff.example.yaml` 为 `config.bff.yaml`（已被 Git 忽略）。本地默认由 Butterfly 文件模式读取，`${VAR}` 不展开。也支持 Consul KV：默认读取 `apps/bff`，设置 `BUTTERFLY_NAMESPACE=kongken/prod` 后读取 `kongken/prod/bff`；`BUTTERFLY_CONFIG_CONSUL_NAMESPACE` 是独立的 Consul API Namespace。完整环境变量和启动示例见 [Consul 配置与路径前缀](backend.md#consul-配置与路径前缀)。

- `db_store` / `redis_store` / `key_prefix` / `auto_migrate`：BFF 认证库与 Redis 限流（见 example 内 `store` 段）。
- `mcp.rate_limit_per_minute`（每个管理令牌每分钟的 MCP 调用数，默认 60；失败认证的按地址限流固定为每分钟 30 次）。
- `mcp.allowed_cidrs`、`mcp.trusted_proxies`、`mcp.forwarded_header`（T9：`POST /mcp/metro` 自己的来源白名单与代理链，与 `admin.*` 是两份独立配置；`allowed_cidrs` 为空 = 拒绝所有 MCP 请求，详见「MCP 的独立来源白名单」）。
- `auth.session_ttl`、`auth.admin_session_max_age`（默认 12h，声明了 `required_role` 的方法要求会话在此时间内签发）、`auth.account_cleanup`（账号注销时清理数据的产品，默认 tripmap 加上已配置上游的 metro；已配置上游的 tripmap/metro 不能漏列）、`auth.cookie_secure`、`auth.csrf_token_ttl`。`auth.oauth_apps`、`auth.oauth_providers`、`auth.oauth_development` 已删除（不再读取）；注册开关与邮件链接来源属于 usersrv 配置（`config.usersrv.example.yaml`）。`auth.admin_session_max_age` 现在从真实的密码验证时间起算。
- `upstreams.usersrv` 与 `grpc.services.usersrv`：必填，BFF 通过它做账号认证与每个请求的 `GetAuthState`；用户系统不可用时受保护请求返回 `unavailable` 且不转发任何业务调用。
- `upstreams.<服务>`（`metro` 可选：不写则 BFF 不启用地铁足迹，也不需要 `grpc.services.metro`）、`grpc.*`、`internal_auth.*`、`client_ip_header`、`cors_allowed_origins`：同前。`grpc.services.<名称>.transport` 为 `tls`（默认）或 `h2c`，见 [内部 gRPC 与凭证](#内部-grpc-与凭证)。

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

### 配置内联签名密钥

文件方式继续兼容；每个密钥的来源必须二选一：

- BFF：`internal_auth.private_key`（内联 **PKCS#8 Ed25519 PEM**）或 `private_key_file`。
- tripmap：`internal_auth.trusted_keys[].public_key`（内联 **PKIX Ed25519 PEM**）或 `public_key_file`。
- 同时设置、都未设置、错误 PEM/DER、非 Ed25519 或无法读取文件，均拒绝启动；不根据加载失败回退另一来源。错误不输出 PEM 内容。不同 `kid` 可分别选择文件或内联来源，重复 `kid` 仍拒绝。

以下内容是占位符，必须替换为**同一密钥对**，两端使用相同 `kid`；不要保留对应的 `*_key_file`：

```yaml
# BFF config.bff.yaml（仅展示 internal_auth）
internal_auth:
  issuer: tripmap-bff
  kid: key-1
  private_key: |
    -----BEGIN PRIVATE KEY-----
    <PKCS#8 Ed25519 private key base64>
    -----END PRIVATE KEY-----
---
# tripmap config.tripmap.yaml（仅展示 internal_auth）
internal_auth:
  issuer: tripmap-bff
  audience: tripmap
  trusted_keys:
    - kid: key-1
      public_key: |
        -----BEGIN PUBLIC KEY-----
        <matching PKIX Ed25519 public key base64>
        -----END PUBLIC KEY-----
```

使用 YAML `|` 保留真实换行；不展开 `${VAR}`，也不把字面量 `\n` 转成换行。内联配置适用于 TLS 与 h2c，不会禁用签名。密钥只在启动时加载，修改配置后需重启；轮换顺序同前。

**保密**：含 `private_key` 的配置本身就是 Secret，仅保存在忽略的本地配置或受控 Secret 配置中，限制文件权限与读取范围；不得提交到 Git、放入公开 ConfigMap、输出到日志或 PR。tripmap 只配置公钥，不应拿到 BFF 私钥。生产使用独立密钥对，不能复用开发密钥。

## 本地开发与部署

首次 `make dev-secrets`（或配置配套的内联签名 PEM）；创建库 `bff` 与 `usersrv`。`make dev` 启动 BFF（18081）、usersrv（18086/18087 内网，需要自己的数据库与 Redis，见 `config.usersrv.example.yaml`）、tripmap（18082/18083 内网）与 metro（18084/18085 内网，需要自己的数据库 `metro`，见 [后端初始化](backend.md#metro-服务地铁足迹)；不用 metro 时在两个终端分别运行 `make dev-bff`、`make dev-usersrv`、`make dev-tripmap`，并去掉 BFF 配置里的 `upstreams.metro`）。生产见 [部署与发布](deployment.md)。

## 新增业务

BFF 共用账号、会话、CSRF 与入口策略（`bff.v1.AuthService`、`internal/bff/auth`、`session`、`ingress`），单点登录的账号库在 BFF；各产品只增加自己的适配层：

1. `proto/bff/<产品>/v1/` 定义浏览器契约（包名 `bff.<产品>.v1`，Go 包别名 `bff<产品>v1`），运行 `make buf`。
2. `internal/bff/<产品>/` 放各服务的适配处理器，并提供与 `internal/bff/tripmap/tripmap.go` 同形状的 `Service`、`HeavyProcedures`、`MountStatus`/`MountRPC`、`MountPublicHTTP`。产品包之间不互相 import。
3. 在 `internal/bff/app/app.go` 的 `publicAPI`、`heavyProcedures`、`mountPublicHTTP` 里调用新产品的 `Mount*`；只读过程登记到 `internal/bff/ingress/write_policy.go`，其余过程自动要求 Origin/CSRF。
4. 在 `internal/bff/gateway/services.go` 登记上游名与 HTTP 路由，在 BFF 配置的 `upstreams`、`grpc.services` 增加地址。
5. 前端应用只使用 `@kongken/api-client/bff/v1/auth_pb` 与自己的 `bff/<产品>/v1/*_pb`。

## 当前限制（多产品）

- 会话/CSRF/注销恢复 Cookie **默认** host-only（无 `Domain`），通常所有前端直连同一个 BFF origin；显式共享 Domain 见下节。不承诺跨根域、第三方 Cookie 或 OIDC。各前端代理到同一 BFF 本身不等于共享会话。
- `bff.tripmap.v1.StatusService` 的响应带有山海足迹专有字段（`footprint_schema`、`share_enabled`、`region_version`），是产品级状态，不是通用健康检查。

### 可选共享 Cookie Domain

仅当部署方信任目标域下**所有**子域时，可配置：

```yaml
auth:
  cookie_secure: true
  cookie_domain: example.com
```

空值保持 host-only。共享值必须是小写 DNS 域（无前导点、`*`、端口或 URL），不能是 IP、公共后缀或私有托管后缀（如 `github.io`）。BFF 启动时要求 HTTPS，并检查规范 API、每个导航应用及全部 CORS origin 的主机均被该 Domain 覆盖；不根据请求 Host/转发头推断域。会话、CSRF、按代次命名的注销恢复 Cookie 的设置、续期与清除使用同一 Domain，仍保留 HttpOnly/Secure/SameSite 规则；退出不清恢复 Cookie，迟到的完成只清自己的恢复代次。

浏览器会把这些 Cookie 发送给该域及**所有子域服务器**，包括静态托管、CDN 或其他服务；HttpOnly 只限制页面脚本，不阻止服务器收到 Cookie。共享 Domain 不扩大 CORS、Origin、角色或 IP 白名单，不免除 CSRF；前端仍用 `credentials: include`。未登记兄弟 origin 即使携带正确 CSRF Cookie 也不能执行写入。

**切换流程**：先停止写入并完成进行中的账号注销，再备份、部署支持此字段的 BFF，最后发布匹配配置。要求用户在所有曾使用的 API 主机清理旧 Cookie 并重新登录（各域的 host-only Cookie 不能由兄弟域删除）。同名但不同值的 Cookie 会被拒绝，不猜哪份身份或 CSRF 是正确的；相同值允许去重。新登录/CSRF/恢复设置与退出/恢复清除还会在当前响应主机过期同名 host-only Cookie，防止残留。错误响应直接携带的恢复 Cookie 也使用配置 Domain。不能依靠此清理替代多主机切换和重新登录，不自动恢复旧会话或清数据库。

测试：`internal/bff/config/cookie_domain_test.go` 覆盖缺省/合法/拒绝配置；`internal/bff/session/shared_cookie_test.go` 使用标准库 Cookie jar 验证跨 HTTPS 主机的会话、CSRF、恢复续期/代次隔离/清除、退出与域外不携带，以及同名冲突 fail-closed 和未登记兄弟 Origin 拒绝。它不是实际浏览器/厂商托管链路验收；既有 host-only 测试继续保留，生产仍需验证真实浏览器。

## 验证

- `internal/bff/auth`：账号服务（会话解析各状态、注销工作流阶段与重试、错误映射）；`internal/bff/ingress`：会话检查失败保持各自含义（`unavailable` 不变成 `unauthenticated`）、注销 Cookie 的例外、投影钩子；`internal/bff/usersrv`：usersrv 错误映射。
- metro 经 BFF 的链路测试（`internal/e2e/metro_*_chain_test.go`，`ChainMetro`）：匿名目录与 `known_version`、`metro_grpc` 就绪、无 `upstreams.metro` 时无 metro 路由、管理接口的角色要求（匿名 / 无角色 / 其他角色 / 管理员 / 缺 CSRF）、校验与 revision 冲突 detail 抵达浏览器、导入预览与写入、审计渠道与执行者、绕过 BFF 直连 metro 时仅含 `sub` 的凭证被拒、骑行者资料与乘坐记录、云端数据删除同时清理 tripmap 与 metro 且单个产品失败时保持待删除并可重试。
- `internal/bff/accesstoken`：令牌创建/校验/吊销/过期/数量上限/只存摘要（用 `SetClock` 覆盖过期）；`internal/bff/metro/mcp`：工具表与 JSON Schema（读 7、写 13）。MCP 链路测试在 `internal/e2e/metro_mcp_chain_test.go`：令牌与作用域（只读令牌调写工具被拒；写令牌 `metro_import_catalog` 不传 `dryRun` 只预览）、吊销/过期/只带 Cookie 的请求失败、令牌数量上限、MCP 写入与 Web 写入共用 `expectedRevision` 与审计（`channel=mcp`、令牌 ID）、限流、协议（批量、通知、未知方法）。
- `internal/bff/ingress`、`internal/bff/app`、`internal/e2e`：Connect 策略与内部 gRPC（TLS 与 h2c）信任。设备会话与改密的链路测试在 `internal/e2e/device_sessions_chain_test.go`（OzID T3：退出其他设备的保留当前会话、幂等、并发与限流，撤销指定设备的跨账号隔离，改密只结束本账号的会话与管理令牌，资料修改不碰 metro 骑行者资料）。
- **`internal/e2e` `ChainHarness`**（见 [后端初始化](backend.md#生成构建与验证)）：真实 BFF + 真实 usersrv + PostgreSQL/Redis + tripmap/metro，只把邮件远端换成本地收件箱。覆盖：邮件注册（统一应答）、登录失败统一、Cookie 会话与跨设备撤销、改密码/重置密码使全部会话失效、敏感操作要求最近的密码验证、usersrv 不可用时受保护调用 `unavailable` 且业务无调用、角色只能由运维授予并实时生效（撤销后重新授予也不恢复旧令牌）、tripmap 展示投影与好友码、metro 管理接口与 MCP（每次调用重新校验）、注销工作流（每个阶段失败/响应丢失后可恢复、产品清理失败时保持“删除中”、回执 24 小时内可重放、跨用户隔离）、错误 Origin/CSRF、响应体不含会话令牌；`go test ./internal/e2e/ -count=1`。浏览器里的注销界面在 OzID（`apps/ozid` 的 Playwright 与 `internal/e2e` 的 OzID 浏览器验收），山海足迹里没有注销界面（T10 起只提供前往 OzID 的入口），真实 Cookie 链路以 e2e 为准。
- **山海足迹浏览器真实栈（T10）**：`internal/e2e` 的 `TestTripMapBrowserChain`（`TRIPMAP_BROWSER_E2E=1`，需 Postgres/Redis、node 与 Chromium）以 HTTPS 同站点的保留测试域名（`id`/`api`/`trip` 三个 `*.ozid-e2e.test` 名称，浏览器用 `--host-resolver-rules` 映射到本机，临时自签证书，反向代理转到真实 BFF），用 `apps/trip-map/playwright.real.config.ts` 驱动产品与 OzID 的真实跨 origin 登录与返回、缓存与原 mutation ID 重放、A/B 切换的 `X-Kongken-User` 守卫和 CSRF 轮换；CI 的 `tripmap-browser` 任务运行它，不允许跳过。
- **Playwright 真实栈**（默认跳过）：`apps/trip-map/tests/bff-cookie-real.spec.ts`；先 `make dev`（BFF、usersrv、tripmap 等价组合），再 `E2E_REAL_BFF=1 TRIPMAP_DEV_BACKEND=http://127.0.0.1:18081 npm run test:e2e -w @kongken/trip-map -- bff-cookie-real.spec.ts`。CI 与常规 `npm run test:e2e` 仍用 fake backend；本 spec 在本沙箱未对真实栈执行。
- 其余 UI：Playwright 模拟后端覆盖登录骨架与业务场景。

## 当前限制

- 见上文「当前限制」；Butterfly 指标端口 `:2223` 本地双进程冲突同前。

## 账号、会话与注销（P4）

**会话**。登录（`LoginWithEmail`）成功后，BFF 生成新的随机令牌放进默认 host-only（可选显式共享 Domain）、HttpOnly Cookie，库里只存它的摘要；会话行记录用户 UUID、usersrv 的 `auth_version`、真实的密码验证时间（`authenticated_at`）和过期时间。每个受保护请求先用令牌摘要找到会话，再调用 usersrv `GetAuthState`：账号必须 `active` 且 `auth_version` 与会话一致，否则会话被撤销并返回 `unauthenticated`；usersrv 不可用则返回 `unavailable`，不转发。改密码或重置密码会撤销该用户的全部会话。`Reauthenticate` 用 usersrv 的 `VerifyPassword` 刷新 `authenticated_at`；管理接口（`required_role`）、创建管理令牌、注销与创建管理令牌要求最近一次验证（管理接口见 `auth.admin_session_max_age`，其余 15 分钟）；改密码要求提交当前密码，由 usersrv 校验。

**设备会话（OzID T3）**。`ListSessions` 只返回本人未撤销且未过期的会话行里真实存在的字段（创建、最近使用、过期时间、是否当前）。`RevokeSession` 按“会话 ID + 调用者用户 ID”匹配，别人的和不存在的 ID 同为 `NOT_FOUND`（同一文案），已撤销的重复撤销成功，撤销自己的会话同时清 Cookie。`RevokeOtherSessions`（登录后，写：CSRF + Origin）用**一条** `UPDATE ... WHERE user_id = ? AND id <> 当前会话 AND NOT revoked AND expires_at > now` 撤销本人其余有效会话，返回撤销条数：幂等，并发调用合计恰好撤销每行一次，不动 `auth_version`、密码、管理令牌，之后新建的会话不受影响；按账号限流（Redis，每 15 分钟 10 次，`RESOURCE_EXHAUSTED`，Redis 故障 `UNAVAILABLE`），被拒绝的调用不撤销任何会话。改密码要求当前密码并受 usersrv 的账号级限流（默认每 15 分钟 10 次，与 `Reauthenticate` 共用），成功后撤销本账号全部会话并清当前 Cookie。

**在途窗口**。BFF 对 usersrv 的 `GetAuthState` 在每个请求开头检查一次，通过后签发的内部凭证有效期最长 60 秒（`servicetoken.Lifetime`，另有 10 秒时钟偏差容忍）：在这之前已经发往产品服务的请求，即使账号随后被禁用、改密或撤销角色，仍可能在该窗口内执行完毕——不宣称即时中断。登录与改密并发时，会话记录的是登录时读到的 `auth_version`，之后版本变化的会话在下一次检查就失效。

**错误**。usersrv 的认证失败返回给浏览器的是 `unauthenticated` 与固定提示；BFF 自己的服务凭证被拒（签名或配置错误）是 `internal`，不会被当成“请重新登录”。其余业务错误保留 usersrv 写好的固定文案。

**注销工作流**（`bff_account_deletions`，按开始注销的那个会话令牌的摘要而不是会话 ID 记录，因为 `BeginDeletion` 会递增 `auth_version`，使自己的会话立即失效）：`DeleteAccount`（确认词 `DELETE` + 幂等键）先落库，再依次执行 `begin`（usersrv `BeginDeletion`：账号变为 deleting，所有会话和 MCP 令牌失效）→ `cleanup`（工作流创建时固定的产品清单，逐个调用幂等的 `CleanupUserData`，含 S3）→ `finish`（usersrv `FinishDeletion`，留 24 小时 tombstone）→ `local`（一个事务内删除本地会话与管理令牌、记录完成与回执过期时间）。每个阶段都可重放：失败只记录次数、错误与退避时间，账号保持“删除中”，调用返回 `unavailable`；`RetryAccountDeletion`（只有发起注销的那个 Cookie 可用，仅凭工作流 ID 不行）与后台每 30 秒一次的恢复任务都能继续，数据库租约保证同一时刻只有一个执行者。回执保留 24 小时，之后被清理。清单中缺少某个产品的清理器是错误，不会跳过。注销恢复凭据是专用的、按凭据代次隔离的 `kongken_del_<令牌摘要前 16 位十六进制>` Cookie（值为开始注销的会话令牌，沿用上述摘要绑定，名字与值同生共死、永不改变；默认 host-only、可选同会话 Domain、HttpOnly、生产 Secure、SameSite=Lax、30 天、读取未完成注销时同名同值续期），只有 `DeleteAccount`、`GetAccountDeletion`、`RetryAccountDeletion` 读取，不能认证、也不能访问另一个账号。读取时只接受名字与值对得上的 Cookie，去重后最多 8 个候选（最多扫描 64 个 Cookie 对）；上一版本地构建用过的固定名 `kongken_del` 只作为候选被采纳，并在对应响应里换成新代次后清掉，不再写入。不可逆工作流只在浏览器已回传本会话那一代的恢复 Cookie 之后才启动：新注销的第一个 `DeleteAccount` 只设置它并以 `FAILED_PRECONDITION`（“account deletion recovery cookie is not stored yet; repeat the request”）回答，重复调用才落库开始。状态与重试按恢复 Cookie 找工作流（没有时退回旧固定名与会话 Cookie 作候选，并补发新代次的 Cookie，过渡升级前已开始的注销），不看当前登录的是谁；多个候选时取幂等键相同的、否则最早的未完成的、再否则最近的回执；同一浏览器里不同幂等键的未完成注销拒绝开始新的（`FAILED_PRECONDITION`，“an account deletion started in this browser has not finished”）。**三个注销 RPC 的任何响应都不改写 `kongken_sid`**（读取、失败、开始、完成、回执重放都一样），依靠服务端撤销使被撤销的会话失效；`Set-Cookie` 没有按值比较的清除语义，把清 `kongken_sid` 挪到开始或完成响应里仍会被迟到的响应覆盖掉后来登录的账号。`LoginWithEmail`、`Logout`、`RevokeSession`、`ChangePassword` 与 `Me` 不改恢复 Cookie；完成（含回执重放）只清除该代次的 Cookie；找不到任何工作流的凭据（回执已过期或被清理、从未开始）的响应**不写任何 Cookie**，这样的 Cookie 是惰性的，随自己的 `Max-Age` 过期。迟到的任何响应只能作用于它自己的代次或什么都不写，不再能带走别的凭据。ingress 对已撤销会话的例外（`Policy.DeletionKnown`）没有 `X-Kongken-User` 可比较，隔离依据是 Origin/CSRF/幂等键的写入保护加上“凭据摘要命中已持久化工作流且幂等键相同”。恢复 Cookie 是单一浏览器里的凭据：清除站点 Cookie 或换浏览器就看不到进度（注销仍由后台完成）。OzID 的 `/close-account` 页面依赖这一点（见 [OzID 功能描述](ozid/features.md#账号注销t7)）。

**重试工具**：运维没有单独的命令行；排查时可对工作流行读取 `stage`、`attempts`、`last_error`，后台任务会自动重试（间隔 30 秒起指数退避，最长 15 分钟）。

**限制**：登录入口只有 `bff.v1.AuthService`：旧的 `/auth/login`、OAuth 回调和 `/auth/logout` 等 HTTP 路径一律 404（没有重定向，也不设 Cookie），已删除的 OAuth RPC 不再存在（`internal/e2e/logto_retired_chain_test.go` 在真实链路上验证）。没有对真实邮件厂商实测。手动退役脚本不会由服务启动自动执行。

## 多 Web App 登录与跨域

账号 Cookie 默认 host-only，可选共享 Domain 不改变精确的 `cors_allowed_origins`：精确允许 Origin 的正常、错误与 OPTIONS 响应均带 Allow-Origin、Allow-Credentials 与 Vary，预检包含 Connect/CSRF/区域头及 X-Kongken-User；不允许 `*` 或任意回显。读取授权不会免除写入的 Origin/CSRF 验证。CSRF token 从 IssueCSRFToken 响应体取得，并在凭据请求中放入 X-CSRF-Token（不要求前端读取 BFF Cookie）。退出只撤销当前 BFF 会话；其他设备会话保留。新注册验证与重置邮件的链接一律指向 usersrv 的 `auth.apps.ozid.origin`（中央 OzID），不由请求决定；请求里的应用 ID 只是返回上下文。`auth.apps` 里其余条目只服务升级前签发或已排队的旧邮件（见 [OzID 功能描述](ozid/features.md#邮件流程t2)）。

注销恢复 Cookie 与 CSRF/Origin：`kongken_del_*` 不参与认证；`DeleteAccount`、`RetryAccountDeletion` 仍要求允许的 Origin 与成对 CSRF，`GetAccountDeletion` 是匿名读；`Set-Cookie` 在成功与错误响应里都由同一套精确 CORS（`Allow-Credentials`）送达。

共享 Cookie 换账号保护：页面为已确定身份的请求携带 `X-Kongken-User`，足迹引擎把该值绑定到自身账号。BFF 在同一请求内解析 Cookie 后比较预期 ID，不相符即 unauthenticated，旧缓存不会写入新账号。该头只能收窄会话身份，不能代替认证或选择其他账号；Me 恢复时不发送它。未升级客户端不发送该头仍按原会话认证，但不能获得这一并发账号切换保护，运营方应同步升级各共享 Cookie 的应用。

## 导航注册表与 `ResolveNavigation`（OzID T1）

`bff.v1.AuthService/ResolveNavigation` 是匿名、只读的过程（不要求 CSRF，不读取或建立会话，不签发票据）：请求为稳定 `app_id` 与可选的 `return_path`，响应 `return_url` 由配置 `navigation.apps.<id>.origin` 加已校验的 pathname 拼成，另含 OzID 的 origin。客户端给出的绝对地址、`//`、反斜线、点段、百分号编码、query、fragment、未登记路径或未知应用都得到 `INVALID_ARGUMENT`；未配置 `navigation` 时得到 `FAILED_PRECONDITION`。

配置段（`config.bff.example.yaml`）：`navigation.api_origin` 与 `navigation.apps`（`ozid` 必需；ID 取 `ozid`、`admin`、`trip-map`、`metro`；每项 `origin`、可选 `default_path` 与 `paths`）。BFF 启动时校验并在出错时拒绝启动：origin 必须精确且在 `cors_allowed_origins` 内、`auth.cookie_secure` 为 true 时必须 https、各 origin 与 `api_origin` 须在同一 schemeful site、origin 不重复。**同站点不等于授权**：未登记的兄弟子域名拿不到 CORS，写请求也过不了 Origin/CSRF 校验（`session_test`/`app_test` 与浏览器测试有覆盖）。Cookie 默认 host-only；只有显式配置并通过校验的 `auth.cookie_domain` 才设置 `Domain`，不因同站点自动扩大作用域。

**注册与重置的返回上下文（T2）。** `RequestRegistration` / `RequestPasswordReset` 的请求可带 `app_id` 与 `return_path`：BFF 在调 usersrv 之前用注册表按 T1 的同一规则校验（不合法为 `INVALID_ARGUMENT`，不创建挑战、不发邮件；没有配置注册表时，携带非空应用 ID 或路径的申请返回 `FAILED_PRECONDITION`，同样发生在调用 usersrv 之前，无上下文的申请照常处理）；`CompleteRegistration` / `ResetPassword` 的响应带回挑战保存的上下文，BFF 出口处再校验一次，应用已下线或路径不再允许就静默丢弃。完成接口不建立会话。因为产品的账号弹窗默认带自己的应用 ID，发布 T2 时 BFF 必须配置 `navigation`。邮件链接指向 usersrv 的 `auth.apps.ozid`，与申请来自哪个应用无关。

详细规则、页面与验证分层见 [OzID 功能描述](ozid/features.md)。

## 管理周界与用户查询（OzID T4）、账号状态与角色修改（T5）

`bff.v1.UserAdminService/ListUsers`、`GetUser`（只读；`required_role = "ozid-admin"`）是浏览器可见的第一批新增管理接口，前面有一道“周界”（`internal/bff/adminguard`，配置段 `admin`）：

- **检查项**：来源 IP 在 `admin.allowed_cidrs` 内、`Origin` 恰好等于 `navigation.apps.admin.origin`、有效会话、平台角色。四项各自独立，且读方法虽免 CSRF，但**不免**这四项。周界先于 CORS 与 CSRF 运行，对任何方法（含 `OPTIONS` 预检）生效；被拒绝时 `servePublic` 返回不带 CORS 许可的 403，拦截器层（纵深）返回 `PERMISSION_DENIED "request rejected"`，两者都不说明是哪一项失败。
- **白名单与客户端地址**：空白名单（或没有注册控制台）拒绝所有请求；CIDR 必须带前缀、无主机位，IPv4 不比 `/8` 宽、IPv6 不比 `/16` 宽，不接受 IPv4 映射网段，配置校验失败启动即失败。客户端地址默认是 TCP 对端；只有对端属于 `admin.trusted_proxies` 才读取 `admin.forwarded_header`（默认 `X-Forwarded-For`，多行合并，从右向左跳过受信代理），无法解析的跳点拒绝。它独立于 `client_ip_header`（后者只服务分享/退订限流）。
- **受周界保护的服务（T8 起）**：`internal/bff/app/admin_perimeter.go` 的 `adminServices` 列出 `bff.v1.UserAdminService`、`bff.metro.v1.AdminService`、`bff.v1.AccessTokenService`。这三个服务的所有浏览器请求（读、写、预检、`/api/<服务>/` 下的路径写法）都先过上述周界；`Policy.Perimeter`（拦截器）与 `servePublic`（HTTP 边缘）共用同一份列表。`TestEveryRoleGatedBrowserServiceIsInsideTheAdminPerimeter` 枚举注册表里所有 `bff.` 前缀且带 `required_role` 的服务，漏列即失败。公开目录、骑行者接口和 MCP 不在其内：`/mcp/metro` 是 Bearer 令牌的机器协议，独立来源白名单是 T9。
- **角色**：BFF 入口按会话角色判断 `required_role`（并要求管理会话的认证时效 `auth.admin_session_max_age`），usersrv 在内部凭证与读取事务里再次独立判断。角色只在声明了 `required_role` 的方法的内部凭证里传递。
- **限流与审计**：按操作者限流（Redis，默认每分钟 120 次，`admin.rate_limit_per_minute`，限流器故障 `UNAVAILABLE`）；每次查询写脱敏日志 `admin user query`；usersrv 在读取事务里写 `security_audit`（`admin.users.list/get`，原因只记查找类型）。BFF 只适配内部契约，不读 usersrv 数据库。
- **T6 审计读取**：`ListSecurityAudit` 登记在只读清单里（无 CSRF），其余检查与用户查询相同（来源、控制台 Origin、会话、`ozid-admin`、`AdminSessionMaxAge` 内的密码认证），共用读取限流；筛选与翻页令牌原样转给 usersrv，BFF 不解析、不读 usersrv 数据库；日志 `admin audit query` 只记 request_id、操作者、筛选的种类（如 `user+action`）、是否翻页、条数与结果，不记任何筛选值。只提供 usersrv 账号审计，不聚合 metro 审计或管理令牌日志。规则见 [控制台功能描述](admin/features.md)。
- **T5 写接口**：`DisableUser`、`EnableUser`、`GrantMetroAdmin`、`RevokeMetroAdmin`（请求只有目标 ID、目标 `auth_version`、变更 ID、原因）不在只读清单里，所以除上述四项外还要过 CSRF，并要求**最近 15 分钟内的真实密码认证**（`session.SensitiveAuthMaxAge`；过旧返回 `FAILED_PRECONDITION`，由页面引导去 OzID 验证后回来，旧请求不重放）。操作者 UUID 取自已验证会话的内部凭证，浏览器无法指定；BFF 写入限流键 `admin:users:write:<用户ID>`，默认每分钟 30 次（`admin.write_rate_limit_per_minute`）。事务、幂等回执、冲突与审计都在 usersrv（`account.ApplyAdminChange`），BFF 只做适配与错误映射；日志 `admin user change` 记 request_id、操作者、动作、目标 ID、`mutation_id`、结果、`changed` 与 `replayed`（不记原因文本）。授权在 usersrv 事务开头检查一次：已通过后才发生的撤权不撤回在途写入（父规格 #77 验收 21），撤权之后到达的请求包括回执重放都被拒绝。规则见 [控制台功能描述](admin/features.md)。
- **范围**：这一周界覆盖用户管理读写接口、控制台页面，以及（T8 起）metro 管理接口（`bff.metro.v1.AdminService`）与管理令牌接口（`bff.v1.AccessTokenService`），三者共用同一份来源白名单与控制台 Origin；MCP（`POST /mcp/metro`）不在这份白名单内，它有自己的机器白名单 `mcp.allowed_cidrs`（T9，见下一条）。
- **MCP 的独立来源白名单（T9）**：`mcp.allowed_cidrs`、`mcp.trusted_proxies`、`mcp.forwarded_header` 是 `POST /mcp/metro` 自己的来源策略，与 `admin.*`（浏览器控制台）是两份互不引用的配置、两个独立对象（各自 `adminguard.NewSource`；代理链解析、CIDR 校验与 IPv4 映射处理复用控制台已验证的同一份代码，`internal/bff/adminguard/source.go`）。检查在 `internal/bff/metro/mcp` 的 `ServeHTTP` 最前面，早于方法检查、Bearer 解析、限流与请求体读取：名单外的任何请求（initialize、ping、tools/list、tools/call 的读/写/导入、批量、通知、非法 JSON、GET、DELETE、OPTIONS）一律得到不带任何提示头的 `403 request rejected`（无 `WWW-Authenticate`、`Allow`、CORS），令牌表、usersrv、限流计数与 metro 都不会被触及；带斜杠的 `/mcp/metro/` 由同一个处理器应答，同样先过周界，不会经路由器的重定向泄露路径。**空名单、或周界没能构建，拒绝所有 MCP 请求**（只关 MCP，其余产品不受影响，启动时写一条 WARN）。无效、过宽（IPv4 宽于 `/8`、IPv6 宽于 `/16`）、全开放、含主机位、IPv4 映射的网段，以及与 `allowed_cidrs` 重叠的受信代理，使 BFF 启动失败，错误信息指明 `mcp.` 段；**这项校验不看是否配置了 metro 上游**：没有 `upstreams.metro`（MCP 不挂载）的 BFF 里写了非法的 `mcp.*`，同样启动失败（`internal/bff/config/config.go` 的 `Validate` 无条件校验，`TestValidateMCPSourceIgnoresWhetherMetroIsConfigured` 覆盖）。客户端地址：对端不在 `mcp.trusted_proxies` 内就是 TCP 对端，不读任何转发头；对端是受信代理时才读 `mcp.forwarded_header`（默认 `X-Forwarded-For`），从右向左跳过受信代理，遇到无法解析的跳点拒绝；`X-Real-IP`、`Forwarded`、`True-Client-IP` 等其他头从不被采信，伪造的转发头进不来。IPv4 映射的 IPv6 客户端地址按 IPv4 匹配。放行之后沿用此前的全部检查（Bearer 摘要、scope、到期与吊销、每次调用向 usersrv 重新读取账号状态、认证版本与角色，所以改密、禁用、角色变更后的旧令牌不会复活），**不**套用浏览器的密码重新认证，也不增加其他身份或管理员规则；工具仍调用同一个 metro `AdminService`。两道周界互不放行：浏览器白名单不放行 MCP，机器白名单不放行浏览器管理接口；Cookie 不能代替 Bearer 令牌；MCP 不设 CORS。解析出的地址同时用于失败认证限流与令牌审计里的客户端地址（取代此前的 `auth.client_ip_header`）。
- **平台管理员的注销保护**：强制点在 usersrv，不在 BFF：`account.BeginDeletion` 在账号行锁内发现 `ozid-admin` 就拒绝，对**所有调用方**有效（BFF 的 `DeleteAccount` 和后台 runner，以及任何持有内部凭证的客户端）；BFF 只负责把拒绝映射成明确错误，并丢弃开始阶段的注销工作流行。产品（山海足迹、地铁足迹）里现有的注销入口走同一接口，同样受保护；OzID 自己的网页注销页（`/close-account`）已由 T7 提供；`DeleteAccount`/`GetAccountDeletion`/`RetryAccountDeletion` 的请求里没有任何指向他人的字段（`internal/bff/auth/deletion_contract_test.go` 守护），所以没有代他人注销的接口。运维处理平台管理员：`usersrv-admin revoke-role -email <地址> -role ozid-admin -operator <姓名> -reason <原因>` 回收角色后，该账号才能像普通账号一样自助注销；强制删除没有网页或 RPC 路径。
- **周界的取舍**：`admin.trusted_proxies` 与 `admin.allowed_cidrs` 不得重叠（启动校验，`admin-web` 容器入口同样校验）。两层对“转发链不可用”的处理不同：BFF 对**无法解析的跳点直接拒绝**，只有没有转发头、或链里全是受信代理时才使用对端（代理自己）的地址，而这个地址不在白名单内（启动校验保证）所以仍被拒绝；Nginx（`real_ip`）遇到无法解析的跳点则**回落到代理自己的地址**再判断白名单。代理地址若在白名单内，任何能经该代理发送畸形头的人都能通过 Nginx 这一层，所以这种组合直接拒绝启动，不靠头格式正确。行为已用真实 Nginx 实测并在容器 CI 里保留反例。
- **测试**：`internal/bff/adminguard`（来源/CIDR/受信代理/转发头单测）、`internal/bff/ingress/perimeter_test.go`、`internal/bff/config`（`TestValidateAdminPerimeter`）、`internal/e2e/user_admin_chain_test.go`、`internal/e2e/metro_admin_perimeter_chain_test.go`（T8：metro 管理与令牌接口的来源、Origin、预检、路径写法、角色，以及上游计数证明被拒请求没有到达业务）、`internal/e2e/metro_mcp_perimeter_chain_test.go`（T9：真实 BFF→usersrv→metro 链上，名单内/外地址、空名单、伪造转发头、13 种协议请求、无效令牌与只读 scope 调写工具，计数证明被拒请求没有执行任何东西，且与浏览器白名单互不放行）、`internal/bff/metro/mcp/perimeter_test.go`（16 种请求在名单外被拒且零副作用）、`internal/e2e/metro_mcp_chain_test.go` 的 `TestChainMCPTokenIsRevalidatedOnEveryCall`（显式 `mcp.allowed_cidrs`：先过周界，名单外同一令牌得到周界的 403 且不到 metro；名单内改密、禁用、角色撤销/重授、注销后旧令牌不复活）；CI 的“MCP source perimeter”步骤以 `-v` 单独执行这些用例，要求每个用例实际 `PASS` 且没有 `SKIP`（`.github/workflows/ci.yml`）、`internal/e2e/user_admin_write_chain_test.go`（四种角色、控制台 Origin、来源策略与预检、查询/分页/审计、限流、角色生命周期与注销保护）、`internal/e2e/admin_browser_test.go`（真实浏览器，`ADMIN_BROWSER_E2E=1`）。

规则、配置、镜像与验证分层见 [控制台功能描述](admin/features.md)。

## 集中账号的来源策略与旧邮件过渡（OzID T12）

`internal/bff/app/account_origin.go` 在 Connect 拦截器里按过程名判断来源：`AuthService` 与 `UserService` 除 `Me`、`IssueCSRFToken`、`ResolveNavigation`、`Logout` 外的全部过程，要求恰好一个 `Origin` 头且等于 OzID 的 origin，否则 `permission_denied`（`request rejected`）。判断按服务名进行，新增的账号过程自动被覆盖，`TestEveryBrowserServiceHasASourceRule` 要求每个浏览器服务属于账号、管理或产品三份列表之一。**提供账号服务（配置了 `upstreams.usersrv`）的 BFF 必须有导航注册表（`navigation.api_origin`）与 `navigation.apps.ozid.origin`，缺失则配置校验失败、拒绝启动（Secure Cookie 模式同样），没有“不受限”的绕过开关**；不提供账号服务的 BFF（纯业务上游）不需要，也没有账号过程要判断。纯本地前端不使用 BFF，不受影响。

`auth.legacy_mail_transition`（`origins`、`until`）：产品自己的旧邮件页（T12 起）只向 OzID 交接，不调账号过程；此例外只为**升级前构建**仍在自己页面完成旧链接的产品镜像：`until` 之前这些产品 origin 可以调用 `CompleteRegistration` 与 `ResetPassword`，其他账号过程仍被拒。`until` 为 RFC 3339，加载时不得晚于现在 7 天；已过期时 BFF 仍启动，拒绝这些 origin 并写警告。清单见 [整体验收](ozid/acceptance.md#旧邮件-ttl-过渡清单)。

产品镜像与示例网关不再代理同源 `/api/*`（只留分享、通知、底图与探针路径）。真实网关与浏览器的整体验收见 [整体验收](ozid/acceptance.md)。
