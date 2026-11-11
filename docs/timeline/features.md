## Timeline 文字记录

Timeline 记录属于当前 OzID 账号，浏览器通过 BFF 的 `bff.timeline.v1.TimelineService` 访问。服务端从内部会话凭证取得用户身份，客户端不能提交用户 ID。

记录需要标题、日期和 IANA 时区，可选发生时间、分类、地点、备注、金额/三位大写币种和网页书签。日期与时间由服务端解析和排序；金额使用最小货币单位整数保存。网页书签只保存经过 scheme、控制字符、长度和内网目标校验的用户输入，不会请求目标 URL。

列表支持搜索、分类和日期范围组合筛选，并按发生时间倒序、ID 升序稳定排序。删除先进入回收站，恢复后回到原排序位置；v1 网页和 MCP 不提供永久删除入口。更新和删除可携带 expected revision，版本不匹配返回冲突；创建携带 mutation id 时重放返回原记录。

账号注销会删除 Timeline 记录，并把关联对象的 opaque key 放入不含用户 ID 的清理队列。清理队列可安全重试。BFF 未配置 Timeline upstream 时不会挂载入口。

网页端已接入时间线、回收站、搜索/筛选、继续加载、详情编辑和附件的 Prepare → 直传 → Complete 流程；BFF 同时提供浏览器 Connect 合同和独立的 Streamable HTTP MCP 入口。附件只接受 JPEG、PNG、WebP、PDF，单个不超过 10 MiB，每条记录最多 20 个；附件下载通过账号隔离的服务端读取，不返回对象存储 key。

当前限制：分类管理、默认分类初始化、金额的“未填写”和 0.00 的有无区分，以及 MCP 的附件工具仍待补齐；MCP 只开放记录和回收站工具。永久删除能力保留在服务端迁移兼容层，v1 网页和 MCP 均不提供入口。

### 并发与重放契约

除创建外，update、trash、restore、purge 必须携带正数 `expected_revision`。服务端在事务内执行带账号、opaque `entry_ref` 和当前 revision 条件的原子更新或删除；条件未命中返回冲突，事务回滚且不写 mutation receipt。响应和 replay snapshot 只含 `entry_ref`，不含内部数据库 UUID。`mutation_id` 必须是 8–128 个 ASCII 字符（`[A-Za-z0-9._:-]`），相同请求重放返回首次快照，不同请求复用同一 ID 会被拒绝。并发重复请求由 `(user_id, mutation_id)` 唯一约束抢占；落败事务回滚后读取已提交的首次 receipt，返回 replay 而非数据库唯一键错误。

迁移先启用 `pgcrypto`，再以随机字节回填 entry/media opaque refs，随后建立唯一索引和 mutation 表；回填失败或 CHECK 约束失败会使迁移事务回滚。生产发布必须先执行 `migrations/timeline/20261107180000_refs_mutations.sql`，再部署依赖新字段的服务。
