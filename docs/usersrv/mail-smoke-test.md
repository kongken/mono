# usersrv 邮件厂商手动冒烟测试

usersrv 的四个邮件 adapter（Mailgun、SendGrid、SMTP、SMTP2GO）只对**本地受控的 HTTP/SMTP 假服务**做自动化契约测试（`make test`、`make test-integration-usersrv`），CI 与任何自动化流程**不会**连接真实厂商，也不应该为此保存厂商凭证。厂商接口格式按其公开文档实现，但没有对真实服务验证过；首次启用某个厂商前，必须由运维按本文手动验证一次。

## 通用准备

- 使用一个**独立的测试发信域名/子域名**和一个你能收信的测试邮箱；不要用真实用户邮箱。
- 域名的 SPF、DKIM、DMARC 由运维在厂商控制台与 DNS 配置，usersrv 不管理也不检查它们；缺失会让邮件进垃圾箱或被拒收。
- 厂商 API Key 只放在被忽略的 `config.usersrv.yaml` 或挂载的 Secret 中，不进仓库、命令行参数、Issue 或日志。优先使用只有“发送邮件”权限的受限 Key。
- 在 `config.usersrv.yaml` 里只启用一个 provider 段，设置 `mail.provider`、`mail.from_email`（必须是厂商已验证的发信地址）、`mail.generation` 与 `mail.encryption`。切换厂商时递增 `generation`，并先排空旧任务（队列里属于旧 provider/代次的任务会被取消，不会改发新厂商）。
- 启动 `bin/usersrv` 前确认配置校验通过：未知 provider、缺少必填参数、混入其他厂商参数、非法 region 或 TLS 模式都会让启动失败。

## 验证步骤（每个厂商各一遍）

1. 启动 usersrv 并确认 `/readyz` 的 `mail_outbox` 为 ready。
2. 触发一封邮件，二选一（都在**隔离的测试环境**里，不要在正式库里操作）：
   - **经完整的栈**（推荐，同时验证 BFF → usersrv → 厂商）：把 BFF、usersrv 与 OzID 按[部署说明](../deployment.md#usersrv-的部署)起好，在 OzID 的注册页对测试邮箱“申请注册”（或对已有测试账号“找回密码”；两个产品都没有自己的账号表单，从产品点“登录”也只会来到 OzID）；usersrv 的注册申请对任何邮箱都返回中性成功，是否真的发信以收信为准。
   - **只测 usersrv 与厂商**：在隔离环境用一个临时 Go 测试或小程序调用 `outbox.Service.IssueStandalone`（`internal/usersrv/outbox`），向测试邮箱签发一封 `verify_email` 邮件。
   注意 `scripts/real-mail-stack.sh` 只在本机起一套真实的 BFF、usersrv、tripmap、metro，并连你自己的本地 SMTP 接收器，**不是**厂商实测；它生成的配置让所有新邮件都指向中央 OzID（`OZID_WEB`，由 `internal/e2e` 的 `TestRealMailStackConfigsAgree` 检查与 BFF 的 `navigation.apps.ozid`、`cors_allowed_origins` 一致），它不启动任何前端，要打开邮件里的链接需自己在该地址提供 OzID。原先配套的 `E2E_REAL_MAIL=1` Playwright 用例已随产品里的账号表单删除（trip-map 在 T10，metro 在 T11）。
3. 在 3 分钟内（worker 轮询间隔 3 秒）检查：
   - 测试邮箱收到中文标题和正文，纯文本与 HTML 两部分都正常，发件人、回复地址（若配置了 `reply_to`）正确；
   - 邮件里的链接形如 `https://<auth.apps.ozid.origin>/account/verify-email#token=…`（T2 起所有新邮件都链接到中央 OzID），令牌在 `#` 之后；
   - Mailgun 与 SendGrid 的控制台里**关闭了点击跟踪**（usersrv 已在请求里关闭；若账户级设置强制开启，链接会经厂商域名改写，令牌会暴露给厂商，必须关掉）；
   - SMTP2GO 的 API 没有逐封关闭跟踪的开关，需在账户设置里关闭点击/打开跟踪；
   - `mail_jobs` 中该任务为 `accepted`，`ciphertext` 与 `key_id` 已清空；
   - usersrv 日志只有任务 ID、provider、模板、状态、错误类、次数、耗时，没有邮箱、令牌、链接或 Key。
4. 失败路径（各做一次后恢复）：
   - 临时换成错误的 API Key：任务应为 `failed`，日志出现 `mail_vendor_auth_failed` 告警，且**不会**重试；
   - 对 Mailgun 分别验证 `region: us` 与 `region: eu` 各自的账户；区域与账户不匹配时厂商通常拒绝请求，按厂商返回的错误调整。
5. SMTP 额外检查：
   - `tls_mode: implicit_tls`（通常 465）或 `starttls`（通常 587）；证书必须是公共 CA 签发且主机名匹配，自签证书会被拒绝，这是预期行为；
   - 服务器只支持 AUTH PLAIN/LOGIN 之外的机制时无法使用（当前只实现这两种）。

## 当前验证状态

截至 P6 交付，**没有对 Mailgun、SendGrid、SMTP、SMTP2GO 中任何一家做过真实发信**：没有可用的厂商账号、已验证的发信域名和受控收件地址。自动化只覆盖本地假服务的契约（认证方式、From/Reply-To、单收件人、中文标题、超时与错误分类），以及用本地 SMTP 接收器走完的注册与重置闭环。下表由执行的运维在上线前填写，不进入仓库。

| 厂商 | 是否已实测 |
| --- | --- |
| Mailgun（US / EU） | 否 |
| SendGrid | 否 |
| SMTP（implicit_tls / starttls） | 否 |
| SMTP2GO | 否 |

## 结果记录

在发布前的检查清单里记录：厂商、日期、执行者、上面各项是否通过。该记录不进入仓库，也不含任何凭证或测试邮箱。
