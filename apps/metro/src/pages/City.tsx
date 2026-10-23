import { catalog } from "@/lib/rpc";
import { describeError } from "@/lib/errors";
import { formatKm, httpsOnly } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { Link } from "@/lib/router";
import { Alert, PageTitle, Spinner, Stat } from "@/components/extras";
import { LineCard } from "@/components/LineCard";
import NotFound from "./NotFound";
import { useRides } from "./useRides";
import { Code } from "@connectrpc/connect";
import { asConnectError } from "@/lib/errors";

export default function CityPage({ slug }: { slug: string }) {
  const [res] = useAsync(() => catalog.getCity({ slug }), [slug]);
  const rides = useRides();
  if (res.status === "loading") return <Spinner />;
  if (res.status === "error") {
    if (asConnectError(res.error).code === Code.NotFound) return <NotFound what="城市" />;
    return <Alert>{describeError(res.error)}</Alert>;
  }
  const { city, lines } = res.value;
  if (!city) return <NotFound what="城市" />;
  const header = city.headerImages.map(httpsOnly).find(Boolean);
  const map = httpsOnly(city.mapSvgUrl);
  return (
    <>
      <p className="mb-2 text-sm">
        <Link to="/" className="text-primary underline">城市</Link> / {city.name}
      </p>
      {header ? <img src={header} alt="" className="mb-4 h-40 w-full rounded-lg object-cover sm:h-56" /> : null}
      <PageTitle>{city.name}</PageTitle>
      <div className="mb-5 grid grid-cols-2 gap-3 sm:max-w-md">
        <Stat label="运营线路" value={res.value.totalLines} />
        <Stat label="总里程" value={formatKm(res.value.totalLengthKm)} />
      </div>
      {rides.error ? <Alert className="mb-3">{rides.error}</Alert> : null}
      {lines.length === 0 ? (
        <p className="text-sm text-muted-foreground">这个城市暂时没有运营线路。</p>
      ) : (
        <ul className="grid gap-2">
          {lines.map((l) => (
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
        <section className="mt-8">
          <h2 className="mb-2 text-lg font-semibold">线路图</h2>
          {/* Shown as an image only: an SVG from a URL is never inlined into the page. */}
          <img src={map} alt={`${city.name}线路图`} className="w-full rounded-lg border bg-white" loading="lazy" />
        </section>
      ) : null}
    </>
  );
}
