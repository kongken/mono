import { Code } from "@connectrpc/connect";
import { rider } from "@/lib/rpc";
import { asConnectError, describeError } from "@/lib/errors";
import { formatKm, httpsOnly, socialLink } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { Alert, Eyebrow, Spinner, Stat } from "@/components/extras";
import { container } from "@/components/Layout";
import { RiddenCities } from "@/components/RiddenCities";
import NotFound from "./NotFound";

const platforms: [string, string][] = [
  ["twitter", "X / Twitter"],
  ["instagram", "Instagram"],
  ["weibo", "微博"],
  ["facebook", "Facebook"],
  ["github", "GitHub"],
];

export default function PublicProfilePage({ handle }: { handle: string }) {
  const [res] = useAsync(() => rider.getPublicProfile({ handle }), [handle]);
  if (res.status === "loading") return <div className={container}><Spinner /></div>;
  if (res.status === "error") {
    // A private profile and a missing one are the same answer on purpose.
    if (asConnectError(res.error).code === Code.NotFound) return <NotFound what="主页" />;
    return <div className={`${container} py-8`}><Alert>{describeError(res.error)}</Alert></div>;
  }
  const { profile, cities, stats } = res.value;
  if (!profile) return <NotFound what="主页" />;
  const header = profile.headerImages.map(httpsOnly).find(Boolean);
  const links = platforms
    .map(([key, label]) => ({ label, href: socialLink(key, profile[key as "twitter"]) }))
    .filter((l): l is { label: string; href: string } => Boolean(l.href));
  return (
    <>
      <section className="relative overflow-hidden bg-deep text-white">
        {header ? <img src={header} alt="" className="absolute inset-0 size-full object-cover opacity-40" /> : null}
        <div className="absolute inset-0 bg-gradient-to-t from-deep via-deep/70 to-deep/30" aria-hidden />
        <div className={`${container} relative flex min-h-52 flex-col justify-end gap-2 pt-10 pb-8 md:min-h-64`}>
          <Eyebrow className="text-white/70">Rider profile</Eyebrow>
          <h1 className="font-headline text-4xl font-bold tracking-tight md:text-5xl">{profile.displayName || `@${profile.handle}`}</h1>
          {profile.subtitle ? <p className="text-base text-white/85">{profile.subtitle}</p> : null}
          <p className="font-code text-sm text-white/70">
            @{profile.handle}
            {profile.location ? ` · ${profile.location}` : ""}
          </p>
        </div>
      </section>
      <div className={`${container} py-8 md:py-10`}>
        <div className="mb-8 grid gap-3 sm:grid-cols-3 lg:max-w-3xl">
          <Stat label="城市" value={stats?.cityCount ?? 0} />
          <Stat label="线路" value={stats?.lineCount ?? 0} />
          <Stat label="里程" value={formatKm(stats?.totalLengthKm ?? 0)} />
        </div>
        {profile.bio ? <p className="mb-5 max-w-2xl whitespace-pre-wrap text-base">{profile.bio}</p> : null}
        {links.length > 0 && (
          <ul className="mb-8 flex flex-wrap gap-2 text-sm">
            {links.map((l) => (
              <li key={l.label}>
                <a href={l.href} rel="noopener noreferrer nofollow" target="_blank" className="inline-block rounded-full bg-surface-lowest px-3.5 py-1.5 font-semibold text-ink shadow-card hover:shadow-float">{l.label}</a>
              </li>
            ))}
          </ul>
        )}
        <h2 className="mb-4 font-headline text-2xl font-bold text-ink">乘坐记录</h2>
        {cities.length === 0 ? <p className="text-sm text-on-variant">还没有乘坐记录。</p> : <RiddenCities cities={cities} />}
      </div>
    </>
  );
}
