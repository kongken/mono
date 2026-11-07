# Timeline 附件与 MCP 服务边界契约

**状态：草案，基于 KON-99 已接受实现 `8c5a51592f2a34504828295e967b75d357e07761`；供后续评审和实现票使用。**

本文是 Timeline 附件服务与 Timeline MCP BFF 的唯一边界草案。它补充现有文字记录闭环，不修改 KON-99 已接受的记录实现，也不在本票启动附件或 MCP 实现。

## 决策摘要

1. `Entry` 与 `MediaObject` 是两个独立的账号拥有对象。一个 Entry 可有零个或多个 MediaObject；MediaObject 只能通过 `entry_id` 关联一个 Entry，不能跨账号挂载。附件摘要可以随 Entry 返回，但附件上传生命周期由独立的 MediaService 管理。
2. MediaObject 采用 `pending`、`ready`、`deleting` 三态。预签名上传只创建 `pending` 元数据和临时对象；浏览器直传成功后必须由客户端调用 CompleteUpload，服务端校验对象后才变为 `ready`。超时、校验失败和删除都进入可重试 cleanup queue。
3. 对象 key 永远由服务端生成，使用随机 opaque ID 和固定命名空间；key 不包含 user ID、Entry ID、文件名、地区、旅行或客户端路径。对外只返回不含内部 ID、PAT 或对象 key 的媒体摘要和短期下载 URL。
4. 用户身份只来自内部会话或内部服务令牌。任何 `user_id`、账号 UUID、PAT、对象 key 或数据库主键都不是浏览器、MCP 客户端或请求消息的替代身份输入。所有 Entry、MediaObject 和 cleanup 查询、更新、删除都带服务端归属边界。
5. v1 附件 API 使用现有 storage 抽象，不携带 tripmap 的 region/trip 语义。Upload 的幂等键是 `(owner, upload_id)`；Complete 和删除使用 mutation_id，并绑定请求摘要、归属和目标对象，重放只能返回原结果。
6. Timeline MCP BFF 是独立的 Streamable HTTP 入口。它只把普通账号会话映射成内部身份，管理令牌只接受固定 audience 和 scope，并且不把 MCP 令牌、账号 ID 或 PAT 传给 Timeline 服务。v1 MCP 工具覆盖 `list`、`search`、`create`、`update`、`trash`、`restore`、`purge`；附件工具延期到附件服务完成后再单独加 scope 和契约。
7. 所有有副作用的 MCP 工具要求 `mutation_id`；更新、回收站、恢复和永久删除还要求 `expected_revision`。账号被禁用、会话 `auth_version` 失效、令牌被撤销或 revision 冲突时拒绝操作，不进行隐式重试。

## 1. 适用范围与现状基线

### 1.1 本文覆盖

- Timeline Entry 与 MediaObject 的数据关系和生命周期。
- 文件元数据、内容类型、大小、图片尺寸和文件名的边界。
- PrepareUpload → 浏览器直传 → CompleteUpload → List/Read/Delete 的服务契约。
- 对象命名空间、缩略图、软删除、永久删除和 cleanup queue。
- 账号隔离、注销、响应脱敏和内部服务认证。
- `bff.timeline.v1.MediaService` 的 proto 草案、错误码和幂等行为。
- Timeline MCP BFF 的路由、传输、令牌、普通账号映射、来源白名单、Bearer、限流、审计、账号状态、`auth_version`、`expected_revision` 和 `mutation_id`。
- 状态转换、权限矩阵、调用图、迁移/回滚/部署顺序、兼容策略和测试矩阵。

### 1.2 已接受的记录基线

KON-99 的 `bff.timeline.v1.TimelineService` 已定义文字记录的 List/Create/Update/Delete/Restore/Purge。记录身份来自内部 auth context，现有请求消息不带 user ID；记录拥有 `revision` 和 `mutation_id`，删除默认进入回收站，永久删除通过明确的 purge 操作完成。现有文字记录的日期、IANA 时区、金额最小单位、书签 URL 校验和稳定排序继续有效。

