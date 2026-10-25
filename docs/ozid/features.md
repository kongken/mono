# OzID 用户中心：功能描述（T1 统一登录与安全返回；T2 统一注册验证与密码重置邮件；T3 账号资料、密码与设备会话）

本文描述 [OzID 规格](spec.md) 中 [Issue #79（T1）](https://github.com/kongken/mono/issues/79)、[Issue #80（T2）](https://github.com/kongken/mono/issues/80) 与 [Issue #81（T3）](https://github.com/kongken/mono/issues/81) 已实现的行为。其余阶段（独立管理控制台、网页用户管理、网页注销页、产品入口收敛、管理 IP 白名单）**尚未实现**，规格仍是它们的依据。文中域名全部是保留示例（`id.example.com` 等），不代表任何真实部署。

## 现状与范围

| 已实现（T1） | 尚未实现 |
| --- | --- |
| `apps/ozid`（`@kongken/ozid`）：登录页、账号概览、密码重新认证页，独立构建与部署 | 注销页（`/close-account`，产品里的注销照旧）；产品里的资料、改密码、设备页面也不变 |
| （T3）`/profile`、`/security`（改密码）、`/sessions`（设备列表、撤销指定设备、退出其他设备、退出这个浏览器）；BFF `RevokeOtherSessions` | |
| （T2）注册、找回密码、`/account/verify-email`、`/account/reset-password` 页面；邮件链接统一落到 OzID；返回上下文随挑战保存 | 产品里的注册、找回密码和邮件落地页仍保留（过渡期与产品入口收敛，后续阶段才移除） |
| BFF `ResolveNavigation`（匿名只读）与 `navigation` 服务端注册表 | `admin` 控制台应用；产品登录入口改指向 OzID |
| 同一 API origin、host-only Cookie、精确 CORS、CSRF 与 Origin 校验的真实浏览器验证 | 管理 IP 白名单；集中账号写来源收紧（后续收尾票） |
| 镜像 `ozid-web`、CI、配置与运行示例 | |

现有账号和业务数据保持不变：T1 没有迁移；T2 只给 `account_tokens` 增加两个可空列（见下文），不改已有行，产品自己的登录和账号页面照旧可用。

## 页面

| 路径 | 内容 |
| --- | --- |
| `/login` | 邮箱＋密码登录。无会话显示表单；已有有效会话显示“已登录”，可继续回到来源应用或进入概览，也可显式“使用其他账号登录”（会先说明这会退出这个浏览器上所有应用的当前登录）。 |
| `/` | 账号概览：显示名与登录邮箱（邮箱只在概览和资料页显示）、资料/安全/设备三个入口。无会话时转到 `/login`。 |
| `/security` | 两种模式。**带来源上下文**（地址里有 `app_id` / `return_path`，产品或控制台因为敏感操作把浏览器送来）：只显示“确认密码”（调用 BFF `Reauthenticate`），成功后更新“上次确认密码”的时间，并回到来源应用，或停留在 OzID。**直接打开**：先是“修改密码”，再是同一个“确认密码”。无会话、会话已失效（改密码、禁用、角色变化等使 `auth_version` 变化）时转到登录，**不会**把失效的身份升级成有效；登录后仍回到原来源。 |
| `/profile` | 本人登录邮箱只读（没有修改邮箱的入口），昵称和头像可编辑。 |
| `/sessions` | 本人有效的浏览器会话列表、撤销指定设备、“退出其他 N 台设备”、“退出这个浏览器的登录”。 |
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

**返回上下文。** 产品（或 OzID 的 `?app_id=&return_path=`）申请注册或重置时，BFF 在发邮件之前用导航注册表校验 `(app_id, return_path)`（规则同 T1），校验失败的请求以 `INVALID_ARGUMENT` 拒绝，不创建挑战、不发邮件。通过的上下文与挑战一起写入 `account_tokens.return_app_id` / `return_path`（两个可空、不可变的列，迁移 `20261025044828_account_token_return_context.sql`，成对出现的检查约束），因此用邮件在**另一个浏览器**里也能续上，不依赖原浏览器的存储。完成注册/重置时 usersrv 回传保存的上下文，BFF 出口处再校验一次：应用已下线或路径不再允许时静默丢弃，页面照常完成、只是没有“回到来源”。BFF 没有配置导航注册表时，申请若携带非空的应用 ID 或路径，以 `FAILED_PRECONDITION` 拒绝（没有注册表就无从担保，静默丢弃会让用户走进一条回不到来源的流程）；这个拒绝发生在调用 usersrv 之前，所以不会创建挑战、写审计、入队或发信；没有上下文的申请照常处理，完成接口在没有注册表时也不回传上下文。注意：trip-map 与 metro 的账号弹窗默认带着自己的应用 ID 作为上下文，所以发布 T2 的 BFF 必须同时配置 `navigation`（至少登记 `ozid` 与各产品）。旧挑战（没有上下文）完成后按无上下文处理，行为不变。usersrv 自己只校验上下文的形态（应用 ID 格式、共享的 `internal/platform/navpath` 路径规则），不持有注册表。

**落地页。** 页面先把令牌读入内存并立即从地址栏清除（`captureLandingToken()`），不写任何浏览器存储；打开页面不发任何请求（不查会话、不查导航、不消耗令牌），所以邮件安全扫描器预取链接不会用掉它；用户提交表单才调用 `CompleteRegistration` / `ResetPassword`；用过、过期、缺失或刷新后都显示同一个“链接不可用”页；在同一标签页打开另一封邮件的链接（只改了 fragment）会以新令牌重新开始。页面带 `<meta name="referrer" content="no-referrer">`。**完成不自动登录，也不改变这个浏览器已有的会话**（页面如此提示）：注册完成只创建账号；重置成功会按既有规则结束该账号的全部会话，但不会替任何人登录。完成后的“去登录”带着已保存并通过校验的上下文进入 `/login?app_id=…&return_path=…`，登录后安全返回。

**响应头。** `deploy/ozid-web/default.conf.template` 为 `/account/verify-email`、`/account/reset-password` 单独声明 `Cache-Control: no-store`、`Referrer-Policy: no-referrer`、`script-src 'self'` 的 CSP 及其余安全头（`add_header` 不继承，所以整组重复）；Vite 开发/预览服务器由 `accountLandingHeaders()` 设置同一组。`scripts/ozid-web-smoke.sh` 在容器里对这两个路径逐项检查。

**过渡期。** 升级前已发出（或已排队）的邮件指向产品自己的落地页，这些页面和令牌仍然有效：`auth.apps` 里保留的 `trip-map`、`metro` 条目只用于这些旧邮件，不会再为任何新邮件生成链接；排队中的旧任务若其来源应用已不再配置，则改用中央链接（令牌相同，OzID 可以完成）。条目可在令牌过期后（最长为 `registration_ttl` 与 `reset_ttl` 中较大者）从配置里删除。令牌从来不会被移到查询串，也没有任何“把片段改写成 query”的兼容层。

### 升级顺序、回滚与兼容边界（T2）

**邮件的切换点就是 usersrv 的升级**：新版 usersrv 一启动，之后签发的所有注册验证与重置邮件都链接到 `auth.apps.ozid.origin`。所以 OzID 的落地页必须先就绪，再升级 usersrv。

升级顺序（每步确认后再进行）：

1. **迁移**：先对 `usersrv` 库应用 `20261025044828_account_token_return_context.sql`。它只增加两个可空列和一个约束，对现有行没有回填、没有重写；旧版 usersrv 二进制不读写这两列，因此迁移可以早于任何二进制升级。
2. **`ozid-web`（带落地页的版本）**：部署并确认 `/account/verify-email`、`/account/reset-password` 可从 `auth.apps.ozid.origin` 打开且响应头正确（`scripts/ozid-web-smoke.sh` 对镜像做同样的检查），并且该 origin 已在 BFF 的 `cors_allowed_origins` 与 `navigation.apps.ozid` 里。**邮件切换前这一步必须完成。**
3. **usersrv**（T2 配置）：加入必填的 `auth.apps.ozid`，**保留** `trip-map`、`metro` 条目直到旧邮件过期。这一步之后新邮件才指向 OzID。
4. **BFF**（T2 版本，且配置了 `navigation`，见下）：晚于 usersrv。
5. 产品与其他前端。

**不支持的组合：新版 BFF 配旧版（T1）usersrv。** 新 BFF 把返回上下文放在原来的 `app_id` 字段里发给 usersrv，而旧 usersrv 把这个字段当作“选邮件 origin 的应用”，并且**空的 `app_id` 会被旧 usersrv 以 `INVALID_ARGUMENT` 拒绝**（例如 OzID 里没有来源应用的注册申请）。因此不能依赖这个组合做过渡；升级必须先 usersrv、后 BFF，回滚则先 BFF、后 usersrv。反过来的组合（旧 BFF 配新 usersrv）按字段含义是安全的（旧 BFF 发来的产品应用 ID 只会被当作返回上下文，链接始终指向 OzID），但它也不属于计划内的发布路径。

**回滚**：不要回退这个迁移（只有新增的可空列；旧二进制忽略它们，约束只限制新写入的行）。回滚 usersrv 到 T1 版本前须确认其配置里仍有 `trip-map` 与 `metro` 条目（T1 版本要求两者都存在），此后新邮件重新指向产品落地页；已经发出、指向 OzID 的邮件在过期前仍需要 `ozid-web` 与一个能完成它们的 BFF 在线（令牌在 usersrv 里，不依赖 BFF 版本），所以回滚后 `ozid-web` 至少保留到令牌过期（≤ `registration_ttl` 与 `reset_ttl` 中较大者）。

**BFF 必须配置 `navigation`**：产品的账号弹窗默认带着自己的应用 ID 作为返回上下文，没有注册表的 BFF 会以 `FAILED_PRECONDITION` 拒绝这类申请（见上文“返回上下文”）。发布 T2 的 BFF 前先把 `navigation`（`ozid` 与各产品）配好。

**已测试与未测试**：
- 已测试（`internal/usersrv/app`、`internal/usersrv/outbox`、`internal/e2e`，真实 Postgres/Redis）：没有上下文（两列为 NULL）的旧注册挑战完成后不返回任何应用（重置挑战走同一段读取代码，没有单独的用例）；升级前已排队的旧任务在“产品来源仍配置”与“已移除”两种配置下的链接；上下文列只填一半被约束拒绝；没有注册表时的 BFF 行为。
- **未执行**：滚动升级矩阵（旧/新 BFF × 旧/新 usersrv 的各种混合，以及 `ozid-web` 新版配旧 BFF 的窗口）没有自动化测试，上面对混合组合的结论来自对字段语义和 T1 代码的阅读，而不是运行结果。发布前如需依赖某个混合状态，请先在隔离环境验证。

## 账号资料、密码与设备（T3）

三个页面都要有效会话：无会话（从未登录、已退出、过期、被撤销、改过密码）转到登录；而检查登录状态本身失败（BFF、usersrv 或 Redis 暂时不可用）显示“这不代表你已退出登录”和重试按钮，**不会**跳转到登录，也不会调用退出或清理任何东西。账号导航只在有会话时出现。

**资料（`/profile`）。** 调用 `UserService.UpdateMyProfile`，请求只有 `display_name` 与 `avatar_url`：邮箱、用户、角色、状态都不是它的字段，所以不能被页面或伪造请求修改。登录邮箱只读，没有修改邮箱的流程。资料只写 usersrv 的共用昵称/头像，`profile_version` 随之变化，各产品在下一次请求时取用；它不碰 metro 的骑行者资料（`handle`、简介等）、metro 目录及其 `revision`（`TestChainSharedProfileEditLeavesEverythingElseAlone` 对此断言：修改前后骑行者资料与目录行数不变，邮箱、角色、`auth_version` 与其他设备的会话也不变）。

**密码（`/security`）。** 调用 `ChangePassword`，请求为当前密码和新密码：当前密码由 usersrv 校验（错误回答 `UNAUTHENTICATED`，页面显示“当前密码不正确”，会话不动）；新密码规则与注册相同（12 到 128 个字符）。成功后 usersrv 递增 `auth_version`，BFF 撤销该账号全部浏览器会话并清除当前 Cookie，既有管理令牌（MCP）在下次复核时因版本不同失效，**当前设备也要重新登录**：页面转到 `/login?reason=password-changed` 并说明“所有设备上的登录都已退出”。其他账号的会话和令牌不受影响；用旧密码登录失败，新密码可以；重新认证不能让已失效的会话复活。猜测当前密码受 usersrv 的账号级限流（默认 `password_per_account` 每 15 分钟 10 次，与“确认密码”共用，每次尝试都计数，成功也算；另有按地址的 `password_per_ip`）：达到上限后即使密码正确也被拒绝（`RESOURCE_EXHAUSTED`，“操作太频繁”），不改任何东西。真实密码验证时间只由 BFF 记录：`Reauthenticate` 与 `ChangePassword` 的请求里没有时间字段，错误密码不会移动它。

**设备（`/sessions`）。** 列表来自 BFF 的会话表，只显示真实存在的数据：登录时间、最近使用时间、有效期，以及哪个是当前设备；**不显示**也不记录设备型号、IP、位置或浏览器名称。
- **撤销指定设备**（`RevokeSession`）：只能撤销本人的会话；别人的会话 ID 与不存在的 ID 得到同一个 `NOT_FOUND`（文案也相同，不泄露存在性）；对已撤销的会话重复撤销成功（幂等，并发重复也都成功）；撤销的是发出请求的这个会话时，等同退出，Cookie 同时被清除。
- **退出其他设备**（`RevokeOtherSessions`）：对本人除当前会话以外所有有效会话执行**一条** `UPDATE`，所以并发中的会话不会漏掉，多个并发调用合计恰好撤销每个会话一次（`TestChainRevokeOtherSessionsIsAtomicUnderConcurrency`：8 个并发调用合计撤销 6 个，调用者不在其中）；幂等，重复调用返回 0 且成功；保留当前会话和 Cookie；**不**递增 `auth_version`、不改密码、不吊销管理（MCP）令牌；调用之后新登录的会话不受影响（只有之后的再次调用才会撤销它）。每个账号每 15 分钟最多 10 次，超过返回 `RESOURCE_EXHAUSTED`，且被拒绝的调用不撤销任何会话。它要求有效会话、CSRF 与允许的 Origin（与其他账号写操作相同）；usersrv 不可用时在会话检查处就返回 `UNAVAILABLE`，什么也不撤销。
- **退出这个浏览器**（`Logout`）：结束这个浏览器上共用的 Cookie 会话；产品等已接入的应用在**下一次**检查登录状态时发现已退出，其他设备保持登录。页面明确写出这个范围，并写明：OzID **不能、也不会**清除各应用保存在这个浏览器里的数据（本地足迹、未同步的修改等）——它没有跨域清理 localStorage 的能力，也不宣称有。退出请求失败时页面说明“你仍处于登录状态”，会话保持不变。
- 读取列表失败显示“暂时无法读取设备列表，这不代表你已退出登录”和重试；只有服务端回答“未登录”才转到登录。操作被限流或服务暂时不可用时，页面分别说“操作太频繁”和“这次请求没有生效”。

**迟到响应。** 登录状态的读取和设备列表的读取都有“只认最新”的边界（`@kongken/account/latest`）：被更新的读取取代、发生在退出之前、或跨越账号变化和页面卸载的旧响应一律丢弃，不再写入状态。旧的“已登录”不能盖掉更新的“未登录”，旧的“未登录”或失败也不能盖掉更新的有效身份；退出成功时，退出之前发出的读取全部作废。`SessionsList` 必须传入当前账号 id（`identity`）：账号一变，旧设备列表立即移除并丢弃还在路上的旧应答；撤销之后的新列表不会被更早发出的读取覆盖。OzID、metro 的登录状态提供者和三处设备列表（OzID、metro、trip-map）共用这套规则。

**产品入口不变。** trip-map 的账号弹窗和 metro 的账号页仍保留自己的资料、改密码和设备表单（共用 `packages/account` 的组件；设备列表现在多显示登录时间和有效期，没有“退出其他 N 台设备”按钮，那只在 OzID 开启）；收敛到 OzID 是后续阶段。

**部署顺序。** T3 没有迁移，也不改 usersrv：只有 BFF（新增 `RevokeOtherSessions`，并让 `RevokeSession` 撤销自己时清 Cookie）和 `ozid-web`。先升级 BFF，再升级 `ozid-web`；顺序反了时，新页面的“退出其他设备”会因 BFF 不认识这个方法而失败（页面显示服务暂时不可用，其余功能不受影响）。回滚 BFF 同理，不涉及数据。`navigation.apps.ozid.paths` 仍只需包含 `/security`（重新认证的返回目标）；资料和设备页不是返回目标，不必登记。

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

1. **代码与自动化**：Go 单测与真实 Postgres/Redis 的 `internal/e2e` 链路测试（`TestChainOzid*`：登录与安全返回、未登记同站点兄弟被拒、失效身份重新认证不升级；T2 的 `TestChainOzid*Mail*`：真实 outbox 与假厂商收件箱，覆盖中央链接、上下文绑定与持久化、旧挑战、另一个浏览器完成、不自动登录且不改已有会话、重放/过期、关闭注册、限流、BFF 出口再校验；这是真实 BFF 与 usersrv，不是 mock；T3 的 `device_sessions_chain_test.go`：退出其他设备保留当前会话与 Cookie、不动 `auth_version` 与管理令牌、不影响他人与之后的新登录、重复与 8 路并发调用合计恰好撤销每个会话一次、10 次/15 分钟限流且被拒不撤销、撤销指定设备的跨账号与不存在 ID 同一 `NOT_FOUND`、并发重复撤销、撤销自己清 Cookie、无会话/缺 CSRF/外来 Origin/usersrv 不可用都不撤销任何会话、改密只结束本账号的会话与管理令牌、猜测当前密码被限流、真实认证时间由服务端写入、资料修改不碰邮箱/角色/`auth_version`/他处会话与 metro 骑行者资料和目录）；`npm test`（路由、导航参数、API base 校验）；`npm run test:e2e:ozid`（**模拟 BFF**，桌面与手机，各种恶意返回参数、键盘、故障分类；T3 的 `ozid-account.spec.ts` 覆盖资料、改密码、设备三个页面（只读邮箱、请求体只含允许字段、错误分类、服务故障不当成退出、“退出其他设备”的说明与结果、退出这个浏览器不清本地存储、手机宽度无横向滚动）；T2 的 `ozid-mail.spec.ts` 覆盖注册/找回密码页面、冷却、限流与关闭注册等状态，落地页的令牌清除、不自动登录、重放与替换链接）。**证据边界**：模拟 BFF 的 Playwright 只证明 OzID 页面自身的行为（表单、键盘、版面、它发出的请求内容、它对服务端回答的处理、被拒绝时不跳转），**不**证明 Cookie 属性、CORS、CSRF、Origin 校验、同站点兄弟被拒或会话不入存储这类跨 origin 会话不变量——这些只由 `internal/e2e` 链路测试和第 3 项的真实栈测试证明。
2. **容器运行验收**：`scripts/ozid-web-smoke.sh`（CI 的 `ozid-web-image` 任务）构建 `ozid-web` 镜像并启动容器，检查 `/healthz`、页面与静态资源（类型、缓存、nosniff、产物里写入的 API base）、真实 Nginx 在页面及各回退路由上返回的 CSP（`connect-src 'self' <API_ORIGIN>`、无通配符、禁止被嵌入）与 `X-Frame-Options: DENY`、换一个有效 `API_ORIGIN` 无需重建，以及缺少或非法 `API_ORIGIN`（空值、http、无 scheme、含路径或结尾斜线、通配符、夹带第二条 CSP 指令、含空格）时容器拒绝启动；缺少或相对的 `VITE_API_BASE_URL` 构建失败。只用保留示例地址。
3. **真实浏览器与代理链**：`OZID_BROWSER_E2E=1 go test ./internal/e2e -run TestOzidBrowserChain`。在受控 HTTPS 下用真实 BFF 与 usersrv，OzID、API、两个产品和一个未登记兄弟分处同站点的不同保留测试子域名（`*.ozid-e2e.test`，由浏览器解析到本机前置代理，使用一次性证书）；Chromium 实测登录、返回、跨应用同一身份、退出、重新认证、Cookie 属性、存储与 URL 无令牌、CSP 无违规、兄弟域名无 CORS、外来 Origin 的写被拒；T2 起还包括真实邮件回路：一个浏览器申请注册/重置，另一个只拿邮件链接的浏览器完成，不自动登录、返回到绑定的产品页，落地页响应头取自 nginx 模板的落地页 `location`，令牌没有出现在任何请求 URL 或 Referer 里；T3 的两个用例（桌面与手机）：每个浏览器上下文是一台独立设备、各有自己的 Cookie 会话，实测改昵称后各设备和产品页同时看到、列表三台设备、撤销指定设备（三台中恰好一台失效）、退出其他设备后另两台与其产品页都已退出而当前设备仍在、之后新登录的设备保持有效、退出这个浏览器后本机各产品页看到匿名而另一台设备不受影响且本源的本地存储未被清除、错误当前密码不改任何东西、改密码后所有设备（含当前）退出并只能用新密码登录，以及外来 Origin / 缺 CSRF / 匿名对 `RevokeOtherSessions` 的请求都被拒且不撤销（邮件厂商是唯一的替身：收件箱经 `mailbox` 测试主机读取；这里的前置代理只**复刻**模板里的头，nginx 本身由第 2 项的容器验收检验）。需要 Node 依赖与 Playwright Chromium，CI 的 `ozid-browser` 任务运行它。
4. **仍须运维确认**（自动化无法覆盖）：真实 DNS、证书与 TLS 终止；反代是否按文档保留 `Host`、覆盖 `X-Real-IP`；真实域名下的 CORS/Cookie 表现；真实邮件厂商的投递与链接跟踪设置（邮件已统一指向 OzID，但至今没有对真实厂商实测，见[手动冒烟](../usersrv/mail-smoke-test.md)）；`auth.apps.ozid.origin` 与 BFF 注册表里 `ozid` 的 origin 是否一致；旧产品条目在令牌过期后的清理；镜像在私有构建参数下的构建。

当前限制：产品自己的登录、注册、找回密码和账号页面尚未改指向 OzID（产品入口收敛是后续阶段），产品的邮件落地页因此仍保留以服务旧邮件；`admin` 应用 ID 已被注册表接受，但控制台应用尚不存在；OzID 还没有注销页；设备列表没有 IP、位置或设备型号（BFF 不记录这些）；OzID 无法清除其他 origin 的浏览器存储；管理 IP 白名单和“集中账号写只接受 OzID Origin”的收紧在后续阶段，T1、T2、T3 都没有改变现有账号写方法的来源策略。注册与重置请求的来源应用只在 BFF 配置了导航注册表时才被接受并保存；`auth.apps.ozid` 现在是 usersrv 的必填配置，没有它 usersrv 拒绝启动。
