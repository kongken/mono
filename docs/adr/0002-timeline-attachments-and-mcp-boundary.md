# Timeline 附件与 MCP 服务边界契约

**状态：草案，基于 KON-99 已接受实现 `8c5a51592f2a34504828295e967b75d357e07761`；供后续评审和实现票使用。**

本文是 Timeline 附件服务与 Timeline MCP BFF 的唯一边界草案。它补充现有文字记录闭环，不修改 KON-99 已接受的记录实现，也不在本票启动附件或 MCP 实现。

## 决策摘要

1. `Entry` 与 `MediaObject` 是两个独立的账号拥有对象。一个 Entry 可有零个或多个 MediaObject；MediaObject 只能通过 `entry_id` 关联一个 Entry，不能跨账号挂载。附件摘要可以随 Entry 返回，但附件上传生命周期由独立的 MediaService 管理。
2. MediaObject 采用 `pending`、`ready`、`deleting` 三态。预签名上传只创建 `pending` 元数据和临时对象；浏览器直传成功后必须由客户端调用 CompleteUpload，服务端校验对象后才变为 `ready`。超时、校验失败和删除都进入可重试 cleanup queue。
3. 对象 key 永远由服务端生成，使用随机 opaque ID 和固定命名空间；key 不包含 user ID、Entry ID、文件名、地区、旅行或客户端路径。对外只返回不含内部 ID、PAT 或对象 key 的媒体摘要和短期下载 URL。
4. 用户身份只来自内部会话或内部服务令牌。任何 `user_id`、账号 UUID、PAT、对象 key 或数据库主键都不是浏览器、MCP 客户端或请求消息的替代身份输入。所有 Entry、MediaObject 和 cleanup 查询、更新、删除都带服务端归属边界。
5. v1 附件 API 使用现有 storage 抽象，不携带 tripmap 的 region/trip 语义。Upload 的幂等键是 `(owner, upload_id)`；Complete 和删除使用 mutation_id，并绑定请求摘要、归属和目标对象。每个 MediaObject 有可读的单调 `revision`：Complete/Delete 要求非零 `expected_revision`，冲突返回 `REVISION_CONFLICT` 并附 `current_revision`；24 小时回执窗口内的重放返回原结果与 `replayed=true`，窗口外按当前 revision 重新判定（stale revision 仍拒绝）。
6. 客户端发起、带 mutation_id 的记录与媒体变更由服务端（TimelineService / MediaService）在业务事务内写持久 mutation 回执，唯一键 `(owner, operation, mutation_id)`，窗口 24 小时；重放返回记录的结果元数据，请求摘要不同返回 `MUTATION_REPLAY_MISMATCH`。后台生命周期转移不写客户端回执，只写带 actor 的状态转移/审计记录（3.4）。MCP BFF 与浏览器 BFF 都不自建回执、不生成或改写 `mutation_id`。
7. Timeline MCP BFF 是独立的 Streamable HTTP 入口。它只把普通账号会话映射成内部身份，管理令牌只接受固定 audience 和 scope，并且不把 MCP 令牌、账号 ID 或 PAT 传给 Timeline 服务。v1 MCP 工具覆盖 `list`、`search`、`create`、`update`、`trash`、`restore`、`purge`；附件工具延期到附件服务完成后再单独加 scope 和契约。
8. 对外标识是服务端签发的 opaque `entry_ref` / `media_ref`：随机 128 位值，按 `(owner, kind)` 存入 `timeline_public_refs`，解析时同时约束 owner 与 kind，失败统一返回 `NOT_FOUND`。MCP 只接受和返回 ref，绝不透传内部 ID；浏览器附件入口同样使用 ref，`Entry.id` 保留既有兼容语义。
9. 所有有副作用的 MCP 工具要求 `mutation_id`；更新、回收站、恢复和永久删除还要求非零 `expected_revision`。账号被禁用、会话 `auth_version` 失效、令牌被撤销或 revision 冲突时拒绝操作，不进行隐式重试。

## 1. 适用范围与现状基线

### 1.1 本文覆盖

- Timeline Entry 与 MediaObject 的数据关系和生命周期。
- 文件元数据、内容类型、大小、图片尺寸和文件名的边界。
- PrepareUpload → 浏览器直传 → CompleteUpload → List/Read/Delete 的服务契约。
- 对象命名空间、缩略图、软删除、永久删除和 cleanup queue。
- 账号隔离、注销、响应脱敏和内部服务认证。
- `bff.timeline.v1.MediaService` 的 proto 草案、Media `revision`、删除/重放结果、错误码和幂等行为。
- 记录与媒体变更的持久 mutation 回执、`expected_revision` 冲突语义和记录侧响应扩展（3.4、3.5）。
- 对外 opaque `entry_ref`/`media_ref` 的签发、解析、脱敏、撤销与记录/媒体映射存储（5.3）。
- Timeline MCP BFF 的路由、传输、令牌、普通账号映射、来源白名单、Bearer、限流、审计、账号状态、`auth_version`、`expected_revision` 和 `mutation_id`。
- 状态转换、权限矩阵、调用图、迁移/回滚/部署顺序、兼容策略和测试矩阵。

### 1.2 已接受的记录基线

KON-99 的 `bff.timeline.v1.TimelineService` 已定义文字记录的 List/Create/Update/Delete/Restore/Purge。记录身份来自内部 auth context，现有请求消息不带 user ID；记录拥有 `revision` 和 `mutation_id`，删除默认进入回收站，永久删除通过明确的 purge 操作完成。现有文字记录的日期、IANA 时区、金额最小单位、书签 URL 校验和稳定排序继续有效。

