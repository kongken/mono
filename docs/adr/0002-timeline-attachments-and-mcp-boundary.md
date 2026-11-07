# Timeline 附件与 MCP 服务边界契约

**状态：草案 v3（按 KON-105 两轮独立 Review 返修），基于 KON-99 已接受实现 `8c5a51592f2a34504828295e967b75d357e07761` 与 delivery branch head `cb5787e`；供后续评审和实现票使用。返修范围见 §1.4。**

本文是 Timeline 附件服务与 Timeline MCP BFF 的唯一边界草案。它补充现有文字记录闭环，不修改 KON-99 已接受的记录实现，也不在本票启动附件或 MCP 实现。

## 决策摘要

1. `Entry` 与 `MediaObject` 是两个独立的账号拥有对象。一个 Entry 可有零个或多个 MediaObject；MediaObject 只能通过 `entry_id` 关联一个 Entry，不能跨账号挂载。附件摘要可以随 Entry 返回，但附件上传生命周期由独立的 MediaService 管理。
2. MediaObject 采用 `pending`、`ready`、`deleting` 三态。预签名上传只创建 `pending` 元数据和临时对象；浏览器直传成功后必须由客户端调用 CompleteUpload，服务端校验对象后才变为 `ready`。超时、校验失败和删除都进入可重试 cleanup queue。
3. 对象 key 永远由服务端生成，使用随机 opaque ID 和固定命名空间；key 不包含 user ID、Entry ID、文件名、地区、旅行或客户端路径。对外只返回不含内部 ID、PAT 或对象 key 的媒体摘要和短期下载 URL。
4. 用户身份只来自内部会话或内部服务令牌。任何 `user_id`、账号 UUID、PAT、对象 key 或数据库主键都不是浏览器、MCP 客户端或请求消息的替代身份输入。所有 Entry、MediaObject 和 cleanup 查询、更新、删除都带服务端归属边界。
5. v1 附件 API 使用现有 storage 抽象，不携带 tripmap 的 region/trip 语义。Upload 的幂等键是 `(owner, upload_id)`；Complete 和删除使用 mutation_id，并绑定请求摘要、归属和目标对象，重放只能返回原结果。
6. Timeline MCP BFF 是独立的 Streamable HTTP 入口。它只把普通账号会话映射成内部身份，管理令牌只接受固定 audience 和 scope，并且不把 MCP 令牌、账号 ID 或 PAT 传给 Timeline 服务。v1 MCP 工具覆盖 `list`、`search`、`create`、`update`、`trash`、`restore`、`purge`；附件工具延期到附件服务完成后再单独加 scope 和契约。
7. 所有有副作用的 MCP 工具要求 `mutation_id`；更新、回收站、恢复和永久删除还要求 `expected_revision`。账号被禁用、会话 `auth_version` 失效、令牌被撤销或 revision 冲突时拒绝操作，不进行隐式重试。
8. MediaObject 有服务端维护的单调 `revision`。每次状态转移递增 1，任何重放或对已 `deleting` 对象的重复删除不递增；Prepare、Complete、List、Get、Delete 对同一对象状态返回同一 revision，`DeleteMedia` 返回删除回执而不是空响应（§3.4）。
9. 对外唯一的对象身份是服务端存储的随机 `entry_ref` / `media_ref`（`timeline_entries.public_ref`、`timeline_media_objects.public_ref`），由 TimelineService 在 owner 范围内解析。MCP 只接受 ref，不接受内部 `id`/UUID，响应不回显内部 ID（§2.4、§6.2.1）。
10. 记录写操作（create/update/trash/restore/purge）与 Media 删除（media_delete）的幂等回执由 **TimelineService / MediaService** 与业务写入同事务持久化到共用表 `timeline_mutations`（`target_kind` 区分 `entry`/`media`），以 `(user_id, mutation_id)` 唯一，绑定 operation、目标 ref 与请求摘要，并必填不可变结果快照；重放返回原结果且不递增 revision，Media 行被 cleanup 删除后仍可重放（§2.5、§3.5）。实现缺口见 §1.4，未闭环前不得开放 MCP 记录工具。

## 1. 适用范围与现状基线

### 1.1 本文覆盖

- Timeline Entry 与 MediaObject 的数据关系和生命周期。
- 文件元数据、内容类型、大小、图片尺寸和文件名的边界。
- PrepareUpload → 浏览器直传 → CompleteUpload → List/Read/Delete 的服务契约。
- 对象命名空间、缩略图、软删除、永久删除和 cleanup queue。
- 账号隔离、注销、响应脱敏和内部服务认证。
- `bff.timeline.v1.MediaService` 的 proto 草案、revision 语义、删除回执、错误码和幂等行为。
- Timeline MCP BFF 的路由、传输、令牌、普通账号映射、来源白名单、Bearer、限流、审计、账号状态、`auth_version`、`expected_revision` 和 `mutation_id`。
- 对外 opaque 引用的存储、解析、归属校验与 MCP 脱敏边界。
- 记录写操作与 Media 删除的持久幂等回执、请求摘要、不可变结果快照与重放结果（§2.5、§3.5）。
- 状态转换、权限矩阵、调用图、迁移/回滚/部署顺序、兼容策略和测试矩阵。

### 1.2 已接受的记录基线

KON-99 的 `bff.timeline.v1.TimelineService` 已定义文字记录的 List/Create/Update/Delete/Restore/Purge。记录身份来自内部 auth context，现有请求消息不带 user ID；Entry 有服务端 `revision`，删除默认进入回收站，永久删除通过明确的 purge 操作完成。现有文字记录的日期、IANA 时区、金额最小单位、书签 URL 校验和稳定排序继续有效。

**已接受实现中只有 Create 具备可用的 mutation 去重**，且是直接查询 `timeline_entries.mutation_id`，没有 `request_hash`、没有 operation、没有结果快照；Update/Delete/Restore/Purge 不消费 `mutation_id`。因此本文 §2.5 冻结的是**目标契约**，不是对现状的背书；现状与目标之间的差距逐条登记在 §1.4，必须由实现票闭环后才能宣布记录写操作幂等。

条目身份同样处于过渡状态：现有 `Entry.id` 是内部数据库 UUID，直接出现在 `bff.timeline.v1` 响应中，`internal/timeline/application/timeline.go` 按该 ID 查询；本文 §2.4 冻结的 `entry_ref` 是新的对外引用列，两者不能混用。

当前 `timeline_media_objects` 和 cleanup 表属于早期骨架，不能被视为附件服务的完整契约：后续实现必须补齐状态、revision、文件元数据、上传预留、过期时间、尺寸和清理事务语义。本文中的字段名和状态是后续 migration/proto 实现的目标。

### 1.3 非目标

- 不引入 tripmap 的 `region_code`、`trip_id`、共享旅行成员或地图语义。
- 不让 Timeline 抓取书签 URL，也不为附件做病毒扫描、OCR 或内容搜索；这些属于另行评审的异步能力。
- 不让浏览器或 MCP 直接访问数据库、对象存储管理 API 或内部 gRPC listener。
- 不把附件上传塞进现有文字记录 Create/Update 的事务；附件完成后通过独立服务关联 Entry。

### 1.4 返修记录与实现差距登记

本版按 KON-105 独立 Review 的两轮返修补全契约：第一轮（review `01a1b67d`，基于 `12f23ad` / delivery head `c7a0b23`）提出 §1.4 表一的三项；第二轮（review `01a1b6a2`，基于 `cb5787e`）要求补齐 Media 删除回执持久化与记录重放快照，见 §1.4 表二。每一项都同时给出契约位置与可验证断言：

| Review 发现（第一轮） | 本文契约 | 可验证断言 |
| --- | --- | --- |
| ① MediaService 的 `expected_revision` 无法由调用方构造（`Media` 与 Delete 响应无 revision） | §2.2 `revision` 字段、§2.3 转移规则、§3.2 `Media.revision = 13` 与 `DeleteMediaResponse`、§3.4 revision 表、§3.5 判定顺序 | §10 “Media revision”：Prepare=1、Complete=2、Delete=3，List/Get/Complete/Delete 一致；重放与重复 Delete 不递增且 `replayed = true`；stale `expected_revision` 返回 `MEDIA_REVISION_CONFLICT` 且无副作用 |
| ② opaque `entry_ref` 与现有 RPC `id` 无映射/存储/归属/脱敏契约 | §2.4 存储列、生成格式、解析入口、归属与旧数据回填；§6.2.1 MCP 输入 schema 与出站脱敏；§9.3 兼容窗口 | §10 “引用映射”：非法格式不访问数据库；UUID 冒充 ref 被 schema 拒绝；他人/已 purge 的 ref 统一 NOT_FOUND；回填后 ref 唯一且稳定；MCP 响应不含 `id`/对象 key |
| ③ Update/Trash/Restore/Purge 未持久幂等，重试可重复副作用或递增 revision | §2.5 回执表、请求摘要、事务与判定顺序、响应契约与保留期 | §10 “回执幂等”：五个写操作各自超时重试逐字段等于首次结果且 revision 不递增；同 mutation_id 不同 payload 返回 `MUTATION_REPLAY_MISMATCH`；并发双写只产生一次副作用；purge 重放不返回 NOT_FOUND |

