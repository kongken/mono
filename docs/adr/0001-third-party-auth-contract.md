# 第三方登录契约、数据模型与迁移回滚方案

**状态：产品决策已于 2026-10-29 在 KON-37 确认，工程契约修订待复审；功能待实现。Stage 2 在本修订评审通过后开始。**

依据：[Issue #103](https://github.com/kongken/mono/issues/103) 与 KON-37 的评审讨论。2026-10-28 的四项工程修订和 2026-10-29 的七项产品建议均获用户“没问题”确认。本文记录确认结果，不再把它们列为未决项。原规格 A01–A21 编号保留；本次对未验证 provider 邮箱的失败路径作收紧，见 §1.3 和 A02。

本 ADR 冻结 Issue #103 的第三方登录扩展边界。仓库当前实现是 usersrv 的邮箱＋密码系统：`users` 保存已验证账号，`account_tokens` 只承载注册和密码重置挑战，BFF 保存浏览器会话，`auth_version` 在认证相关变更时递增；运行时没有 OAuth/Logto。本文把 Google OIDC 与 GitHub OAuth 接入定义为默认关闭的增量能力，不把现有邮箱＋密码契约改成兼容层，也不允许外部 provider 直接成为产品服务的账号来源。

## 决策摘要

1. usersrv 继续是唯一账号来源。外部身份是挂在 usersrv 账号上的登录凭据，不能替代 `users.id`，也不能作为业务数据外键。
2. 外部身份的唯一键是 `(provider, issuer, subject)`。Google 的 `sub` 与 GitHub 的 provider 用户 ID 都按不透明字符串保存；邮箱、昵称和头像是资料输入，不是身份键。
3. `password_hash` 改为可空。密码为空表示无密码账号；无密码账号必须至少有一个仍有效的外部身份。首次设置密码在同一事务中写入哈希并递增 `auth_version`，之后账号可以同时使用密码和外部身份。
4. 所有一次性挑战和写操作回执都有明确用途、过期时间、使用者绑定和幂等键。回执只回答原请求的结果，不凭回执恢复已删除的账号或身份。
5. OAuth/OIDC 的浏览器 `state`、PKCE verifier、nonce 和返回地址由 BFF 持有；provider secret、code exchange 和 claims 校验由 usersrv 的 provider-neutral adapter 持有。产品前端不直接调用 provider。
6. Google OIDC 与 GitHub OAuth 的配置均默认关闭；没有配置、未通过启动校验或未显式启用时，BFF 不生成授权 URL。真实厂商冒烟只在发布门槛执行，不进入普通 CI。
7. 迁移采用只增不删的扩展顺序。旧账号、旧会话和旧数据不转换成外部身份；回滚只能关闭 provider 并回退代码，不能在已有外部身份写入后向下执行破坏性降级。

## 1. 领域边界与不变量

### 1.1 术语

| 术语 | 定义 |
| --- | --- |
| 外部身份 | provider 对某个用户主体的稳定标识，由 `provider + issuer + subject` 唯一确定 |
| provider | 本系统支持的外部认证实现，目前为 `google` 和 `github` |
| 身份绑定 | 已登录账号把一个外部身份加入自己的账号 |
| 身份解绑 | 已登录账号移除一个外部身份，但必须保留至少一种可用登录方式 |
| 登录挑战 | BFF 为一次浏览器登录生成的 state/PKCE/nonce 上下文 |
| 操作挑战 | usersrv 为绑定、解绑、首次设密或恢复操作保存的一次性授权上下文 |
| 操作回执 | 按 `(user_id, operation, mutation_id)` 唯一的结果，用于安全重放；匿名注册结果保存在其挑战行 |
| 绑定时间 | 外部身份第一次成功绑定到账号的 `linked_at`；不能用登录时间覆盖 |

### 1.2 不能被破坏的规则

- `users.id` 是不可变 UUID。外部 subject、provider 用户 ID、邮箱和返回地址都不能成为内部 `sub`。
- 一条外部身份只能属于一个账号。一次登录发现已绑定身份时只登录其所属账号；发现同邮箱的另一个账号时不能自动合并。
- provider 返回的 email 只参与“新账号创建时的提示资料”和可选的邮箱验证策略，不参与身份匹配，不自动把外部身份绑定到已有同邮箱账号。
- Google 必须校验 issuer、audience、签名、`exp`、`iat`、nonce 和 `sub`；GitHub 必须用授权 code 换取 token，校验 token 归属、GitHub API 响应和 `id`，不接受客户端传来的用户 ID。
- 不把 email claim 当作 OzID 已验证邮箱。外部登录的 provider 邮箱不满足 `require_verified_email` 时统一返回 `PROVIDER_REJECTED / UNAUTHENTICATED`，不创建账号、身份或注册邮件挑战。改用现有邮箱验证注册或其他登录方式；绑定已有账号必须是独立的、同时证明当前账号及目标身份的 Link 流程。
- 外部登录成功只创建 BFF 会话，不直接创建产品 profile。产品仍按现有“首次受保护调用时按需建立投影”的规则工作。
- 绑定、解绑、首次设密、恢复成功都会写安全审计；改变可登录凭据集合的操作递增 `auth_version`。单纯重复操作不递增，但要返回原回执。
- 解绑最后一个外部身份时，账号必须已有密码或另一个有效身份；删除最后一种登录方式返回 `FAILED_PRECONDITION / LAST_CREDENTIAL`。
- 删除账号会删除外部身份、挑战、回执和账号；审计只保留不可逆的用户 UUID、动作、时间和结果，不能保存 provider access token、refresh token、authorization code 或原始 claims。

### 1.3 已确认的产品选择

| 选择 | 冻结结论 |
| --- | --- |
| 第三方注册 | 允许无密码账号；provider 证明、必要的 OzID 邮箱验证和显式创建确认全部完成后才创建正式用户。Google 仅已验证 Gmail / 有效 Workspace 托管地址可免 OzID 邮件验证；GitHub 已验证候选地址及其他 Google 地址仍须 OzID 验证 |
| 同邮箱冲突 | 邮箱唯一性保持；不自动合并、绑定、登录或创建重复账号，不提示该邮箱是否已存在。统一引导使用原方式登录后主动绑定 |
| 首次设密 | 可选，允许长期无密码；使用已有邮箱重置流程首次设密，不接受仅凭 provider 回调设置密码 |
| 身份数量 | 每账号每 provider 最多一条，可同时有 Google 和 GitHub；不是可配置的多身份上限 |
| 最后身份解绑 | 删除最后一条外部身份前必须已有可用密码；仍有另一条身份时可解绑，邮件恢复本身不计为登录方式 |
| 资料更新 | 每次成功认证更新 provider 快照；昵称和头像仅在注册时用作默认值，后续不覆盖 OzID 邮箱、本地资料或产品数据 |
| 首批范围 | Google 和 GitHub 同版覆盖普通用户注册、登录、绑定与解绑，只在 OzID 提供入口；管理功能继续要求近期真实 OzID 密码认证；两 provider 独立开关且默认关闭 |

未验证、缺失或不可投递的 provider 邮箱在本版外部登录中走统一失败和现有邮箱注册引导，不新增 `EMAIL_VERIFICATION_REQUIRED` 对外错误。此项是已确认工程建议对 #103 A02 中“未验证或缺失邮箱继续补填”的收紧；不得同时实现两种相反分支。provider 已绑定身份的稳定主体匹配不以邮箱查找账号；若本次登录不满足验证策略，仍拒绝本次认证而不删除原绑定。

## 2. usersrv 数据模型与索引

以下是目标模型。字段名是契约名称；具体 Ent 生成字段和 SQL 约束必须由 schema 生成器产生。

### 2.1 `external_identities`

| 字段 | 类型/约束 | 说明 |
| --- | --- | --- |
| `id` | UUID PK | 内部身份记录 ID |
| `user_id` | UUID NOT NULL | usersrv `users.id`；级联删除 |
| `provider` | enum `google/github` | provider-neutral adapter 的稳定名称 |
| `issuer` | 规范 URL，NOT NULL | OIDC 用 `https://accounts.google.com`；GitHub 固定为配置的 issuer 标识 |
| `subject` | 不透明字符串，NOT NULL | Google `sub` 或 GitHub `id` 的规范十进制文本 |
| `email_snapshot` | 可空 | 最近一次 provider 返回的邮箱，仅供账号界面审计/提示，不用于唯一性 |
| `email_verified_at` | 可空 | provider 明确证明邮箱时记录；不是本地邮箱验证记录 |
| `display_name_snapshot` | 可空 | 最近一次 provider 资料快照，不覆盖用户自己的基础资料 |
| `avatar_url_snapshot` | 可空 | 只保存通过 URL 规则校验的公开 URL |
| `linked_at` | timestamptz NOT NULL IMMUTABLE | 第一次绑定时间 |
| `last_authenticated_at` | timestamptz NOT NULL | 最近一次通过该身份完成登录 |
| `updated_at` | timestamptz NOT NULL | provider 资料快照更新时间 |

约束和索引：

- `UNIQUE(provider, issuer, subject)`：防止同一外部身份绑定两个账号。
- `UNIQUE(user_id, provider)`：落实每账号每 provider 一条；同时在持有用户行锁时检查并返回 `IDENTITY_LIMIT_REACHED`。同一身份已属于本用户时先按幂等规则处理，不误报数量超限。
- `INDEX(user_id, linked_at, id)`：稳定列出账号的绑定身份。
- `CHECK(provider IN ('google','github'))`，`issuer` 必须是该 provider 配置的规范 issuer；不能由请求选择任意 issuer。
- `user_id` 外键引用 `users(id) ON DELETE CASCADE`。跨服务产品库仍然只存用户 UUID，不建立跨库外键。
- 不保存 provider access token、refresh token、ID token、authorization code 或完整 claims。若未来需要 provider API 授权，另建独立的加密凭据表和独立权限评审。

### 2.2 `users` 的认证字段

在现有 `users` 上增量调整：

| 字段 | 目标语义 |
| --- | --- |
| `password_hash` | NULL 表示无密码账号；非 NULL 必须是自描述 Argon2id 哈希 |
| `password_set_at` | 新代码设置或重置密码时写入；旧账号历史设密时间未知则保留 NULL，不伪造时间。has_password 只以 hash 非 NULL 判断 |
| `auth_version` | 保持现有单调递增规则；外部身份绑定/解绑和首次设密也递增 |

`password_hash IS NULL` 不能只靠 `users` 行内 CHECK 证明“存在外部身份”，因为 PostgreSQL CHECK 不能安全引用另一张表。usersrv 的事务写路径必须执行以下不变量，并由并发集成测试覆盖：

- 创建无密码账号的事务必须同时创建第一条有效 `external_identities`。
- 删除或解绑身份的事务遵守 §5.4 的 external key → users → identity 顺序，在用户锁下重算删除后仍有密码或至少一条身份。
- `ResetPassword` 首次设密事务锁账号并写哈希；成功后账号不再依赖外部身份。
- 账号删除在 usersrv 内按外键级联清理身份；BFF 的产品清理仍由注销工作流负责。

### 2.3 `auth_challenges`

现有 `account_tokens` 继续服务注册和密码重置，避免改变既有 token 的用途语义。第三方扩展新增 `auth_challenges`，不复用注册 token 的字段号或 purpose。

| 字段 | 类型/约束 | 说明 |
| --- | --- | --- |
| `id` | UUID PK | 挑战 ID |
| `kind` | enum | `external_login`, `external_registration`, `identity_link`, `identity_unlink`, `set_password`, `recovery`, `account_deletion` |
| `user_id` | UUID NULL/NOT NULL | 登录挑战为空；绑定及账号操作必须绑定用户 |
| `provider` | enum NULL | 登录/绑定必填，解绑/设密为空 |
| `issuer` | canonical NULL | 由 provider 配置决定 |
| `bff_flow_id` | UUID NULL UNIQUE | 外部登录/绑定挑战与 BFF flow 的一一关联键；不是授权凭据 |
| `context_hash` | bytea | 对用途、provider、配置代次、固定 redirect URI、BFF 浏览器绑定摘要及发起会话的规范摘要；通过内部签名请求传递 |
| `session_binding_hash` | bytea NULL | 账号敏感操作绑定发起会话，不能由客户端指定 |
| `target_identity_id` | UUID NULL | 解绑等操作绑定具体身份记录，不能因重绑复用旧确认 |
| `provider_config_version` | bigint NULL | 发起时的配置代次；提交时必须仍匹配 |
| `proof_ciphertext` | bytea NULL | 跨请求所需的最小已验证身份/候选邮箱证明，加密、短期、不可给客户端；不含 OAuth token、code 或完整 claims |
| `email_token_hash`, `confirmed_at` | bytea / timestamptz NULL | 新增邮件确认只保存 token 摘要；POST 确认后仍需原浏览器最终提交 |
| `status` | enum | `issued`, `exchanging`, `ready`, `completed`, `failed`；见 §5 |
| `result_mutation_id`, `result_request_hash` | string / bytea NULL | 匿名注册最终提交的幂等键及规范请求 hash；每个挑战仅能保存一个最终结果 |
| `result_user_id`, `result_expires_at` | UUID / timestamptz NULL | 注册成功创建的 UUID 及结果保留期限；不是恢复/登录凭据 |
| `auth_version` | bigint NULL | 签发时版本，要求提交时仍匹配 |
| `return_context` | 受注册表约束的 app/path | 只保存已由 BFF 解析的返回上下文 |
| `expires_at` | timestamptz | 浏览器流程 15 分钟，新增邮件确认 10 分钟；组合证明取最早到期 |
| `consumed_at` | timestamptz NULL | 单次消费 |
| `created_at` | timestamptz | 创建时间 |

usersrv 不保存 `state_hash`、原始 state、PKCE verifier 或 nonce。它们只属于 BFF。challenge ID 和 flow ID 都不是持有者凭据；每次调用仍校验内部签名、context、用途、账号/会话和版本。新增 `(expires_at)`、`(user_id, kind, expires_at)` 索引。邮件申请、确认和重发沿用现有限流，重发作废旧 token。邮箱确认子状态由 `email_token_hash/confirmed_at/consumed_at` 派生，不与授权码交换的 status 混用；最终流程在注册/绑定证明已 ready 时设置对应 kind。

### 2.4 `auth_operation_receipts`

写操作回执用于丢失响应后的安全重试，不是通用事件表。

| 字段 | 约束 | 说明 |
| --- | --- | --- |
| `id` | UUID PK | 回执 ID |
| `user_id` | UUID NOT NULL | 账号范围 |
| `operation` | enum | `identity_link`, `identity_unlink`, `set_password`, `recovery` |
| `mutation_id` | 1–64 字符 | 调用方幂等键 |
| `request_hash` | 32-byte SHA-256 | 相同 mutation_id 不允许换请求体 |
| `result` | 固定枚举 | `applied`, `already_applied`, `rejected` |
| `auth_version` | bigint | 操作后的版本或拒绝时版本 |
| `identity_id` | UUID NULL | 受影响身份，不存 token |
| `created_at`, `expires_at` | timestamptz | 成功回执保留 24 小时 |

唯一约束为 `UNIQUE(user_id, operation, mutation_id)`，并建立 `(user_id, created_at DESC)` 和 `(expires_at)` 索引。回执过期后 mutation ID 可以复用，但新的请求 hash 必须重新计算；已删除账号没有回执接管权限。

### 2.5 审计、outbox 与 BFF 状态

- `security_audit` 增加固定动作：`external.login`、`external.link`、`external.unlink`、`password.set_first`、`account.recovery`、`external.login.reject`。保留现有“不记录邮箱、密码、token、claims”的规则。
- 账号状态变更、凭据变化和身份绑定变化与审计在同一 usersrv 事务提交。登录审计可以在成功建立账号会话前写入；失败登录只写脱敏 provider、结果分类和 request ID。
- 成功绑定、解绑、首次设密在同一账号事务写入通知事件和加密 outbox，唯一事件键为 `(source_kind, source_id, notification_kind)`：绑定/解绑 source 为操作回执 ID，首次设密为现有 reset token ID。幂等重放不重复入队。收件人为 OzID 已验证邮箱，通知不含 token 或一键撤销链接。投递失败不回滚已提交操作；投递重试/不确定结果沿用现有分类，不承诺远端 exactly-once。
- 不改变现有 `verify_email` / `reset_password` 用途；新增注册邮箱/操作确认模板使用独立 purpose，敏感邮件材料仅进入加密 outbox，不入日志；OAuth code/token 永不入队。
- BFF 新增 `oauth_browser_states`：`id`、`usersrv_challenge_id UNIQUE`、`state_hash UNIQUE`、`provider`、`intent`、`browser_binding_hash`、`session_id/user_id nullable`、`auth_version nullable`、`provider_config_version`、`context_hash`、`nonce_hash`、`pkce_verifier_ciphertext`、`return_context`、`created_at`、`expires_at`、`consumed_at`、`result_status`。nonce 原文放在同一加密流程载荷中，以便内部提交校验；建立 `(expires_at)` 索引。
- BFF 状态只允许当前浏览器会话继续绑定；匿名登录 state 必须绑定启动时的 CSRF/浏览器上下文摘要。callback 不接受客户端传入的 user ID、provider subject 或 return URL。
- BFF 继续保存 `auth_browser_sessions` 和 `bff_access_tokens` 的 `auth_version`。外部登录创建新会话时写入 usersrv 返回的当前版本；角色和账号状态仍由每请求 `GetAuthState` 复核。

### 2.6 BFF / usersrv 跨服务绑定与消费

1. BFF 在 OzID Origin + CSRF 校验后生成 flow ID、至少 32 字节随机 state/浏览器绑定、PKCE S256 和 Google nonce，计算 context hash。绑定意图同时记录发起 user、session 和 `auth_version`；各标签页流程独立。
2. BFF 调用 `BeginExternalLogin(flow_id, intent, provider, context_hash, 固定 redirect URI, 授权参数)`；usersrv 创建唯一 `bff_flow_id` 的 challenge 并返回 `challenge_id` 和授权 URL。授权参数只在构造 URL 时使用，不保存在 usersrv。BFF 持久化 challenge 引用后才给浏览器 URL；BFF 写失败留下的 usersrv 孤儿挑战到期清理，不能独立完成认证。
3. callback 从 state 摘要找 BFF 行，校验 provider、浏览器绑定、TTL、意图和发起会话仍有效。缺失/重放/过期/跨浏览器统一 `STATE_INVALID`，不调用 usersrv、不写会话。有效上下文中的 callback 用条件更新 `consumed_at IS NULL AND expires_at > now` 原子抢占；并发最多一个成功。取消或协议错误也终结该有效流程。
4. BFF 将存储的 challenge ID、flow ID、context hash、code、解密 verifier/nonce 通过内部签名 RPC 交 usersrv，不采信 callback 中的用户、邮箱或返回地址。usersrv 核对持久化绑定后，用短事务把 `issued` 改为 `exchanging`；只有抢占者可在事务外交换 code、验证 provider。
5. provider 校验成功后按 §5.4 锁序完成身份/账号检查，将 challenge 标为 `ready`（待注册或待绑定确认）或 `completed`（已有身份登录）；账号写入、最终 challenge 消费、审计、回执、通知在各自 usersrv 事务中原子提交。失败标为 `failed`，原 code 不能重兑。
6. 两个服务不承诺跨库事务。BFF state 已消费而 RPC 超时、进程退出或 provider 结果丢失时，此 callback 不可重试，重新发起授权；滞留 `exchanging` 到期失败，不能再次兑换。已提交账号保留，重登同一身份获得同一 UUID。注册/绑定的最终提交通过原浏览器独立流程凭据和 mutation 回执处理丢响应，不通过重放 callback 恢复。
7. BFF 在设置会话前再次核对发起浏览器当前会话代次/意图，拒绝迟到或换账号的结果；绑定成功不创建替换会话。清理只删除本 flow 的临时材料，不清理其他标签页、现有会话或注销恢复 Cookie。每次完成、失败或到期清理 verifier/nonce 和短期证明。

## 3. usersrv.v1 与 bff.v1 契约

### 3.1 usersrv.v1 新增 RPC

现有邮箱密码 RPC 保持字段和错误含义；`ResetPassword` 扩展到 NULL 哈希，仍以有效邮箱 token 而非外部回调授权。以下新增方法名及职责为实现契约，message 字段号在 Stage 2 的 proto 中生成：

| RPC | 作用 | 关键不变量 |
| --- | --- | --- |
| `BeginExternalLogin` | 校验已启用 provider，生成授权参数/挑战引用 | 不创建账号，不接受任意 issuer/redirect URI |
| `CompleteExternalLogin` | 用 challenge、code、verifier、nonce 完成 adapter 校验 | 已绑定身份返回当前用户；未绑定只返回 `registration_pending` 或 `link_pending`，不创建用户，不返回 subject |
| `RequestExternalRegistrationEmail` / `ConfirmExternalRegistrationEmail` | 必要的 OzID 联系邮箱验证 | 仅有效 provider 证明可申请；编辑邮箱作废旧确认；邮件确认本身不创建用户 |
| `CompleteExternalRegistration` | 原浏览器显式确认创建 | 重查注册/provider 开关、邮箱证明与唯一性；用户、首个身份、challenge、回执和审计同事务 |
| `BeginIdentityOperation` / `ConfirmIdentityOperation` / `GetIdentityOperation` | 绑定/解绑/注销的单次账号确认与状态查询 | 有密码验证当前密码；无密码使用 OzID 邮件；绑定用户、会话、版本、动作和目标 |
| `LinkExternalIdentity` | 把已校验身份绑定到签名凭证的用户 | 要求当前 `auth_version`；同身份已属于同用户则幂等，属于他人返回冲突 |
| `UnlinkExternalIdentity` | 删除指定身份 | 请求只接受身份记录 ID；禁止删除最后凭据 |
| `SetInitialPassword` | 邮箱首次设密的语义名称，复用 `ResetPassword`，不新增 provider 设密旁路 | 目标来自 reset token；提交时仍无密码且版本匹配，设置后不自动登录 |
| `RecoverAccountWithExternalIdentity` | 已绑定身份的重新登录语义，复用 Begin/CompleteExternalLogin | 不新增绕过邮箱或账号状态的恢复 RPC，不提升版本 |
| `GetExternalIdentities` | 返回本人绑定身份的安全摘要 | 不返回 subject 原文、token 或 claims |

建议 message 字段：`provider`、`challenge_id`、`mutation_id`、`expected_auth_version`、`external_identity_id`。用户 ID 只从 BFF 签名凭证的 `sub` 取得；匿名登录的目标用户只由 usersrv 对 `(provider, issuer, subject)` 查询或创建，绝不从请求体取得。

`CompleteExternalLogin` 返回互斥结果：`authenticated`（`user_id`、profile、roles、`auth_version`、`has_password`）或 `registration_pending/link_pending`（challenge 引用、可展示候选资料、必要验证状态）。待定结果不携带用户 ID、角色或会话授权；正式注册信息仅在显式创建完成后返回。本人身份列表只含内部 identity ID、provider、显示标签和时间，不包含 subject、token 或完整 claims。匿名请求不暴露邮箱是否占用。

### 3.2 bff.v1 浏览器契约

| RPC/HTTP 路由 | 权限 | 语义 |
| --- | --- | --- |
| `StartExternalLogin` | 匿名/当前会话 | 解析稳定 provider 与返回路径，创建 BFF state，返回 provider authorization URL |
| `CompleteExternalLogin` | callback 匿名入口 | 按 §2.6 消费 state，转发内部证明；仅 authenticated 结果可设置 HttpOnly Cookie，待注册/绑定结果跳固定 OzID 完成页 |
| `RequestExternalRegistrationEmail` / `ConfirmExternalRegistrationEmail` / `GetExternalRegistration` / `CompleteExternalRegistration` | 受浏览器流程保护；邮件确认允许另一设备 | 邮件 GET 不消费；原浏览器显式最终提交才创建及自动登录 |
| `ListExternalIdentities` | 当前会话 | 返回 provider 名称、显示标签、`linked_at`、最近认证时间 |
| `LinkExternalIdentity` | 当前会话＋当前密码或本操作邮件确认 | 创建绑定挑战并进入 provider；不接受 email 作为绑定键 |
| `UnlinkExternalIdentity` | 当前会话＋当前密码或本操作邮件确认 | 传 `external_identity_id` 和 mutation_id；最后凭据拒绝 |
| `BeginIdentityOperation` / `ConfirmIdentityOperation` / `GetIdentityOperation` | 原会话或其绑定的结果查询流程凭据 | 密码/邮件证明只授权具体操作；成功后旧会话只能凭原流程查询同一回执，不能执行新写操作或建立会话 |
| `SetInitialPassword` | 现有 RequestPasswordReset / ResetPassword 邮件流程 | 无需外部认证旁路；首次设密成功提升版本，全部旧会话失效，不自动登录 |
| `RecoverWithExternalIdentity` | 匿名 callback | 只从已绑定身份恢复，成功后建立新会话，不自动链接新身份 |

callback 的 `error`、`error_description` 和 provider 返回体不能原样转发给浏览器；前端只收到固定错误分类和本地化文案。成功后的 return URL 必须来自 BFF `navigation` 注册表。

固定 HTTP callback 为 `/api/identity/callback/google` 和 `/api/identity/callback/github`，只在 gateway 中显式登记；历史 `/auth/*` 继续不可用。普通写 RPC 要求 OzID Origin + CSRF；跨站 callback 单独使用 state/浏览器绑定校验，不豁免其他写请求。callback 及邮件落地页均 no-store/no-referrer，处理后移除协议敏感参数，不把 code、state 或新持有者票据传给 SPA。`authenticated_at` 在第三方新会话为空，只有真实密码校验才能写入，不能用会话创建时间代替。

### 3.3 错误语义

| reason | gRPC/浏览器状态 | 使用场景 |
| --- | --- | --- |
| `PROVIDER_DISABLED` | `FAILED_PRECONDITION` / `failed_precondition` | provider 未启用或未完成配置 |
| `STATE_INVALID` | `FAILED_PRECONDITION` / `unauthenticated` | state 缺失、重复、过期、浏览器上下文不匹配 |
| `PROVIDER_REJECTED` | `UNAUTHENTICATED` / `unauthenticated` | 用户取消、code 无效、provider 身份无法验证 |
| `IDENTITY_CONFLICT` | `ALREADY_EXISTS` / `already_exists` | 外部身份属于另一个账号 |
| `IDENTITY_LIMIT_REACHED` | `FAILED_PRECONDITION` / `failed_precondition` | 本账号该 provider 已有另一条绑定 |
| `REGISTRATION_UNAVAILABLE` | `FAILED_PRECONDITION` / `failed_precondition` | 注册关闭、邮箱冲突或不能完成创建；统一引导原方式登录/邮箱注册，不提供邮箱占用 reason |
| `NOT_FOUND` | `NOT_FOUND` / `not_found` | 本人解绑目标不存在且无可重放回执；不透露其他账号是否拥有该 ID |
| `LAST_CREDENTIAL` | `FAILED_PRECONDITION` / `failed_precondition` | 解绑后无密码且无其他身份 |
| `VERSION_CONFLICT` | `ABORTED` / `aborted` | expected auth version 过期 |
| `MUTATION_REPLAY_MISMATCH` | `ABORTED` / `aborted` | mutation_id 重用但 request_hash 不同 |
| `CHALLENGE_INVALID` | `FAILED_PRECONDITION` / `failed_precondition` | 操作挑战用途/用户/版本不匹配 |
| `INVALID_ARGUMENT` | `INVALID_ARGUMENT` / `invalid_argument` | provider、路径、密码或 ID 格式错误 |
| `UNAVAILABLE` | `UNAVAILABLE` / `unavailable` | usersrv、Redis、provider 元数据或必要存储不可用 |

`PROVIDER_REJECTED` 不区分用户取消、错误 code 和不满足邮箱验证策略的 provider 证明；不暴露内部未验证邮箱分类，不定义对外 `EMAIL_VERIFICATION_REQUIRED` reason。有效且未绑定的身份进入显式注册确认，不是“未知身份错误”，不得用候选邮箱查账号后改变响应。账号 disabled/deleting 沿用现有中性认证失败。日志只记录 provider、固定结果分类、流程/request ID 和必要的内部用户 UUID，不含邮箱、外部主体或协议秘密。

## 4. provider-neutral adapter

usersrv 内部接口不暴露 Google/GitHub 的 SDK 类型：

```text
ExternalProvider
  Name() ProviderName
  AuthorizationURL(ctx, AuthorizationRequest) (url, error)
  ExchangeCode(ctx, CodeExchangeRequest) (ProviderIdentity, error)
  ValidateConfiguration() error
```

`ProviderIdentity` 只允许返回：规范 provider、issuer、subject、可选 email、email_verified、`email_trust`、display_name、avatar_url、认证时间。`email_trust` 取 `google_authoritative/provider_verified/unverified`；只有完成令牌校验后证明为 Gmail 或有效 Workspace 托管邮箱时可取第一类，不能仅依赖邮箱后缀或客户端 hosted-domain。GitHub 验证邮箱最多为第二类，不免 OzID 验证。adapter 必须负责：

- 固定官方 endpoint、TLS 证书验证、禁止重定向到未配置 host；
- 上游响应体大小、超时、JSON schema 和错误分类限制；
- 清除 access/refresh token，不把 secret 写入日志、审计或响应；
- Google 的 OIDC discovery/JWKS 缓存和 nonce 校验；
- GitHub code exchange、用户身份 API、必要的 verified email API 调用，并把 GitHub 数字 ID 转为规范 subject；
- 启动时校验 client ID、client secret、redirect URI、allowed issuer 和 enabled provider 的完整性。

adapter 不自动重试有副作用的 code exchange，不在 provider 之间故障转移。provider 配置代次写入 challenge，配置变更会使未完成 challenge 失败并要求重新开始。

## 5. 状态机、幂等和锁序

### 5.1 注册/首次设密

- 邮箱注册保持现状：申请 → `account_tokens` 邮箱验证 → 创建 active 账号；不因 provider 扩展改变中性响应。
- 外部登录新账号：授权 → provider 验证 → `registration_pending`，此时没有正式用户。受信 Gmail/Workspace 邮箱可直接作为已验证联系地址；GitHub 已验证候选地址及其他 Google 地址通过 OzID 邮件确认。原浏览器显式提交时再锁 external key、重查开关/证明/邮箱唯一性，创建 active 账号＋首条身份＋注册回执＋审计并消费挑战；新账号无密码、无管理角色。邮箱并发冲突回滚整个事务并统一 `REGISTRATION_UNAVAILABLE`。
- 新注册幂等结果保存在挑战本行（提交前没有 user_id），字段为 `result_mutation_id/result_request_hash/result_user_id/result_expires_at`。挑战主键及行锁保证每个挑战至多一次成功，结果保留 24 小时；相同 mutation/hash 只返回同一 UUID，不重复建号。不同 hash 为 `MUTATION_REPLAY_MISMATCH`，已完成挑战不接受新的 mutation；不能凭结果引用直接签发会话。注册昵称/头像沿用现有校验，不抓取远程头像内容。
- 无密码账号首次设密：申请现有 reset 邮件 → 邮箱 token 验证 → 锁账号 → 锁 reset token → 写密码、递增版本、消费该账号全部 reset token、写审计和唯一通知事件 → completed。现有 reset token 单次规则不改变；重复提交是中性 TOKEN_INVALID，不伪造新的重置回执或第二次通知。
- 新增注册邮箱/操作邮件：`issued → confirmed → consumed`；POST 确认不执行最终操作，GET/扫描器不消费。邮箱 token 仅保存摘要，10 分钟到期；原浏览器 flow 有效期最多 15 分钟。两种证明都有效才可最终提交；另一设备不得因此改变 Cookie 或绑定。已有注册/密码重置仍可在另一设备完成，不受新增交接规则改写。

### 5.2 绑定与解绑

- 绑定：当前账号密码确认或本操作邮件确认＋独立 provider 身份证明 → 原浏览器显式提交 → 按 §5.4 加锁、核对 challenge/版本和 provider 开关 → 插入身份 → `auth_version + 1` → 原子消费挑战、审计、通知事件和回执。当前会话也失效，成功页引导重新登录，不签发替换会话。
- 解绑：当前账号密码确认或本操作邮件确认 → 通过身份 ID 非锁定读取不可变 external key → external-key advisory lock → users 行锁 → identity 行锁 → challenge/receipt → 重读目标与剩余凭据 → 删除指定记录 → `auth_version + 1` → 挑战、审计、通知事件和回执同事务提交。无目标但有同请求回执先重放；否则 `NOT_FOUND`。旧 identity ID 不能删除后来重绑的新记录。
- 绑定同一身份到同一账号是幂等成功；绑定到不同账号是 `IDENTITY_CONFLICT`，不提供自动合并、转移或“证明邮箱后抢占”。

### 5.3 恢复

已绑定的外部身份登录成功后，usersrv 返回当前账号和版本；BFF 创建新会话。它不会把新的 provider email 绑定到账号，也不会改变账号邮箱、密码或角色。账号处于 disabled/deleting 时与邮箱登录一样拒绝，不通过 provider 绕过状态。

所有 provider 关闭时仍保留现有邮箱首次设密/重置能力；邮箱也不可访问时无强制改邮箱、转移身份或运维认证后门。无密码注销使用 `account_deletion` 邮件挑战绑定本账号/会话/版本/具体请求，加现有中文“注销”确认词；BFF 先建立恢复 Cookie，再 BeginDeletion，保留平台管理员保护与跨产品持久化清理。清理本地绑定不依赖 provider 在线。

### 5.4 全局锁序

事务使用以下总顺序，只获取实际需要的锁；不需要 external key 的纯密码/状态变更和仅删除本账号关联的注销流程跳过第 1 项，不能拿 users 锁后再获取 external key：

1. 对规范化 external key `(provider, issuer, subject)` 获取事务 advisory lock；多个 key 按字节序排序。
2. 查询并锁定目标 `users` 行 `FOR UPDATE`；新账号在 identity key 锁下创建。
3. 锁定相关 `external_identities` 行；多行按规范 external key、identity ID 排序。
4. 锁定相关 `auth_challenges` / `account_tokens` 行（各类内按 ID 排序）。
5. 查询并锁定同一 `(user_id, operation, mutation_id)` 的回执；不存在时在用户行锁下插入并依赖唯一约束兜底。
6. 写入 `users`、`external_identities`、挑战消费状态、审计、通知/outbox 和回执后提交。

只涉及密码的现有流程仍先锁 `users`，不持有 external key；它不能在同一事务中再反向获取 external key。任何需要多个身份的操作先按 external key 排序。provider 网络请求永远发生在数据库事务外。

| RPC/操作 | 实际锁集合（从左到右，省略不存在的行） |
| --- | --- |
| BeginExternalLogin / code 抢占 | 只锁本 challenge 的短事务，提交后才发 provider 网络请求；不得持锁再锁用户 |
| CompleteExternalLogin / 外部重新登录 | external key → users → identity → challenge；更新快照/认证时间，不提升 auth_version |
| CompleteExternalRegistration | external key → 插入新 users（邮箱唯一索引）→ identity → challenge/注册回执；插入冲突整笔回滚，不改已有邮箱账号 |
| LinkExternalIdentity | external key → users → identity（已有时）→ provider/操作 challenges（ID 排序）→ receipt |
| UnlinkExternalIdentity | 非锁定读取 key → external key → users → 指定 identity → challenge → receipt；拿锁后重验 ID/归属/剩余方式 |
| SetInitialPassword / ResetPassword | 非锁定解析 token → users → account_tokens → 通知去重事件；锁后复核 token 及签发版本，不获取 external key |
| Begin/ConfirmIdentityOperation | users → 本操作 challenge；先行只读信息拿锁后重验，不反向请求 external key |
| BeginDeletion / FinishDeletion | users → 账号已有身份行（按 key 排序）→ challenges/tokens → 注销回执；只删除本用户关联、不争抢新 key，因此不持有/等待 advisory key，外键级联同步完成 |
| 现有改密、禁用/启用、角色变更 | 保持 users-first；不获取 external-key advisory lock |

已有身份的 user_id 和 external key 不允许原地转移/修改。解绑预读可能过期，拿锁后必须复核；注册邮箱与普通注册竞争依赖同一邮箱唯一约束。重放回执在版本失败之前识别，但仍需校验原操作/会话绑定及 request_hash；回执只能显示原结果，不授予旧会话继续执行或登录的能力。

## 6. 配置与发布边界

新增 usersrv 配置为 `auth.external.enabled`、`auth.external.providers.{google,github}.{enabled,client_id,client_secret,redirect_uri,config_version}`、`auth.external.require_verified_email=true`（本期不可放宽）、`auth.external.proof_key_id`。官方 issuer/endpoint 固定，redirect URI 必须与 BFF 已登记 callback 精确匹配，不能由请求扩展。

BFF 增加 `auth.external.{enabled,state_ttl,confirmation_ttl,state_encryption_key_id}`，TTL 分别默认 15/10 分钟；仅接受固定 provider 名称、callback 与导航注册表。OAuth client secret 只在 usersrv Secret 中，BFF 仅有自己的临时载荷加密 key。配置代次变化使旧流程失败，未启用项不要求配置真实 Secret。

全局和各 provider 均默认 `enabled=false`；启用项缺少 secret、redirect URI 不匹配、issuer 不在固定列表、必要加密 key 缺失或 provider 配置混杂时所属服务启动失败。关闭项不发授权 URL，新登录/绑定和未提交流程均拒绝；已建本地会话不因停用退出。查看/解绑与邮箱首次设密不依赖 provider 在线。readiness 不连接真实 provider；临时厂商故障不阻塞邮箱密码登录、匿名地图或公开目录。

流程启动/回调/提交分别限流，沿用原规格每 IP 30 次/15 分钟、账号敏感操作 10 次/15 分钟、token 提交 5 次/15 分钟的工程默认值；新增邮件沿用 60 秒冷却及现有邮箱/IP/全局合计限额。Redis 故障拒绝受控操作，不降级绕过验证。

Google OIDC 与 GitHub OAuth 各有一条真实厂商冒烟门槛：授权、取消、成功登录、绑定、解绑、重复 callback、过期 state、未验证邮箱、provider 暂时不可用、改密后旧会话失效。凭据、测试邮箱和域名由运维提供；CI 只使用本地 fake provider，不能把 fake 测试写成真实厂商已验证。

## 7. 旧 schema 兼容矩阵

| 旧对象/行为 | 目标状态 | 兼容处理 | 破坏性边界 |
| --- | --- | --- | --- |
| `users.password_hash NOT NULL` | 可空，另加 `password_set_at` | 先放宽约束；现有哈希原样保留 | 不得把 NULL 回填为空字符串 |
| `account_tokens` 注册/重置 | 保持原表和 purpose | 不改已有字段号/用途；新挑战用 `auth_challenges` | 不把 OAuth state 写进 account token |
| `auth_browser_sessions` | 保持 user UUID、token hash、auth_version | 新会话写当前版本；旧 `auth_version=0` 继续失效 | 不导入旧 Logto/旧 session |
| `bff_access_tokens` | 保持版本复核 | 外部身份不会直接创建管理令牌 | 不因 provider 登录绕过角色检查 |
| `security_audit` | 增加固定外部身份动作 | 同现有脱敏与保留策略 | 不回填 provider token/claims |
| 旧 `auth_identities` / Logto 表 | 退役历史对象 | 只按现有手动退役方案清理 | 不自动迁移为 external identity |
| 产品 `users` profile | 继续保存业务投影 | 以 usersrv UUID 对齐，首次业务调用按需确保 | 不用 provider email 重新匹配历史资料 |
| BFF OAuth state | 当前运行时不存在 | 新增独立状态表，默认不接受第三方流程 | 不复活旧 `/auth/callback/*` 语义 |

## 8. 迁移顺序与回滚约束

1. **冻结与备份**：记录当前 usersrv/BFF migration head、备份 usersrv 与 BFF 数据库，确认 provider 开关关闭。
2. **只增 schema**：应用 usersrv `password_hash` 可空和 `password_set_at`；创建 `external_identities`、`auth_challenges`、`auth_operation_receipts` 及索引；BFF 创建 `oauth_browser_states`。不删旧表、不改旧 token 语义。
3. **生成与部署兼容代码**：在开发时先修改 schema、运行 `make ent-usersrv`、`make ent-bff` 并生成/评审迁移；发布时使用第 2 步已评审的迁移，不在生产运行生成器。提交 Ent 生成物及 `make usersrv-migration` / `make bff-migration` 生成的迁移与校验清单；proto 改变运行 `make buf` 并提交 Go/TypeScript 生成物。兼容版本必须能读取 NULL hash，所有旧 usersrv 读写实例退出后才能允许无密码写入。
4. **双版本兼容观察**：先部署能识别新列但仍关闭 provider 的 usersrv/BFF；确认邮箱注册、登录、重置、注销和旧 session 行为不变。
5. **部署 adapter 与 fake provider**：配置 provider 但保持 `enabled=false`；启动校验、错误映射、挑战过期和审计测试通过后才允许 staging 开关。
6. **staging 开启**：只允许测试 provider 账号，验证真实 BFF + usersrv + PostgreSQL/Redis 链路；记录 `auth_version`、绑定时间、回执和回滚演练证据。
7. **发布门槛**：完成 Google/GitHub 各自真实冒烟和安全审查后，按 provider 单独开启。一个 provider 的故障不自动开启另一个。

回滚规则：

- 尚无 NULL password_hash、外部身份/挑战/回执写入时，可回退旧二进制并保留新表；发布前用数据检查证明这一条件，不能只按部署步骤猜测。
- 一旦任意外部身份、挑战或回执写入，禁止回滚 SQL 删除列/表；先关闭 provider 路由，再回退到能读取 NULL hash、支持邮箱首次设密并保留新数据的兼容版本，或发布 forward migration 修复。出现首个无密码账号后绝不能运行只接受非空 hash 的旧 usersrv。
- 不把无密码账号回填成随机密码、不删除外部身份、不把 provider 账号合并进旧账号。provider 全关闭时使用已有已验证邮箱首次设密恢复；没有邮箱控制权不得由运维静默开放登录。
- 数据库迁移失败时停止发布，不启动带半套 schema 的 usersrv；已完成的 additive migration 可保留，下一次发布继续。
- 真实厂商故障只触发 provider 级关闭；邮箱密码、匿名和公开功能的回滚边界不扩大。

## 9. A01–A21 验收映射

编号与 [Issue #103 §9](https://github.com/kongken/mono/issues/103) 一致，均为待实现/待验证要求，不能将此表视为测试结果。A02 的已确认路径收紧明确列出，不把新的编号覆盖原验收含义。

| 编号 | 原验收主题与本契约要求 | 模块/测试层 | 必须留存的证据 |
| --- | --- | --- | --- |
| A01 | Google Gmail/Workspace 显式确认前无用户，确认后无密码/无管理角色并自动登录 | usersrv 注册 + BFF + OzID 浏览器 | fake OIDC、真实库建号原子性、确认前后浏览器状态 |
| A02 | GitHub/非托管 Google 地址需 OzID 邮件验证；编辑邮箱撤销旧证明。本版未验证/缺失 provider 邮箱按 §1.3 中性失败并转现有邮箱注册，不走原补填分支 | adapter + 注册挑战 + 邮件浏览器 | 地址信任矩阵、无半用户断言、错误中性化、编辑后旧 token 失效 |
| A03 | 同一稳定身份登录到同一 UUID；更新快照不改 OzID/产品资料，密码方式保持 | usersrv + BFF integration | provider 邮箱/用户名变更前后的 UUID、资料、密码登录断言 |
| A04 | 同邮箱无自动合并/登录/重复建号，不暴露邮箱占用 | usersrv 注册/Link + 契约测试 | 邮箱唯一性竞争、中性响应、无自动绑定数据库断言 |
| A05 | 本人列表/绑定/解绑；每 provider 一条、全局身份唯一，无覆盖/转移 | usersrv Ent + 真实库并发 + BFF 权限 | 两个唯一约束、同用户上限、跨账号抢绑仅一方成功 |
| A06 | 不删光登录方式；双并发解绑安全；旧请求不删重绑记录 | usersrv transaction/integration | 无密码最后身份拒绝、并发结果、重绑新 ID 与旧回执重放 |
| A07 | 邮件首次设密后旧会话/MCP 全失效，不自动登录；无密码登录中性失败 | reset + BFF integration | NULL 哈希、token/版本单次性、全部旧凭据拒绝、新密码登录 |
| A08 | 注册/provider 独立开关在最终提交检查，停用不删关联/退出旧会话 | usersrv/BFF config + integration | 关闭注册仍可已有身份登录/绑定、直接 RPC 不可绕开 provider 开关 |
| A09 | 新邮件跨设备只确认，GET 不消费，原浏览器显式最终提交 | challenge + BFF + 真实浏览器 | 两设备 Cookie/账号隔离、预取与 GET、原浏览器最终修改 |
| A10 | 过期/消费/换账号/退出/版本变化失败，不能跨动作/目标/会话复用 | challenge + BFF integration/browser | 用途/目标/会话/版本负例矩阵、ID 本身不授予权限 |
| A11 | 绑定/解绑/首次设密原子提升版本；失败/重放不重增、不影响他人 | usersrv receipt/audit + BFF | 数据库事务、丢响应重试、旧 session/access token 请求拒绝 |
| A12 | 第三方/邮件不刷新密码时间；管理读写时效/角色/Origin/IP/MCP 保持 | BFF auth/admin + gateway/browser | 空 authenticated_at 拒绝、现有管理读/15 分钟写与边界回归 |
| A13 | 无密码注销邮件确认+确认词，恢复 Cookie 先行，清理可恢复，管理员保护 | usersrv deletion + BFF + 产品集成 | provider 离线清理、故障重试、其他用户/匿名缓存不变 |
| A14 | 安全通知随提交唯一入队；收件人/脱敏正确；失败不回滚；旧邮件不回归 | usersrv audit/mail/outbox + integration | 唯一事件键、重放不重复入队、加密载荷、假邮件失败与限流 |
| A15 | state/nonce/PKCE/签名/issuer/audience/时效/跨 provider/浏览器均校验 | adapter + BFF 契约/浏览器 | fake OIDC/JWKS/OAuth 负例、只消费一次、不写 session/绑定 |
| A16 | 登录/绑定意图隔离，切换账号/多标签页/迟到结果不覆盖新身份 | BFF + 真实浏览器 | 预授权攻击、扣住回调响应、标签页与流程 Cookie 清理隔离 |
| A17 | 注册/绑定/解绑/设密/禁用/注销竞争满足唯一性/版本/锁序，审计通知不漏重 | usersrv + Postgres/Redis 集成 | 真实并发事务、失败回滚、锁序评审、回执/事件数量 |
| A18 | 取消/超时/provider/Redis/usersrv/数据库故障不半建号、不清旧会话 | usersrv/BFF 故障注入 + 浏览器 | RPC/code 结果丢失、exchanging 到期、建号后重新登录同一 UUID |
| A19 | navigation/Cookie/CORS/CSRF 保持，秘密不进 URL/存储/Referer/日志 | BFF gateway/navigation + 浏览器 | 恶意路径负例、跨 origin、代理/应用追踪脱敏扫描 |
| A20 | 非空旧库升级/兼容回滚保留数据；无密码不能退旧版或补随机密码 | migration + deploy + integration | 非空数据库升级/回滚演练、migration head、旧 Logto 入口拒绝 |
| A21 | 桌面/手机/键盘流程、错误/取消/待邮件/过期、停用恢复引导 | OzID/packages/account + browser/e2e | 两尺寸与键盘场景、provider 全关邮箱设密、故障不清缓存 |

真实厂商 Google、GitHub 和邮件冒烟是另列的上线门槛，不替代 A01–A21 的自动化证据，也不进入普通 CI。

## 10. 生成物、命令和提交边界

- 改 usersrv Ent schema：运行 `make ent-usersrv`，提交 schema、生成 Go 文件和版本化 `migrations/usersrv/`。
- 改 BFF Ent schema：运行 `make ent-bff`，提交生成 Go 文件和 `migrations/bff/`。
- 改 `proto/usersrv/v1/` 或 `proto/bff/v1/`：运行 `make buf`，提交 Go/TypeScript 生成物；同时运行 `buf lint`。
- 改配置、adapter 或业务代码：运行 `make test`、`make build`、`make check-import-boundaries`；存储和真实内部链路另跑 `make test-integration-usersrv` / `make test-integration`。
- 本 ADR 只冻结契约和迁移方案，不创建 schema、proto、配置或 provider 实现；Stage 2 的实现票必须以本文的已确认产品选择和工程复审结果为输入。

## 11. 评审与后续交付

产品未决项：无。七项产品选择和四项工程修订已确认，见 §1.3 与 KON-37 评论记录；不再要求重复产品确认。

本修订仍需工程复审，重点检查 §2.6 的跨服务单次消费、§3 的 RPC/中性错误、§5.4 的可执行锁序及 §9 与原验收编号的一致性。复审通过后进入既有 Stage 2 模型/协议实现票；本 ADR 的提交不代表功能已实现、已部署或真实厂商已验证。

后续实现须同步受影响的 docs/ozid/features.md、docs/usersrv/spec.md、docs/bff.md、docs/backend.md、docs/admin/features.md、docs/ozid/acceptance.md 及部署文档，保持实现证据与发布门槛分开登记。