已接受实现只在 `CreateEntry` 查询历史 mutation（`internal/timeline/application/timeline.go:161-174`）；`UpdateEntry` 只写入 mutation_id（`:193-195`），`DeleteEntry`/`RestoreEntry`/`PurgeEntry` 完全不消费它（`:202-258`）。因此现有行为**不构成** update/trash/restore/purge 的幂等保证；第 3.5 节冻结后续实现补齐的持久回执语义，在它落地前不得把这些方法当作可安全重试。

当前 `timeline_media_objects` 和 cleanup 表属于早期骨架，不能被视为附件服务的完整契约：后续实现必须补齐状态、文件元数据、上传预留、过期时间、尺寸、`revision`、引用行和清理事务语义。本文中的字段名和状态是后续 migration/proto 实现的目标。

### 1.3 非目标

- 不引入 tripmap 的 `region_code`、`trip_id`、共享旅行成员或地图语义。
- 不让 Timeline 抓取书签 URL，也不为附件做病毒扫描、OCR 或内容搜索；这些属于另行评审的异步能力。
- 不让浏览器或 MCP 直接访问数据库、对象存储管理 API 或内部 gRPC listener。
- 不把附件上传塞进现有文字记录 Create/Update 的事务；附件完成后通过独立服务关联 Entry。

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
- MediaObject 的 `entry_id` 是内部关联字段；外部响应只返回服务端签发的 opaque `entry_ref`/`media_ref`（5.3），不返回数据库主键或对象 key。
- 一个 MediaObject 只有一个原始文件和至多一个服务端生成的缩略图。缩略图不是可独立上传的 MediaObject。
- Entry 删除进入回收站时，附件仍保留并可读；Entry 永久删除时，关联 MediaObject 一并标为 `deleting`，对象 key 进入 cleanup queue，数据库关系在事务中删除或转入不可恢复清理记录。
- cleanup queue 只存随机对象 key、状态、尝试次数和时间，不存 user ID、Entry ID、文件名或 PAT。

### 2.2 MediaObject 字段

| 字段 | 规则 |
| --- | --- |
| `media_ref` | 服务端生成的对外 opaque 引用；不可当作身份或存储 key；不回显内部 UUID |
| `entry_id` | 内部字段；创建和查询必须按当前 owner 约束；不接受跨账号引用 |
| `state` | `pending`、`ready`、`deleting`；禁止客户端写任意状态 |
| `revision` | 服务端单调版本：插入 pending 时为 1，每次状态转移（pending→ready，任意→deleting）或可见元数据变化 +1；只读，客户端不得写入；Complete/Delete 用它做乐观并发（见 3.4） |
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
- `deleting` 只允许 cleanup worker 重试；客户端重复 Delete 返回原删除结果或 `NOT_FOUND` 的统一安全结果。
- cleanup 成功后删除对象和缩略图（若有），再标记 queue done；对象不存在视为成功。数据库状态更新失败可安全重试。
- 每次转移在同一事务内递增 `revision`。客户端发起的 Complete/Delete 还必须在同一事务写入持久 mutation 回执（3.4）；后台转移（过期回收、校验失败、Entry purge、账号注销、worker 重试）没有客户端 mutation_id，因此不写 mutation 回执，只写状态转移记录（actor=system|worker、reason、from_state→to_state、revision），并用条件更新保证只转移一次。
- 所有转移写入审计事件，但审计只允许 opaque media_ref、结果类别、时间和 actor 类别，不含 key、内容、文件名全文或令牌。后台转移记录不参与 24 小时回执窗口、不可重放、不产生 `replayed`。

## 3. 内部服务契约：记录补强与 MediaService

### 3.1 身份与调用边界

MediaService 仅接受内部服务调用。调用方必须提供由 BFF 或受信任内部服务签发的内部 bearer，并通过 `rpcauth` context 得到 owner；请求消息不含 `user_id`。账号状态和 `auth_version` 在 BFF 身份映射时检查，内部服务也必须拒绝空身份和未知账号。

现有 storage 抽象提供 `Put`、`Head`、`Delete` 和短期签名 URL 能力。MediaService 不向业务层暴露 bucket、endpoint、credential、对象 key 或 storage provider 名称。

MediaService 与 TimelineService 共享 timeline 模块内的引用仓库（5.3）：Entry 的 `entry_ref` 由 TimelineService 签发，media 的 `media_ref` 由 MediaService 签发；两者解析时都同时约束当前 owner 与 kind。MCP 与浏览器附件入口只传 ref，不传内部 ID。

### 3.2 `bff.timeline.v1.MediaService` proto 草案

以下是 v1 的规范字段；字段编号一经发布不得复用。`entry_ref`/`media_ref` 是服务端签发的 opaque 引用（见 5.3）：内部实现使用数据库 UUID，但任何响应都不得暴露内部 UUID。

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
  rpc DeleteMedia(DeleteMediaRequest) returns (MediaMutationResult);
}

