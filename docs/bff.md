# BFF（公网入口）

一个仓库承载多个业务时，BFF（`cmd/bff`）是唯一的公网入口：按路由表把请求转发到各业务服务，并统一处理跨域、客户端地址和追踪脱敏。业务服务只在内网监听。当前只有 tripmap 一个业务。

## 当前状态

**当前已实现（透明网关）**：BFF 原样转发请求，不改变任何接口行为；前端仍使用同样的 `/api`、`/s/` 路径。

**当前限制**：认证仍在 tripmap。BFF 不校验令牌，`Authorization` 原样转发；账号、会话与 OAuth 仍存于 tripmap 数据库。迁移计划见下文“待实现”。

```
浏览器 → 前端 Nginx（BACKEND_UPSTREAM）→ BFF :18081 ─┬→ tripmap（内网）
                                                    └→ 以后的业务
```

## 路由

路由表在 `internal/bff/gateway/services.go`，每个业务登记三类信息：

| 字段 | 含义 | tripmap |
| --- | --- | --- |
| `Packages` | Connect 过程 `/api/<包名>.<服务>/<方法>` 按包名转发 | `tripmap.v1` |
| `Routes` | 其他 HTTP 路径：精确匹配，或以 `/` 结尾时按前缀匹配，最长者优先；携带凭据的路径填写 `Trace` | `/s`、`/s/`、`/api/share/`（分享令牌）、`/api/notifications/`（退订签名） |
| `Headers` | 浏览器客户端额外发送、需要跨域放行的请求头 | `X-Tripmap-Schema`、`X-Tripmap-Regions` |

- 未登记的路径和不规范路径（含 `..`、`//`）返回 404，不转发。
- 转发保留方法、路径、查询串、请求体（流式，不缓冲）、`Host` 及其余请求头；`Forwarded` / `X-Forwarded-*` 原样传递。
- 服务不可达返回 502。日志只记录服务名和连接错误，不记录可能含令牌的 URL。
- BFF 不设请求体上限：前端 Nginx 限制为 16 MiB，各服务按接口自行限制。

## 统一处理的事项

- **客户端地址**：BFF 按自身的 `client_ip_header`（未设置时用 TCP 对端地址）算出客户端地址，转发时总是覆盖 `X-Real-IP`，客户端无法自选地址。业务服务配置 `auth.client_ip_header: X-Real-IP`，用于登录与分享限流。
- **跨域**：`/api/` 下的跨域请求由 BFF 按 `cors_allowed_origins` 应答，预检请求不转发；业务服务返回的 `Access-Control-*` 头会被去掉，避免出现两套。
- **追踪脱敏**：Butterfly 的 otel 中间件会记录原始路径；登记了 `Trace` 的路径会在追踪里替换为模板（如 `/s/{token}`），查询串置空。

## 健康检查

| 路径 | 行为 |
| --- | --- |
| `GET /ping` | BFF 存活，返回 `{"message":"pong"}` |
| `GET /readyz` | 并发请求每个服务的 `/readyz`（各 2 秒超时）：全部 200 时返回 200，否则 503；`dependencies` 按服务名列出 `ready` |

BFF 启动时不要求业务服务已经可用，只在 `/readyz` 中反映。

## 配置

复制 `config.bff.example.yaml` 为 `config.bff.yaml`（已被 Git 忽略）。与 tripmap 一样由 Butterfly 文件模式读取，`${VAR}` 不展开。

- `upstreams.<服务>`：路由表中每个服务的内网基础地址，如 `http://tripmap:18081`。缺少某个服务、写了路由表中没有的服务名，或地址不是不带路径、查询和账号的 http(s) 地址时，BFF 拒绝启动。
- `client_ip_header`：BFF 前面可信反向代理写入的客户端地址头。位于前端镜像 Nginx 之后设为 `X-Real-IP`，位于 Cloudflare 之后设为 `CF-Connecting-IP`；客户端可直连 BFF 时不要设置。
- `cors_allowed_origins`：允许跨站调用 API 的浏览器来源，规则与 tripmap 原有配置相同；同源部署无需配置。

