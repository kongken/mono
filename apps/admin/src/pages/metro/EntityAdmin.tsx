import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@kongken/ui/button";
import { Input } from "@kongken/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import { createEpoch } from "@kongken/account/latest";
import { Field, Notice } from "@kongken/account/ui";
import { Problem } from "@/components/Problem";
import { Spinner } from "@/components/Layout";
import { Select, TableFrame, Textarea } from "@/components/metro/ui";
import { saveDraft, takeDraft } from "@/lib/draft.ts";
import { conflictRevision, describeMetroError, fieldProblems, isNotFound, isSessionLevel, type FieldProblem } from "@/lib/metro-errors";
import { diffForms, isForm, problemFor, type FieldDef, type Form } from "@/lib/metro-form";
import { useSession } from "@/lib/session";
import type { Entity, Row } from "./entities";

type Editing = { mode: "create" } | { mode: "edit"; id: string; revision: bigint; key: string };

/** What the dialog starts from when the person comes back from the password confirmation. */
type Restored = { form: Form; conflict: Row | "gone" | undefined };

const scopeOf = (entity: Entity, editing: Editing) => `metro:${entity.id}:${editing.mode === "edit" ? `edit:${editing.id}` : "create"}`;

type View = { kind: "loading" } | { kind: "ready" } | { kind: "failed"; error: unknown };

/**
 * A searchable, paged table of one kind of record with create, edit and delete. Writes carry the
 * revision the person saw; a lost race keeps what they typed and asks them to confirm again.
 */
