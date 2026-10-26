import { useEffect, useState } from "react";
import type { ProfileInput } from "@kongken/api-client/bff/metro/v1/rider_pb";
import { Button } from "@kongken/ui/button";
import { Input } from "@kongken/ui/input";
import { rider, write } from "@/lib/rpc";
import { describeError, fieldProblems, isUnauthenticated } from "@/lib/errors";
import { formatKm, lines as splitLines, parseYear } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { useSession } from "@/lib/session";
import { ozid } from "@/lib/account-center";
import { Link } from "@/lib/router";
import { Alert, Eyebrow, Field, PageTitle, Panel, Spinner, Stat, Textarea, TextField } from "@/components/extras";
import { container } from "@/components/Layout";
import { Buildings, MapTrifold, Path, Ruler } from "@phosphor-icons/react";
import { RiddenCities } from "@/components/RiddenCities";

const shell = (children: React.ReactNode) => <div className={`${container} py-8 md:py-12`}>{children}</div>;

export default function MePage() {
  const { session, signIn, signingIn, refresh } = useSession();
  if (session.status === "loading") return shell(<Spinner />);
  if (session.status === "unavailable") {
    // Not "please sign in": nothing says the person is signed out, only that the check could not be made.
    return shell(
      <>
        <PageTitle eyebrow={<Eyebrow className="text-brand">My footprints</Eyebrow>}>我的足迹</PageTitle>
        <Alert className="mb-4 max-w-xl">暂时无法确认登录状态，请稍后重新检查。</Alert>
        <Button variant="outline" onClick={() => void refresh()}>重新检查</Button>
      </>,
    );
  }
  if (session.status === "anonymous") {
    return shell(
      <>
        <PageTitle eyebrow={<Eyebrow className="text-brand">My footprints</Eyebrow>}>我的足迹</PageTitle>
        <Panel className="flex max-w-xl flex-col items-start gap-4 p-6">
          <Path className="size-10 text-brand" weight="duotone" aria-hidden />
          <p className="text-base text-on-variant">登录后可以记录坐过的线路，并设置公开主页。</p>
          <Button disabled={signingIn} onClick={signIn}>登录</Button>
        </Panel>
      </>,
    );
  }
  return shell(<SignedInMe />);
}

function SignedInMe() {
  const { refresh } = useSession();
  // A read that is refused because the session ended or changed asks the BFF who the Cookie belongs to now.
  const [rides, reload] = useAsync(() => rider.listMyLines({}).catch((e) => { if (isUnauthenticated(e)) void refresh(); throw e; }), []);
  const [message, setMessage] = useState<{ kind: "error" | "success"; text: string } | undefined>();
  return (
    <>
      <PageTitle eyebrow={<Eyebrow className="text-brand">My footprints</Eyebrow>} sub="你坐过的城市、线路与里程。">我的足迹</PageTitle>
      {message ? <Alert kind={message.kind} className="mb-4">{message.text}</Alert> : null}
      <section aria-labelledby="rides" className="mb-12">
        <h2 id="rides" className="mb-4 font-headline text-2xl font-bold text-ink">乘坐记录</h2>
        {rides.status === "loading" && <Spinner />}
        {rides.status === "error" && <Alert>{describeError(rides.error)}</Alert>}
        {rides.status === "ready" && (
          <>
            <div className="mb-6 grid gap-3 sm:grid-cols-3 lg:max-w-3xl">
              <Stat label="城市" value={rides.value.stats?.cityCount ?? 0} icon={<Buildings />} />
              <Stat label="线路" value={rides.value.stats?.lineCount ?? 0} icon={<MapTrifold />} />
              <Stat label="里程" value={formatKm(rides.value.stats?.totalLengthKm ?? 0)} icon={<Ruler />} />
            </div>
            {rides.value.cities.length === 0 ? (
              <Panel className="flex max-w-xl flex-col items-start gap-2 p-6">
                <Path className="size-10 text-outline" weight="duotone" aria-hidden />
                <p className="font-headline text-lg font-bold text-ink">还没有记录</p>
                <p className="text-sm text-on-variant">
                  去 <Link to="/" className="font-semibold text-brand underline">城市页</Link> 标记坐过的线路，足迹会出现在这里。
                </p>
              </Panel>
            ) : (
              <RiddenCities
                cities={rides.value.cities}
                action={(l) => <RideControls lineCode={l.lineCode} lineName={l.lineName} year={l.riddenYear} onChanged={reload} onError={(t) => setMessage({ kind: "error", text: t })} />}
              />
            )}
          </>
        )}
      </section>
      <ProfileForm onSaved={(t) => setMessage({ kind: "success", text: t })} />
      <p className="rounded-xl bg-surface-low p-4 text-sm text-on-variant" data-testid="account-pointer">
        登录邮箱、账号昵称与头像、密码、登录设备和注销账号，请到
        {ozid.enabled ? (
          <>
            {" "}
            <a href={ozid.home} target="_blank" rel="noopener noreferrer" className="font-semibold text-brand underline" data-testid="ozid-link-home-me">
              OzID 账号设置
            </a>
          </>
        ) : (
          " OzID 账号设置"
        )}
        。
      </p>
    </>
  );
}