| Review 发现（第二轮，基于 `cb5787e`） | 本文契约 | 可验证断言 |
| --- | --- | --- |
| ④ Media 删除回执无可执行持久化契约：未定义 owner/media_ref/operation/request_hash/result_revision/tombstone/保留期/唯一键与同事务关系；调用图让 Media 指向只定义记录操作的 `timeline_mutations`；判定顺序要先确认行存在，而行会被 cleanup 清理，因此 cleanup 后无法重放 | §2.5 共用表的 `target_kind`/`media_ref`/必填快照与 CHECK；§3.5 字段表、保留期与“回执不随行删除”、6 步判定顺序（回执先于可见性）；§7 调用图标注两类回执；§10 “Media 删除回执” | §10 “Media 删除回执”：首次 Delete 后 `worker` 删对象并清行，同 mutation_id 重放仍返回逐字段相等的 tombstone 与 `result_revision`、`replayed = true`，且不访问行/storage；不同 mutation_id 且行已清理 → `MEDIA_NOT_FOUND`；回执与状态变更同事务（回执写失败则状态回滚） |
| ⑤ 记录重放响应自相矛盾：`result_snapshot` 标为“可选”但 update/trash/restore 要求逐字段重放；`MutationReceipt.operation` 枚举漏掉 create，而 create 也走同一张表 | §2.5 `result_snapshot` 改为 `NOT NULL` + `snapshot_version` + 不可变（无 UPDATE 权限）+ 每个 operation 的响应/快照表；`operation` 改为闭集并含 `create`/`media_delete`，`MutationReceipt` 增 `target_kind`；§10 “回执幂等”增 create 与快照不可变断言 | §10 “回执幂等”：create 重放返回同一 `entry_ref` 与 `revision = 1` 且不新建行；五个记录操作各自“首次响应用 = 重放响应”字节级/字段级相等，即使行随后被 update/trash 修改；`receipt.operation` 在 create 上为 `create`、在 DeleteMedia 上为 `media_delete` |

以下缺口属于**实现未闭环**，不因本文冻结而消失；每项必须在对应实现票完成并通过 §10 的发布阻断项后，才能开放 MCP 记录工具或附件入口。

| 缺口 | 当前实现事实 | 闭环要求 |
| --- | --- | --- |
| Update 不消费 mutation_id | `internal/timeline/application/timeline.go` 的 UpdateEntry 只把 mutation_id 写入行内，不查询旧回执 | 改用 `timeline_mutations` 并按 §2.5 判定顺序执行 |
| Delete/Restore/Purge 不消费 mutation_id；`DeleteEntry` 的 `permanent = true` 另有一条绕过 purge 语义的物理删除路径 | 三个方法完全忽略请求中的 mutation_id；超时重试会再次写入并递增 revision，purge 重放返回 NOT_FOUND | 同上；purge 重放必须返回回执结果而不是 NOT_FOUND；`permanent` 按 §2.5 废弃 |
| Create 回执不符合 §2.5 | 以 `entry.mutation_id` 查询旧行；无 operation/request_hash/结果快照，不同 payload 复用同一 mutation_id 会静默返回旧记录 | 迁移到 `timeline_mutations`，保留行级兼容读取直到迁移完成 |
| Media 无 revision | `timeline_media_objects` 没有 revision 与状态列，Delete 返回 Empty | 按 §2.2/§3.2 增加列、响应字段与 §3.4 revision 表 |
| Media 无删除回执 | Delete 忽略 mutation_id，也没有任何回执存储；MediaObject 行与对象被 cleanup 清理后无可重放的 tombstone | 按 §2.5/§3.5 在 `timeline_mutations` 写入 `target_kind = media` 的不可变 tombstone 快照，并使回执命中先于对象可见性 |
| 回执快照与 operation 契约不完整（文档层面已于本轮修正） | 旧版把 `result_snapshot` 写成可空、`operation` 枚举漏掉 `create`；若照此实现，update/trash/restore/create 的重放会重新读行或缺失快照 | 实现时 `result_snapshot NOT NULL`、`snapshot_version = 1`、应用账号无 `UPDATE` 权限；`operation` 取闭集且含 `create`/`media_delete` |
| Entry 无对外 opaque 引用 | `timeline_entries.id`（UUID）直接出现在 BFF 响应并被 MCP 复用 | 按 §2.4 增加 `public_ref` 回填、唯一索引与 MCP 输入输出约束 |

## 2. 领域模型与不变量

### 2.1 关系

```text
Account (usersrv 的内部账号)
  └── 0..N Entry
        └── 0..N MediaObject

MediaObject --(对象 key, 不含账号身份)--> Storage object
MediaObject --(opaque object key)--> CleanupObject 0..N（幂等合并）
```

- Entry 的 owner 是内部不可变账号身份；MediaObject 的 owner 必须与 Entry owner 相同。
- MediaObject 的 `entry_id` 是内部关联字段，外部响应只返回存储的 opaque `media_ref`（`timeline_media_objects.public_ref`），不返回数据库主键。
- 一个 MediaObject 只有一个原始文件和至多一个服务端生成的缩略图。缩略图不是可独立上传的 MediaObject。
- Entry 删除进入回收站时，附件仍保留并可读；Entry 永久删除时，关联 MediaObject 一并标为 `deleting`，对象 key 进入 cleanup queue，数据库关系在事务中删除或转入不可恢复清理记录。
- cleanup queue 只存随机对象 key、状态、尝试次数和时间，不存 user ID、Entry ID、文件名或 PAT。

### 2.2 MediaObject 字段

| 字段 | 规则 |
| --- | --- |
| `media_ref` | 服务端生成并存储的对外 opaque 引用（`timeline_media_objects.public_ref`，见 §2.4）；不可当作身份或存储 key；不回显内部 UUID |
| `entry_id` | 内部字段；创建和查询必须按当前 owner 约束；不接受跨账号引用 |
| `state` | `pending`、`ready`、`deleting`；禁止客户端写任意状态 |
| `revision` | 服务端维护的单调版本号，从 1 开始；每次状态转移递增 1，重放不递增；Prepare/Complete/List/Get/Delete 对同一状态返回同一值（§3.4） |
| `filename` | 只保留用于展示的 Unicode 文件名；规范化为 basename，去除控制字符、NUL、路径分隔符和双向控制符；最多 255 个 Unicode code point，响应可按 UTF-8 截断 |
| `content_type` | 服务端根据白名单和上传头/魔数判定；客户端声明只用于预检；v1 允许 `image/jpeg`、`image/png`、`image/webp`、`application/pdf` |
| `size_bytes` | 服务端 Complete 时从对象元数据读取；必须大于 0；图片最大 25 MiB，PDF 最大 50 MiB；总单个附件上限 50 MiB |
| `width` / `height` | 仅图片在 Complete 时由服务端解码得到，范围 1–12000；非图片为 null；不能由客户端伪造 |
| `object_key` | 内部 opaque key；只写入存储适配器和 cleanup queue，绝不进入 API、日志、审计或 MCP 输出 |
| `thumbnail_key` | 内部 opaque key；仅图片 ready 后生成；生成失败不阻塞原图 ready，但摘要标记无缩略图并进入重试 |
| `upload_id` | 客户端生成的幂等键，最多 128 个 ASCII 字符；只在服务端内部保存摘要或受保护值，不回显原值 |
| `request_hash` | 对规范化的 owner、entry、文件名、声明 MIME、大小、尺寸限制和 upload_id 计算的服务端哈希；重放时必须一致 |
| `upload_expires_at` | pending 直传凭证过期时间，默认 15 分钟，最长 1 小时；过期后 Complete 返回 `UPLOAD_EXPIRED` |
| `created_at` / `updated_at` | 服务端时间戳；不接受客户端覆盖 |
| `deleted_at` | deleting 完成或数据库关系删除的内部时间；不向普通用户返回 |

服务端必须再次检查实际对象的大小、MIME、图片解码结果和允许的格式。扩展名不是类型判定依据，客户端 `content_type` 不是可信事实。

### 2.3 状态机

```text
                 PrepareUpload
                      │
                      ▼
                   pending ─────── upload expires / validation fails ─────▶ deleting
                      │                                                        │
       CompleteUpload │                                                        │ cleanup retry
       + object check │                                                        ▼
                      ▼                                                     removed
                    ready ─────────────── Delete ───────────────────────────▶ deleting
                      │
                      └──────── Entry purge/account deletion ───────────────▶ deleting
```

