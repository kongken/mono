import { useCallback, useEffect, useState } from "react";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { AuditLog } from "@kongken/api-client/bff/metro/v1/admin_pb";
import { Button } from "@kongken/ui/button";
import { Input } from "@kongken/ui/input";
import { createEpoch } from "@kongken/account/latest";
import { Field, TextField } from "@kongken/account/ui";
import { Problem } from "@/components/Problem";
import { Spinner } from "@/components/Layout";
import { Select, TableFrame } from "@/components/metro/ui";
import { metroAdmin } from "@/lib/rpc";

const dateFmt = new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "medium" });

type Filters = { entityType: string; actorUserId: string; channel: string; from: string; to: string };
type View = { kind: "loading" } | { kind: "ready" } | { kind: "failed"; error: unknown };

/** The catalogue's audit log: who changed what, through the web or the MCP endpoint, with before and after. */
export default function AuditPage() {
  const [filters, setFilters] = useState<Filters>({ entityType: "", actorUserId: "", channel: "", from: "", to: "" });
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [next, setNext] = useState("");
  const [view, setView] = useState<View>({ kind: "loading" });
  const [open, setOpen] = useState<string | undefined>();
  const [reads] = useState(createEpoch);

  const load = useCallback(
    async (f: Filters, token = "", append = false) => {
      const current = reads.begin();
      if (!append) setView({ kind: "loading" });
      try {
        const res = await metroAdmin.listAuditLogs({
          entityType: f.entityType, actorUserId: f.actorUserId.trim(), channel: f.channel, pageSize: 50, pageToken: token,
          from: f.from ? timestampFromDate(new Date(`${f.from}T00:00:00`)) : undefined,
          to: f.to ? timestampFromDate(new Date(`${f.to}T23:59:59`)) : undefined,
        });
        if (!current()) return;
        setLogs((cur) => (append ? [...cur, ...res.logs] : res.logs));
        setNext(res.nextPageToken);
        setView({ kind: "ready" });
      } catch (e) {
        if (current()) setView({ kind: "failed", error: e });
      }
    },
    [reads],
  );

  useEffect(() => {
    void load(filters);
  }, [load, filters]);
  useEffect(() => () => reads.invalidate(), [reads]);

  const set = (k: keyof Filters, v: string) => setFilters((f) => ({ ...f, [k]: v }));

  return (
    <section aria-label="审计日志" className="grid gap-4">
      <form className="grid gap-3 sm:grid-cols-3" onSubmit={(e) => e.preventDefault()}>
        <Field label="对象">
          {(a) => (
            <Select {...a} value={filters.entityType} onChange={(e) => set("entityType", e.target.value)}>
              <option value="">全部</option>
              <option value="city">城市</option>
              <option value="rolling_stock">车型</option>
              <option value="line">线路</option>
              <option value="topic">专题</option>
            </Select>
          )}
        </Field>
        <Field label="渠道">
          {(a) => (
            <Select {...a} value={filters.channel} onChange={(e) => set("channel", e.target.value)}>
              <option value="">全部</option>
              <option value="web">网页</option>
              <option value="mcp">MCP</option>
            </Select>
          )}
        </Field>
        <TextField label="执行者用户 ID" value={filters.actorUserId} onChange={(e) => set("actorUserId", e.target.value)} autoComplete="off" spellCheck={false} />
        <Field label="从">{(a) => <Input {...a} type="date" value={filters.from} onChange={(e) => set("from", e.target.value)} />}</Field>
        <Field label="到">{(a) => <Input {...a} type="date" value={filters.to} onChange={(e) => set("to", e.target.value)} />}</Field>
      </form>
      {view.kind === "loading" ? <Spinner /> : null}
      {view.kind === "failed" ? <Problem error={view.error} retry={() => void load(filters)} /> : null}
      {view.kind === "ready" ? (
        <>
          <TableFrame>
            <table className="w-full min-w-[32rem] text-left text-sm">
              <caption className="sr-only">审计日志</caption>
              <thead className="border-b bg-muted text-xs text-muted-foreground">
                <tr>
                  {["时间", "动作", "对象", "渠道", "执行者", ""].map((h, i) => (
                    <th key={i} scope="col" className="px-3 py-2 font-medium">{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {logs.length === 0 ? (
                  <tr>
                    <td colSpan={6} className="px-3 py-6 text-center text-muted-foreground">没有记录。</td>
                  </tr>
                ) : null}
                {logs.map((l) => (
                  <LogRow key={l.id} log={l} open={open === l.id} onToggle={() => setOpen(open === l.id ? undefined : l.id)} />
                ))}
              </tbody>
            </table>
          </TableFrame>
          {next ? (
            <div>
              <Button variant="outline" onClick={() => void load(filters, next, true)}>加载更多</Button>
            </div>
          ) : null}
        </>
      ) : null}
    </section>
  );
}

function LogRow({ log, open, onToggle }: { log: AuditLog; open: boolean; onToggle: () => void }) {
  return (
    <>
      <tr className="border-b">
        <td className="whitespace-nowrap px-3 py-2">{log.createdAt ? dateFmt.format(timestampDate(log.createdAt)) : ""}</td>
        <td className="px-3 py-2">{log.action}</td>
        <td className="px-3 py-2">{log.entityType}</td>
        <td className="px-3 py-2">{log.channel === "mcp" ? `MCP${log.tokenId ? ` (${log.tokenId.slice(0, 8)})` : ""}` : "网页"}</td>
        <td className="max-w-40 truncate px-3 py-2" title={log.actorUserId}>{log.actorUserId}</td>
        <td className="px-3 py-2 text-right">
          <Button size="sm" variant="ghost" aria-expanded={open} onClick={onToggle}>{open ? "收起" : "详情"}</Button>
        </td>
      </tr>
      {open ? (
        <tr className="border-b bg-muted/40">
          <td colSpan={6} className="px-3 py-3">
            <div className="grid gap-3 md:grid-cols-2">
              <Snapshot title="之前" json={log.beforeJson} />
              <Snapshot title="之后" json={log.afterJson} />
            </div>
          </td>
        </tr>
      ) : null}
    </>
  );
}

function Snapshot({ title, json }: { title: string; json: string }) {
  let pretty = json;
  try {
    pretty = json ? JSON.stringify(JSON.parse(json), null, 2) : "";
  } catch {
    // Show it as it is.
  }
  return (
    <div className="min-w-0">
      <div className="mb-1 text-xs font-medium text-muted-foreground">{title}</div>
      <pre className="max-h-64 overflow-auto rounded bg-card p-2 text-xs">{pretty || "（无）"}</pre>
    </div>
  );
}
