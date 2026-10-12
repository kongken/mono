import { useEffect, useMemo, useRef, useState } from "react";
import { geoConicConformal, geoMercator, geoPath } from "d3-geo";
import { normalizeMap, type MapData } from "./lib/maps";
import {
  ArrowLeft,
  ArrowUpRight,
  Check,
  Compass,
  DownloadSimple,
  FloppyDisk,
  MapPin,
  Mountains,
  Plus,
  MagnifyingGlass,
  X,
  Trash,
  UploadSimple,
  CaretRight,
  Minus,
  House,
  MapTrifold,
  CalendarBlank,
  ArrowCounterClockwise,
} from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import regions from "./regions.json";
import "./App.css";

type Region = (typeof regions)[number];
type Visit = {
  code: string;
  name: string;
  province: string;
  color: string;
  date: string;
  note: string;
};
type Saved = { version: 1; visits: Visit[]; provinces: Record<string, string> };
const STORAGE = "shanhai-travel-v1";
const colors = [
  { value: "#4b937d", name: "山野绿" },
  { value: "#e2af5c", name: "日落黄" },
  { value: "#d88370", name: "珊瑚橙" },
  { value: "#719fc6", name: "海岸蓝" },
  { value: "#a08cbd", name: "暮光紫" },
];
const empty: Saved = { version: 1, visits: [], provinces: {} };
const today = () => new Date().toLocaleDateString("en-CA");
const provinceByCode = (code: string) => regions.find((p) => p.code === code);
function validateSaved(data: unknown): Saved {
  if (!data || typeof data !== "object") throw new Error("文件格式不正确");
  const s = data as Saved;
  if (
    s.version !== 1 ||
    !Array.isArray(s.visits) ||
    !s.provinces ||
    typeof s.provinces !== "object"
  )
    throw new Error("请选择山海足迹导出的 JSON 文件");
  const ids = new Set<string>();
  for (const v of s.visits) {
    const p = provinceByCode(v.province);
    if (
      !p?.cities.some((c) => c.code === v.code && c.name === v.name) ||
      ids.has(v.code) ||
      !colors.some((c) => c.value === v.color) ||
      typeof v.note !== "string" ||
      v.note.length > 1000 ||
      typeof v.date !== "string" ||
      (v.date && !/^\d{4}-\d{2}-\d{2}$/.test(v.date))
    )
      throw new Error("足迹数据无效或重复");
    ids.add(v.code);
  }
  for (const [code, color] of Object.entries(s.provinces))
    if (!provinceByCode(code) || !colors.some((c) => c.value === color))
      throw new Error("省份标记数据无效");
  return {
    version: 1,
    visits: s.visits.map((v) => ({
      code: v.code,
      name: v.name,
      province: v.province,
      color: v.color,
      date: v.date,
      note: v.note,
    })),
    provinces: s.provinces,
  };
}
function loadSaved() {
  try {
    const raw = localStorage.getItem(STORAGE);
    return raw ? validateSaved(JSON.parse(raw)) : empty;
  } catch {
    return empty;
  }
}

