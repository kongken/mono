# 后端初始化

## 当前状态与技术栈

后端基础设施已初始化，参考 `neo-box` 的技术栈和分层：Go 1.26.5+、Butterfly、Gin、ConnectRPC、Ent、PostgreSQL、Redis、AWS SDK for Go v2（S3）。依赖版本以 `go.mod` 为准，RPC 契约使用 Buf 生成 Go 与 TypeScript 代码。

当前已实现服务启动、依赖检查、Ent 建表、S3 对象存储封装与基础客户端。当前接口只提供健康状态；登录、足迹读写、云同步、上传签名、备份和分享仍待实现。前端城市足迹继续保存在浏览器，初始化后端不会迁移或覆盖本地记录。

Redis 客户端已接入，后续用于会话、缓存或限流；本次没有把城市足迹存入 Redis。对象内容使用 S3，PostgreSQL 只存业务记录和媒体元数据。

## 项目结构

| 路径 | 职责 |
| --- | --- |
| `cmd/tripmap/` | Butterfly 服务入口，配置加载、启动、退出资源清理 |
| `internal/app/` | Bootstrap、依赖注入、Gin 路由 |
| `internal/config/` | 业务配置及必填存储名称校验 |
| `internal/application/` | ConnectRPC 服务实现 |
| `internal/health/` | 有超时的依赖就绪检查 |
| `internal/blobstore/` | S3 Put/Get/Delete 与 bucket 检查，不依赖具体供应方 |
| `internal/ent/schema/` | 数据库 schema 源文件 |
| `internal/ent/` | 生成的 Ent 客户端与迁移结构 |
| `proto/tripmap/v1/` | Protobuf 接口定义 |
| `pkg/proto/`、`src/gen/` | 生成的 Go / TypeScript 契约 |
| `src/api/system.ts` | 前端 Connect-Web 健康状态客户端 |
| `cmd/storage-init/` | 显式初始化配置中指定的 S3 bucket |
| `internal/integration/` | 可选的实际 PostgreSQL、Redis、S3 集成测试 |

前端仍位于根目录 `src/`，无需搬迁到 `front/`。后端 Go module 位于根目录；前后端分别构建。

## 本地开发

需要 Go 1.26.5+、Docker Compose、Node.js/npm。重新生成 RPC 代码另需 Buf，生成插件由 `buf.gen.yaml` 指定。

```bash
cp config.example.yaml config.yaml
make deps-up
make storage-init
make dev
```

另开终端启动前端：

```bash
npm install
npm run dev
```

