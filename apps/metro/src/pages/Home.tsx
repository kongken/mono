import { Link } from "@/lib/router";
import { loadCities, loadTopics } from "@/lib/catalog";
import { describeError } from "@/lib/errors";
import { formatKm } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { Alert, PageTitle, Spinner } from "@/components/extras";

export default function Home() {
  const [cities] = useAsync(loadCities, []);
  const [topics] = useAsync(loadTopics, []);
  return (
    <>
      <PageTitle sub="浏览各城市的地铁线路，登录后记录你坐过的线路。">城市</PageTitle>
      {cities.status === "loading" && <Spinner />}
      {cities.status === "error" && <Alert>{describeError(cities.error)}</Alert>}
      {cities.status === "ready" &&
        (cities.value.length === 0 ? (
          <p className="text-sm text-muted-foreground">还没有城市数据。</p>
        ) : (
          <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {cities.value.map((c) => (
              <li key={c.slug}>
                <Link to={`/cities/${encodeURIComponent(c.slug)}`} className="block rounded-lg border bg-card p-4 hover:border-primary">
                  <div className="text-lg font-medium">{c.name}</div>
                  <div className="mt-1 text-sm text-muted-foreground">
                    {c.lineCount} 条线路 · {formatKm(c.totalLengthKm)}
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        ))}

      <h2 className="mb-3 mt-10 text-lg font-semibold">车型专题</h2>
      {topics.status === "loading" && <Spinner />}
      {topics.status === "error" && <Alert>{describeError(topics.error)}</Alert>}
      {topics.status === "ready" &&
        (topics.value.length === 0 ? (
          <p className="text-sm text-muted-foreground">暂无专题。</p>
        ) : (
          <ul className="flex flex-wrap gap-2">
            {topics.value.map((t) => (
              <li key={t.slug}>
                <Link to={`/topics/${encodeURIComponent(t.slug)}`} className="inline-block rounded-full border bg-card px-3 py-1.5 text-sm hover:border-primary">
                  {t.name}
                </Link>
              </li>
            ))}
          </ul>
        ))}
    </>
  );
}
