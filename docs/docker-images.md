# Docker 镜像与 GitHub Actions

## 独立镜像

镜像命名沿用 neo-box：tripmap 后端使用仓库名，前端增加 `-front` 后缀，BFF 增加 `-bff` 后缀。它们通过同一个 Actions workflow 的矩阵分别构建，构建上下文均为仓库根目录。

| 镜像 | Dockerfile | 运行内容 |
| --- | --- | --- |
| `ghcr.io/kongken/trip-map` | 根目录 `Dockerfile` | tripmap 业务服务，18081（HTTP）与 18083（内部 gRPC over TLS）端口，运行时挂载配置、证书与 BFF 公钥，只在内网暴露 |
| `ghcr.io/kongken/trip-map-bff` | `deploy/bff/Dockerfile` | BFF 公网入口，18081 端口，运行时挂载配置、CA 与签名私钥（见 [BFF](bff.md)） |
| `ghcr.io/kongken/trip-map-front` | `deploy/frontend/Dockerfile` | Nginx 提供 Vite 静态文件，80 端口，同源转发后端请求 |

`.github/workflows/docker-publish.yml` 使用官方 Checkout、Buildx、GHCR 登录、metadata 和 build-push actions，按已核对的 commit SHA 固定版本。构建 linux/amd64 镜像，各镜像使用独立 GitHub Actions 缓存。

## 触发与标签

| 触发事件 | 标签 | 发布行为 |
| --- | --- | --- |
| 推送 main | `main`、`sha-<短提交ID>` | 发布全部镜像 |
| 推送其他分支 | `nightly`、`sha-<短提交ID>` | 发布全部镜像，nightly 指向最后一次完成的分支构建 |
| 推送 `vX.Y.Z` | `X.Y.Z`、`X.Y`、`X`、`latest`、SHA 标签 | 发布全部版本镜像 |
| 面向 main 的 PR | `pr-<编号>`、SHA 标签 | 只构建，不登录或发布到 GHCR |
| 手动运行 | 按所选 ref 生成标签，至少包含 SHA 标签 | 发布全部镜像 |

标签生成规则由 [Docker metadata action](https://github.com/docker/metadata-action) 提供。与 neo-box 的默认构建一致，当前只发布 amd64；若需要 ARM 部署再增加平台和对应验证。

发布使用 Actions 自带的 `GITHUB_TOKEN` 和 `packages: write` 权限，不需要另外配置 Docker Hub 密钥。发布完成后可在仓库 Actions 页面和 GHCR Packages 查看 digest、标签与可见性。公开仓库不代表新建 GHCR package 一定自动公开；按需要在 Package 设置中调整访问范围。

## 构建检查

tripmap 与 BFF 镜像只复制本服务和 `internal/platform` 的代码，只运行这些包的 Go 单元测试再生成静态二进制，因此业务之间的越界 import 会使镜像构建失败；没有实际存储测试连接时，集成测试会跳过。实际 PostgreSQL/Redis/S3 验证仍通过 `make test-integration` 单独运行。

前端 Docker 构建会运行 `npm test`、`npm run check:maps` 和生产构建；这些检查会读取 `internal/tripmap/share/assets/geometry.json`（F6 预览几何）和 `internal/tripmap/regions/{regions,catalog}.json`（F10 两端索引/摘要校验），Dockerfile 已单独 COPY 它们；前端镜像按 workspace 复制各 `package.json` 后 `npm ci`，再复制 `packages/` 与 `apps/web`（不含浏览器测试），产物取自 `apps/web/dist`；同时复制前端构建配方用于输入清单回归检查。它们只在构建阶段使用，不随前端运行镜像发布。tripmap 镜像构建的测试读取 `packages/fixtures` 与 `migrations/tripmap`，也已 COPY；BFF 镜像构建的测试读取 `config.bff.example.yaml`，并复制 `pkg/`（生成的契约）；预览资源在 `internal` 下随源码进入镜像，`CGO_ENABLED=0` 即可构建。地图 GeoJSON 和静态资源随镜像发布。

本地构建：

```bash
docker build -t trip-map-backend:local .
docker build -f deploy/bff/Dockerfile -t trip-map-bff:local .
docker build -f deploy/frontend/Dockerfile -t trip-map-front:local .
```

## 前端运行配置

前端默认转发到 `backend:18081`，通过运行时环境变量 `BACKEND_UPSTREAM` 指定后端入口的 `主机:端口`。该地址应为 BFF，例如 `trip-map-bff:18081`；BFF 再转发到内网的 tripmap，从直连 tripmap 切换的顺序见 [BFF 部署](bff.md#本地开发与部署)。前后端容器需位于同一 Docker 网络，或该地址可从前端容器访问。

`/api/`、`/ping` 和 `/readyz` 保留完整路径转发到后端；F6 的 `/s/` 与 `/api/share/` 也转发到后端（后端对 `/s/<令牌>` 返回带 Open Graph 标签的页面），这两类路径不写访问日志、`error_log` 限为 `crit`，因为路径含分享令牌，详见 [公开分享页与预览图](sharing.md#配置与部署)；后端需配置 `share.public_base_url` 与 `share.app_index_url`（指向前端容器的根地址，且不得路由回 `/s/`）。SPA 路由返回 index.html，不存在的地图或打包资源返回 404。哈希静态文件长缓存，index.html 不缓存，地图资源重新验证缓存。

前端镜像默认不配置 `VITE_API_BASE_URL`，页面是纯本地模式，不会请求后端，没有后端时也可单独运行；需要连接后端时构建时传入 `--build-arg VITE_API_BASE_URL=/api`（当前 CI 未传递）。

前端 `/healthz` 仅检查静态服务，不代表后端可用；后端就绪状态检查 `/readyz`。Nginx 在请求时解析后端地址，后端暂时不存在不会阻止静态站点启动。默认 resolver 使用 Docker 内置 DNS `127.0.0.11`，运行容器时需加入自定义 Docker 网络（Compose 默认创建此类网络），而不是 Docker 默认 bridge。

```bash
docker run --rm --network trip-map_default -p 8088:80 \
  -e BACKEND_UPSTREAM=backend:18081 \
  ghcr.io/kongken/trip-map-front:main
```

后端见 [后端初始化](backend.md)；部署见 [部署与发布](deployment.md)。镜像中不包含真实 `config.yaml` 或 `.env`，生产配置在运行时挂载。镜像发布只完成构建与上传，不会自动部署服务。
