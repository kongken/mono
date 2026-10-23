import { useState } from "react";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { AccessToken } from "@kongken/api-client/bff/v1/access_token_pb";
import { Button } from "@kongken/ui/button";
import { Input } from "@kongken/ui/input";
import { tokens, write, mcpEndpoint } from "@/lib/rpc";
import { describeError } from "@/lib/errors";
import { useAsync } from "@/lib/useAsync";
import { Alert, Badge, Field, Select, Spinner } from "@/components/extras";

const dateFmt = new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium" });

function status(t: AccessToken): { label: string; tone: "ok" | "bad" | "muted" } {
  if (t.revokedAt) return { label: "已吊销", tone: "bad" };
  if (t.expiresAt && timestampDate(t.expiresAt).getTime() < Date.now()) return { label: "已过期", tone: "muted" };
  return { label: "有效", tone: "ok" };
}

/** Management tokens for MCP clients: the secret is shown once, right after creation. */
export default function TokensPage() {
  const [list, reload] = useAsync(() => tokens.listAccessTokens({ allUsers: true }), []);
  const [name, setName] = useState("");
  const [scope, setScope] = useState("metro:read");
  const [days, setDays] = useState("30");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [secret, setSecret] = useState<string | undefined>();
  const [copied, setCopied] = useState(false);
  const endpoint = mcpEndpoint();

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    setCopied(false);
    try {
      const res = await write(() => tokens.createAccessToken({ name, audience: "metro", scopes: [scope], expiresInDays: Number(days) }));
      setSecret(res.secret);
      setName("");
      reload();
    } catch (x) {
      setError(describeError(x));
    } finally {
      setBusy(false);
    }
  };
  const revoke = async (id: string) => {
    setError(undefined);
    try {
      await write(() => tokens.revokeAccessToken({ id }));
      reload();
    } catch (x) {
      setError(describeError(x));
    }
  };
  const copy = async () => {
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  return (
    <section aria-label="管理令牌">
      <p className="mb-3 max-w-2xl text-sm text-muted-foreground">管理令牌让 MCP 客户端以你的身份读取或修改目录。令牌的权限等同于你创建它时的权限，请像密码一样保管。</p>

      <form onSubmit={(e) => void create(e)} className="mb-5 grid max-w-xl gap-3 rounded-lg border bg-card p-4">
        <h2 className="font-medium">创建令牌</h2>
        <Field label="名称">{(a) => <Input {...a} value={name} maxLength={64} required onChange={(e) => setName(e.target.value)} />}</Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="权限">
            {(a) => (
              <Select {...a} value={scope} onChange={(e) => setScope(e.target.value)}>
                <option value="metro:read">只读</option>
                <option value="metro:write">读写</option>
              </Select>
            )}
          </Field>
          <Field label="有效期">
            {(a) => (
              <Select {...a} value={days} onChange={(e) => setDays(e.target.value)}>
                <option value="7">7 天</option>
                <option value="30">30 天</option>
                <option value="90">90 天</option>
              </Select>
            )}
          </Field>
        </div>
        <div><Button type="submit" disabled={busy || name.trim() === ""}>创建</Button></div>
      </form>

      {secret ? (
        <div className="mb-5 grid max-w-2xl gap-2 rounded-lg border border-amber-500/60 bg-amber-50 p-4" role="status">
          <p className="text-sm font-medium text-amber-900">请现在复制令牌：它只显示这一次，刷新或离开后无法再看到。</p>
          <code className="break-all rounded bg-white px-2 py-1 text-xs" data-testid="token-secret">{secret}</code>
          <div className="flex gap-2">
            <Button size="sm" onClick={() => void copy()}>{copied ? "已复制" : "复制"}</Button>
            <Button size="sm" variant="outline" onClick={() => setSecret(undefined)}>我已保存</Button>
          </div>
        </div>
      ) : null}

      {error ? <Alert className="mb-3">{error}</Alert> : null}
      {list.status === "loading" && <Spinner />}
      {list.status === "error" && <Alert>{describeError(list.error)}</Alert>}
      {list.status === "ready" && (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <table className="w-full text-left text-sm">
            <thead className="bg-muted text-xs text-muted-foreground">
              <tr>{["名称", "权限", "过期", "最近使用", "状态", ""].map((h, i) => <th key={i} scope="col" className="px-3 py-2 font-medium">{h}</th>)}</tr>
            </thead>
            <tbody>
              {list.value.tokens.length === 0 && <tr><td colSpan={6} className="px-3 py-6 text-center text-muted-foreground">还没有令牌。</td></tr>}
              {list.value.tokens.map((t) => {
                const s = status(t);
                return (
                  <tr key={t.id} className="border-t">
                    <td className="px-3 py-2">{t.name}</td>
                    <td className="px-3 py-2">{t.scopes.includes("metro:write") ? "读写" : "只读"}</td>
                    <td className="whitespace-nowrap px-3 py-2">{t.expiresAt ? dateFmt.format(timestampDate(t.expiresAt)) : ""}</td>
                    <td className="whitespace-nowrap px-3 py-2">{t.lastUsedAt ? dateFmt.format(timestampDate(t.lastUsedAt)) : "从未"}</td>
                    <td className="px-3 py-2"><Badge tone={s.tone}>{s.label}</Badge></td>
                    <td className="px-3 py-2 text-right">
                      {s.label === "有效" ? <Button size="sm" variant="ghost" className="text-destructive" aria-label={`吊销 ${t.name}`} onClick={() => void revoke(t.id)}>吊销</Button> : null}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <div className="mt-6 max-w-2xl">
        <h2 className="mb-1 font-medium">在 MCP 客户端中使用</h2>
        <p className="mb-2 text-sm text-muted-foreground">端点：<code className="break-all">{endpoint}</code>（Streamable HTTP，使用 Bearer 令牌）。</p>
        <pre className="overflow-x-auto rounded bg-muted p-3 text-xs">{JSON.stringify({ mcpServers: { metro: { url: endpoint, headers: { Authorization: "Bearer <令牌>" } } } }, null, 2)}</pre>
      </div>
    </section>
  );
}