message Empty {}
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
  int64 revision = 13;               // monotonic, read-only, see 3.4
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
  int64 expected_revision = 4;       // required, > 0, revision read at Prepare
}
message ListMediaRequest { string entry_ref = 1; }
message ListMediaResponse { repeated Media media = 1; }
message GetMediaRequest { string media_ref = 1; }
message DeleteMediaRequest {
  string media_ref = 1;
  int64 expected_revision = 2;       // required, > 0
  string mutation_id = 3;            // required
}
message MediaResponse { Media media = 1; bool replayed = 2; }
message MediaMutationResult {
  string media_ref = 1;              // the deleted object's opaque ref
  int64 revision = 2;                // revision produced by this mutation
  string state = 3;                  // pending|ready|deleting
  bool replayed = 4;                 // true when served from the receipt
  string mutation_id = 5;            // echoed back, never generated by the server
}
```

`Media.revision` 是删除和完成上传的唯一并发凭据；`DeleteMedia` 不再返回 `Empty`，因此调用方在超时重试时能区分“首次应用”和“回执重放”。同一个字段集合同步用于内部 `timeline.v1.MediaService` 与 BFF 的 `bff.timeline.v1.MediaService`：两者都不返回 media 内部 UUID，内部版本只服务 BFF，不直接暴露给浏览器或 MCP。`PrepareUploadResponse.media.revision` 在创建 pending 时固定为 1，客户端必须原样带回 Complete/Delete。

实现可在内部 protobuf 中加入 `request_hash`、`object_version` 和 worker 字段，但这些字段不进入 BFF 或 MCP 的公开响应。`download_url` 和 `thumbnail_url` 每次读取按当前 owner 签发，TTL 不超过 5 分钟；URL 不写日志。

### 3.3 方法语义

- **PrepareUpload**：校验 Entry 属于当前 owner、文件名/MIME/大小白名单和账号配额；在数据库事务内插入 pending MediaObject 与 cleanup reservation，再由 storage 生成 staging key 和短期 PUT URL。相同 `(owner, upload_id)` 且规范请求哈希相同则返回原 pending 预留；哈希不同返回 `IDEMPOTENCY_KEY_REUSED`。
- **浏览器直传**：浏览器只向签名 URL PUT，不能自选 bucket/key 或携带内部 bearer。PUT 失败由客户端重新调用 PrepareUpload 或在有效期内重试。
- **CompleteUpload**：按 owner、media_ref、upload_id 查询对象，按 3.4 的优先级先查未过期回执（命中即返回回执结果并置 `replayed=true`）；无有效回执才校验非零 `expected_revision` 等于当前 revision（不匹配返回 `REVISION_CONFLICT` 并带 `current_revision`），再对 staging 对象执行 Head、大小/MIME/魔数/图片尺寸检查，成功后原子更新 ready、`revision+1` 并安排缩略图。已 ready 且 `expected_revision` 等于当前 revision 时返回当前摘要作为幂等成功（`replayed=false`），不改变状态。对象不存在、过期或校验失败不会变 ready。
- **ListMedia/GetMedia**：只返回当前 owner 对指定 Entry 的 ready 或仍可见 pending 摘要；deleting 不出现在普通列表。下载 URL 是读取时签发的短期 URL，服务端先再次检查 owner 和 Entry。
- **DeleteMedia**：必须按 owner 查询。按 3.4 的优先级先查未过期回执；无有效回执才校验非零 `expected_revision` 并执行转移：事务内将 ready/pending 改为 deleting、递增 `revision`、写 cleanup queue 和 mutation 回执，提交后返回 `MediaMutationResult{media_ref, revision, state=deleting, replayed}`；worker 删除原图和缩略图，Delete 调用本身不等待对象存储完成。相同 mutation_id 与相同请求摘要的重放返回回执里的 revision/state 并置 `replayed=true`；无有效回执但对象已 `deleting` 且 `expected_revision` 等于当前 revision 时，不改变状态、不再入队、不递增 revision，返回当前 revision/state（`replayed=false`）；stale revision 返回 `REVISION_CONFLICT`（附 `current_revision`），不会重复入队。

### 3.4 Media 的 revision、删除与重放契约

- revision 单调递增、永不复用：`PrepareUpload` 创建 pending 时为 1；`pending→ready`、`→deleting` 各 +1。删除后对象仍以 `deleting` 可读，`GetMedia` 返回其最后 revision；对象被 worker 物理清除后 `GetMedia`/`ListMedia` 返回 `MEDIA_NOT_FOUND`。
- `expected_revision` 在 v1 变更方法（Complete/Delete）中必填且必须 > 0；缺省或 0 返回 `REVISION_REQUIRED`（`INVALID_ARGUMENT`），避免与历史“0 表示不校验”的浏览器记录语义混淆。
- 冲突响应在 error details 里带 `current_revision` 和当前 `state`；两者只对当前 owner 有效，属于公开信息，不携带内部 ID 或对象 key。
- 重放结果只包含回执记录的结果元数据：`media_ref`、该次变更产生的 `revision`、`state`、`replayed=true`、回显的 `mutation_id`。不在回执里保存文件内容或历史摘要；需要当前字段的客户端用 `GetMedia`/`ListMedia` 重新读取（回执可能早于后续变更，返回历史快照反而会误导客户端）。
- **执行优先级（Complete/Delete 统一，终止歧义）**：① 先按 `(owner, media_ref, operation, mutation_id)` 查未过期回执；命中且请求摘要一致即原样返回回执结果（`replayed=true`），不再比较当前 revision；摘要不同返回 `MUTATION_REPLAY_MISMATCH`。② 无有效回执才校验 `expected_revision` 与当前 revision，不匹配返回 `REVISION_CONFLICT`（error details 带 `current_revision`、`state`），不产生任何副作用。③ 通过后才执行转移、递增 revision，并在同一事务插入回执。
- 回执窗口 24 小时（与 usersrv `auth_operation_receipts` 的窗口一致）。过期后同一 mutation_id 视为新请求，完全按 ② 判定：Complete 遇到已 ready 且 `expected_revision` 为旧值（通常 1）→ `REVISION_CONFLICT`；只有携带当前 revision（2）时才返回当前 ready 摘要且不改变状态；Delete 同理。因此过期回放不会改状态、不会重复入队、不会产生第二次副作用。
- 回执只覆盖客户端发起、带 mutation_id 的 Complete/Delete。后台转移（pending 过期、校验失败、Entry purge、账号注销、worker cleanup 重试）不写 `timeline_media_mutation_receipts`、不生成客户端回执，也不进入 24 小时窗口；它们用条件状态更新（`UPDATE ... WHERE state IN (...) AND revision = ?`）保证单次转移，并写 `actor=system|worker`、`reason`、`from_state`、`to_state`、`revision` 的状态转移记录用于审计与排障。worker 对 `deleting` 的重试只重试对象存储删除，不改变状态机 revision。
- 回执与状态变更必须在同一数据库事务内提交；唯一约束冲突时回滚整个事务并重读回执，因此不存在“对象已变 deleting 但没有回执”的窗口。

### 3.5 记录侧的 mutation 回执与响应扩展

KON-99 已接受实现对 update/trash/restore/purge 没有持久幂等（见 1.2）。后续实现必须把回执所有权固定在 **TimelineService**，MCP BFF 与浏览器 BFF 都不另建回执：

- 表 `timeline_mutation_receipts` 字段：`id`、`user_id`、`operation`（`create|update|trash|restore|purge`）、`mutation_id`、`request_hash`（SHA-256，32 字节）、`result_kind`（`applied|already_applied`）、`entry_id`（内部目标，可空）、`result_revision`、`result_deleted`（bool）、`created_at`、`expires_at`；唯一索引 `(user_id, operation, mutation_id)`，普通索引 `(user_id, created_at)`、`(expires_at)`。附件服务使用独立表或同形状表（`timeline_media_mutation_receipts`），不复用记录回执；该表只记录客户端 Complete/Delete，后台状态转移不写回执（3.4）。
- 请求摘要覆盖 `operation || owner || 目标内部 id || expected_revision || 规范化后的全部可写字段`（Create 为新建字段集，Update 为完整字段集，Trash/Restore/Purge 为目标 id 与 expected_revision）。摘要不同即 `MUTATION_REPLAY_MISMATCH`（`ALREADY_EXISTS`），不能因 mutation_id 相同就返回别人的结果。
- 提交顺序固定为：同一事务内先按 `(user_id, operation, mutation_id)` 查回执；命中且摘要一致就直接返回记录结果（此时**不再比较当前 revision**，所以超时重试不会被并发编辑影响）；未命中则校验 `expected_revision`、执行变更、插入回执，提交。插入命中唯一冲突时回滚整个事务、重读回执再返回，保证只有一次变更提交成功。
- 回执只保存结果元数据，不保存历史快照：`create` 返回原 `entry_id`/`revision`；`update` 返回该次变更产生的 `revision`；`trash` 返回 `deleted=true` 与 `revision`；`restore` 返回 `revision`；`purge` 返回 `purged=true` 与最终 revision。所有重放响应置 `replayed=true`；客户端若需要当前字段应重新 list/get。
- `timeline_entries.mutation_id` 不再作为幂等来源，仅保留为历史列；回执过期后的同 id 重放会真正再次执行，此时 stale `expected_revision` 返回 `REVISION_CONFLICT`，不会二次写入。
- proto 采用新增字段与新增返回消息，不改变既有字段号（`pkg/proto/timeline/v1` 与 `pkg/proto/bff/timeline/v1` 同步）：
  - `DeleteEntry` 返回 `DeleteEntryResponse { int64 revision = 1; bool deleted = 2; bool replayed = 3; }`。
  - `PurgeEntry` 返回 `PurgeEntryResponse { int64 final_revision = 1; bool purged = 2; bool replayed = 3; }`。
  - `EntryResponse` 增加 `bool replayed = 2`，覆盖 Create/Update/Restore 的重放标记。
  - `DeleteEntryRequest`（`permanent` 已占 4）、`RestoreEntryRequest`、`PurgeEntryRequest`、`UpdateEntryRequest` 各新增可选 `string entry_ref`（分别为字段 5/4/4/4）作为 `id` 的替代目标；MCP 只发 `entry_ref`，浏览器继续发 `id`，两者同时出现且不一致时返回 `INVALID_ARGUMENT`。

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
| `REVISION_CONFLICT` | `ABORTED` | expected_revision 不匹配；details 带 `current_revision` 与当前 state |
| `REVISION_REQUIRED` | `INVALID_ARGUMENT` | v1 变更/删除未提供非零 expected_revision |
| `MUTATION_REPLAY_MISMATCH` | `ALREADY_EXISTS` | mutation_id 的请求摘要/目标对象不同 |
| `STORAGE_UNAVAILABLE` | `UNAVAILABLE` | 可重试的 storage 故障，不暴露供应商错误 |
| `INTERNAL` | `INTERNAL` | 统一内部错误；日志保留关联 ID |

“找不到”与“没有权限”对外统一为同类 `NOT_FOUND`，避免跨账号猜测；内部安全审计可记录更细原因，但不得回送调用方。`REVISION_CONFLICT` 的 error details 可以带 `current_revision`（以及 opaque `entry_ref`/`media_ref`）：它们是只对当前 owner 有效的公开信息，不得附带内部 ID、user ID、字段内容或对象 key。

## 5. 账号隔离、注销与隐私

### 5.1 归属边界

- Entry 查询始终包含 `user_id = authenticated owner`；Media 查询始终同时包含 owner 和 Entry/Media 关系。
- `entry_ref`、`media_ref`、mutation_id 和 upload_id 只能在当前 owner 命名空间解释；随机引用不是授权凭证。引用解析固定为：身份映射 → `resolve_ref(owner, kind, ref)` → 按内部 ID + owner 查业务表（5.3），三步任一失败都是同一个 `NOT_FOUND`。
- Update/Delete/Purge 先做归属查询再做 mutation；SQL delete/update 的 where 条件再次包含 owner，避免 TOCTOU 或错误调用方造成越权。
- cleanup worker 只消费 opaque object key。管理重试按 queue row 授权，不能用用户传入的 key；对象存储删除使用固定 bucket/prefix allowlist。
- 任何 response mapper 都使用显式白名单。不得直接序列化 Ent、storage SDK、auth context 或 error 对象。

### 5.2 注销

注销工作流先让账号系统使账号和 `auth_version` 失效，再调用 Timeline cleanup。Timeline 事务删除/标记该 owner 的 Entry、MediaObject、`timeline_public_refs` 行与记录回执，并将每个原图和缩略图 key 作为独立 cleanup item 写入队列；队列不保存 user ID。即使账号已不存在，cleanup 仍可重试。注销响应只返回既有 opaque receipt，不包含 Timeline 内部 ID 或对象 key。

注销期间新的 Prepare/Complete/List/Read/Delete 均拒绝；已签发但未使用的浏览器 URL 因 owner 状态或短 TTL 失效。浏览器直传对象即使已经写入，也由 pending reservation 的过期 cleanup 兜底。

### 5.3 对外 opaque 引用（entry_ref / media_ref）

审查意见指出 MCP 的 `entry_ref` 与已接受 Timeline 实现（`proto/bff/timeline/v1/timeline.proto:18-29` 的 `Entry.id`、`internal/timeline/application/timeline.go:185-255` 按数据库 ID 查询）之间缺少可执行的映射。本 ADR 冻结如下边界：

- **映射所有权在 timeline 服务内部**，新增表 `timeline_public_refs`：`ref`（主键，形如 `er_`/`mr_` + 22 位 base64url，即 128 位随机值）、`owner_id`、`kind`（`entry|media`）、`internal_id`、`created_at`、`revoked_at`（可空）；唯一索引 `(owner_id, kind, internal_id)` 保证一个实体只有一个稳定 ref，`ref` 自身唯一。Entry 的 ref 在第一次需要对外输出时惰性创建（`INSERT ... ON CONFLICT DO NOTHING` 后重读，天然幂等）；MediaObject 的 ref 在 PrepareUpload 创建对象时写入。
- **解析必须在 owner 边界内完成**：`resolve_ref(owner, kind, ref)` 的查询条件是 `ref = ? AND owner_id = ? AND kind = ? AND revoked_at IS NULL`，命中后用 `internal_id` 再按 owner 查业务表；任一条件不满足都返回统一的 `NOT_FOUND`。别账号的 ref、伪造/篡改的 ref、把 `entry_ref` 当 `media_ref` 用（前缀与 kind 双重校验）在第一步就失败，不能靠服务端“知道 ID 就能操作”。
- **proto 边界**：`timeline.v1` 与 `bff.timeline.v1` 的 `Entry` 新增 `string entry_ref = 18`（只增不改，`id` 保留原字段号与含义）；`Update/Delete/Restore/Purge` 请求新增可选 `entry_ref`（见 3.5），与 `id` 二选一。MCP BFF 只发 `entry_ref`、只回 `entry_ref`，内部 `Entry.id` 不进入 MCP 请求或响应；浏览器继续用 `id`（已接受行为），新增附件入口用 ref。
- **响应脱敏**：MCP 适配层使用显式白名单 `entry_ref,title,date,time,timezone,category,location,amount_minor,currency,note,url,url_title,url_note,revision,created_at,updated_at`（附件摘要另加 `media_ref,filename,content_type,size_bytes,width,height,revision` 与短期 URL）；不透传 `Entry.id`、`Entry.deleted_at`、`user_id`、Ent/storage/error 对象。媒体摘要一律不含 media 内部 UUID、对象 key 或 bucket。
- **生命周期与撤销**：ref 在实体生命周期内稳定；进入回收站不撤销（restore 后客户端保存的 ref 仍有效）；purge 与账号注销在事务内删除对应 ref 行。物理清除后的 ref 解析返回 `NOT_FOUND`，不会重新指向新对象。ref 不是凭证：每次请求仍校验会话/令牌、账号状态与 `auth_version`，跨账号持有 ref 不产生任何权限。
- **分页 token**：`page_token` 由 TimelineService 生成与解析（不透明、owner 作用域、带筛选条件摘要与短期过期），MCP BFF 只原样透传、不缓存、不重编；筛选条件与 token 不匹配返回 `INVALID_ARGUMENT`，token 被篡改或跨账号使用返回 `INVALID_ARGUMENT`/`NOT_FOUND`，不泄露内部排序键或内部 cursor。
- **已否决的替代方案**：无状态 AEAD/HMAC 引用（不在数据库存映射）能省一张表，但需要 MCP BFF 持有密钥环、处理轮换与引用过期，且浏览器附件路径还需第二套密钥；持久映射表由服务端统一签发/撤销，能覆盖注销与 purge，代价只是每个 ref 的一次索引查询。若以后必须让引用可单独撤销或跨服务解析，也只能沿持久表扩展，不能两种方案混用。

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

### 6.3 工具契约

| 工具 | 必需 scope | 输入关键字段 | 规则 |
| --- | --- | --- | --- |
| `timeline.list` | `timeline:read` | query、category、from_date、to_date、page_size、page_token | 只返回当前账号；稳定排序和分页 token 不泄露内部 cursor；每项带 `entry_ref` 与 `revision` |
| `timeline.search` | `timeline:read` | query、page_size、page_token | 是 list 的受限别名，不抓取 URL，不做跨账号搜索 |
| `timeline.create` | `timeline:write` | entry fields、mutation_id | 标题/日期/时区/金额/书签沿用现有校验；重复 mutation 返回原 `entry_ref`/`revision` 并置 `replayed=true` |
| `timeline.update` | `timeline:write` | opaque entry_ref、entry fields、expected_revision、mutation_id | revision 不符返回 `REVISION_CONFLICT`（带 `current_revision`）；不能修改 owner 或内部字段；成功返回新 revision |
| `timeline.trash` | `timeline:trash` | entry_ref、expected_revision、mutation_id | 软删除；返回 `deleted=true` 与 revision；重复请求幂等；不接受隐式永久删除参数 |
| `timeline.restore` | `timeline:trash` | entry_ref、expected_revision、mutation_id | 只恢复当前账号回收站记录；返回恢复后 revision |
| `timeline.purge` | `timeline:purge` | entry_ref、expected_revision、mutation_id | 明确不可恢复；附件进入 cleanup queue；返回 `purged=true` 与最终 revision；默认需要更强 scope |

MCP 工具错误映射为稳定 `code`、可操作的短消息和可选 `retryable`，例如 `AUTH_EXPIRED`、`FORBIDDEN`、`NOT_FOUND`、`VALIDATION_FAILED`、`REVISION_REQUIRED`、`REVISION_CONFLICT`、`MUTATION_REPLAY_MISMATCH`、`RATE_LIMITED`、`SERVICE_UNAVAILABLE`。所有副作用工具都必须在输入 schema 中要求 mutation_id；BFF 不能替客户端生成并在重试中改变它。

附件在本版 MCP 中延期：MCP 客户端不能调用 PrepareUpload、签名 URL、CompleteUpload 或下载 URL。后续若开放，必须单独增加 `timeline:media` scope、工具输入/输出白名单、大小和内容类型限制，并复用 MediaService 的 owner 检查与 mutation 语义。

### 6.4 revision、mutation 与重放在 MCP 层的传递

- MCP BFF 是回执的**透明传递者**：它不生成、不改写、不缓存 mutation_id，也不持有回执表。同一个工具调用的重试必须带同一个 mutation_id，由 TimelineService 的唯一约束判定首次应用还是回放。BFF 只把回执结果里的 `replayed`、`revision`、`deleted|purged` 映射到工具输出。
- 读工具（list/search）必须输出 `revision`，客户端用它作为下一次 update/trash/restore/purge 的 `expected_revision`；不接受“让服务端自己查最新 revision”的省略形式。
- `expected_revision` 在 update/trash/restore/purge 的 JSON schema 里必填且为整数 > 0；缺失、0 或非整数返回 `REVISION_REQUIRED`。MCP 不沿用浏览器路径“0 表示不校验”的兼容语义。
- 冲突输出：`REVISION_CONFLICT` 附 `current_revision`（和当前 `entry_ref`），客户端应用它重新读取后再提交；不自动重试、不静默覆盖。
- 回放输出：`replayed=true` 且返回回执记录的 `revision`/`deleted` 状态；不返回历史字段快照。客户端需要当前字段时重新 list/get。
- `MUTATION_REPLAY_MISMATCH` 表示同一 mutation_id 曾用于不同工具、不同目标或不同字段，必须报错而不是“幂等成功”；BFF 不能通过自动换 id 避开该错误。
- 服务端 `REVISION_CONFLICT`/`MUTATION_REPLAY_MISMATCH` 在 MCP 层保持同名稳定 `code`，携带 `retryable: false`，不暴露内部错误文本或目标内部 ID。

### 6.5 限流、审计与可观测性

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
  MBFF -->|opaque entry_ref + owner| T
  T --> DB[(Timeline DB)]
  T --> REF[(public refs)]
  T --> REC[(mutation receipts)]
  M --> REF
  M --> REC
  M --> DB
  M -->|signed PUT/GET| S[(Storage abstraction)]
  Browser -->|short lived signed PUT| S
  M --> Q[Opaque cleanup queue]
  Q --> S
  Usersrv[usersrv / auth] -->|status + auth_version| BFF
  Usersrv -->|status + token validation| MBFF
```