- `pending → ready` 只能由 CompleteUpload 在同一 owner 和 upload_id 的预留记录上执行。
- `pending → deleting` 由过期、拒绝、取消或账号删除触发。
- `ready → deleting` 由用户删除、Entry purge 或账号注销触发。
- 每次状态转移在同一事务内把 `revision` 加 1；重放、重复 Delete 和 cleanup worker 的对象删除都不改变 `revision`（§3.4）。
- `deleting` 只允许 cleanup worker 重试；客户端对已 `deleting` 对象的重复 Delete 返回同一 tombstone 摘要（`state = deleting`、原 revision、`replayed = true`），不返回歧义的 `NOT_FOUND`（§3.4）。
- cleanup 成功后删除对象和缩略图（若有），再标记 queue done；对象不存在视为成功。数据库状态更新失败可安全重试。
- 所有转移写入审计事件，但审计只允许 opaque media_ref、结果类别、时间和 actor 类别，不含 key、内容、文件名全文或令牌。

### 2.4 对外 opaque 引用与内部 ID 映射

对外可见的对象身份只有一个：服务端生成并存储的随机引用。内部 UUID、数据库主键和对象 key 不进入外部响应、MCP 工具结果、错误文本、日志或审计。

| 项 | 契约 |
| --- | --- |
| 存储位置 | `timeline_entries.public_ref`、`timeline_media_objects.public_ref`（Ent 字段 `public_ref`），各自 `UNIQUE`，回填完成后为 `NOT NULL`；不另建映射表，避免双写与归属漂移 |
| 生成 | 创建对象时用 `crypto/rand` 生成 128 bit 随机值，按 Crockford base32（去 I/L/O/U）小写编码；Entry 前缀 `ent_`、Media 前缀 `med_`，固定 30 字符（4 前缀 + 26 数据位） |
| 不可推导 | 不哈希、不编码、不关联内部 UUID 或 user ID；ref 不是授权凭证，不能单独用于访问 |
| 稳定性 | ref 在 trash、restore、revision 变化、附件增删和浏览器/MCP 读取之间保持不变；purge 后不回收、不复用 |
| 格式校验 | 服务端先按 `^(ent\|med)_[0-9a-hjkmnp-tv-z]{26}$` 校验，再做归属查询；格式不合法返回 `INVALID_REFERENCE`，不访问数据库 |
| 解析 | 唯一入口 `resolveEntryRef` / `resolveMediaRef`：`WHERE public_ref = ? AND user_id = <authenticated owner>`；他人 ref 与不存在返回同一个 `ENTRY_NOT_FOUND` / `MEDIA_NOT_FOUND` |
| 归属 | 解析、关联、更新和删除的 SQL where 都包含 `user_id`；Media 额外校验 `entry.owner = media.owner = current owner`，禁止跨账号挂载 |
| 旧数据 | 迁移先为已有行回填 ref 并建唯一索引，再置为 `NOT NULL`；回填冲突阻止发布，不允许留空值或临时 UUID 分支 |

引用与内部 ID 的对应关系（`bff.timeline.v1` 增量，字段号不得复用）：

- `Entry` 新增 `string entry_ref = 18;`，所有响应始终填充；`id = 1` 标记为内部字段、对外弃用。
- 定位请求新增 ref 字段并保留旧 `id`：`DeleteEntryRequest.entry_ref = 5`、`RestoreEntryRequest.entry_ref = 4`、`PurgeEntryRequest.entry_ref = 4`；`UpdateEntryRequest` 的 ref 放在嵌套 `Entry.entry_ref`。
- 解析优先级：只带 `entry_ref` → 按 ref 解析；只带旧 `id` → 仅浏览器 BFF 过渡窗口允许，MCP 一律拒绝；两者都带且指向不同对象 → `INVALID_ARGUMENT`，不得静默以 ref（或 id）为准。
- `ListEntriesResponse.next_page_token` 是不透明签名串，内部只含 `(occurred_at, public_ref)`，不含内部 ID 或数据库 cursor。

未采用“MCP BFF 维护独立 ref→id 映射表”的方案：BFF 无状态且没有数据库，独立映射会引入第二份归属真相和额外清理责任；ref 作为对象属性存放在对象行上，解析与归属校验可在一次索引查询内完成。

### 2.5 写操作的持久幂等回执（记录与附件删除）

结论：幂等回执由 **TimelineService / MediaService** 与业务写入在同一数据库事务内持久化，**不由 BFF 或 MCP BFF 缓存**。理由：BFF 无状态且多副本，进程内缓存无法覆盖网络重试、超时重试和跨副本重试；只有同库同事务的回执才能保证“副作用 + 回执”要么都生效、要么都不生效。

幂等回执存储于新表 `timeline_mutations`。记录写入与附件删除**共用同一张表**，用 `target_kind` 区分，避免两套判定顺序和两套重放语义（记录的 `target_kind = entry`，Media 删除的 `target_kind = media`，后者的完整契约见 §3.5）：

| 列 | 规则 |
| --- | --- |
| `id` | 内部主键；不对外，也不进入分页 token |
| `user_id` | 回执归属 owner；来自内部 auth context，不接受请求参数 |
| `mutation_id` | 客户端提供的幂等键，8–128 个 ASCII `[A-Za-z0-9._:-]`；与 owner 组成唯一键 |
| `target_kind` | `entry`、`media`；与 `operation` 组合校验（见下表） |
| `operation` | 闭集：`create`、`update`、`trash`、`restore`、`purge`、`media_delete` |
| `request_hash` | 规范请求摘要，SHA-256 hex；规范化字段集合见下 |
| `entry_ref` | `target_kind = entry` 时必填；purge 后仍保留供重放返回 |
| `media_ref` | `target_kind = media` 时必填；Media 行被 cleanup 删除后仍保留供重放返回 |
| `result_revision` | 该次写入产生的 revision；重放原样返回 |
| `result_state` | `active`、`trashed`、`purged`、`deleting` |
| `result_snapshot` | **必填（`NOT NULL`）**：首次执行时返回给客户端的完整不可变结果，重放逐字段原样返回；编码与内容见下 |
| `snapshot_version` | 当前为 `1`；新增字段时升版，重放按写入时的版本解码，旧回执不被新代码重新序列化 |
| `created_at` | 服务端时间；回执不可修改，只随账号注销删除 |

约束：

- 唯一索引 `(user_id, mutation_id)`；mutation_id 按 owner 命名空间解释，两个账号可以使用相同字符串。
- `CHECK ((target_kind = 'entry' AND entry_ref IS NOT NULL AND media_ref IS NULL) OR (target_kind = 'media' AND media_ref IS NOT NULL AND entry_ref IS NULL))`，避免出现既有 entry_ref 又有 media_ref 或两者都为空的回执行。
- `CHECK (operation IN ('create','update','trash','restore','purge')) = (target_kind = 'entry')`，即 `media_delete` 只能配 `media`；`target_kind = media` 时 `operation` 只能是 `media_delete`。
- 回执只 INSERT、不 UPDATE：应用角色对该表只有 `INSERT`/`SELECT` 权限（无 `UPDATE`/`DELETE`，注销与保留期清理走单独角色），从数据库层保证快照不可变。
- `result_snapshot` 大小由 Entry/Media 字段上限自然约束（Entry 完整响应或 Media tombstone 的规范 JSON），不得截断、不得只存差异；实现可压缩存储。

`result_snapshot` 是“重放即原样返回”的唯一来源，**不是**对业务行的再次序列化：

- 内容为首次执行时真实返回的响应负载（记录：完整 `Entry` 含 `revision`、`deleted_at`、`entry_ref`；Media：tombstone 摘要含 `media_ref`、`entry_ref`、`state`、`revision`、`filename`、`content_type`、`size_bytes`、`width`、`height`、`created_at`、`updated_at`）。
- 重放**不重新读取业务行**，因此同一 mutation_id 之后再发生 update/trash/restore/purge 也不会改变旧回执的重放结果。
- `purge` 与 `media_delete` 的终态也要快照：`purge` 存终态记录（`entry_ref`、最终 `revision`、`purged_at`，无正文），`media_delete` 存 tombstone；两者都不得留 NULL，否则行被物理删除/清理后无法重放。
- `create` 与其它写操作同表同判定顺序：首次执行时把新建的 Entry 写入快照，重放返回同一 Entry（同一 `entry_ref`、`revision = 1`、`replayed = true`），不会创建第二条记录。

操作与现有 RPC 的对应（`operation` 是回执与 MCP 的稳定名，不随 proto 消息名变化）：

| `target_kind` | `operation` | 现有 RPC | 说明 |
| --- | --- | --- | --- |
| `entry` | `create` | `CreateEntry` | 新记录，`result_state = active`，`result_revision = 1`，返回 `EntryResponse`（含 receipt） |
| `entry` | `update` | `UpdateEntry` | 只改可变字段，`result_state = active` |
| `entry` | `trash` | `DeleteEntry`（`permanent = false`） | 软删除，`result_state = trashed`，revision + 1 |
| `entry` | `restore` | `RestoreEntry` | `result_state = active`；已 active 时幂等成功且 revision 不变，但仍为该 mutation_id 写回执 |
| `entry` | `purge` | `PurgeEntry` | 物理删除，`result_state = purged`，重放完全依赖回执 |
| `media` | `media_delete` | `DeleteMedia` | 状态转 `deleting`，`result_state = deleting`，revision + 1，重放见 §3.5 |

