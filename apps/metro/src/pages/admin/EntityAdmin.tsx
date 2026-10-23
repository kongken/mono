import { useCallback, useEffect, useState } from "react";
import { Button } from "@kongken/ui/button";
import { Input } from "@kongken/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import { conflictRevision, describeError, fieldProblems, type FieldProblem } from "@/lib/errors";
import { invalidateCatalog } from "@/lib/catalog";
import { Alert, Field, Select, Spinner, Textarea } from "@/components/extras";
import type { Entity, FieldDef, Form, Row } from "./entities";

type Editing = { mode: "create" } | { mode: "edit"; id: string; revision: bigint };

const fieldProblemFor = (problems: FieldProblem[], key: string) =>
  problems.find((p) => p.field === key || p.field.endsWith(`.${key}`) || p.field.endsWith(`.${key.replace(/[A-Z]/g, (c) => `_${c.toLowerCase()}`)}`))?.reason;

/** A searchable, paged table of one kind of record with create, edit and delete. */
export default function EntityAdmin({ entity }: { entity: Entity }) {
  const [query, setQuery] = useState("");
  const [rows, setRows] = useState<Row[]>([]);
  const [next, setNext] = useState("");
  const [total, setTotal] = useState(0n);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<string | undefined>();
  const [notice, setNotice] = useState<string | undefined>();

  const [editing, setEditing] = useState<Editing | undefined>();
  const [deleting, setDeleting] = useState<Row | undefined>();

  const load = useCallback(
    async (q: string, token = "", append = false) => {
      setState((s) => (append ? s : "loading"));
      try {
        const page = await entity.list(q, token);
        setRows((cur) => (append ? [...cur, ...page.rows] : page.rows));
        setNext(page.next);
        setTotal(page.total);
        setState("ready");
        setError(undefined);
        return page.rows;
      } catch (e) {
        setError(describeError(e));
        setState("error");
        return [];
      }
    },
    [entity],
  );

  // Search after a short pause; the first load is immediate.
  useEffect(() => {
    const t = setTimeout(() => void load(query), query ? 300 : 0);
    return () => clearTimeout(t);
  }, [load, query]);

  return (
    <section aria-label={entity.title}>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Input aria-label={`搜索${entity.title}`} placeholder={`搜索${entity.title}`} value={query} onChange={(e) => setQuery(e.target.value)} className="max-w-xs" />
        <span className="text-sm text-muted-foreground">共 {total.toString()} 条</span>
        <Button className="ml-auto" onClick={() => setEditing({ mode: "create" })}>新建{entity.noun}</Button>
      </div>
      {notice ? <Alert kind="success" className="mb-3">{notice}</Alert> : null}
      {state === "loading" && <Spinner />}
      {state === "error" && <Alert>{error}</Alert>}
      {state === "ready" && (
        <>
          <div className="overflow-x-auto rounded-lg border bg-card">
            <table className="w-full text-left text-sm">
              <thead className="bg-muted text-xs text-muted-foreground">
                <tr>
                  {entity.columns.map((c) => <th key={c} scope="col" className="px-3 py-2 font-medium">{c}</th>)}
                  <th scope="col" className="px-3 py-2 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {rows.length === 0 && (
                  <tr><td colSpan={entity.columns.length + 1} className="px-3 py-6 text-center text-muted-foreground">没有{entity.noun}。</td></tr>
                )}
                {rows.map((r) => (
                  <tr key={r.id} className="border-t">
                    {r.cells.map((c, i) => <td key={i} className="px-3 py-2">{c}</td>)}
                    <td className="whitespace-nowrap px-3 py-2 text-right">
                      <Button size="sm" variant="ghost" aria-label={`编辑 ${r.key}`} onClick={() => setEditing({ mode: "edit", id: r.id, revision: r.revision })}>编辑</Button>
                      <Button size="sm" variant="ghost" className="text-destructive" aria-label={`删除 ${r.key}`} onClick={() => setDeleting(r)}>删除</Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {next ? <div className="mt-3"><Button variant="outline" onClick={() => void load(query, next, true)}>加载更多</Button></div> : null}
        </>
      )}

      {editing && (
        <EditDialog
          entity={entity}
          editing={editing}
          row={editing.mode === "edit" ? rows.find((r) => r.id === editing.id) : undefined}
          onClose={() => setEditing(undefined)}
          onSaved={(text) => {
            invalidateCatalog();
            setEditing(undefined);
            setNotice(text);
            void load(query);
          }}
          refresh={async (id) => (await load(query)).find((r) => r.id === id)}
        />
      )}
      {deleting && (
        <DeleteDialog
          entity={entity}
          row={deleting}
          onClose={() => setDeleting(undefined)}
          onDeleted={() => {
            invalidateCatalog();
            setDeleting(undefined);
            setNotice(`已删除 ${deleting.key}。`);
            void load(query);
          }}
          refresh={async (id) => (await load(query)).find((r) => r.id === id)}
        />
      )}
    </section>
  );
}

function EditDialog({ entity, editing, row, onClose, onSaved, refresh }: {
  entity: Entity;
  editing: Editing;
  row: Row | undefined;
  onClose: () => void;
  onSaved: (text: string) => void;
  refresh: (id: string) => Promise<Row | undefined>;
}) {
  const [form, setForm] = useState<Form>(() => (row ? { ...row.form } : entity.emptyForm()));
  const [revision, setRevision] = useState(editing.mode === "edit" ? editing.revision : 0n);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [problems, setProblems] = useState<FieldProblem[]>([]);
  const [stale, setStale] = useState(false);
  const isEdit = editing.mode === "edit";

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    setProblems([]);
    try {
      if (editing.mode === "edit") await entity.update(editing.id, revision, form);
      else await entity.create(form);
      onSaved(isEdit ? "已保存修改。" : `已创建${entity.noun}。`);
    } catch (x) {
      setProblems(fieldProblems(x));
      setStale(conflictRevision(x) !== undefined);
      setError(describeError(x));
      setBusy(false);
    }
  };
  // Another editor changed the record: show their version and let the user redo the edit.
  const reload = async () => {
    if (editing.mode !== "edit") return;
    setBusy(true);
    const fresh = await refresh(editing.id);
    if (!fresh) return onClose();
    setForm({ ...fresh.form });
    setRevision(fresh.revision);
    setStale(false);
    setError(undefined);
    setBusy(false);
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{isEdit ? `编辑${entity.noun}` : `新建${entity.noun}`}</DialogTitle>
          <DialogDescription>{isEdit ? "保存会替换所有字段。" : "填写后保存。"}</DialogDescription>
        </DialogHeader>
        <form onSubmit={(e) => void save(e)} className="grid gap-3">
          {entity.fields.map((f) => (
            <FieldInput key={f.key} def={f} value={form[f.key]} locked={isEdit && f.lockedOnEdit === true} error={fieldProblemFor(problems, f.key)} onChange={(v) => setForm((cur) => ({ ...cur, [f.key]: v }))} />
          ))}
          {error ? (
            <Alert>
              {stale ? "这条记录已被他人修改。" : error}
              {stale ? <Button type="button" size="sm" variant="outline" className="ml-2" onClick={() => void reload()}>刷新并重新编辑</Button> : null}
            </Alert>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>取消</Button>
            <Button type="submit" disabled={busy}>保存</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function FieldInput({ def, value, locked, error, onChange }: { def: FieldDef; value: string | boolean | undefined; locked: boolean; error?: string; onChange: (v: string | boolean) => void }) {
  if (def.kind === "checkbox") {
    return (
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" className="size-4" checked={value === true} onChange={(e) => onChange(e.target.checked)} /> {def.label}
      </label>
    );
  }
  const v = String(value ?? "");
  return (
    <Field label={def.label} hint={def.hint} error={error}>
      {(a) =>
        def.kind === "textarea" ? (
          <Textarea {...a} value={v} onChange={(e) => onChange(e.target.value)} />
        ) : def.kind === "select" ? (
          <Select {...a} value={v} onChange={(e) => onChange(e.target.value)}>
            {def.options?.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </Select>
        ) : (
          <Input {...a} value={v} disabled={locked} inputMode={def.kind === "number" ? "numeric" : def.kind === "decimal" ? "decimal" : undefined} onChange={(e) => onChange(e.target.value)} />
        )
      }
    </Field>
  );
}

function DeleteDialog({ entity, row, onClose, onDeleted, refresh }: {
  entity: Entity;
  row: Row;
  onClose: () => void;
  onDeleted: () => void;
  refresh: (id: string) => Promise<Row | undefined>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [revision, setRevision] = useState(row.revision);
  const [stale, setStale] = useState(false);
  const confirm = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await entity.remove(row.id, revision);
      onDeleted();
    } catch (e) {
      setStale(conflictRevision(e) !== undefined);
      setError(describeError(e));
      setBusy(false);
    }
  };
  const reload = async () => {
    const fresh = await refresh(row.id);
    if (!fresh) return onClose();
    setRevision(fresh.revision);
    setStale(false);
    setError(undefined);
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>删除{entity.noun} {row.key}？</DialogTitle>
          <DialogDescription>不可恢复。被引用时会失败。</DialogDescription>
        </DialogHeader>
        {error ? (
          <Alert>
            {error}
            {stale ? <Button size="sm" variant="outline" className="ml-2" onClick={() => void reload()}>刷新</Button> : null}
          </Alert>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button variant="destructive" disabled={busy} onClick={() => void confirm()}>确认删除</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