MCP BFF 只调用 Timeline BFF 内部服务接口，不绕过 owner checks；浏览器直传只使用一次性、短期、固定 key 的签名 URL。MCP 与浏览器附件入口只传 opaque ref，ref 解析、owner 校验、mutation 回执和 revision 递增都在服务端事务内完成；BFF 不持有回执表，也不接触 `timeline_public_refs` 以外的映射。

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

“自己”始终由内部会话/令牌映射的 owner 决定，不由请求参数决定。MCP 所有行只接受 opaque `ref`，浏览器路径使用既有 `id`；两种入口都不接受请求方提供的 user ID、PAT 或对象 key。账号注销与 purge 同时删除该 owner 的 ref 行与回执行，使旧引用失效。

## 9. 迁移、发布与回滚

### 9.1 数据迁移

1. 先部署只读兼容代码和 cleanup worker，确认现有 `timeline_entries`、`timeline_media_objects`、`timeline_cleanup_objects` 可安全读取。
2. 增量增加 MediaObject 状态、文件元数据、upload reservation、过期时间、尺寸、`revision` 和 request hash 字段；默认值兼容现有空字段（`revision` 默认 1）。为 `(owner, upload_id)` 建唯一索引时先清理/隔离重复非空值，不能把旧空值误视为同一上传。
3. 增加 cleanup queue 的 opaque key 约束、attempt/lease 字段和必要索引；不把 owner 或 Entry 字段加入队列。
4. 新建 `timeline_mutation_receipts`（字段与索引见 3.5）和 `timeline_public_refs`（见 5.3）；两者都只增不删，回执按 24 小时窗口清理，ref 在实体 purge/账号注销时删除。后台转移记录复用现有审计事件通道，不新建客户端回执表。`timeline_entries.mutation_id` 保留为历史列，不再作为幂等来源，也不要求唯一索引。
5. 部署 MediaService 和 BFF 路由；先关闭 MCP media scope，完成对象存储 staging prefix 的生命周期规则后再允许 PrepareUpload。
6. 观测 pending 超时、Complete 失败、命中率（回执重放 / revision 冲突 / ref 解析失败）、cleanup 成功率和跨账号拒绝后，再开放浏览器附件入口。
7. 最后按独立评审开放 MCP 记录工具；附件 MCP 另行发布。

