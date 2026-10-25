import { useEffect, useRef, useState } from "react";
import type { ImportCatalogResponse } from "@kongken/api-client/bff/metro/v1/admin_pb";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { Problem } from "@/components/Problem";
import { Spinner } from "@/components/Layout";
import { Textarea } from "@/components/metro/ui";
import { saveDraft, takeDraft } from "@/lib/draft.ts";
import { describeMetroError, fieldProblems, isSessionLevel } from "@/lib/metro-errors";
import { metroAdmin, write } from "@/lib/rpc";
import { useSession } from "@/lib/session";

type Preview =
  | { kind: "idle" }
  | { kind: "checking" }
  | { kind: "ok"; text: string; res: ImportCatalogResponse }
  | { kind: "failed"; error: unknown };

const MAX_BYTES = 1 << 20;

/** Paste or choose a catalogue file: it is validated as a dry run first, and only then can it be applied. */
export default function ImportPage() {
  const { session } = useSession();
  const userId = session.status === "signed-in" ? session.profile.user.id : "";
  const [text, setText] = useState("");
  const [preview, setPreview] = useState<Preview>({ kind: "idle" });
  const [applying, setApplying] = useState(false);
  const [done, setDone] = useState<string | undefined>();
  const [error, setError] = useState<unknown>(undefined);
  const [restored, setRestored] = useState(false);
  const seq = useRef(0);
  // Emptying the box after a successful import must not wipe the success message.
  const keepDone = useRef(false);

  // Coming back from the password confirmation with a document in the box: it is offered again as text and
  // checked again as a preview; applying it is a click the person makes.
  const looked = useRef(false);
  useEffect(() => {
    if (looked.current || userId === "") return;
    looked.current = true;
    const draft = takeDraft(window.sessionStorage, userId, "metro:import");
    const data = draft?.data as { text?: unknown } | undefined;
    if (typeof data?.text === "string" && data.text !== "") {
      setText(data.text);
      setRestored(true);
    }
  }, [userId]);

  // Every change of the text is checked again after a short pause; only the latest check counts.
  useEffect(() => {
    if (keepDone.current) keepDone.current = false;
    else setDone(undefined);
    setError(undefined);
    if (text.trim() === "") return setPreview({ kind: "idle" });
    if (new Blob([text]).size > MAX_BYTES) return setPreview({ kind: "failed", error: new Error("文件超过 1 MiB。") });
    setPreview({ kind: "checking" });
    const mine = ++seq.current;
    const t = setTimeout(async () => {
      try {
        const res = await write(() => metroAdmin.importCatalog({ catalogJson: text, dryRun: true }));
        if (mine === seq.current) setPreview({ kind: "ok", text, res });
      } catch (e) {
        if (mine === seq.current) setPreview({ kind: "failed", error: e });
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
      const res = await write(() => metroAdmin.importCatalog({ catalogJson: preview.text, dryRun: false }));
      setDone(`已导入：${summary(res)}。目录版本 ${res.catalogVersion}。`);
      keepDone.current = true;
      setText("");
    } catch (e) {
      setError(e);
    } finally {
      setApplying(false);
    }
  };

  const keepText = () => saveDraft(window.sessionStorage, { userId, scope: "metro:import", data: { text } });
  const errors = preview.kind === "ok" ? preview.res.errors : [];
  const canApply = preview.kind === "ok" && errors.length === 0 && !applying;
  const failure = preview.kind === "failed" ? preview.error : error;

  return (
    <section aria-label="导入" className="grid gap-3">
      <p className="max-w-2xl text-sm text-muted-foreground">
        导入 <code>metro-catalog/1</code> JSON：按城市 slug、车型代码、线路代码、专题 slug 新增或更新，不会删除文件里没有的记录。任何一条出错则整体不写入。
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <label className="text-sm">
          选择文件 <input type="file" accept="application/json,.json" onChange={(e) => void readFile(e.target.files?.[0])} className="ml-1 text-sm" />
        </label>
      </div>
      <Textarea aria-label="目录 JSON" rows={12} spellCheck={false} className="font-mono text-xs" placeholder='{"format":"metro-catalog/1","cities":[],"rolling_stocks":[],"lines":[],"topics":[]}' value={text} onChange={(e) => setText(e.target.value)} />
      <div className="grid gap-3" aria-live="polite">
        {restored ? <Notice kind="info">已恢复你在验证密码前粘贴、尚未导入的内容，下面是重新校验的预览。确认无误后请再点击“确认导入”。</Notice> : null}
        {preview.kind === "checking" ? <Spinner label="正在校验" /> : null}
        {failure !== undefined ? (
          isSessionLevel(failure) ? (
            <Problem error={failure} keepBeforeReauth={keepText} />
          ) : (
            <Notice kind="error">
              <span className="whitespace-pre-wrap">{failureText(failure)}</span>
            </Notice>
          )
        ) : null}
        {preview.kind === "ok" ? (
          <>
            <Notice kind={errors.length ? "error" : "info"}>预览（尚未写入）：{summary(preview.res)}</Notice>
            {errors.length > 0 ? (
              <ul className="list-disc pl-6 text-sm text-destructive" aria-label="错误">
                {errors.map((e, i) => (
                  <li key={i}><code>{e.field}</code>：{e.reason}</li>
                ))}
              </ul>
            ) : null}
          </>
        ) : null}
        {done ? <Notice kind="success">{done}</Notice> : null}
      </div>
      <div>
        <Button type="button" disabled={!canApply} onClick={() => void apply()}>确认导入</Button>
      </div>
    </section>
  );
}

function failureText(e: unknown): string {
  const problems = fieldProblems(e);
  if (problems.length > 0) return problems.map((p) => `${p.field}：${p.reason}`).join("\n");
  return e instanceof Error && !("code" in e) ? e.message : describeMetroError(e);
}

function summary(r: ImportCatalogResponse): string {
  const part = (label: string, c?: { created: number; updated: number; unchanged: number }) =>
    c ? `${label} 新增 ${c.created} / 更新 ${c.updated} / 无变化 ${c.unchanged}` : `${label} 0`;
  return [part("城市", r.cities), part("车型", r.rollingStocks), part("线路", r.lines), part("专题", r.topics)].join("；");
}
