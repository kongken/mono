import { useCallback, useEffect, useRef, useState } from "react";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { AccessToken } from "@kongken/api-client/bff/v1/access_token_pb";
import { Button } from "@kongken/ui/button";
import { Input } from "@kongken/ui/input";
import { createEpoch } from "@kongken/account/latest";
import { Field, Notice } from "@kongken/account/ui";
import { Problem } from "@/components/Problem";
import { Spinner } from "@/components/Layout";
import { Badge, Select, TableFrame } from "@/components/metro/ui";
import { saveDraft, takeDraft } from "@/lib/draft.ts";
import { describeMetroError, isSessionLevel } from "@/lib/metro-errors";
import { mcpEndpoint, tokens, write } from "@/lib/rpc";
import { useSession } from "@/lib/session";

const dateFmt = new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium" });

function status(t: AccessToken): { label: string; tone: "ok" | "bad" | "muted" } {
  if (t.revokedAt) return { label: "已吊销", tone: "bad" };
  if (t.expiresAt && timestampDate(t.expiresAt).getTime() < Date.now()) return { label: "已过期", tone: "muted" };
  return { label: "有效", tone: "ok" };
}

type View = { kind: "loading" } | { kind: "ready"; tokens: AccessToken[] } | { kind: "failed"; error: unknown };

/**
 * Management tokens for MCP clients. The secret is shown once, right after creation, in this page's
 * memory only: never stored, never in the address, gone on reload or when the page is left.
 */
export default function TokensPage() {
  const { session } = useSession();
  const userId = session.status === "signed-in" ? session.profile.user.id : "";
  const [view, setView] = useState<View>({ kind: "loading" });
  const [name, setName] = useState("");
  const [scope, setScope] = useState("metro:read");
  const [days, setDays] = useState("30");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(undefined);
  const [secret, setSecret] = useState<string | undefined>();
  const [copied, setCopied] = useState(false);
  const [restored, setRestored] = useState(false);
  const [reads] = useState(createEpoch);
  const endpoint = mcpEndpoint();

  const load = useCallback(() => {
    const current = reads.begin();
    tokens.listAccessTokens({ allUsers: true }).then(
      (res) => current() && setView({ kind: "ready", tokens: res.tokens }),
      (error: unknown) => current() && setView({ kind: "failed", error }),
    );
  }, [reads]);

  useEffect(() => {
    load();
    return () => reads.invalidate();
  }, [load, reads]);

  // The form comes back after a password confirmation as it was typed; creating the token is a click.
  const looked = useRef(false);
  useEffect(() => {
    if (looked.current || userId === "") return;
    looked.current = true;
    const draft = takeDraft(window.sessionStorage, userId, "metro:tokens");
    const d = draft?.data as { name?: unknown; scope?: unknown; days?: unknown } | undefined;
    if (!d) return;
    if (typeof d.name === "string") setName(d.name);
    if (d.scope === "metro:read" || d.scope === "metro:write") setScope(d.scope);
    if (d.days === "7" || d.days === "30" || d.days === "90") setDays(d.days);
    setRestored(true);
  }, [userId]);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    setCopied(false);
    setRestored(false);
    try {
      const res = await write(() => tokens.createAccessToken({ name, audience: "metro", scopes: [scope], expiresInDays: Number(days) }));
      setSecret(res.secret);
      setName("");
      load();
    } catch (x) {
      setError(x);
    } finally {
      setBusy(false);
    }
  };
  const revoke = async (id: string) => {
    setError(undefined);
    try {
      await write(() => tokens.revokeAccessToken({ id }));
      load();
    } catch (x) {
      setError(x);
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
    <section aria-label="管理令牌" className="grid gap-4">
      <p className="max-w-2xl text-sm text-muted-foreground">管理令牌让 MCP 客户端以你的身份读取或修改目录。令牌的权限等同于你创建它时的权限，请像密码一样保管。</p>

      <form onSubmit={(e) => void create(e)} className="grid max-w-xl gap-3 rounded-lg border bg-card p-4">
        <h2 className="font-medium">创建令牌</h2>
        {restored ? <Notice kind="info">已恢复你在验证密码前填写的内容。令牌还没有创建，请核对后点击“创建”。</Notice> : null}
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
        <div>
          <Button type="submit" disabled={busy || name.trim() === ""}>创建</Button>
        </div>
      </form>

      {secret ? (
        <div className="grid max-w-2xl gap-2 rounded-lg border border-primary/40 bg-secondary p-4" role="status">
          <p className="text-sm font-medium">请现在复制令牌：它只显示这一次，刷新或离开后无法再看到。</p>
          <code className="break-all rounded bg-background px-2 py-1 text-xs" data-testid="token-secret">{secret}</code>
          <div className="flex gap-2">
            <Button type="button" size="sm" onClick={() => void copy()}>{copied ? "已复制" : "复制"}</Button>
            <Button type="button" size="sm" variant="outline" onClick={() => setSecret(undefined)}>我已保存</Button>
          </div>
        </div>
      ) : null}

      {error !== undefined ? (
        isSessionLevel(error) ? (
          <Problem error={error} keepBeforeReauth={() => saveDraft(window.sessionStorage, { userId, scope: "metro:tokens", data: { name, scope, days } })} />
        ) : (
          <Notice kind="error">{describeMetroError(error)}</Notice>
        )
      ) : null}
      {view.kind === "loading" ? <Spinner /> : null}
      {view.kind === "failed" ? <Problem error={view.error} retry={load} /> : null}
      {view.kind === "ready" ? (
        <TableFrame>
          <table className="w-full min-w-[32rem] text-left text-sm">
            <caption className="sr-only">管理令牌</caption>
            <thead className="border-b bg-muted text-xs text-muted-foreground">
              <tr>
                {["名称", "权限", "过期", "最近使用", "状态", ""].map((h, i) => (
                  <th key={i} scope="col" className="px-3 py-2 font-medium">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {view.tokens.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-3 py-6 text-center text-muted-foreground">还没有令牌。</td>
                </tr>
              ) : null}
              {view.tokens.map((t) => {
                const s = status(t);
                return (
                  <tr key={t.id} className="border-b last:border-0">
                    <td className="px-3 py-2">{t.name}</td>
                    <td className="px-3 py-2">{t.scopes.includes("metro:write") ? "读写" : "只读"}</td>
                    <td className="whitespace-nowrap px-3 py-2">{t.expiresAt ? dateFmt.format(timestampDate(t.expiresAt)) : ""}</td>
                    <td className="whitespace-nowrap px-3 py-2">{t.lastUsedAt ? dateFmt.format(timestampDate(t.lastUsedAt)) : "从未"}</td>
                    <td className="px-3 py-2"><Badge tone={s.tone}>{s.label}</Badge></td>
                    <td className="px-3 py-2 text-right">
                      {s.label === "有效" ? (
                        <Button type="button" size="sm" variant="ghost" className="text-destructive" aria-label={`吊销 ${t.name}`} onClick={() => void revoke(t.id)}>吊销</Button>
                      ) : null}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </TableFrame>
      ) : null}

      <div className="max-w-2xl">
        <h2 className="mb-1 font-medium">在 MCP 客户端中使用</h2>
        <p className="mb-2 text-sm text-muted-foreground">端点：<code className="break-all">{endpoint}</code>（Streamable HTTP，使用 Bearer 令牌）。</p>
        <pre className="overflow-x-auto rounded bg-muted p-3 text-xs">{JSON.stringify({ mcpServers: { metro: { url: endpoint, headers: { Authorization: "Bearer <令牌>" } } } }, null, 2)}</pre>
      </div>
    </section>
  );
}
