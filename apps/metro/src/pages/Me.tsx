import { useEffect, useState } from "react";
import type { ProfileInput } from "@kongken/api-client/bff/metro/v1/rider_pb";
import { Button } from "@kongken/ui/button";
import { Input } from "@kongken/ui/input";
import { rider, write } from "@/lib/rpc";
import { describeError, fieldProblems } from "@/lib/errors";
import { formatKm, lines as splitLines, parseYear } from "@/lib/format";
import { useAsync } from "@/lib/useAsync";
import { useSession } from "@/lib/session";
import { Link } from "@/lib/router";
import { Alert, Field, PageTitle, Spinner, Stat, Textarea, TextField } from "@/components/extras";
import { RiddenCities } from "@/components/RiddenCities";

export default function MePage() {
  const { session, signIn } = useSession();
  if (session.status === "loading") return <Spinner />;
  if (session.status === "anonymous") {
    return (
      <>
        <PageTitle>我的足迹</PageTitle>
        <p className="mb-3 text-sm text-muted-foreground">登录后可以记录坐过的线路，并设置公开主页。</p>
        <Button onClick={signIn}>登录</Button>
      </>
    );
  }
  return <SignedInMe />;
}

function SignedInMe() {
  const [rides, reload] = useAsync(() => rider.listMyLines({}), []);
  const [message, setMessage] = useState<{ kind: "error" | "success"; text: string } | undefined>();
  return (
    <>
      <PageTitle>我的足迹</PageTitle>
      {message ? <Alert kind={message.kind} className="mb-4">{message.text}</Alert> : null}
      <section aria-labelledby="rides" className="mb-10">
        <h2 id="rides" className="mb-3 text-lg font-semibold">乘坐记录</h2>
        {rides.status === "loading" && <Spinner />}
        {rides.status === "error" && <Alert>{describeError(rides.error)}</Alert>}
        {rides.status === "ready" && (
          <>
            <div className="mb-4 grid grid-cols-3 gap-3 sm:max-w-lg">
              <Stat label="城市" value={rides.value.stats?.cityCount ?? 0} />
              <Stat label="线路" value={rides.value.stats?.lineCount ?? 0} />
              <Stat label="里程" value={formatKm(rides.value.stats?.totalLengthKm ?? 0)} />
            </div>
            {rides.value.cities.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                还没有记录。去 <Link to="/" className="text-primary underline">城市页</Link> 标记坐过的线路。
              </p>
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
      <p className="border-t pt-4 text-sm text-muted-foreground">
        登录邮箱、昵称、密码、登录设备和注销账号，请到 <Link to="/account" className="text-primary underline">账号设置</Link>。
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
    <span className="flex items-center gap-2">
      <Input
        aria-label={`${lineName} 乘坐年份`}
        inputMode="numeric"
        placeholder="年份"
        className="h-8 w-20"
        value={value}
        aria-invalid={Number.isNaN(parsed) || undefined}
        onChange={(e) => setValue(e.target.value)}
      />
      <Button size="sm" variant="outline" disabled={busy || value === (year ? String(year) : "")} onClick={() => void saveYear()}>保存</Button>
      <Button size="sm" variant="ghost" disabled={busy} aria-label={`取消标记 ${lineName}`} onClick={() => void remove()}>取消标记</Button>
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
  if (loaded.status === "error") return <Alert>{describeError(loaded.error)}</Alert>;
  return (
    <section aria-labelledby="profile" className="mb-10">
      <h2 id="profile" className="mb-3 text-lg font-semibold">资料与公开主页</h2>
      <form onSubmit={(e) => void save(e)} className="grid max-w-xl gap-4">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={form.isPublic} onChange={(e) => set("isPublic", e.target.checked)} className="size-4" />
          公开我的主页（需要先设置 handle）
        </label>
        <TextField label="Handle" hint="3 到 32 位小写字母、数字或下划线；公开地址是 /u/handle" value={form.handle} error={err("handle")} onChange={(e) => set("handle", e.target.value)} autoCapitalize="none" />
        <TextField label="显示名称" value={form.displayName} error={err("display_name") ?? err("displayName")} onChange={(e) => set("displayName", e.target.value)} />
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
