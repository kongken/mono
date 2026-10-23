import { useEffect, useRef, useState } from "react";
import type { ImportCatalogResponse } from "@kongken/api-client/bff/metro/v1/admin_pb";
import { Button } from "@kongken/ui/button";
import { admin, write } from "@/lib/rpc";
import { invalidateCatalog } from "@/lib/catalog";
import { describeError, fieldProblems } from "@/lib/errors";
import { Alert, Spinner, Textarea } from "@/components/extras";

type Preview =
  | { kind: "idle" }
  | { kind: "checking" }
  | { kind: "ok"; text: string; res: ImportCatalogResponse }
  | { kind: "failed"; text: string; message: string };

const MAX_BYTES = 1 << 20;

/** Paste or choose a catalogue file: it is validated as a dry run first, and only then can it be applied. */
export default function ImportPage() {
  const [text, setText] = useState("");
  const [preview, setPreview] = useState<Preview>({ kind: "idle" });
  const [applying, setApplying] = useState(false);
  const [done, setDone] = useState<string | undefined>();
  const [error, setError] = useState<string | undefined>();
  const seq = useRef(0);
  // Emptying the box after a successful import must not wipe the success message.
  const keepDone = useRef(false);

  // Every change of the text is checked again after a short pause; only the latest check counts.
  useEffect(() => {
    if (keepDone.current) keepDone.current = false;
    else setDone(undefined);
    setError(undefined);
    if (text.trim() === "") return setPreview({ kind: "idle" });
    if (new Blob([text]).size > MAX_BYTES) return setPreview({ kind: "failed", text, message: "文件超过 1 MiB。" });
    setPreview({ kind: "checking" });
    const mine = ++seq.current;
    const t = setTimeout(async () => {
      try {
        const res = await write(() => admin.importCatalog({ catalogJson: text, dryRun: true }));
        if (mine === seq.current) setPreview({ kind: "ok", text, res });
      } catch (e) {
        if (mine !== seq.current) return;
        const problems = fieldProblems(e);
        setPreview({ kind: "failed", text, message: problems.length ? problems.map((p) => `${p.field}：${p.reason}`).join("\n") : describeError(e) });
      }
    }, 400);
    return () => clearTimeout(t);
  }, [text]);

  const readFile = async (file: File | undefined) => {
    if (file) setText(await file.text());
  };

  const apply = async () => {
    if (preview.kind !== "ok") return;
    setApplying(true);
    setError(undefined);
    try {
      const res = await write(() => admin.importCatalog({ catalogJson: preview.text, dryRun: false }));
      invalidateCatalog();
      setDone(`已导入：${summary(res)}。目录版本 ${res.catalogVersion}。`);
      keepDone.current = true;
      setText("");
    } catch (e) {
      setError(describeError(e));
    } finally {
      setApplying(false);
    }
  };

  const errors = preview.kind === "ok" ? preview.res.errors : [];
  const canApply = preview.kind === "ok" && errors.length === 0 && !applying;

  return (
    <section aria-label="导入">
      <p className="mb-3 max-w-2xl text-sm text-muted-foreground">
        导入 <code>metro-catalog/1</code> JSON：按城市 slug、车型代码、线路代码、专题 slug 新增或更新，不会删除文件里没有的记录。任何一条出错则整体不写入。
      </p>
      <div className="mb-3 flex flex-wrap items-center gap-3">
        <label className="text-sm">
          选择文件 <input type="file" accept="application/json,.json" onChange={(e) => void readFile(e.target.files?.[0])} className="ml-1 text-sm" />
        </label>
      </div>
      <Textarea aria-label="目录 JSON" rows={12} spellCheck={false} className="font-mono text-xs" placeholder='{"format":"metro-catalog/1","cities":[],"rolling_stocks":[],"lines":[],"topics":[]}' value={text} onChange={(e) => setText(e.target.value)} />
      <div className="mt-4 grid gap-3" aria-live="polite">
        {preview.kind === "checking" && <Spinner label="正在校验" />}
        {preview.kind === "failed" && <Alert><span className="whitespace-pre-wrap">{preview.message}</span></Alert>}
        {preview.kind === "ok" && (
          <>
            <Alert kind={errors.length ? "error" : "info"}>
              预览（尚未写入）：{summary(preview.res)}
            </Alert>
            {errors.length > 0 && (
              <ul className="list-disc pl-6 text-sm text-destructive" aria-label="错误">
                {errors.map((e, i) => <li key={i}><code>{e.field}</code>：{e.reason}</li>)}
              </ul>
            )}
          </>
        )}
        {done ? <Alert kind="success">{done}</Alert> : null}
        {error ? <Alert>{error}</Alert> : null}
      </div>
      <div className="mt-4">
        <Button disabled={!canApply} onClick={() => void apply()}>确认导入</Button>
      </div>
    </section>
  );
}

function summary(r: ImportCatalogResponse): string {
  const part = (label: string, c?: { created: number; updated: number; unchanged: number }) =>
    c ? `${label} 新增 ${c.created} / 更新 ${c.updated} / 无变化 ${c.unchanged}` : `${label} 0`;
  return [part("城市", r.cities), part("车型", r.rollingStocks), part("线路", r.lines), part("专题", r.topics)].join("；");
}