`DeleteEntryRequest.permanent` 废弃：v1 实现票将其改为只做软删除，`permanent = true` 返回 `INVALID_ARGUMENT`，浏览器 UI 改用 `PurgeEntry`（永久删除已在 purging 权限与 MCP scope 下单独治理）。字段编号保留但标为 deprecated，v2 删除；不得为它维护第二套物理删除路径和回执语义。

`request_hash` 的输入按固定字段顺序序列化后取 SHA-256：

- 公共：owner、`operation`、目标 `entry_ref`、`expected_revision`。
- `update`：可变字段全集（title、date、time、timezone、category、location、amount_minor、currency、note、url、url_title、url_note），缺省值归一化。
- `trash` / `purge`：目标版本之外的行为开关（`permanent` 已废弃，见下）；
- `media_delete`：`target_kind`、`media_ref`、`expected_revision`、`upload_id`（若请求携带）。
- 排除：`mutation_id` 自身、传输头、trace id、请求时间等非语义字段。

事务与判定顺序（所有记录写操作共用）：

1. `INSERT ... ON CONFLICT (user_id, mutation_id) DO NOTHING` 抢占回执行。
2. 插入成功 → 在同一事务内执行领域写入并写回执结果 → commit。任何一步失败则整体回滚，回执行一并消失，客户端重试按首次执行处理。
3. 插入冲突 → 读取既有回执并比较 `operation` 与 `request_hash`：
   - 完全一致 → 返回 `result_snapshot` 解码出的原始负载并标记 `receipt.replayed = true`；**不重新读取业务行、不执行任何写入、不递增 revision、不改变 `deleted_at`**。
   - 任一不一致（含同 mutation_id 不同 payload）→ `MUTATION_REPLAY_MISMATCH`（`ALREADY_EXISTS`），无副作用。
4. 并发重试：唯一索引让第二个事务阻塞到第一个提交；提交后按步骤 3 返回。第一个事务回滚时第二个成为新的首次执行。
5. 回执判定先于对象可见性判定：`trash` 重放即使记录已不在活动列表、`purge` 重放即使行已物理删除，也必须返回原成功结果，而不是 `ENTRY_NOT_FOUND`。
6. `expected_revision` 只在首次执行（步骤 2）内校验；不匹配返回 `REVISION_CONFLICT` 且不写回执，因此客户端可以修正版本后用同一 mutation_id 重试，不会被旧回执挡住。
7. 回执表上线后，update/trash/restore/purge 的 mutation_id 必填：空值返回 `INVALID_ARGUMENT`；MCP 从第一版就强制，浏览器 BFF 由客户端生成并在重试中复用，BFF 不得多发一个 mutation_id。
8. 超时语义：服务端只有在“回执行已提交”后才返回成功；写入是否提交对客户端不可见，因此调用方必须用同一 mutation_id 重试，不能生成新键（新键可能重复递增 revision 或重复 Trash/Purge）。

响应契约（`bff.timeline.v1` 增量；本条为 proto 变更，实现票落地时重新生成客户端）：

```proto
message MutationReceipt {
  string mutation_id = 1;
  string target_kind = 2;       // entry|media
  string operation = 3;         // create|update|trash|restore|purge|media_delete
  int64 result_revision = 4;
  string result_state = 5;      // active|trashed|purged|deleting
  bool replayed = 6;            // true = 由持久回执返回
  google.protobuf.Timestamp recorded_at = 7;
}
message EntryResponse { Entry entry = 1; MutationReceipt receipt = 2; }
message DeleteEntryResponse { Entry entry = 1; MutationReceipt receipt = 2; }
message PurgeEntryResponse { MutationReceipt receipt = 1; }
```

`operation` 是闭集字符串，不再随 proto 消息名扩展；新增操作必须先修订本文。`snapshot_version` 只在存储层使用，不进入公开响应。

每个操作的响应与快照内容（重放时逐字段等于此表所述内容）：

| `operation` | 响应消息 | `result_state` / `result_revision` | `result_snapshot` 内容 |
| --- | --- | --- | --- |
| `create` | `EntryResponse` | `active` / `1` | 新建 Entry 完整响应（含 `entry_ref`、`revision = 1`） |
| `update` | `EntryResponse` | `active` / 前一值 + 1 | 更新后 Entry 完整响应 |
| `trash` | `DeleteEntryResponse` | `trashed` / 前一值 + 1 | 软删后 Entry（`deleted_at` 非空） |
| `restore` | `EntryResponse` | `active` / 前一值 + 1，或已 active 时不变 | 恢复后 Entry |
| `purge` | `PurgeEntryResponse` | `purged` / 删除前最后 revision | 终态记录（`entry_ref`、`revision`、`purged_at`，无正文），**不得为 NULL** |
| `media_delete` | `DeleteMediaResponse` | `deleting` / 前一值 + 1 | Media tombstone 完整摘要，**不得为 NULL**（§3.5） |

- `trash` 响应携带删除后的 Entry（`deleted_at` 非空、revision = N+1）与回执；重放逐字段等于首次结果，`receipt.replayed = true`。
- `restore` 响应携带恢复后的 Entry（revision = N+1）；对已 active 的记录再次 restore 是幂等成功，revision 不变。
- `purge` 响应没有 Entry，回执给出 `result_state = purged` 与最终 revision；行已删除，重放只能依赖回执。
- `create` 的响应携带新建 Entry 与回执（`operation = create`、`result_state = active`、`result_revision = 1`）；重放返回同一 Entry 与同一 `entry_ref`，不新建第二行。
- MCP 工具必须原样回传 `mutation_id` 与 `receipt.replayed`，超时后不得自行重试并生成新 mutation_id。

保留期：记录回执与 Media 删除回执统一保留 30 天（可配置、有上限）或直到账号注销；注销事务内按 owner 删除记录回执与 Media 回执。回执保留期**不依赖业务行寿命**：`purge` 物理删行、Media cleanup 删行或删对象都不删除回执，cleanup worker 也不得以任何理由写或删 `timeline_mutations`。回执过期后，幂等退化为领域状态幂等：update 依赖 `expected_revision`（过时写入返回 `REVISION_CONFLICT`，不会重复递增），trash/restore 对已处于目标状态的记录返回成功且不递增 revision，purge 与 media_delete 则无法再给出原 tombstone（分别返回 `ENTRY_NOT_FOUND` / `MEDIA_NOT_FOUND`），需由客户端保证在保留期内重试。

## 3. 内部 MediaService 契约

### 3.1 身份与调用边界

MediaService 仅接受内部服务调用。调用方必须提供由 BFF 或受信任内部服务签发的内部 bearer，并通过 `rpcauth` context 得到 owner；请求消息不含 `user_id`。账号状态和 `auth_version` 在 BFF 身份映射时检查，内部服务也必须拒绝空身份和未知账号。

现有 storage 抽象提供 `Put`、`Head`、`Delete` 和短期签名 URL 能力。MediaService 不向业务层暴露 bucket、endpoint、credential、对象 key 或 storage provider 名称。

### 3.2 `bff.timeline.v1.MediaService` proto 草案

以下是 v1 的规范字段；字段编号一经发布不得复用。`MediaRef` 是可公开的 opaque 引用，内部 service 实现可以使用数据库 UUID，但不得把它直接暴露在响应中。

```proto
syntax = "proto3";
package bff.timeline.v1;
option go_package = "github.com/kongken/mono/pkg/proto/bff/timeline/v1;bfftimelinev1";

import "google/protobuf/timestamp.proto";

service MediaService {
  rpc PrepareUpload(PrepareUploadRequest) returns (PrepareUploadResponse);
  rpc CompleteUpload(CompleteUploadRequest) returns (MediaResponse);
  rpc ListMedia(ListMediaRequest) returns (ListMediaResponse);
  rpc GetMedia(GetMediaRequest) returns (MediaResponse);
  rpc DeleteMedia(DeleteMediaRequest) returns (DeleteMediaResponse);
}

message Media {
  string media_ref = 1;
  string entry_ref = 2;
  string state = 3;                 // pending|ready|deleting
  string filename = 4;
  string content_type = 5;
  int64 size_bytes = 6;
  int32 width = 7;
  int32 height = 8;
  string download_url = 9;           // short lived, audience-limited
  string thumbnail_url = 10;         // short lived, empty when unavailable
  google.protobuf.Timestamp created_at = 11;
  google.protobuf.Timestamp updated_at = 12;
  int64 revision = 13;               // server-maintained, monotonic; see 3.4
}
message PrepareUploadRequest {
  string entry_ref = 1;
  string upload_id = 2;
  string filename = 3;
  string content_type = 4;
  int64 size_bytes = 5;
}
message PrepareUploadResponse {
  Media media = 1;
  string upload_url = 2;              // short lived signed PUT URL
  google.protobuf.Timestamp expires_at = 3;
}
message CompleteUploadRequest {
  string media_ref = 1;
  string upload_id = 2;
  string mutation_id = 3;
}
message ListMediaRequest { string entry_ref = 1; }
message ListMediaResponse { repeated Media media = 1; }
message GetMediaRequest { string media_ref = 1; }
message DeleteMediaRequest {
  string media_ref = 1;
  int64 expected_revision = 2;
  string mutation_id = 3;
}
message MediaResponse { Media media = 1; }
message DeleteMediaResponse {
  Media media = 1;                   // tombstone: state=deleting + final revision
  bool replayed = 2;                 // true when served from a stored receipt or an already-deleting row
  MutationReceipt receipt = 3;       // operation = media_delete, target_kind = media (见 3.5)
}
```