当前 `timeline_media_objects` 和 cleanup 表属于早期骨架，不能被视为附件服务的完整契约：后续实现必须补齐状态、文件元数据、上传预留、过期时间、尺寸和清理事务语义。本文中的字段名和状态是后续 migration/proto 实现的目标。

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
- MediaObject 的 `entry_id` 是内部关联字段，外部响应只在确有必要时返回 opaque `media_ref`，不返回数据库主键。
- 一个 MediaObject 只有一个原始文件和至多一个服务端生成的缩略图。缩略图不是可独立上传的 MediaObject。
- Entry 删除进入回收站时，附件仍保留并可读；Entry 永久删除时，关联 MediaObject 一并标为 `deleting`，对象 key 进入 cleanup queue，数据库关系在事务中删除或转入不可恢复清理记录。
- cleanup queue 只存随机对象 key、状态、尝试次数和时间，不存 user ID、Entry ID、文件名或 PAT。

### 2.2 MediaObject 字段

| 字段 | 规则 |
| --- | --- |
| `media_ref` | 服务端生成的对外 opaque 引用；不可当作身份或存储 key；不回显内部 UUID |
| `entry_id` | 内部字段；创建和查询必须按当前 owner 约束；不接受跨账号引用 |
| `state` | `pending`、`ready`、`deleting`；禁止客户端写任意状态 |
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
- 所有转移写入审计事件，但审计只允许 opaque media_ref、结果类别、时间和 actor 类别，不含 key、内容、文件名全文或令牌。

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
  rpc DeleteMedia(DeleteMediaRequest) returns (Empty);
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
```

实现可在内部 protobuf 中加入 `request_hash`、`object_version` 和 worker 字段，但这些字段不进入 BFF 或 MCP 的公开响应。`download_url` 和 `thumbnail_url` 每次读取按当前 owner 签发，TTL 不超过 5 分钟；URL 不写日志。

### 3.3 方法语义

- **PrepareUpload**：校验 Entry 属于当前 owner、文件名/MIME/大小白名单和账号配额；在数据库事务内插入 pending MediaObject 与 cleanup reservation，再由 storage 生成 staging key 和短期 PUT URL。相同 `(owner, upload_id)` 且规范请求哈希相同则返回原 pending 预留；哈希不同返回 `IDEMPOTENCY_KEY_REUSED`。
- **浏览器直传**：浏览器只向签名 URL PUT，不能自选 bucket/key 或携带内部 bearer。PUT 失败由客户端重新调用 PrepareUpload 或在有效期内重试。
- **CompleteUpload**：按 owner、media_ref、upload_id 查询 pending；对 staging 对象执行 Head、大小/MIME/魔数/图片尺寸检查，成功后原子更新 ready 并安排缩略图。相同 mutation_id 和请求哈希重放返回原 ready 结果；状态已 ready 且 upload_id 相同也返回原摘要。对象不存在、过期或校验失败不会变 ready。
- **ListMedia/GetMedia**：只返回当前 owner 对指定 Entry 的 ready 或仍可见 pending 摘要；deleting 不出现在普通列表。下载 URL 是读取时签发的短期 URL，服务端先再次检查 owner 和 Entry。
- **DeleteMedia**：必须按 owner 查询。事务内将 ready/pending 改为 deleting、写 cleanup queue，并按 mutation_id 记录结果；worker 删除原图和缩略图，Delete 调用本身不等待对象存储完成。对已 deleting 的相同请求返回幂等成功。

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
| `REVISION_CONFLICT` | `ABORTED` | expected_revision 不匹配 |
| `MUTATION_REPLAY_MISMATCH` | `ALREADY_EXISTS` | mutation_id 的请求摘要不同 |
| `STORAGE_UNAVAILABLE` | `UNAVAILABLE` | 可重试的 storage 故障，不暴露供应商错误 |
| `INTERNAL` | `INTERNAL` | 统一内部错误；日志保留关联 ID |

“找不到”与“没有权限”对外统一为同类 `NOT_FOUND`，避免跨账号猜测；内部安全审计可记录更细原因，但不得回送调用方。

## 5. 账号隔离、注销与隐私

### 5.1 归属边界

- Entry 查询始终包含 `user_id = authenticated owner`；Media 查询始终同时包含 owner 和 Entry/Media 关系。
- `entry_ref`、`media_ref`、mutation_id 和 upload_id 只能在当前 owner 命名空间解释；随机引用不是授权凭证。
- Update/Delete/Purge 先做归属查询再做 mutation；SQL delete/update 的 where 条件再次包含 owner，避免 TOCTOU 或错误调用方造成越权。
- cleanup worker 只消费 opaque object key。管理重试按 queue row 授权，不能用用户传入的 key；对象存储删除使用固定 bucket/prefix allowlist。
- 任何 response mapper 都使用显式白名单。不得直接序列化 Ent、storage SDK、auth context 或 error 对象。

### 5.2 注销

注销工作流先让账号系统使账号和 `auth_version` 失效，再调用 Timeline cleanup。Timeline 事务删除/标记该 owner 的 Entry、MediaObject，并将每个原图和缩略图 key 作为独立 cleanup item 写入队列；队列不保存 user ID。即使账号已不存在，cleanup 仍可重试。注销响应只返回既有 opaque receipt，不包含 Timeline 内部 ID 或对象 key。

注销期间新的 Prepare/Complete/List/Read/Delete 均拒绝；已签发但未使用的浏览器 URL 因 owner 状态或短 TTL 失效。浏览器直传对象即使已经写入，也由 pending reservation 的过期 cleanup 兜底。

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
| `timeline.list` | `timeline:read` | query、category、from_date、to_date、page_size、page_token | 只返回当前账号；稳定排序和分页 token 不泄露内部 cursor |
| `timeline.search` | `timeline:read` | query、page_size、page_token | 是 list 的受限别名，不抓取 URL，不做跨账号搜索 |
| `timeline.create` | `timeline:write` | entry fields、mutation_id | 标题/日期/时区/金额/书签沿用现有校验；重复 mutation 返回原记录 |
| `timeline.update` | `timeline:write` | opaque entry_ref、entry fields、expected_revision、mutation_id | revision 不符返回 `REVISION_CONFLICT`；不能修改 owner 或内部字段 |
| `timeline.trash` | `timeline:trash` | entry_ref、expected_revision、mutation_id | 软删除；重复请求幂等；不接受隐式永久删除参数 |
| `timeline.restore` | `timeline:trash` | entry_ref、expected_revision、mutation_id | 只恢复当前账号回收站记录 |
| `timeline.purge` | `timeline:purge` | entry_ref、expected_revision、mutation_id | 明确不可恢复；附件进入 cleanup queue；默认需要更强 scope |

MCP 工具错误映射为稳定 `code`、可操作的短消息和可选 `retryable`，例如 `AUTH_EXPIRED`、`FORBIDDEN`、`NOT_FOUND`、`VALIDATION_FAILED`、`REVISION_CONFLICT`、`MUTATION_REPLAY_MISMATCH`、`RATE_LIMITED`、`SERVICE_UNAVAILABLE`。所有副作用工具都必须在输入 schema 中要求 mutation_id；BFF 不能替客户端生成并在重试中改变它。

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
  T --> DB[(Timeline DB)]
  M --> DB
  M -->|signed PUT/GET| S[(Storage abstraction)]
  Browser -->|short lived signed PUT| S
  M --> Q[Opaque cleanup queue]
  Q --> S
  Usersrv[usersrv / auth] -->|status + auth_version| BFF
  Usersrv -->|status + token validation| MBFF
```

