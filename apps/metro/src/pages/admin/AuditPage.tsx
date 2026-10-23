import { useCallback, useEffect, useState } from "react";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { AuditLog } from "@kongken/api-client/bff/metro/v1/admin_pb";
import { Button } from "@kongken/ui/button";
import { admin } from "@/lib/rpc";
import { describeError } from "@/lib/errors";
import { Alert, Field, Select, Spinner, TextField } from "@/components/extras";
import { Input } from "@kongken/ui/input";

const dateFmt = new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "medium" });

export default function AuditPage() {
  const [filters, setFilters] = useState({ entityType: "", actorUserId: "", channel: "", from: "", to: "" });
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [next, setNext] = useState("");
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<string | undefined>();
  const [open, setOpen] = useState<string | undefined>();

  const load = useCallback(async (f: typeof filters, token = "", append = false) => {
    if (!append) setState("loading");
    try {
      const res = await admin.listAuditLogs({
        entityType: f.entityType, actorUserId: f.actorUserId.trim(), channel: f.channel, pageSize: 50, pageToken: token,
        from: f.from ? timestampFromDate(new Date(`${f.from}T00:00:00`)) : undefined,
        to: f.to ? timestampFromDate(new Date(`${f.to}T23:59:59`)) : undefined,
      });
      setLogs((cur) => (append ? [...cur, ...res.logs] : res.logs));
      setNext(res.nextPageToken);
      setState("ready");
    } catch (e) {
      setError(describeError(e));
      setState("error");
    }
  }, []);

  useEffect(() => {
    void load(filters);
  }, [load, filters]);

  const set = (k: keyof typeof filters, v: string) => setFilters((f) => ({ ...f, [k]: v }));

  return (
    <section aria-label="审计日志">
      <form className="mb-4 grid gap-3 sm:grid-cols-3" onSubmit={(e) => e.preventDefault()}>
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
        <TextField label="执行者用户 ID" value={filters.actorUserId} onChange={(e) => set("actorUserId", e.target.value)} />
        <Field label="从">{(a) => <Input {...a} type="date" value={filters.from} onChange={(e) => set("from", e.target.value)} />}</Field>
        <Field label="到">{(a) => <Input {...a} type="date" value={filters.to} onChange={(e) => set("to", e.target.value)} />}</Field>
      </form>
      {state === "loading" && <Spinner />}
      {state === "error" && <Alert>{error}</Alert>}
      {state === "ready" && (
        <>
          <div className="overflow-x-auto rounded-lg border bg-card">
            <table className="w-full text-left text-sm">
              <thead className="bg-muted text-xs text-muted-foreground">
                <tr>{["时间", "动作", "对象", "渠道", "执行者", ""].map((h, i) => <th key={i} scope="col" className="px-3 py-2 font-medium">{h}</th>)}</tr>
              </thead>
              <tbody>
                {logs.length === 0 && <tr><td colSpan={6} className="px-3 py-6 text-center text-muted-foreground">没有记录。</td></tr>}
                {logs.map((l) => (
                  <FragmentRow key={l.id} log={l} open={open === l.id} onToggle={() => setOpen(open === l.id ? undefined : l.id)} />
                ))}
              </tbody>
            </table>
          </div>
          {next ? <div className="mt-3"><Button variant="outline" onClick={() => void load(filters, next, true)}>加载更多</Button></div> : null}
        </>
      )}
    </section>
  );
}

function FragmentRow({ log, open, onToggle }: { log: AuditLog; open: boolean; onToggle: () => void }) {
  return (
    <>
      <tr className="border-t">
        <td className="whitespace-nowrap px-3 py-2">{log.createdAt ? dateFmt.format(timestampDate(log.createdAt)) : ""}</td>
        <td className="px-3 py-2">{log.action}</td>
        <td className="px-3 py-2">{log.entityType}</td>
        <td className="px-3 py-2">{log.channel === "mcp" ? `MCP${log.tokenId ? ` (${log.tokenId.slice(0, 8)})` : ""}` : "网页"}</td>
        <td className="max-w-40 truncate px-3 py-2" title={log.actorUserId}>{log.actorUserId}</td>
        <td className="px-3 py-2 text-right"><Button size="sm" variant="ghost" aria-expanded={open} onClick={onToggle}>{open ? "收起" : "详情"}</Button></td>
      </tr>
      {open && (
        <tr className="border-t bg-muted/40">
          <td colSpan={6} className="px-3 py-3">
            <div className="grid gap-3 md:grid-cols-2">
              <Snapshot title="之前" json={log.beforeJson} />
              <Snapshot title="之后" json={log.afterJson} />
            </div>
          </td>
        </tr>
      )}
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
