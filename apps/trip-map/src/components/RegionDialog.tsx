import { useEffect, useRef, useState } from "react";
import { ArrowRightIcon, ArrowsClockwiseIcon } from "@phosphor-icons/react";
import { regionApi } from "@/api/regions";
import { describeApiError } from "@/lib/errors";
import { REGION_VERSION } from "@/lib/region-catalog";
import type { Cloud } from "@/hooks/useCloud";
import { Button } from "@kongken/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import "./regions.css";
import areas from "@kongken/geo-data/regions.json";
const provinceName = (code: string) => areas.find(p => p.code === code)?.name ?? code;

type Preview = Awaited<ReturnType<typeof regionApi.preview>>;
export default function RegionDialog({ open, onOpenChange, cloud }: { open: boolean; onOpenChange: (open: boolean) => void; cloud: Cloud }) {
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [retryNeeded, setRetryNeeded] = useState(false);
  const generation = useRef(0);
  const pending = useRef<{ version: string; revision: number; id: string } | null>(null);
  const userId = cloud.user?.id;
  const ready = cloud.canWrite && !cloud.pending && cloud.fresh && cloud.sync === "idle" && !cloud.busy;

  useEffect(() => {
    const id = ++generation.current;
    pending.current = null;
    if (!open || !userId) return;
    regionApi.preview().then(p => { if (id === generation.current) setPreview(p); })
      .catch(e => { if (id === generation.current) setError(describeApiError(e)); })
      .finally(() => { if (id === generation.current) setBusy(false); });
    return () => { generation.current = id + 1; };
  }, [open, userId]);

  const reload = async () => {
    const id = generation.current;
    setBusy(true); setError(""); setDone("");
    try { const p = await regionApi.preview(); if (id === generation.current) { setPreview(p); pending.current = null; setRetryNeeded(false); } }
    catch (e) { if (id === generation.current) setError(describeApiError(e)); }
    finally { if (id === generation.current) setBusy(false); }
  };
  const apply = async () => {
    if (!preview || !ready || busy || preview.blockers.length || !preview.changes.length) return;
    const id = generation.current;
    pending.current ??= { version: preview.version, revision: preview.revision, id: crypto.randomUUID() };
    const request = pending.current;
    setRetryNeeded(true);
    setBusy(true); setError("");
    try {
      const r = await regionApi.apply(request.version, request.revision, request.id);
      if (id !== generation.current) return;
      setDone(`已迁移 ${r.changed} 条区域记录${r.snapshotId ? "；迁移前的足迹已保存在历史版本" : ""}。旅行、颜色、独立省份标记与媒体均保留。`);
      setPreview(null); pending.current = null; setRetryNeeded(false);
      await cloud.retry();
    } catch (e) { if (id === generation.current) setError(describeApiError(e)); }
    finally { if (id === generation.current) setBusy(false); }
  };
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="migration-dialog region-dialog">
      <DialogHeader>
        <DialogTitle>区域数据与兼容</DialogTitle>
        <DialogDescription>先预览，再迁移云端旧区域。不会清空足迹，也不会更新已发布的分享。</DialogDescription>
      </DialogHeader>
      <p className="region-version">本页索引版本 <code title={REGION_VERSION}>{REGION_VERSION.slice(0, 23)}…</code></p>
      <p className="region-hint">地图仍随页面静态分发。旧备份与本机缓存按已审核映射读取；未知版本、未映射或碰撞记录保留原文并停止自动覆盖。</p>
      {busy && <p role="status">正在检查或迁移区域数据…</p>}
      {error && <p role="alert" className="account-error">{error}</p>}
      {done && <p role="status" className="account-sync">{done}</p>}
      {preview && <>
        <p className="region-hint">前后端索引一致 · 云端版本 {preview.revision}</p>
        {preview.blockers.length > 0 ? <div role="alert">
          <p>发现无法安全迁移的记录，没有修改任何内容。请导出备份并联系维护者。</p>
          <ul className="region-upgrade-list">{preview.blockers.map(b => <li key={b}>{b}</li>)}</ul>
        </div> : preview.changes.length === 0 ? <p role="status">云端区域数据已是当前版本，无需迁移。</p> : <>
          <p>将迁移 {preview.changes.length} 条区域记录，迁移前会自动保存历史版本：</p>
          <ul className="region-upgrade-list" aria-label="区域迁移预览">{preview.changes.map(c => <li key={c.fromCode}>
            <strong>{c.fromName}{c.mediaOnly ? "（仅保留媒体）" : ""}</strong><span>{provinceName(c.fromProvince)} / {c.fromCode} <ArrowRightIcon aria-hidden="true" /> {provinceName(c.toProvince)} / {c.toCode}</span>
            {c.fromName !== c.toName && <span>更名为 {c.toName}</span>}
          </li>)}</ul>
        </>}
      </>}
      {!ready && <p className="region-hint">请先完成同步、处理冲突或缓存异常，再确认迁移。</p>}
      <div className="region-actions">
        <Button variant="outline" disabled={busy} onClick={() => void reload()}><ArrowsClockwiseIcon aria-hidden="true" />重新预览</Button>
        <Button disabled={busy || !ready || !preview?.changes.length || !!preview?.blockers.length} onClick={() => void apply()}>{retryNeeded ? "重试确认迁移" : "确认迁移区域"}</Button>
      </div>
    </DialogContent>
  </Dialog>;
}