Compose 只启动本项目的开发依赖，使用命名卷保存数据。S3 开发实例使用 [RustFS 官方容器](https://docs.rustfs.com/en/installation/container/docker)，提供持久化 S3 兼容接口；生产环境可配置 AWS S3 或其他兼容供应方，服务代码无需更换。

| 服务 | 本地地址 |
| --- | --- |
| PostgreSQL | `127.0.0.1:15432`，数据库 `tripmap` |
| Redis | `127.0.0.1:16379` |
| S3 API | `http://127.0.0.1:19000` |
| S3 开发控制台 | `http://127.0.0.1:19001` |
| Go 后端 | `http://127.0.0.1:18081` |
| Vite 前端 | 启动时输出地址，通常为 `http://localhost:5173` |

端口避开了现有 `neo-box` 开发服务。Vite 代理 `/api`、`/ping` 和 `/readyz` 到 `18081`；若改变后端端口，同时更新代理配置。`make deps-down` 仅停止并移除本项目容器，保留数据卷。

## 配置与启动规则

`config.example.yaml` 提供本地示例凭据。复制后的 `config.yaml` 和 `.env` 已被 Git 忽略，实际部署凭据只放在未跟踪配置或挂载的 Secret 中。

Butterfly 从 `BUTTERFLY_CONFIG_FILE_PATH` 读取同一个 YAML，`BUTTERFLY_CONFIG_TYPE=file` 启用文件模式。与 `neo-box` 一致，YAML 中的 `${VAR}` 不会自动展开。`.env.example` 提供文件模式与端口示例；`make dev` 直接设置这些变量，无需自动加载 `.env`。

- `db_store` 指向 `store.db` 的命名连接，必须为 PostgreSQL。
- `redis_store` 指向 `store.redis` 的命名连接。
- `storage.s3_store` 指向 `store.s3` 的命名客户端，必须配置 bucket。
- `storage.key_prefix` 设置对象 key 前缀，示例为 `tripmap`。
- `auto_migrate: true` 在启动时通过 Ent 创建或补充表结构。本地示例开启；生产 schema 变更应先评审迁移，关闭自动迁移后仍需自行执行经过验证的迁移。本次尚无独立的版本化迁移流水线。

启动时检查 PostgreSQL、Redis 与 S3 bucket，缺失或不可连接则启动失败。后端不会在启动时自动创建 bucket；`make storage-init` 是独立显式操作，bucket 已存在且可访问时直接成功，不会配置公共访问策略。

S3 兼容服务可设置 endpoint 和 `use_path_style: true`；AWS S3 可省略 endpoint 并按供应方配置 region 和访问凭据。该 Butterfly 版本使用配置文件中的静态凭据。PostgreSQL 密码在框架内直接拼接 DSN，需避免 `@ : / ? # %` 等未转义字符。

## 已实现接口

| 接口 | 行为 |
| --- | --- |
| `GET /ping` | 存活检查，返回 `{"message":"pong"}` |
| `GET /readyz` | 检查 PostgreSQL、Redis 和 S3 bucket；全部可用返回 200，否则 503 |
| `POST /api/tripmap.v1.SystemService/GetStatus` | ConnectRPC 健康状态，返回 `ready` 和各依赖的 `name` / `ready` |

就绪检查对每个依赖设置 2 秒超时并发执行，只返回状态，不返回原始驱动错误、连接字符串或凭据。RPC 请求成功时 HTTP 状态表示 RPC 调用结果，依赖可用性应读取响应中的 `ready`；运维探针使用 `/readyz`。

```bash
curl --fail http://127.0.0.1:18081/ping
curl --fail http://127.0.0.1:18081/readyz
curl --fail http://127.0.0.1:18081/api/tripmap.v1.SystemService/GetStatus \
  -H 'Content-Type: application/json' \
  -H 'Connect-Protocol-Version: 1' \
  -d '{}'
```

健康接口无用户数据，无需登录。本次没有开放用户或媒体读写接口；新增私有业务接口前应先接入认证、用户隔离和服务端区域索引校验。前端 `getBackendStatus()` 已可调用生成客户端，但界面尚未主动请求后端。

## 数据库模型

| 表 | 关键字段与约束 |
| --- | --- |
| `users` | UUID 字符串 ID、可空唯一邮箱、显示名、预留 revision、创建/更新时间 |
| `city_visits` | 所属用户、区域 ID/名称、所属省份、颜色、日期字符串、备注；`user_id + region_code` 唯一 |
| `province_marks` | 所属用户、省份 ID、颜色；`user_id + province_code` 唯一 |
| `media_objects` | 所属用户、唯一 object key、内容类型、非负字节大小、pending/ready 状态 |

城市足迹与省份独立标记分表。删除城市不影响省份独立标记；用户归属为必填外键，删除用户时数据库级联删除这些元数据。级联删除媒体元数据不会删除 S3 实体对象，未来账号删除功能需另行实现对象清理。

时间戳默认 UTC。旅行日期仍是 `YYYY-MM-DD` 字符串，允许空值，不做时区转换。Ent 验证日期是否为有效日历日期，并限制颜色为前端五种色值、备注按与前端一致的 UTF-16 长度限制为最多 1000。完整区域有效性校验尚待业务服务实现。

当前模型保留一用户一城市一足迹，预留表不等于已实现云同步或附件上传。现有浏览器备份格式仍为 `version: 1`，尚无从本地数据到上述表的迁移逻辑。

## 生成、构建与验证

```bash
make ent            # 修改 Ent schema 后生成客户端
make buf            # 修改 proto 后生成 Go / TypeScript 契约
buf lint
make test           # Go 单元测试；未配置连接时跳过实际存储集成测试
make build          # bin/tripmap
npm test
npm run build
```

实际存储集成测试在开发依赖已启动且 bucket 已初始化后运行：

```bash
make test-integration
```

该命令使用 Compose 示例的本地地址和开发凭据。使用其他测试环境时，设置 `TRIPMAP_TEST_POSTGRES_DSN`、`TRIPMAP_TEST_REDIS_ADDR`、`TRIPMAP_TEST_REDIS_PASSWORD`、`TRIPMAP_TEST_S3_ENDPOINT`、`TRIPMAP_TEST_S3_ACCESS_KEY`、`TRIPMAP_TEST_S3_SECRET_KEY`、`TRIPMAP_TEST_S3_BUCKET`，运行 `go test -v ./internal/integration`。

PostgreSQL 测试创建独立随机 schema 并清理，Redis 测试使用随机 key 和 TTL，S3 测试仅操作随机测试对象并删除。测试覆盖城市唯一约束、非法日期/颜色、省份标记独立性、用户级联、Redis 读写和 S3 上传下载删除。

`Dockerfile` 仅构建后端镜像，运行时挂载 `/config/config.yaml`；容器中连接 Compose 依赖时，配置地址使用服务名和容器端口，而不是宿主机的 `127.0.0.1`。

## 下一阶段

从 [后端功能调研](backend-research.md) 中选择业务切片继续实现：先明确认证和用户归属、修复本地读取失败保护，再实现足迹读写及迁移。基础设施初始化不代表 F0–F12 已完成。