该文件是未发布的草案，没有老客户端；因此 `DeleteMedia` 的返回类型直接定为 `DeleteMediaResponse` 而不是 `Empty`，不保留空响应兼容分支。

实现可在内部 protobuf 中加入 `request_hash`、`object_version` 和 worker 字段，但这些字段不进入 BFF 或 MCP 的公开响应。`download_url` 和 `thumbnail_url` 每次读取按当前 owner 签发，TTL 不超过 5 分钟；URL 不写日志。

### 3.3 方法语义

- **PrepareUpload**：校验 Entry 属于当前 owner、文件名/MIME/大小白名单和账号配额；在数据库事务内插入 `pending` MediaObject（`revision = 1`）与 cleanup reservation，再由 storage 生成 staging key 和短期 PUT URL。相同 `(owner, upload_id)` 且规范请求哈希相同则返回原 `pending` 预留（同一 revision）；哈希不同返回 `IDEMPOTENCY_KEY_REUSED`。
- **浏览器直传**：浏览器只向签名 URL PUT，不能自选 bucket/key 或携带内部 bearer。PUT 失败由客户端重新调用 PrepareUpload 或在有效期内重试。
- **CompleteUpload**：按 owner、media_ref、upload_id 查询 `pending`；对 staging 对象执行 Head、大小/MIME/魔数/图片尺寸检查，成功后在同一事务内 `state = ready`、`revision += 1` 并安排缩略图。相同 mutation_id 和请求哈希重放返回原 ready 摘要（原 revision，`state = ready`）；状态已 ready 且 upload_id 相同则视为重复 Complete，返回当前摘要但不递增 revision。对象不存在、过期或校验失败不会变 ready。
- **ListMedia/GetMedia**：只返回当前 owner 对指定 Entry 的 ready 或仍可见 pending 摘要，并携带当前 revision；`deleting` 不出现在普通列表（Get 对 `deleting` 返回 `MEDIA_NOT_FOUND`）。下载 URL 是读取时签发的短期 URL，服务端先再次检查 owner 和 Entry；同一状态下的多次读取返回同一 revision。
- **DeleteMedia**：必须按 owner 查询，返回 tombstone 与 `MutationReceipt`（`operation = media_delete`、`target_kind = media`，完整契约见 §3.5）。事务内将 ready/pending 改为 deleting、revision + 1、写 cleanup queue、写回执并提交；worker 删除原图和缩略图，Delete 调用本身不等待对象存储完成。重复 Delete 与重放均不递增 revision。

### 3.4 revision 语义

`Media.revision` 是**可读、可比较、单调递增**的版本号，由服务端在同一事务内与状态一起更新；不得用 `updated_at`、对象 HEAD 或对象版本推导。

| 事件 | revision | 返回位置 |
| --- | --- | --- |
| PrepareUpload 创建 pending | `1` | `PrepareUploadResponse.media.revision` |
| CompleteUpload pending → ready | 前一值 + 1 | `MediaResponse.media.revision` |
| DeleteMedia ready/pending → deleting | 前一值 + 1 | `DeleteMediaResponse.media.revision` |
| 同一 mutation_id / 同一 upload_id 的重放 | 不变 | 响应携带回执中的 revision |
| 对已 deleting 对象的重复 Delete | 不变 | tombstone 摘要 + `replayed = true` |
| cleanup worker 删除对象／清理 MediaObject 行 | 不变 | 不回传；只能通过 §3.5 的回执重放获得 |
| Entry purge / 账号注销批量转 deleting | 各对象 + 1 | 不计入用户响应 |

该表是 revision 的唯一真相：`DeleteMedia` 返回的 tombstone 必须带本次转移后的 `revision`，与后续 `GetMedia`（对象已不可见）和 `ListMedia`（已移除）不冲突；不再可观察的对象只能通过回执重放（§3.5）获得原 revision。

### 3.5 Media 删除回执的持久化与重放

Media 删除与记录写入共用 `timeline_mutations`（§2.5），但有自己的 `target_kind`/`operation` 和判定顺序；本节覆盖 Reviewer 指出的“cleanup 后无法重放”缺口。

存储字段（均为首次执行时写入，之后不可修改）：

| 字段 | Media 删除时的取值 |
| --- | --- |
| `user_id` | 当前 owner（来自内部 auth context） |
| `target_kind` | `media` |
| `operation` | `media_delete` |
| `media_ref` | 被删除对象的 `public_ref`；行被 cleanup 删除后仍保留 |
| `entry_ref` | 所属 Entry 的 `public_ref`，便于审计与后续关联；不参与定位 |
| `request_hash` | `owner + target_kind + operation + media_ref + expected_revision + upload_id（若提供）` 的 SHA-256 |
| `result_revision` | 首次执行后对象的 `revision` |
| `result_state` | `deleting` |
| `result_snapshot` | 首次执行返回的 tombstone 完整摘要（§2.5 字段集），**必填** |
| `created_at` | 首次执行的服务端时间 |

唯一键 `(user_id, mutation_id)` 与记录回执共用；`media_ref` 不唯一，因为同一对象可以被不同 mutation_id 重复删除（第二次起走“已 deleting”幂等分支）。保留期为 30 天（可配置、有上限）或直到账号注销，且**与 MediaObject 行、原图对象、缩略图对象的寿命无关**：

- cleanup worker 只能删除对象和 MediaObject 行（或将其转为不可恢复的清理记录），**一律不写、不删 `timeline_mutations`**。
- MediaObject 行被清理后，同一 mutation_id 的重放仍从回执返回原 tombstone 与 `result_revision`，不查询行、不访问 storage。
- 回执过期后，同一 mutation_id 的重放退化为当前可见性判定（行已清理 → `MEDIA_NOT_FOUND`）；客户端必须在保留期内重试，不得自行生成新 mutation_id 来“修复”超时。

`DeleteMedia` 的判定顺序固定如下（回执命中先于对象可见性）：

1. 只做无 DB 校验：`media_ref` 按 `^med_[0-9a-hjkmnp-tv-z]{26}$` 校验、`mutation_id` 非空且在字符集内；不合法 → `INVALID_REFERENCE` / `INVALID_ARGUMENT`。
2. 按 `(user_id = current owner, mutation_id)` 查回执（**不先查 MediaObject**）。命中：`target_kind = media`、`operation = media_delete`、`media_ref` 与请求一致、`request_hash` 一致 → 返回 `result_snapshot` 解码的 tombstone + `result_revision` + `replayed = true`；**不校验 `expected_revision`、不写库、不访问 storage**，因此 cleanup 完成后重放仍然成功。任一项不一致 → `MUTATION_REPLAY_MISMATCH`。
3. 回执未命中 → 按 `(public_ref = media_ref, user_id = owner)` 查 MediaObject：不存在 → `MEDIA_NOT_FOUND`（不区分“不存在”与“不属于你”），不区分“从未存在”和“已 cleanup 且回执过期”。
4. 行已为 `deleting` → 幂等 tombstone：由当前行构造摘要（`revision` 不变、`replayed = true`），并为该 mutation_id 写入回执（无状态变更），使后续重放不再依赖该行。本步先于步骤 5 是有意的：一旦进入 `deleting`，就不存在可冲突的后续状态转移，用陈旧 `expected_revision` 重试也应得到幂等成功而不是冲突。
5. `expected_revision != 0` 且不等于当前 `revision` → `MEDIA_REVISION_CONFLICT`（`ABORTED`），无副作用、不写回执。
6. 首次执行事务：`revision += 1`、`state = deleting`、写 cleanup queue、`INSERT` 回执（含 `result_revision` 与 tombstone 快照）→ commit；worker 异步删除对象。任一步失败整体回滚，回执行一并消失，客户端可用同一 mutation_id 重试。

实现映射：上述步骤 2–3 落库时就是 §2.5 的 `INSERT ... ON CONFLICT (user_id, mutation_id) DO NOTHING`——冲突的插入不修改任何行，随后读取既有回执返回；“回执先于对象可见性”的语义不变，也不会在并发首写时把行当成“已存在对象”误判。