### 9.2 回滚

- schema 回滚只允许停用新路由和新写入；保留新列和 queue 行，不能在已有数据写入后执行破坏性 down migration。
- MediaService 回滚到只读/关闭状态时，cleanup worker 必须继续运行，防止 staging 对象泄露；旧 Timeline 文字记录不受影响。
- 若 Complete 校验出现问题，关闭 PrepareUpload 和附件入口，保留 ready 数据和 cleanup；不能把 ready 强行改回 pending。
- MCP 出现安全问题时立即撤销 audience/scope 或关闭路由，递增账号 auth_version/撤销令牌；不删除 Entry 数据，不通过客户端补偿恢复。
- 回滚不删除 `timeline_mutation_receipts` 与 `timeline_public_refs`：删除回执会让已应用的变更在客户端重试时二次执行，删除 ref 会让客户端保存的引用意外失效且没有安全收益；只停写并让清理 worker 按窗口过期。

### 9.3 兼容策略

- 现有 TimelineService 的字段编号、状态码和文字记录排序保持兼容；附件摘要新增字段只能采用新 proto 字段号和可选语义。
- 老客户端看不到附件时仍可正常读写文字 Entry；老客户端发来的 Entry Update 不得清空服务端已有附件。
- 记录 purge 仍是明确操作；实现附件联动时先写 cleanup reservation，再删除关系，保证旧服务和新 worker 可共存。
- MCP 工具 schema 采用版本化工具名或显式 `v1` 路由；字段只增不改含义。错误 `code` 稳定，message 可本地化。
- `Entry` 只新增字段 `entry_ref = 18`；`DeleteEntry`/`PurgeEntry` 的返回消息从 `Empty` 换成带字段的消息，旧客户端会忽略未知字段（二进制与 Connect JSON 均可解析），新客户端需重新生成 stub。旧浏览器路径继续以 `id` 为目标，MCP 路径只认 `entry_ref`。
- ref 契约发布后只能新增 kind 或字段，不能改变 ref 含义、跨 owner/kind 复用或互换 `er_`/`mr_` 前缀；`entry_ref` 与 `id` 同时给出且不一致必须拒绝。
- 回执窗口从 24 小时改长需要单独评审（存储与键空间）；改短只影响可重试窗口，不改变已应用结果——客户端应把“超时后 stale revision”视为“可能已应用”。

