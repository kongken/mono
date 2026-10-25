# OzID 用户中心：功能描述（T1：统一登录、重新认证与安全返回）

本文描述 [OzID 规格](spec.md) 中 [Issue #79（T1）](https://github.com/kongken/mono/issues/79) 已实现的行为。其余阶段（独立管理控制台、网页用户管理、集中邮件落地、产品入口收敛、管理 IP 白名单）**尚未实现**，规格仍是它们的依据。文中域名全部是保留示例（`id.example.com` 等），不代表任何真实部署。

## 现状与范围

| 已实现（T1） | 尚未实现 |
| --- | --- |
| `apps/ozid`（`@kongken/ozid`）：登录页、账号概览、密码重新认证页，独立构建与部署 | 资料、改密码、设备管理、注销等自助页面；注册与找回密码入口及邮件落地页（产品里的这些页面不变） |
| BFF `ResolveNavigation`（匿名只读）与 `navigation` 服务端注册表 | `admin` 控制台应用；产品登录入口改指向 OzID |
| 同一 API origin、host-only Cookie、精确 CORS、CSRF 与 Origin 校验的真实浏览器验证 | 管理 IP 白名单；集中账号写来源收紧（后续收尾票） |
| 镜像 `ozid-web`、CI、配置与运行示例 | |

现有账号和业务数据保持不变：没有迁移、没有 schema 变化，产品自己的登录和账号页面照旧可用。

## 页面

| 路径 | 内容 |
| --- | --- |
| `/login` | 邮箱＋密码登录。无会话显示表单；已有有效会话显示“已登录”，可继续回到来源应用或进入概览，也可显式“使用其他账号登录”（会先说明这会退出这个浏览器上所有应用的当前登录）。 |
| `/` | 账号概览：显示名与登录邮箱（邮箱只在这里显示）、安全入口。无会话时转到 `/login`。 |
| `/security` | 重新输入当前密码（调用 BFF `Reauthenticate`），成功后更新“上次确认密码”的时间，并回到来源应用，或停留在 OzID。无会话、会话已失效（改密码、禁用、角色变化等使 `auth_version` 变化）时转到登录，**不会**把失效的身份升级成有效；登录后仍回到原来源。 |
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
- `ResolveNavigation` 匿名、只读（不要求 CSRF）、不建立会话、不签发任何票据；能构造出合法参数并不能获得身份。

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

1. **代码与自动化**：Go 单测与真实 Postgres/Redis 的 `internal/e2e` 链路测试（`TestChainOzid*`：登录与安全返回、未登记同站点兄弟被拒、失效身份重新认证不升级）；`npm test`（路由、导航参数、API base 校验）；`npm run test:e2e:ozid`（模拟 BFF，桌面与手机，各种恶意返回参数、键盘、故障分类）。
2. **容器运行验收**：`scripts/ozid-web-smoke.sh`（CI 的 `ozid-web-image` 任务）构建 `ozid-web` 镜像并启动容器，检查 `/healthz`、页面与静态资源（类型、缓存、nosniff、产物里写入的 API base）、真实 Nginx 在页面及各回退路由上返回的 CSP（`connect-src 'self' <API_ORIGIN>`、无通配符、禁止被嵌入）与 `X-Frame-Options: DENY`、换一个有效 `API_ORIGIN` 无需重建，以及缺少或非法 `API_ORIGIN`（空值、http、无 scheme、含路径或结尾斜线、通配符、夹带第二条 CSP 指令、含空格）时容器拒绝启动；缺少或相对的 `VITE_API_BASE_URL` 构建失败。只用保留示例地址。
3. **真实浏览器与代理链**：`OZID_BROWSER_E2E=1 go test ./internal/e2e -run TestOzidBrowserChain`。在受控 HTTPS 下用真实 BFF 与 usersrv，OzID、API、两个产品和一个未登记兄弟分处同站点的不同保留测试子域名（`*.ozid-e2e.test`，由浏览器解析到本机前置代理，使用一次性证书）；Chromium 实测登录、返回、跨应用同一身份、退出、重新认证、Cookie 属性、存储与 URL 无令牌、CSP 无违规、兄弟域名无 CORS、外来 Origin 的写被拒。需要 Node 依赖与 Playwright Chromium，CI 的 `ozid-browser` 任务运行它。
4. **仍须运维确认**（自动化无法覆盖）：真实 DNS、证书与 TLS 终止；反代是否按文档保留 `Host`、覆盖 `X-Real-IP`；真实域名下的 CORS/Cookie 表现；邮件厂商（邮件链接仍由产品入口按原样发送，集中到 OzID 是后续阶段）；镜像在私有构建参数下的构建。

当前限制：产品自己的登录/账号页面尚未改指向 OzID；`admin` 应用 ID 已被注册表接受，但控制台应用尚不存在；OzID 里没有注册和找回密码入口；管理 IP 白名单和“集中账号写只接受 OzID Origin”的收紧在后续阶段，本票没有改变现有账号写方法的来源策略。