`replayed = true` 的语义是“本次没有产生新的状态转移”，它既包括步骤 2 的回执命中，也包括步骤 4 的已 deleting 幂等分支；客户端不得据此推断原调用方的身份或请求内容。

`expected_revision = 0` 表示“无条件删除”，仅供浏览器 BFF 的显式删除按钮使用；MCP 与任何自动化调用必须传非零值。无条件删除仍不跳过步骤 2、4，因此重试不会产生第二次状态转移。客户端构造 `expected_revision` 的唯一合法来源是 Prepare/Complete/List/Get 返回的 `media.revision`；不得自行累计或猜测。

Media 删除不经 MCP 暴露（附件工具延期，§6.3），因此 `media_delete` 不在 MCP 工具错误映射表中，也不增加 MCP scope。

## 4. 错误码与安全响应

内部 gRPC 使用标准 status code，并在 error details 中带稳定的 `reason`；响应文本不包含 user ID、Entry ID、MediaObject UUID、对象 key、bucket、PAT 或 storage 错误原文。

| reason | gRPC code | 语义 |
| --- | --- | --- |
| `AUTH_REQUIRED` | `UNAUTHENTICATED` | 没有有效内部身份 |
| `ACCOUNT_DISABLED` | `PERMISSION_DENIED` | 账号不可用 |
| `AUTH_VERSION_STALE` | `UNAUTHENTICATED` | 会话/令牌版本已失效 |
| `ENTRY_NOT_FOUND` | `NOT_FOUND` | Entry 不属于当前 owner 或不存在；统一响应避免枚举 |
| `MEDIA_NOT_FOUND` | `NOT_FOUND` | Media 不属于当前 owner 或不存在 |
| `INVALID_FILENAME` / `INVALID_CONTENT_TYPE` / `SIZE_LIMIT_EXCEEDED` | `INVALID_ARGUMENT` | 元数据或限制失败 |
| `QUOTA_EXCEEDED` | `RESOURCE_EXHAUSTED` | 账号或 Entry 配额已满 |
| `IDEMPOTENCY_KEY_REUSED` | `ALREADY_EXISTS` | upload_id/mutation_id 绑定了不同请求 |
| `UPLOAD_EXPIRED` | `FAILED_PRECONDITION` | 预签名上传已过期 |
| `UPLOAD_NOT_FOUND` | `FAILED_PRECONDITION` | staging 对象不存在 |
| `UPLOAD_CONTENT_MISMATCH` | `INVALID_ARGUMENT` | 实际对象与声明不符 |
| `REVISION_CONFLICT` | `ABORTED` | 记录写入的 expected_revision 不匹配 |
| `MEDIA_REVISION_CONFLICT` | `ABORTED` | DeleteMedia 的 expected_revision 不匹配且无法归约为幂等删除（§3.4 步骤 4） |
| `MUTATION_REPLAY_MISMATCH` | `ALREADY_EXISTS` | mutation_id 的请求摘要不同 |
| `INVALID_REFERENCE` | `INVALID_ARGUMENT` | `entry_ref`/`media_ref` 格式非法，或同时提供了指向不同对象的 id 与 ref；不访问数据库 |
| `STORAGE_UNAVAILABLE` | `UNAVAILABLE` | 可重试的 storage 故障，不暴露供应商错误 |
| `INTERNAL` | `INTERNAL` | 统一内部错误；日志保留关联 ID |

“找不到”与“没有权限”对外统一为同类 `NOT_FOUND`，避免跨账号猜测；内部安全审计可记录更细原因，但不得回送调用方。

## 5. 账号隔离、注销与隐私

### 5.1 归属边界

- Entry 查询始终包含 `user_id = authenticated owner`；Media 查询始终同时包含 owner 和 Entry/Media 关系。
- `entry_ref`、`media_ref`、mutation_id 和 upload_id 只能在当前 owner 命名空间解释；随机引用不是授权凭证。
- 引用解析必须走 §2.4 的统一入口，先做格式校验、再做 `public_ref + user_id` 查询；不得在 BFF、MCP BFF 或前端拼装或反推内部 ID。
- 返回给外部的 Entry 只允许携带 `entry_ref`；内部 `id` 仅用于服务内部调用和过渡期浏览器兼容，`id` 不得进入 MCP 工具结果或错误体。
- Update/Delete/Purge 先做归属查询再做 mutation；SQL delete/update 的 where 条件再次包含 owner，避免 TOCTOU 或错误调用方造成越权。
- 幂等回执按 `(user_id, mutation_id)` 读写，回执中的 `entry_ref`/快照不跨账号复用；不得用别人的 mutation_id 查询或重放。
- cleanup worker 只消费 opaque object key。管理重试按 queue row 授权，不能用用户传入的 key；对象存储删除使用固定 bucket/prefix allowlist。
- 任何 response mapper 都使用显式白名单。不得直接序列化 Ent、storage SDK、auth context 或 error 对象。

### 5.2 注销

注销工作流先让账号系统使账号和 `auth_version` 失效，再调用 Timeline cleanup。Timeline 事务删除/标记该 owner 的 Entry、MediaObject，并在同一事务内按 owner 删除 `timeline_mutations` 中的记录回执与 Media 删除回执（`target_kind` 两类都删），再将每个原图和缩略图 key 作为独立 cleanup item 写入队列；队列不保存 user ID。即使账号已不存在，cleanup 仍可重试。注销响应只返回既有 opaque receipt，不包含 Timeline 内部 ID 或对象 key。

注销期间新的 Prepare/Complete/List/Read/Delete 均拒绝；已签发但未使用的浏览器 URL 因 owner 状态或短 TTL 失效。浏览器直传对象即使已经写入，也由 pending reservation 的过期 cleanup 兜底。注销完成后 `public_ref` 不得重新分配给新账号；因为 ref 是随机生成且随行删除，不存在回收池，新账号自然得到不同 ref。

## 6. Timeline MCP BFF 边界

### 6.1 路由与传输

- 路由固定为 `POST /api/mcp/timeline/v1`，只接受 MCP Streamable HTTP 的 JSON 请求和可选 SSE 响应；不提供 WebSocket、GET 管理接口或任意 gRPC 转发。
- `Authorization: Bearer <token>` 是唯一认证头。Cookie、query token、body token 和自定义 user ID 均拒绝。
- 来源白名单只允许配置的 MCP client origins；无白名单配置时 endpoint 默认关闭。CORS 只开放明确 origin、`POST`、`Authorization` 和 `Content-Type`，禁止通配符凭据。
- 每个请求生成 request ID；MCP response 只带工具定义的公开字段，过滤账号 ID、PAT、对象 key、内部错误和栈信息。

### 6.2 令牌与普通账号映射

管理令牌由 usersrv/BFF 签发和撤销，BFF 只保存 token hash、`token_id`、account owner、audience、scope、issued_at、expires_at、auth_version 和 revoked_at。原始 PAT 只在创建时显示一次，不进入 Timeline 服务或日志。

校验必须同时满足：签名/格式有效、`aud = oz.mcp.timeline`、`scope` 含所需工具 scope、未过期、未撤销、账号状态 active、token `auth_version` 等于当前账号版本。普通账号映射只使用令牌中由内部签发的不可替代 account binding；客户端传入的 `user_id`、email、昵称或外部 provider subject 一律忽略或以 `INVALID_ARGUMENT` 拒绝。

推荐 scope：`timeline:read` 覆盖 list/search；`timeline:write` 覆盖 create/update；`timeline:trash` 覆盖 trash/restore；`timeline:purge` 覆盖 purge。附件 scope `timeline:media` 不在本版 MCP 工具中开放。

### 6.2.1 `entry_ref` 的输入输出边界

MCP 工具 schema 只暴露 opaque 引用，不暴露内部 ID：

- 所有工具的目标参数名为 `entry_ref`，pattern 固定 `^ent_[0-9a-hjkmnp-tv-z]{26}$`；UUID、空串、`id` 字段、URL 或数据库主键形状的输入在 schema 层直接拒绝为 `VALIDATION_FAILED`，不产生数据库查询。
- 工具 schema 不定义 `id`、`user_id`、`entry_id` 或任何 UUID 形状的字段，并设置 `additionalProperties: false`，避免客户端把内部 ID 当作替代标识。
- 分页 token 是不透明签名串，内部只含 `(occurred_at, public_ref)`；MCP BFF 不解析、不改写，直接透传给 TimelineService。
- `timeline.create` 的响应必须返回服务端生成的 `entry_ref` 与 `revision`；后续 update/trash/restore/purge 只能使用该 ref 与该 revision。
- MCP BFF 只做 ref 透传，不做 ref↔id 解析：解析与归属校验在 TimelineService 内完成（§2.4）。因此即使 TimelineService 内部返回 `Entry.id`，MCP 出站映射也必须删除。
- 响应字段白名单固定为 `entry_ref`、`revision`、业务字段和 `receipt`；`Entry.id`、MediaObject UUID、对象 key、下载 URL 一律在出站映射时丢弃。
- 错误：ref 格式错误 → `VALIDATION_FAILED`；ref 不存在或属于他人 → 统一 `NOT_FOUND`；两者都不回显内部 ID，也不区分“格式对但不存在”与“属于他人”。