MCP BFF 只调用 Timeline BFF 内部服务接口，不绕过 owner checks；浏览器直传只使用一次性、短期、固定 key 的签名 URL。

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

“自己”始终由内部会话/令牌映射的 owner 决定，不由请求参数决定。

## 9. 迁移、发布与回滚

### 9.1 数据迁移

1. 先部署只读兼容代码和 cleanup worker，确认现有 `timeline_entries`、`timeline_media_objects`、`timeline_cleanup_objects` 可安全读取。
2. 增量增加 MediaObject 状态、文件元数据、upload reservation、过期时间、尺寸和 request hash 字段；默认值兼容现有空字段。为 `(owner, upload_id)` 建唯一索引时先清理/隔离重复非空值，不能把旧空值误视为同一上传。
3. 增加 cleanup queue 的 opaque key 约束、attempt/lease 字段和必要索引；不把 owner 或 Entry 字段加入队列。
4. 部署 MediaService 和 BFF 路由；先关闭 MCP media scope，完成对象存储 staging prefix 的生命周期规则后再允许 PrepareUpload。
5. 观测 pending 超时、Complete 失败、cleanup 成功率和跨账号拒绝后，再开放浏览器附件入口。
6. 最后按独立评审开放 MCP 记录工具；附件 MCP 另行发布。

