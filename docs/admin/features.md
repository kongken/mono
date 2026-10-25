# 统一管理控制台：功能描述（T4 受控控制台与用户查询）

本文描述 [OzID 规格](../ozid/spec.md) 中 [Issue #82（T4）](https://github.com/kongken/mono/issues/82) 已实现的行为：独立的管理控制台 `apps/admin`、它的受控入口（来源网络、控制台 Origin、会话与平台角色），以及只读的用户查询。**尚未实现**的部分见文末“当前限制”：用户管理写操作、metro 后台迁移、账号审计页、metro 管理接口的来源策略切换。文中域名与地址全部是保留示例（`admin.example.com`、`203.0.113.0/24` 等），不代表任何真实部署。

## 现状与范围

| 已实现（T4） | 尚未实现 |
|---|---|
| `apps/admin`（`@kongken/admin`）：独立构建、独立镜像 `admin-web`；匿名跳 OzID 登录并返回；无角色显示“没有权限”（不跳转、不循环）；按真实角色显示分区；“管理 OzID 账号”链接到 OzID | metro 后台页面迁移（控制台里没有 metro 分区，也不展示假分区）、账号审计页 |
| BFF `bff.v1.UserAdminService.ListUsers/GetUser`（只读）和内部 `usersrv.v1.UserAdminService`；`required_role = "ozid-admin"`，BFF 入口与 usersrv 各自独立校验 | 用户管理**写**（禁用/启用、授予/撤销角色）：本票不开放，网页上没有任何入口 |
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

- `/`：角色与可用分区。`/users`：筛选 + 表格 + 分页；`/users/:id`：账号详情（只读）。
- 筛选：邮箱精确（规范化为小写；不是前缀/包含）、用户 ID、昵称（字面包含，不区分大小写，`%`、`_` 是普通字符，1–40 字符）、状态、角色。邮箱/用户 ID/昵称**最多选一种**。筛选条件只保存在组件状态里，**不写入地址栏、历史或浏览器存储**。
- 分页：服务端按“创建时间倒序、ID 倒序”稳定排序；默认 50 条，上限 100，负数拒绝；翻页令牌不透明（页面不解析，不展示），“上一页”由页面保存已走过的令牌栈。较慢的旧响应不会覆盖较新的结果（最新请求才生效）。
- 返回字段只有：用户 ID、邮箱、昵称、状态（正常/已禁用/注销中）、角色、注册时间。**没有**密码哈希、令牌、会话、产品私有数据，也没有批量导出。
- 错误按含义显示：会话失效 → 回 OzID 登录；密码确认过旧（`AdminSessionMaxAge`，默认 12 小时内的认证）→ 提示并给“前往 OzID 验证密码”按钮（不自动跳转，所以不会循环，也不重放输入）；缺角色 → “没有权限”；限流、服务不可用 → 可重试提示。**来源不在白名单**时，浏览器的预检/调用被 BFF 以不带 CORS 许可的 403 拒绝，页面只能看到“无法连接服务器”（浏览器不会把被拒绝的跨域回答交给脚本）；而在网关处，这样的来源连页面都拿不到。

## 受控入口（周界）

三道彼此独立、规则一致的防线，缺一不可：

1. **网关/代理**（运维自管，示例见 `deploy/reverse-proxy/Caddyfile.example`）：对控制台主机做来源限制。
2. **`admin-web` 镜像的 Nginx**：控制台页面、静态资源、SPA fallback、甚至 404 都在来源白名单之后；来源不在名单的请求对**所有路径**得到 403（正文只有 `forbidden`，不带控制台内容，也不通过 404/403 差异暴露文件是否存在）；`Host` 必须恰好是 `ADMIN_HOST`（别的名字或带端口一律不回应）；`/healthz` 只在默认 server 上应答，不在控制台主机上（Nginx 的 `return` 先于访问检查，放在控制台 server 上等于对所有来源开门）。
3. **BFF**（`admin` 配置）：对每个新增的用户管理读接口独立检查**来源 IP、控制台 Origin、有效会话、平台角色**；周界检查在 CORS 与 CSRF 之前运行并对任何方法（含 `OPTIONS` 预检）生效：被拒绝时只返回不带 CORS 许可的 403，不说明是哪个条件失败。

规则：

- 白名单为空（或没有注册 `navigation.apps.admin`）= **全部拒绝**，不会自动放开。CIDR 必须带前缀、无主机位，IPv4 不得比 `/8` 宽、IPv6 不得比 `/16` 宽，不接受 IPv4 映射的 IPv6 网段；`0.0.0.0/0`、`::/0`、通配符启动即被拒绝（BFF 配置校验，容器入口脚本同样校验）。
- 客户端地址：默认就是 TCP 对端。**只有**对端属于 `trusted_proxies` 时才读取转发头（默认 `X-Forwarded-For`，多行合并），从右向左跳过受信代理跳点，取第一个不受信地址；无法解析的跳点拒绝；没有转发头则代理本身就是客户端。伪造转发头、`X-Real-IP`/`Forwarded` 等其他头、另一个 Host/端口或另一个代理都绕不过去。代理必须**追加**而不是透传客户端的值（Caddy 示例用 `header_up X-Forwarded-For {remote_host}` 覆盖）。
- Origin 必须**恰好等于** `navigation.apps.admin.origin`（单个值）。产品（山海足迹、地铁足迹）在公共 CORS 列表里也不能调用管理数据；缺失或伪造 Origin 不是认证，仍须同时通过 Cookie、来源和角色。读方法免 CSRF，但**不免**来源、Origin、角色。
- 受影响的现有功能：登录、注册、账号设置、产品匿名与普通功能都不在周界内，来源不在名单时照常可用（浏览器验收实测）。
- 本票**没有**给 metro 的 `AdminService`/`AccessTokenService` 加来源限制；它们的来源策略随各自迁移任务切换，不能因本票悄悄关闭现有维护入口。

## 平台角色 `ozid-admin`

- 只通过运维 CLI 授予/撤销：`usersrv-admin grant-role -email E|-user-id U -role ozid-admin -operator NAME -reason TEXT`，`revoke-role` 同理（没有单独的 `init` 命令：第一个平台管理员也是这样授予；**不会**因为谁最先注册而自动授予）。每次变更提升账号 `auth_version`（旧会话和管理令牌在下次检查失效）并写 `security_audit`（含被拒绝和未改变的尝试）。
- `ozid-admin` 与 `metro-admin` 相互独立：持有一个不意味着持有另一个，撤销一个不影响另一个。
- usersrv 迁移 `migrations/usersrv/20261025120000_platform_admin_role.sql` 把 `user_roles` 的角色检查约束扩展为 `('metro-admin','ozid-admin')`；回滚前必须先撤销所有 `ozid-admin`，否则旧约束会拒绝这些行。
- 平台管理员保护：`BeginDeletion` 在账号行锁内发现 `ozid-admin` 即拒绝（原因 `PLATFORM_ADMIN_PROTECTED`，审计 `deletion.begin=refused_platform_admin`）；BFF 的注销流程在开始阶段遇到这个永久拒绝时丢弃该工作流，所以之后即使撤销角色，账号也不会被之前的请求自动关闭。共享前端包 `@kongken/account` 的错误分类已把这个拒绝识别为独立的 `platform_admin` 类（给出“请联系运维先回收管理员角色”的明确提示）；网页注销页本身是后续阶段，目前只有 BFF 的注销接口受此保护。

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
```

`admin-web` 镜像（`deploy/admin-web`）：

| 类型 | 名称 | 说明 |
|---|---|---|
| 构建参数（必填，无默认） | `VITE_API_BASE_URL` | BFF 的公开 API base（绝对 https，例：`https://api.example.com/api`）；控制台没有同源 `/api` 代理 |
| 构建参数（必填，无默认） | `VITE_OZID_ORIGIN` | OzID 的 origin（登录与密码确认在那里完成） |
| 运行环境变量（必填） | `API_ORIGIN` | 上面 API base 的 origin，只用于页面 CSP 的 `connect-src` |
| 运行环境变量（必填） | `ADMIN_HOST` | 控制台唯一回应的主机名（小写、无端口、无通配符） |
| 运行环境变量（必填） | `ADMIN_ALLOWED_CIDRS` | 逗号分隔的来源网段；**空值、全开、无前缀、含主机位的值都使容器拒绝启动** |
| 运行环境变量（可选） | `ADMIN_TRUSTED_PROXIES`、`ADMIN_REAL_IP_HEADER` | 前置代理网段与它写入的头（默认 `X-Forwarded-For`）；不设则只看 TCP 对端 |

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

**部署顺序。** usersrv（含迁移 `20261025120000_platform_admin_role.sql`）→ BFF（新配置 `admin`、注册 `navigation.apps.admin`、CORS 加入控制台 origin）→ 网关与 `admin-web`。BFF 先于控制台上线时，管理读接口已经受周界保护。回滚：先下线控制台，再回滚 BFF；回滚 usersrv 迁移前先用 CLI 撤销所有 `ozid-admin`。镜像不会自动部署、不会自动开放白名单。

## 验证与限制

按三层分别陈述证据，不互相替代：

1. **代码与自动化**：Go 单测与真实 Postgres/Redis 的 `internal/e2e`（`TestChainUserAdmin*`：四种角色矩阵、控制台 Origin、来源策略（含受信代理链、伪造头、预检、其他路径）、查询/分页/脱敏审计、限流、角色生命周期与注销保护、认证过旧）；`internal/bff/adminguard`、`ingress` 周界单测、`config` 校验单测；usersrv 的 `useradmin` 单测与 gRPC 层独立授权；迁移集成测试；`cmd/usersrv-admin` 的 CLI 测试；`npm run test:admin`；`npm run test:e2e:admin`（**模拟 BFF**，覆盖匿名跳转、无权限不循环、按角色分区、列表/筛选/分页/详情、过期响应不覆盖新结果、各类错误、手机宽度、地址与存储无个人信息）。模拟 BFF 的结果只证明页面自身行为。
2. **真实浏览器与代理链**：`ADMIN_BROWSER_E2E=1 go test ./internal/e2e -run TestAdminBrowserChain`（CI 的 `admin-browser` 任务）。真实 BFF 与 usersrv，OzID、API、控制台和一个外来产品页分处同站点的不同保留测试子域名（`*.ozid-e2e.test`，一次性证书）；Chromium 实测：匿名 → OzID 登录 → 返回 `/users`、Cookie 仅属于 API 主机且不进存储、无角色“没有权限”且无循环无查询、只有 metro-admin 无用户分区、真实查询（邮箱精确、昵称字面包含、角色筛选、详情）、来源在名单外时页面和资源全部 403、已打开的控制台再查询被 BFF 周界拒绝而 OzID 账号功能不受影响、外来产品页（在公共 CORS 列表内）用操作员 Cookie 读用户被拒。这里的网关是**替身**（用同一份 Go 周界规则和模板里的 CSP/头复刻 `admin-web` 的行为，并按受信代理的方式写 `X-Forwarded-For`）；Nginx 本身由 `scripts/admin-web-smoke.sh` 的容器验收检验。
3. **运维仍须自行验证**：真实 DNS、证书与 TLS 终止、真实来源地址与白名单取值、真实前置代理/CDN 链路（谁在 `trusted_proxies` 里、是否覆盖而非透传 `X-Forwarded-For`）、邮件厂商。自动化只覆盖受控环境，不能替代这些检查。

当前限制：

- 用户管理写操作（禁用/启用、授予/撤销角色）和账号审计页未开放；角色与账号状态仍只能用 CLI。
- 控制台没有 metro 分区；metro `AdminService`/`AccessTokenService`/MCP 的来源策略和页面迁移是后续任务。metro-admin 在 BFF 侧的“把暂时故障当成未登录”的既有行为属于 [#89（T11）](https://github.com/kongken/mono/issues/89) 的故障与失效区分验收，届时修复并回归，T4 不处理。
- 限流只在 BFF 一层（usersrv 不另做）。分页令牌不做签名（只含位置，服务端每页仍校验调用者与过滤条件）。
- 来源不在名单时浏览器看到的是网络类错误而不是“来源被拒”：这是浏览器对不带 CORS 许可的跨域回答的行为，页面无法区分，也不应该区分。
