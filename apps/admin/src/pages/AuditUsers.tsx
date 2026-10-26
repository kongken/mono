import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "@kongken/ui/button";
import { Input } from "@kongken/ui/input";
import { createEpoch } from "@kongken/account/latest";
import { Field, Notice } from "@kongken/account/ui";
import { Problem } from "@/components/Problem";
import { Spinner } from "@/components/Layout";
import { Link, navigate, useSearch } from "@/lib/router";
import { actionLabel, CHANNEL_LABEL, listAudit, REASON_HIDDEN, RESULT_FILTERS, resultLabel, STATUS_NAME, type AuditOriginRow, type AuditPage, type AuditRow } from "@/lib/audit";
import { auditFilterProblem, EMPTY_AUDIT_FILTERS, userOfSearch, type AuditFilters } from "@/lib/routes.ts";
import { ROLE_LABEL } from "@/lib/users";

const SELECT = "h-9 w-full rounded-md border bg-background px-3 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50";

type View = { kind: "loading" } | { kind: "ready"; page: AuditPage } | { kind: "failed"; error: unknown };

const KNOWN_ACTIONS = ["admin.account.disable", "admin.account.enable", "admin.role.grant:metro-admin", "admin.role.revoke:metro-admin", "admin.users.list", "admin.users.get", "admin.audit.list", "role.grant:metro-admin", "role.revoke:metro-admin"];

/**
 * The account security and administration audit: what usersrv recorded about accounts (changes, refusals,
 * unchanged results, management reads, command-line operations). It is not a unified audit of the platform:
 * the metro catalogue and the management-token call log keep their own. The filters live in this component
 * (an account ID may arrive in the address from the detail page; nothing else is kept in the address or
 * storage), and only the newest request may write its answer.
 */
