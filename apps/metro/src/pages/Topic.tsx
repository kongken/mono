import { Code } from "@connectrpc/connect";
import { catalog } from "@/lib/rpc";
import { asConnectError, describeError } from "@/lib/errors";
import { formatKm } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { Link } from "@/lib/router";
import { Alert, Eyebrow, PageTitle, Panel, Spinner, Stat } from "@/components/extras";
import { container } from "@/components/Layout";
import { LineCard } from "@/components/LineCard";
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
  const maxKm = Math.max(1, ...cities.map((c) => c.totalLengthKm));
  return (
    <div className={`${container} py-8 md:py-12`}>
      <p className="mb-3 font-code text-xs tracking-wider text-on-variant">
        <Link to="/topics" className="underline-offset-2 hover:underline">车型专题</Link> / {topic.name}
      </p>
      <PageTitle eyebrow={<Eyebrow className="text-brand">Rolling stock topic</Eyebrow>}>{topic.name}</PageTitle>
      <div className="mb-6 grid grid-cols-2 gap-3 sm:max-w-lg">
        <Stat label="命中线路" value={res.value.totalLines} unit="条" />
        <Stat label="总里程" value={formatKm(res.value.totalLengthKm)} />
      </div>
      {cities.length > 0 && (
        <Panel aria-labelledby="by-city" className="mb-8 p-5">
          <h2 id="by-city" className="mb-4 font-headline text-lg font-bold text-ink">按城市分布</h2>
          <ul className="grid gap-3">
            {cities.map((c) => (
              <li key={c.citySlug} className="grid grid-cols-[5rem_1fr_auto] items-center gap-3 text-sm sm:grid-cols-[6rem_1fr_auto]">
                <Link to={`/cities/${encodeURIComponent(c.citySlug)}`} className="truncate font-semibold text-ink hover:underline">{c.cityName}</Link>
                <div className="h-3 rounded-full bg-surface-high" role="img" aria-label={`${c.cityName} ${c.lineCount} 条，${formatKm(c.totalLengthKm)}`}>
                  <div className="h-3 rounded-full bg-brand" style={{ width: `${Math.max(2, (c.totalLengthKm / maxKm) * 100)}%` }} />
                </div>
                <span className="num whitespace-nowrap text-on-variant">{c.lineCount} 条 · {formatKm(c.totalLengthKm)}</span>
              </li>
            ))}
          </ul>
        </Panel>
      )}
      {rides.error ? <Alert className="mb-4">{rides.error}</Alert> : null}
      {lines.length === 0 ? (
        <p className="text-sm text-on-variant">没有线路符合这个专题。</p>
      ) : (
        <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {lines.map((l) => (
            <LineCard key={l.code} line={l} showCity ridden={rides.signedIn ? rides.codes.has(l.code) : undefined} onToggle={rides.toggle} busy={rides.busy === l.code} />
          ))}
        </ul>
      )}
    </div>
  );
}