## 本地开发与部署

`make dev` 同时启动 BFF（`18081`，Vite 代理目标不变）和 tripmap（`18082`）；也可分别运行 `make dev-bff` 和 `make dev-tripmap`。

镜像 `ghcr.io/kongken/trip-map-bff` 由 `deploy/bff/Dockerfile` 构建，容器内端口 18081，配置挂载在 `/config/config.yaml`。从“前端直连 tripmap”切换到 BFF 的顺序：

1. 部署 BFF，`upstreams.tripmap` 指向 tripmap 的内网地址；把 tripmap 原来的 `auth.client_ip_header` 和 `auth.cors_allowed_origins` 分别移到 BFF 的 `client_ip_header` 和 `cors_allowed_origins`。
2. tripmap 设置 `auth.client_ip_header: X-Real-IP`，并只在内网暴露。
3. 前端容器的 `BACKEND_UPSTREAM` 改为 BFF 地址。

回退时把 `BACKEND_UPSTREAM` 改回 tripmap 即可：前端 Nginx 同样会写 `X-Real-IP`，tripmap 自己的跨域处理也仍然保留。

## 新增业务

1. 新建 `proto/<业务>/v1/`、`cmd/<业务>/`、`internal/<业务>/`，需要数据库时使用 `migrations/<业务>/`。服务提供 `/ping` 与 `/readyz`，只在内网监听，并设置 `client_ip_header: X-Real-IP`。
2. 在 `internal/bff/gateway/services.go` 登记 `Packages`、`Routes`、`Headers`；路径或查询串带凭据时填写 `Trace`。
3. 在每份 BFF 配置的 `upstreams` 中加上服务地址（缺少时 BFF 拒绝启动）。
4. 新增 `deploy/<业务>/Dockerfile`，只复制本服务与 `internal/platform`，并在 `.github/workflows/docker-publish.yml` 的矩阵中登记。

各服务镜像只复制本服务与 `internal/platform` 的代码，因此业务之间互相 import，或 `internal/platform` 依赖某个业务时，对应镜像会构建失败。

## 当前限制

- 认证与账号仍在 tripmap（见上文）。
- 没有聚合接口：一个页面需要多个业务的数据时，前端分别调用。
- BFF 自身没有限流，限流仍由各服务完成。
- Butterfly 把指标端口固定为 `:2223`。本地 `make dev` 在同一台机器上运行两个进程时，后启动的进程会记录一条 `error serving metrics` 后继续运行，但没有指标；容器部署各有独立端口，不受影响。

## 待实现

以下为已确定方向，尚未实现。

**认证迁入 BFF**

- 账号、密码哈希、OAuth 绑定、会话和注销凭据迁入 BFF 自己的数据库（同一 PostgreSQL 实例中的独立库）。用户 ID 和令牌哈希原样迁移，用户无需重新登录。
- `AuthService` 与 OAuth 改由 BFF 提供，计划使用 `account.v1` 包；过渡期 BFF 继续响应 `tripmap.v1.AuthService/*`，兼容缓存的旧前端。
- BFF 校验会话后，去掉客户端自带的身份头，改为注入签名的身份声明：BFF 用 Ed25519 私钥签名，业务服务只持有公钥；声明包含用户 ID、会话 ID、短有效期和目标服务。无令牌或令牌无效时按匿名转发，哪些方法允许匿名调用仍由业务服务决定。
- tripmap 保留 `users` 作为业务档案：revision、schema_version、好友字段，以及用户名、昵称、头像的副本，按身份声明补建或更新；现有级联删除继续在 tripmap 内部生效。

**注销编排**

由 BFF 标记注销，再逐个调用业务服务的内部接口 `DeleteUserData`（可重复调用，不经 BFF 对外暴露），最后删除账号与会话；完成后移除 tripmap 中的密码哈希等身份字段。
