import { Code } from "@connectrpc/connect";
import { catalog } from "@/lib/rpc";
import { asConnectError, describeError } from "@/lib/errors";
import { formatKm } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { Link } from "@/lib/router";
import { Alert, PageTitle, Spinner, Stat } from "@/components/extras";
import { LineCard } from "@/components/LineCard";
import NotFound from "./NotFound";
import { useRides } from "./useRides";

export default function TopicPage({ slug }: { slug: string }) {
  const [res] = useAsync(() => catalog.getRollingStockTopic({ slug }), [slug]);
  const rides = useRides();
  if (res.status === "loading") return <Spinner />;
  if (res.status === "error") {
    if (asConnectError(res.error).code === Code.NotFound) return <NotFound what="专题" />;
    return <Alert>{describeError(res.error)}</Alert>;
  }
  const { topic, lines, cities } = res.value;
  if (!topic) return <NotFound what="专题" />;
  const maxKm = Math.max(1, ...cities.map((c) => c.totalLengthKm));
  return (
    <>
      <p className="mb-2 text-sm">
        <Link to="/" className="text-primary underline">城市</Link> / 专题 / {topic.name}
      </p>
      <PageTitle>{topic.name}</PageTitle>
      <div className="mb-5 grid grid-cols-2 gap-3 sm:max-w-md">
        <Stat label="命中线路" value={res.value.totalLines} />
        <Stat label="总里程" value={formatKm(res.value.totalLengthKm)} />
      </div>
      {cities.length > 0 && (
        <section aria-labelledby="by-city" className="mb-6 rounded-lg border bg-card p-4">
          <h2 id="by-city" className="mb-3 text-sm font-semibold">按城市分布</h2>
          <ul className="grid gap-2">
            {cities.map((c) => (
              <li key={c.citySlug} className="grid grid-cols-[6rem_1fr_auto] items-center gap-3 text-sm">
                <Link to={`/cities/${encodeURIComponent(c.citySlug)}`} className="truncate text-primary underline">{c.cityName}</Link>
                <div className="h-3 rounded bg-muted" role="img" aria-label={`${c.cityName} ${c.lineCount} 条，${formatKm(c.totalLengthKm)}`}>
                  <div className="h-3 rounded bg-primary" style={{ width: `${Math.max(2, (c.totalLengthKm / maxKm) * 100)}%` }} />
                </div>
                <span className="tabular-nums text-muted-foreground">{c.lineCount} 条 · {formatKm(c.totalLengthKm)}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {rides.error ? <Alert className="mb-3">{rides.error}</Alert> : null}
      {lines.length === 0 ? (
        <p className="text-sm text-muted-foreground">没有线路符合这个专题。</p>
      ) : (
        <ul className="grid gap-2">
          {lines.map((l) => (
            <LineCard key={l.code} line={l} showCity ridden={rides.signedIn ? rides.codes.has(l.code) : undefined} onToggle={rides.toggle} busy={rides.busy === l.code} />
          ))}
        </ul>
      )}
    </>
  );
}
