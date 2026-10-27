import { Code } from "@connectrpc/connect";
import { catalog } from "@/lib/rpc";
import { asConnectError, describeError } from "@/lib/errors";
import { formatKm } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { Link } from "@/lib/router";
import { Alert, Eyebrow, PageTitle, Panel, Spinner, Stat } from "@/components/extras";
import { container } from "@/components/Layout";
import { LineCard } from "@/components/LineCard";
import { RideEditor } from "@/components/RideEditor";
import { Button } from "@kongken/ui/button";
import NotFound from "./NotFound";
import { useRides } from "./useRides";

export default function TopicPage({ slug }: { slug: string }) {
  const [res] = useAsync(() => catalog.getRollingStockTopic({ slug }), [slug]);
  const rides = useRides();
  if (res.status === "loading") return <div className={container}><Spinner /></div>;
  if (res.status === "error") {
    if (asConnectError(res.error).code === Code.NotFound) return <NotFound what="专题" />;
    return <div className={`${container} py-8`}><Alert>{describeError(res.error)}</Alert></div>;
  }
  const { topic, lines, cities } = res.value;
  if (!topic) return <NotFound what="专题" />;
  const byCount = [...cities].sort((a, b) => b.lineCount - a.lineCount || a.citySlug.localeCompare(b.citySlug));
  const maxCount = Math.max(1, ...cities.map((c) => c.lineCount));
  const riddenCount = lines.filter((l) => rides.codes.has(l.code)).length;
  const percent = lines.length ? Math.round((riddenCount / lines.length) * 1000) / 10 : 0;
  return (
    <div className={`${container} py-8 md:py-12`}>
      <p className="mb-3 font-code text-xs tracking-wider text-on-variant">
        <Link to="/topics" className="underline-offset-2 hover:underline">车型专题</Link> / {topic.name}
      </p>
      <PageTitle eyebrow={<Eyebrow className="text-brand">Rolling stock topic</Eyebrow>}>{topic.name}</PageTitle>
      <div className="mb-6 grid gap-3 sm:grid-cols-3">
        <Stat label="命中线路" value={res.value.totalLines} unit="条" />
        <Stat label="总里程" value={formatKm(res.value.totalLengthKm)} />
        <Stat label="已坐过" value={rides.ready ? `${riddenCount} / ${lines.length}` : "—"} unit="条" note={rides.ready ? `${percent}% 已完成` : rides.signedIn ? "正在加载足迹；读取失败时可重试。" : "登录后查看专题完成度"} />
      </div>
      {cities.length > 0 && (
        <Panel aria-labelledby="by-city" className="mb-8 p-5">
          <h2 id="by-city" className="font-headline text-lg font-bold text-ink">按城市分布</h2>
          <p className="mt-1 mb-4 text-sm text-on-variant">按线路数量排序，单位：条；里程作为补充。</p>
          <ul className="grid gap-3">
            {byCount.map((c) => (
              <li key={c.citySlug} className="grid grid-cols-[4rem_minmax(0,1fr)] items-center gap-x-3 gap-y-1 text-sm sm:grid-cols-[6rem_minmax(0,1fr)_auto]">
                <Link to={`/cities/${encodeURIComponent(c.citySlug)}`} className="truncate font-semibold text-ink hover:underline">{c.cityName}</Link>
                <div className="h-3 rounded-full bg-surface-high" role="img" aria-label={`${c.cityName} ${c.lineCount} 条，${formatKm(c.totalLengthKm)}`}>
                  <div className="h-3 rounded-full bg-brand" style={{ width: `${(c.lineCount / maxCount) * 100}%` }} />
                </div>
                <span className="num col-start-2 text-on-variant sm:col-start-auto">{c.lineCount} 条 · {formatKm(c.totalLengthKm)}</span>
              </li>
            ))}
          </ul>
        </Panel>
      )}
      {rides.error ? <Alert className="mb-4">{rides.error} <Button variant="outline" size="sm" onClick={rides.retry}>重新加载足迹</Button></Alert> : null}
      {lines.length === 0 ? (
        <p className="text-sm text-on-variant">没有线路符合这个专题。</p>
      ) : (
        <section aria-labelledby="topic-ledger" className="grid gap-6">
          <h2 id="topic-ledger" className="font-headline text-2xl font-bold text-ink">各城市线路台账</h2>
          {byCount.map((c) => (
            <section key={c.citySlug} aria-label={`${c.cityName}线路台账`}>
              <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                <h3 className="font-headline text-xl font-bold text-ink">{c.cityName} <span className="text-sm font-medium text-on-variant">{c.lineCount} 条 · {formatKm(c.totalLengthKm)}</span></h3>
                <Link to={`/cities/${encodeURIComponent(c.citySlug)}`} className="inline-flex min-h-11 items-center text-sm font-semibold text-brand underline underline-offset-4" aria-label={`${c.cityName}城市详情`}>城市详情 →</Link>
              </div>
              <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                {lines.filter((l) => l.citySlug === c.citySlug).map((l) => (
                  <LineCard key={l.code} line={l} ridden={rides.ready ? rides.codes.has(l.code) : undefined} riddenYear={rides.records?.get(l.code)} onEdit={rides.edit} busy={rides.disabled} />
                ))}
              </ul>
            </section>
          ))}
        </section>
      )}
      {rides.editing ? <RideEditor key={rides.editing.code} line={rides.editing} year={rides.records?.get(rides.editing.code)} ridden={rides.codes.has(rides.editing.code)} onSave={rides.save} onRemove={rides.remove} onClose={rides.close} /> : null}
    </div>
  );
}
