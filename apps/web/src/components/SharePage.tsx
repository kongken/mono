import { useEffect, useMemo, useState } from "react";
import { geoPath } from "d3-geo";
import { Compass, MapPin } from "@phosphor-icons/react";
import { Button } from "@trip-map/ui/button";
import { fetchPublicShare, publicApi, ShareNotFound, sharePhotoUrl } from "@/api/public";
import { normalizeMap, type MapData } from "@/lib/maps";
import { mapProjection, SEA_INSET, seaInsetPaths } from "@/lib/projection";
import type { ShareContent, SharePhoto } from "@/lib/share";
import { nationalProvinceColors } from "@/lib/travel";
import regions from "@trip-map/geo-data/regions.json";
import "./SharePage.css";

type State =
  | { kind: "loading" }
  | { kind: "ready"; content: ShareContent }
  | { kind: "missing" }
  | { kind: "error" }
  | { kind: "disabled" };

const provinceName = (code: string) => regions.find((p) => p.code === code)?.name ?? code;
const percent = (n: number, total: number) => (total ? `${((n / total) * 100).toFixed(1)}%` : "0.0%");

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <main className="share-page">
      <header className="share-brand">
        <span className="share-brand-icon">
          <Compass size={22} weight="duotone" />
        </span>
        <span>山海足迹</span>
      </header>
      {children}
    </main>
  );
}

function Photos({ token, photos }: { token: string; photos: SharePhoto[] }) {
  const [failed, setFailed] = useState<Set<string>>(new Set());
  const shown = photos.filter((p) => !failed.has(p.id));
  if (!shown.length) return null;
  return (
    <ul className="share-photos">
      {shown.map((p) => (
        <li key={p.id}>
          <img
            src={sharePhotoUrl(token, p.id)}
            alt="旅途照片"
            width={p.w}
            height={p.h}
            loading="lazy"
            decoding="async"
            referrerPolicy="no-referrer"
            onError={() => setFailed((prev) => new Set(prev).add(p.id))}
          />
        </li>
      ))}
    </ul>
  );
}

function ShareMap({ content }: { content: ShareContent }) {
  const [map, setMap] = useState<MapData | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    fetch(`${import.meta.env.BASE_URL}maps/100000.json`, { signal: controller.signal, credentials: "omit" })
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error("map"))))
      .then((data) => setMap(normalizeMap(data as MapData)))
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => controller.abort();
  }, []);
  const drawing = useMemo(() => {
    if (!map) return null;
    const path = geoPath(mapProjection(map));
    return { path, inset: seaInsetPaths(map) };
  }, [map]);
  // The same fixed palette as the app and the preview image.
  const colors = useMemo(
    () => nationalProvinceColors({ version: 2, visits: [], provinces: Object.fromEntries(content.provinces.map((c) => [c, "x"])) }),
    [content.provinces],
  );
  if (failed) return <p className="share-note">地图暂时无法显示，下方仍列出已探索的省份。</p>;
  if (!map || !drawing) return <div className="share-map share-map-loading" aria-hidden />;
  const explored = new Set(content.provinces);
  return (
    <svg
      className="share-map"
      viewBox="0 0 850 620"
      role="img"
      aria-label={`中国地图，已探索 ${content.stats.explored_provinces} 个省份`}
    >
      <g>
        {map.features
          .filter((f) => f.properties.name)
          .map((f) => {
            const code = String(f.properties.adcode);
            const d = drawing.path(f);
            if (!d) return null;
            return (
              <path key={code} d={d} fill={explored.has(code) ? colors[code] : "#e9eee9"} stroke="#fffefa" strokeWidth="0.8">
                <title>{`${provinceName(code)}${explored.has(code) ? " · 已探索" : ""}`}</title>
              </path>
            );
          })}
      </g>
      <g transform={`translate(${SEA_INSET.x},${SEA_INSET.y})`}>
        <rect width={SEA_INSET.width} height={SEA_INSET.height} rx="4" fill="#fafbf8" stroke="#d6ddd7" />
        {drawing.inset.map((d, i) => (
          <path key={i} d={d} fill="none" stroke="#b6c5ba" strokeWidth="1" />
        ))}
        <text x="41" y="99" textAnchor="middle" fontSize="10" fill="#7f8e74">
          南海诸岛
        </text>
      </g>
    </svg>
  );
}

