# 山海足迹

使用 React、Vite、TypeScript、Tailwind CSS 和 shadcn/ui 开发的中国旅行地图，后端使用 Go、Butterfly、Gin、ConnectRPC、Ent 与 PostgreSQL/Redis/S3。地图使用 d3-geo 渲染本地 GeoJSON，无需地图 API Key；省内地图可选开启 OpenStreetMap 底图（Leaflet，默认关闭）。

## 运行

推荐 Node.js 24，最低版本以 Vite 的要求为准。

```bash
npm install
npm run dev
```

生产构建和预览：

```bash
npm test
npm run build
npm run preview
```

## 后端启动

```bash
cp config.example.yaml config.yaml
make deps-up
make storage-init
make dev
```

前端无需后端即可独立运行；仅当构建时设置 `VITE_API_BASE_URL`（如 `/api`）才会连接后端，详见 [后端初始化说明](docs/backend.md)。配置、端口、数据库模型和验证方式也见该文档。后端基础设施已就绪，前端足迹仍采用浏览器本地保存；后端已提供账号、会话和足迹读写接口，前端登录与云同步尚待接入。

## 功能

- 全国与省内地图探索，城市一键标记 / 取消并自动配色。
- 旅行日期、备注、目的地搜索、足迹收藏与探索统计。
- 浏览器本地自动保存、JSON 备份导入导出，以及手机适配。

完整交互规则和验收场景见 [功能描述](docs/features.md)。数据保存在当前浏览器，不会跨设备同步，建议定期导出备份。

## 地图来源和边界说明

地图文件已包含在 `public/maps/`，构建后随静态资源部署。来源、边界年份、区域 ID 及存储格式见 [数据与地图说明](docs/data-and-maps.md)。

```bash
npm run check:maps  # 检查地图完整性、投影、多边形方向和区域 ID
npm run download:maps  # 联网更新省内地图与城市索引
```

shadcn 组件通过官方 CLI 添加，源文件在 `src/components/ui/`；地图归一化逻辑在 `src/lib/maps.ts`，主要业务界面在 `src/App.tsx`。

## Docker 镜像

GitHub Actions 分别构建并发布 `ghcr.io/kongken/trip-map`（后端）和 `ghcr.io/kongken/trip-map-front`（前端）。main 分支使用 `main` 标签，版本 tag 发布对应版本；PR 仅构建。详情见 [Docker 镜像与 Actions](docs/docker-images.md)。

## 项目文档

- [文档索引](docs/README.md)
- [项目协作与文档同步规范](AGENTS.md)
