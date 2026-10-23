# 项目文档

这里记录仓库的当前约定与各产品的功能和数据说明，供产品讨论和后续开发共同使用。产品文档放在 `docs/<产品>/`（山海足迹：`docs/trip-map/`；地铁足迹：`docs/metro/`），其余文档描述共用的后端、BFF、部署和仓库布局。

| 文档 | 内容 | 何时更新 |
| --- | --- | --- |
| [功能描述](trip-map/features.md) | 使用流程、交互规则、统计口径、验收场景和功能限制 | 用户可见功能发生变化时 |
| [数据与地图说明](trip-map/data-and-maps.md) | 足迹结构、本地保存、备份格式、地图来源及区域索引 | 存储、备份或地图数据发生变化时 |
| [公开分享页与预览图](trip-map/sharing.md) | F6 的快照模型、公开接口、隐私规则、预览图资源、部署配置、限制和测试覆盖 | 分享功能、配置或限制变化时 |
| [前后端 monorepo 布局](monorepo.md) | 前端 workspaces（`apps/`、`packages/`）与 Go 服务的目录约定、共享包职责、新增应用步骤、Cloudflare Pages 构建设置 | 目录结构、workspace 包或静态托管构建设置变化时 |
| [Docker 镜像与 Actions](docker-images.md) | 前端、BFF、tripmap 与 metro 镜像，构建发布触发和运行配置 | 镜像或发布流程变化时 |
| [部署与发布](deployment.md) | 同源反代、Secret、密钥轮换、内网监听、健康探针、Legacy 导入 | 部署拓扑或运维流程变化时 |
| [BFF（公网入口）](bff.md) | 公网 Connect、内部 gRPC 与 Ed25519 凭证、跨域与配置 | BFF 路由、配置或拓扑变化时 |
| [后端初始化](backend.md) | 已确定的技术栈、开发环境、文件与 Consul 配置及路径前缀、健康接口、数据库模型、版本化数据库迁移（`migrations/tripmap/`）和验证方式 | 后端基础设施、接口、schema 或迁移文件变化时 |
| [后端功能调研](trip-map/backend-research.md) | 引入后端后可扩展的功能、数据模型与接口草案、同步与迁移、技术选型倾向、分阶段交付和待决问题（待实现，非当前行为） | 后端方向的决策或范围发生变化时 |
| [地铁足迹功能描述](metro/features.md) | 页面与登录、标记与公开主页、管理后台、导入导出、管理令牌与 MCP 的用户可见规则、验收场景（前端 17–21）与限制 | 用户可见功能发生变化时 |
| [地铁足迹（metro）规格](metro/spec.md) | 第二个产品的目标、决策、数据模型、接口、Logto 角色授权、admin 与 MCP、`apps/metro`、分阶段交付、验收场景与待决问题（P0–P7 均已实现；实现后的当前行为以功能描述、[后端初始化](backend.md#metro-服务地铁足迹) 与 [BFF](bff.md) 为准） | 规格范围或决策变化时 |
| [项目协作说明](../AGENTS.md) | 开发约定、文档同步和验证要求 | 协作流程发生变化时 |
| [Issue tracker](agents/issue-tracker.md) | GitHub Issues 操作、父子任务和依赖约定 | 任务跟踪方式变化时 |
| [Triage labels](agents/triage-labels.md) | 五类标准分流角色与 GitHub 标签映射 | 标签词汇变化时 |
| [Domain docs](agents/domain.md) | single-context 领域词汇表与 ADR 的布局和读取规则 | 领域文档组织方式变化时 |
| [项目运行说明](../README.md) | 安装、开发、构建和地图维护命令 | 项目启动方式发生变化时 |

## 维护方式

功能修改与对应文档一起完成。先描述用户可观察到的结果，再写规则和限制；使用“省级区域”“城市足迹”“省份独立标记”等已有术语。

现有文档描述已实现行为。新增文档应在上表登记，尚未实现的功能应明确标为待实现。区域数量以地图检查脚本和当前数据为准。
