# usersrv 自建用户系统规格

> **状态：实现中（P1、P2 已实现，P3–P6 待实现）。** 当前主干的浏览器登录仍使用 Logto；`usersrv` 目前提供账号核心与邮件模块（见下方“实现状态登记”，邮件还没有对外入口，注册与找回流程在 P3），BFF 尚未调用它，**不开放任何新登录或注册**。实现期间不得把本文中尚未登记为已实现的目标能力写成已经上线。
>
> 规格与评审：[GitHub Issue #75](https://github.com/kongken/mono/issues/75)。本文是实现规格的仓库副本，评审修改同步到该 Issue。具体安全参数、流程细节及交付拆分是本规格提出的工程默认值，不代表用户逐项确认；第 1 节单列已确认要求。实现后同步各产品的当前行为文档，并在本文逐阶段登记状态。

## 实现状态登记

| 阶段 | 状态 | 说明 |
| --- | --- | --- |
| P1 usersrv 骨架与账号核心 | **已实现** | 见下文“P1 已实现内容”；单元与集成测试通过，不开放新登录 | 已实现 |
| P2 邮件模块 | **已实现** | 见下文“P2 已实现内容”；四 adapter 通过本地假 HTTP/SMTP 服务的契约测试，真实厂商只有[手动冒烟文档](mail-smoke-test.md)，尚未对真实服务验证；邮件还没有对外入口（P3 才会签发） | 已实现 |
| P3 账号流程 | 待实现 | `RequestRegistration`、`CompleteRegistration`、`AuthenticateWithEmail`、`RequestPasswordReset`、`ResetPassword` 的契约已定稿，服务端返回 `UNIMPLEMENTED` | 待实现 |
| P4 BFF 与产品鉴权 | 待实现 | BFF 尚未登记或调用 usersrv；`bff.v1` 仍是 Logto 契约 | 待实现 |
| P5 前端 | 待实现 | 两个前端应用仍使用 Logto 登录 |
| P6 发布与彻底退役 | 待实现 | 镜像、CI 发布矩阵、`deploy/usersrv/` 与 Logto 退役均未做 | 待实现 |

### P1 已实现内容

实现说明与命令见 [后端初始化](../backend.md#usersrv-服务账号核心p1)。对规格的补充（均不改变第 1 节与既定默认值）：

- 账号核心在 `internal/usersrv/account`：邮箱规范化与校验（`identity`，§4.1：只接受单个纯 ASCII 地址，IDN 须填 punycode）、昵称与头像校验、Argon2id（`passwordhash`，默认 64 MiB、3 次迭代、并行度 1、盐 16 字节、输出 32 字节，单进程最多 2 个并发计算，未知邮箱使用等成本虚拟哈希）、账号状态 `active/disabled/deleting`、`auth_version` 在同一事务中单调递增、安全审计同事务写入。
- 数据模型：`users`、`user_roles`、`account_tokens`、`security_audit`，另外两处补充：`deletion_tombstones`（§9.2 要求的注销 tombstone，24 小时，仅用户 UUID、工作流 ID 与时间）、`users.deletion_started_at`/`deletion_mutation_id`（记录拥有 `deleting` 状态的注销工作流）；`security_audit` 增加 `reason`（仅角色 CLI 使用，操作者不得写入邮箱）。`mail_jobs` 在 P2 创建。
- 内部契约 `usersrv.v1`（`proto/usersrv/v1/`）：`SystemService.GetStatus`；`AccountService` 已实现 `GetAuthState`、`GetMyAccount`、`VerifyPassword`、`ChangePassword`、`UpdateMyProfile`、`BeginDeletion`、`FinishDeletion`，所有方法都要求 BFF 签名凭证（`aud=usersrv`、绑定方法、60 秒）；本人方法只取凭证中的 `sub`，请求体没有用户 ID 字段。`GetAuthState` 与 `GetMyAccount` 拆开：前者每请求复核用，不含邮箱；后者含登录邮箱，仅供本人账号界面。
- 错误通过 `google.rpc.ErrorInfo`（domain `usersrv.v1`）区分原因（`INVALID_CREDENTIALS`、`ACCOUNT_NOT_ACTIVE`、`VERSION_CONFLICT`、`RATE_LIMITED`、`INVALID_ARGUMENT`、`DELETION_MISMATCH`），BFF 适配层据此把“错误密码”与内部签名错误分开（§9.1）。
- 限流（§10，Redis，原子计数，Redis 不可用时返回 `UNAVAILABLE` 且不放行）：密码验证（重新认证、修改密码）每用户 10 次/15 分钟、每 IP 40 次/15 分钟，成功也计数；资料写入每用户 30 次/分钟；注销每用户 10 次/15 分钟。Redis key 中的主体用独立 HMAC 密钥摘要。注册、重置、邮件合计限额的计数器在 P2 实现（见下），由 P3 的注册/找回流程调用。
- 注销：`BeginDeletion` 幂等切到 `deleting`、递增 `auth_version`、作废该用户及其邮箱的账号令牌；`FinishDeletion` 要求 `deleting` 且工作流 ID 一致，删除账号与角色（级联）、作废令牌并写 tombstone，24 小时内同用户同工作流重放成功；同邮箱可重新注册得到新 UUID。已禁用账号不能开始注销（需先启用）。
- 角色与状态：只有命令行 `usersrv-admin` 能授予/撤销 `metro-admin`、禁用/启用账号；授予只限已验证的 active 账号，每次尝试（含被拒）记录操作者、原因、结果；没有“首个用户自动管理员”，也没有 RPC 入口。授予/撤销/禁用/启用若改变了状态就递增 `auth_version`（已处于目标状态时只写审计，不递增）。
- 后台清理：每小时清除过期令牌（过期或消费超过 7 天）、过期 tombstone、超过 90 天的审计行；它是 `/readyz` 报告的 `worker` 本地状态。
- `auth.apps.<id>.origin`、令牌 TTL 与 HMAC 密钥已进入配置并在启动时校验，邮件链接在 P2 使用，令牌的签发与验证流程在 P3。
- 已测量的 Argon2id 成本见 [后端初始化](../backend.md#usersrv-服务账号核心p1) 的“资源预算”。

### P2 已实现内容

实现说明、配置与运维见 [后端初始化](../backend.md#usersrv-邮件模块p2)。对规格的补充（均不改变第 1 节与既定默认值）：

- `internal/usersrv/mailer`：统一的 `Send(ctx, message)`；固定的中文验证/重置模板（纯文本与 HTML，参数转义，收件人和标题拒绝 CR/LF）；四个 adapter（Mailgun 与 SendGrid、SMTP2GO 用原生 HTTP API，SMTP 支持 `implicit_tls`/`starttls` 且始终校验证书）。HTTP adapter 禁止重定向、响应体限 16 KiB 并脱敏、默认 10 秒超时；错误分为 temporary/permanent/uncertain。配置校验让未知 provider、缺参数、混入其他厂商参数、非法 region/TLS 模式都在启动时失败。
- 邮件跟踪：Mailgun 与 SendGrid 请求里显式关闭点击/打开跟踪（否则令牌会经厂商域名改写）；SMTP2GO API 没有逐封开关，需在其账户里关闭。SMTP 目前只实现 AUTH PLAIN/LOGIN，且只在 TLS 或回环地址上发送凭证。
- 加密 outbox 表 `mail_jobs`（迁移 `migrations/usersrv/20261024153533_mail_outbox.sql`）：收件人、模板参数与明文令牌整体 AES-256-GCM 加密（带 key ID，关联数据绑定任务 ID 与用途）；状态机 `pending/sending/accepted/failed/uncertain/expired/cancelled`；密文只在 `pending/sending` 存在，数据库 `CHECK` 保证终态必须清空。同进程 worker 用数据库 lease（发送超时＋30 秒）抢占，多副本只会发一次；发送前复核挑战（存在、未消费、未过期）与账号（active、`auth_version` 一致、邮箱未被注册）。最多 3 次尝试，退避 1、5 分钟，重试用同一令牌；`uncertain` 永不自动重发；任务记录保留 7 天，每小时清理。任务固定签发时的 provider 与配置代次，不一致则取消（`config_changed`）；队列里有任务但缺少其解密 key 时启动失败。`BeginDeletion`/`FinishDeletion` 取消该用户未发的账号邮件。
- 链接固定为 `auth.apps.<app_id>.origin` 的精确 origin 加 `/account/verify-email` 或 `/account/reset-password`，令牌放在 `#token=` 片段里；调用方不能指定 URL。
- 限流（Redis 原子，邮箱用独立 HMAC 摘要；Redis 不可用返回 `UNAVAILABLE`）：每客户端 IP、全服务、每邮箱 60 秒冷却、每邮箱每用途、每邮箱合计，默认值见 `config.usersrv.example.yaml`。超出邮箱维度只“抑制”（调用方仍给统一成功应答，不发信）；超出 IP/全服务维度返回 `RESOURCE_EXHAUSTED`。
- 日志与指标：没有指标导出器，只有 `Service.Snapshot()` 计数和结构化日志（任务 ID、provider、模板、状态、错误类、次数、耗时、`alert`），不含邮箱、令牌、链接或密钥；告警事件为 `mail_vendor_auth_failed`、`mail_uncertain`、`mail_decrypt_failed`、`mail_key_missing`、`mail_backlog` 等。`/readyz` 增加 `mail_outbox`（worker 本地状态）。
- 测试：四个 adapter 对本地 HTTP/SMTP 假服务做契约测试（Mailgun US/EU 各一例），outbox 与限流用真实 PostgreSQL/Redis。**没有、也不会在 CI 里连接真实厂商**；手动步骤见 [邮件厂商手动冒烟测试](mail-smoke-test.md)。

## 1. 已确认要求

1. 新增独立后端 `usersrv`，承载自有用户功能，供山海足迹、地铁足迹及后续产品共用。
2. **完全移除 Logto**，不保留为备用认证提供方；不再依赖其账号、资料、角色、OAuth 回调或管理接口。
3. **邮箱是唯一登录标识，使用邮箱＋密码认证。** 不支持用户名、手机号、第三方登录或邮箱验证码免密登录。昵称、好友邀请码及地铁公开主页 handle 都不是登录标识。
4. 注册需要验证邮箱，支持邮件重置密码。
5. 账号邮件支持 **Mailgun、SendGrid、SMTP、SMTP2GO** 四种可配置发送实现。Mailgun、SendGrid、SMTP2GO 用各自原生 HTTP API；SMTP 用标准协议。
6. 四种实现共用模板和发送 interface；按配置选用，不自动跨厂商故障切换。验证与重置令牌短期、单次有效，验证存储只保存摘要；接口限流，不通过找回密码响应披露账号是否存在；密钥只留服务端。
7. 不迁移或兼容旧账号、旧用户 ID、旧会话、旧角色、旧管理令牌或旧云端数据。**不因此授权自动清空数据库、对象存储或浏览器数据**；清理如有需要，另提供明确目标与范围的手动步骤。

## 2. 范围与非目标

### 首版交付

- 邮箱注册验证、邮箱密码登录、退出、设备列表与撤销。
- 读取和修改自己的基础资料、修改密码、忘记密码、真实密码重新认证。
- 管理员命令行授予/撤销角色、禁用/启用账号；不默认把第一个注册者设为管理员。
- 账号注销，幂等清理所有启用产品的数据，失败可以重试。
- 两个前端接入自建账号流程；BFF、内部凭证、metro 管理与 MCP 不再依赖 Logto。
- 四种邮件 adapter、可靠的待发任务、配置校验与运维验证。

### 首版不做

- 修改登录邮箱、多个登录邮箱、账号合并、外部身份绑定；这次没有保留 Logto 的身份映射模型。
- 社交登录、OAuth/OIDC 提供方、Passkey、MFA、无密码登录、独立用户中心前端应用。
- Web 用户管理后台、自定义角色编辑器；首版角色白名单只有现有的 `metro-admin`。
- 保留账号但一键删除全部云端数据的新独立流程：现有入口将明确改为“注销账号”，不能沿用旧文案悄悄扩大删除范围。
- 邮件营销、送达/打开追踪、退信 webhook、跨厂商自动切换、发送成功等同最终送达的承诺。
- 重写山海足迹 F12 通知订阅：F12 的通知邮箱仍独立于登录邮箱，原有 SMTP 配置与用户同意规则不变。本规格要求四种渠道的是 usersrv 账号事务邮件；以后有第二个发送使用方时再抽取共享模块。
- 改变地图、足迹、备份格式、匿名本地模式或公开目录功能。不做历史认证迁移不等于删除现有本地数据兼容代码。

## 3. 术语与架构

| 术语 | 本规格含义 |
| --- | --- |
| 账号 | 一位用户跨产品共用的登录资格，包含唯一登录邮箱、密码凭据与账号状态 |
| 用户 ID | usersrv 创建的不可变 UUID；产品数据及内部凭证的 `sub` 使用它，不使用邮箱作为外键 |
| 基础资料 | 跨产品的昵称、头像；不是地铁骑行者资料，也不是足迹或好友设置 |
| 邮箱验证 | 证明拥有注册邮箱，**不等于登录** |
| 重新认证 | 在已有会话下再次成功验证当前密码，不是刷新 Cookie 或调用 Me |
| 注销账号 | 关闭共用登录资格，清理所有启用产品的数据，最后删除账号；不同于退出登录 |
| 邮件已接受 | 发送厂商或 SMTP 服务器接受了邮件，不代表最终送达收件箱 |

```text
浏览器（trip-map / metro）
  └─ Connect + Cookie + CSRF → BFF（唯一公网入口）
       ├─ usersrv：账号、密码、资料、角色、邮箱令牌、账号邮件
       ├─ tripmap：足迹、好友、媒体及旅行通知等业务数据
       └─ metro：目录、乘坐记录及骑行者资料
MCP 客户端 ─ Bearer 管理令牌 → BFF → metro
                                  └→ usersrv 复核账号资格
```

### 职责与依赖

| 模块 | 拥有的状态与规则 |
| --- | --- |
| usersrv | 账号唯一性、密码哈希、账号状态、角色、认证版本、验证/重置令牌、账号事务邮件、账号安全审计 |
| BFF | Cookie 会话、CSRF、CORS、浏览器身份约束、管理令牌、注销工作流与完成回执；不保存密码、登录邮箱唯一索引或 Logto 身份映射 |
| tripmap / metro | 自有业务数据与展示投影；不直接读 usersrv 库，不校验密码，不用邮箱查找好友 |

- 新增 `cmd/usersrv/`、`internal/usersrv/{app,application,config,ent,mailer,...}/`、`proto/usersrv/v1/`、`migrations/usersrv/`、`config.usersrv.example.yaml`、`deploy/usersrv/`。
- 共用浏览器账号契约继续放 `proto/bff/v1/`，BFF 的适配仍在 `internal/bff/auth/` 等共用模块；usersrv 不是某个产品的 BFF 包。
- Go、Butterfly、Gin、ConnectRPC、Ent、PostgreSQL、Redis 沿用仓库技术栈。usersrv 用独立 PostgreSQL 库；首版不需要 S3。各服务有独立 schema 与数据库权限，跨库不建外键。
- 业务包不互相 import，BFF 也不 import `internal/usersrv`；通过生成的 gRPC 契约交互。更新 import 检查与镜像复制范围。
- usersrv 只在内网提供 `/ping`、`/readyz` 和原生 gRPC。复用 `grpcx`、`servicetoken`、`rpcauth`：BFF 签名、`aud=usersrv`、绑定方法、60 秒有效期；匿名账号方法也需要有效 BFF 内部凭证。签名不能替代传输保密，承载密码的生产链路必须 TLS，或显式批准且由其他层提供等效加密的隧道/服务网格；本地 loopback 可显式 h2c。

## 4. 账号与凭据规则

### 4.1 邮箱唯一性

- 所有输入入口共用一套规范化：去除两端 ASCII 空白，ASCII 邮箱整体转小写；这是本系统明确选择的大小写不敏感策略，不声称所有邮件系统的本地部分都不区分大小写。
- 首版只接受单个 ASCII 地址，最长 254 字节、本地部分最多 64 字节；域名须是有效 DNS 名称。拒绝显示名、多个地址、注释、引号式本地部分、控制字符、CR/LF 及国际化邮箱。域名可使用合法的 ASCII punycode。
- 不去掉 `+tag`，不合并 Gmail 的点号，不按厂商推测别名。规范邮箱为数据库唯一键，不能只靠“先查再建”。
- 昵称不唯一；界面不再有 username 字段。邮箱只向本人账号接口和必要发送过程提供，不进入公开资料、分享、好友列表、内部角色凭证或普通业务投影。
- 基础昵称默认“旅行者”，去除首尾空白后 1–40 个 Unicode 码点，无控制字符；头像首版仅接受空值或不带凭据的 HTTPS URL（最长 500 字节），服务端不抓取。地铁独有的 handle、简介、社交链接仍归 metro。

### 4.2 密码

- 12–128 个 Unicode 码点，UTF-8 最多 512 字节；不 trim、不截断、不做 Unicode 归一化，不强制大小写/数字/特殊符号组合。允许粘贴、自动填充及密码管理器。
- 使用带随机盐的 Argon2id，保存可自描述参数的编码哈希。建议默认内存 64 MiB、迭代 3、并行度 1、盐至少 16 字节、输出 32 字节；实施时压测确认资源预算，不降低参数而不说明。
- 单进程密码计算默认并发最多 2 个，有界等待并受请求 deadline 限制；拒绝超大输入后才做哈希。未知邮箱使用等成本虚拟哈希，避免明显快路径；不得宣称响应时间完全不可区分。
- 明文密码不落库、不进队列、日志、追踪、指标或响应。修改密码要求当前密码；密码重置只接受重置令牌，不接受客户端指定目标用户 ID。

### 4.3 状态

- 正式账号只有 `active`、`disabled`、`deleting`；只有验证邮箱后才创建正式账号，没有可登录的“待验证用户”。
- `disabled` 拒绝登录、重置及所有受保护访问；启用后仍需重新登录，旧凭证不恢复。
- `deleting` 禁止登录、重置、资料写入及普通业务访问；只走既有注销工作流，不允许取消注销。邮箱在最终删除前仍被占用。
- 最终注销后允许同邮箱重新注册，但生成全新用户 ID、无角色、无旧数据或旧管理令牌。

## 5. 用户流程

### 5.1 注册与验证

1. 用户选择注册，输入邮箱；前端调用 `RequestRegistration(email, app_id)`，不在此阶段保存密码。
2. 未注册邮箱生成 `registration` 令牌和待发邮件；已注册、禁用、注销中的邮箱不生成可注册令牌。语法正确时统一返回“若该邮箱可注册，验证邮件将发送，请检查收件箱”。
3. 邮件链接打开对应应用的验证页，用户设置密码及昵称，显式提交 `CompleteRegistration(token, password, display_name)`。
4. usersrv 在一个事务内校验用途、过期时间和未消费状态，按邮箱串行化完成操作，创建已验证 `active` 账号、消费该令牌并使同邮箱的其他注册令牌失效。唯一索引保证并发只能创建一个账号。
5. 成功页引导用邮箱＋密码登录；**验证成功不自动建立会话**。过期、无效、已消费或邮箱已被注册的链接统一显示不可用，可重新申请。

选择“先验证邮箱，再设置密码”是工程默认，用来避免攻击者先用他人的邮箱设置已知密码，再等邮箱主人点击验证链接完成账号预占。邮件扫描器 GET 或仅打开页面不能创建账号或消费令牌。

- 注册令牌默认 30 分钟有效；重发产生新邮件，但不立即作废尚未过期的旧链接，避免第三方反复申请使收件人无法完成；首次完成后全部作废。
- 请求注册不创建业务 profile；验证完成也不创建各产品业务记录。
- 可配置关闭新注册；此时申请和完成都统一拒绝开放注册，不影响既有用户登录与找回密码。

### 5.2 登录、会话与退出

- `LoginWithEmail(email, password)` 只接受邮箱；密码错误、邮箱不存在、账号禁用或正在注销，返回同一 `unauthenticated` 文案“邮箱或密码错误，或账号不可用”。格式错误可返回字段校验错误，不返回账号存在性。
- usersrv 返回用户 ID、基础资料、当前角色和 `auth_version`；BFF 生成全新随机会话令牌（库中仅存摘要），写入版本和真实认证时间。不可复用登录前的 session ID，响应体不返回令牌。
- 沿用 host-only `kongken_sid`、Path `/`、HttpOnly、SameSite=Lax，生产 Secure；固定有效期默认 7 天，不因调用 Me 自动延长。
- 成功校验密码后建立 BFF 会话；登录不依赖 tripmap 或 metro 就绪。tripmap 的基础资料投影改为首次受保护业务调用前按需确保，失败只影响该产品调用，不回滚账号或阻止另一个产品使用。投影只含用户 ID、昵称和头像，不含邮箱。
- Me 返回本人邮箱、资料、角色和会话信息；设备列表、撤销设备与退出仍由 BFF 管理。退出幂等，撤销当前会话，清除当前主机 Cookie；不删除账号或其他设备。
- 两应用通过同一 API 主机直连且携带 credentials 时共享 Cookie；从各自域名代理 `/api` 则会话隔离，不能据此宣称跨域单点登录。沿用精确 CORS 白名单、CSRF 双提交及 `X-Kongken-User` 防跨账号缓存写入。

### 5.3 忘记密码与重置

1. `RequestPasswordReset(email, app_id)` 对语法正确的地址统一返回相同成功文案；只有 `active` 账号创建 `password_reset` 令牌及邮件任务。收信失败不向申请者暴露账号是否存在。
2. 链接打开重置页，用户输入两次新密码，显式提交 `ResetPassword(token, new_password)`；GET 和页面预览不修改账号。
3. 默认 15 分钟有效；令牌绑定目标用户和签发时的 `auth_version`。事务内锁定账号，确认仍 active、版本一致，再更新密码、消费全部重置令牌并递增认证版本。
4. 全部已有浏览器会话和 MCP 管理令牌失效；重置成功不自动登录，用户用新密码重新登录。
5. 过期、已消费、用途错误、账号状态/版本改变等情况统一提示链接不可用；重置并发只能一份成功。发送重置邮件本身不能撤销会话或改变密码，防止请求造成拒绝服务。

### 5.4 资料、修改密码与重新认证

- 仅能读写本人的基础资料；不能由请求体选择 user ID、角色、邮箱或账号状态。昵称/头像修改不增加认证版本，不覆盖 metro 的骑行者资料。
- usersrv 是基础资料权威源。tripmap 只保存展示投影，每次在线调用时按 usersrv 的资料版本确保投影不落后；不触碰业务 revision、好友码或已有旅行。
- `ChangePassword(current_password, new_password)` 成功后递增认证版本，使所有浏览器会话和 MCP 令牌失效，当前设备也需重新登录。
- `Reauthenticate(password)` 成功后只更新当前 BFF 会话的 `authenticated_at`；BFF 必须比较 usersrv 返回版本和该会话版本，不能把过期身份“升级”为有效会话。
- 注销账号、创建管理令牌要求最近 15 分钟内真实密码认证；管理 RPC 沿用默认 12 小时的 `admin_session_max_age`，超时可重新认证，不把新 Cookie 签发时间当作重新认证。

### 5.5 注销账号与失败恢复

目标是删除**共用账号和所有启用产品的数据**，两个应用都明确使用“注销账号”，列出清理范围与不可恢复后果；不能只写“删除我的足迹”。

1. 当前有效会话，最近 15 分钟内密码认证，提交 `confirmation="DELETE"` 和幂等 `mutation_id`。前端仍要求中文二次确认，并提醒导出未同步修改。
2. BFF **先持久化**注销工作流（用户 ID、mutation ID、原会话 Cookie 摘要、产品清理器清单），再调用 usersrv `BeginDeletion`。后者幂等切换 `deleting`、递增认证版本、作废账号令牌并取消未发账号邮件。
3. 所有普通访问和 MCP 新请求失效。原 Cookie 只可凭注销工作流访问注销状态/重试，仍校验 CSRF/Origin；不能靠它访问 Me、资料或业务，也不能仅凭工作流 ID 操作。
4. BFF 调用清单中每个产品的幂等 `CleanupUserData`，包含 S3 清理；失败记录进度并返回可重试状态，不能因为产品临时不可用或配置后来移除就悄悄跳过。usersrv 不 import 或直接调用业务代码。
5. 全部产品成功后调用 usersrv `FinishDeletion`，删除账号、凭据、角色、令牌及基础资料，再清理 BFF 的会话/管理令牌并写完成回执。原 Cookie 摘要可在 24 小时内重放完成结果，然后清 Cookie。跨库步骤无分布式事务，逐步幂等。
6. 每一步响应丢失或进程重启都可从已持久化工作流恢复；后台继续清理，Cookie 过期后也不会遗弃任务。运维可按任务 ID 显式重试，不能由匿名调用者接管。

不删除浏览器匿名本地足迹或其他账号缓存；成功后前端只按既有规则清理本账号缓存。保留最小安全/目录审计（用户 UUID、动作、时间、结果），不保留邮箱、密码或令牌；已公开 CDN 内容的失效仍受原有缓存限制约束。

## 6. 认证版本与角色一致性

**不把跨库“通知 BFF 删会话”当作安全保证。** usersrv 维护单调递增的 `auth_version`；改密、重置、禁用/启用、授予/撤销角色、开始注销都在更新账号的同一事务递增。

- BFF 会话及管理令牌保存签发时版本。每个受保护浏览器请求、每次 MCP 工具调用，在下发业务凭证前调用 usersrv `GetAuthState`，检查账号 active、版本一致和当前角色。首版不做正向资格缓存；usersrv/存储不可用时返回 `unavailable`，不放行、不降级用旧角色快照。
- 同一个请求可以复用一次复核结果，MCP 批量调用逐工具复核；未登录的匿名 RPC 不附带用户身份，不因 usersrv 不可用而自动变为受保护。
- 版本变更后，**下一次复核**拒绝旧凭证；不能承诺撤回已通过复核的在途请求或已签发内部凭证，后者仍受现有 60 秒有效期与时钟偏差约束。并发登录/改密时即使 BFF 晚写入旧版本会话，也不能使用。
- MCP 令牌仍有固定 audience、scopes、到期时间、吊销状态与角色快照，原明文只显示一次规则不变；权限取快照与当前角色交集，且版本必须匹配。角色撤销后不会继续可用到 90 天，重新授予也不恢复旧令牌；需重新登录、重新创建。
- `metro-admin` 保持原角色名。usersrv 的管理 CLI 仅对已验证 active 账号授予，记录操作者标识/原因/结果；没有“首个用户自动管理员”、配置邮箱自动提权或浏览器自行设置 roles 的入口。
- BFF 与业务仍独立执行 `required_role`；只在声明需要角色的方法的内部凭证里传递必要角色，不添加邮箱或密码。角色管理不给管理员读取他人私有足迹的能力。

## 7. 邮件模块

### 7.1 Interface 与四种 adapter

账号业务只提供收件人、固定模板类型、受控参数、任务 ID 给邮件模块；渲染统一中文主题、纯文本及 HTML 正文。发送 seam 只需一个 `Send(ctx, message)`，返回接受结果或分类错误；厂商响应、认证和协议差异封装在各 adapter 内。

| `mail.provider` | 实现与必填配置 | 规则 |
| --- | --- | --- |
| `mailgun` | 原生 HTTP API，`api_key`、`domain`、`region` | region 仅 `us` / `eu`，选用对应官方端点，不默认向另一地域重试 |
| `sendgrid` | 原生 HTTP API，`api_key` | 固定官方端点，使用具有发送权限的最小权限 key |
| `smtp` | SMTP，`host`、`port`、`tls_mode`，可选成对配置的认证用户名/密码 | `tls_mode` 为 `implicit_tls` 或 `starttls`，生产无明文及跳过证书验证模式；STARTTLS 缺失或失败即终止 |
| `smtp2go` | 原生 HTTP API，`api_key` | 独立 adapter，不仅仅视作“可配置一个 SMTP 主机” |

共用配置有 `from_email`、`from_name`、可选 `reply_to`、发送超时默认 10 秒；全部地址、主题和自定义头拒绝 CR/LF，HTML 参数转义，调用者不可注入任意 HTML 或邮件头。From 来自服务端固定配置，不能由请求覆盖。

- 只配置一个 active provider；缺少其必填参数、配置未知 provider、同时留下其他 provider 的非空参数、无效发件地址/TLS 设置时启动失败，不静默选择 SMTP。
- HTTP 使用受信 CA、固定官方主机、禁止重定向和向未知端点转发密钥；仅测试可注入受控 transport。响应体限量读取并脱敏，不把厂商原始错误发给浏览器。
- SMTP 校验证书与主机名；仅显式本地开发模式允许 loopback 无 TLS 测试服务器，不允许生产回退。不实现 POP/IMAP。
- 全部凭据留在忽略配置或挂载 Secret，不写前端、公开配置、Issue、PR 或日志。发送健康检查不能真的发信。

### 7.2 令牌、链接与待发任务

- 验证/重置令牌至少 32 字节密码学随机数，URL-safe 编码。验证表只保存 SHA-256 摘要、用途、邮箱/用户绑定、签发版本、过期及消费状态，不保存明文；密码令牌不能互换用途。
- 邮件链接只能由服务端 `auth.apps.<app_id>` 中的精确 HTTPS origin 和固定路径生成，首版为 `/account/verify-email`、`/account/reset-password`；本地开发仅允许显式 loopback HTTP。请求不能提供 origin、任意 callback 或返回地址；未知 app_id 拒绝，账号状态不改变。
- 令牌放链接 fragment（`#token=...`），不放 query/path。验证页在加载其他资源前读取进内存并用 `history.replaceState` 清除 fragment；不保存到 localStorage/sessionStorage，不发给埋点，不加载第三方脚本；页面设置 no-referrer、no-store。清除后刷新需重新打开邮件，不能因此持久化秘密。GET 不消费令牌，提交时才随敏感 RPC body 发送。
- usersrv 在同一数据库事务创建令牌及 `mail_jobs` outbox，接口仅等待可靠入队，不等待外部发信。worker 在同一进程后台运行，用数据库 lease 防多副本并发发送；网络调用不持有数据库事务。
- **持久化重试与“只存摘要”的区别必须明确**：outbox 发送前需要能恢复原令牌，所以收件人、模板参数及原令牌整体用 AES-256-GCM 加密后暂存；令牌验证表仍只存摘要。加密 key 独立放 Secret（带 key ID、支持轮换），关联数据绑定任务 ID/用途；不得把 key 与密文放同一数据库。已接受、永久失败、过期或取消后立即清除密文。没有发送任务明文落库的替代路径，也不在重试时换发新的令牌。
- outbox 只含模板数据，不存密码；元数据不含明文收件人/令牌。最小任务状态记录保留 7 天，过期挑战记录每日清理；worker 开始发送前复核挑战与账号，跳过已消费、过期、账号被禁用/删除或版本失配的邮件。已开始的外部发送无法撤回，但邮件中的链接在状态变更后不可用。
- 任务固定签发时的 provider 与配置代次，不因配置切换自动改发其他厂商。切换前排空/取消旧任务，用户显式重新申请；轮换加密 key 要保留尚有任务使用的解密 key，缺失即拒绝启动 worker 并告警，不能悄悄丢弃任务。

### 7.3 失败、重试与观测

- 状态：`pending`、`sending`、`accepted`、`failed`、`uncertain`、`expired`、`cancelled`。`accepted` 只表示供应商接受。
- 明确未接受的临时失败（如连接前失败、协议明确临时拒绝）最多尝试 3 次，退避 1 分钟、5 分钟；到达令牌期限则停止。永久拒绝、认证失败、无效收件人不重试。
- 发送后连接断开、超时、worker 在提交结果前崩溃等无法确认接受结果的情况标记 `uncertain`，**不自动重发**。lease 到期不能直接转回 pending。HTTP 5xx 不能一律假定未接受，应按厂商协议分类。
- 本系统不承诺 exactly-once 或最终送达。用户显式“重新发送”是新申请，受同样限流；相同发送任务不会自动跨厂商补发。传输层不能藏有不受业务控制的自动请求重试。
- 结构化日志/指标仅包含请求 ID、任务 ID、provider、模板类型、状态、错误分类、尝试次数和耗时；不含邮箱、链接、密码、API key、原始正文或未清洗厂商响应。账号日志可使用内部用户 UUID。
- 告警包括待发任务积压、永久失败、uncertain、解密失败及厂商认证失败；健康接口不披露厂商错误和 Secret。

### 7.4 厂商文档参考

实现时按官方当前契约核对请求、认证与错误分类，不把下面的链接等同于已完成实测：

- Mailgun：<https://documentation.mailgun.com/docs/mailgun/api-reference/send/mailgun/messages>
- SendGrid：<https://www.twilio.com/docs/sendgrid/api-reference/mail-send/mail-send>
- SMTP2GO：<https://developers.smtp2go.com/reference/send-an-email>
- SMTP STARTTLS：<https://www.rfc-editor.org/rfc/rfc3207>；隐式 TLS：<https://www.rfc-editor.org/rfc/rfc8314>。

## 8. 数据模型（拟新增，不是当前 schema）

| 所有者/表 | 关键字段和约束 |
| --- | --- |
| usersrv `users` | UUID、`email_canonical` 唯一、`email_verified_at` 非空、`password_hash`、昵称/头像、`profile_version`、`status`、`auth_version`、创建/更新时间、注销开始时间 |
| usersrv `user_roles` | `user_id + role` 唯一，角色只允许白名单；与账号同库外键级联删除 |
| usersrv `account_tokens` | UUID、`token_hash` 唯一、`purpose`、规范邮箱、可空目标 user ID、签发认证版本、app ID、过期/消费时间；注册令牌不需要预先存在 user ID |
| usersrv `mail_jobs` | 任务 ID、挑战 ID、provider/配置代次、模板类型、密文/key ID、状态、尝试次数、下一次尝试/lease 时间、分类结果；终态清密文，状态+计划时间索引 |
| usersrv `security_audit` | 用户 UUID（如有）、操作者标识、动作、请求 ID、结果、时间；不记录完整邮箱、密码及令牌；默认保留 90 天后清理 |
| BFF 浏览器会话 | 用户 UUID、Cookie 摘要、`auth_version`、真实认证时间、创建/最后使用/过期/撤销时间；不跨库 FK usersrv |
| BFF 管理令牌 | 保留摘要、audience、scopes、角色快照、到期/吊销状态，增加 `auth_version`；不再 FK Logto 身份映射 |
| BFF 注销工作流/回执 | 幂等 mutation ID、用户 UUID、原 Cookie 摘要、产品清理器快照、阶段、结果、回执过期时间；不依赖仍然存在的用户 FK |

- 认证版本与状态变更必须在同一 usersrv 事务；重置/注册令牌消费及其对应业务更新必须原子提交，竞争失败不能部分创建用户或覆盖密码。
- 安全审计与角色/状态/凭据变更同事务落库；不存在账号的请求只记录脱敏安全事件。审计不是恢复已注销身份的账号映射。
- Ent 和契约通过生成器更新；新增 usersrv 迁移与生成命令，扩展 `cmd/migrate-gen` 的目标注册，禁止手工改生成文件。
- BFF 移除 `auth_identities` 与 OAuth state 的运行时依赖；退役旧表用单独手动 SQL，不把破坏性 DROP/全库清空混进正常启动。

## 9. 接口草案

命名与拆分供实现时定稿；下面的方法与不变量是交付要求。浏览器统一 Connect，内部统一 gRPC。所有匿名写操作也必须执行 Origin/CSRF 校验，不能因标注 anonymous 而放行。

### 9.1 浏览器 `bff.v1`

| 服务/方法 | 权限 | 输入/结果要点 |
| --- | --- | --- |
| `AuthService.IssueCSRFToken` | 匿名，只读豁免 | 沿用响应体 token 与 CSRF Cookie |
| `AuthService.RequestRegistration` | 匿名写 | email、app_id；统一受理提示，不返回 user 或验证令牌 |
| `AuthService.CompleteRegistration` | 匿名写 | token、password、display_name；完成不签发会话 |
| `AuthService.LoginWithEmail` | 匿名写 | email、password；仅 Cookie 承载新会话，响应返回本人资料/过期时间 |
| `AuthService.RequestPasswordReset` | 匿名写 | email、app_id；统一受理提示 |
| `AuthService.ResetPassword` | 匿名写 | token、new_password；成功不登录 |
| `AuthService.Me` / `ListSessions` | 当前会话，只读 | 当前账号、角色；本人的设备列表 |
| `AuthService.Logout` / `RevokeSession` | 当前会话，写 | 退出清 Cookie；撤销不能指定其他用户的会话 |
| `AuthService.Reauthenticate` | 当前会话，写 | 当前密码；只刷新真实认证时间 |
| `AuthService.ChangePassword` | 当前会话，写 | 当前密码、新密码；全部凭证失效 |
| `UserService.UpdateMyProfile` | 当前会话，写 | 仅基础昵称/头像，返回本人资料 |
| `AuthService.DeleteAccount` | 当前会话＋近期密码认证，写 | DELETE、mutation_id；新含义为注销共用账号 |
| `AuthService.GetAccountDeletion` / `RetryAccountDeletion` | 仅注销工作流凭据 | 前者只读，后者校验 CSRF；只能访问该 Cookie 对应任务 |

- 旧 `Register` / `Login` 的 username 字段不能改号复用成 email；新方法明确命名，删除旧方法和 OAuth RPC，退役字段号/名称按 protobuf 规则 reserved，不为旧客户端提供回退链。
- Me 的邮箱字段只用于本人账号界面；不顺手复制到全部产品 User 响应。认证 RPC 请求体不记录；默认限制请求体 16 KiB，参数超限在哈希前拒绝。
- usersrv 认证失败按明确类型映射为浏览器 `unauthenticated`，不能继续使用“所有上游 unauthenticated 都是内部服务故障”的通用业务映射；错误凭证与真实内部签名错误必须在 adapter 区分，后者仍为 internal。
- `invalid_argument` 只用于格式和密码策略；`resource_exhausted` 用于可公开的 IP/全局限流；账号维度邮件抑制仍返回统一成功。有效登录但缺角色为 `permission_denied`，存储/资格复核不可用为 `unavailable`，错误不含厂商或数据库详情。

### 9.2 内部 `usersrv.v1`

- `AccountService`：注册申请/完成、密码认证、重置申请/完成、校验当前密码、修改密码、更新本人资料、读取认证状态与资料、开始/完成注销。注册/密码认证等无会话方法标注 anonymous 但仍验证 BFF 内部签名；它们不接受浏览器伪造的 sub。
- 本人方法从签名的 `sub` 取得目标用户；认证版本/密码重新校验结果与 BFF 会话版本比较。`GetAuthState` 仅在已有用户凭证上下文读取对应用户，不接受任意请求体 user ID。
- `BeginDeletion` / `FinishDeletion` 绑定原用户 sub 和工作流 mutation ID；幂等重复完成依赖注销 tombstone/完成回执（24 小时），不能通过“用户已不存在”就允许任意状态推进。
- usersrv 的 gRPC 拦截器不能为 `GetAuthState` 再反向依赖自身资格 RPC，避免循环；它验证 BFF 签名，资格状态由目标方法查询。产品 RPC 的鉴权继续使用平台拦截器。
- `SystemService.GetStatus`：内部匿名状态，仅有依赖与 worker 就绪信息。CLI 的角色/禁用操作不公开为匿名 RPC，也不给普通用户配置一个任意 user ID 的管理方法。

## 10. 滥用防护与链接安全

以下是可配置的默认上限，计数器放 Redis，并发扣减必须原子；Redis 不可用时相关账号请求返回统一 `unavailable`，不能绕过限流发送或验证密码。

| 动作 | 默认限制 |
| --- | --- |
| 登录/重新认证/修改密码中的密码验证 | 每规范邮箱或用户 10 次/15 分钟；每 IP 40 次/15 分钟，成功也计数 |
| 注册邮件申请 | 每 IP 20 次/小时；每邮箱冷却 60 秒、5 次/小时 |
| 重置邮件申请 | 每 IP 20 次/小时；每邮箱冷却 60 秒、5 次/小时 |
| 所有账号邮件合计 | 每邮箱 8 次/小时；服务全局 200 次/小时，按供应商实际额度调整 |
| 注册完成/重置令牌提交 | 每 IP 30 次/15 分钟；每 token 摘要 5 次/15 分钟，错误和成功都计数 |
| 资料写入 | 每用户 30 次/分钟 |
| 注销首次请求/显式重试 | 每用户/工作流 10 次/15 分钟，自动后台清理另受并发限制 |

- 邮箱、用户/token 维度限制对不存在目标同样计数；邮件抑制不得返回区别账号存在性的剩余额度或错误。重置邮件申请不能锁定账号登录。
- Redis key 中邮箱用带独立服务端密钥的 HMAC，不写明文邮箱或简单可枚举哈希；IP 取 BFF 已验证的客户端地址，不信任客户端自填头。
- 服务端不得向客户端返回验证/重置令牌，即使开发模式；本地通过假邮件服务器收信测试，不开放“调试取验证码”公网方法。
- 所有代理、APM、RPC 拦截器都排除账号敏感请求体；前端对密码字段使用正确 autocomplete，不将凭据写浏览器存储。
- 生产 Cookie、CORS、链接白名单、加密材料缺失均在启动时失败，禁止自动切到本地开发模式；邮件厂商临时故障不使匿名地图/公开目录不可用。

## 11. 两个应用与既有功能的调整

- `apps/trip-map` 和 `apps/metro` 都提供邮箱登录、申请注册、验证后设置密码、忘记密码、重置、基础资料、改密及注销入口；复用 `packages/ui`，两应用共同的账号表单/客户端逻辑在第二个使用方出现时抽到 `packages/`，应用之间不互相 import。
- 邮件落地页固定 `/account/verify-email`、`/account/reset-password`，由各应用路由和静态站 fallback 接住，不再走 `/auth/` OAuth 代理。注册/重置成功回到该应用登录入口，不自动跳转客户端提供的外站。
- trip-map 继续保留本地模式；没有 `VITE_API_BASE_URL` 时不出现登录入口，也不发账号请求。账号过期/失效仍先保留未同步缓存并允许导出，不能为了更换认证删除本地足迹。
- metro 的 `handle`、骑行者资料公开规则不变；公开昵称与 usersrv 基础昵称相互独立，登录不覆盖用户已编辑的产品资料。
- tripmap 的好友申请首版只接受唯一 `fc_` 邀请码，移除“按用户名查找”；不能改成邮箱找人以泄露登录标识。不改变已有好友/共同旅行权限规则。
- 两应用共享一个 BFF 主机时，退出/改密/角色撤销在下次请求被另一应用观察到；沿用 `X-Kongken-User`，不能把账号 A 的待同步内容写进 B。
- 提供桌面与手机布局、键盘操作、字段错误、发送中/已申请/链接失效/限流/网络失败状态；公共响应不显示“这个邮箱已注册”等枚举提示。

## 12. 配置、部署与 Logto 退役

### usersrv 配置草案

- `db_store`、`redis_store`、`key_prefix`、`auto_migrate`、内部 gRPC 与 `internal_auth` 沿用现有风格。
- `auth.registration_enabled`、`auth.apps.<id>.origin`、令牌 TTL、密码哈希资源预算、各限流参数；应用 ID 至少支持 `trip-map`、`metro`，两者配置必须与 BFF 的允许来源保持一致。
- `mail.provider`、共用发件人/超时、对应 adapter 参数；`mail.encryption.active_key_id`、解密 key 集合；HMAC key 独立于邮件加密和 BFF 签名 key。
- 默认生产必须有可用的配置材料才能启动；启动只做本地参数与数据库/Redis 检查，不因外部邮件 API 一次抖动阻断全部登录；没有可用发信配置不能启动成“看似能注册”的半成品。
- 本地拟使用 usersrv HTTP `127.0.0.1:18086`、gRPC `127.0.0.1:18087`，避开现有 18081–18085；独立开发邮件接收器，所有测试使用独立 schema / Redis 前缀。

### BFF 与部署

- usersrv 为必要上游；在 `gateway/services.go`、`upstreams.usersrv`、`grpc.services.usersrv` 登记，BFF `/readyz` 增加 `usersrv_grpc`。usersrv `/readyz` 检查 PostgreSQL、Redis 和 worker 本地状态，不向邮件厂商发请求或发信。
- BFF 继续拥有 session TTL、Cookie、CORS、CSRF、管理会话时效、产品清理器配置；不再保存 OAuth app/state/provider 配置。账号链接 origin 白名单归 usersrv，浏览器 CORS 白名单归 BFF，启动/部署测试检查二者协调。
- 补充 usersrv 镜像、Compose 开发/部署示例、CI 发布矩阵、构建、Ent 与迁移命令、Makefile 启动、独立数据库/Secret、TLS 和签名公钥、配置文件/Consul key。新增命令只在实现后写入运行 README，不能把拟定命令当成当前可用。
- 移除运行时 `internal/bff/oauth`、Logto provider、`/auth/login` 与 `/auth/callback/*`、OAuth state Cookie/Redis、`VITE_OAUTH_APP_ID`、Logto-only 配置及废弃 RPC。旧登录 URL 返回 404，不转向仍能登录的备用链。
- 更新 metro 角色说明、MCP 角色时效、账号注销文案，以及所有真实栈 fake Logto 测试为真实 usersrv＋假邮件发送器。
- 不在本任务自动删除外部 Logto 租户、基础设施或生产 Secret。仓库移除使用依赖，外部资源退役由运维单独执行。
- 用新空库/新部署验证闭环，停止旧后端再开放新登录，不能让 Logto-only 与 usersrv 两套认证同时写同一业务环境。旧 Cookie 不导入；退役表/测试数据/S3 清理按库和 bucket 显式审核、手动执行。失败回退到旧部署也不能让新旧用户数据混用。

## 13. 分阶段交付

状态见上方“实现状态登记”。这是一份父规格，评审后再拆原生子 Issue，不在本轮把尚未评审的参数标为 ready-for-agent。

| 阶段 | 范围 | 依赖/完成门槛 | 状态 |
| --- | --- | --- | --- |
| P1 usersrv 骨架与账号核心 | 独立库、配置、健康、契约、邮箱规范化、Argon2id、状态/版本、角色 CLI、import 检查 | 本规格评审；可单测/集成测试，不开放新登录 | 已实现 |
| P2 邮件模块 | 四 adapter、统一模板、加密 outbox、lease、失败分类、限流、固定链接 | P1 存储；四种假服务契约测试通过 | 已实现 |
| P3 账号流程 | 注册验证、邮箱密码认证、找回/改密、资料、重新认证、令牌并发 | P1/P2；真实 usersrv＋假邮件集成闭环 | 待实现 |
| P4 BFF 与产品鉴权 | 新浏览器契约、会话版本复核、MCP 版本/角色复核、tripmap 投影/好友入口、注销工作流 | P3；真实 BFF→usersrv→两产品 E2E 通过 | 待实现 |
| P5 前端 | 两应用表单、邮件落地页、设备/资料/改密/注销、错误/离线保护、共用包 | P4 契约；桌面/手机 Playwright 和真实账号链 | 待实现 |
| P6 发布与彻底退役 | 镜像、配置、CI、Secret/手动清理文档、删除全部运行时 Logto 入口及测试依赖 | P1–P5；禁旧认证、文档同步、隔离环境演练 | 待实现 |

P1–P5 可分支递进验证，不能以临时保留 Logto 为最终兼容方案；整条账号链完成前不得在正式环境开启半套注册/登录。

## 14. 验收场景

### 账号核心与真实内部链路

1. ` Alice+tag@Example.com ` 与 `alice+tag@example.com` 归同邮箱；`alice@example.com`、`a.lice@example.com`、`alice+tag@example.com` 不被错误合并。拒绝多地址、CRLF、国际化和超长输入。
2. 请求注册不建立正式用户/会话，不在队列存密码；GET 验证页不消费令牌。确认邮箱后设置密码才建 active 账号；重复/并发完成只创建一个 UUID。
3. 已注册、未注册、禁用/注销中地址的注册/重置申请，在相同系统健康条件下同状态、同文案，不返回存在性或收件信息。
4. 注册链接过期、用途错误、已消费或邮箱被另一并发流程占用统一失败；失败不覆盖既有密码/角色。攻击者事先请求他人邮箱不能替对方预设密码。
5. 邮箱密码登录，只有正确 active 账号签发新 BFF Cookie；未知邮箱/错密码/禁用账号表现一致。响应、浏览器存储及日志均无明文会话令牌或密码。
6. 重置成功只改本人密码、认证版本递增、所有旧浏览器会话/MCP 令牌在下一次复核失败；旧密码失败，新密码成功，不自动登录。并发重置一份成功，另一份不覆盖。
7. 改密必须当前密码正确；刷新 Me、延长 Cookie、创建新 Cookie 不能替代真实重新认证。超过敏感操作时效后必须再次验证密码。
8. usersrv 不可用时保护接口和 MCP 拒绝执行，匿名地图/目录不被误改为受保护；tripmap 暂停不阻塞登录和 metro 使用。
9. 角色只可由受控 CLI 操作，普通注册无管理员。授予/撤销、禁用/启用都使旧版本失效；MCP 令牌不会因以后重新授予角色恢复。
10. BFF 与 metro 均检查 required_role；伪造 sub、角色、用户 ID 或请求头无法提权。甲用户不能读写乙用户资料、会话、密码或业务数据。
11. 并发登录与改密产生的旧版本会话不能使用；已签发内部凭证的短期在途窗口有文档和测试，不宣称即时中断。
12. 基础资料变更正确更新 tripmap 展示投影，不泄露邮箱、不推进足迹 revision、不覆盖 metro 骑行者资料；好友只能通过邀请码添加。

### 邮件四 adapter 与安全

13. 四 adapter 分别通过本地受控 HTTP/SMTP 服务验证认证方式、From/Reply-To、单收件人、中文标题、HTML/纯文本、超时/取消及错误分类；Mailgun US/EU 主机各有用例，SMTP2GO 是原生 API 用例。
14. 缺参、未知 provider、混合厂商参数、无效 Mailgun region、SMTP 未提供 STARTTLS、证书错误、重定向到其他主机均拒绝，不泄露 key、不回退明文或另一厂商。
15. outbox 与挑战原子提交；令牌表无明文 token，outbox 原文无收件人/token/邮件正文，日志和追踪也不含这些内容（账号表仍保存登录所需邮箱）。密文只可用独立 key 解密，key 轮换和缺失有明确行为；发送终态及过期清掉密文。
16. 多 worker 同任务不会同时发送；明确未接受的临时错误按上限退避，永久错误不重试，结果未知/发送后崩溃变 uncertain 不自动补发；配置切换不改投其他厂商。
17. 邮件链接 origin/path 来自 app 白名单，无开放重定向；HTTP 请求、Referer、代理日志没有令牌，fragment 在页面读取后清除，提交才消费；页面无第三方脚本，刷新需重开邮件。
18. IP/邮箱/token/global 限流及并发扣减生效，Redis 故障不放行；账号邮件限流不影响该用户正常登录；发送前发现已消费/过期/禁用/版本失配的任务不发信，在途邮件即使送达也不能使用已失效的链接。

### 注销、多应用和前端

19. 两应用明确提示注销**共用账号与全部启用产品数据**；确认不足或近期密码认证不足时不开始。原有“删除云端数据不注销 Logto”的文案/行为不残留。
20. 每个注销阶段注入响应丢失/重启/存储失败可恢复；S3 或任一产品失败时账号保持 deleting、邮件/MCP/普通访问关闭，重试直到成功，不出现能登录的半删除用户。
21. 注销完成后仅原工作流凭据可重放成功，工作流 ID 不能接管；同邮箱重新注册得到新 UUID，无旧数据/角色。其他用户、匿名本地足迹和非本账号缓存不变。
22. 真正跨 origin 的两个同站点应用直连同一 BFF：登录后共享 Me，退出/改密后另一应用下次调用观察失效；独立 `/api` 代理则验证会话不共享。Cookie、CORS、CSRF 和账号切换保护都执行。
23. 桌面和手机验证全部表单、错误、键盘操作、密码管理器、重发/冷却、链接过期和网络失败；未同步缓存遇到失效不自动删除，能导出。
24. 无任何 Logto 服务/配置/凭据即可完成注册→收信→验证→登录→业务写入→重置→重新登录→注销；旧 `/auth/login`、callback 和 OAuth RPC 不可用于认证。

### 验证与交付要求

- Go 修改运行 `make test`、`make build`、`make check-import-boundaries`；存储/模型变更另在独立开发环境跑 `make test-integration`，补上 usersrv 集成目标。
- 契约修改运行 `make buf`、`buf lint`，提交 Go/TypeScript 生成物；Ent 通过新增生成目标和迁移生成器更新。
- 应用代码修改运行 `npm run build`；标记/配色改动另 `npm test`。本任务不改变地图数据或投影，无需以身份改造名义重绘地图。
- `internal/e2e` 使用真实 BFF、usersrv、PostgreSQL/Redis 和产品服务；只替换邮件远端，不 mock 账号核心。Playwright 同时覆盖 fake BFF UI 与可选的真实收信链路，两者不能混为验收。
- 四厂商真实发送 smoke test 需要运维提供凭据、已验证发信域和受控收件地址，手动显式执行，不在普通 CI 发真实邮件；未做实测时明确标注，不能仅凭 fake adapter 测试声称生产已验证。发信域 SPF/DKIM/DMARC 由运维配置。

## 15. 文档同步与评审状态

本次仅落规格与文档入口；没有新增运行接口、可执行配置或数据库迁移，不需为文档变更构建应用。

实施时按阶段同步：

- `docs/backend.md`：usersrv 目录、模型、配置、迁移、角色 CLI、启动和验证；删除 Logto-only 当前描述。
- `docs/bff.md`：邮箱账号契约、资格复核、Cookie/CSRF、MCP 时效、注销回执、路由登记；删除 OAuth 登录/回调/配置。
- `docs/trip-map/features.md`、`docs/metro/features.md`：全部用户可见账号行为与验收，注销范围、好友入口、角色时效、离线限制。
- `docs/metro/spec.md`：标记旧 Logto 角色与账号方案被本规格替代，保留目录/业务规则。
- `docs/trip-map/data-and-maps.md`：若实现影响账号缓存字段/版本或备份处理，同次说明；不把旧云端不迁移扩大为浏览器本地清空。
- `docs/deployment.md`、`docs/docker-images.md`、`docs/monorepo.md`、根 `README.md`：新服务、独立库、Secret、邮件配置、部署/命令、外部 Logto 资源手动退役与实测限制。
- `GLOSSARY.md` 与 `docs/README.md`：统一账号、注销等概念与入口，已实现/待实现明确区分。

**待评审工程默认：** 邮箱格式范围、先验证再设置密码、不自动登录、密码参数、会话期限、认证版本复核频率、角色 CLI、注销代替原数据删除入口、邮件 outbox 与密钥管理、F12 不在本轮改造、各限流值。用户已确认的第 1 节不作为待决方向重新开启；若调整默认值，先改本规格与验收，再拆分实施任务。

