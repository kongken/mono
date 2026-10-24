# Logto 退役与手动清理

Logto 已从本仓库的运行时中完全移除（见 [usersrv 规格](spec.md#p6-已实现内容)）：BFF 没有 OAuth/OIDC 代码，没有 `/auth/login`、`/auth/callback/*`，没有 Logto 配置项；两个前端没有 `VITE_OAUTH_APP_ID` 和 `/auth/` 代理，边缘配置示例对 `/auth/*` 返回 404。账号来源只有 usersrv。

本文只列出**仓库之外、需要运维手动处理**的旧数据与外部资源，并给出每一项的目标与范围。核心规则：

- **服务启动、自动迁移和镜像入口都不会 DROP 表、清库、清 Redis 或清对象存储。** 本文的每个动作都是人工、显式、可审核的步骤，执行前请先备份。
- 旧账号、旧用户 ID、旧会话、旧角色、旧管理令牌和旧云端数据**不迁移**（规格第 1 节第 7 条）。不执行本文的清理，新系统也能正常运行；清理只是回收空间、消除遗留数据混用的风险。
- 新旧认证不能同时写同一个业务环境：停旧后端、再开放新登录；失败回退到旧部署时，也不能让新旧用户数据混用（需要恢复备份，见最后一节）。
- 本文的 SQL 与步骤**没有在真实生产环境执行过**，只在本地隔离的 PostgreSQL 17 上由自动化测试检查（见最后一节“验证状态”）。

## 外部资源（不在本仓库内）

仓库只移除了对 Logto 的依赖，**不会**也无法删除外部资源。由运维按各自平台流程处理，并在确认新系统稳定后再做：

| 资源 | 建议动作 |
| --- | --- |
| Logto 租户、应用（trip-map、metro 的回调登记）、角色 `metro-admin`、用户 | 在 Logto 控制台停用/删除；删除前导出所需审计信息。仓库不读取其中任何数据 |
| 部署系统里的 Logto 应用 ID、应用密钥、issuer、`oauth_apps` 配置项和对应 Secret | 从 Consul/配置文件/Secret 中移除；BFF 的新配置没有这些键 |
| 前端构建参数 `VITE_OAUTH_APP_ID` | 从 CI/构建配置中移除（仓库的 Dockerfile 与 workflow 已不再传递） |
| 边缘代理里自定义的 `/auth/` 规则 | 删除或改为 404；仓库示例已改为 404 |

## 旧数据对象一览

### BFF 数据库（`bff`）

| 对象 | 内容 | 现状 | 处理 |
| --- | --- | --- | --- |
| `auth_identities` | 旧 `(issuer, sub)` → 本地业务用户 ID 映射与展示资料 | 运行时不再读写；`auth_browser_sessions` 与 `bff_access_tokens` 的外键已由 `20261024161800_usersrv_sessions.sql` 去掉 | 手动 `deploy/sql/bff/retire_logto_identities.sql` |
| `account_deletion_receipts` | 旧注销回执 | 被新表 `bff_account_deletions` 取代 | 同上脚本 |
| `auth_sessions`、`auth_users`（更早的旧表） | 更早版本遗留 | 不再使用；可能不存在 | 同上脚本（`IF EXISTS`） |
| `auth_browser_sessions.roles`（列） | 旧版会话里的 Logto 角色快照 | 不再读取；Ent 不删除多余的列；列里的值对新系统没有任何含义 | 可选，手动：`ALTER TABLE auth_browser_sessions DROP COLUMN IF EXISTS roles;`（只在所有 BFF 副本升级后；回滚旧版本前必须恢复备份） |
| `auth_browser_sessions` 旧行 | 旧 Cookie 会话（无 `auth_version`） | 第一次使用时被撤销，用户重新登录 | 无需处理；也可以在开放新登录前整表 `DELETE`（不影响结构） |
| 保留 | `auth_browser_sessions`、`bff_access_tokens`、`bff_account_deletions` | 新系统使用 | 不要删除 |

脚本的目标与范围：只作用于 **BFF 数据库**；建议用 `psql -v ON_ERROR_STOP=1 -f` 执行。先在事务中检查 `bff_account_deletions` 和 `auth_browser_sessions` 已存在、没有未完成（`stage <> 'done'`）的注销工作流，再按 `auth_identities`、`account_deletion_receipts`、`auth_sessions`、`auth_users` 的顺序 `DROP TABLE IF EXISTS`（没有 `CASCADE`，遇到意外依赖会失败并停下，由人审核）。检查不通过时脚本整体回滚，不删除任何东西。

### Redis

旧 OAuth state 以 `<BFF key_prefix>oauth:<state>` 为键，TTL 10 分钟；没有任何持久数据。不需要清理，等待自然过期即可。需要提前清除时，用 `SCAN`（不要在生产环境使用 `KEYS`）按 BFF 的 `key_prefix` 加 `oauth:` 匹配后逐个 `DEL`。**不要**对整个 Redis 做 `FLUSHDB`：tripmap、usersrv 与 BFF 共用同一个 Redis 实例时，限流、CSRF 与注销相关的键都在其中。

### tripmap 业务数据库（`tripmap`）

| 步骤 | 脚本 | 目标与范围 |
| --- | --- | --- |
| 删除认证遗留列（必须在所有 tripmap 副本升级后） | `deploy/sql/tripmap/post_import_drop_legacy_auth_columns.sql` | 只作用于 tripmap 库的 `users` 表：去掉 `email`、`provider`、`external_id`、`password_hash`、`disabled`、`deletion_requested_at` 列及相关唯一索引；保留业务 `users` 表和全部业务结构。脚本先确认连的是 tripmap 业务库 |
| **可选、破坏性**：清除测试数据 | `deploy/sql/tripmap/reset_test_profiles.sql` | 只作用于 tripmap 库：`DELETE FROM users`，经外键级联删除全部业务资料、足迹、媒体元数据等；**保留** `media_cleanups`，让延迟清理仍能处理暂存对象。**只能在确认全部云端数据都是可丢弃的测试数据、且尚未接纳真实用户时运行**；停止所有应用副本与维护 worker 后再执行；开放新登录后禁止再运行 |

注意：用户在新系统里注册会得到全新的 UUID，旧业务数据的 `user_id` 与新账号没有任何对应关系；不清理时，这些行成为无主数据（不会被任何新账号读到，也不会被注销流程清理）。

### metro 业务数据库（`metro`）

metro 是新产品，不导入旧数据，没有 Logto 时代的用户数据，不需要退役清理。

### S3 对象存储

tripmap 的对象全部在 `<storage.key_prefix>/media/` 下（默认前缀 `tripmap`）：`media/files/<id>`、`media/thumbnails/<id>`、`media/staging/<uuid>`。metro 与 usersrv 不使用 S3。`reset_test_profiles.sql` **不会**删除任何对象；如果需要物理清除测试对象，请在**删除元数据之前**盘点，否则无法再知道哪些对象属于被丢弃的账号。

手动步骤（目标：一个明确的 bucket 与前缀；范围：只删测试数据）：

1. 确认 bucket 与前缀：读取 `config.tripmap.yaml` 的 `store.s3.<s3_store>.bucket` 与 `storage.key_prefix`；确认这个 bucket 里没有真实用户数据。
2. 在 tripmap 库导出清单（只读）：`SELECT object_key, thumbnail_key, upload_key FROM media_objects;` 和 `SELECT object_key FROM media_cleanups;`，保存为清单文件。清单中的键要加上 `key_prefix/` 才是 bucket 里的完整键。
3. 用 S3 兼容客户端（例如 `aws s3 ls --endpoint-url <endpoint> s3://<bucket>/<prefix>/media/ --recursive`）列出实际对象，与清单对比，确认清单覆盖所有要删的对象，再逐项或按 `<prefix>/media/` 前缀删除。**只删除 `<prefix>/media/`，不要删除 bucket 本身或其他前缀**；`storage-init` 配置的 CORS、生命周期规则属于 bucket 配置，不要动。
4. 删除后抽样 `HEAD` 确认；之后再运行 `reset_test_profiles.sql`（如果要清库）。

## 建议顺序

1. 备份 BFF、tripmap、metro 数据库和对象存储清单；暂停登录与业务写入，停止旧版（含 Logto 时代的 BFF、tripmap）全部副本。
2. 部署 usersrv（独立库、Secret、TLS，见[部署](../deployment.md#usersrv-的部署)），应用 `migrations/usersrv/`、BFF 与 tripmap 的版本化迁移，部署新的 BFF/tripmap/metro/前端。
3. 开放新登录前：按需执行 tripmap 的 `reset_test_profiles.sql`（可选，破坏性）及 S3 清理。
4. 验证（注册→收信→验证→登录→业务写入→重置→重新登录→注销，见[验证清单](spec.md#验证状态)）。
5. 稳定后：BFF 的 `retire_logto_identities.sql`、tripmap 的 `post_import_drop_legacy_auth_columns.sql`、（可选）`roles` 列；然后处理外部资源。
6. 恢复流量。

## 回退

清理脚本执行之后，数据库**不能**直接回滚到旧版本：旧版需要 `auth_identities` 和旧用户列。需要回到旧部署时，恢复第 1 步的备份，并保证旧部署使用旧数据库、新部署使用新的（空）usersrv 库——不要让两套认证写同一个业务环境。新系统创建的账号、会话和业务数据在回退时丢失，回退前应通知用户。

## 验证状态

- `deploy/sql/bff/retire_logto_identities.sql`：`internal/e2e/retire_logto_sql_test.go` 在真实 PostgreSQL 上把 `migrations/bff/` 全部迁移应用后，放入旧身份、旧回执、新会话与进行中的注销工作流，验证存在未完成注销时脚本失败且不删任何表，完成后只删除四个旧表、可重复执行、不影响新系统用的表。
- `deploy/sql/tripmap/post_import_drop_legacy_auth_columns.sql`：`internal/tripmap/integration/migrations_test.go` 验证执行两次的结果与 Ent 模型一致。`reset_test_profiles.sql` 没有自动化测试，只有脚本内的库类型检查。
- 以上都**没有在真实生产数据上演练**。S3 手动步骤只是操作说明：账号注销清理对象的行为由 `internal/e2e/real_s3_deletion_chain_test.go` 在真实 S3 兼容服务（RustFS）上验证，批量清理测试对象的命令没有对任何 bucket 演练，也没有提供自动化批量删除工具。

## 仓库残留检查

代码与配置里不应再有 Logto 入口。用下面的命令检查（排除依赖目录、生成物与已应用的历史迁移）：

```bash
rg -in 'logto|oauth|auth/login|auth/callback|oauth_apps|auth_identities' \
  --glob '!node_modules' --glob '!*.sum' --glob '!migrations/**' --glob '!*_pb*' --glob '!*.pb.go' .
```

允许保留的命中只有：本文与 `docs/usersrv/spec.md`、`docs/metro/spec.md`（已标注为历史记录）以及其他文档里“已退役”的说明；`deploy/sql/bff/retire_logto_identities.sql` 与它的测试 `internal/e2e/retire_logto_sql_test.go`；断言旧路径与旧 RPC 已消失的 `internal/e2e/logto_retired_chain_test.go`；断言界面没有 Logto / OAuth 文案或旧路径不被识别的前端测试（`apps/*/tests/account*.spec.ts`、`apps/trip-map/scripts/*.test.mjs`、`packages/account/src/fragment.test.ts`）；`deploy/reverse-proxy/Caddyfile.example` 里说明 `/auth/*` 返回 404 的注释；`apps/trip-map/src/hooks/useCloud.ts` 里清除旧 `sessionStorage` 键 `shanhai-oauth-pending-v1` 的一行；metro 骑行者 `handle` 保留字表里的 `oauth`；以及 `migrations/bff/` 中已应用的历史迁移（版本化迁移不改写）。
