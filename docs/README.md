# 项目文档

这里记录仓库的当前约定与各产品的功能和数据说明，供产品讨论和后续开发共同使用。产品文档放在 `docs/<产品>/`（山海足迹：`docs/trip-map/`；地铁足迹：`docs/metro/`），其余文档描述共用的后端、BFF、部署和仓库布局。

| 文档 | 内容 | 何时更新 |
| --- | --- | --- |
| [功能描述](trip-map/features.md) | 使用流程、交互规则、统计口径、验收场景和功能限制 | 用户可见功能发生变化时 |
| [数据与地图说明](trip-map/data-and-maps.md) | 足迹结构、本地保存、备份格式、地图来源及区域索引 | 存储、备份或地图数据发生变化时 |
| [公开分享页与预览图](trip-map/sharing.md) | F6 的快照模型、公开接口、隐私规则、预览图资源、部署配置、限制和测试覆盖 | 分享功能、配置或限制变化时 |
| [前后端 monorepo 布局](monorepo.md) | 前端 workspaces（`apps/`、`packages/`）与 Go 服务的目录约定、共享包职责、新增应用步骤、Cloudflare Pages 构建设置 | 目录结构、workspace 包或静态托管构建设置变化时 |
| [Docker 镜像与 Actions](docker-images.md) | 前端、BFF、tripmap、metro 与 usersrv 镜像，构建发布触发和运行配置 | 镜像或发布流程变化时 |
| [部署与发布](deployment.md) | 同源反代、Secret、密钥轮换、内网监听、健康探针、Legacy 导入 | 部署拓扑或运维流程变化时 |
| [BFF（公网入口）](bff.md) | 公网 Connect、内部 gRPC 与 Ed25519 凭证、跨域与配置 | BFF 路由、配置或拓扑变化时 |
| [OzID 与统一管理控制台规格](ozid/spec.md) | 已确认范围；T1（登录、重新认证、安全返回）、T2（统一注册验证与密码重置邮件）、T3（账号资料、密码与设备会话管理）与 T4（受控统一管理控制台与用户查询）、T5（普通账号状态与 metro-admin 的安全修改）、T6（账号安全与管理审计查询）、T7（账号注销与失败恢复）T8（metro 管理功能迁入控制台，浏览器管理周界随之切换）与 T9（MCP 独立来源白名单 `mcp.allowed_cidrs`）已实现，其余待实现：产品入口收敛及分阶段验收 | 用户中心或统一管理控制台的规格、范围与评审结论变化时 |
| [OzID 与控制台整体验收（T12）](ozid/acceptance.md) | #77 验收项到实现与测试的映射、真实网关与浏览器验收（两组保留域名）、最终来源策略、旧邮件 TTL 过渡清单、已定位修复的失败与运维待验项 | 验收范围、来源策略或网关示例变化时 |
| [OzID 功能描述（T1、T2、T3）](ozid/features.md) | 已实现：`apps/ozid` 的登录、注册、找回密码、邮件落地页（注册验证、密码重置）、概览、密码重新认证，以及资料、改密码与设备会话页面（含 BFF `RevokeOtherSessions`），邮件链接统一落到 OzID 与返回上下文随挑战保存，BFF `ResolveNavigation` 与 `navigation` 注册表、安全返回规则、同站点跨域会话不变量、构建/镜像/运行、验证分层与限制 | OzID 页面、导航规则、配置或部署变化时 |
| [统一管理控制台功能描述（T4、T5、T6、T8）](admin/features.md) | 已实现：T8 的 metro 目录管理（城市、线路、车型、专题、导入/导出、目录审计、管理令牌）迁入控制台，其管理接口与令牌接口并入管理周界，验证密码前的输入草稿只存在本标签页、失败时不离开页面（`@kongken/metro-core` 共享纯函数）；T6 的账号安全与管理审计查询（`ListSecurityAudit`：渠道与操作者显式区分、筛选与稳定翻页、重放的精确原始关联、读取本身被审计、脱敏）；T5 的普通账号禁用/启用与 `metro-admin` 授予/撤销（原因、目标版本、变更 ID 幂等回执、近 15 分钟密码认证、同事务审计）；`apps/admin` 独立控制台（匿名跳 OzID、无权限不循环、按真实角色分区）、只读用户查询（`UserAdminService.ListUsers/GetUser`）、管理周界（来源白名单、受信代理链、控制台 Origin、会话与 `ozid-admin` 角色，页面/资源/SPA fallback 与 BFF 管理读接口各自独立限制）、`ozid-admin` 的 CLI 授予与注销保护、`admin-web` 镜像与网关示例、验证分层与限制 | 控制台页面、周界规则、配置或部署变化时 |
| [后端初始化](backend.md) | 已确定的技术栈、开发环境、文件与 Consul 配置及路径前缀、健康接口、数据库模型、版本化数据库迁移（`migrations/tripmap/`）和验证方式 | 后端基础设施、接口、schema 或迁移文件变化时 |
| [后端功能调研](trip-map/backend-research.md) | 引入后端后可扩展的功能、数据模型与接口草案、同步与迁移、技术选型倾向、分阶段交付和待决问题（待实现，非当前行为） | 后端方向的决策或范围发生变化时 |
| [地铁足迹功能描述](metro/features.md) | 页面与登录、标记与公开主页、管理后台、导入导出、管理令牌与 MCP 的用户可见规则、验收场景（前端 17–21）与限制 | 用户可见功能发生变化时 |
| [usersrv 自建用户系统规格](usersrv/spec.md) | 取代 Logto 的邮箱＋密码用户系统：已确认要求、数据与安全参数、分阶段交付（P1–P6，均已实现）、逐阶段实现状态登记与验证状态（实现后的当前行为以 [后端初始化](backend.md#usersrv-服务账号核心p1)、[邮件模块](backend.md#usersrv-邮件模块p2)与[账号流程](backend.md#usersrv-账号流程p3)为准） | 规格范围或阶段状态变化时 |
| [Logto 退役与手动清理](usersrv/retirement.md) | 退役的外部资源、旧表、Redis 状态、测试数据与 S3 对象：目标、范围、顺序、回滚与验证状态；全部手动执行 | 退役对象或脚本变化时 |
| [usersrv 邮件厂商手动冒烟测试](usersrv/mail-smoke-test.md) | 启用 Mailgun、SendGrid、SMTP、SMTP2GO 前由运维手动执行的验证步骤；自动化测试只连本地假服务，不连真实厂商 | 邮件 adapter、跟踪或配置要求变化时 |
| [地铁足迹（metro）规格](metro/spec.md) | 第二个产品的目标、决策、数据模型、接口、admin 与 MCP（账号与角色已改由 usersrv 提供）、`apps/metro`、分阶段交付、验收场景与待决问题（P0–P7 均已实现；实现后的当前行为以功能描述、[后端初始化](backend.md#metro-服务地铁足迹) 与 [BFF](bff.md) 为准） | 规格范围或决策变化时 |
| [项目协作说明](../AGENTS.md) | 开发约定、文档同步和验证要求 | 协作流程发生变化时 |
| [Issue tracker](agents/issue-tracker.md) | GitHub Issues 操作、父子任务和依赖约定 | 任务跟踪方式变化时 |
| [Triage labels](agents/triage-labels.md) | 五类标准分流角色与 GitHub 标签映射 | 标签词汇变化时 |
| [Domain docs](agents/domain.md) | single-context 领域词汇表与 ADR 的布局和读取规则 | 领域文档组织方式变化时 |
| [项目运行说明](../README.md) | 安装、开发、构建和地图维护命令 | 项目启动方式发生变化时 |

## 维护方式

功能修改与对应文档一起完成。先描述用户可观察到的结果，再写规则和限制；使用“省级区域”“城市足迹”“省份独立标记”等已有术语。

现有文档描述已实现行为。新增文档应在上表登记，尚未实现的功能应明确标为待实现。区域数量以地图检查脚本和当前数据为准。
