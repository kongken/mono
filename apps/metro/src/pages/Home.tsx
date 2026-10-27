import { useMemo, useState } from "react";
import { ArrowRight, MagnifyingGlass, Path } from "@phosphor-icons/react";
import type { City } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { regions, regionLabel } from "@kongken/metro-core/catalog";
import { Link } from "@/lib/router";
import { loadCities, loadLines, loadTopics } from "@/lib/catalog";
import { matchesLine } from "@/lib/discovery";
import { describeError } from "@/lib/errors";
import { formatKm, httpsOnly } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { Alert, Eyebrow, Spinner } from "@/components/extras";
import { container } from "@/components/Layout";
import { FilterChips } from "@/components/FilterChips";

export function CityCard({ city }: { city: City }) {
  const image = city.headerImages.map(httpsOnly).find(Boolean);
  return (
    <li>
      <Link
        to={`/cities/${encodeURIComponent(city.slug)}`}
        className="group flex h-full flex-col overflow-hidden rounded-xl bg-surface-lowest shadow-card transition-shadow hover:shadow-float"
      >
        <div className="relative h-32 overflow-hidden bg-deep">
          {image ? (
            <img src={image} alt="" loading="lazy" className="size-full object-cover transition-transform duration-500 group-hover:scale-105" />
          ) : (
            <span className="absolute -right-2 -bottom-6 font-headline text-[8rem] leading-none font-bold text-white/10 select-none" aria-hidden>
              {city.name.slice(0, 1)}
            </span>
          )}
          <div className="absolute inset-0 bg-gradient-to-t from-deep/80 to-transparent" aria-hidden />
          <h3 className="absolute bottom-3 left-4 font-headline text-2xl font-bold text-white">{city.name}</h3>
          {city.region ? <span className="absolute top-3 left-4 rounded-full bg-deep/80 px-2 py-1 text-xs text-white">{regionLabel(city.region)}</span> : null}
        </div>
        <div className="flex flex-1 items-end justify-between gap-3 p-4">
          <dl className="flex gap-6">
            <div>
              <dt><Eyebrow className="block">线路</Eyebrow></dt>
              <dd className="num mt-0.5 font-headline text-xl font-bold text-ink">{city.lineCount} <span className="text-sm font-semibold text-on-variant">条</span></dd>
            </div>
            <div>
              <dt><Eyebrow className="block">里程</Eyebrow></dt>
              <dd className="num mt-0.5 font-headline text-xl font-bold text-ink">{formatKm(city.totalLengthKm)}</dd>
            </div>
          </dl>
          <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-surface-high text-ink transition-colors group-hover:bg-deep group-hover:text-white" aria-hidden>
            <ArrowRight className="size-4" weight="bold" />
          </span>
        </div>
      </Link>
    </li>
  );
}