export default function AuditUsersPage() {
  const [draft, setDraft] = useState<AuditFilters>(() => ({ ...EMPTY_AUDIT_FILTERS, userId: userOfSearch(window.location.search) }));
  const [applied, setApplied] = useState<AuditFilters>(draft);
  const [problem, setProblem] = useState("");
  const [tokens, setTokens] = useState<string[]>([""]);
  const [view, setView] = useState<View>({ kind: "loading" });
  const [reads] = useState(createEpoch);
  const aborter = useRef<AbortController | null>(null);

  // Following a link to this page again (from the menu or from an account) starts from what the link says.
  const search = useSearch();
  const seen = useRef(search);
  useEffect(() => {
    if (seen.current === search) return;
    seen.current = search;
    const fresh = { ...EMPTY_AUDIT_FILTERS, userId: userOfSearch(search) };
    setDraft(fresh);
    setApplied(fresh);
    setProblem("");
    setTokens([""]);
  }, [search]);

  const token = tokens[tokens.length - 1]!;
  const load = useCallback(() => {
    const current = reads.begin();
    aborter.current?.abort();
    const abort = (aborter.current = new AbortController());
    setView({ kind: "loading" });
    listAudit(applied, token, abort.signal).then(
      (page) => current() && setView({ kind: "ready", page }),
      (error: unknown) => current() && setView({ kind: "failed", error }),
    );
  }, [applied, token, reads]);

  useEffect(() => {
    load();
    return () => {
      reads.invalidate();
      aborter.current?.abort();
    };
  }, [load, reads]);

  const set = (k: keyof AuditFilters) => (v: string) => setDraft((d) => ({ ...d, [k]: v }));

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const bad = auditFilterProblem(draft);
    setProblem(bad);
    if (bad) return;
    setTokens([""]);
    // A new object even when nothing changed: asking again is how an administrator refreshes the list.
    setApplied({ ...draft });
  };

  const clear = () => {
    setDraft(EMPTY_AUDIT_FILTERS);
    setApplied(EMPTY_AUDIT_FILTERS);
    setProblem("");
    setTokens([""]);
    if (window.location.search) navigate("/audit/users", true);
  };

  return (
    <div className="grid gap-6">
      <div className="grid gap-1">
        <h1 className="text-xl font-semibold">账号审计</h1>
        <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
          这里是账号服务记录的账号安全与管理操作：谁、在哪个渠道、对哪个账号、做了什么、结果如何，包括被拒绝和没有变化的尝试。地铁目录的操作记录在地铁管理里，管理令牌的调用日志仍在原处；这不是全站统一的审计。
        </p>
      </div>
      <form onSubmit={submit} className="grid gap-3 rounded-lg border bg-card p-4 sm:grid-cols-2 lg:grid-cols-4" aria-label="查询审计记录" data-testid="audit-filters">
        <Field label="被操作账号 ID" hint="审计记录所属的账号">
          {(a) => <Input {...a} value={draft.userId} onChange={(e) => set("userId")(e.target.value)} autoComplete="off" spellCheck={false} maxLength={36} />}
        </Field>
        <Field label="操作者账号 ID" hint="网页操作者">
          {(a) => <Input {...a} value={draft.operatorId} onChange={(e) => set("operatorId")(e.target.value)} autoComplete="off" spellCheck={false} maxLength={36} />}
        </Field>
        <Field label="渠道">
          {(a) => (
            <select {...a} className={SELECT} value={draft.channel} onChange={(e) => set("channel")(e.target.value)}>
              <option value="">全部</option>
              <option value="web">网页控制台</option>
              <option value="cli">命令行（运维）</option>
              <option value="self">账号本人</option>
              <option value="system">系统</option>
            </select>
          )}
        </Field>
        <Field label="结果">
          {(a) => (
            <select {...a} className={SELECT} value={draft.result} onChange={(e) => set("result")(e.target.value)}>
              <option value="">全部</option>
              {RESULT_FILTERS.map((r) => (
                <option key={r} value={r}>{resultLabel(r)}</option>
              ))}
            </select>
          )}
        </Field>
        <Field label="操作类型" hint="例如 admin.account.disable">
          {(a) => (
            <>
              <Input {...a} list="audit-actions" value={draft.action} onChange={(e) => set("action")(e.target.value)} autoComplete="off" spellCheck={false} maxLength={64} />
              <datalist id="audit-actions">
                {KNOWN_ACTIONS.map((x) => (
                  <option key={x} value={x}>{actionLabel(x)}</option>
                ))}
              </datalist>
            </>
          )}
        </Field>
        <Field label="操作编号" hint="同一编号过期后可能被复用，不一定是同一次操作">
          {(a) => <Input {...a} value={draft.mutationId} onChange={(e) => set("mutationId")(e.target.value)} autoComplete="off" spellCheck={false} maxLength={64} />}
        </Field>
        <Field label="开始时间（含）" hint="按你浏览器所在时区；列表里的时间也是本地时间">
          {(a) => <Input {...a} type="datetime-local" value={draft.from} onChange={(e) => set("from")(e.target.value)} />}
        </Field>
        <Field label="结束时间（不含）" hint="按你浏览器所在时区">
          {(a) => <Input {...a} type="datetime-local" value={draft.to} onChange={(e) => set("to")(e.target.value)} />}
        </Field>
        {problem ? (
          <div className="sm:col-span-2 lg:col-span-4" data-testid="audit-filter-problem">
            <Notice kind="error">{problem}</Notice>
          </div>
        ) : null}
        <div className="flex gap-2 sm:col-span-2 lg:col-span-4">
          <Button type="submit">查询</Button>
          <Button type="button" variant="outline" onClick={clear}>
            清除条件
          </Button>
        </div>
      </form>

      {applied.userId ? (
        <p className="text-sm" data-testid="audit-scope">
          当前只看账号 <code className="break-all">{applied.userId}</code> 的记录。{" "}
          <Link to={`/users/${applied.userId}`} className="rounded-sm text-primary underline-offset-4 hover:underline">查看账号详情</Link>
        </p>
      ) : null}

      {view.kind === "loading" ? <Spinner label="正在查询" /> : null}
      {view.kind === "failed" ? <Problem error={view.error} retry={load} /> : null}
      {view.kind === "ready" ? (
        <>
          {view.page.rows.length === 0 ? (
            <p className="rounded-lg border bg-card p-6 text-sm text-muted-foreground" data-testid="no-audit">没有符合条件的记录。</p>
          ) : (
            <ol className="grid gap-3" data-testid="audit-list" aria-label="审计记录">
              {view.page.rows.map((r) => (
                <AuditItem key={r.id} row={r} />
              ))}
            </ol>
          )}
          <nav aria-label="分页" className="flex items-center gap-3 text-sm">
            <Button variant="outline" size="sm" disabled={tokens.length <= 1} onClick={() => setTokens((t) => t.slice(0, -1))}>
              上一页
            </Button>
            <span className="text-muted-foreground" data-testid="page-number">第 {tokens.length} 页</span>
            <Button variant="outline" size="sm" disabled={!view.page.next} onClick={() => setTokens((t) => [...t, view.page.next])}>
              下一页
            </Button>
          </nav>
        </>
      ) : null}
    </div>
  );
}

const tone = (result: string) =>
  result === "ok" ? "bg-emerald-100 text-emerald-900 dark:bg-emerald-950 dark:text-emerald-200" : result.startsWith("refused") ? "bg-red-100 text-red-900 dark:bg-red-950 dark:text-red-200" : "bg-muted text-foreground";

const roles = (r: string[]) => r.map((x) => ROLE_LABEL[x] ?? x).join("、") || "无";
const status = (s: string) => STATUS_NAME[s] ?? s;