### 9.2 回滚

- schema 回滚只允许停用新路由和新写入；保留新列和 queue 行，不能在已有数据写入后执行破坏性 down migration。
- MediaService 回滚到只读/关闭状态时，cleanup worker 必须继续运行，防止 staging 对象泄露；旧 Timeline 文字记录不受影响。
- 若 Complete 校验出现问题，关闭 PrepareUpload 和附件入口，保留 ready 数据和 cleanup；不能把 ready 强行改回 pending。
- MCP 出现安全问题时立即撤销 audience/scope 或关闭路由，递增账号 auth_version/撤销令牌；不删除 Entry 数据，不通过客户端补偿恢复。

### 9.3 兼容策略

- 现有 TimelineService 的字段编号、状态码和文字记录排序保持兼容；附件摘要新增字段只能采用新 proto 字段号和可选语义。
- 老客户端看不到附件时仍可正常读写文字 Entry；老客户端发来的 Entry Update 不得清空服务端已有附件。
- 记录 purge 仍是明确操作；实现附件联动时先写 cleanup reservation，再删除关系，保证旧服务和新 worker 可共存。
- MCP 工具 schema 采用版本化工具名或显式 `v1` 路由；字段只增不改含义。错误 `code` 稳定，message 可本地化。

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
| MCP 协议 | Streamable HTTP、来源白名单、Bearer 位置、分页 token、工具 schema、SSE 错误 | 只接受定义的传输和字段；无 cookie/query token |
| MCP 安全 | 跨账号 ref、scope 越权、撤销后重放、auth_version 变化、限流 | 无数据泄露；写操作停止；Retry-After 正确 |
| 隐私 | 日志、审计、错误、响应、下载 URL、cleanup row 检查 | 不出现 user ID、PAT、对象 key、签名 URL 或敏感内容 |
| 兼容 | 老 Entry 客户端读写、迁移前空附件字段、服务降级 | 文字记录闭环不回归；新列可空且可回滚停写 |
| 运行 | storage 404/5xx、DB 事务失败、queue worker 崩溃/重复执行 | 对象不存在视为成功；可重试；无孤儿 key 的不可控增长 |

必须至少包含真实数据库/内存 storage 的服务测试、BFF 认证与错误映射测试、MCP Streamable HTTP 合同测试，以及浏览器直传的端到端测试。跨账号、注销、Complete 重放、cleanup 失败重试和 MCP 撤销/版本失效是发布阻断项。

## 11. 后续实现交付边界

附件实现票应交付：migration、storage adapter 接入、MediaService proto 生成物、owner checks、Prepare/Complete/List/Get/Delete、缩略图和 cleanup worker、错误映射及服务测试；不得自行添加 tripmap 语义或 MCP 工具。

MCP 实现票应交付：Streamable HTTP 路由、来源白名单、Bearer/audience/scope 校验、普通账号映射、令牌撤销和 auth_version 检查、限流、审计、七个 v1 工具、分页和 revision/mutation 错误映射；附件工具必须另有 scope 和评审。

任何需要改变 Entry 字段、用户身份来源、对象 key 语义、错误码、永久删除默认值或 MCP token audience 的变更，都必须先修订本文并重新评审。
