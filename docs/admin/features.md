# 统一管理控制台：功能描述（T4 受控控制台与用户查询，T5 账号状态与角色修改）

本文描述 [OzID 规格](../ozid/spec.md) 中 [Issue #82（T4）](https://github.com/kongken/mono/issues/82) 已实现的行为：独立的管理控制台 `apps/admin`、它的受控入口（来源网络、控制台 Origin、会话与平台角色），以及用户查询；[Issue #83（T5）](https://github.com/kongken/mono/issues/83) 在其上加入普通账号的禁用/启用与 `metro-admin` 的授予/撤销（见“账号状态与角色修改”）。**尚未实现**的部分见文末“当前限制”：metro 后台迁移、账号审计页、metro 管理接口的来源策略切换。文中域名与地址全部是保留示例（`admin.example.com`、`203.0.113.0/24` 等），不代表任何真实部署。

## 现状与范围

| 已实现（T4、T5） | 尚未实现 |
|---|---|
| `apps/admin`（`@kongken/admin`）：独立构建、独立镜像 `admin-web`；匿名跳 OzID 登录并返回；无角色显示“没有权限”（不跳转、不循环）；按真实角色显示分区；“管理 OzID 账号”链接到 OzID | metro 后台页面迁移（控制台里没有 metro 分区，也不展示假分区）、账号审计页 |
| BFF `bff.v1.UserAdminService` 与内部 `usersrv.v1.UserAdminService`：`ListUsers/GetUser`（只读）、T5 的 `DisableUser/EnableUser/GrantMetroAdmin/RevokeMetroAdmin`（仅这四个动作）；`required_role = "ozid-admin"`，BFF 入口与 usersrv 各自独立校验 | 网页上授予/撤销平台角色、改平台管理员的状态或角色、设置他人密码、代登录、代注销、批量或任意角色替换（没有对应接口） |
| 管理面“周界”（`admin` 配置）：来源 IP 白名单、受信代理链、精确控制台 Origin；BFF 对每个管理读接口独立检查，网关镜像对页面、静态资源和 SPA fallback 同样限制 | 现有 metro `AdminService`/`AccessTokenService` 的来源策略：随各自迁移任务切换，**T4 没有给它们加 IP 限制** |
| 平台角色 `ozid-admin`：只能由运维 CLI `usersrv-admin grant-role/revoke-role` 授予/撤销，授予不随首位注册用户，不继承也不授予 `metro-admin`；每次变更提升 `auth_version` 并写审计 | 控制台的 metro 目录管理分区（继续使用现有管理入口） |
| 平台管理员账号从引入起**不能被网页自助注销**（`BeginDeletion` 拒绝）；权限收回后也不会被之前被拒的注销自动关闭 | |

## 角色与可见内容

控制台按**真实角色**显示分区；这只决定“显示什么”，每一次调用都由服务端再判断一次，隐藏菜单不是授权。

| 账号 | 首页 | `/users`、`/users/:id` | 调用管理 API |
|---|---|---|---|
| 无角色 | “没有权限”页（可退出换账号、可去 OzID），不跳转 | “没有权限”，不发起任何用户查询 | `PERMISSION_DENIED` |
| 只有 `ozid-admin` | 用户分区 | 可用 | 允许 |
| 只有 `metro-admin` | 说明“地铁目录管理还没有迁入控制台”，无分区 | “没有权限” | `PERMISSION_DENIED`（**不**继承） |
| 两个角色都有 | 用户分区（metro 分区待迁移任务） | 可用 | 允许 |

匿名访问任何页面：控制台整页跳到 `OzID /login?app_id=admin&return_path=<当前页>`，登录后由 BFF 注册表（`navigation.apps.admin`）决定返回地址。未知地址（不是 `/`、`/users`、`/users/<UUID>`）显示控制台自己的“没有这个页面”，不要求登录、不展示数据。会话查询本身失败（网络、服务不可用）显示故障和“重试”，**不当成已退出**。

## 页面与交互

- `/`：角色与可用分区。`/users`：筛选 + 表格 + 分页；`/users/:id`：账号详情，普通账号在此有“状态与角色”操作（T5）。
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
- 本票**没有**给 metro 的 `AdminService`/`AccessTokenService` 加来源限制；它们的来源策略随各自迁移任务切换，不能因本票悄悄关闭现有维护入口。

## 平台角色 `ozid-admin`

- 只通过运维 CLI 授予/撤销：`usersrv-admin grant-role -email E|-user-id U -role ozid-admin -operator NAME -reason TEXT`，`revoke-role` 同理（没有单独的 `init` 命令：第一个平台管理员也是这样授予；**不会**因为谁最先注册而自动授予）。每次变更提升账号 `auth_version`（旧会话和管理令牌在下次检查失效）并写 `security_audit`（含被拒绝和未改变的尝试）。
- `ozid-admin` 与 `metro-admin` 相互独立：持有一个不意味着持有另一个，撤销一个不影响另一个。
- usersrv 迁移 `migrations/usersrv/20261025120000_platform_admin_role.sql` 把 `user_roles` 的角色检查约束扩展为 `('metro-admin','ozid-admin')`；回滚前必须先撤销所有 `ozid-admin`，否则旧约束会拒绝这些行。
- 平台管理员保护：**强制点在 usersrv，对所有调用方有效**——`BeginDeletion` 在账号行锁内发现 `ozid-admin` 即拒绝（原因 `PLATFORM_ADMIN_PROTECTED`，审计 `deletion.begin=refused_platform_admin`，账号状态与 `auth_version` 不变），BFF 的 `DeleteAccount`、后台 runner 和任何持有内部凭证的客户端都一样。BFF 只做两件事：把拒绝映射成明确错误；在开始阶段遇到这个永久拒绝时丢弃该工作流行，所以之后即使撤销角色，账号也不会被之前的请求自动关闭。产品（山海足迹、地铁足迹）里现有的注销入口走同一个 `DeleteAccount`，同样受保护。**缺少的只是 OzID 自己的网页注销页**（后续阶段），不是“所有前端都没有注销入口”。共享前端包 `@kongken/account` 的错误分类已把这个拒绝识别为独立的 `platform_admin` 类（给出“请联系运维先回收管理员角色”的明确提示）；OzID 的网页注销页本身是后续阶段。

## 账号状态与角色修改（T5）

只有四个动作：禁用/启用普通账号、授予/撤销 `metro-admin`。没有任意角色字符串、没有整个角色数组替换、没有批量。

**界面。** `/users/:id` 的“状态与角色”区按目标当前情况给出按钮；点击打开确认框，显示目标（昵称、邮箱、用户 ID）、现在与之后的状态/角色、影响清单，以及必填原因（1–200 字符，拒绝控制字符与明显的密码/令牌/邮箱，服务端按去掉首尾空白后的内容再校验）。平台管理员的详情页只显示“网页上不能修改它的状态或角色，请联系运维使用命令行处理”，注销中的账号只显示“这个账号正在注销，只能查看”，都没有按钮；已禁用账号不能被授予角色（需先启用）。

**一次写请求要同时通过：** 来源白名单 → 控制台 Origin → 有效会话 → 当前持有 `ozid-admin` → CSRF → 最近 **15 分钟**内的真实密码认证（比读取严格；`AdminSessionMaxAge` 仍是 12 小时只作用于读取）。密码确认过旧时返回“请重新输入密码”：确认框给出“前往 OzID 验证密码”按钮，验证后回到原页面，**旧的写请求不会被自动重放**，管理员需要重新点击。BFF 对写入另有限流（键 `admin:users:write:<用户ID>`，默认每分钟 30 次，`admin.write_rate_limit_per_minute`）。

**操作者与时间不由浏览器给出。** 操作者 UUID 来自 BFF 已验证会话的内部凭证；请求体只有目标 ID、目标认证版本、变更 ID、原因。usersrv 在事务内再次确认操作者仍正常且持有 `ozid-admin`，并独立检查目标约束，不信任 BFF。

**一个事务内按顺序完成**（`account.ApplyAdminChange`）：确认操作者 → 对目标行 `FOR UPDATE`（与 CLI 授予/撤销/禁用、`BeginDeletion` 是同一把锁，所以 CLI 同时授予平台角色不可能绕过网页禁用的保护：谁先拿锁，另一方就按提交后的状态被拒绝）→ 查本操作者的幂等回执 → 目标持有 `ozid-admin` 则拒绝 → 目标 `auth_version` 与请求不符则拒绝（需刷新）→ 动作自己的约束（注销中的账号四个动作都拒绝，所以**不能用“启用”取消注销**；授予要求账号正常且邮箱已验证）→ 更新状态/角色 → **仅在确实发生变化时** `auth_version` +1 → 写回执与审计。

**幂等与冲突。**

- 请求带 `expected_auth_version` 与 `mutation_id`。版本不符返回 `ABORTED`（`TARGET_CHANGED`），页面提示“账号已被其他人修改”，只提供“刷新”，刷新后重新决定。
- 同一操作者、同一变更 ID、**同样内容**（动作、目标、版本、原因）返回原结果（`replayed=true`，不再改动，不再提升版本）；内容不同返回 `ALREADY_EXISTS`（`MUTATION_REUSED`）。回执保存 24 小时、按**操作者隔离**（键是 操作者+变更 ID），另一个管理员使用同一个 ID 互不影响。回执即使在目标已不存在时也能重放。页面在响应丢失后用同一个变更 ID 重试，原因改动后换新 ID。被拒绝的请求不留回执，可以刷新后重做。
- 已经处于目标状态的请求（再次禁用已禁用账号、再次授予已有角色等）结果为 `unchanged`：不提升版本、不使旧会话失效，审计分类为 `unchanged`；重试已完成的请求也不会再次提升版本。
- 启用或重新授予**不会让旧会话和管理令牌复活**：禁用与撤销已经提升了 `auth_version`，启用不会回退它，旧凭证按版本一直失效；账号需要重新登录，令牌需要重新创建。授予角色同样提升版本，所以账号会话重建后才带新角色。

**审计。** 每次尝试在同一事务内写一行 `security_audit`：真实操作者（`actor_user_id`）、目标、动作（`admin.account.disable`、`admin.account.enable`、`admin.role.grant:metro-admin`、`admin.role.revoke:metro-admin`）、原因、变更 ID、渠道（`web`；CLI 为 `cli`）、变更前后的状态与角色、请求关联 ID、时间，以及结果：`ok`、`unchanged`、`replayed`、`refused_forbidden`、`not_found`、`refused_target_protected`、`refused_version_conflict`、`refused_not_active`、`refused_mutation_reuse`。不含密码、令牌或邮箱明文的查询内容；BFF 日志 `admin user change` 同样只记录 request_id、操作者、动作、目标 ID 和结果。

**错误映射。**

| 情形 | 内部码 | 页面 |
|---|---|---|
| 目标是平台管理员 | `FAILED_PRECONDITION`（`TARGET_PROTECTED`） | “网页上不能修改平台管理员” |
| 目标已被修改 | `ABORTED`（`TARGET_CHANGED`） | “账号已变化”+刷新 |
| 变更 ID 被不同内容使用 | `ALREADY_EXISTS`（`MUTATION_REUSED`） | 重新发起（新 ID） |
| 注销中/未验证等不允许 | `FAILED_PRECONDITION` | “账号当前不能这样修改” |
| 密码确认过旧 | `FAILED_PRECONDITION`（`bffauth.ErrReauthRequired`） | 前往 OzID 验证并返回 |
| 缺角色 / 来源不允许 / 限流 / 服务不可用 | 与读取相同 | 与读取相同 |

**迁移与数据。** `migrations/usersrv/20261025130000_admin_mutations.sql`：`security_audit` 增加 `channel`、`actor_user_id`、`mutation_id`、`before/after_status`、`before/after_roles`（旧行取空默认值，`channel` 为空即迁移前由 CLI 写入的记录，数据原样保留）及检查约束；新建 `admin_mutations`（回执，`(operator_id, mutation_id)` 唯一，`expires_at` 索引）。usersrv 的定期清理（`PurgeExpired`）同时删除已过期回执。回滚前不需要数据迁移；回滚只会让新列与新表不再被使用。Ent 代码由 `make ent-usersrv` 生成，Protobuf 由 `make buf` 生成。

## 查询接口与审计

`UserAdminService`（BFF 与 usersrv 同名，字段见 `proto/bff/v1/user_admin.proto`、`proto/usersrv/v1/admin.proto`）：

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
    admin: {origin: https://admin.example.com, paths: ["/users", "/users/*"]}
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

**部署顺序。** usersrv（含迁移 `20261025120000_platform_admin_role.sql` 与 T5 的 `20261025130000_admin_mutations.sql`）→ BFF（新配置 `admin`、注册 `navigation.apps.admin`、CORS 加入控制台 origin）→ 网关与 `admin-web`。BFF 先于控制台上线时，管理读接口已经受周界保护。回滚：先下线控制台，再回滚 BFF；回滚 usersrv 迁移前先用 CLI 撤销所有 `ozid-admin`。镜像不会自动部署、不会自动开放白名单。

## 验证与限制

按三层分别陈述证据，不互相替代：

1. **代码与自动化**：Go 单测与真实 Postgres/Redis 的 `internal/e2e`（`TestChainUserAdmin*`：四种角色矩阵、控制台 Origin、来源策略（含受信代理链、伪造头、预检、其他路径）、查询/分页/脱敏审计、限流、角色生命周期与注销保护、认证过旧）；`internal/bff/adminguard`、`ingress` 周界单测、`config` 校验单测；usersrv 的 `useradmin` 单测与 gRPC 层独立授权；迁移集成测试（含 `TestPlatformRoleMigrationUpgradesAnExistingDatabaseInPlace`：在已有 `metro-admin` 行的上一版库上单独应用 `20261025120000_platform_admin_role.sql`，数据原样保留、新约束只接受两个白名单角色、回滚顺序）；真实 PostgreSQL 的并发测试 `internal/usersrv/account/deletion_race_test.go`（用行锁闸门强制“授予/撤销先拿锁”与“注销先拿锁”两个顺序，另有 40 轮不加闸门的真并发，断言任何时刻不会同时处于 `deleting` 且持有平台角色；拒绝后撤销角色不会自动注销、需要新的明确请求）；`cmd/usersrv-admin` 的 CLI 测试；`npm run test:admin`；`npm run test:e2e:admin`（**模拟 BFF**，覆盖匿名跳转、无权限不循环、按角色分区、列表/筛选/分页/详情、过期响应不覆盖新结果、各类错误、手机宽度、地址与存储无个人信息）。模拟 BFF 的结果只证明页面自身行为。**T5 增加：** `internal/usersrv/account/adminweb_test.go`（真实 PostgreSQL：四个动作、受保护目标、注销中、版本冲突、同 ID 同内容重放、同 ID 不同内容拒绝、按操作者隔离、回执过期、未变化不提升版本、禁用/撤销后启用不复活旧凭证、并发相同请求只生效一次、CLI 授予平台角色与网页禁用在两种锁序下的结果，以及去掉 `FOR UPDATE` 或近期认证检查后对应测试会失败的变异验证）、`internal/usersrv/integration/migrations_test.go`（在上一版库上原地升级，旧审计行保留）、`internal/usersrv/app/useradmin_test.go`（gRPC 层角色与错误映射）、`internal/e2e/user_admin_write_chain_test.go`（完整链路：来源/Origin/角色/CSRF/15 分钟认证叠加、操作者不可伪造、旧会话与真实 metro 管理令牌不复活、审计内容、禁止项没有任何入口）、`apps/admin` 的 `change.test.ts` 与 Playwright“account changes”用例（模拟 BFF，桌面与手机：影响与原因、版本冲突刷新、重新认证后不重放、丢失响应后同 ID 重试、原因变化换新 ID、受保护/注销中账号无操作）。
2. **真实浏览器与代理链**：`ADMIN_BROWSER_E2E=1 go test ./internal/e2e -run TestAdminBrowserChain`（CI 的 `admin-browser` 任务）。真实 BFF 与 usersrv，OzID、API、控制台和一个外来产品页分处同站点的不同保留测试子域名（`*.ozid-e2e.test`，一次性证书）；Chromium 实测：匿名 → OzID 登录 → 返回 `/users`、Cookie 仅属于 API 主机且不进存储、无角色“没有权限”且无循环无查询、只有 metro-admin 无用户分区、真实查询（邮箱精确、昵称字面包含、角色筛选、详情）、来源在名单外时页面和资源全部 403、已打开的控制台再查询被 BFF 周界拒绝而 OzID 账号功能不受影响、外来产品页（在公共 CORS 列表内）用操作员 Cookie 读用户被拒。**T5 增加（桌面与手机各一遍）：** 操作员在详情页依次禁用、启用、授予、撤销 `metro-admin`，被修改账号在另一个浏览器里的会话每次都在下一次请求失效，禁用时无法登录，重新登录后才带新角色；平台管理员自己的详情页没有任何操作；网关控制主机把操作员的密码认证时间调到 16 分钟前后，写入得到“请重新输入密码”，经 OzID `/security` 输错、输对密码后回到原详情页，页面没有重放写请求，账号未被改动，再人为重新发起才生效；来源移到白名单外后，已打开的控制台的写入被 BFF 周界拒绝。这里的网关是**替身**（用同一份 Go 周界规则和模板里的 CSP/头复刻 `admin-web` 的行为，并按受信代理的方式写 `X-Forwarded-For`）；Nginx 本身由 `scripts/admin-web-smoke.sh` 的容器验收检验。
3. **运维仍须自行验证**：真实 DNS、证书与 TLS 终止、真实来源地址与白名单取值、真实前置代理/CDN 链路（谁在 `trusted_proxies` 里、是否覆盖而非透传 `X-Forwarded-For`）、邮件厂商。自动化只覆盖受控环境，不能替代这些检查。

当前限制：

- 账号审计页未开放（审计行已写入 `security_audit`，目前由运维直接查询）；平台角色、平台管理员的状态与角色、密码、注销仍只能用 CLI 或用户自己在 OzID 完成。
- 网页写入的 15 分钟认证窗口是代码常量（与密码修改、注销等敏感操作共用 `SensitiveAuthMaxAge`），不可配置；写入限流可通过 `admin.write_rate_limit_per_minute` 调整。
- 控制台没有 metro 分区；metro `AdminService`/`AccessTokenService`/MCP 的来源策略和页面迁移是后续任务。metro-admin 在 BFF 侧的“把暂时故障当成未登录”的既有行为属于 [#89（T11）](https://github.com/kongken/mono/issues/89) 的故障与失效区分验收，届时修复并回归，T4 不处理。
- 限流只在 BFF 一层（usersrv 不另做）。分页令牌不做签名（只含位置，服务端每页仍校验调用者与过滤条件）。
- 来源不在名单时浏览器看到的是网络类错误而不是“来源被拒”：这是浏览器对不带 CORS 许可的跨域回答的行为，页面无法区分，也不应该区分。