/** "正常 → 已禁用", only for what the row really carries; a refusal or a replay has no after. */
function change(before: { beforeStatus: string; afterStatus: string; beforeRoles: string[]; afterRoles: string[] }): string[] {
  const out: string[] = [];
  if (before.beforeStatus || before.afterStatus) out.push(`状态：${status(before.beforeStatus) || "—"} → ${status(before.afterStatus) || "—"}`);
  if (before.beforeRoles.length || before.afterRoles.length) out.push(`角色：${roles(before.beforeRoles)} → ${roles(before.afterRoles)}`);
  return out;
}

function Operator({ row }: { row: AuditRow }) {
  if (row.channel === "cli") return <span data-testid="audit-operator">命令行 · 运维标识 <code>{row.operatorLabel}</code>（不是网页账号）</span>;
  if (row.channel === "system") return <span data-testid="audit-operator">系统</span>;
  if (!row.operatorId) return <span data-testid="audit-operator">—</span>;
  return (
    <span data-testid="audit-operator">
      {row.channel === "self" ? "账号本人 " : "账号 "}
      <Link to={`/users/${row.operatorId}`} className="break-all rounded-sm text-primary underline-offset-4 hover:underline">{row.operatorId}</Link>
    </span>
  );
}

function Origin({ row }: { row: AuditRow }) {
  if (row.result !== "replayed") return null;
  const o: AuditOriginRow | null = row.origin;
  return (
    <div className="rounded-md bg-muted p-3 text-xs leading-relaxed" data-testid="audit-origin">
      <p className="font-medium">这是对一次已完成操作的重复提交，没有再执行。</p>
      {o ? (
        <>
          <p>
            当时的记录：{o.createdAt ? o.createdAt.toLocaleString("zh-CN") : "—"} · {actionLabel(o.action)} · {resultLabel(o.result)}
          </p>
          {change(o).map((c) => (
            <p key={c}>{c}</p>
          ))}
        </>
      ) : row.originId ? (
        <p>当时的记录已超出保留范围。</p>
      ) : (
        <p>没有关联到原始记录（这条收据早于关联功能）。</p>
      )}
    </div>
  );
}

function AuditItem({ row }: { row: AuditRow }) {
  const facts: [string, React.ReactNode, string][] = [
    ["渠道", CHANNEL_LABEL[row.channel], "audit-channel"],
    ["操作者", <Operator key="op" row={row} />, "audit-operator-cell"],
    [
      "账号",
      row.userId ? (
        <Link key="user" to={`/users/${row.userId}`} className="break-all rounded-sm text-primary underline-offset-4 hover:underline" data-testid="audit-user">{row.userId}</Link>
      ) : (
        "—"
      ),
      "audit-user-cell",
    ],
  ];
  const changes = change(row);
  return (
    <li className="grid gap-2 rounded-lg border bg-card p-4 text-sm" data-testid="audit-row">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium" data-testid="audit-action">{actionLabel(row.action)}</span>
        <code className="text-xs text-muted-foreground">{row.action}</code>
        <span className={`rounded-full px-2 py-0.5 text-xs ${tone(row.result)}`} data-testid="audit-result">{resultLabel(row.result)}</span>
        <time className="ml-auto text-xs text-muted-foreground" dateTime={row.createdAt?.toISOString()} data-testid="audit-time">
          {row.createdAt ? row.createdAt.toLocaleString("zh-CN") : "—"}
        </time>
      </div>
      <dl className="grid gap-x-4 gap-y-1 sm:grid-cols-[6rem_1fr]">
        {facts.map(([k, v, id]) => (
          <div key={k} className="contents">
            <dt className="text-muted-foreground">{k}</dt>
            <dd data-testid={id}>{v}</dd>
          </div>
        ))}
        {row.reason ? (
          <div className="contents">
            <dt className="text-muted-foreground">原因</dt>
            <dd className="break-words" data-testid="audit-reason">
              {row.reason === REASON_HIDDEN ? <span className="text-muted-foreground">（原因已隐藏：早期记录的自由文本未经检查，不在控制台显示）</span> : row.reason}
            </dd>
          </div>
        ) : null}
        {changes.length ? (
          <div className="contents">
            <dt className="text-muted-foreground">变化</dt>
            <dd data-testid="audit-change">{changes.join("；")}</dd>
          </div>
        ) : null}
        {row.mutationId ? (
          <div className="contents">
            <dt className="text-muted-foreground">操作编号</dt>
            <dd className="break-all"><code data-testid="audit-mutation">{row.mutationId}</code></dd>
          </div>
        ) : null}
        {row.requestId ? (
          <div className="contents">
            <dt className="text-muted-foreground">请求 ID</dt>
            <dd className="break-all"><code>{row.requestId}</code></dd>
          </div>
        ) : null}
      </dl>
      <Origin row={row} />
    </li>
  );
}