export default function EntityAdmin({ entity }: { entity: Entity }) {
  const { session } = useSession();
  const userId = session.status === "signed-in" ? session.profile.user.id : "";
  const [query, setQuery] = useState("");
  const [rows, setRows] = useState<Row[]>([]);
  const [next, setNext] = useState("");
  const [total, setTotal] = useState(0n);
  const [view, setView] = useState<View>({ kind: "loading" });
  const [notice, setNotice] = useState<string | undefined>();
  const [reads] = useState(createEpoch);

  const [editing, setEditing] = useState<Editing | undefined>();
  const [restored, setRestored] = useState<Restored | undefined>();
  const [deleting, setDeleting] = useState<Row | undefined>();

  const load = useCallback(
    async (q: string, token = "", append = false): Promise<Row[]> => {
      const current = reads.begin();
      if (!append) setView({ kind: "loading" });
      try {
        const page = await entity.list(q, token);
        if (!current()) return [];
        setRows((cur) => (append ? [...cur, ...page.rows] : page.rows));
        setNext(page.next);
        setTotal(page.total);
        setView({ kind: "ready" });
        return page.rows;
      } catch (e) {
        if (current()) setView({ kind: "failed", error: e });
        return [];
      }
    },
    [entity, reads],
  );

  // Search after a short pause; the first load is immediate. Only the newest answer is shown.
  useEffect(() => {
    const t = setTimeout(() => void load(query), query ? 300 : 0);
    return () => clearTimeout(t);
  }, [load, query]);
  useEffect(() => () => reads.invalidate(), [reads]);

  // Coming back from the password confirmation with something typed: offer it again, never submit it.
  const looked = useRef(false);
  useEffect(() => {
    if (looked.current || userId === "") return;
    looked.current = true;
    const draft = takeDraft(window.sessionStorage, userId, `metro:${entity.id}:`);
    const data = draft?.data as { form?: unknown; id?: unknown; key?: unknown; baseRevision?: unknown } | undefined;
    if (!draft || !data || !isForm(data.form)) return;
    const form = data.form;
    if (draft.scope.endsWith(":create")) {
      setRestored({ form, conflict: undefined });
      setEditing({ mode: "create" });
      setNotice("已恢复你在验证密码前填写、尚未提交的内容。请核对后再保存，它不会自动提交。");
      return;
    }
    if (typeof data.id !== "string" || typeof data.key !== "string" || typeof data.baseRevision !== "string") return;
    const { id, key } = data;
    const base = data.baseRevision;
    void entity.list(key, "").then(
      (page) => {
        const row = page.rows.find((r) => r.id === id);
        setNotice("已恢复你在验证密码前填写、尚未提交的修改。请核对后再保存，它不会自动提交。");
        if (!row) {
          setRestored({ form, conflict: "gone" });
          setEditing({ mode: "edit", id, revision: BigInt(base), key });
        } else {
          setRestored({ form, conflict: row.revision.toString() === base ? undefined : row });
          setEditing({ mode: "edit", id, revision: row.revision, key });
        }
      },
      () => setNotice("没能恢复验证密码前未提交的内容：读取记录失败。"),
    );
  }, [entity, userId]);

  const refreshRow = async (id: string, key: string) => (await entity.list(key, "")).rows.find((r) => r.id === id);

  return (
    <section aria-label={entity.title} className="grid gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <Input aria-label={`搜索${entity.title}`} placeholder={`搜索${entity.title}`} value={query} onChange={(e) => setQuery(e.target.value)} className="max-w-xs" />
        <span className="text-sm text-muted-foreground">共 {total.toString()} 条</span>
        <Button type="button"
          className="ml-auto"
          onClick={() => {
            setRestored(undefined);
            setEditing({ mode: "create" });
          }}
        >
          新建{entity.noun}
        </Button>
      </div>
      {notice ? <Notice kind="success">{notice}</Notice> : null}
      {view.kind === "loading" ? <Spinner /> : null}
      {view.kind === "failed" ? <Problem error={view.error} retry={() => void load(query)} /> : null}
      {view.kind === "ready" ? (
        <>
          <TableFrame>
            <table className="w-full min-w-[32rem] text-left text-sm">
              <caption className="sr-only">{entity.title}列表</caption>
              <thead className="border-b bg-muted text-xs text-muted-foreground">
                <tr>
                  {entity.columns.map((c) => (
                    <th key={c} scope="col" className="px-3 py-2 font-medium">{c}</th>
                  ))}
                  <th scope="col" className="px-3 py-2 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {rows.length === 0 ? (
                  <tr>
                    <td colSpan={entity.columns.length + 1} className="px-3 py-6 text-center text-muted-foreground">没有{entity.noun}。</td>
                  </tr>
                ) : null}
                {rows.map((r) => (
                  <tr key={r.id} className="border-b last:border-0">
                    {r.cells.map((c, i) => (
                      <td key={i} className="px-3 py-2">{c}</td>
                    ))}
                    <td className="whitespace-nowrap px-3 py-2 text-right">
                      <Button type="button"
                        size="sm"
                        variant="ghost"
                        aria-label={`编辑 ${r.key}`}
                        onClick={() => {
                          setRestored(undefined);
                          setEditing({ mode: "edit", id: r.id, revision: r.revision, key: r.key });
                        }}
                      >
                        编辑
                      </Button>
                      <Button type="button" size="sm" variant="ghost" className="text-destructive" aria-label={`删除 ${r.key}`} onClick={() => setDeleting(r)}>
                        删除
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableFrame>
          {next ? (
            <div>
              <Button type="button" variant="outline" onClick={() => void load(query, next, true)}>加载更多</Button>
            </div>
          ) : null}
        </>
      ) : null}

      {editing ? (
        <EditDialog
          entity={entity}
          editing={editing}
          userId={userId}
          row={editing.mode === "edit" ? rows.find((r) => r.id === editing.id) : undefined}
          restored={restored}
          onClose={() => {
            setEditing(undefined);
            setRestored(undefined);
          }}
          onSaved={(text) => {
            setEditing(undefined);
            setRestored(undefined);
            setNotice(text);
            void load(query);
          }}
          refresh={refreshRow}
        />
      ) : null}
      {deleting ? (
        <DeleteDialog
          entity={entity}
          row={deleting}
          onClose={() => setDeleting(undefined)}
          onDeleted={() => {
            setDeleting(undefined);
            setNotice(`已删除 ${deleting.key}。`);
            void load(query);
          }}
          refresh={refreshRow}
        />
      ) : null}
    </section>
  );
}

function EditDialog({ entity, editing, userId, row, restored, onClose, onSaved, refresh }: {
  entity: Entity;
  editing: Editing;
  userId: string;
  row: Row | undefined;
  restored: Restored | undefined;
  onClose: () => void;
  onSaved: (text: string) => void;
  refresh: (id: string, key: string) => Promise<Row | undefined>;
}) {
  const [form, setForm] = useState<Form>(() => restored?.form ?? (row ? { ...row.form } : entity.emptyForm()));
  const [revision, setRevision] = useState(editing.mode === "edit" ? editing.revision : 0n);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(undefined);
  const [problems, setProblems] = useState<FieldProblem[]>([]);
  // The record as it is now, once a save found that someone else changed it (or removed it): what the person typed stays.
  const [conflict, setConflict] = useState<Row | "gone" | undefined>(restored?.conflict);
  const [rebased, setRebased] = useState(false);
  const isEdit = editing.mode === "edit";
  // Field errors are shown at their fields; only those that belong to no field of this form are said in words.
  const unmapped = problems.filter((p) => !entity.fields.some((f) => problemFor([p], f.key, f.serverKey)));

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (conflict) return;
    setBusy(true);
    setError(undefined);
    setProblems([]);
    setRebased(false);
    try {
      if (editing.mode === "edit") await entity.update(editing.id, revision, form);
      else await entity.create(form);
      onSaved(isEdit ? "已保存修改。" : `已创建${entity.noun}。`);
    } catch (x) {
      // Someone else changed the record, or removed it: look at it as it is now, and keep what was typed.
      if ((conflictRevision(x) !== undefined || isNotFound(x)) && editing.mode === "edit") {
        try {
          setConflict((await refresh(editing.id, editing.key)) ?? "gone");
        } catch (y) {
          setError(y);
        }
      } else {
        setProblems(fieldProblems(x));
        setError(x);
      }
      setBusy(false);
    }
  };

  const keepMine = () => {
    if (conflict && conflict !== "gone") setRevision(conflict.revision);
    setConflict(undefined);
    setRebased(true);
  };
  const takeLatest = () => {
    if (conflict && conflict !== "gone") {
      setForm({ ...conflict.form });
      setRevision(conflict.revision);
    }
    setConflict(undefined);
    setProblems([]);
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
            <FieldInput key={f.key} def={f} value={form[f.key]} locked={isEdit && f.lockedOnEdit === true} error={problemFor(problems, f.key, f.serverKey)} onChange={(v) => setForm((cur) => ({ ...cur, [f.key]: v }))} />
          ))}
          {conflict ? <ConflictPanel entity={entity} conflict={conflict} mine={form} onKeepMine={keepMine} onTakeLatest={takeLatest} /> : null}
          {rebased ? <Notice kind="info">已基于最新版本。你的内容没有改动，请确认后再次点击保存。</Notice> : null}
          {error !== undefined && isSessionLevel(error) ? (
            <Problem
              error={error}
              keep={{
                save: () =>
                  saveDraft(window.sessionStorage, {
                    userId,
                    scope: scopeOf(entity, editing),
                    data: { form, id: editing.mode === "edit" ? editing.id : "", key: editing.mode === "edit" ? editing.key : "", baseRevision: revision.toString() },
                  }),
                text: () => JSON.stringify(form, null, 2),
                filename: `metro-${entity.id}-form.json`,
              }}
            />
          ) : error !== undefined && problems.length === 0 ? (
            <Notice kind="error">{describeMetroError(error)}</Notice>
          ) : unmapped.length > 0 ? (
            <Notice kind="error">{unmapped.map((p) => `${p.field}：${p.reason}`).join("；")}</Notice>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>取消</Button>
            <Button type="submit" disabled={busy || conflict !== undefined}>保存</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** The record changed while the person was editing: show what differs, keep their content, and let them choose. */
function ConflictPanel({ entity, conflict, mine, onKeepMine, onTakeLatest }: {
  entity: Entity;
  conflict: Row | "gone";
  mine: Form;
  onKeepMine: () => void;
  onTakeLatest: () => void;
}) {
  if (conflict === "gone") {
    return (
      <div className="grid gap-2" data-testid="conflict">
        <Notice kind="error">这条记录已被删除，不能再保存。你填写的内容仍保留在上面，可以复制后关闭。</Notice>
      </div>
    );
  }
  const diffs = diffForms(entity.fields, conflict.form, mine);
  return (
    <div className="grid gap-3 rounded-md border border-destructive/40 p-3" data-testid="conflict">
      <Notice kind="error">这条记录已被他人修改。你填写的内容仍保留在上面，没有被覆盖；请对照最新内容，确认后再保存。</Notice>
      {diffs.length === 0 ? (
        <p className="text-sm text-muted-foreground">最新内容与你填写的相同，只是版本变了。</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs" aria-label="与最新内容的差异">
            <thead className="text-muted-foreground">
              <tr>
                <th scope="col" className="py-1 pr-3 font-medium">字段</th>
                <th scope="col" className="py-1 pr-3 font-medium">最新内容</th>
                <th scope="col" className="py-1 font-medium">你填写的</th>
              </tr>
            </thead>
            <tbody>
              {diffs.map((d) => (
                <tr key={d.key} className="border-t align-top">
                  <th scope="row" className="py-1 pr-3 font-medium">{d.label}</th>
                  <td className="py-1 pr-3 whitespace-pre-wrap break-all">{d.latest}</td>
                  <td className="py-1 whitespace-pre-wrap break-all">{d.mine}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        <Button type="button" size="sm" variant="outline" onClick={onKeepMine}>保留我的修改，基于最新版本</Button>
        <Button type="button" size="sm" variant="outline" onClick={onTakeLatest}>放弃我的修改，载入最新内容</Button>
      </div>
    </div>
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
            {def.options?.map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
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
  refresh: (id: string, key: string) => Promise<Row | undefined>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(undefined);
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
      setError(e);
      setBusy(false);
    }
  };
  // A lost race never deletes by itself: the latest revision is taken and the person confirms once more.
  const reload = async () => {
    const fresh = await refresh(row.id, row.key);
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
        {error !== undefined && isSessionLevel(error) ? (
          <Problem error={error} />
        ) : error !== undefined ? (
          <Notice kind="error">
            {describeMetroError(error)}
            {stale ? (
              <Button type="button" size="sm" variant="outline" className="ml-2" onClick={() => void reload()}>
                载入最新版本后重新确认
              </Button>
            ) : null}
          </Notice>
        ) : null}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>取消</Button>
          <Button type="button" variant="destructive" disabled={busy} onClick={() => void confirm()}>确认删除</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