### 6.3 工具契约

| 工具 | 必需 scope | 输入关键字段 | 规则 |
| --- | --- | --- | --- |
| `timeline.list` | `timeline:read` | query、category、from_date、to_date、page_size、page_token | 只返回当前账号；稳定排序和分页 token 不泄露内部 cursor |
| `timeline.search` | `timeline:read` | query、page_size、page_token | 是 list 的受限别名，不抓取 URL，不做跨账号搜索 |
| `timeline.create` | `timeline:write` | entry fields、mutation_id | 标题/日期/时区/金额/书签沿用现有校验；重复 mutation 按 §2.5 返回原回执；响应返回 `entry_ref` |
| `timeline.update` | `timeline:write` | opaque entry_ref、entry fields、expected_revision、mutation_id | revision 不符返回 `REVISION_CONFLICT`；重放不递增 revision；不能修改 owner 或内部字段 |
| `timeline.trash` | `timeline:trash` | entry_ref、expected_revision、mutation_id | 软删除；重复请求幂等（回执重放或已 trashed 返回原 revision）；不接受隐式永久删除参数 |
| `timeline.restore` | `timeline:trash` | entry_ref、expected_revision、mutation_id | 只恢复当前账号回收站记录；重放不递增 revision |
| `timeline.purge` | `timeline:purge` | entry_ref、expected_revision、mutation_id | 明确不可恢复；附件进入 cleanup queue；重放返回回执（`result_state = purged`）而不是 `NOT_FOUND` |

MCP 工具错误映射为稳定 `code`、可操作的短消息和可选 `retryable`，例如 `AUTH_EXPIRED`、`FORBIDDEN`、`NOT_FOUND`、`VALIDATION_FAILED`、`REVISION_CONFLICT`、`MUTATION_REPLAY_MISMATCH`、`RATE_LIMITED`、`SERVICE_UNAVAILABLE`。所有副作用工具都必须在输入 schema 中要求 mutation_id；BFF 不能替客户端生成并在重试中改变它。工具结果必须原样返回 `receipt.replayed`：客户端据此判断本次是否为重放，不能以 HTTP/SSE 重连次数自行推断。

附件在本版 MCP 中延期：MCP 客户端不能调用 PrepareUpload、签名 URL、CompleteUpload 或下载 URL。后续若开放，必须单独增加 `timeline:media` scope、工具输入/输出白名单、大小和内容类型限制，并复用 MediaService 的 owner 检查与 mutation 语义。

### 6.4 限流、审计与可观测性

限流按 `token_id`、账号 opaque binding 和来源 IP 三个维度组合，默认建议为每 token 每分钟 120 次读、30 次写、10 次 purge；配置必须有上限，达到限制返回 `429` 和 `Retry-After`。限流失败关闭 endpoint，不能在 Redis 不可用时无限放行。

审计记录 action、结果类别、token_id 摘要、账号 opaque binding 摘要、request ID、来源类别、时间和 error reason。禁止记录 Authorization、原始 mutation payload、Entry 内容、URL 全文、对象 key、签名 URL、user ID 和 PAT。安全事件（撤销、auth_version 失效、跨账号猜测、限流）保留足够的不可逆摘要以关联调查。

## 7. 调用图

```mermaid
title="Timeline 记录、附件与 MCP 边界"
flowchart LR
  Browser[Timeline browser] -->|session bearer| BFF[Browser BFF]
  MCP[MCP client] -->|Bearer management token| MBFF[Timeline MCP BFF\nStreamable HTTP]
  BFF -->|internal auth context| T[TimelineService]
  BFF -->|internal auth context| M[MediaService]
  MBFF -->|mapped owner + scope| T
  T --> DB[(Timeline DB\nentries + media)]
  M --> DB
  T --> R[timeline_mutations\ntarget_kind=entry]
  M --> R2[timeline_mutations\ntarget_kind=media]
  M -->|signed PUT/GET| S[(Storage abstraction)]
  Browser -->|short lived signed PUT| S
  M --> Q[Opaque cleanup queue]
  Q --> S
  Usersrv[usersrv / auth] -->|status + auth_version| BFF
  Usersrv -->|status + token validation| MBFF
```

MCP BFF 只调用 Timeline BFF 内部服务接口，不绕过 owner checks；浏览器直传只使用一次性、短期、固定 key 的签名 URL。记录回执与 Media 删除回执写在同一张表的两个 `target_kind` 分区，均与业务写入共用同一数据库和事务，BFF 不缓存回执；cleanup worker 只接触对象与 MediaObject 行，从不读写回执。

## 8. 权限矩阵

| 操作 | 普通登录会话 | MCP read | MCP write | MCP trash | MCP purge | cleanup worker | account cleanup |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 读 Entry | 自己 | 自己 | 自己 | 自己 | 自己 | 否 | 否 |
| 建/改 Entry | 自己 | 否 | 自己 | 否 | 否 | 否 | 否 |
| Prepare/Complete 上传 | 浏览器后续票 | 否 | 否 | 否 | 否 | 否 | 内部注销例外 |
| 读 Media 摘要/签名 URL | 自己 | 本版不开放 | 本版不开放 | 本版不开放 | 本版不开放 | 否 | 否 |
| 软删 Entry/Media | 自己 | 否 | 否 | 自己 | 否 | 否 | 注销范围 |
| 恢复 Entry | 自己 | 否 | 否 | 自己 | 否 | 否 | 否 |
| 永久删 Entry/Media | 需要明确 UI 操作 | 否 | 否 | 否 | 自己 | 队列对象 | 注销范围 |
| 直接按 key 删对象 | 否 | 否 | 否 | 否 | 否 | 仅 queue key | 仅 queue key |
| 按 `entry_ref`/`media_ref` 解析对象 | 自己 | 自己（MCP 传入的 ref） | 自己 | 自己 | 自己 | 否 | 否 |
| 按内部 `id` 访问（过渡兼容） | 自己 | 否 | 否 | 否 | 否 | 否 | 否 |
| 写/读 `timeline_mutations` 回执 | 自己（通过业务写入，两类 `target_kind`） | 否（MCP BFF 不缓存） | 否 | 否 | 否 | 否 | 注销时按 owner 删除 |

“自己”始终由内部会话/令牌映射的 owner 决定，不由请求参数决定。MCP 侧永远不接收内部 `id`，也不做 ref→id 解析（§6.2.1）；浏览器侧的旧 `id` 访问只在 §9.3 的兼容窗口内保留。

## 9. 迁移、发布与回滚

### 9.1 数据迁移

1. 先部署只读兼容代码和 cleanup worker，确认现有 `timeline_entries`、`timeline_media_objects`、`timeline_cleanup_objects` 可安全读取。
2. 增量增加 MediaObject 状态、`revision`、`public_ref`、文件元数据、upload reservation、过期时间、尺寸和 request hash 字段；默认值兼容现有空字段。为 `(owner, upload_id)` 建唯一索引时先清理/隔离重复非空值，不能把旧空值误视为同一上传。
3. 为 `timeline_entries` 增加 `public_ref` 并回填历史行：在单个迁移事务内生成 ref、校对 `UNIQUE`，回填失败（碰撞）则中止发布；回填完成且代码已能两种读取后再置 `NOT NULL`。
4. 建 `timeline_mutations`：`id`、`user_id`、`mutation_id`、`target_kind`、`operation`、`request_hash`、`entry_ref`、`media_ref`、`result_revision`、`result_state`、`result_snapshot NOT NULL`、`snapshot_version`、`created_at`；唯一索引 `(user_id, mutation_id)`；两条 `CHECK`（目标引用互斥、`target_kind` 与 `operation` 组合）；应用角色只授 `INSERT`/`SELECT`（无 `UPDATE`/`DELETE`），保留期清理与注销用单独角色。表建好后同一迁移窗口内为 Media 删除接入 `target_kind = media` 的回执写入，不另建表。
5. 将 Create 的行内 `mutation_id` 去重迁到 `timeline_mutations`（§2.5）。迁移窗口内 Create 先查回执表、再回退旧列读取；回执写入稳定后保留旧列作为只写备份，确认无旧版本服务后删除；迁移期间不回填历史 `timeline_entries.mutation_id` 为回执行（旧行没有 `result_snapshot`，无法构造不可变重放结果）。
6. 增加 cleanup queue 的 opaque key 约束、attempt/lease 字段和必要索引；不把 owner 或 Entry 字段加入队列。cleanup worker 不获得 `timeline_mutations` 的任何写权限。
7. 部署 MediaService 和 BFF 路由；先关闭 MCP media scope，完成对象存储 staging prefix 的生命周期规则后再允许 PrepareUpload。
8. 观测 pending 超时、Complete 失败、cleanup 成功率和跨账号拒绝后，再开放浏览器附件入口。
9. 最后按独立评审开放 MCP 记录工具；附件 MCP 另行发布。

