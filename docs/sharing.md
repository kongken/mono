# 公开分享页与预览图（F6）

本文描述 F6 当前**已实现**的行为、部署要求和限制。用户可见规则与验收场景见 [功能描述](features.md#公开分享页与预览图f6)，数据结构见 [数据与地图说明](data-and-maps.md#f6-分享快照与预览资源)，接口与配置见 [后端初始化](backend.md#f6-公开分享tripmapv1shareservice)。

## 模型：显式发布的快照

每个账号最多有一份分享配置（`shares` 表，`user_id` 唯一）。访客看到的不是实时足迹，而是**发布时生成的不可变快照**：之后新增的城市、旅行、备注和照片都保持私有，直到所有者再次预览并确认更新。

| 操作 | 读取当前足迹 | 要求 |
| --- | --- | --- |
| 首次发布、新增字段或照片、更新内容 | 是 | 先预览；提交时带预览时的足迹 revision，已变化则返回 `aborted`，前端刷新状态，用户重新预览并确认 |
| 收回字段（隐藏城市、日期、备注、照片，或取消某张照片） | 否 | 只从已发布快照里移除，立即生效 |
| 改变可见性（链接 / 公开）而内容不变 | 否 | 版本递增，快照与链接不变 |
| 改为“不公开” | 否 | 设置保留为草稿；链接和快照删除，再次发布生成**新**令牌 |
| 撤销 | 否 | 无条件、立即；链接、快照和令牌删除 |

可见性有三种：`private`（只保存设置，没有公开访问）、`link`（持有链接即可，`noindex`）、`public`（可被搜索引擎收录，但没有目录或发现入口）。默认字段只有省份地图和统计；城市名称在 `show_cities` 开关之后，日期、备注、照片必须同时开启城市名称。默认设置不需要额外确认，但发布本身始终是一次显式确认。

**照片。** 只能逐张选择所有者自己的、已确认的 JPEG 照片，最多 12 张，所在城市必须是已打卡城市；不支持 PDF，也不会自动加入以后上传的照片。每次发布给照片生成随机句柄（`p` + 8 位），句柄到媒体 ID 的映射只存在服务端。每次读取照片都会重新检查媒体记录：照片删除或不再是 ready JPEG 后，分享路径立即 404，私有接口也不会绕过。

## 公开接口

| 路径 | 内容 |
| --- | --- |
| `GET /s/<token>` | 应用 `index.html` 加上 Open Graph / Twitter Card / robots / referrer 标签；浏览器随后由前端渲染只读分享页 |
| `GET /s/<token>/og.png` | 1200×630 PNG 预览图 |
| `GET /api/share/<token>/data` | 公开 JSON（与预览返回的 `content_json` 相同） |
| `GET /api/share/<token>/photos/<句柄>?size=thumb\|full` | 选中的照片 |

均支持 GET 与 HEAD。`/s/` 与 `/api/share/` 之下的其他路径、未知或撤销或私有的令牌、被停用的账号、已删除的照片、格式错误的令牌、分享未开启，**全部返回同一份 404**（相同头与正文，不回显令牌，也不回退到个人地图）。

- 所有对外响应使用 `Cache-Control: no-store`，包括 PNG。令牌、可见性和 `user.disabled` 在**每个请求**里查询数据库，之后才可能读取 Redis 渲染缓存；撤销后链接立即失效。预览图缓存键为 `分享ID:版本`，只在查询成功之后读取。
- Redis 故障时公开路由返回 503（关闭，而不是放行）。每个客户端地址每分钟有页面 / 图片 / 照片限额，另有失败查找（猜测令牌）的更低限额，超限返回 429。默认值：页面与数据 120、图片 30、照片 300、失败 30，可在 `share.*_rate_per_minute` 调整。
- 页面 HTML 带严格同源 CSP（可用 `share.html_csp` 覆盖）、`Referrer-Policy: no-referrer`、`X-Frame-Options: DENY`；`X-Robots-Tag` 与 `<meta robots>` 随可见性（`link` 为 noindex，`public` 为 index）。
- 公开响应是显式白名单（`internal/tripmap/share.Content`）：只含省份代码、统计、城市代码、名称、颜色、旅行的日期与备注（按开关）和照片句柄与尺寸。不包含用户 ID、用户名、昵称、旅行 ID、媒体 ID、对象键、文件名或隐藏字段；不会直接序列化 Ent 或 proto 的私有模型。
- 前端匿名分享页（`SharePage`）在挂载任何内容之前由路径决定（`apps/web/src/main.tsx`）：`App` 模块虽随入口打包，但模块顶层不访问存储，分享路径下从不渲染，因此不挂载 `App`、不读写 localStorage、不读取会话，也不发送 `Authorization`；`/s/` 下的非法路径显示同一份“链接不存在或已撤销”，不会进入个人地图。

## 预览图与地图资源

预览图由 Go 后端纯 Go 渲染（`CGO_ENABLED=0` 可构建），不依赖第三方请求：

- 几何：`internal/tripmap/share/assets/geometry.json`，由 `npm run prepare:share-geometry`（`apps/web/scripts/prepare-share-geometry.mjs`）按前端同一投影（`apps/web/src/lib/projection.ts`）和固定省份配色生成，随项目分发。`npm run check:maps` 校验它与随项目分发的 GeoJSON、`regions.json`、固定配色一致。
- 字体：SIL OFL 的 Noto Sans SC Regular / Bold 子集（各约 18 KB，`apps/web/scripts/subset-share-font.sh` 生成，许可证 `OFL.txt` 随文件保留）。子集只含图中用到的固定文字和数字，渲染时不绘制任何用户文字，因此预览图只显示省份层级的统计，不显示城市名、备注或日期。
- 统计：与应用统计页同口径（直辖市区县与特别行政区在全国范围各算 1 座城市，合计 391 座城市、34 个省级区域）。`packages/fixtures/share-stats.json` 同时驱动 Go 测试和前端 `npm test`。

## 配置与部署

分享默认关闭：`share.public_base_url` 为空时，所有者接口返回“未开启”，公开路由全部 404，`GetStatus.share_enabled=false`，前端不显示入口；独立本地模式不受影响。

```yaml
share:
  public_base_url: https://trip.example.com   # 分享链接 = 它 + /s/<令牌>，不使用请求的 Host 头
  app_index_url: https://trip.example.com/    # 受信任配置：后端取它的 index.html 以注入 OG 标签
  # html_csp: ""                              # 开发服务器需要放宽
```

- `app_index_url` 只来自配置，不受用户输入影响；校验为 http(s)、无凭据、无查询和片段，且不得包含 `/s/` 或 `/api/`。获取时限 2 秒、正文上限 1 MiB、不跟随重定向、要求 `text/html`，成功结果缓存 60 秒，失败时最多使用 10 分钟内的旧副本，再失败则返回自带元数据的最小页面。请求带 `X-Tripmap-Index-Fetch` 标记，带标记的请求访问 `/s/` 一律 404，防止配置错误时递归。
- **前端 Nginx 或反向代理必须把 `/s/` 与 `/api/share/` 转发到后端**（BFF 的路由表已登记这两个路径，并在追踪中隐去令牌），否则链接预览看不到 OG 标签（SPA 回退只会返回不含标签的 index.html）。镜像内的 `deploy/frontend/default.conf.template` 已配置；开发时 Vite 开发服务器代理 `/s/`（`vite preview` 只提供构建出的单页应用，打开分享链接能显示分享页，但没有 OG 标签）。
- 令牌即凭据，不得出现在日志中：Butterfly 不输出 Gin 请求日志；OpenTelemetry 的 Gin 中间件会记录原始路径，分享路由把 span 的路径属性替换为路由模板；Nginx 对 `/s/` 与 `/api/share/` 设置 `access_log off`，并把这些位置的 `error_log` 限制为 `crit`（否则上游错误会记录请求行）。自行搭建的代理需要同样处理。
- 位于代理之后时必须设置 `auth.client_ip_header`（例如 `X-Real-IP` 或 `CF-Connecting-IP`），否则所有访客共用代理的地址，限额互相影响。该请求头只能由受信任的代理写入。位于 BFF 之后设为 `X-Real-IP`，BFF 总是覆盖写入该头。
- 同源部署（前端镜像的 Nginx）是推荐方式。前端与 API 分属不同域名时：`/s/` 所在的来源必须能提供前端的 `/assets/`（或用绝对 `BASE_URL` 构建），`share.html_csp` 需允许 API 域名的 `connect-src`；`VITE_API_BASE_URL` 必须能被匿名页面访问。
- 升级顺序：先执行数据库迁移（`migrations/tripmap/20261014022913_f6_shares.sql`，后端 `auto_migrate` 或手动），再部署后端，最后部署前端。后端镜像和前端镜像构建需要的文件（`internal/tripmap/share/assets`、`packages/fixtures`、`migrations`）已在各自 Dockerfile 中 COPY。

## 当前限制

- 分享页是快照语义：发布后的新足迹不会出现，需要所有者预览并更新。
- 撤销让链接、页面、数据、照片和预览图立即返回 404，但**已被他人下载、转发或被平台缓存的内容无法收回**；社交平台可能继续展示它们已抓取的预览。
- 令牌以明文存储在 `shares.token`（唯一索引，需要按令牌查询）。数据库只读泄露即可暴露有效令牌；它是 256 位随机值，不可猜测，但不是哈希。
- 不支持：密码保护、过期时间、访问统计、目录或发现页、PDF、自动加入以后上传的照片、微信与微博（它们需要平台自己的抓取与白名单，不在 F6 范围）。
- 预览图只显示省份层级统计，不显示城市名、备注或日期；文字限定在固定字体子集内。
- 分享需要登录才能管理；匿名访客只能读取。
- Facebook、X、LinkedIn 等真实平台的抓取尚未验证；当前只验证了响应里的 OG、Twitter Card 标签与 PNG（尺寸、类型、no-store）。**不要把 F6 或外部社交平台描述为已在线验收。**
- 账号注销在第一个禁用事务里就关闭公开访问并删除分享行，即使随后对象存储清理失败，分享也已不可访问。

## 测试覆盖

- Go（`internal/tripmap/application/share_test.go`，真实 Gin 路由 + PostgreSQL + Redis + 内存对象存储）：默认隐私、字段依赖、用户隔离、快照语义、收回即时生效与新增需确认、OG HTML 与 PNG、索引获取的递归与缓存、撤销后统一 404、照片逐张选择与删除后失效、幂等回放与精确请求哈希、并发保存与撤销、账号注销先关闭访问、停用账号与配置关闭、限流与 Redis 故障关闭、超大快照被拒绝、schema 1 账号。
- Go 单元：统计与固定样例、预览图尺寸与字体字形、HTML 转义与标签替换、索引源限制、配置校验、路由在未配置时的统一 404、迁移约束与用户级联。
- 前端（`apps/web/scripts/share.test.mjs`、`apps/web/tests/share.spec.ts`）：路径解析与非法 `/s/` 路径、严格内容解析、统计固定样例、配色一致；所有者的预览 / 发布 / 收回 / 更新 / 撤销、预览后足迹变化、响应丢失后复用 mutation ID、匿名页只读、未挂载应用、无存储读写、无认证头、320px 无溢出、键盘操作。
