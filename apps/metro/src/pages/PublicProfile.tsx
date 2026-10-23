import { Code } from "@connectrpc/connect";
import { rider } from "@/lib/rpc";
import { asConnectError, describeError } from "@/lib/errors";
import { formatKm, httpsOnly, socialLink } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { Alert, PageTitle, Spinner, Stat } from "@/components/extras";
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
  if (res.status === "loading") return <Spinner />;
  if (res.status === "error") {
    // A private profile and a missing one are the same answer on purpose.
    if (asConnectError(res.error).code === Code.NotFound) return <NotFound what="主页" />;
    return <Alert>{describeError(res.error)}</Alert>;
  }
  const { profile, cities, stats } = res.value;
  if (!profile) return <NotFound what="主页" />;
  const header = profile.headerImages.map(httpsOnly).find(Boolean);
  const links = platforms
    .map(([key, label]) => ({ label, href: socialLink(key, profile[key as "twitter"]) }))
    .filter((l): l is { label: string; href: string } => Boolean(l.href));
  return (
    <>
      {header ? <img src={header} alt="" className="mb-4 h-40 w-full rounded-lg object-cover sm:h-56" /> : null}
      <PageTitle sub={profile.subtitle || undefined}>{profile.displayName || `@${profile.handle}`}</PageTitle>
      <p className="-mt-3 mb-4 text-sm text-muted-foreground">
        @{profile.handle}
        {profile.location ? ` · ${profile.location}` : ""}
      </p>
      {profile.bio ? <p className="mb-4 whitespace-pre-wrap text-sm">{profile.bio}</p> : null}
      {links.length > 0 && (
        <ul className="mb-5 flex flex-wrap gap-3 text-sm">
          {links.map((l) => (
            <li key={l.label}>
              <a href={l.href} rel="noopener noreferrer nofollow" target="_blank" className="text-primary underline">{l.label}</a>
            </li>
          ))}
        </ul>
      )}
      <div className="mb-6 grid grid-cols-3 gap-3 sm:max-w-lg">
        <Stat label="城市" value={stats?.cityCount ?? 0} />
        <Stat label="线路" value={stats?.lineCount ?? 0} />
        <Stat label="里程" value={formatKm(stats?.totalLengthKm ?? 0)} />
      </div>
      {cities.length === 0 ? <p className="text-sm text-muted-foreground">还没有乘坐记录。</p> : <RiddenCities cities={cities} />}
    </>
  );
}
