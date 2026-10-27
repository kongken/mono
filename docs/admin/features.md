# 统一管理控制台：功能描述（T4 受控控制台与用户查询，T5 账号状态与角色修改，T6 账号安全与管理审计查询，T8 metro 目录管理）

本文描述 [OzID 规格](../ozid/spec.md) 中 [Issue #82（T4）](https://github.com/kongken/mono/issues/82) 已实现的行为：独立的管理控制台 `apps/admin`、它的受控入口（来源网络、控制台 Origin、会话与平台角色），以及用户查询；[Issue #83（T5）](https://github.com/kongken/mono/issues/83) 在其上加入普通账号的禁用/启用与 `metro-admin` 的授予/撤销（见“账号状态与角色修改”）；[Issue #84（T6）](https://github.com/kongken/mono/issues/84) 加入账号安全与管理审计查询页（见“账号安全与管理审计查询（T6）”）。[Issue #86（T8）](https://github.com/kongken/mono/issues/86) 把 metro 管理功能迁入控制台并把 metro 的浏览器管理接口与管理令牌接口放进同一道周界（见“metro 目录管理（T8）”）。[Issue #87（T9）](https://github.com/kongken/mono/issues/87) 给 MCP 加了独立于这道周界的机器来源白名单（`mcp.allowed_cidrs`，见 [BFF 文档](../bff.md)）。**尚未实现**的部分见文末“当前限制”。文中域名与地址全部是保留示例（`admin.example.com`、`203.0.113.0/24` 等），不代表任何真实部署。

## 现状与范围

| 已实现（T4、T5、T6、T8、T9） | 尚未实现 |
|---|---|
| `apps/admin`（`@kongken/admin`）：独立构建、独立镜像 `admin-web`；匿名跳 OzID 登录并返回；无角色显示“没有权限”（不跳转、不循环）；按真实角色显示分区；“管理 OzID 账号”链接到 OzID | 全站统一审计（metro 目录审计与管理令牌调用日志各自留在原处） |
| BFF `bff.v1.UserAdminService` 与内部 `usersrv.v1.UserAdminService`：`ListUsers/GetUser`（只读）、T6 的 `ListSecurityAudit`（只读，账号审计）、T5 的 `DisableUser/EnableUser/GrantMetroAdmin/RevokeMetroAdmin`（仅这四个动作）；`required_role = "ozid-admin"`，BFF 入口与 usersrv 各自独立校验 | 网页上授予/撤销平台角色、改平台管理员的状态或角色、设置他人密码、代登录、代注销、批量或任意角色替换（没有对应接口） |
| 管理面“周界”（`admin` 配置）：来源 IP 白名单、受信代理链、精确控制台 Origin；BFF 对每个管理读接口独立检查，网关镜像对页面、静态资源和 SPA fallback 同样限制 | —（MCP 是机器协议，**不在**浏览器管理周界内；它由独立的 `mcp.allowed_cidrs` 保护，T9 已实现） |
| 平台角色 `ozid-admin`：只能由运维 CLI `usersrv-admin grant-role/revoke-role` 授予/撤销，授予不随首位注册用户，不继承也不授予 `metro-admin`；每次变更提升 `auth_version` 并写审计 | |
| 平台管理员账号从引入起**不能被网页自助注销**（`BeginDeletion` 拒绝）；权限收回后也不会被之前被拒的注销自动关闭 | |

## 角色与可见内容

控制台按**真实角色**显示分区；这只决定“显示什么”，每一次调用都由服务端再判断一次，隐藏菜单不是授权。

| 账号 | 首页 | `/users`、`/users/:id`、`/audit/users` | 调用管理 API |
|---|---|---|---|
| 无角色 | “没有权限”页（可退出换账号、可去 OzID），不跳转 | “没有权限”，不发起任何用户查询 | `PERMISSION_DENIED` |
| 只有 `ozid-admin` | 用户与账号审计分区；`/metro/*`：“没有权限”，不发起任何 metro 调用 | 可用 | 用户管理允许，metro 管理 `PERMISSION_DENIED` |
| 只有 `metro-admin` | “地铁目录”分区（T8；没有 metro 上游时不显示分区） | `/users`、`/audit/users`：“没有权限” | 用户管理 `PERMISSION_DENIED`（**不**继承），metro 管理允许 |
| 两个角色都有 | 用户、账号审计与“地铁目录”分区 | 可用 | 允许 |

匿名访问任何页面：控制台整页跳到 `OzID /login?app_id=admin&return_path=<当前页>`，登录后由 BFF 注册表（`navigation.apps.admin`）决定返回地址。未知地址（不是 `/`、`/users`、`/users/<UUID>`、`/audit/users`、`/metro` 及其下的 metro 页面）显示控制台自己的“没有这个页面”，不要求登录、不展示数据。会话查询本身失败（网络、服务不可用）显示故障和“重试”，**不当成已退出**。

## 页面与交互

- `/`：角色与可用分区。`/users`：筛选 + 表格 + 分页；`/users/:id`：账号详情，普通账号在此有“状态与角色”操作（T5），并有“查看此账号的审计记录”链接（T6）；`/audit/users`：账号审计（T6）。
- 筛选：邮箱精确（规范化为小写；不是前缀/包含）、用户 ID、昵称（字面包含，不区分大小写，`%`、`_` 是普通字符，1–40 字符）、状态、角色。邮箱/用户 ID/昵称**最多选一种**。筛选条件只保存在组件状态里，**不写入地址栏、历史或浏览器存储**。
- 分页：服务端按“创建时间倒序、ID 倒序”稳定排序；默认 50 条，上限 100，负数拒绝；翻页令牌不透明（页面不解析，不展示），“上一页”由页面保存已走过的令牌栈。较慢的旧响应不会覆盖较新的结果（最新请求才生效）。
- 返回字段只有：用户 ID、邮箱、昵称、状态（正常/已禁用/注销中）、角色、注册时间，以及供修改使用的目标认证版本 `auth_version`（T5）。**没有**密码哈希、令牌、会话、产品私有数据，也没有批量导出。
- 错误按含义显示：会话失效 → 回 OzID 登录；密码确认过旧（`AdminSessionMaxAge`，默认 12 小时内的认证）→ 提示并给“前往 OzID 验证密码”按钮（不自动跳转，所以不会循环，也不重放输入）；缺角色 → “没有权限”；限流、服务不可用 → 可重试提示。**来源不在白名单**时，浏览器的预检/调用被 BFF 以不带 CORS 许可的 403 拒绝，页面只能看到“无法连接服务器”（浏览器不会把被拒绝的跨域回答交给脚本）；而在网关处，这样的来源连页面都拿不到。

## 受控入口（周界）

三道彼此独立、规则一致的防线，缺一不可：

1. **网关/代理**（运维自管，示例见 `deploy/reverse-proxy/Caddyfile.example`）：对控制台主机做来源限制。
2. **`admin-web` 镜像的 Nginx**：控制台页面、静态资源、SPA fallback、甚至 404 都在来源白名单之后；来源不在名单的请求对**所有路径**得到 403（正文只有 `forbidden`，不带控制台内容，也不通过 404/403 差异暴露文件是否存在）；`Host` 必须恰好是 `ADMIN_HOST`（别的名字或带端口一律不回应）；`/healthz` 只在默认 server 上应答，不在控制台主机上（Nginx 的 `return` 先于访问检查，放在控制台 server 上等于对所有来源开门）。
3. **BFF**（`admin` 配置）：对每个新增的用户管理读接口独立检查**来源 IP、控制台 Origin、有效会话、平台角色**；周界检查在 CORS 与 CSRF 之前运行并对任何方法（含 `OPTIONS` 预检）生效：被拒绝时只返回不带 CORS 许可的 403，不说明是哪个条件失败。

规则：

- 白名单为空（或没有注册 `navigation.apps.admin`）= **全部拒绝**，不会自动放开。CIDR 必须带前缀、无主机位，IPv4 不得比 `/8` 宽、IPv6 不得比 `/16` 宽，不接受 IPv4 映射的 IPv6 网段；`0.0.0.0/0`、`::/0`、通配符启动即被拒绝（BFF 配置校验，容器入口脚本同样校验）。
- 客户端地址：默认就是 TCP 对端。**只有**对端属于 `trusted_proxies` 时才读取转发头（默认 `X-Forwarded-For`，多行合并），从右向左跳过受信代理跳点，取第一个不受信地址；无法解析的跳点拒绝；没有转发头则代理本身就是客户端。伪造转发头、`X-Real-IP`/`Forwarded` 等其他头、另一个 Host/端口或另一个代理都绕不过去。代理必须**追加**而不是透传客户端的值（Caddy 示例用 `header_up X-Forwarded-For {remote_host}` 覆盖）。
- **`trusted_proxies` 与 `allowed_cidrs` 不得重叠**（BFF 配置校验与 `admin-web` 容器入口各自在启动时拒绝，任一方向的包含都算）。原因：两层对“转发链不可用”的处理不同——BFF 对无法解析的跳点直接拒绝，但 Nginx（`real_ip`）遇到不是地址的跳点会**回落到代理自己的地址**再判断白名单。实测（Nginx 1.26，代理网段同时在白名单里）：`X-Forwarded-For: garbage`、`<白名单内地址>, garbage` 和没有该头都得到 200，等于任何经过该代理的人都能打开控制台。两个列表分开之后，回落地址（代理自己，或全是代理的链里的最左跳）永远不在白名单内，所有不可用链都落在 403。分开后的实测（真实 Nginx，`scripts/admin-web-smoke.sh` 的容器 CI 保留这些反例）：`garbage`、`unknown`、`<白名单内地址>, garbage`、`<白名单内地址>, <代理>, garbage`、只有代理、`<地址>:80`（外部地址）、带尾随文字、第二行头里的 `garbage` 都是 403；最后一跳有效时其左边的垃圾不会被读到，仍按最后一跳判断（200，BFF 同样）。两层在边角上不完全相同（Nginx 接受 `地址:端口` 和尾随逗号而 BFF 拒绝；Nginx 拒绝前导逗号而 BFF 只看最右一跳），因为浏览器请求必须同时通过两层，所以只会更严，不会更松。
- Origin 必须**恰好等于** `navigation.apps.admin.origin`（单个值）。产品（山海足迹、地铁足迹）在公共 CORS 列表里也不能调用管理数据；缺失或伪造 Origin 不是认证，仍须同时通过 Cookie、来源和角色。读方法免 CSRF，但**不免**来源、Origin、角色。
- 受影响的现有功能：登录、注册、账号设置、产品匿名与普通功能都不在周界内，来源不在名单时照常可用（浏览器验收实测）。
- T4 落地时**没有**给 metro 的 `AdminService`/`AccessTokenService` 加来源限制，以免悄悄关闭当时的维护入口；它们已在 T8 随页面迁移并入这道周界（见“metro 目录管理（T8）”）。MCP 仍不在其内。

## 平台角色 `ozid-admin`

- 只通过运维 CLI 授予/撤销：`usersrv-admin grant-role -email E|-user-id U -role ozid-admin -operator NAME -reason TEXT`，`revoke-role` 同理（没有单独的 `init` 命令：第一个平台管理员也是这样授予；**不会**因为谁最先注册而自动授予）。每次变更提升账号 `auth_version`（旧会话和管理令牌在下次检查失效）并写 `security_audit`（含被拒绝和未改变的尝试）。
- `ozid-admin` 与 `metro-admin` 相互独立：持有一个不意味着持有另一个，撤销一个不影响另一个。
- usersrv 迁移 `migrations/usersrv/20261025120000_platform_admin_role.sql` 把 `user_roles` 的角色检查约束扩展为 `('metro-admin','ozid-admin')`；回滚前必须先撤销所有 `ozid-admin`，否则旧约束会拒绝这些行。
- 平台管理员保护：**强制点在 usersrv，对所有调用方有效**——`BeginDeletion` 在账号行锁内发现 `ozid-admin` 即拒绝（原因 `PLATFORM_ADMIN_PROTECTED`，审计 `deletion.begin=refused_platform_admin`，账号状态与 `auth_version` 不变），BFF 的 `DeleteAccount`、后台 runner 和任何持有内部凭证的客户端都一样。BFF 只做两件事：把拒绝映射成明确错误；在开始阶段遇到这个永久拒绝时丢弃该工作流行，所以之后即使撤销角色，账号也不会被之前的请求自动关闭。产品（山海足迹、地铁足迹）里现有的注销入口走同一个 `DeleteAccount`，同样受保护。OzID 自己的网页注销页（`/close-account`）已由 T7 提供，对平台管理员给出同样的明确拒绝与“联系运维”说明（见 [OzID 功能描述](../ozid/features.md#账号注销t7)）。共享前端包 `@kongken/account` 的错误分类已把这个拒绝识别为独立的 `platform_admin` 类（给出“请联系运维先回收管理员角色”的明确提示）。

## 账号状态与角色修改（T5）

只有四个动作：禁用/启用普通账号、授予/撤销 `metro-admin`。没有任意角色字符串、没有整个角色数组替换、没有批量。

**界面。** `/users/:id` 的“状态与角色”区按目标当前情况给出按钮；点击打开确认框，显示目标（昵称、邮箱、用户 ID）、现在与之后的状态/角色、影响清单，以及必填原因（1–200 字符，拒绝控制字符与明显的密码/令牌/邮箱，服务端按去掉首尾空白后的内容再校验）。平台管理员的详情页只显示“网页上不能修改它的状态或角色，请联系运维使用命令行处理”，注销中的账号只显示“这个账号正在注销，只能查看”，都没有按钮；已禁用账号不能被授予角色（需先启用）。

**一次写请求要同时通过：** 来源白名单 → 控制台 Origin → 有效会话 → 当前持有 `ozid-admin` → CSRF → 最近 **15 分钟**内的真实密码认证（比读取严格；`AdminSessionMaxAge` 仍是 12 小时只作用于读取）。密码确认过旧时返回“请重新输入密码”：确认框给出“前往 OzID 验证密码”按钮，验证后回到原页面，**旧的写请求不会被自动重放**，管理员需要重新点击。BFF 对写入另有限流（键 `admin:users:write:<用户ID>`，默认每分钟 30 次，`admin.write_rate_limit_per_minute`）。

**操作者与时间不由浏览器给出。** 操作者 UUID 来自 BFF 已验证会话的内部凭证；请求体只有目标 ID、目标认证版本、变更 ID、原因。usersrv 在事务内再次确认操作者仍正常且持有 `ozid-admin`，并独立检查目标约束，不信任 BFF。

**一个事务内按顺序完成**（`account.ApplyAdminChange`）：确认操作者 → 对目标行 `FOR UPDATE`（与 CLI 授予/撤销/禁用、`BeginDeletion` 是同一把锁，所以 CLI 同时授予平台角色不可能绕过网页禁用的保护：谁先拿锁，另一方就按提交后的状态被拒绝）→ 查本操作者的幂等回执 → 目标持有 `ozid-admin` 则拒绝 → 目标 `auth_version` 与请求不符则拒绝（需刷新）→ 动作自己的约束（注销中的账号四个动作都拒绝，所以**不能用“启用”取消注销**；授予要求账号正常且邮箱已验证）→ 更新状态/角色 → **仅在确实发生变化时** `auth_version` +1 → 写回执与审计。

**幂等与冲突。**

- 请求带 `expected_auth_version` 与 `mutation_id`。版本不符返回 `ABORTED`（`TARGET_CHANGED`），页面提示“账号已被其他人修改”，只提供“刷新”，刷新后重新决定。
- 同一操作者、同一变更 ID、**同样内容**（动作、目标、版本、原因）返回原结果（`replayed=true`，不再改动，不再提升版本）。这个结果是**第一次请求当时**的快照（状态、角色、`auth_version`、是否发生变化），不是账号现在的状态，也不是一次新的变化；内容不同返回 `ALREADY_EXISTS`（`MUTATION_REUSED`）。回执保存 24 小时、按**操作者隔离**（键是 操作者+变更 ID），另一个管理员使用同一个 ID 互不影响。回执即使在目标已不存在时也能重放。**重放前先确认操作者当前仍是正常的 `ozid-admin`**：撤权或被禁用之后才到达的请求（包括带着原变更 ID 的重放）一律以 `refused_forbidden` 拒绝，不读取也不使用回执，回执不被改动；重新获得授权后同一回执仍可答复。回执在恰好 `expires_at` 的时刻即视为过期（早一个时间刻度仍有效），过期后该变更 ID 可以被新请求使用。页面在响应丢失后用同一个变更 ID 重试，原因改动后换新 ID。被拒绝的请求不留回执，可以刷新后重做。
- 已经处于目标状态的请求（再次禁用已禁用账号、再次授予已有角色等）结果为 `unchanged`：不提升版本、不使旧会话失效，审计分类为 `unchanged`；重试已完成的请求也不会再次提升版本。
- 启用或重新授予**不会让旧会话和管理令牌复活**：禁用与撤销已经提升了 `auth_version`，启用不会回退它，旧凭证按版本一直失效；账号需要重新登录，令牌需要重新创建。授予角色同样提升版本，所以账号会话重建后才带新角色。

**审计。** 每次尝试在同一事务内写一行 `security_audit`：真实操作者（`actor_user_id`）、目标、动作（`admin.account.disable`、`admin.account.enable`、`admin.role.grant:metro-admin`、`admin.role.revoke:metro-admin`）、原因、变更 ID、渠道（网页为 `web`，CLI 为 `cli`；T6 起显式存储，另有 `self`、`system`，见 T6 一节）、变更前后的状态与角色（发生变化的那次操作有真实的前后值；`unchanged` 行与目标已读取之后才被拒绝的行记录读到的前后值，更早被拒绝的行为空；`replayed` 行为空，见下）、请求关联 ID、时间，以及结果：`ok`、`unchanged`、`replayed`、`refused_forbidden`、`not_found`、`refused_target_protected`、`refused_version_conflict`、`refused_not_active`、`refused_mutation_reuse`。不含密码、令牌或邮箱明文的查询内容。

**重放的审计语义。** `replayed` 行只表示“把原结果重新送了一遍”：它有真实操作者、目标、动作、原因和变更 ID，但 `before_*`、`after_*` 一律为空——回执快照既不是账号当前状态，重放也不是一次新变化，所以不在这一行里描述状态。要看那次真实变化，**不能**用（`actor_user_id`，`mutation_id`）去推断：回执 24 小时后过期，过期后同一个变更 ID 可被新请求复用；被拒绝的请求不留回执，也可能用同一个 ID。所以同一个（操作者，变更 ID）下可以有多个使用周期，每个周期各有自己的原始记录（`ok`/`unchanged`，或被拒绝的行）和零到多行重放，（操作者，变更 ID）不对应唯一的原始事件。T6 起重放行带 `origin_id`——它答复的那一行原始审计的 ID（由回执在写入时记下）——这是精确关联，见下一节；T6 之前写入的回执没有这个关联，对应重放行 `origin_id` 为空，只能说明“发生过重放”。

**在途请求的授权边界。** 授权在事务开头检查一次（操作者仍是正常的 `ozid-admin`），不对操作者行加锁。事务已通过检查之后，CLI 撤销操作者权限或禁用它，在途的这一次写入仍会完成；这是被接受的边界（[父规格 #77](https://github.com/kongken/mono/issues/77) 验收 21 不承诺撤回已开始的请求），不为此增加更强的串行化。边界只在于“已经开始的”请求：撤权或禁用**提交之后**才开始的请求，包括回执重放，都被拒绝。

**BFF 日志。** `admin user change` 记录 request_id、操作者、动作、目标 ID、变更 ID（`mutation_id`）、结果、是否发生变化（`changed`）和是否重放（`replayed`）；不记录原因文本，也没有邮箱。

**错误映射。**

| 情形 | 内部码 | 页面 |
|---|---|---|
| 目标是平台管理员 | `FAILED_PRECONDITION`（`TARGET_PROTECTED`） | “网页上不能修改平台管理员” |
| 目标已被修改 | `ABORTED`（`TARGET_CHANGED`） | “账号已变化”+刷新 |
| 变更 ID 被不同内容使用 | `ALREADY_EXISTS`（`MUTATION_REUSED`） | 重新发起（新 ID） |
| 注销中/未验证等不允许 | `FAILED_PRECONDITION` | “账号当前不能这样修改” |
| 密码确认过旧 | `FAILED_PRECONDITION`（`bffauth.ErrReauthRequired`） | 前往 OzID 验证并返回 |
| 缺角色 / 来源不允许 / 限流 / 服务不可用 | 与读取相同 | 与读取相同 |

**迁移与数据。** `migrations/usersrv/20261025130000_admin_mutations.sql`：`security_audit` 增加 `channel`、`actor_user_id`、`mutation_id`、`before/after_status`、`before/after_roles`（旧行取空默认值，`channel` 为空即迁移前写入的记录，数据原样保留；T6 的迁移 `20261025140000_audit_query.sql` 把这些空值按下文规则回填）及检查约束；新建 `admin_mutations`（回执，`(operator_id, mutation_id)` 唯一，`expires_at` 索引）。usersrv 的定期清理（`PurgeExpired`）同时删除已过期回执。回滚前不需要数据迁移；回滚只会让新列与新表不再被使用。Ent 代码由 `make ent-usersrv` 生成，Protobuf 由 `make buf` 生成。

## 账号安全与管理审计查询（T6）

`/audit/users` 把 usersrv 已经写入 `security_audit` 的账号安全与管理记录交给平台管理员查看：谁（哪个渠道、哪个操作者）、对哪个账号、做了什么、为什么、结果如何；**真实的状态/角色变化、被拒绝的尝试、没有变化的结果都在里面**。

**范围与边界。** 只读 usersrv 的账号审计。metro 目录审计仍由 metro 服务保存并在 metro 管理里查看，管理令牌的调用日志仍是 BFF 现有范围；没有新的跨库原子事务，也不假装是全站统一审计（页面顶部写明）。`metro-admin` 看不到账号审计，`ozid-admin` 也不因此看到 metro 的目录审计。

**读取要同时通过：** 来源白名单 → 控制台 Origin → 有效会话 → 当前持有 `ozid-admin` → 最近 `AdminSessionMaxAge` 内的真实密码认证（与用户查询一致；是读取，不要求 CSRF）。BFF 方法 `bff.v1.UserAdminService/ListSecurityAudit` 在入口层按只读过程登记，usersrv 再在读取事务里确认调用者仍是正常的 `ozid-admin`（拒绝也写一行 `refused_forbidden`）。读取共用 BFF 的读取限流。

**渠道与操作者（显式存储，不靠读取时猜测）。** `security_audit.channel` 现在对每一行都有值：

| 渠道 | 含义 | 操作者展示 |
|---|---|---|
| `web` | 管理控制台里的修改与管理读取 | 操作者是真实账号（用户 UUID，`actor_user_id`），页面显示为可点开的账号 ID |
| `cli` | 运维命令行（`usersrv-admin`） | 只有受控的运维标识（`actor`，1–64 个可见字符，由 CLI 校验），页面标明“运维标识，不是网页账号”，**不当作任何账号** |
| `self` | 账号本人的流程（登录、改密、注销、创建等） | 账号本人（该行所属账号） |
| `system` | 系统自己 | “系统” |

自由文本的操作者永远不会被当成网页身份：网页身份只来自 `actor_user_id`，CLI 标识只在 `cli` 渠道里作为名字显示。`origin` 之外的展示字段只有：行 ID、时间、渠道、操作者、目标账号 ID、动作、结果、原因（读取层统一判定，见下一段）、请求关联 ID、变更 ID、变更前后状态与角色，以及重放的原始记录摘要。**没有**密码、令牌、完整请求、原始异常、邮箱或产品私有内容；请求 ID 与 CLI 标识展示前再做一次可见字符裁剪。

**迁移回填。** `20261025140000_audit_query.sql` 增加 `admin_mutations.audit_id`、`security_audit.origin_id`、`security_audit.reason_checked`（`boolean NOT NULL DEFAULT false`）和两条索引（`(actor_user_id, created_at)`、`(action, created_at)`），再给迁移前 `channel` 为空的旧行分类。**只看 `actor` 文本不可靠**：CLI 的运维标识可以是 `system`，也可以是 `user:<UUID>` 的样子（字符集允许 `:`），所以回填**先按动作归属、再按操作者**，固定顺序如下：

1. CLI 独有的动作（`role.grant:*`、`role.revoke:*`、`account.enable`、`account.disable`）→ `cli`，运维标识叫什么都一样（叫 `system` 或 `user:<UUID>` 也是 `cli`，不会被当成系统或账号本人）；
2. `admin.users.*` 且 `actor` 形如 `user:<UUID>` → `web`，同时补上 `actor_user_id`，原因标记为已检查（那是代码生成的描述）；
3. `registration.request`、`password_reset.request` 且 `actor = 'system'` → `system`；
4. 账号本人的流程（`account.create`、`login`、`password.verify`、`password.change`、`password.reset`、`deletion.begin`、`deletion.finish`）→ `self`，**且必须** `actor = 'user:' || user_id`（操作者就是该行所属账号，这些流程就是这样写的，是唯一可靠的依据）；
5. 其余无法归到网页或本人身份的旧行 → `cli`，运维标识就是原 `actor` 文本，页面标明“不是网页账号”，**不会**显示成任何账号。

`reason_checked`：迁移前已经是 `web` 渠道的行（T5 的网页修改，写入时做过原因内容检查）和第 2 条回填的行标为已检查；其余旧行保持 `false`（历史自由文本从未检查，见下一段）。迁移后所有写入点显式写渠道，并标记 `reason_checked = true`。原始 `actor`、`reason` 与全部历史数据原样保留；回滚只会让新列和索引不再被使用，不需要数据迁移。**锁与耗时：**`CREATE INDEX` 是普通（非 `CONCURRENTLY`）建索引，会在 `security_audit` 上持有阻止写入的锁；之后是四条分类 `UPDATE` 加一条 `reason_checked` 标记 `UPDATE`，行数与表大小成正比。若迁移工具把整个文件放在一个事务里执行（迁移测试就是这样），锁会持续到整个文件提交，期间产生审计行的请求（登录、改密、管理操作）会等待，可能超时；逐条自动提交也会在每条索引创建期间阻塞写入。在本地开发机的一台 PostgreSQL 上用 100 万条旧审计行单次实测整个文件约 100 秒（只是数量级参考，不是生产承诺）；生产请先在数据副本上测一次，放在低峰期执行，并预留写入等待。`ADD COLUMN ... DEFAULT false` 在 PostgreSQL 11 以上只改元数据，不重写表。

**原因文本的读取规则。** 读取层对每一行的原因文本统一判定，**不靠前端遮挡**，数据库里的原始行不改：只有 `reason_checked` 为真、且现在仍然通过写入时的同一套规则（1–200 个字符、无控制字符、不像邮箱、密码或令牌）的文本才原样返回；其余（包括迁移前从未检查过的历史自由文本——CLI 在网页修改出现前只检查长度——以及任何不再通过的内容）只返回固定标记 `[hidden]`，页面显示为“原因已隐藏”。判定不了的历史文本宁可整条隐藏，也不猜。原因为空的行仍然为空，不会变成标记。嵌套的“重放原始记录”（`AuditOrigin`）**没有任何自由文本字段**（只有行 ID、时间、动作、结果与前后状态、角色），所以原始行里的原因不可能经它泄露；测试用字段清单固定这一点。规则是启发式的：通过检查的文本不等于被证明安全，所以写入端的检查和操作规范仍然有效。

**筛选与稳定翻页。** 筛选项：被操作账号 ID、操作者账号 ID（网页操作者，或 `self` 行的账号本人）、渠道、动作、结果、变更 ID、开始时间（含）与结束时间（不含）。全部有固定格式（UUID；动作与结果 `^[a-z0-9][a-z0-9._:-]{0,63}$`；变更 ID `^[A-Za-z0-9._-]{1,64}$`；开始早于结束），格式不对返回 `INVALID_ARGUMENT` 且**不回显输入**，页面在发送前先做同样的检查。默认 50 条，最多 100，负数拒绝。排序“创建时间倒序、行 ID 倒序”是全序，翻页令牌是不透明的 base64url（位置：微秒时间与行 ID）。这是**键集翻页，不是快照**：每一页都是“排在该位置之后的最新若干行”的一次新查询。边界要说清楚：① 行的 `created_at` 在事务提交**之前**由服务取值，并发事务里时间更早但提交更晚的行，可能在一次正在进行的翻页中被错过（它只对之后重新发起的查询可见）；② 翻页期间新写入的行排在更新的一侧，不会打乱已经走过的页，但同一次翻页也不一定看到它们；③ 每次读取自己也写一行 `admin.audit.list`，永远是更新的一侧。因此**不提供快照式或“无遗漏”保证**——需要核对完整性时请缩小时间范围后重新查询。令牌不签名，只含位置，**不绑定筛选条件**：服务端每页校验调用者与过滤条件的格式，但不会发现“换了筛选却沿用旧令牌”。所以**更换任何筛选条件都必须重置游标**（回到第一页、丢弃旧令牌）；页面在每次提交筛选或清除时就是这样做的，直接调用接口的人同理。

**审计本身被审计。** 每次读取在同一事务里写一行 `admin.audit.list`（`web` 渠道，真实操作者）；原因是脱敏的，只有用了哪些筛选和是否翻页（如 `filters=user,action paged`），**不含任何筛选值**。所以“谁在什么时候查过审计”可以在审计里查到，也意味着每次查询都会让最新一页多出这一行。

**重放的精确关联。** 变更 ID 在回执过期后可被复用，被拒绝的尝试也可能使用同一个 ID，所以展示**不假定**（操作者，变更 ID）对应唯一的原始事件，也不按它去匹配。正确做法是：回执在首次成功时记下那一行审计的 ID（`admin_mutations.audit_id`），重放时审计行写入 `origin_id` 指向它；列表只在被指向的行确实存在、且变更 ID、操作者、目标与重放一致、本身不是重放时才展示“当时的记录”（时间、动作、结果、前后状态）。因此：不会跨使用周期误关联；原始行已被清理时显示“当时的记录已超出保留范围”；T6 之前的回执没有关联，显示“没有关联到原始记录”。页面的“操作编号”筛选旁边也注明“同一编号过期后可能被复用，不一定是同一次操作”。

**BFF 日志。** `admin audit query` 记录 request_id、操作者、使用了哪几类筛选（如 `user+action+mutation`）、是否翻页、条数与结果；**不记录任何筛选值**（账号 ID、变更 ID、时间、原因都不在），也没有邮箱。

**页面。** `/audit/users`（平台管理员的分区“账号审计”；首页卡片说明范围）：筛选表单 + 记录列表（桌面与手机同一套卡片布局，无横向溢出）+ 上一页/下一页；记录显示动作名与原始分类、结果（已完成、无变化、重复提交、各类被拒绝）、渠道、操作者、账号、原因、变化（状态与角色前后）、变更 ID、请求 ID；重放行显示原始记录摘要。账号详情页有“查看此账号的审计记录”链接，进入 `/audit/users?user=<账号 ID>`——地址里只可能有一个合法的账号 UUID（标识符，不是邮箱或机密，其他筛选条件不进地址、历史或浏览器存储）；记录里的账号 ID 与操作者 ID 链接到对应账号详情。**时间。** 筛选里的开始/结束时间是浏览器的本地时区（`datetime-local`），按那一刻对应的时间点发送；列表里的时间也用本地时区显示，页面上有提示。**换人。** 控制台页面和“有效账号 ID”绑定：另一个标签把这个浏览器登录成别的账号后，页面在下一次检查（页面回到前台）发现会话账号变了，就立即移除旧账号的列表、详情、筛选、打开的操作对话框和仍在途的读取，新账号从一个新的页面重新加载；旧账号还在等待的读写应答被丢弃，不会显示在新账号的页面上，也不会触发新的会话检查。同一账号再次检查不会重置页面。（真实发出的写入属于服务端按当时 Cookie 决定的那个账号，页面不能替它改变；因此换人后的页面不沿用旧账号的任何输入。）错误的含义与用户查询一致（会话失效回登录、密码确认过旧给“前往 OzID 验证密码”、无权限、来源不允许、限流与服务不可用可重试）。

## metro 目录管理（T8）

metro 的城市、线路、车型、专题（增删改）、导入/导出、目录审计和管理令牌页面已从 metro 公开站迁入控制台的“地铁目录”分区（`/metro`、`/metro/cities`、`/metro/lines`、`/metro/rolling-stocks`、`/metro/topics`、`/metro/import`、`/metro/export`、`/metro/audit`、`/metro/tokens`）。不是 iframe，也不是链接到旧站；`apps/admin` 与 `apps/metro` 不互相 import，两边共用的纯函数（线路状态文案、里程格式）放在 `packages/metro-core`，没有复制。

城市编辑器可维护区域（华东/华北/华南/西南/华中/东北/西北，可留未分类）；线路编辑器可维护车型分类（A/B/L/胶轮/其他特种，可未分类）与按起终点顺序的完整站名列表（每行一站，可留空；填写时须与站数相等）。这些字段用于公开站的筛选、站点搜索与起终点展示，保存和再次编辑应完整保留；MCP 与 JSON 导入导出使用同样校验，具体限制见 [地铁目录规则](../metro/features.md#目录数据规则)。

- **谁能看到、谁能操作。** 只有持有 `metro-admin` 的账号看到分区；只有 `ozid-admin` 的平台管理员直接打开 `/metro/*` 只看到“没有权限”，页面不发出任何 metro 调用。BFF 对 `bff.metro.v1.AdminService` 的每个过程声明 `required_role = "metro-admin"` 并检查，metro 服务再从内部凭证独立检查一次。
- **没有 metro 上游时。** BFF 不挂载 metro 管理服务，控制台探测到后不显示分区，`/metro/*` 只说明“这个环境没有部署地铁目录服务”，不展示可操作的假页面。metro 暂时不可用时分区仍在，页面上说明并提供重试。
- **周界。** `internal/bff/app/admin_perimeter.go` 列出受周界保护的服务：`bff.v1.UserAdminService`、`bff.metro.v1.AdminService`、`bff.v1.AccessTokenService`。对它们的每个浏览器请求（读、写、导入导出、审计、令牌；预检；`/api/<服务>/` 下的任何路径写法）先过来源白名单与受信代理链，再过精确的控制台 Origin，然后才是会话、角色和 `AdminSessionMaxAge`；名单外或 Origin 不对一律是同一个泛化拒绝，业务服务（metro、令牌库）收不到请求。`TestEveryRoleGatedBrowserServiceIsInsideTheAdminPerimeter` 枚举所有 `bff.` 前缀且带 `required_role` 的服务，新增管理服务忘记列入周界会使测试失败。公开目录（`bff.metro.v1.CatalogService`）、骑行者功能和 `/me` 不在周界内。
- **MCP。** `/mcp/metro` 是 Bearer 令牌的机器协议，**不在**这道浏览器周界内，行为不变；它有自己的机器来源白名单 `mcp.allowed_cidrs`（T9），与控制台的 `admin.allowed_cidrs` 互不放行，见 [BFF 文档](../bff.md)。
- **规则保持不变，并随页面迁移。** 字段错误显示在对应字段（`city`、`rolling_stock` 等服务端字段名映射到表单字段）；被引用的记录删除时说明被谁引用；更新与删除带 `expected_revision`；导入默认先预览（dry run，不写入），有错误不能确认，确认后全有或全无；导出文件可直接再导入，且预览显示“无变化”；目录审计按渠道（网页/MCP）和对象筛选并显示前后快照；目录版本由服务端递增，公开站按 `known_version` 失效。普通目录编辑**不**要求密码或原因。
- **修订冲突。** 保存遇到他人已改的记录时，页面保留你已输入的内容，显示与最新版本的差异；“保留我的修改，基于最新版本”之后还要再点一次“保存”才写入，“放弃我的修改，载入最新内容”则不写入；记录已被删除时只提示并保留输入以便复制。删除遇到冲突同样不自动重试。
- **管理令牌。** 创建需要 15 分钟内的密码认证（与 T5 相同），明文只在创建响应里显示一次、只在内存中，刷新后消失，也不进地址或任何存储；数量上限（10）、有效期（7/30/90 天）、scope（`metro:read`/`metro:write`）、最近使用和吊销行为不变；页面显示的 MCP 端点来自 `VITE_API_BASE_URL`。
- **需要重新认证时。** 页面不会自己跳转：显示“前往 OzID 验证密码”。点击时先把未提交的表单内容（城市/线路等编辑框、导入文本、令牌表单）存入本标签页的 `sessionStorage`（一个位置、30 分钟有效、按用户与页面区分、读取即删除、退出登录时清除；只含你填写的内容，不含会话、CSRF 或令牌明文），**保存成功才**前往 OzID，验证后经返回路径（`/metro/*` 在注册表内）回到同一页面。回来后内容只是**重新出现**：显示“已恢复……尚未提交”，不自动提交、不重放旧写入；另一个账号读到别人留下的草稿时得不到内容，且草稿被丢弃；如果记录在此期间被他人修改，回来后按冲突流程要求再次确认。
- **浏览器拒绝保存时。** 存储被禁用或空间已满，页面**不离开**：显示“浏览器拒绝了保存……你填写的内容仍在页面上”，不发出任何请求；可以“重试保存并前往 OzID”（腾出空间或改设置后），“复制内容”（当前表单/文本的 JSON），“导出为文件”，或明确选择“不保留，直接前往 OzID”（回来后需自己重新填写）。点击前的说明不承诺“已保留”，只说明保存成功才会前往。
- **旧页面。** metro 公开站的 `/admin`、`/admin/<分区>` 已退役为“页面不存在”（不保留可继续管理的旧路由，也没有旧管理 bundle、令牌页或管理 RPC 客户端）；公开站只保留地铁足迹的普通功能。

**测试分层。**

1. 纯函数与单测：`apps/admin` 的 `route.test.ts`、`draft.test.ts`、`metro-form.test.ts`，`packages/metro-core`，`apps/metro` 的路由测试（`/admin*` 为 notfound）。
2. 页面行为（Playwright，模拟 BFF，桌面与手机）：`apps/admin/tests/metro.spec.ts`——角色可见性、无上游、不可用、周界关闭、CRUD、字段错误、引用删除、修订冲突三种结果、迟到响应、导入预览/确认、导出再导入、审计渠道、令牌一次性明文、上限与已过期令牌的显示、重新认证往返与草稿恢复、存储被拒与配额已满时不离开页面且不发出任何请求（复制、导出、重试与明确放弃）、OzID 按钮不是提交按钮（`type="button"` 属性与原生 `submit` 事件监听）、另一个账号取不到上一个账号的草稿；`apps/metro` 的 Playwright 测旧 `/admin` 退役与普通功能回归。
3. Go 链路（真实 BFF、usersrv、metro 与 PostgreSQL/Redis，`internal/e2e`）：`TestChainMetroAdminPerimeter*`（来源白名单、伪造头、受信代理、Origin、预检、路径写法、周界先于会话、metro 上游调用计数与令牌/城市数量证明被拒请求没有到达业务）、`TestChainMetroAdminRolesAndReauthentication`、`TestChainMetroAdminAbsentWithoutAnUpstream`、`TestChainMetroAdminPerimeterLeavesMCPAlone`，以及既有的 `TestChainMetroAdmin*`、`TestChainMCP*`（现经控制台 Origin 调用）。
4. 真实浏览器与网关替身（`ADMIN_BROWSER_E2E=1 go test ./internal/e2e -run TestAdminBrowserChain`，桌面与手机各一遍；设置 `ADMIN_BROWSER_ARTIFACTS=<目录>` 时把浏览器运行日志、JSON 报告和失败用例的 trace、截图、视频留在该目录，CI 的 `admin-browser` 作业把它与完整 Go 测试日志作为构件上传，失败时用它定位，重跑通过不算证据）：真实 metro 上的城市/线路创建、字段错误、引用删除限制、另一个标签页造成的修订冲突、导入预览与确认、导出文件再导入无变化、审计的网页渠道与前后快照、令牌明文只显示一次、密码认证过旧经 OzID 往返回到 `/metro/tokens` 且表单内容恢复但不重放、另一个 metro 管理员在同一标签页登录时取不到上一个账号留下的草稿、控制台里创建/删除城市后匿名读者手里的目录版本立即过期并读到新数据、只有平台角色的账号没有 metro 分区也没有 metro 请求、来源移出白名单后已打开的页面读取被 BFF 拒绝且重新打开页面得到 403、外来产品页用 metro 管理员 Cookie 调用管理/导出/审计/令牌接口被拒。

## 查询接口与审计

`UserAdminService`（BFF 与 usersrv 同名，字段见 `proto/bff/v1/user_admin.proto`、`proto/usersrv/v1/admin.proto`）：

- `ListSecurityAudit`（T6）：账号审计列表，见“账号安全与管理审计查询（T6）”。
- `ListUsers`：`email`（精确）、`user_id`、`nickname`（字面包含）、`status`、`role`、`page_size`、`page_token`。`GetUser`：`user_id`；不存在返回 `NOT_FOUND`。
- usersrv 在读取事务里**再次**确认调用者是仍然正常的、当前持有 `ozid-admin` 的账号（不信任调用者带来的角色声明），审计行与读取在同一个事务里写入：`admin.users.list`/`admin.users.get`，结果 `ok`/`not_found`/`refused_forbidden`；审计原因是**脱敏**的（只记录用了哪类查找和是否分页，如 `lookup=email status role paged`，不含任何邮箱、昵称或 ID 值）。
- BFF 按操作者限流（Redis 计数，键 `admin:users:<用户ID>`），默认每分钟 120 次（`admin.rate_limit_per_minute`）；限流器故障返回 `UNAVAILABLE`（不放行）。每次查询写一行脱敏日志 `admin user query`（request_id、操作者、动作、查找类型、条数、结果）。
- 错误文案不泄露内部细节。BFF 只适配内部契约，**从不读取 usersrv 的数据库**。

## 配置

BFF（`config.bff.example.yaml`，生产值只写在私有部署配置里）：

```yaml
cors_allowed_origins: [https://admin.example.com]   # 控制台 origin 必须在其中
navigation:
  api_origin: https://api.example.com
  apps:
    admin: {origin: https://admin.example.com, paths: ["/users", "/users/*", "/audit/users", "/metro", "/metro/*"]}
admin:
  allowed_cidrs: ["203.0.113.0/24"]      # 空 = 全部拒绝
  trusted_proxies: ["198.51.100.0/24"]   # 直连 BFF 的代理网段；空 = 不信任任何转发头
  forwarded_header: X-Forwarded-For
  rate_limit_per_minute: 120
  write_rate_limit_per_minute: 30        # T5 写入，按操作者
```

`admin-web` 镜像（`deploy/admin-web`）：

| 类型 | 名称 | 说明 |
|---|---|---|
| 构建参数（必填，无默认） | `VITE_API_BASE_URL` | BFF 的公开 API base（绝对 https，例：`https://api.example.com/api`）；控制台没有同源 `/api` 代理 |
| 构建参数（必填，无默认） | `VITE_OZID_ORIGIN` | OzID 的 origin（登录与密码确认在那里完成） |
| 运行环境变量（必填） | `API_ORIGIN` | 上面 API base 的 origin，只用于页面 CSP 的 `connect-src` |
| 运行环境变量（必填） | `ADMIN_HOST` | 控制台唯一回应的主机名（小写、无端口、无通配符） |
| 运行环境变量（必填） | `ADMIN_ALLOWED_CIDRS` | 逗号分隔的来源网段；**空值、全开、无前缀、含主机位的值都使容器拒绝启动** |
| 运行环境变量（可选） | `ADMIN_TRUSTED_PROXIES`、`ADMIN_REAL_IP_HEADER` | 前置代理网段与它写入的头（默认 `X-Forwarded-For`）；不设则只看 TCP 对端。**与 `ADMIN_ALLOWED_CIDRS` 重叠（任一方向包含）的代理网段使容器拒绝启动** |

两份白名单（BFF 的 `admin.allowed_cidrs` 与镜像的 `ADMIN_ALLOWED_CIDRS`）、两份受信代理必须一起改；它们是各自独立的防线，不是互相替代。

## 构建、测试与运行

```bash
# 本机开发：Vite 5176（BFF 配置示例里的 http://localhost:5176），API base 与 OzID origin 见 .env.development
npm run dev:admin

# 构建：两个公开参数都必须给出，缺失或不合法时构建失败
VITE_API_BASE_URL=https://api.example.com/api VITE_OZID_ORIGIN=https://id.example.com npm run build:admin

npm run test:admin        # 路由与返回路径的单测
npm run test:e2e:admin    # Playwright，模拟 BFF，桌面与手机

# 镜像（Nginx，纯静态；只在来源白名单之后提供页面）
docker build -f deploy/admin-web/Dockerfile \
  --build-arg VITE_API_BASE_URL=https://api.example.com/api \
  --build-arg VITE_OZID_ORIGIN=https://id.example.com -t admin-web .
scripts/admin-web-smoke.sh   # 构建并启动容器，实测白名单、伪造头、Host、响应头与拒绝启动（CI 的 admin-web-image 任务）
```

**部署顺序。** usersrv（含迁移 `20261025120000_platform_admin_role.sql`、T5 的 `20261025130000_admin_mutations.sql` 与 T6 的 `20261025140000_audit_query.sql`，它会在 `security_audit` 上建索引并回填，期间写入会等待，须低峰执行，见“迁移回填”的锁与耗时说明）→ BFF（新配置 `admin`、注册 `navigation.apps.admin`、CORS 加入控制台 origin）→ 网关与 `admin-web`。BFF 先于控制台上线时，管理读接口已经受周界保护。回滚：先下线控制台，再回滚 BFF；回滚 usersrv 迁移前先用 CLI 撤销所有 `ozid-admin`。镜像不会自动部署、不会自动开放白名单。

## 验证与限制

按三层分别陈述证据，不互相替代：

1. **代码与自动化**：Go 单测与真实 Postgres/Redis 的 `internal/e2e`（`TestChainUserAdmin*`：四种角色矩阵、控制台 Origin、来源策略（含受信代理链、伪造头、预检、其他路径）、查询/分页/脱敏审计、限流、角色生命周期与注销保护、认证过旧）；`internal/bff/adminguard`、`ingress` 周界单测、`config` 校验单测；usersrv 的 `useradmin` 单测与 gRPC 层独立授权；迁移集成测试（含 `TestPlatformRoleMigrationUpgradesAnExistingDatabaseInPlace`：在已有 `metro-admin` 行的上一版库上单独应用 `20261025120000_platform_admin_role.sql`，数据原样保留、新约束只接受两个白名单角色、回滚顺序）；真实 PostgreSQL 的并发测试 `internal/usersrv/account/deletion_race_test.go`（用行锁闸门强制“授予/撤销先拿锁”与“注销先拿锁”两个顺序，另有 40 轮不加闸门的真并发，断言任何时刻不会同时处于 `deleting` 且持有平台角色；拒绝后撤销角色不会自动注销、需要新的明确请求）；`cmd/usersrv-admin` 的 CLI 测试；`npm run test:admin`；`npm run test:e2e:admin`（**模拟 BFF**，覆盖匿名跳转、无权限不循环、按角色分区、列表/筛选/分页/详情、过期响应不覆盖新结果、各类错误、手机宽度、地址与存储无个人信息）。模拟 BFF 的结果只证明页面自身行为。**T5 增加：** `internal/usersrv/account/adminweb_test.go`（真实 PostgreSQL：四个动作、受保护目标、注销中、版本冲突、同 ID 同内容重放（原审计行的真实前后值、重放行的空前后值与按操作者+变更 ID 的关联）、已有回执后操作者被撤销平台角色或被禁用再用原请求重放均被拒绝且不改目标不动回执、恰好 `expires_at` 的过期边界、同 ID 不同内容拒绝、按操作者隔离、回执过期、未变化不提升版本、禁用/撤销后启用不复活旧凭证、并发相同请求只生效一次、CLI 授予平台角色与网页禁用在两种锁序下的结果，以及去掉 `FOR UPDATE` 或近期认证检查后对应测试会失败的变异验证）、`internal/usersrv/integration/migrations_test.go`（在上一版库上原地升级，旧审计行保留）、`internal/usersrv/app/useradmin_test.go`（gRPC 层角色与错误映射）、`internal/e2e/user_admin_write_chain_test.go`（完整链路：来源/Origin/角色/CSRF/15 分钟认证叠加、操作者不可伪造、旧会话与真实 metro 管理令牌不复活、审计内容、禁止项没有任何入口）、`apps/admin` 的 `change.test.ts` 与 Playwright“account changes”用例（模拟 BFF，桌面与手机：影响与原因、版本冲突刷新、重新认证后不重放、丢失响应后同 ID 重试、原因变化换新 ID、受保护/注销中账号无操作）。
   **T6 增加（按实际测试名）：**
   - `internal/usersrv/account/auditquery_test.go`（真实 PostgreSQL）：`TestAuditListShowsRealChangesAndSeparatesTheChannels`（网页修改的真实前后值，被拒绝与无变化可见，四种渠道与操作者互不混淆，CLI 标识不被当作账号）、`TestAuditListFiltersValidateAndCombine`（各筛选项及组合、非法格式与不回显）、`TestAuditListPagesStablyEvenWhenRowsShareATimestamp`、`TestAuditListNeedsThePlatformRoleNow`（撤权后下一次读被拒绝并留下 `refused_forbidden`）、`TestAuditListReadIsAuditedWithoutFilterValues`、`TestAuditListHoldsNoSensitiveContent`、`TestAuditLegacyReasonsNeverLeaveTheReadLayer`（旧的未检查原因、含邮箱或令牌的原因、已标记但不再合规的原因都只返回 `[hidden]`，合规原因与空原因不变，嵌套原始记录里没有任何原文，数据库原始行不变）、`TestAuditOriginHoldsNoFreeText`（`AuditOrigin` 的字段清单）、`TestAuditReplayLinksTheExactOriginAcrossReusedKeys`（回执过期后同一变更 ID 的两个使用周期与同键被拒绝尝试不会误关联，清理后的原始行与无关联的旧回执有明确状态）、`TestAuditRowsOfEveryChannelAreClassifiedByTheMigration`（用真实迁移 SQL 逐类验证回填，含运维标识叫 `system`、`user:<UUID>`（别人的、恰好是该行所属账号的、管理员的）的 CLI 旧行与无法归属的旧行）。
   - `internal/usersrv/integration/migrations_test.go`：`TestAuditQueryMigrationUpgradesAnExistingDatabaseInPlace`（在上一版库上原地升级：旧行保留、撞名的 CLI 旧行仍归 `cli` 且 `actor` 与动作原样、`reason_checked` 只标已知安全的行、回执仍有效）、`TestMigrationDirectoryChecksum`（`atlas.sum`）。
   - `internal/usersrv/app/useradmin_test.go`（gRPC 层）：`TestListSecurityAuditOverRPC`（角色、过滤与分页）、`TestListSecurityAuditHidesLegacyReasonsOverRPC`（旧敏感原因在每一页、每个响应里都只是标记）。
   - `internal/e2e/user_admin_audit_chain_test.go`（完整链路）：`TestChainUserAuditRoleMatrix`（只有 `metro-admin` 读不到账号审计）、`TestChainUserAuditPerimeterAndSession`（来源白名单与伪造头、受信代理、控制台 Origin、预检、认证过旧不到达 usersrv、读取限流）、`TestChainUserAuditShowsWhatReallyHappened`（真实修改后可见，含重放的精确原始、拒绝与无变化、CLI 行、筛选与分页、非法输入不回显）、`TestChainUserAuditLogsCarryNoFilterValuesOrRows`、`TestChainUserAuditHidesLegacyReasonsEndToEnd`（旧敏感原因不到浏览器、不进任何日志行，库里原文仍在）。
   - `apps/admin`：`route.test.ts`；Playwright（模拟 BFF，桌面与手机各一遍）`console.spec.ts` 的“account audit”（列表与翻页、重放原始、渠道与操作者、筛选与清除、非法筛选不发送、真实修改后可见、地址与存储无个人信息、较慢旧响应不覆盖新结果、被隐藏的原因只显示说明、各类错误、窄屏无溢出）、“audit times follow the browser's time zone”（筛选时间按浏览器时区发送）和“the account behind the session changes”（两位平台管理员跨标签换人：审计页、用户列表、账号详情与操作对话框、仍在等待的写入与迟到响应、同一账号再次检查不重置页面）。
2. **真实浏览器与代理链**：`ADMIN_BROWSER_E2E=1 go test ./internal/e2e -run TestAdminBrowserChain`（CI 的 `admin-browser` 任务）。真实 BFF 与 usersrv，OzID、API、控制台和一个外来产品页分处同站点的不同保留测试子域名（`*.ozid-e2e.test`，一次性证书）；Chromium 实测：匿名 → OzID 登录 → 返回 `/users`、Cookie 仅属于 API 主机且不进存储、无角色“没有权限”且无循环无查询、只有 metro-admin 有“地铁目录”分区而无用户分区、真实查询（邮箱精确、昵称字面包含、角色筛选、详情）、来源在名单外时页面和资源全部 403、已打开的控制台再查询被 BFF 周界拒绝而 OzID 账号功能不受影响、外来产品页（在公共 CORS 列表内）用操作员 Cookie 读用户被拒。**T6 增加（桌面与手机各一遍）：** 从 `/audit/users` 匿名登录后回到该地址（BFF 注册表允许 `/audit/users`）；无角色与只有 `metro-admin` 的账号既没有审计数据也没有发出审计请求；操作员在详情页真实禁用再启用账号后，从详情页链接进入的审计里按顺序看到两行网页修改（前后状态、原因、同一个操作者账号），CLI 渠道只显示运维标识；真实列表的筛选与翻页；页面与地址、存储不含邮箱、密码、令牌；外来产品页用操作员 Cookie 读审计被拒，已打开的审计页在来源移出白名单后再查询被 BFF 拒绝。**T5 增加（桌面与手机各一遍）：** 操作员在详情页依次禁用、启用、授予、撤销 `metro-admin`，被修改账号在另一个浏览器里的会话每次都在下一次请求失效，禁用时无法登录，重新登录后才带新角色；平台管理员自己的详情页没有任何操作；网关控制主机把操作员的密码认证时间调到 16 分钟前后，写入得到“请重新输入密码”，经 OzID `/security` 输错、输对密码后回到原详情页，页面没有重放写请求，账号未被改动，再人为重新发起才生效；来源移到白名单外后，已打开的控制台的写入被 BFF 周界拒绝。这里的网关是**替身**（用同一份 Go 周界规则和模板里的 CSP/头复刻 `admin-web` 的行为，并按受信代理的方式写 `X-Forwarded-For`）；Nginx 本身由 `scripts/admin-web-smoke.sh` 的容器验收检验。
3. **运维仍须自行验证**：真实 DNS、证书与 TLS 终止、真实来源地址与白名单取值、真实前置代理/CDN 链路（谁在 `trusted_proxies` 里、是否覆盖而非透传 `X-Forwarded-For`）、邮件厂商。自动化只覆盖受控环境，不能替代这些检查。

当前限制：

- 账号审计只覆盖 usersrv 保存的账号安全与管理记录（不是全站统一审计；metro 目录审计、管理令牌调用日志不在其中）；没有导出、没有按邮箱查询的入口（邮箱不进审计，也不能成为审计查询条件）；审计保留期限与清理由运维按数据策略决定，原始记录被清理后重放行只显示“超出保留范围”。平台角色、平台管理员的状态与角色、密码、注销仍只能用 CLI 或用户自己在 OzID 完成。
- 网页写入的 15 分钟认证窗口是代码常量（与密码修改、注销等敏感操作共用 `SensitiveAuthMaxAge`），不可配置；写入限流可通过 `admin.write_rate_limit_per_minute` 调整。
- MCP 的来源策略由 [#87（T9）](https://github.com/kongken/mono/issues/87) 独立实现（`mcp.allowed_cidrs`，不受管理 IP 白名单影响，也不放行浏览器管理接口）。metro-admin 在 BFF 侧的“把暂时故障当成未登录”的既有行为属于 [#89（T11）](https://github.com/kongken/mono/issues/89) 的故障与失效区分验收，届时修复并回归，T4 不处理。
- 限流只在 BFF 一层（usersrv 不另做）。分页令牌不做签名、不绑定筛选条件（只含位置；换筛选须重置游标），审计翻页是键集翻页而不是快照，并发提交的边界见“筛选与稳定翻页”。
- 来源不在名单时浏览器看到的是网络类错误而不是“来源被拒”：这是浏览器对不带 CORS 许可的跨域回答的行为，页面无法区分，也不应该区分。

## 整体验收（T12）

控制台与管理 API 的整套周界（网关、`admin-web`、BFF 三层，浏览器与 MCP 两份独立名单，伪造转发头、路径写法、预检与未登记 Host）在真实 Caddy 与 Nginx、真实浏览器下，对两组不同的保留域名验证，名单外浏览器的控制台一律 403 空体而 OzID 与产品照常。映射、结果与运维待验项见 [整体验收](../ozid/acceptance.md)。