## 10. 测试矩阵

| 类别 | 必测场景 | 通过条件 |
| --- | --- | --- |
| 身份隔离 | A 读取/改/删 B 的 Entry、Media、media_ref、entry_ref；猜测 UUID/opaque ref | 统一 NOT_FOUND；B 数据、key、ID 不泄露 |
| 认证 | 缺 bearer、错误 audience/scope、过期/撤销令牌、auth_version 失效、disabled account | 统一拒绝；不调用业务写操作 |
| 上传幂等 | Prepare 重放、相同 upload_id 不同请求、Complete 重放、网络超时后重试 | 相同请求返回同一结果；冲突返回稳定错误且无重复对象 |
| 内容校验 | 超大文件、空文件、错误 MIME、扩展名伪装、坏图片、尺寸超限、PDF 超限 | 不变为 ready；staging 最终进入 cleanup |
| 生命周期 | pending 过期、ready 删除、重复删除、Entry trash/restore/purge、缩略图失败 | 状态转换符合状态机；worker 可重试，删除幂等 |
| 注销 | 注销时在途上传、已 ready 媒体、队列重试、账号重新注册 | 新账号不能访问旧数据；cleanup 不依赖旧 user ID |
| 版本并发 | stale expected_revision、同 mutation_id 不同 payload、purge 竞态 | 返回 REVISION_CONFLICT/REPLAY_MISMATCH，无静默覆盖 |
| 媒体并发 | Prepare 后并发 Complete、删除时 stale revision、无有效回执时重复 Delete、回执过期后 Complete 已 ready | 只接受一个有效转移；未过期回执优先于 revision 比较；过期后 stale revision 固定返回 REVISION_CONFLICT，当前 revision 返回同摘要且不改状态；冲突带回 current_revision |
| 后台转移 | pending 过期、Complete 校验失败、Entry purge、账号注销、worker 重试与并发读取竞争 | 条件更新只转移一次；不写客户端回执；转移记录带 actor/reason 与 revision，不含 key/内容；worker 重试不改变 revision |
| 引用解析 | 跨账号 ref、伪造/篡改 ref、entry_ref 当 media_ref、purge 后 ref、注销后 ref、分页 token 换筛选 | 统一 NOT_FOUND；ref 不可跨 owner/kind 复用；无内部 ID/排序键泄露；token 不可跨筛选复用 |
| 回执重放 | update/trash/restore/purge/complete/delete 超时重试、同 mutation_id 不同摘要、回执过期后重放、并发双击、事务中断 | 只应用一次；摘要不同报 MUTATION_REPLAY_MISMATCH；过期后 stale revision 拒绝而非二次写入；无“变更成功但无回执”窗口 |
| MCP revision | 省略/0/非整数 expected_revision、过期值、回放 | REVISION_REQUIRED / REVISION_CONFLICT 带 current_revision / 回放 replayed=true |
| MCP 协议 | Streamable HTTP、来源白名单、Bearer 位置、分页 token、工具 schema、SSE 错误 | 只接受定义的传输和字段；无 cookie/query token |
| MCP 安全 | 跨账号 ref、scope 越权、撤销后重放、auth_version 变化、限流 | 无数据泄露；写操作停止；Retry-After 正确 |
| 隐私 | 日志、审计、错误、响应、下载 URL、cleanup row 检查 | 不出现 user ID、PAT、对象 key、签名 URL 或敏感内容 |
| 兼容 | 老 Entry 客户端读写、迁移前空附件字段、服务降级 | 文字记录闭环不回归；新列可空且可回滚停写 |
| 运行 | storage 404/5xx、DB 事务失败、queue worker 崩溃/重复执行 | 对象不存在视为成功；可重试；无孤儿 key 的不可控增长 |