function App() {
  const [saved, setSaved] = useState<Saved>(loadSaved);
  const [province, setProvince] = useState<Region | null>(null);
  const [map, setMap] = useState<MapData | null>(null);
  const [mapError, setMapError] = useState(false);
  const [retry, setRetry] = useState(0);
  const [query, setQuery] = useState("");
  const [tab, setTab] = useState<"map" | "visits">("map");
  const [editor, setEditor] = useState(false);
  const [draftProvince, setDraftProvince] = useState("330000");
  const [draftCity, setDraftCity] = useState("");
  const [draftColor, setDraftColor] = useState(colors[0].value);
  const [draftDate, setDraftDate] = useState(today());
  const [draftNote, setDraftNote] = useState("");
  const [toast, setToast] = useState("");
  const [storageError, setStorageError] = useState(false);
  const [hover, setHover] = useState("");
  const [zoom, setZoom] = useState(1);
  const [importOpen, setImportOpen] = useState<Saved | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE, JSON.stringify(saved));
      setStorageError(false);
    } catch {
      setStorageError(true);
    }
  }, [saved]);
  useEffect(() => {
    if (!toast) return;
    const timeout = setTimeout(() => setToast(""), 4000);
    return () => clearTimeout(timeout);
  }, [toast]);
  useEffect(() => {
    const controller = new AbortController();
    setMap(null);
    setMapError(false);
    setZoom(1);
    setHover("");
    fetch(
      `${import.meta.env.BASE_URL}maps/${province?.code || "100000"}.json`,
      { signal: controller.signal },
    )
      .then((r) => {
        if (!r.ok) throw new Error();
        return r.json();
      })
      .then((data) => setMap(normalizeMap(data)))
      .catch((e) => {
        if (e.name !== "AbortError") setMapError(true);
      });
    return () => controller.abort();
  }, [province, retry]);
  const visitedProvinces = new Set([
    ...Object.keys(saved.provinces),
    ...saved.visits.map((v) => v.province),
  ]);
  const provinceColor = (code: string) =>
    saved.provinces[code] ||
    saved.visits.find((v) => v.province === code)?.color;
  const currentVisits = saved.visits
    .filter((v) => !province || v.province === province.code)
    .sort((a, b) => b.date.localeCompare(a.date));
  const filteredProvinces = regions.filter(
    (p) =>
      p.name.includes(query) || p.cities.some((c) => c.name.includes(query)),
  );
  const draftRegion = provinceByCode(draftProvince)!;
  const isDistrict =
    province &&
    ["110000", "120000", "310000", "500000", "810000", "820000"].includes(
      province.code,
    );
  const unit = isDistrict ? "区县" : "城市";
  const projection = useMemo(() => {
    if (!map) return null;
    const shapes = map.features.filter((f) => f.properties.name);
    // Mainland bounds define the national camera. Maritime lines have their own inset.
    const bounds = province
      ? { ...map, features: shapes }
      : {
          type: "FeatureCollection" as const,
          features: shapes.map((f) => {
            if (f.geometry.type !== "MultiPolygon") return f;
            return {
              ...f,
              geometry: {
                ...f.geometry,
                coordinates: f.geometry.coordinates.filter((p) =>
                  p[0].some((point) => point[1] > 17.5),
                ),
              },
            };
          }),
        };
    return (
      province
        ? geoMercator()
        : geoConicConformal().parallels([25, 47]).rotate([-105, 0])
    ).fitExtent(
      [
        [60, 35],
        [790, 565],
      ],
      bounds,
    );
  }, [map, province]);
  const path = projection ? geoPath(projection) : null;
  const openEditor = (p: string, city = "") => {
    const existing = saved.visits.find((v) => v.code === city);
    setDraftProvince(p);
    setDraftCity(city);
    setDraftColor(existing?.color || colors[0].value);
    setDraftDate(existing?.date ?? today());
    setDraftNote(existing?.note || "");
    setEditor(true);
  };
  const selectDraftCity = (code: string) => {
    setDraftCity(code);
    const existing = saved.visits.find((v) => v.code === code);
    setDraftColor(existing?.color || colors[0].value);
    setDraftDate(existing?.date ?? today());
    setDraftNote(existing?.note || "");
  };
  const saveVisit = () => {
    const city = draftRegion.cities.find((c) => c.code === draftCity);
    if (!city) return;
    const visit = {
      code: city.code,
      name: city.name,
      province: draftProvince,
      color: draftColor,
      date: draftDate,
      note: draftNote.trim(),
    };
    setSaved((s) => ({
      ...s,
      visits: [...s.visits.filter((v) => v.code !== city.code), visit],
    }));
    setEditor(false);
    setToast(`已记录 ${city.name} 的足迹`);
  };
  const exportData = () => {
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(saved, null, 2)], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = `山海足迹-${today()}.json`;
    a.click();
    URL.revokeObjectURL(url);
    setToast("足迹备份已导出");
  };
  const importFile = async (file?: File) => {
    if (!file) return;
    try {
      if (file.size > 2_000_000)
        throw new Error("文件过大，请选择足迹备份文件");
      setImportOpen(validateSaved(JSON.parse(await file.text())));
    } catch (e) {
      setToast(e instanceof Error ? e.message : "无法读取文件");
    }
    if (fileInput.current) fileInput.current.value = "";
  };
  const navigate = (p: Region | null) => {
    setProvince(p);
    setQuery("");
    setTab("map");
  };

  return (
    <div className="app-shell">
      <header className="header">
        <a
          className="brand"
          href="#"
          onClick={(e) => {
            e.preventDefault();
            navigate(null);
          }}
        >
          <span className="brand-icon">
            <Compass size={26} weight="duotone" />
          </span>
          <span>
            山海足迹<small>SHANHAI TRAVEL</small>
          </span>
        </a>
        <nav aria-label="主导航">
          <button
            className={tab === "map" ? "nav-active" : ""}
            onClick={() => setTab("map")}
          >
            <MapTrifold size={18} />
            我的地图
          </button>
          <button
            className={tab === "visits" ? "nav-active" : ""}
            onClick={() => setTab("visits")}
          >
            <MapPin size={18} />
            旅行足迹<span className="nav-count">{saved.visits.length}</span>
          </button>
        </nav>
        <div className="header-end">
          <span className="local-status">
            <span />
            {storageError ? "保存失败，请导出备份" : "本地自动保存"}
          </span>
          <Button
            variant="outline"
            className="export-button"
            onClick={exportData}
          >
            <DownloadSimple size={17} />
            导出足迹
          </Button>
          <span className="avatar">
            <Mountains size={22} />
          </span>
        </div>
      </header>
      <main>
        <section className="intro">
          <div>
            <div className="eyebrow">
              <span />
              每一段旅程，都值得被记住
            </div>
            <h1>
              世界很大，<span>慢慢留下足迹。</span>
            </h1>
            <p>用颜色收藏走过的城市，让回忆在地图上生长。</p>
          </div>
          <div className="stats">
            <div>
              <strong>
                {visitedProvinces.size}
                <small>/ 34</small>
              </strong>
              <span>已探索省份</span>
            </div>
            <i />
            <div>
              <strong>{saved.visits.length}</strong>
              <span>已留下足迹</span>
            </div>
            <i />
            <div>
              <strong>
                {Math.round((visitedProvinces.size / 34) * 100)}
                <small>%</small>
              </strong>
              <span>中国探索度</span>
            </div>
          </div>
        </section>
        <div className="workspace">
          <section className="map-panel">
            <div className="map-toolbar">
              <div className="breadcrumbs">
                <button onClick={() => navigate(null)}>
                  <House size={16} />
                  中国
                </button>
                {province && (
                  <>
                    <CaretRight size={13} />
                    <span>{province.short}</span>
                  </>
                )}
              </div>
              <span className="map-mode">
                {tab === "visits"
                  ? "足迹收藏"
                  : province
                    ? `${unit}地图`
                    : "全国地图"}
              </span>
              <Button
                size="sm"
                className="add-main"
                onClick={() => openEditor(province?.code || "330000")}
              >
                <Plus size={16} />
                添加足迹
              </Button>
            </div>
            {tab === "map" ? (
              <div className="map-stage">
                <div className="map-heading">
                  {province && (
                    <button
                      className="back-button"
                      aria-label="返回全国地图"
                      onClick={() => navigate(null)}
                    >
                      <ArrowLeft size={18} />
                    </button>
                  )}
                  <div>
                    <h2>
                      {province
                        ? `${province.short}，走过的风景`
                        : "我的中国旅行地图"}
                    </h2>
                    <p>
                      {province
                        ? `点击${unit}，给旅行记忆添一种颜色`
                        : "点击一个省份，开启你的城市足迹"}
                    </p>
                  </div>
                </div>
                {mapError ? (
                  <div className="map-message">
                    地图加载失败
                    <Button
                      variant="outline"
                      onClick={() => setRetry((r) => r + 1)}
                    >
                      重新加载
                    </Button>
                  </div>
                ) : !map || !path || !projection ? (
                  <div className="map-message">
                    <Compass className="loading" size={30} />
                    正在展开地图…
                  </div>
                ) : (
                  <svg
                    className="china-map"
                    viewBox="0 0 850 620"
                    role="group"
                    aria-label={
                      province ? `${province.name}${unit}地图` : "中国省份地图"
                    }
                  >
                    <defs>
                      <pattern
                        id="dots"
                        width="16"
                        height="16"
                        patternUnits="userSpaceOnUse"
                      >
                        <circle cx="1" cy="1" r="0.6" fill="#d5dcd7" />
                      </pattern>
                    </defs>
                    <rect width="850" height="620" fill="url(#dots)" />
                    <g
                      transform={`translate(${425 * (1 - zoom)},${310 * (1 - zoom)}) scale(${zoom})`}
                    >
                      {map.features
                        .filter((f) => f.properties.name)
                        .map((feature) => {
                          const code = String(feature.properties.adcode),
                            fill = province
                              ? saved.visits.find((v) => v.code === code)?.color
                              : provinceColor(code);
                          const center =
                            feature.properties.centroid ||
                            feature.properties.center;
                          const point = center ? projection(center) : null;
                          const label = province
                            ? feature.properties.name.replace(/市$/, "")
                            : provinceByCode(code)?.short ||
                              feature.properties.name;
                          const activate = () => {
                            if (province) openEditor(province.code, code);
                            else navigate(provinceByCode(code) || null);
                          };
                          return (
                            <g
                              key={code}
                              className={`map-region ${fill ? "visited" : ""}`}
                              role="button"
                              tabIndex={0}
                              aria-label={`${feature.properties.name}${fill ? "，已去过" : ""}`}
                              onClick={activate}
                              onKeyDown={(e) => {
                                if (e.key === "Enter" || e.key === " ") {
                                  e.preventDefault();
                                  activate();
                                }
                              }}
                              onMouseEnter={() =>
                                setHover(feature.properties.name)
                              }
                              onMouseLeave={() => setHover("")}
                            >
                              <path
                                d={path(feature) || ""}
                                fill={fill || "#e9eee9"}
                                stroke={fill ? "#ffffff" : "#fafcf9"}
                                strokeWidth={province ? 1.4 : 1.5}
                                vectorEffect="non-scaling-stroke"
                              >
                                <title>
                                  {feature.properties.name}
                                  {fill ? " · 已去过" : " · 点击探索"}
                                </title>
                              </path>
                              {point && (
                                <text
                                  x={point[0]}
                                  y={point[1]}
                                  textAnchor="middle"
                                  dy=".35em"
                                  className={`map-label ${fill ? "colored" : ""} ${["110000", "120000", "310000", "810000", "820000"].includes(code) && !province ? "small-label" : ""}`}
                                >
                                  {label}
                                </text>
                              )}
                            </g>
                          );
                        })}
                    </g>
                    {!province && (
                      <g className="sea-inset" transform="translate(736,458)">
                        <rect
                          width="83"
                          height="109"
                          rx="4"
                          fill="#fafbf8"
                          stroke="#d6ddd7"
                        />
                        {map.features
                          .filter((f) => !f.properties.name)
                          .map((f, i) => (
                            <path
                              key={i}
                              d={
                                geoPath(
                                  geoMercator().fitExtent(
                                    [
                                      [9, 7],
                                      [74, 87],
                                    ],
                                    f,
                                  ),
                                )(f) || ""
                              }
                              fill="none"
                              stroke="#b6c5ba"
                              strokeWidth="1"
                            />
                          ))}
                        <text x="41" y="99" textAnchor="middle">
                          南海诸岛
                        </text>
                      </g>
                    )}
                  </svg>
                )}
                <div className="map-caption">
                  <Compass size={17} />
                  <span>
                    {hover ||
                      (province
                        ? "给每一座走过的城市，留下专属记忆"
                        : "山川湖海，下一站总有新的风景")}
                  </span>
                </div>
                <div className="map-controls">
                  <button
                    aria-label="放大地图"
                    disabled={zoom >= 1.6}
                    onClick={() => setZoom((z) => Math.min(1.6, z + 0.2))}
                  >
                    <Plus size={18} />
                  </button>
                  <button
                    aria-label="缩小地图"
                    disabled={zoom <= 1}
                    onClick={() => setZoom((z) => Math.max(1, z - 0.2))}
                  >
                    <Minus size={18} />
                  </button>
                  <button aria-label="重置地图缩放" onClick={() => setZoom(1)}>
                    <ArrowCounterClockwise size={17} />
                  </button>
                </div>
              </div>
            ) : (
              <div className="collection">
                <div className="collection-heading">
                  <h2>我的旅行收藏</h2>
                  <p>每一个城市，都有属于你的故事。</p>
                </div>
                {currentVisits.length ? (
                  <div className="visit-grid">
                    {currentVisits.map((v) => (
                      <button
                        key={v.code}
                        className="visit-card"
                        onClick={() => openEditor(v.province, v.code)}
                      >
                        <span
                          className="visit-card-icon"
                          style={{ background: v.color + "20", color: v.color }}
                        >
                          <MapPin size={25} weight="duotone" />
                        </span>
                        <small>{provinceByCode(v.province)?.short}</small>
                        <h3>{v.name}</h3>
                        <p>{v.note || "有些风景，只属于走过的人。"}</p>
                        <span className="visit-date">
                          <CalendarBlank size={14} />
                          {v.date || "日期未记录"}
                          <ArrowUpRight size={16} />
                        </span>
                      </button>
                    ))}
                  </div>
                ) : (
                  <div className="empty-collection">
                    <MapTrifold size={54} weight="duotone" />
                    <h3>你的故事，从第一座城市开始</h3>
                    <p>记录去过的地方，慢慢拼出属于你的旅行地图。</p>
                    <Button
                      onClick={() => openEditor(province?.code || "330000")}
                    >
                      <Plus size={16} />
                      记录第一站
                    </Button>
                  </div>
                )}
              </div>
            )}
            <div className="map-legend">
              <span>足迹色彩</span>
              {colors.map((c) => (
                <span key={c.value}>
                  <i style={{ background: c.value }} />
                  {c.name}
                </span>
              ))}
              <span className="not-visited">
                <i />
                未探索
              </span>
            </div>
          </section>
          <aside className="sidebar">
            <div className="sidebar-title">
              <div>
                <h2>{province ? province.short : "探索目的地"}</h2>
                <p>
                  {province
                    ? `已走过 ${currentVisits.length} 个${unit}`
                    : "从一个省份，开始一段旅程"}
                </p>
              </div>
              <span className="sidebar-icon">
                <MapPin size={21} weight="duotone" />
              </span>
            </div>
            <div className="search-wrap">
              <MagnifyingGlass size={17} />
              <Input
                aria-label={province ? "搜索城市" : "搜索省份或城市"}
                placeholder={province ? `搜索${unit}名称` : "搜索省份或城市…"}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              {query && (
                <button aria-label="清空搜索" onClick={() => setQuery("")}>
                  <X size={14} />
                </button>
              )}
            </div>
            {!province ? (
              <>
                <div className="list-heading">
                  <span>全部省份</span>
                  <span>{visitedProvinces.size} / 34 已探索</span>
                </div>
                <div className="province-list">
                  {filteredProvinces.map((p) => {
                    const count = saved.visits.filter(
                      (v) => v.province === p.code,
                    ).length;
                    const visited = visitedProvinces.has(p.code);
                    return (
                      <button
                        key={p.code}
                        className="province-row"
                        onClick={() => navigate(p)}
                      >
                        <span
                          className={`province-dot ${visited ? "has-visit" : ""}`}
                          style={
                            visited ? { background: provinceColor(p.code) } : {}
                          }
                        />
                        <span>{p.short}</span>
                        <small>
                          {count
                            ? `${count} 处足迹`
                            : visited
                              ? "已去过"
                              : "待探索"}
                        </small>
                        <CaretRight size={14} />
                      </button>
                    );
                  })}
                  {!filteredProvinces.length && (
                    <p className="no-results">没有找到目的地，试试其他名称。</p>
                  )}
                </div>
              </>
            ) : (
              <>
                <div className="province-actions">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setSaved((s) => {
                        const next = { ...s.provinces };
                        if (next[province.code]) delete next[province.code];
                        else next[province.code] = colors[0].value;
                        return { ...s, provinces: next };
                      });
                      setToast(
                        saved.provinces[province.code]
                          ? "已取消省份单独标记，城市足迹保留"
                          : `已标记去过${province.short}`,
                      );
                    }}
                  >
                    <Check size={15} />
                    {saved.provinces[province.code]
                      ? "取消省份标记"
                      : "标记去过此省"}
                  </Button>
                  <Button size="sm" onClick={() => openEditor(province.code)}>
                    <Plus size={15} />
                    记录{unit}
                  </Button>
                </div>
                <div className="list-heading">
                  <span>全部{unit}</span>
                  <span>{province.cities.length} 个区域</span>
                </div>
                <div className="province-list">
                  {province.cities
                    .filter((c) => c.name.includes(query))
                    .map((c) => {
                      const visit = saved.visits.find((v) => v.code === c.code);
                      return (
                        <button
                          key={c.code}
                          className="province-row"
                          onClick={() => openEditor(province.code, c.code)}
                        >
                          <span
                            className={`province-dot ${visit ? "has-visit" : ""}`}
                            style={visit ? { background: visit.color } : {}}
                          />
                          <span>{c.name}</span>
                          <small>{visit ? "已去过" : "待探索"}</small>
                          {visit ? <Check size={14} /> : <Plus size={14} />}
                        </button>
                      );
                    })}
                  {!province.cities.some((c) => c.name.includes(query)) && (
                    <p className="no-results">没有找到匹配的{unit}。</p>
                  )}
                </div>
              </>
            )}
            <div className="travel-note">
              <span className="note-icon">
                <Mountains size={28} weight="duotone" />
              </span>
              <div>
                <strong>不必走遍世界</strong>
                <p>每一步，都是自己的风景。</p>
              </div>
              <ArrowUpRight size={18} />
            </div>
          </aside>
        </div>
        <section className="recent-section">
          <div className="recent-heading">
            <div>
              <h2>
                最近的足迹 <span>RECENT JOURNEYS</span>
              </h2>
              <p>回头看看，那些让你心动的地方。</p>
            </div>
            <button
              onClick={() => {
                setTab("visits");
                setProvince(null);
              }}
            >
              查看全部
              <ArrowUpRight size={16} />
            </button>
          </div>
          {saved.visits.length ? (
            <div className="recent-grid">
              {[...saved.visits]
                .sort((a, b) => b.date.localeCompare(a.date))
                .slice(0, 4)
                .map((v) => (
                  <button
                    className="recent-item"
                    key={v.code}
                    onClick={() => openEditor(v.province, v.code)}
                  >
                    <span
                      style={{ color: v.color, background: v.color + "18" }}
                    >
                      <MapPin size={22} weight="duotone" />
                    </span>
                    <div>
                      <h3>
                        {v.name}
                        <small>{provinceByCode(v.province)?.short}</small>
                      </h3>
                      <p>{v.date || "日期未记录"}</p>
                    </div>
                    <ArrowUpRight size={17} />
                  </button>
                ))}
            </div>
          ) : (
            <div className="recent-empty">
              <MapPin size={20} />
              <span>还没有记录足迹。把你去过的第一座城市，留在这里。</span>
              <button onClick={() => openEditor("330000")}>
                添加第一站
                <Plus size={15} />
              </button>
            </div>
          )}
        </section>
        <footer>
          <span>
            <Compass size={15} />
            山海足迹 · 让每一次出发都有迹可循
          </span>
          <div>
            <button onClick={() => fileInput.current?.click()}>
              <UploadSimple size={14} />
              导入备份
            </button>
            <button onClick={exportData}>
              <DownloadSimple size={14} />
              导出备份
            </button>
            <span>地图数据：GeoAtlas / twgeojson</span>
          </div>
        </footer>
      </main>
      <input
        type="file"
        accept=".json,application/json"
        hidden
        ref={fileInput}
        onChange={(e) => void importFile(e.target.files?.[0])}
      />
      <Dialog open={editor} onOpenChange={setEditor}>
        <DialogContent className="editor-dialog">
          <DialogHeader>
            <span className="dialog-icon">
              <MapPin size={25} weight="duotone" />
            </span>
            <DialogTitle>
              {saved.visits.some((v) => v.code === draftCity)
                ? "编辑城市足迹"
                : "记录一段新的旅程"}
            </DialogTitle>
            <DialogDescription>
              选一个颜色，把走过的风景留在地图上。
            </DialogDescription>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              saveVisit();
            }}
          >
            <div className="form-row">
              <label>
                省份
                <select
                  value={draftProvince}
                  onChange={(e) => {
                    setDraftProvince(e.target.value);
                    setDraftCity("");
                    setDraftColor(colors[0].value);
                    setDraftDate(today());
                    setDraftNote("");
                  }}
                >
                  {regions.map((p) => (
                    <option key={p.code} value={p.code}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                城市 / 区县
                <select
                  required
                  value={draftCity}
                  onChange={(e) => selectDraftCity(e.target.value)}
                >
                  <option value="">选择目的地</option>
                  {draftRegion.cities.map((c) => (
                    <option key={c.code} value={c.code}>
                      {c.name}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <fieldset>
              <legend>足迹颜色</legend>
              <div className="color-picker">
                {colors.map((c) => (
                  <button
                    type="button"
                    key={c.value}
                    className={draftColor === c.value ? "selected" : ""}
                    aria-label={c.name}
                    aria-pressed={draftColor === c.value}
                    onClick={() => setDraftColor(c.value)}
                  >
                    <span style={{ background: c.value }}>
                      {draftColor === c.value && (
                        <Check size={17} weight="bold" />
                      )}
                    </span>
                    <small>{c.name}</small>
                  </button>
                ))}
              </div>
            </fieldset>
            <label className="field-label">
              旅行日期
              <Input
                type="date"
                value={draftDate}
                onChange={(e) => setDraftDate(e.target.value)}
              />
            </label>
            <label className="field-label">
              旅行记忆 <span>选填</span>
              <textarea
                placeholder="那天的天气、遇见的人，或一顿念念不忘的美食…"
                maxLength={1000}
                value={draftNote}
                onChange={(e) => setDraftNote(e.target.value)}
              />
            </label>
            <div className="dialog-actions">
              {saved.visits.some((v) => v.code === draftCity) && (
                <Button
                  type="button"
                  variant="ghost"
                  className="delete-button"
                  onClick={() => {
                    setSaved((s) => ({
                      ...s,
                      visits: s.visits.filter((v) => v.code !== draftCity),
                    }));
                    setEditor(false);
                    setToast("已移除这座城市的足迹");
                  }}
                >
                  <Trash size={16} />
                  移除足迹
                </Button>
              )}
              <Button
                type="button"
                variant="outline"
                onClick={() => setEditor(false)}
              >
                取消
              </Button>
              <Button type="submit" disabled={!draftCity}>
                <Check size={17} />
                保存足迹
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!importOpen}
        onOpenChange={(open) => {
          if (!open) setImportOpen(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>导入旅行足迹</DialogTitle>
            <DialogDescription>
              备份包含 {importOpen?.visits.length}{" "}
              条城市足迹。导入将替换当前记录，建议先导出当前备份。
            </DialogDescription>
          </DialogHeader>
          <div className="dialog-actions">
            <Button variant="outline" onClick={exportData}>
              <DownloadSimple size={16} />
              备份当前数据
            </Button>
            <Button
              onClick={() => {
                if (importOpen) setSaved(importOpen);
                setImportOpen(null);
                setToast("备份已导入");
              }}
            >
              确认替换并导入
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      {toast && (
        <div className="toast" role="status">
          <Check size={18} />
          {toast}
          <button aria-label="关闭提示" onClick={() => setToast("")}>
            <X size={16} />
          </button>
        </div>
      )}
      {storageError && (
        <div className="storage-warning" role="alert">
          <FloppyDisk size={18} />
          浏览器存储不可用，请导出备份以保留足迹。
          <button onClick={exportData}>立即导出</button>
        </div>
      )}
    </div>
  );
}
export default App;