### 9.2 回滚

- schema 回滚只允许停用新路由和新写入；保留新列和 queue 行，不能在已有数据写入后执行破坏性 down migration。
- MediaService 回滚到只读/关闭状态时，cleanup worker 必须继续运行，防止 staging 对象泄露；旧 Timeline 文字记录不受影响。
- 若 Complete 校验出现问题，关闭 PrepareUpload 和附件入口，保留 ready 数据和 cleanup；不能把 ready 强行改回 pending。
- MCP 出现安全问题时立即撤销 audience/scope 或关闭路由，递增账号 auth_version/撤销令牌；不删除 Entry 数据，不通过客户端补偿恢复。

### 9.3 兼容策略

- 现有 TimelineService 的字段编号、状态码和文字记录排序保持兼容；附件摘要新增字段只能采用新 proto 字段号和可选语义。
- 新增 `Entry.entry_ref = 18` 与各定位请求的 ref 字段属于只增不改：旧客户端继续用 `id`，响应中新字段被旧客户端忽略。
- `Entry.id` 的弃用分两个窗口：兼容窗口内仅浏览器 BFF 接受 `id`（服务端仍按 owner 解析），MCP 从第一版就只接受 `entry_ref`；窗口结束后浏览器 BFF 也拒绝 `id`，届时正文升为 v2 并重新评审。
- `DeleteEntry`/`PurgeEntry` 的返回类型从 `Empty` 改为携带 `MutationReceipt` 的响应消息，是本 ADR 明确记录的预发布 proto 变更：gRPC 线上仍然兼容（旧客户端忽略新字段），但生成代码是源码级不兼容，必须在同一实现票重新生成客户端并更新调用方；执行 `buf breaking` 时要针对该变更单独标注豁免，不能靠关闭检查通过。
- `DeleteEntryRequest.permanent` 标为 deprecated 并在 v1 实现票改为 `INVALID_ARGUMENT`，与 `DeleteEntry` 返回类型变更同批下发，浏览器 UI 同时改用 `PurgeEntry`；两者必须在同一提交内完成，不能出现 UI 已不用但服务端仍静默物理删除的窗口。
- 老客户端看不到附件时仍可正常读写文字 Entry；老客户端发来的 Entry Update 不得清空服务端已有附件。
- 记录 purge 仍是明确操作；实现附件联动时先写 cleanup reservation，再删除关系，保证旧服务和新 worker 可共存。
- MCP 工具 schema 采用版本化工具名或显式 `v1` 路由；字段只增不改含义。错误 `code` 稳定，message 可本地化。

## 10. 测试矩阵

| 类别 | 必测场景 | 通过条件 |
| --- | --- | --- |
| 身份隔离 | A 读取/改/删 B 的 Entry、Media、media_ref、entry_ref；猜测 UUID/opaque ref | 统一 NOT_FOUND；B 数据、key、ID 不泄露 |
| 认证 | 缺 bearer、错误 audience/scope、过期/撤销令牌、auth_version 失效、disabled account | 统一拒绝；不调用业务写操作 |
| 上传幂等 | Prepare 重放、相同 upload_id 不同请求、Complete 重放、网络超时后重试 | 相同请求返回同一结果；冲突返回稳定错误且无重复对象 |
| Media revision | Prepare 后读 revision=1；Complete 后=2；Delete 后=3；List/Get/Complete/Delete 在同一状态下结果一致；重放与重复 Delete 不递增 | 各路径返回的 revision 相等；重放响应 `replayed = true` 且 revision 不变；stale expected_revision 返回 `MEDIA_REVISION_CONFLICT` 且无副作用 |
| 内容校验 | 超大文件、空文件、错误 MIME、扩展名伪装、坏图片、尺寸超限、PDF 超限 | 不变为 ready；staging 最终进入 cleanup |
| 生命周期 | pending 过期、ready 删除、重复删除、Entry trash/restore/purge、缩略图失败 | 状态转换符合状态机；worker 可重试，删除幂等 |
| 引用映射 | `entry_ref` 格式非法、UUID 充当 ref、两者同时不一致、他人 ref、已 purge 的 ref 重试、旧数据回填后读取 | 格式错 `INVALID_REFERENCE` 且无 DB 查询；他人/不存在统一 NOT_FOUND；旧行回填后 ref 稳定且唯一 |
| 回执幂等 | create/update/trash/restore/purge 各自超时重试；同 mutation_id 不同 payload；两账号使用相同 mutation_id；并发双写；purge 后重试；回执过期后重试；`DeleteEntry(permanent=true)`；首次响应后行又被修改或物理删行，再用同一 mutation_id 重放 | 重放逐字段等于首次响应（取自 `result_snapshot`，不重读行）且 revision 不递增；payload 不一致返回 `MUTATION_REPLAY_MISMATCH`；并发只产生一次副作用；purge 重放返回回执而不是 NOT_FOUND；create 重放不新建行且 `operation = create`；`permanent=true` 返回 `INVALID_ARGUMENT` 且无删除 |
| Media 删除回执 | DeleteMedia 成功后 worker 删除对象并清理 MediaObject 行，再用同一 mutation_id 重放；回执过期后的新 mutation_id；同 mutation_id 不同 expected_revision/media_ref；回执 INSERT 与状态变更的失败注入 | cleanup 后重放仍返回逐字段相等的 tombstone 与 `result_revision`、`replayed = true`，不访问行与 storage；新 mutation_id 且行已清理返回 `MEDIA_NOT_FOUND`；摘要不符返回 `MUTATION_REPLAY_MISMATCH`；任一写入失败则状态与回执同时回滚，无孤立回执或孤立状态 |
| 注销 | 注销时在途上传、已 ready 媒体、队列重试、账号重新注册 | 新账号不能访问旧数据；cleanup 不依赖旧 user ID |
| 版本并发 | stale expected_revision、同 mutation_id 不同 payload、purge 竞态 | 返回 REVISION_CONFLICT/REPLAY_MISMATCH，无静默覆盖；冲突不写回执 |
| MCP 协议 | Streamable HTTP、来源白名单、Bearer 位置、分页 token、工具 schema、SSE 错误 | 只接受定义的传输和字段；无 cookie/query token |
| MCP 安全 | 跨账号 ref、scope 越权、撤销后重放、auth_version 变化、限流 | 无数据泄露；写操作停止；Retry-After 正确 |
| 隐私 | 日志、审计、错误、响应、下载 URL、cleanup row 检查 | 不出现 user ID、PAT、对象 key、签名 URL 或敏感内容 |
| 兼容 | 老 Entry 客户端读写、迁移前空附件字段、服务降级、旧 `id` 请求 | 文字记录闭环不回归；新列可空且可回滚停写；兼容窗口内 `id` 只对浏览器 BFF 生效 |
| 运行 | storage 404/5xx、DB 事务失败、queue worker 崩溃/重复执行 | 对象不存在视为成功；可重试；无孤儿 key 的不可控增长 |

必须至少包含真实数据库/内存 storage 的服务测试、BFF 认证与错误映射测试、MCP Streamable HTTP 合同测试，以及浏览器直传的端到端测试。跨账号、注销、Complete 重放、cleanup 失败重试、MCP 撤销/版本失效、**Media revision 一致性**、**update/trash/restore/purge/create 回执重放与快照不可变**、**Media 删除回执在 cleanup 后仍可重放**和**ref 映射越权**是发布阻断项。

## 11. 后续实现交付边界

附件实现票应交付：migration（含 `revision`、`public_ref` 与唯一索引）、storage adapter 接入、MediaService proto 生成物、owner checks、Prepare/Complete/List/Get/Delete、**revision（§3.4）与删除回执（§3.5，写入共用表）**、缩略图和 cleanup worker（不读写回执）、错误映射及服务测试；不得自行添加 tripmap 语义或 MCP 工具。

记录服务补票（MCP 前置）应交付：`timeline_mutations` 表与必需迁移（含 `target_kind`/`media_ref`/`NOT NULL` 快照/`CHECK`/只插不改权限）、把 Create 从行内 mutation_id 迁到回执表、为 update/trash/restore/purge 实现 §2.5 判定顺序与响应回执、为 `timeline_entries` 增加 `public_ref` 与 ref 解析入口、补齐 §10 的回执与引用测试；本票必须先建表，附件实现票才能写入 Media 删除回执（依赖顺序：记录服务补票 → 附件票 / MCP 票）。

MCP 实现票应交付：Streamable HTTP 路由、来源白名单、Bearer/audience/scope 校验、普通账号映射、令牌撤销和 auth_version 检查、限流、审计、七个 v1 工具、`entry_ref` 输入 schema 与出站脱敏（§6.2.1）、分页和 revision/mutation 错误映射；附件工具必须另有 scope 和评审。

任何需要改变 Entry 字段、对外引用方案、用户身份来源、对象 key 语义、错误码、永久删除默认值或 MCP token audience 的变更，都必须先修订本文并重新评审。