必须至少包含真实数据库/内存 storage 的服务测试、BFF 认证与错误映射测试、MCP Streamable HTTP 合同测试，以及浏览器直传的端到端测试。跨账号、注销、Complete 重放与回执过期语义、后台转移的 actor/无回执边界、cleanup 失败重试、ref 解析与撤销、四种记录副作用的重放、以及 MCP 撤销/版本失效是发布阻断项。

## 11. 后续实现交付边界

附件实现票应交付：migration（媒体字段、`revision`、upload reservation、`timeline_mutation_receipts`、`timeline_public_refs`）、storage adapter 接入、MediaService proto 生成物、owner checks、ref 签发/解析、Prepare/Complete/List/Get/Delete、客户端回执与后台状态转移记录（3.4）、缩略图和 cleanup worker、错误映射及服务测试；不得自行添加 tripmap 语义或 MCP 工具。

MCP 实现票应交付：Streamable HTTP 路由、来源白名单、Bearer/audience/scope 校验、普通账号映射、令牌撤销和 auth_version 检查、限流、审计、七个 v1 工具、分页和 revision/mutation 错误映射、ref 脱敏映射；记录侧回执和 `entry_ref` proto 字段由 TimelineService 实现票先交付，MCP 票不得自行实现回执或引用映射。