export default function Home() {
  const [cities] = useAsync(loadCities, []);
  const [topics] = useAsync(loadTopics, []);
  const [lines] = useAsync(loadLines, []);
  const [query, setQuery] = useState("");
  const [region, setRegion] = useState("all");
  const shown = useMemo(() => {
    if (cities.status !== "ready") return [];
    const q = query.trim().toLowerCase();
    const lineCities = new Set(lines.status === "ready" && q ? lines.value.filter((l) => matchesLine(l, q)).map((l) => l.citySlug) : []);
    return cities.value.filter((c) => (region === "all" || (region === "unknown" ? !c.region : c.region === region)) &&
      (!q || c.name.toLowerCase().includes(q) || c.slug.toLowerCase().includes(q) || lineCities.has(c.slug)));
  }, [cities, lines, query, region]);
  const totals = cities.status === "ready" ? { cities: cities.value.length, lines: cities.value.reduce((n, c) => n + c.lineCount, 0), km: cities.value.reduce((n, c) => n + c.totalLengthKm, 0) } : undefined;

  return (
    <>
      <section className="bg-surface-low">
        <div className={`${container} grid gap-8 py-10 md:py-14 lg:grid-cols-[1fr_auto] lg:items-end`}>
          <div className="max-w-2xl">
            <Eyebrow className="text-brand">Metro footprint archive</Eyebrow>
            <h1 className="mt-3 font-headline text-4xl leading-[1.1] font-bold tracking-tight text-ink md:text-5xl">探索城市地铁网络</h1>
            <p className="mt-4 text-base text-on-variant md:text-lg">浏览各城市的地铁线路与车型专题；登录后，记录你坐过的每一条线路。</p>
            <label className="relative mt-6 block max-w-lg">
              <span className="sr-only">搜索城市或线路</span>
              <MagnifyingGlass className="pointer-events-none absolute top-1/2 left-3.5 size-5 -translate-y-1/2 text-outline" aria-hidden />
              <input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="城市、拼音或线路名称，例如 上海、Beijing、1号线"
                className="h-12 w-full rounded-xl bg-surface-lowest pr-4 pl-11 text-base shadow-card outline-none placeholder:text-outline focus-visible:ring-[3px] focus-visible:ring-ring/30"
              />
            </label>
          </div>
          {totals ? (
            <dl className="grid grid-cols-3 gap-6 rounded-xl bg-surface-lowest px-6 py-4 shadow-card" aria-label="目录概览">
              {[
                ["城市", String(totals.cities), ""],
                ["线路", String(totals.lines), "条"],
                ["里程", String(Math.round(totals.km)), "km"],
              ].map(([label, value, unit]) => (
                <div key={label}>
                  <dt><Eyebrow className="block">{label}</Eyebrow></dt>
                  <dd className="num mt-1 font-headline text-3xl font-bold text-ink">
                    {value}
                    {unit ? <span className="ml-1 text-sm font-semibold text-on-variant">{unit}</span> : null}
                  </dd>
                </div>
              ))}
            </dl>
          ) : null}
        </div>
      </section>

      <div className={`${container} py-8 md:py-12`}>
        <section aria-labelledby="cities-h">
          <div className="mb-5 flex items-end justify-between gap-3">
            <h2 id="cities-h" className="font-headline text-2xl font-bold text-ink">城市</h2>
            {cities.status === "ready" ? <Eyebrow>{shown.length} / {cities.value.length} cities</Eyebrow> : null}
          </div>
          <div className="mb-5 grid gap-3">
            <FilterChips label="筛选区域" value={region} options={[{ value: "all", label: "全部区域" }, ...regions, { value: "unknown", label: "未分类" }]} onChange={setRegion} />
            <p role="status" className="text-sm text-on-variant">{cities.status === "ready" ? `找到 ${shown.length} 座城市` : "正在加载城市"}</p>
            {query.trim() && lines.status === "loading" ? <Spinner label="正在加载线路搜索…" /> : null}
            {lines.status === "error" ? <Alert kind="info">线路搜索暂时不可用，可以按城市名称搜索，或刷新后重试。</Alert> : null}
          </div>
          {cities.status === "loading" && <Spinner />}
          {cities.status === "error" && <Alert>{describeError(cities.error)}</Alert>}
          {cities.status === "ready" &&
            (cities.value.length === 0 ? (
              <p className="text-sm text-on-variant">还没有城市数据。</p>
            ) : shown.length === 0 ? (
              <div className="grid justify-items-start gap-3 rounded-xl bg-surface-lowest p-6 shadow-card">
                <p className="text-sm text-on-variant">没有找到符合当前区域和搜索条件的城市。</p>
                <button type="button" className="min-h-11 rounded-lg bg-deep px-4 py-2 text-sm font-semibold text-white" onClick={() => { setQuery(""); setRegion("all"); }}>重置搜索条件</button>
              </div>
            ) : (
              <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                {shown.map((c) => (
                  <CityCard key={c.slug} city={c} />
                ))}
              </ul>
            ))}
        </section>

        <section aria-labelledby="topics-h" className="mt-12">
          <div className="mb-5 flex items-end justify-between gap-3">
            <h2 id="topics-h" className="font-headline text-2xl font-bold text-ink">车型专题</h2>
            <Link to="/topics" className="text-sm font-semibold text-brand hover:underline">查看全部 →</Link>
          </div>
          {topics.status === "loading" && <Spinner />}
          {topics.status === "error" && <Alert>{describeError(topics.error)}</Alert>}
          {topics.status === "ready" &&
            (topics.value.length === 0 ? (
              <p className="text-sm text-on-variant">暂无专题。</p>
            ) : (
              <ul className="flex flex-wrap gap-2">
                {topics.value.map((t) => (
                  <li key={t.slug}>
                    <Link to={`/topics/${encodeURIComponent(t.slug)}`} className="inline-block rounded-full bg-surface-lowest px-4 py-2 text-sm font-semibold text-ink shadow-card transition-shadow hover:shadow-float">
                      {t.name}
                    </Link>
                  </li>
                ))}
              </ul>
            ))}
        </section>

        <section className="mt-12 flex flex-wrap items-center justify-between gap-4 rounded-xl bg-deep p-6 text-white md:p-8">
          <div className="flex items-start gap-4">
            <Path className="mt-1 size-8 shrink-0" weight="duotone" aria-hidden />
            <div>
              <h2 className="font-headline text-xl font-bold text-white">你的足迹，你的地铁地图</h2>
              <p className="mt-1 text-sm text-white/80">标记坐过的线路，统计城市、线路与里程，并生成公开主页。</p>
            </div>
          </div>
          <Link to="/me" className="rounded-lg bg-white px-5 py-2.5 text-sm font-bold text-deep transition-opacity hover:opacity-90">打开足迹页</Link>
        </section>
      </div>
    </>
  );
}
