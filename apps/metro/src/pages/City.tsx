import { useState } from "react";
import { Code } from "@connectrpc/connect";
import { catalog } from "@/lib/rpc";
import { asConnectError, describeError } from "@/lib/errors";
import { formatKm, httpsOnly } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { Link } from "@/lib/router";
import { cn } from "@kongken/ui/utils";
import { Alert, Eyebrow, Spinner, Stat } from "@/components/extras";
import { container } from "@/components/Layout";
import { LineCard } from "@/components/LineCard";
import NotFound from "./NotFound";
import { useRides } from "./useRides";

type Filter = "all" | "ridden" | "todo";

export default function CityPage({ slug }: { slug: string }) {
  const [res] = useAsync(() => catalog.getCity({ slug }), [slug]);
  const rides = useRides();
  const [filter, setFilter] = useState<Filter>("all");
  if (res.status === "loading") return <div className={container}><Spinner /></div>;
  if (res.status === "error") {
    if (asConnectError(res.error).code === Code.NotFound) return <NotFound what="城市" />;
    return <div className={`${container} py-8`}><Alert>{describeError(res.error)}</Alert></div>;
  }
  const { city, lines } = res.value;
  if (!city) return <NotFound what="城市" />;
  const header = city.headerImages.map(httpsOnly).find(Boolean);
  const map = httpsOnly(city.mapSvgUrl);
  const riddenHere = rides.signedIn ? lines.filter((l) => rides.codes.has(l.code)).length : 0;
  const shown = filter === "all" || !rides.signedIn ? lines : lines.filter((l) => rides.codes.has(l.code) === (filter === "ridden"));
  const filters: [Filter, string][] = [["all", "全部"], ["ridden", "已坐过"], ["todo", "未坐过"]];
  return (
    <>
      <section className="relative overflow-hidden bg-deep text-white">
        {header ? <img src={header} alt="" className="absolute inset-0 size-full object-cover opacity-40" /> : null}
        <div className="absolute inset-0 bg-gradient-to-t from-deep via-deep/70 to-deep/30" aria-hidden />
        <div className={`${container} relative flex min-h-56 flex-col justify-end gap-3 pt-10 pb-8 md:min-h-72`}>
          <nav aria-label="位置" className="font-code text-xs tracking-wider text-white/70">
            <Link to="/" className="underline-offset-2 hover:underline">城市</Link> / {city.name}
          </nav>
          <h1 className="font-headline text-4xl font-bold tracking-tight md:text-6xl">{city.name}</h1>
        </div>
      </section>

      <div className={`${container} py-6 md:py-10`}>
        <div className="-mt-12 mb-8 grid gap-3 sm:grid-cols-2 lg:grid-cols-[1fr_1fr_1.4fr] relative">
          <Stat label="运营线路" value={res.value.totalLines} unit="条" />
          <Stat label="总里程" value={formatKm(res.value.totalLengthKm)} />
          {rides.signedIn ? (
            <div className="rounded-xl bg-brand-container p-4 text-on-brand-container shadow-card sm:col-span-2 lg:col-span-1">
              <Eyebrow className="text-on-brand-container/80">我在这座城市</Eyebrow>
              <div className="mt-2 flex items-baseline gap-1.5">
                <span className="num font-headline text-3xl leading-none font-bold sm:text-4xl">{riddenHere}</span>
                <span className="text-sm font-semibold">/ {lines.length} 条已坐过</span>
              </div>
              <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-white/60" role="img" aria-label={`已坐过 ${riddenHere} 条，共 ${lines.length} 条`}>
                <div className="h-full rounded-full bg-brand" style={{ width: `${lines.length ? (riddenHere / lines.length) * 100 : 0}%` }} />
              </div>
            </div>
          ) : (
            <div className="rounded-xl bg-surface-lowest p-4 shadow-card sm:col-span-2 lg:col-span-1">
              <Eyebrow>我的足迹</Eyebrow>
              <p className="mt-2 text-sm text-on-variant">登录后，点击线路上的「标记坐过」即可记录你的乘坐。</p>
            </div>
          )}
        </div>

        {rides.error ? <Alert className="mb-4">{rides.error}</Alert> : null}
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h2 className="font-headline text-2xl font-bold text-ink">线路</h2>
          {rides.signedIn && lines.length > 0 ? (
            <div role="group" aria-label="筛选线路" className="flex gap-1 rounded-full bg-surface-high p-1">
              {filters.map(([key, label]) => (
                <button
                  key={key}
                  type="button"
                  aria-pressed={filter === key}
                  onClick={() => setFilter(key)}
                  className={cn("rounded-full px-3.5 py-1 text-sm font-semibold transition-colors", filter === key ? "bg-deep text-white" : "text-on-variant hover:text-ink")}
                >
                  {label}
                </button>
              ))}
            </div>
          ) : null}
        </div>
        {lines.length === 0 ? (
          <p className="text-sm text-on-variant">这个城市暂时没有运营线路。</p>
        ) : shown.length === 0 ? (
          <p className="text-sm text-on-variant">这个分类下没有线路。</p>
        ) : (
          <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {shown.map((l) => (
              <LineCard
                key={l.code}
                line={l}
                ridden={rides.signedIn ? rides.codes.has(l.code) : undefined}
                onToggle={rides.toggle}
                busy={rides.busy === l.code}
              />
            ))}
          </ul>
        )}
        {map ? (
          <section className="mt-12" aria-labelledby="map-h">
            <h2 id="map-h" className="mb-3 font-headline text-2xl font-bold text-ink">线路图</h2>
            {/* Shown as an image only: an SVG from a URL is never inlined into the page. */}
            <img src={map} alt={`${city.name}线路图`} className="w-full rounded-xl bg-white p-2 shadow-card" loading="lazy" />
          </section>
        ) : null}
      </div>
    </>
  );
}