function Content({ token, content }: { token: string; content: ShareContent }) {
  const { stats, fields } = content;
  const byProvince = new Map(stats.by_province.map((p) => [p.code, p]));
  const provinces = regions.filter((p) => content.provinces.includes(p.code));
  const cities = (code: string) => content.cities.filter((c) => c.province === code);
  return (
    <>
      <section aria-labelledby="share-title" className="share-intro">
        <h1 id="share-title">我的旅行足迹</h1>
        <p>发布于 {content.published_on}。这是作者发布时的内容，不会随后续记录自动更新。</p>
      </section>
      <dl className="share-stats">
        <div>
          <dt>已探索省份</dt>
          <dd>
            {stats.explored_provinces} <small>/ {stats.total_provinces}</small>
          </dd>
        </div>
        <div>
          <dt>打卡城市</dt>
          <dd>
            {stats.cities} <small>/ {stats.total_cities}</small>
          </dd>
        </div>
        <div>
          <dt>旅行次数</dt>
          <dd>{stats.trips}</dd>
        </div>
      </dl>
      <ShareMap content={content} />
      <section aria-labelledby="share-progress">
        <h2 id="share-progress">省份进度</h2>
        {provinces.length === 0 ? (
          <p className="share-note">还没有探索的省份。</p>
        ) : (
          <ul className="share-provinces">
            {provinces.map((p) => {
              const stat = byProvince.get(p.code);
              return (
                <li key={p.code}>
                  <span>{p.name}</span>
                  {stat ? (
                    <>
                      <progress value={stat.cities} max={stat.total} aria-label={`${p.name}城市进度`} />
                      <span>
                        {stat.cities} / {stat.total} · {percent(stat.cities, stat.total)}
                      </span>
                    </>
                  ) : (
                    <span className="share-marked">已标记</span>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>
      {fields.cities && (
        <section aria-labelledby="share-cities">
          <h2 id="share-cities">城市足迹</h2>
          {content.cities.length === 0 ? (
            <p className="share-note">作者还没有打卡城市。</p>
          ) : (
            provinces
              .filter((p) => cities(p.code).length)
              .map((p) => (
                <div key={p.code} className="share-province-group">
                  <h3>{p.name}</h3>
                  <ul className="share-cities">
                    {cities(p.code).map((c) => (
                      <li key={c.code}>
                        <div className="share-city-head">
                          <i style={{ background: c.color }} aria-hidden />
                          <MapPin size={15} aria-hidden />
                          <strong>{c.name}</strong>
                          <small>{c.trips.length} 次旅行</small>
                        </div>
                        {fields.photos && c.photos && c.photos.length > 0 && <Photos token={token} photos={c.photos} />}
                        {c.trips.map((t, i) => {
                          const any = (fields.dates && t.date) || (fields.notes && t.note) || (t.photos && t.photos.length);
                          return any ? (
                            <div key={i} className="share-trip">
                              {fields.dates && t.date && <time>{t.date}</time>}
                              {fields.notes && t.note && <p>{t.note}</p>}
                              {fields.photos && t.photos && t.photos.length > 0 && <Photos token={token} photos={t.photos} />}
                            </div>
                          ) : null;
                        })}
                      </li>
                    ))}
                  </ul>
                </div>
              ))
          )}
        </section>
      )}
      <footer className="share-footer">
        <a href={import.meta.env.BASE_URL}>创建我自己的足迹地图</a>
      </footer>
    </>
  );
}

/**
 * The anonymous, read-only page behind /s/<token>. It never mounts the application,
 * reads no local data or session, and sends no credentials.
 */
export default function SharePage({ token }: { token: string }) {
  const [state, setState] = useState<State>(publicApi.enabled ? { kind: "loading" } : { kind: "disabled" });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    document.title = "旅行足迹 · 山海足迹";
    if (!publicApi.enabled) return;
    const controller = new AbortController();
    setState({ kind: "loading" });
    fetchPublicShare(token, controller.signal)
      .then((content) => setState({ kind: "ready", content }))
      .catch((e) => {
        if (controller.signal.aborted) return;
        setState({ kind: e instanceof ShareNotFound ? "missing" : "error" });
      });
    return () => controller.abort();
  }, [token, attempt]);
  switch (state.kind) {
    case "ready":
      return (
        <Shell>
          <Content token={token} content={state.content} />
        </Shell>
      );
    case "loading":
      return (
        <Shell>
          <p className="share-note" role="status">
            正在打开…
          </p>
        </Shell>
      );
    case "error":
      return (
        <Shell>
          <h1>暂时无法打开</h1>
          <p className="share-note" role="alert">
            网络或服务暂时不可用，请稍后重试。
          </p>
          <Button variant="outline" onClick={() => setAttempt((n) => n + 1)}>
            重试
          </Button>
        </Shell>
      );
    case "disabled":
      return (
        <Shell>
          <h1>此站点未开启分享</h1>
          <p className="share-note">这个地址没有连接到分享服务。</p>
        </Shell>
      );
    default:
      return <NotFound />;
  }
}

/** One page for every link that cannot be shown: unknown, revoked or malformed. */
export function NotFound() {
  return (
    <Shell>
      <h1>链接不存在或已撤销</h1>
      <p className="share-note">请向分享者确认链接是否有效。</p>
    </Shell>
  );
}
