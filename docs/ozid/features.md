# OzID 用户中心：功能描述（T1 统一登录与安全返回；T2 统一注册验证与密码重置邮件）

本文描述 [OzID 规格](spec.md) 中 [Issue #79（T1）](https://github.com/kongken/mono/issues/79) 与 [Issue #80（T2）](https://github.com/kongken/mono/issues/80) 已实现的行为。其余阶段（独立管理控制台、网页用户管理、集中邮件落地、产品入口收敛、管理 IP 白名单）**尚未实现**，规格仍是它们的依据。文中域名全部是保留示例（`id.example.com` 等），不代表任何真实部署。

## 现状与范围

| 已实现（T1） | 尚未实现 |
| --- | --- |
| `apps/ozid`（`@kongken/ozid`）：登录页、账号概览、密码重新认证页，独立构建与部署 | 资料、改密码、设备管理、注销等自助页面（产品里的这些页面不变） |
| （T2）注册、找回密码、`/account/verify-email`、`/account/reset-password` 页面；邮件链接统一落到 OzID；返回上下文随挑战保存 | 产品里的注册、找回密码和邮件落地页仍保留（过渡期与产品入口收敛，后续阶段才移除） |
| BFF `ResolveNavigation`（匿名只读）与 `navigation` 服务端注册表 | `admin` 控制台应用；产品登录入口改指向 OzID |
| 同一 API origin、host-only Cookie、精确 CORS、CSRF 与 Origin 校验的真实浏览器验证 | 管理 IP 白名单；集中账号写来源收紧（后续收尾票） |
| 镜像 `ozid-web`、CI、配置与运行示例 | |

现有账号和业务数据保持不变：T1 没有迁移；T2 只给 `account_tokens` 增加两个可空列（见下文），不改已有行，产品自己的登录和账号页面照旧可用。

## 页面

| 路径 | 内容 |
| --- | --- |
| `/login` | 邮箱＋密码登录。无会话显示表单；已有有效会话显示“已登录”，可继续回到来源应用或进入概览，也可显式“使用其他账号登录”（会先说明这会退出这个浏览器上所有应用的当前登录）。 |
| `/` | 账号概览：显示名与登录邮箱（邮箱只在这里显示）、安全入口。无会话时转到 `/login`。 |
| `/security` | 重新输入当前密码（调用 BFF `Reauthenticate`），成功后更新“上次确认密码”的时间，并回到来源应用，或停留在 OzID。无会话、会话已失效（改密码、禁用、角色变化等使 `auth_version` 变化）时转到登录，**不会**把失效的身份升级成有效；登录后仍回到原来源。 |
| `/register`、`/forgot-password` | 申请注册、找回密码：只收邮箱；回答始终中性（不透露邮箱是否已注册）。有发送中、重新发送冷却、限流、注册已关闭、网络/服务故障等状态；登录页提供两个入口，三个页面之间切换会带着来源上下文。 |
| `/account/verify-email`、`/account/reset-password` | 邮件里的链接落地页（见下一节）。 |
| 其他路径 | “页面不存在”，不回退到别的页面。 |

表单支持键盘完成（字段顺序、Enter 提交、错误先说明字段），桌面与手机宽度均无横向滚动。登录失败只给一句中性文案；网络或服务故障与“已退出”分开，提供重试，不会清掉已有的会话显示。

## 导航上下文与安全返回

产品把浏览器顶层导航到 OzID，只带两个查询参数：

```
https://id.example.com/login?app_id=metro&return_path=%2Fcity%2Fshanghai
```

- `app_id`：稳定应用 ID（`ozid`、`admin`、`trip-map`、`metro`），不是主机名；`return_path`：该应用允许的规范 pathname，可省略（省略则返回该应用的默认路径）。
- OzID 在**任何跳转之前**调用 BFF `ResolveNavigation`，只跳转服务端返回的 `return_url`；该地址由注册表的 origin 加已校验 pathname 拼成，页面不用自己拼接，也不解析客户端给出的 URL。
- 拒绝（`INVALID_ARGUMENT`）的情形：未知应用或 `app_id` 本身是地址；`return_path` 是绝对 URL、`//` 开头的协议相对地址、含反斜线、`.`/`..` 段、百分号编码（含编码的点段）、query、fragment、控制字符、超长，或不在该应用登记的路径内；参数重复；有 `return_path` 而无 `app_id`。被拒绝时登录/确认密码仍可完成，但只进入 OzID（概览），页面说明“无法确认要返回的应用或页面，因此不会跳转”，**不会**先跳到不受控的地址。
- 服务端回答 `INVALID_ARGUMENT` 或 `FAILED_PRECONDITION`（BFF 未配置 `navigation`）都按“被拒绝”处理；注册表请求本身失败（网络错误、服务端故障）与“被拒绝”分开显示（可稍后重试），同样不跳转。
- 没有来源上下文时，登录后进入概览；登录和重新认证都不会自动重放此前的管理写入，业务页面回来后需要重新发起操作。
- 返回不依赖客户端给出的用户 ID、认证时间或昵称：业务页面回来后用 BFF Cookie 调用 `Me` 确定身份。
- `ResolveNavigation` 匿名、只读（不要求 CSRF）、不建立会话、不签发任何票据；能构造出合法参数并不能获得身份。它在浏览器之外也能直接调用，所以**有意只公开已登记的导航信息**：已登记应用的 origin 加允许的路径（返回的 `return_url`、`ozid_origin`），不含任何用户数据、会话、账号是否存在等信息，也不提供身份能力；未登记的输入一律得到同一个拒绝回答，不说明是应用、路径还是格式的问题。

## 邮件流程（T2）

**链接。** 注册验证和密码重置邮件的链接只有一种形状：`<中央 OzID origin><固定路径>#token=<令牌>`，固定路径是 `/account/verify-email` 或 `/account/reset-password`，令牌只在 URL fragment。邮件 origin 只来自 usersrv 的服务端配置 `auth.apps.ozid.origin`（必填），发起请求的产品、请求里的任何字段都不能改变它；邮件正文和链接里没有来源应用或返回路径。`ozid` 的 origin 必须和 BFF `navigation.apps.ozid.origin` 一致，并在 `cors_allowed_origins` 里（运维配置项，启动时 usersrv 只校验自己的配置）。

**返回上下文。** 产品（或 OzID 的 `?app_id=&return_path=`）申请注册或重置时，BFF 在发邮件之前用导航注册表校验 `(app_id, return_path)`（规则同 T1），校验失败的请求以 `INVALID_ARGUMENT` 拒绝，不创建挑战、不发邮件。通过的上下文与挑战一起写入 `account_tokens.return_app_id` / `return_path`（两个可空、不可变的列，迁移 `20261025044828_account_token_return_context.sql`，成对出现的检查约束），因此用邮件在**另一个浏览器**里也能续上，不依赖原浏览器的存储。完成注册/重置时 usersrv 回传保存的上下文，BFF 出口处再校验一次：应用已下线或路径不再允许时静默丢弃，页面照常完成、只是没有“回到来源”。没有注册表的 BFF 不接受也不回传上下文。旧挑战（没有上下文）完成后按无上下文处理，行为不变。usersrv 自己只校验上下文的形态（应用 ID 格式、共享的 `internal/platform/navpath` 路径规则），不持有注册表。

**落地页。** 页面先把令牌读入内存并立即从地址栏清除（`captureLandingToken()`），不写任何浏览器存储；打开页面不发任何请求（不查会话、不查导航、不消耗令牌），所以邮件安全扫描器预取链接不会用掉它；用户提交表单才调用 `CompleteRegistration` / `ResetPassword`；用过、过期、缺失或刷新后都显示同一个“链接不可用”页；在同一标签页打开另一封邮件的链接（只改了 fragment）会以新令牌重新开始。页面带 `<meta name="referrer" content="no-referrer">`。**完成不自动登录，也不改变这个浏览器已有的会话**（页面如此提示）：注册完成只创建账号；重置成功会按既有规则结束该账号的全部会话，但不会替任何人登录。完成后的“去登录”带着已保存并通过校验的上下文进入 `/login?app_id=…&return_path=…`，登录后安全返回。

**响应头。** `deploy/ozid-web/default.conf.template` 为 `/account/verify-email`、`/account/reset-password` 单独声明 `Cache-Control: no-store`、`Referrer-Policy: no-referrer`、`script-src 'self'` 的 CSP 及其余安全头（`add_header` 不继承，所以整组重复）；Vite 开发/预览服务器由 `accountLandingHeaders()` 设置同一组。`scripts/ozid-web-smoke.sh` 在容器里对这两个路径逐项检查。

**过渡期。** 升级前已发出（或已排队）的邮件指向产品自己的落地页，这些页面和令牌仍然有效：`auth.apps` 里保留的 `trip-map`、`metro` 条目只用于这些旧邮件，不会再为任何新邮件生成链接；排队中的旧任务若其来源应用已不再配置，则改用中央链接（令牌相同，OzID 可以完成）。条目可在令牌过期后（最长为 `registration_ttl` 与 `reset_ttl` 中较大者）从配置里删除。令牌从来不会被移到查询串，也没有任何“把片段改写成 query”的兼容层。

## 服务端配置：`navigation`

BFF 配置（`config.bff.example.yaml`）新增段落，字段与[规格 §4](spec.md) 对应：

```yaml
cors_allowed_origins: [https://id.example.com, https://trip.example.com, https://metro.example.com]
navigation:
  api_origin: https://api.example.com       # BFF 的规范公开 origin
  apps:
    ozid:     {origin: https://id.example.com, paths: ["/security"]}
    trip-map: {origin: https://trip.example.com}
    metro:    {origin: https://metro.example.com, paths: ["/me", "/city/*"]}
```

路径规则：默认路径（缺省 `/`）总是允许；`paths` 里的项是精确 pathname，或以 `/*` 结尾的前缀（`/city/*` 允许 `/city` 及其下所有路径，`/*` 允许整个应用）。启动时校验，**配置缺失或不合法会让 BFF 启动失败并指出字段**，不会降级：

- `navigation` 段整体缺省 = 不启用，`ResolveNavigation` 返回 `FAILED_PRECONDITION`（带来源参数的登录只进入 OzID，不跳转，也不会放行任何地址）。设置了任一字段就需要 `api_origin` 与至少含 `ozid` 的 `apps`。
- 每个 origin 必须是精确的 `scheme://host[:port]`（无路径、query、userinfo）；`auth.cookie_secure: true` 时必须是 https（plain http 仅限本机回环主机名/地址，且只在不启用 secure Cookie 时可用）。
- 每个应用 origin 必须同时出现在 `cors_allowed_origins`，各应用 origin 不重复。
- `api_origin` 与每个应用 origin 必须在同一 schemeful site（按 public suffix 判定），否则 host-only Cookie 不会被浏览器发送。同站点是必要条件，**不是**授权：兄弟子域名只要不在 `cors_allowed_origins` 就拿不到 CORS，也过不了 Origin/CSRF 校验。
- 同一份代码可用于不同的同站点域名组合：更换域名只改这里和前端构建参数，不改源码。

## 会话与跨域不变量

- 所有前端（OzID、产品）直接调用**同一个** API origin，带 `credentials: include`。`kongken_sid` 为 host-only（无 `Domain`）、HttpOnly、Secure（生产）、SameSite=Lax、Path `/`；会话令牌不在 URL，也不进任何浏览器存储。OzID 不读取 API 主机的 Cookie：CSRF 令牌取自 `IssueCSRFToken` 的响应体并放进 `X-CSRF-Token`。
- CORS 是精确 origin（协议、主机、端口），不用 `*`、不回显任意 Origin；预检和错误响应与正常响应一致。同站点 Cookie 被附带不等于通过 Origin/CSRF：写请求仍须 Origin 在允许列表且 CSRF 成对。
- OzID 没有同源 `/api` 代理：构建时 `VITE_API_BASE_URL` 必须是绝对 `https://` 地址（带路径，如 `https://api.example.com/api`；仅本机回环开发地址允许 `http`）。缺失或不合法时**构建失败**；运行时遇到非法值也显示明确的配置错误页，而不是发出请求。

## 构建、镜像与运行

```bash
# 本机开发：Vite 5175，API base 默认为 .env.development 的回环示例 http://localhost:18081/api
npm run dev:ozid

# 构建：必须给出 BFF 的公开 API base
VITE_API_BASE_URL=https://api.example.com/api npm run build:ozid

# 镜像（Nginx，只提供静态文件，不转发任何请求）
docker build -f deploy/ozid-web/Dockerfile \
  --build-arg VITE_API_BASE_URL=https://api.example.com/api -t ozid-web .
docker run --rm -p 8089:80 -e API_ORIGIN=https://api.example.com ozid-web
```

- 镜像没有默认 API 地址：没传 `VITE_API_BASE_URL` 时构建失败。`API_ORIGIN`（运行时环境变量）是该地址的 origin，用于页面的 Content-Security-Policy（`connect-src 'self' <API_ORIGIN>`）；缺失、非 https、含路径或通配符时容器拒绝启动。响应头还有 `X-Frame-Options: DENY`、`frame-ancestors 'none'`、`Referrer-Policy: no-referrer`，外壳 `index.html` 不缓存。
- CI 用保留示例地址构建 OzID 以验证构建配方；发布工作流同样用示例地址构建 `ghcr.io/kongken/mono-ozid-web`，它只是构建检查，**不是可直接部署的镜像**：部署方用自己的私有构建参数构建。真实域名、IP 白名单和凭据不进入公开仓库。
- 运行示例见 `deploy/compose.stack.example.yaml`（`ozid-web`）和 `deploy/reverse-proxy/Caddyfile.example`（`api.`、`id.` 两个示例站点）。
- 镜像发布和 CI 都不会自动部署，不会清理数据库、对象存储或浏览器存储。

## 验证与限制

分四层给出结果（完成报告里同样分开）：

1. **代码与自动化**：Go 单测与真实 Postgres/Redis 的 `internal/e2e` 链路测试（`TestChainOzid*`：登录与安全返回、未登记同站点兄弟被拒、失效身份重新认证不升级；T2 的 `TestChainOzid*Mail*`：真实 outbox 与假厂商收件箱，覆盖中央链接、上下文绑定与持久化、旧挑战、另一个浏览器完成、不自动登录且不改已有会话、重放/过期、关闭注册、限流、BFF 出口再校验；这是真实 BFF 与 usersrv，不是 mock）；`npm test`（路由、导航参数、API base 校验）；`npm run test:e2e:ozid`（**模拟 BFF**，桌面与手机，各种恶意返回参数、键盘、故障分类；T2 的 `ozid-mail.spec.ts` 覆盖注册/找回密码页面、冷却、限流与关闭注册等状态，落地页的令牌清除、不自动登录、重放与替换链接）。**证据边界**：模拟 BFF 的 Playwright 只证明 OzID 页面自身的行为（表单、键盘、版面、它发出的请求内容、它对服务端回答的处理、被拒绝时不跳转），**不**证明 Cookie 属性、CORS、CSRF、Origin 校验、同站点兄弟被拒或会话不入存储这类跨 origin 会话不变量——这些只由 `internal/e2e` 链路测试和第 3 项的真实栈测试证明。
2. **容器运行验收**：`scripts/ozid-web-smoke.sh`（CI 的 `ozid-web-image` 任务）构建 `ozid-web` 镜像并启动容器，检查 `/healthz`、页面与静态资源（类型、缓存、nosniff、产物里写入的 API base）、真实 Nginx 在页面及各回退路由上返回的 CSP（`connect-src 'self' <API_ORIGIN>`、无通配符、禁止被嵌入）与 `X-Frame-Options: DENY`、换一个有效 `API_ORIGIN` 无需重建，以及缺少或非法 `API_ORIGIN`（空值、http、无 scheme、含路径或结尾斜线、通配符、夹带第二条 CSP 指令、含空格）时容器拒绝启动；缺少或相对的 `VITE_API_BASE_URL` 构建失败。只用保留示例地址。
3. **真实浏览器与代理链**：`OZID_BROWSER_E2E=1 go test ./internal/e2e -run TestOzidBrowserChain`。在受控 HTTPS 下用真实 BFF 与 usersrv，OzID、API、两个产品和一个未登记兄弟分处同站点的不同保留测试子域名（`*.ozid-e2e.test`，由浏览器解析到本机前置代理，使用一次性证书）；Chromium 实测登录、返回、跨应用同一身份、退出、重新认证、Cookie 属性、存储与 URL 无令牌、CSP 无违规、兄弟域名无 CORS、外来 Origin 的写被拒；T2 起还包括真实邮件回路：一个浏览器申请注册/重置，另一个只拿邮件链接的浏览器完成，不自动登录、返回到绑定的产品页，落地页响应头取自 nginx 模板的落地页 `location`，令牌没有出现在任何请求 URL 或 Referer 里（邮件厂商是唯一的替身：收件箱经 `mailbox` 测试主机读取；这里的前置代理只**复刻**模板里的头，nginx 本身由第 2 项的容器验收检验）。需要 Node 依赖与 Playwright Chromium，CI 的 `ozid-browser` 任务运行它。
4. **仍须运维确认**（自动化无法覆盖）：真实 DNS、证书与 TLS 终止；反代是否按文档保留 `Host`、覆盖 `X-Real-IP`；真实域名下的 CORS/Cookie 表现；真实邮件厂商的投递与链接跟踪设置（邮件已统一指向 OzID，但至今没有对真实厂商实测，见[手动冒烟](../usersrv/mail-smoke-test.md)）；`auth.apps.ozid.origin` 与 BFF 注册表里 `ozid` 的 origin 是否一致；旧产品条目在令牌过期后的清理；镜像在私有构建参数下的构建。

当前限制：产品自己的登录、注册、找回密码和账号页面尚未改指向 OzID（产品入口收敛是后续阶段），产品的邮件落地页因此仍保留以服务旧邮件；`admin` 应用 ID 已被注册表接受，但控制台应用尚不存在；OzID 还没有资料、改密码、设备管理与注销页面；管理 IP 白名单和“集中账号写只接受 OzID Origin”的收紧在后续阶段，T1、T2 都没有改变现有账号写方法的来源策略。注册与重置请求的来源应用只在 BFF 配置了导航注册表时才被接受并保存；`auth.apps.ozid` 现在是 usersrv 的必填配置，没有它 usersrv 拒绝启动。
