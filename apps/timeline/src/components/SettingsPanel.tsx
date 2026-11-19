import { useEffect, useState } from "react";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { AccessToken } from "@kongken/api-client/bff/v1/access_token_pb";
import { accessTokens, write } from "@/lib/rpc.ts";

export function SettingsPanel() {
  const [tokens, setTokens] = useState<AccessToken[]>([]);
  const [name, setName] = useState("");
  const [days, setDays] = useState("90");
  const [scope, setScope] = useState("timeline:read");
  const [secret, setSecret] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = async () => {
    try { setTokens((await accessTokens.listAccessTokens({ allUsers: false })).tokens.filter((token) => token.audience === "timeline")); }
    catch (e) { setError(e instanceof Error ? e.message : "读取令牌失败"); }
  };
  useEffect(() => { void load(); }, []);
  const create = async () => {
    setError(null); setSecret(null);
    try {
      const response = await write(() => accessTokens.createAccessToken({ name: name.trim(), audience: "timeline", scopes: [scope], expiresInDays: Number(days) }));
      setSecret(response.secret); setName(""); await load();
    } catch (e) { setError(e instanceof Error ? e.message : "创建令牌失败"); }
  };
  const revoke = async (id: string) => {
    try { await write(() => accessTokens.revokeAccessToken({ id })); await load(); }
    catch (e) { setError(e instanceof Error ? e.message : "撤销令牌失败"); }
  };
  return <section className="mt-8 rounded-card border border-outline-variant bg-card p-5" aria-label="Timeline PAT 设置">
    <h2 className="text-base font-semibold text-card-foreground">Timeline PAT</h2>
    <p className="mt-1 text-xs text-muted-foreground">令牌只在创建完成时显示一次；Cookie 不能代替 PAT。</p>
    <div className="mt-4 grid gap-2 sm:grid-cols-[1fr_110px_150px_auto]">
      <input value={name} onChange={(e) => setName(e.target.value)} placeholder="令牌名称" className="h-9 rounded-md border border-outline-variant px-2 text-sm" />
      <input value={days} onChange={(e) => setDays(e.target.value)} type="number" min="1" max="90" className="h-9 rounded-md border border-outline-variant px-2 text-sm" aria-label="有效天数" />
      <select value={scope} onChange={(e) => setScope(e.target.value)} className="h-9 rounded-md border border-outline-variant px-2 text-sm"><option value="timeline:read">只读</option><option value="timeline:write">读写</option></select>
      <button type="button" onClick={() => void create()} className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-on-primary">创建</button>
    </div>
    {secret !== null && <div className="mt-3 rounded-md bg-primary-container/30 p-3 text-xs"><p className="font-medium">请立即复制，关闭后不会再次显示：</p><code className="mt-1 block break-all select-all">{secret}</code></div>}
    {error !== null && <p className="mt-3 text-xs text-destructive">{error}</p>}
    <ul className="mt-4 divide-y divide-outline-variant/60">{tokens.map((token) => <li key={token.id} className="flex items-center justify-between gap-3 py-3 text-sm"><span><b>{token.name}</b><span className="ml-2 text-xs text-muted-foreground">{token.scopes.join(", ")} · {token.expiresAt ? timestampDate(token.expiresAt).toLocaleDateString() : ""}</span></span><button type="button" onClick={() => void revoke(token.id)} className="text-xs text-destructive hover:underline">撤销</button></li>)}</ul>
  </section>;
}
