import { ArrowRight, Train } from "@phosphor-icons/react";
import type { RollingStockTopic } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { Link } from "@/lib/router";
import { loadTopics } from "@/lib/catalog";
import { describeError } from "@/lib/errors";
import { useAsync } from "@/lib/useAsync";
import { Alert, Eyebrow, PageTitle, Spinner } from "@/components/extras";
import { container } from "@/components/Layout";

export function TopicTile({ topic }: { topic: RollingStockTopic }) {
  const facts = [topic.cars.length ? `${topic.cars.join(" / ")} 节编组` : "", topic.rollingStockCodes.length ? `${topic.rollingStockCodes.length} 种车型` : ""].filter(Boolean);
  return (
    <li>
      <Link
        to={`/topics/${encodeURIComponent(topic.slug)}`}
        className="group flex h-full items-center gap-4 rounded-xl bg-surface-lowest p-5 shadow-card transition-shadow hover:shadow-float"
      >
        <span className="flex size-12 shrink-0 items-center justify-center rounded-xl bg-brand-container text-on-brand-container">
          <Train className="size-6" weight="duotone" aria-hidden />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block font-headline text-lg font-bold text-ink">{topic.name}</span>
          {facts.length ? <Eyebrow className="mt-1 block normal-case">{facts.join(" · ")}</Eyebrow> : null}
        </span>
        <ArrowRight className="size-5 shrink-0 text-outline transition-transform group-hover:translate-x-0.5 group-hover:text-ink" aria-hidden />
      </Link>
    </li>
  );
}

/** `/topics`: every rolling-stock topic of the catalogue. */
export default function TopicsIndex() {
  const [topics] = useAsync(loadTopics, []);
  return (
    <div className={`${container} py-8 md:py-12`}>
      <PageTitle eyebrow={<Eyebrow className="text-brand">Rolling stock</Eyebrow>} sub="按车型与编组汇总线路，看看哪些城市有同款列车。">车型专题</PageTitle>
      {topics.status === "loading" && <Spinner />}
      {topics.status === "error" && <Alert>{describeError(topics.error)}</Alert>}
      {topics.status === "ready" &&
        (topics.value.length === 0 ? (
          <p className="text-sm text-on-variant">暂无专题。</p>
        ) : (
          <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {topics.value.map((t) => (
              <TopicTile key={t.slug} topic={t} />
            ))}
          </ul>
        ))}
    </div>
  );
}
