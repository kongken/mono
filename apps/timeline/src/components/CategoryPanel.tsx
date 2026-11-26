import { useEffect, useState } from "react";
import type { Category } from "@kongken/api-client/bff/timeline/v1/timeline_pb";
import { mutationID, timeline, write } from "@/lib/rpc.ts";

export function CategoryPanel() {
  const [categories, setCategories] = useState<Category[]>([]);
  const [name, setName] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const load = async () => {
    try { setCategories((await timeline.listCategories({})).categories); setError(null); }
    catch (e) { setError(e instanceof Error ? e.message : "读取分类失败"); }
  };
  useEffect(() => { void load(); }, []);
  const create = async () => {
    if (name.trim() === "") return;
    try { await write(() => timeline.createCategory({ name: name.trim(), mutationId: mutationID() })); setName(""); await load(); }
    catch (e) { setError(e instanceof Error ? e.message : "创建分类失败"); }
  };
  const toggle = async (category: Category) => {
    try {
      await write(() => timeline.updateCategory({ categoryRef: category.categoryRef, name: category.name, active: !category.active, expectedRevision: category.revision, mutationId: mutationID() }));
      await load();
    } catch (e) { setError(e instanceof Error ? e.message : "更新分类失败"); }
  };
  const rename = async (category: Category) => {
    try { await write(() => timeline.updateCategory({ categoryRef: category.categoryRef, name: editName.trim(), active: category.active, expectedRevision: category.revision, mutationId: mutationID() })); setEditing(null); await load(); }
    catch (e) { setError(e instanceof Error ? e.message : "重命名分类失败"); }
  };
  return <section className="rounded-card bg-paper p-5" aria-label="分类管理">
    <h2 className="text-base font-medium text-ink">分类管理</h2>
    <div className="mt-4 flex gap-2"><input value={name} onChange={(e) => setName(e.target.value)} placeholder="新分类名称" className="field h-9 flex-1 bg-canvas px-2 text-sm" /><button type="button" onClick={() => void create()} className="rounded-card bg-leaf px-4 text-[13px] font-medium text-white transition-colors hover:bg-leaf-hover">添加</button></div>
    {error !== null && <p className="mt-3 text-xs text-required">{error}</p>}
    <ul className="mt-4 divide-y divide-line">{categories.map((category) => <li key={category.categoryRef} className="flex items-center justify-between gap-3 py-3 text-sm"><span className="min-w-0 flex-1">{editing === category.categoryRef ? <input value={editName} onChange={(e) => setEditName(e.target.value)} className="field h-8 w-full bg-canvas px-2" /> : <>{category.name}{category.isDefault && <small className="ml-2 text-soft">默认</small>}</>}</span><span className="flex shrink-0 gap-2"><button type="button" disabled={category.isOther} onClick={() => editing === category.categoryRef ? void rename(category) : (setEditing(category.categoryRef), setEditName(category.name))} className="text-xs text-leaf disabled:opacity-40">{editing === category.categoryRef ? "保存" : "改名"}</button><button type="button" disabled={category.isOther} onClick={() => void toggle(category)} className="text-xs text-leaf disabled:opacity-40">{category.active ? "停用" : "启用"}</button></span></li>)}</ul>
  </section>;
}