function RideControls({ lineCode, lineName, year, onChanged, onError }: { lineCode: string; lineName: string; year: number; onChanged: () => void; onError: (t: string) => void }) {
  const [value, setValue] = useState(year ? String(year) : "");
  const [busy, setBusy] = useState(false);
  useEffect(() => setValue(year ? String(year) : ""), [year]);
  const parsed = parseYear(value);
  const saveYear = async () => {
    if (Number.isNaN(parsed)) return onError("年份需要在 1900 到今年之间，或留空。");
    setBusy(true);
    try {
      await write(() => rider.markLine({ lineCode, riddenYear: parsed }));
      onChanged();
    } catch (e) {
      onError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    setBusy(true);
    try {
      await write(() => rider.unmarkLine({ lineCode }));
      onChanged();
    } catch (e) {
      onError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <span className="flex items-center gap-1.5">
      <Input
        aria-label={`${lineName} 乘坐年份`}
        inputMode="numeric"
        placeholder="年份"
        className="h-8 w-20 rounded-lg bg-surface-lowest"
        value={value}
        aria-invalid={Number.isNaN(parsed) || undefined}
        onChange={(e) => setValue(e.target.value)}
      />
      <Button size="sm" variant="outline" disabled={busy || value === (year ? String(year) : "")} onClick={() => void saveYear()}>保存</Button>
      <Button size="sm" variant="ghost" className="text-on-variant" disabled={busy} aria-label={`取消标记 ${lineName}`} onClick={() => void remove()}>取消标记</Button>
    </span>
  );
}

type ProfileForm = { handle: string; displayName: string; subtitle: string; bio: string; location: string; headerImages: string; twitter: string; instagram: string; weibo: string; facebook: string; github: string; isPublic: boolean };

const toForm = (p?: ProfileInput): ProfileForm => ({
  handle: p?.handle ?? "", displayName: p?.displayName ?? "", subtitle: p?.subtitle ?? "", bio: p?.bio ?? "", location: p?.location ?? "",
  headerImages: (p?.headerImages ?? []).join("\n"), twitter: p?.twitter ?? "", instagram: p?.instagram ?? "", weibo: p?.weibo ?? "",
  facebook: p?.facebook ?? "", github: p?.github ?? "", isPublic: p?.isPublic ?? false,
});

function ProfileForm({ onSaved }: { onSaved: (text: string) => void }) {
  const [loaded] = useAsync(() => rider.getMyProfile({}), []);
  const [form, setForm] = useState<ProfileForm>(toForm());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [problems, setProblems] = useState<{ field: string; reason: string }[]>([]);
  useEffect(() => {
    if (loaded.status === "ready") setForm(toForm(loaded.value.profile));
  }, [loaded]);
  const set = <K extends keyof ProfileForm>(k: K, v: ProfileForm[K]) => setForm((f) => ({ ...f, [k]: v }));
  const err = (k: string) => problems.find((p) => p.field === k || p.field.endsWith(`.${k}`))?.reason;
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    setProblems([]);
    try {
      await write(() =>
        rider.updateMyProfile({
          profile: { ...form, headerImages: splitLines(form.headerImages) },
        }),
      );
      onSaved("资料已保存。");
    } catch (x) {
      setProblems(fieldProblems(x));
      setError(describeError(x));
    } finally {
      setBusy(false);
    }
  };
  if (loaded.status === "loading") return <Spinner />;
  if (loaded.status === "error") return <Alert className="mb-8">{describeError(loaded.error)}</Alert>;
  return (
    <section aria-labelledby="profile" className="mb-8">
      <h2 id="profile" className="mb-1 font-headline text-2xl font-bold text-ink">骑行者资料与公开主页</h2>
      <p className="mb-4 max-w-2xl text-sm text-on-variant" data-testid="rider-profile-note">
        这里的显示名称、简介和链接只用于你的公开主页，与账号昵称和头像互不影响；账号昵称与头像在 OzID 账号设置里修改。
      </p>
      <form onSubmit={(e) => void save(e)} className="grid max-w-xl gap-4 rounded-xl bg-surface-lowest p-5 shadow-card">
        <label className="flex items-center gap-2.5 rounded-lg bg-surface-low px-3 py-2.5 text-sm font-semibold">
          <input type="checkbox" checked={form.isPublic} onChange={(e) => set("isPublic", e.target.checked)} className="size-4 accent-[#2c694e]" />
          公开我的主页（需要先设置 handle）
        </label>
        <TextField label="Handle" hint="3 到 32 位小写字母、数字或下划线；公开地址是 /u/handle" value={form.handle} error={err("handle")} onChange={(e) => set("handle", e.target.value)} autoCapitalize="none" />
        <TextField label="主页显示名称" value={form.displayName} error={err("display_name") ?? err("displayName")} onChange={(e) => set("displayName", e.target.value)} />
        <TextField label="一句话介绍" value={form.subtitle} onChange={(e) => set("subtitle", e.target.value)} />
        <Field label="简介">{(a) => <Textarea {...a} value={form.bio} onChange={(e) => set("bio", e.target.value)} />}</Field>
        <TextField label="所在地" value={form.location} onChange={(e) => set("location", e.target.value)} />
        <Field label="头图（每行一个 https 地址，最多 8 个）" error={err("header_images") ?? err("headerImages")}>
          {(a) => <Textarea {...a} value={form.headerImages} onChange={(e) => set("headerImages", e.target.value)} />}
        </Field>
        {(["twitter", "instagram", "weibo", "facebook", "github"] as const).map((k) => (
          <TextField key={k} label={`${k[0].toUpperCase()}${k.slice(1)}（用户名或 https 链接）`} value={form[k]} error={err(k)} onChange={(e) => set(k, e.target.value)} />
        ))}
        {error ? <Alert>{error}</Alert> : null}
        <div><Button type="submit" disabled={busy}>保存资料</Button></div>
      </form>
    </section>
  );
}
