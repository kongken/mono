import { useEffect, useMemo, useState } from "react";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { useSearch, Link } from "@/lib/router";
import { identity, external, ensureCsrf } from "@/lib/rpc";
import { PageHeader, Spinner } from "@/components/Layout";
import { readPending, type Pending } from "@/lib/identityPending";

const operationKey = "ozid:identity-operation";

export default function IdentitiesPage() {
  const search = useSearch();
  const flowId = useMemo(() => new URLSearchParams(search).get("flow") ?? "", [search]);
  const [data, setData] = useState<Awaited<ReturnType<typeof identity.getIdentityCapabilities>> | null>(null);
  const [pending, setPending] = useState<Pending | null>(() => readPending(sessionStorage));
  const [password, setPassword] = useState("");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const refresh = () => void identity.getIdentityCapabilities({}).then(setData).catch(() => setError("暂时无法读取登录方式，请稍后重试。"));
  useEffect(refresh, []);
  useEffect(() => { if (flowId && pending?.kind === "identity_link" && !pending.flowId) { const next = { ...pending, flowId }; setPending(next); sessionStorage.setItem(operationKey, JSON.stringify(next)); } }, [flowId, pending]);
  if (error) return <Notice kind="error">{error}</Notice>;
  if (!data) return <Spinner />;

  const hasPassword = data.hasPassword;

  async function beginLink(provider: string) {
    setBusy(true); setError(undefined);
    try {
      await ensureCsrf();
      const mutationId = crypto.randomUUID();
      const draft: Pending = { kind: "identity_link", provider, challengeId: "", mutationId };
      setPending(draft); sessionStorage.setItem(operationKey, JSON.stringify(draft));
      const op = await identity.beginIdentityOperation({ kind: "identity_link", provider, currentPassword: password });
      const next: Pending = { kind: "identity_link", provider, challengeId: op.challengeId, mutationId };
      sessionStorage.setItem(operationKey, JSON.stringify(next));
      const start = await external.startExternalLogin({ provider, intent: "link", appId: "ozid", returnPath: "/identities" });
      window.location.assign(start.authorizationUrl);
    } catch { sessionStorage.removeItem(operationKey); setPending(null); setError("无法开始绑定，请确认密码后重试。"); setBusy(false); }
  }
  async function beginUnlink(identityId: string) {
    setBusy(true); setError(undefined);
    try { await ensureCsrf(); const mutationId = crypto.randomUUID(); const draft: Pending = { kind: "identity_unlink", identityId, challengeId: "", mutationId }; setPending(draft); sessionStorage.setItem(operationKey, JSON.stringify(draft)); const op = await identity.beginIdentityOperation({ kind: "identity_unlink", externalIdentityId: identityId, currentPassword: password }); const next: Pending = { kind: "identity_unlink", identityId, challengeId: op.challengeId, mutationId }; setPending(next); sessionStorage.setItem(operationKey, JSON.stringify(next)); }
    catch { sessionStorage.removeItem(operationKey); setPending(null); setError("无法开始解绑，请确认密码后重试。"); } finally { setBusy(false); }
  }
  async function confirm() {
    if (!pending || (!hasPassword && !token)) return;
    setBusy(true); setError(undefined);
    try {
      await ensureCsrf(); if (!hasPassword) await identity.confirmIdentityOperation({ challengeId: pending.challengeId, token });
      if (pending.kind === "identity_link" && pending.flowId) await identity.linkExternalIdentity({ operationChallengeId: pending.challengeId, providerFlowId: pending.flowId, mutationId: pending.mutationId });
      if (pending.kind === "identity_unlink" && pending.identityId) await identity.unlinkExternalIdentity({ operationChallengeId: pending.challengeId, externalIdentityId: pending.identityId, mutationId: pending.mutationId });
      sessionStorage.removeItem(operationKey); setPending(null); setToken(""); setPassword(""); refresh();
    } catch { setError("确认失败，验证码可能已过期，请重新开始操作。"); } finally { setBusy(false); }
  }
  return <div className="grid max-w-2xl gap-8" data-testid="identities-page">
    <PageHeader eyebrow="OzID 统一身份管理" title="登录方式">绑定或解绑第三方登录。每次敏感操作都需要近期密码认证或邮件验证码，最后一个登录方式不能被移除。</PageHeader>
    <section className="grid gap-4 rounded-xl bg-card p-6 shadow-sm sm:p-8">
      <label className="grid gap-1 text-sm">当前密码（已设置密码的账号）<input className="rounded border p-2" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" /></label>
      {!data.hasPassword ? <Notice kind="info">账号还没有密码。可先通过 <Link className="underline" to="/forgot-password">忘记密码流程设置首次密码</Link>，设置后再回来管理登录方式。</Notice> : null}
      <div className="flex gap-2"><Button disabled={busy} onClick={() => void beginLink("google")}>绑定 Google</Button><Button disabled={busy} onClick={() => void beginLink("github")}>绑定 GitHub</Button></div>
      {data.identities.length === 0 ? <div>暂无第三方登录方式。</div> : data.identities.map((item) => <div className="flex items-center justify-between rounded-lg border p-3" key={item.id}><span>{item.displayLabel || item.provider}</span><Button variant="outline" disabled={busy} onClick={() => void beginUnlink(item.id)}>解绑</Button></div>)}
    </section>
    {pending ? <section className="grid gap-3 rounded-xl bg-card p-6 shadow-sm sm:p-8">{data.hasPassword ? null : <><Notice kind="info">验证码已发送到你的邮箱。完成第三方登录后，在此输入验证码。</Notice><input className="rounded border p-2" value={token} onChange={(e) => setToken(e.target.value)} placeholder="邮箱验证码" inputMode="numeric" /></>}<Button disabled={busy || (!data.hasPassword && !token) || (pending.kind === "identity_link" && !pending.flowId)} onClick={() => void confirm()}>确认操作</Button></section> : null}
    {error ? <Notice kind="error">{error}</Notice> : null}
  </div>;
}