任何需要改变 Entry 字段、用户身份来源、对象 key 语义、错误码、永久删除默认值、回执窗口或 MCP token audience 的变更，都必须先修订本文并重新评审。

## 12. 本修订处理的评审意见

本 ADR 当前版本基于 KON-105 两轮评审意见修订：首轮评论 `01a1b67d`（发现 1–3），第二轮评论 `01a1b69f`（发现 4–5）。对应关系如下：

1. **Media 无法取得 expected_revision**：新增 `Media.revision`（3.2）、Complete/Delete 必填非零 `expected_revision` 与 `MediaMutationResult`（3.2、3.3、3.4），并定义冲突 details（`current_revision`）与重放返回值。
2. **opaque 引用与已接受 Timeline 契约脱节**：新增 5.3 的 `timeline_public_refs` 映射表、owner+kind 解析规则、`Entry.entry_ref` 与请求侧 `entry_ref` 字段、响应白名单和撤销/生命周期；明确映射由 timeline 服务内部持有，MCP 不做 ID 映射（5.3、6.4、9.1、9.3）。
3. **mutation replay 只覆盖 Create**：新增 3.5 的持久回执表、请求摘要、提交顺序与并发唯一约束，明确回执归 TimelineService（媒体归 MediaService）、MCP 透明传递；补齐 Delete/Purge 响应与 `replayed` 字段，并把回执/ref 纳入迁移、回滚、测试矩阵。
4. **CompleteUpload 过期回放语义冲突**：3.4 增加统一执行优先级（未过期回执 → 当前 revision 校验 → 转移），并规定回执过期后按新请求判定：已 ready + stale revision 固定返回 `REVISION_CONFLICT`，仅当前 revision 返回同摘要且不改状态；同步 3.3 与 10 测试矩阵。
5. **后台生命周期转移的回执边界**：明确回执只覆盖客户端 Complete/Delete；后台转移（过期、校验失败、Entry purge、注销、worker 重试）使用条件状态更新 + `actor=system|worker` 转移记录，不写 mutation 回执、不参与 24 小时窗口、不可重放；同步 2.3、3.4、3.5、10、11。

验证命令（变更后应可重复执行）：`./scripts/check-doc-links.sh`；`git diff --check`。本票不修改实现代码，因此行为验证仍以父票 `8c5a51592f2a34504828295e967b75d357e07761` 的 `go test ./internal/timeline/... ./internal/bff/...` 为基线。
