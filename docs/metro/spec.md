# 地铁足迹（metro）产品规格

> **后续管理入口迁移，待实现：** [OzID 与统一管理控制台规格](../ozid/spec.md) 定义把已有 metro 管理页面迁到独立控制台，账号入口迁到 OzID，并为浏览器管理面与 MCP 分别增加来源 IP 白名单。本文已实现的地铁领域规则保持不变；当前页面与部署尚未切换。

> **状态：P0–P7 已实现。** 本文是规格，描述要做什么以及为什么这样做；落地后的当前行为以 [功能描述](features.md)、[后端初始化](../backend.md#metro-服务地铁足迹) 和 [BFF](../bff.md) 为准，与下表的偏差和补充决定记在下面。
>
> **账号与角色部分已被 [usersrv 规格](../usersrv/spec.md) 取代。** 下文凡提到 Logto、OAuth、`oauth_apps`、Logto 角色、`roles` scope 与 userinfo 的地方，都是**当时的设计与决策记录，不再是当前行为**：账号是 usersrv 的邮箱＋密码账号，`metro-admin` 是 usersrv 的角色（只能由运维命令 `usersrv-admin` 授予或撤销），BFF 每个请求与每个 MCP 工具调用都向 usersrv 复核账号状态、认证版本与角色，没有 `roles` 快照随会话保存，也没有 `oauth_apps`。目录、业务规则、接口、管理后台与 MCP 令牌的设计仍然有效。当前行为见 [BFF](../bff.md) 与 [功能描述](features.md)。

**实现状态**

| 阶段 | 状态 | 说明 |
| --- | --- | --- |
| P0 共用层 | 已实现 | 角色进会话与凭证、`required_role`、`admin_session_max_age`、账号清理多产品化、`oauth_apps` 沿用既有机制（历史：已被 usersrv 取代） |
| P1 服务骨架与目录读取 | 已实现 | `cmd/metro`、Ent 与 `migrations/metro/`、`CatalogService`（内部与 BFF）、可选上游、`metro_grpc` 就绪探测、模块边界检查 |
| P2 管理接口 | 已实现 | 12 个增删改、4 个管理列表、导入导出、审计、引用完整性、`expected_revision` |
| P3 骑行者 | 已实现 | `RiderService`、`AccountCleanupService`、BFF 适配与清理器 |
| P4 MCP | 已实现 | `bff_access_tokens`、`bff.v1.AccessTokenService`、`POST /mcp/metro`（20 个工具）、按令牌限流；验收 14–16 由 `internal/e2e/metro_mcp_chain_test.go` 覆盖 |
| P5 前端公开部分 | 已实现 | `apps/metro`：首页、城市、专题、公开主页、`/me` |
| P6 前端管理后台 | 已实现 | 四类目录的增删改、导入、导出、审计、令牌；验收 17–21 由 Playwright（模拟 BFF，桌面与手机）覆盖 |
| P7 部署与文档 | 已实现 | `deploy/metro/`、`deploy/metro-web/`、镜像矩阵与 CI、Compose/Caddy 示例、`make dev-metro-web`、文档与 `GLOSSARY.md`；Dockerfile 的输入在临时目录里验证过，但没有实际执行 `docker build` |

**P4–P7 的补充决定与偏差**：

- **MCP 库（待决问题 7）**：不引入 Go MCP 库，手写无状态 JSON-RPC（`initialize`、`ping`、`tools/list`、`tools/call`，批量，通知返回 202）。理由见 [BFF](../bff.md)。
- **工具名与字段**：20 个工具（读 7、写 13）；输入字段使用 camelCase（与浏览器契约的 JSON 形式一致），`metro_get_entity` 以 `entityType` 与 `key` 定位实体。JSON Schema 由 proto 描述符生成。
- **令牌审计（偏差）**：令牌的创建与吊销写 BFF 的 `slog`（令牌 ID、用户、名称、作用域），不写 metro 的 `admin_audit`——审计表属于 metro，BFF 不直接写它。MCP 调用造成的目录写入仍按 8.3 记 `channel=mcp` 与令牌 ID。
- **失败认证限流**：除按令牌限流外，按客户端地址限制失败认证（每分钟 30 次），防止猜测令牌。
- **前端**：自带 History API 路由；管理列表由服务端搜索与分页（“加载更多”）；`tsconfig.app.json` 关闭了 `erasableSyntaxOnly`（生成的契约含 enum）；管理后台按需懒加载。
- **Playwright 使用模拟 BFF**（`page.route`），不连接真实后端；前后端的真实链路只由 Go 的 `internal/e2e` 验证。

**实现中的补充决定**（规格未写明处）：

- 管理列表方法叫 `ListAdminCities`、`ListAdminRollingStocks`、`ListAdminLines`、`ListAdminRollingStockTopics`（与匿名的 `CatalogService` 请求/响应消息同包，避免重名）；每个写方法有自己的响应消息，返回实体（含 `revision`）和写入后的 `catalog_version`。更新与删除按 `id` 定位，线路、城市引用用 `slug`/`code`。
- `Update` 提交的值与已存值相同时不写入（revision、版本、审计都不动）；`status` 未指定按 `operating`；车型专题必须至少给出 `rolling_stock_codes` 或 `cars` 之一（避免“无条件”专题匹配所有线路）；`ExtendedOn` 不得早于 `CommencedOn`。
- 没有任何变化的导入不加 `catalog_state.version`、不写审计；`ExportCatalog` 也登记为 25 秒重任务。导入文档里车型专题的键是 `topics`（与 6.3 示例一致）。
- 内部契约与浏览器契约的管理、骑行者消息逐字段同形，BFF 经 protobuf 线格式转换，由 `TestContractsStayWireCompatible` 守住一致性；目录读取契约仍是手写映射。
- 骑行者 `handle` 先小写化再校验；`GetMyProfile` 在资料不存在时返回默认值而不创建；`MarkLine` 不带年份会清空年份。
- 待决问题 5（Logto 角色声明）：已按 Logto 核心源码核实 `roles` scope 在 userinfo 中返回 `roles`（角色名即 Logto 角色名）；**未对真实租户验证**，部署前仍需确认。

## 1. 背景与目标

旧服务 `metro-srv` 是一个地铁爱好者站点的后端：展示各城市的线路与车型数据，用户登录后标记自己坐过的线路。它的数据来自 Google Sheet 同步，账号来自 Discourse SSO。

本产品把这些功能迁入本仓库，成为第二个产品，目标如下：

1. **新产品，不是迁移。** 不导入旧数据，不兼容旧账号；账号体系与山海足迹完全一致（当时是 Logto + BFF 会话；现为 usersrv + BFF 会话）。
2. **数据由管理员维护。** 取消 Google Sheet 同步，改为管理员通过 admin 接口维护城市、线路、车型和车型专题。
3. **两种入口。** admin 接口既能被管理后台页面使用，也能被 AI 客户端经 MCP 调用。两者走同一套业务规则，不存在绕过校验的后门。
4. **完整的产品形态。** 同时交付后端、BFF 契约和前端应用 `apps/metro`。

### 非目标

- 不做 Google Sheet / CSV 在线同步，不做旧数据导入工具。管理员可用 JSON 批量导入作为日常录入手段（见 6.3），但它不是迁移工具。
- 不做离线优先和本地存储。本产品是在线应用，只有公开页面可以匿名浏览。
- 不做线路地图绘制、换乘规划、实时运营信息。
- 不提供 Logto 之外的登录方式，不保留 Discourse、SAML、Google OAuth。
- 不做多语言。界面和数据以简体中文为主。

## 2. 已确定的决策

| 决策 | 结论 | 理由 |
| --- | --- | --- |
| 服务归属 | 新建业务服务 `metro`，不放进 tripmap | tripmap 是中国省市足迹，这里的 `City` 是地铁城市，同名不同义；`AGENTS.md` 要求一个产品一套目录，业务包之间不互相 import |
| 数据库 | 独立 PostgreSQL 库，Ent 建模，迁移在 `migrations/metro/` | 与现有服务一致；旧服务的 MySQL + MongoDB + Redis 锁全部去掉，用唯一约束代替锁 |
| 账号 | （历史）Logto，经 BFF 会话；现为 usersrv。业务只认内部凭证里的用户 ID | 与 tripmap 一致 |
| 管理员 | （历史）Logto 角色；现为 usersrv 角色，角色名 `metro-admin` | 用户指定；由运维命令授予，不在业务库维护管理员名单 |
| 浏览器入口 | 只经 BFF，契约在 `proto/bff/metro/v1`（包 `bff.metro.v1`） | `AGENTS.md`：BFF 是唯一公网入口 |
| MCP | 由 BFF 对外提供，工具与 admin 接口一一对应 | 业务服务不暴露公网；规则只实现一次 |
| 前端 | `apps/metro`，包名 `@kongken/metro` | 多产品命名约定见 [monorepo](../monorepo.md) |

## 3. 术语

沿用 `AGENTS.md` 和 `docs/agents/domain.md` 的规则：先使用现有用语。以下是本产品新增的概念，实现时同步写入根目录 `GLOSSARY.md`（该文件目前不存在，由 domain-modeling 在落地时创建）。

| 术语 | 含义 |
| --- | --- |
| 城市 | 有地铁的城市，用 `slug` 标识。**不是**山海足迹的“行政区域/城市足迹” |
| 线路 | 一条地铁线，用 `code` 标识，属于一个城市 |
| 车型 | 一种列车型号（旧称 Rolling Stock），用 `code` 标识 |
| 车型专题 | 按车型编码或编组数筛选线路并按城市汇总的统计页 |
| 乘坐记录 | 用户标记“坐过”某条线路，可带年份 |
| 骑行者资料 | 用户的公开主页资料：昵称、简介、社交链接、是否公开 |
| 目录 | 城市、线路、车型、车型专题的统称，由管理员维护 |
| 管理员 | 持有 `metro-admin` 角色的用户（当时是 Logto 角色，现为 usersrv 角色） |
| 管理令牌 | 供 MCP 客户端使用的长期凭证，由管理员创建，可吊销（见 8） |

## 4. 架构

```
浏览器 ──Cookie+CSRF──▶ BFF ──内部 gRPC（Ed25519 凭证）──▶ metro（内网）
MCP 客户端 ──Bearer 管理令牌──▶ BFF ┘
```

目录与登记位置（均按 `docs/monorepo.md` 的多产品约定）：

| 层 | 位置 |
| --- | --- |
| 业务服务入口 | `cmd/metro/` |
| 业务代码 | `internal/metro/{app,application,config,ent/schema,...}` |
| 内部契约 | `proto/metro/v1/` |
| 浏览器契约 | `proto/bff/metro/v1/` |
| BFF 适配 | `internal/bff/metro/`，并提供 `MountRPC`；MCP 在 `internal/bff/metro/mcp/` |
| 迁移 | `migrations/metro/` |
| 前端 | `apps/metro/` |
| 部署 | `deploy/metro/`、`deploy/metro-web/` |
| 文档 | `docs/metro/` |

边界规则不变：`internal/metro` 不 import `internal/tripmap`，`internal/platform` 不依赖任何业务包；镜像只复制本服务与 `internal/platform`。`make check-import-boundaries` 需覆盖新包。

## 5. 数据模型

PostgreSQL，Ent 定义。所有目录实体带 `revision`（从 1 起，每次修改加 1）用于并发控制（见 6.2）。字符串长度上限在服务端强制，下表只列业务含义。

### 5.1 目录

**`cities`**

| 字段 | 说明 |
| --- | --- |
| `id` | UUID，内部主键 |
| `slug` | 唯一，`^[a-z0-9]+(-[a-z0-9]+)*$`，≤32，用于 URL |
| `name` | 展示名，≤32 |
| `header_images` | https 图片地址列表，≤8 项（来自旧 `CityConfig`） |
| `map_svg_url` | https 地址，可空（来自旧 `CityConfig.MapSvg`） |
| `sort_order` | 整数，首页排序，默认 0 |
| `revision`、`created_at`、`updated_at` | |

旧 `City.ProvinceID` 没有被任何接口使用，不带入。

**`rolling_stocks`**

| 字段 | 说明 |
| --- | --- |
| `id` | UUID |
| `code` | 唯一，≤32，如 `A型`、`SFM04` 这类现实编码，不限制字符集但禁止首尾空白和控制字符 |
| `length_m`、`width_m` | 数值，可空 |
| `capacity` | 定员，可空 |
| `revision`、时间戳 | |

**`lines`**

| 字段 | 说明 |
| --- | --- |
| `id` | UUID |
| `code` | 唯一，`^[a-z0-9][a-z0-9-]{1,31}$`，如 `sh-1`；创建后不可改 |
| `city_id` | 外键 → `cities`，`ON DELETE RESTRICT` |
| `name` | 展示名，≤32 |
| `number` | 排序号；`0` 表示未编号，排在最后（保持旧 `GetSortNumber` 行为） |
| `status` | 枚举：`planned`（规划）、`under_construction`（在建）、`operating`（运营中）、`suspended`（停运）、`retired`（已退役） |
| `rolling_stock_id` | 外键 → `rolling_stocks`，可空，`ON DELETE RESTRICT` |
| `cars`、`cars_addition` | 编组数与加长编组数，≥0；展示为 `6` 或 `6/8` |
| `stations` | 站数，≥0 |
| `length_km` | `numeric(8,2)`，≥0 |
| `color` | `#RRGGBB`，服务端不再自动补 `#`，不合法即拒绝 |
| `commenced_on`、`extended_on` | 日期，可空；只存日期，不存时区 |
| `is_loop` | 是否环线 |
| `revision`、时间戳 | |

旧数据里的 `Status = 1` 对应 `operating`。公开列表默认只返回 `operating`，可按状态筛选。

**`rolling_stock_topics`**（来自旧配置 `rsTopic`，改为数据库管理）

| 字段 | 说明 |
| --- | --- |
| `slug` | 唯一，URL 标识 |
| `name` | 展示名 |
| `rolling_stock_codes` | 车型编码列表；为空表示不按车型筛选 |
| `cars` | 编组数列表；线路的 `cars` 或 `cars_addition` 命中其一即满足 |
| `revision`、时间戳 | |

筛选规则保持旧行为：线路必须是 `operating`；`cars` 条件命中；若 `rolling_stock_codes` 非空，线路的车型编码也必须在其中。

**`catalog_state`**：单行表，`version` 单调递增。管理员任何一次成功写入（包括导入）在同一事务内加 1，用于前端缓存校验（见 6.1 的 `known_version`）。

**`admin_audit`**：见 6.4。

### 5.2 用户数据

**`rider_profiles`**（旧 `UserConfig`）

| 字段 | 说明 |
| --- | --- |
| `user_id` | 主键，BFF 业务用户 ID（内部凭证里的 `sub`） |
| `handle` | 唯一，可空，`^[a-z0-9_]{3,32}$`；有保留字表（`admin`、`me`、`api` 等）；用于 `/u/<handle>` |
| `display_name`、`subtitle`、`bio`、`location` | 文本，各有长度上限（bio ≤500） |
| `header_images` | https 图片地址，≤8 项 |
| `twitter`、`instagram`、`weibo`、`facebook`、`github` | 用户名或 https 链接，服务端按平台校验 |
| `is_public` | 默认 `false` |

资料行在第一次写入时按需创建，不由登录触发（避免每个产品都要挂登录钩子）。

**`rider_lines`**（旧 `UserLinesData`，Mongo 文档拆成关系表）

| 字段 | 说明 |
| --- | --- |
| `user_id`、`line_id` | 联合唯一；`line_id` 外键 `ON DELETE RESTRICT` |
| `ridden_year` | 可空，1900 到当年 |
| `created_at`、`updated_at` | |

标记和取消都是单条幂等写入，不再需要分布式锁。统计（城市数、线路数、总里程）在查询时计算，不存储。

## 6. 接口

约定同 `docs/bff.md`：BFF 契约默认受保护，匿名方法用 `platform.v1.anonymous` 声明；写操作自动要求 Origin/CSRF，只读过程登记到 `internal/bff/ingress/write_policy.go`；重任务登记到 `HeavyProcedures`。内部 `metro.v1` 与浏览器契约 `bff.metro.v1` 是两层独立契约，BFF 逐方法适配。

### 6.1 公开与登录用户接口

| 服务.方法 | 访问 | 说明 |
| --- | --- | --- |
| `CatalogService.ListCities` | 匿名 | 城市列表，含线路数；支持 `known_version`，未变化返回 `not_modified` |
| `CatalogService.GetCity` | 匿名 | 按 `slug` 返回城市、线路（按 `number` 排序，未编号在后）、总线路数和总里程 |
| `CatalogService.ListLines` | 匿名 | 可选 `city_slug`、`status`（默认 `operating`） |
| `CatalogService.ListRollingStockTopics` / `GetRollingStockTopic` | 匿名 | 专题详情：命中的线路、按城市分组、每城线路数与里程；**不再返回随机颜色**，由前端按城市 slug 哈希出稳定颜色 |
| `RiderService.GetPublicProfile` | 匿名 | 按 `handle`；资料不存在或 `is_public=false` 一律 `NOT_FOUND`，不泄露是否存在。返回资料、乘坐记录（按城市分组并带线路颜色）和统计 |
| `RiderService.GetMyProfile` / `UpdateMyProfile` | 登录 | 读写自己的资料；`handle` 冲突返回 `ALREADY_EXISTS` |
| `RiderService.ListMyLines` | 登录 | 自己的乘坐记录 |
| `RiderService.MarkLine` / `UnmarkLine` | 登录 | 按 `line_code`；`MarkLine` 重复调用更新年份；`UnmarkLine` 对未标记的线路也成功；线路不存在返回 `NOT_FOUND`；`retired` 和 `planned` 线路不能新标记（已有记录可保留、可取消） |

旧接口的 `PUT /mark_line?action=unmark` 拆成两个 RPC，不再用查询参数区分动作。

### 6.2 管理接口 `AdminService`

内部 `metro.v1.AdminService` 与浏览器 `bff.metro.v1.AdminService` 同形。所有方法要求角色 `metro-admin`（见 7）。

| 方法 | 说明 |
| --- | --- |
| `Create/Update/Delete` × `City`、`RollingStock`、`Line`、`RollingStockTopic` | 共 12 个。`Update` 是整体替换可编辑字段，必须带 `expected_revision`，不符返回 `FAILED_PRECONDITION`（响应带当前 revision），避免两名管理员或 MCP 客户端互相覆盖 |
| `List*`（管理视角） | 返回全部状态，含 `revision`；分页，单页上限 200 |
| `ImportCatalog` | 见 6.3 |
| `ExportCatalog` | 导出全部目录为 6.3 的 JSON，可作为备份和种子数据 |
| `ListAuditLogs` | 见 6.4 |

引用完整性规则：

- 被线路引用的城市、车型不能删除，返回 `FAILED_PRECONDITION`，错误里列出引用数量。
- 有乘坐记录的线路不能删除；应把状态改为 `retired`。
- 修改线路所属城市是允许的；乘坐记录按 `line_id` 关联，不受影响。
- `Line.code` 创建后不可改（用户记录、外部链接都依赖它）。
- 删除车型专题没有引用限制。

校验失败返回 `INVALID_ARGUMENT`，并带字段级错误列表（字段路径 + 原因），前端和 MCP 都可直接展示。

### 6.3 批量导入与导出

用 JSON 代替 Google Sheet，既是日常录入手段，也是备份和环境间搬运方式。

```json
{
  "format": "metro-catalog/1",
  "cities": [{"slug": "shanghai", "name": "上海", "header_images": [], "map_svg_url": "", "sort_order": 0}],
  "rolling_stocks": [{"code": "02A02", "length_m": 22.0, "width_m": 3.0, "capacity": 310}],
  "lines": [{"code": "sh-1", "city": "shanghai", "name": "1号线", "number": 1, "status": "operating", "rolling_stock": "02A02", "cars": 6, "cars_addition": 0, "stations": 28, "length_km": 36.9, "color": "#E4002B", "commenced_on": "1993-05-28", "is_loop": false}],
  "topics": [{"slug": "six-cars", "name": "6 节编组", "rolling_stock_codes": [], "cars": [6]}]
}
```

规则：

- 以自然键 upsert：城市 `slug`、车型 `code`、线路 `code`、专题 `slug`。文件里的线路用 `city` 和 `rolling_stock` 的自然键引用，同文件内或库内已有均可。
- **全有或全无**：任一条校验失败则整体不写入。
- `dry_run=true` 只校验并返回变更预览（新增、更新、无变化各多少条，以及逐条错误），不写入、不加 `catalog_state.version`、不写审计。
- 导入只新增和更新，**不删除**文件里没有的记录。
- 单次最多 2000 条记录，请求体上限 1 MiB。登记为 25 秒重任务。
- 日期格式只接受 `YYYY-MM-DD`（旧的 `2006年01月02日` 不再支持）。
- 导入不能修改已存在线路的 `code`；对已存在记录，导入遵守同样的引用完整性规则。
- 导入对已存在记录不检查 `expected_revision`（整体是管理员的有意覆盖），但每条实际发生变化的记录 `revision` 加 1，审计里逐条记录。

### 6.4 审计

每次管理写入（含导入、令牌创建和吊销）在同一事务内写一行 `admin_audit`：

| 字段 | 说明 |
| --- | --- |
| `actor_user_id` | 执行者 |
| `channel` | `web` 或 `mcp` |
| `token_id` | MCP 渠道时填管理令牌 ID，否则空 |
| `request_id` | BFF 传下的请求 ID |
| `action` | 如 `line.update` |
| `entity_type`、`entity_id` | |
| `before`、`after` | JSON 快照（不含敏感字段） |
| `created_at` | |

只增不改，v1 不自动清理。`ListAuditLogs` 支持按实体、执行者、时间过滤，分页。

### 6.5 账号数据清理

`metro.v1.AccountCleanupService.CleanupUserData`：删除该用户的 `rider_profiles` 与 `rider_lines`，幂等，用户不存在也成功。不删除审计里的 `actor_user_id`（审计是运维记录，不含个人资料）。BFF 的“删除云端数据”流程需要对 metro 也调用（见 9.2）。

## 7. 授权

### 7.1 角色如何到达业务服务

（历史，设计当时的现状）BFF 只保存 Logto 的 `(issuer, sub)` 和展示资料，内部凭证只有 `sub`。需要补足三处：

1. **登录时取角色。** 向 Logto 请求 `roles` scope，把 userinfo 里的 `roles` 随会话保存（会话表新增 `roles` 与 `authenticated_at`）。已存在的会话没有角色，视为非管理员。部署前必须核实所用 Logto 版本在 userinfo 中返回 `roles`（见待决问题 5）。
2. **声明式要求。** 在 `proto/platform/v1/options.proto` 新增方法选项 `required_role`（字符串）。与 `anonymous` 一样，由 BFF 和业务服务**各自独立**执行：BFF 在 ingress 检查会话角色，metro 的服务端拦截器检查内部凭证里的角色。没有该选项的方法不检查角色。
3. **凭证带角色。** 内部凭证（`internal/platform/servicetoken`）增加 `rol` 声明，只包含 BFF 已验证的角色名；`rpcauth.Call` 增加 `Roles`，只有凭证验证通过后才可读。60 秒有效期、`aud`、`mth` 的约束不变。这一改动对 tripmap 无影响（`rol` 可选）。

### 7.2 规则

- 缺少角色返回 `PERMISSION_DENIED`；未登录返回 `UNAUTHENTICATED`。
- 管理员权限的时效：角色在登录时取得，Logto 里撤销角色不会立刻使既有会话失效（与 `docs/bff.md` 中“Logto 远端账号禁用不会实时撤销”的既有限制一致）。为限制窗口，**管理过程要求会话签发不超过 `admin_session_max_age`（默认 12 小时）**，超出返回 `UNAUTHENTICATED` 并提示重新登录。该值可配置。
- 管理员在管理页面之外没有额外的用户数据权限：不能读取其他用户的未公开资料。
- `Me` 响应增加 `roles`（`bff.v1`，共用契约），前端据此显示管理入口；它只是展示开关，真正的检查在服务端。

## 8. MCP

### 8.1 形态

- BFF 在公网主机提供 MCP 端点 `POST /mcp/metro`（MCP Streamable HTTP，**无状态**，不使用 SSE 会话）。路径登记在 `gateway.BFFOwnedRoutes`，是 BFF 自有路由，不转发。
- 只接受 `Authorization: Bearer <管理令牌>`。**忽略 Cookie**，因此不存在 CSRF 面；不设置 CORS 放行（MCP 客户端不是浏览器页面）。
- 每个工具调用被翻译成对 metro 内部 `AdminService` 的 gRPC 调用，凭证里带令牌创建者的 `sub` 和 `metro-admin` 角色。**MCP 路径与 Web 路径调用同一个内部方法**，校验、并发控制、审计行为完全一致。
- 按令牌做 Redis 限流（默认 60 次/分钟，可配置）。

### 8.2 管理令牌

MCP 客户端通常只支持静态请求头或自己的 OAuth 流程，不同客户端差异大，因此 v1 采用 BFF 自管的令牌，不依赖 MCP 客户端的 OAuth 能力（备选方案见待决问题 1）。

| 项 | 规则 |
| --- | --- |
| 创建 | 登录管理员在管理页面创建；需要会话 + CSRF + `metro-admin`；明文令牌（`mcp_` 前缀 + 32 字节随机十六进制）**只显示一次** |
| 存储 | 只存 SHA-256 摘要，与会话令牌的存法一致；表在 BFF 库（它属于身份层），新增 `bff_access_tokens` |
| 字段 | `id`、`user_id`、`name`、`audience`（`metro`）、`scopes`、`roles`（创建时角色快照）、`expires_at`、`last_used_at`、`revoked_at`、`created_at` |
| 作用域 | `metro:read`（只读工具）、`metro:write`（写工具，隐含 read）；创建时选择，默认只读 |
| 有效期 | 必填，最长 90 天 |
| 吊销 | 创建者或任何管理员可吊销；吊销立即生效（每次请求查库） |
| 数量 | 每个用户最多 10 个有效令牌 |
| 角色时效 | 令牌带创建时的角色快照；创建者在 Logto 失去角色后，令牌最长仍可用到过期。这是已知限制，缓解办法是短有效期和吊销（见待决问题 1） |
| RPC | 共用契约 `bff.v1.AccessTokenService`：`CreateAccessToken`、`ListAccessTokens`、`RevokeAccessToken`，不绑定 metro |

### 8.3 工具

工具名与管理接口对应，输入输出为 JSON，字段名与浏览器契约一致。

| 作用域 | 工具 |
| --- | --- |
| `metro:read` | `metro_list_cities`、`metro_list_lines`、`metro_list_rolling_stocks`、`metro_list_topics`、`metro_get_entity`、`metro_export_catalog`、`metro_list_audit_logs` |
| `metro:write` | `metro_create_*`、`metro_update_*`、`metro_delete_*`（`city`、`rolling_stock`、`line`、`topic` 各一）、`metro_import_catalog` |

面向 AI 客户端的安全约束：

- `metro_import_catalog` 的 `dry_run` **默认为 `true`**，必须显式传 `false` 才会写入。
- `update` 和 `delete` 必须带 `expected_revision`，客户端需要先读再写。
- 删除类工具的描述里写明“不可恢复，被引用时会失败”。
- 工具错误以 MCP 的工具错误结果返回，文字内容含字段级校验信息，便于模型自行修正后重试。
- 列表类工具单次最多 200 条，超出需翻页。
- MCP 渠道的写入在审计里标记 `channel=mcp` 与令牌 ID。

v1 不提供 MCP resources 和 prompts，也不提供面向普通用户的工具。

### 8.4 实现依赖

需选择 Go 的 MCP 实现库（官方 `modelcontextprotocol/go-sdk` 或社区库），在实现时按维护状态、Streamable HTTP 无状态支持评估后决定，作为 `internal/bff/metro/mcp` 的内部依赖，不泄漏到其他包。

## 9. 共用层需要的改动

新增第二个产品会暴露当前 BFF 里仍然按“只有 tripmap”写死的部分。这些是 metro 的前置工作，单独成阶段（见 12 的 P0）。

### 9.1 BFF 登记

- `internal/bff/gateway/services.go`：登记 `metro`（`GRPC: true`）。
- `config.bff.example.yaml`：`upstreams.metro`、`grpc.services.metro`；`auth.oauth_apps.metro`（`origin`、`default_path`、`allowed_paths`）；`cors_allowed_origins` 加 metro 前端来源。
- `internal/bff/app/app.go`：调用 `internal/bff/metro` 的 `MountRPC`、MCP 挂载，登记重任务（`ImportCatalog`）。
- `internal/bff/ingress/write_policy.go`：登记新增的只读 RPC。
- `BrowserHeaders` 不变：metro 不需要 `X-Tripmap-*` 头。

### 9.2 账号流程多产品化

`internal/bff/auth/service.go` 现在直接持有 tripmap 的 `ProfileServiceClient` 和 `AccountCleanupServiceClient`。需要改为：

- 登录后的资料同步只对 tripmap 保留（metro 资料按需创建，不挂登录钩子）。
- “删除云端数据”改为对一组产品清理器依次调用，每个都幂等；任一产品失败则整体仍处于“待删除”，重试时对已成功的产品重复调用无副作用。回执保持由 BFF 持有。
- 清理器列表由配置决定哪些产品启用，避免未部署 metro 的环境调用失败。

### 9.3 会话与令牌表

`migrations/bff/` 增加迁移：会话加 `roles`、`authenticated_at`；新增 `bff_access_tokens`。修改 BFF 的 Ent schema 后运行 `make ent-bff`，用 `make bff-migration` 生成迁移。

## 10. 前端 `apps/metro`

技术栈与 `apps/trip-map` 相同：React、Vite、TypeScript、shadcn（`@kongken/ui/*`）、`@phosphor-icons/react`；API 只用 `@kongken/api-client` 的 `bff/v1` 与 `bff/metro/v1`。不 import `apps/trip-map`；两个应用都需要的代码出现时再抽到 `packages/`。

### 10.1 页面

| 路由 | 登录 | 内容 |
| --- | --- | --- |
| `/` | 否 | 城市列表（线路数、总里程）；入口到车型专题 |
| `/cities/:slug` | 否 | 头图、线路卡片（颜色条、站数、里程、车型、编组、开通年份、状态）、城市汇总；登录后每条线路可一键标记“坐过” |
| `/topics/:slug` | 否 | 车型专题：命中线路、按城市分组的线路数与里程图表 |
| `/u/:handle` | 否 | 公开主页：资料、社交链接、按城市分组的乘坐记录、统计；非公开显示统一的“不存在”页 |
| `/me` | 是 | 我的乘坐记录（按城市分组，勾选与年份）、资料编辑、公开开关与 handle、删除我的数据 |
| `/admin` | 管理员 | 见 10.2 |

### 10.2 管理后台

| 页面 | 功能 |
| --- | --- |
| 城市、线路、车型、专题 | 表格（搜索、分页）、创建/编辑表单、删除；编辑时携带 `expected_revision`，遇到冲突提示“已被他人修改”并提供刷新 |
| 导入 | 粘贴或选择 JSON 文件 → 自动 `dry_run` 预览（新增/更新/无变化数量与逐条错误）→ 确认后才写入 |
| 导出 | 下载 6.3 格式的 JSON |
| 审计日志 | 按实体、执行者、渠道、时间过滤 |
| 管理令牌 | 列表、创建（名称、作用域、有效期，明文只显示一次并提醒复制）、吊销；显示 MCP 端点地址和配置示例 |

没有管理员角色的用户不显示入口；直接访问 `/admin` 显示无权限页（服务端仍会拒绝每一次调用）。

### 10.3 前端规则

- 在线应用：不做本地持久化的业务数据；公开目录数据用内存缓存，并用 `known_version` 校验，未变化时不重新拉取。
- 图片只用 `<img>` 加载；`map_svg_url` **绝不内联 SVG**（避免脚本注入），只能作为图片显示。所有用户和管理员提供的链接只接受 `https`。
- 沿用 `packages/ui` 与响应式布局；桌面和手机都要可用。主题色由本产品单独决定，不照搬山海足迹的手账绿（见待决问题 6）。
- 登录走 BFF 的 `/auth/login?app=metro`，返回后用 `Me` 恢复身份；不处理授权码，不保存 token。
- 新增前端应用的步骤按 `docs/monorepo.md`：根 `package.json` 增加 `dev:metro` 等脚本，`apps/metro/` 复制 Vite/TS 配置并加 `@source` 指向 `packages/ui`，`deploy/metro-web/Dockerfile`，CI 与镜像发布矩阵登记。

## 11. 配置与部署

- `config.metro.example.yaml`：本地开发值，包含 `store.db`、`store.redis`、`grpc.*`、`internal_auth`（只放公钥）；真实凭据放被忽略的 `config.metro.yaml`。
- `Makefile`：`dev-metro`、`build`（加 `bin/metro`）；`make dev` 同时启动 BFF、tripmap、metro；`make test` 覆盖新包。
- 新增 `deploy/metro/Dockerfile`、`deploy/metro-web/`，Compose/K8s/反代示例同步增加；镜像发布和 CI 矩阵登记。
- 不需要新增 S3：v1 的头图和地图是外链 URL（见待决问题 3）。
- （历史，已不适用）Logto：创建角色 `metro-admin`，登记 metro 前端应用对应的 BFF 回调。现在用 `usersrv-admin grant-role` 授予角色，见 [部署](../deployment.md#usersrv-的部署)。

## 12. 分阶段交付

每阶段结束都应能独立验证，并同步文档。

| 阶段 | 内容 | 依赖 |
| --- | --- | --- |
| **P0 共用层** | 角色进会话与凭证（7.1）；`required_role` 选项与两侧拦截；账号清理多产品化（9.2）；`bff_access_tokens` 不在此阶段 | 无 |
| **P1 服务骨架与目录读取** | `cmd/metro`、配置、Ent schema 与迁移、健康检查；`CatalogService` 内部与 BFF 契约；BFF 登记；`catalog_state` | P0 中的登记部分 |
| **P2 管理接口** | `AdminService` 全部方法、引用完整性、`expected_revision`、审计、导入导出 | P1，P0 的角色 |
| **P3 骑行者** | `RiderService`、`AccountCleanupService` | P1，P0 的清理多产品化 |
| **P4 MCP** | `AccessTokenService`、BFF 自有 MCP 端点、工具、限流 | P2 |
| **P5 前端公开部分** | `apps/metro` 脚手架、首页、城市页、专题页、公开主页、`/me` | P1、P3 |
| **P6 前端管理后台** | 8 的管理页面、令牌页 | P2、P4、P5 |
| **P7 部署与文档** | Dockerfile、CI、Compose/K8s、`docs/metro/features.md`、`docs/backend.md`、`docs/bff.md`、`docs/README.md`、根 README | 全部 |

P1 以后每个阶段都应包含对应的 Go 测试；P5、P6 包含 Playwright 场景（模拟后端），真实链路由 `internal/e2e` 增加 metro 用例覆盖。

## 13. 验收场景

后端（`internal/metro` 集成测试与 `internal/e2e`）：

1. 匿名调用 `CatalogService.GetCity`：返回该城市线路，未编号线路排在最后；总里程等于线路里程之和。
2. 匿名调用管理方法：`UNAUTHENTICATED`；登录但无 `metro-admin`：`PERMISSION_DENIED`；直接以仅含 `sub`、不含 `rol` 的内部凭证调用 metro 的管理方法：被 metro 拦截器拒绝（证明业务服务独立执行）。
3. 管理员创建线路时 `color` 写成 `E4002B`：`INVALID_ARGUMENT`，字段路径为 `color`；写 `#E4002B` 成功。
4. 两个管理员先后用同一 `expected_revision` 更新同一条线路：第二个得到 `FAILED_PRECONDITION`。
5. 删除被线路引用的城市：`FAILED_PRECONDITION`；删除有乘坐记录的线路：失败，改为 `retired` 后成功，且用户的历史记录仍可读。
6. 导入含一条非法线路的 JSON：`dry_run=false` 时整体不写入，`catalog_state.version` 不变；`dry_run=true` 返回逐条错误且不写审计。
7. 导入合法 JSON 两次：第二次全部“无变化”，`revision` 不增加。
8. 目录写入成功后，`ListCities(known_version=旧值)` 返回完整数据，`known_version=新值` 返回 `not_modified`。
9. 用户 A 标记线路 X 两次：只有一条记录，年份以最后一次为准；取消未标记的线路成功。
10. 用户 B 的资料 `is_public=false`：`GetPublicProfile` 返回 `NOT_FOUND`，与不存在的 handle 无法区分；设为公开后可见；handle 与他人冲突返回 `ALREADY_EXISTS`。
11. 用户 A 无法读取或修改用户 B 的乘坐记录。
12. 删除云端数据后，A 的资料和乘坐记录消失；tripmap 数据也被清理；任一产品清理失败时保持待删除并可重试。
13. 管理会话签发超过 `admin_session_max_age`：管理方法返回 `UNAUTHENTICATED`，普通用户方法不受影响。

MCP：

14. 用只读令牌调用写工具：被拒绝；用写令牌调用 `metro_import_catalog` 且不传 `dry_run`：只返回预览。
15. 吊销令牌后立即调用：失败；过期令牌：失败；携带有效 Cookie 但无 Bearer：失败。
16. MCP 的更新写入审计，`channel=mcp` 且带令牌 ID；在 Web 和 MCP 上对同一条数据的并发更新互相触发 `expected_revision` 冲突。

前端（Playwright，模拟后端；桌面与手机尺寸）：

17. 匿名浏览首页 → 城市页 → 专题页；尝试标记线路时被引导登录。
18. 登录用户在城市页标记线路，`/me` 立即反映；取消后消失。
19. 非管理员看不到管理入口，直接访问 `/admin` 显示无权限页；管理员可创建城市和线路，遇到修订冲突看到提示。
20. 导入页：粘贴含错误的 JSON，预览显示错误且“确认导入”不可用；修正后预览正确，确认后目录页显示新数据。
21. 创建管理令牌：明文只显示一次，刷新后列表只显示名称、作用域和过期时间。

## 14. 待决问题

以下需要产品或运维给出结论后才能定稿；每项都给出了建议。

1. **MCP 认证方式。** 建议 v1 用本文的 BFF 管理令牌（兼容所有 MCP 客户端，可吊销）。备选是让 MCP 客户端走 Logto 的 OAuth，BFF 校验 Logto 签发的 JWT：权限时效更好（短期令牌、角色即时），但依赖 MCP 客户端对 OAuth 授权与动态注册的支持，Logto 侧也需配置 API 资源。接受“令牌创建者失去角色后令牌最长可用到过期”这一限制吗？如果不接受，可以在每次使用令牌时通过 Logto 管理 API 复核角色，代价是增加对 Logto 管理 API 凭据的依赖。
2. **公开主页默认值。** 本文定为默认私有、需要 handle、用户主动公开。旧站是默认公开，是否需要保持？
3. **图片与 SVG 来源。** 本文定为外链 https URL，不做上传。若需要后台上传，需新增 S3 和媒体清理，范围明显变大，建议作为后续阶段。
4. **角色粒度。** 本文只有一个 `metro-admin`。是否需要“只读运维”或“编辑但不能删除/导入”等细分角色？`required_role` 机制支持以后增加。
5. **Logto 角色声明（技术核实）。** 需要在实际使用的 Logto 租户确认：请求 `roles` scope 后，userinfo 是否返回 `roles`，以及角色名是否就是 `metro-admin`。若不返回，则退回到登录时调用 Logto 管理 API 查询，P0 需要相应增加管理 API 凭据配置。
6. **产品命名与视觉。** 产品对外名称、域名、主题色和图标尚未确定；本文中的 `metro` 是工程内部名称。
7. **MCP 库选型。**（已决定）手写，见文首“补充决定”；需要 resources 或服务端推送时再评估官方 SDK。
8. **已有线路的批量下线。** 当前没有批量改状态的接口，可通过导入 JSON（同 `code`、新 `status`）完成；是否需要专门的批量接口？

## 15. 完成时的文档同步清单

按 `AGENTS.md` 的要求，在对应阶段随代码一起完成：

- `docs/metro/features.md`：用户可见规则与第 13 节中的前端验收场景（P5、P6）。
- `docs/backend.md`：metro 服务的目录、配置、数据库模型、迁移、健康接口（P1 起）。
- `docs/bff.md`：`bff.metro.v1` 路由、`required_role`、`bff.v1.AccessTokenService`、MCP 端点、账号清理多产品化、`oauth_apps.metro`、`/readyz` 新增的 `metro_grpc`（P0 起）。
- `docs/monorepo.md`：新增的 `apps/metro`、命名约定中的第二个产品示例。
- `docs/README.md`：入口表登记本目录文档。
- 根 `README.md`：启动命令（`make dev` 含 metro、`npm run dev:metro`）。
- 根目录 `GLOSSARY.md`：第 3 节新增术语。
- 本文：已落地的部分迁出并标注，避免与“当前已实现”混淆。
