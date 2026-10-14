import { useCallback, useEffect, useRef, useState } from "react";
import {
  ArrowCounterClockwise,
  Check,
  ClockCounterClockwise,
  FloppyDisk,
} from "@phosphor-icons/react";
import { Code, ConnectError } from "@connectrpc/connect";
import { snapshotApi } from "@/api/snapshots";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Cloud } from "@/hooks/useCloud";
import { describeApiError } from "@/lib/errors";
import {
  planChanges,
  planRestore,
  reasonLabel,
  type RestorePlan,
  type SnapshotInfo,
} from "@/lib/snapshots";
import type { Saved } from "@/lib/travel";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  cloud: Cloud;
  /** The account's current footprints, as shown on the map. */
  current: Saved;
};

type Preview = {
  snapshot: SnapshotInfo;
  plan: RestorePlan;
  /** Revision the preview was computed against; the server verifies it. */
  baseRevision: number;
};

type Done = { changed: number; undo: SnapshotInfo | null };

const when = (ms: number) =>
  new Date(ms).toLocaleString("zh-CN", { hour12: false });

const counts = (s: SnapshotInfo) =>
  `${s.visitCount} 个城市足迹、${s.tripCount} 次旅行、${s.markCount} 个省份标记`;

export default function SnapshotDialog({
  open,
  onOpenChange,
  cloud,
  current,
}: Props) {
  const [snapshots, setSnapshots] = useState<SnapshotInfo[] | null>(null);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState("");
  const [preview, setPreview] = useState<Preview | null>(null);
  // After a restore the dialog waits until this revision has been loaded.
  const [syncing, setSyncing] = useState<{ revision: number; done: Done } | null>(
    null,
  );
  const [done, setDone] = useState<Done | null>(null);
  const attempt = useRef<{ key: string; id: string } | null>(null);

  const ready =
    cloud.fresh && cloud.canWrite && !cloud.pending && cloud.sync === "idle";

  const load = useCallback(async () => {
    try {
      setSnapshots(await snapshotApi.list());
      setError("");
    } catch (e) {
      setSnapshots((prev) => prev ?? []);
      setError(describeApiError(e));
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    setPreview(null);
    setDone(null);
    setSyncing(null);
    setNote("");
    setError("");
    attempt.current = null;
    void load();
  }, [open, load]);

  // Finished once the sync engine has caught up with the restored revision.
  const { revision } = cloud;
  useEffect(() => {
    if (!syncing) return;
    if (cloud.sync === "idle" && !cloud.pending && revision() === syncing.revision) {
      setDone(syncing.done);
      setSyncing(null);
      void load();
    }
  }, [syncing, cloud.sync, cloud.pending, revision, load]);

  const run = async (label: string, action: () => Promise<void>) => {
    setBusy(label);
    setError("");
    setNote("");
    try {
      await action();
    } catch (e) {
      if (e instanceof ConnectError && e.code === Code.Aborted) {
        setError("云端数据在预览后发生了变化，没有恢复任何内容。请重新选择快照。");
        setPreview(null);
        void cloud.retry();
      } else if (
        e instanceof ConnectError &&
        e.code === Code.FailedPrecondition
      ) {
        setError("该快照包含当前行政区划不再支持的区域，无法恢复。");
      } else if (e instanceof ConnectError && e.code === Code.NotFound) {
        setError("该快照已过期或不存在。");
        void load();
      } else {
        setError(describeApiError(e));
      }
    } finally {
      setBusy("");
    }
  };

  const save = () =>
    run("saving", async () => {
      const { created } = await snapshotApi.create("manual");
      await load();
      setNote(
        created
          ? "已保存当前版本的快照。"
          : "当前内容已有相同的快照，无需重复保存。",
      );
    });

  const choose = (snapshot: SnapshotInfo) =>
    run("previewing", async () => {
      const baseRevision = cloud.revision();
      if (baseRevision === null) throw new Error("not loaded");
      const { saved } = await snapshotApi.get(snapshot.id);
      setDone(null);
      setPreview({
        snapshot,
        plan: planRestore(current, saved),
        baseRevision,
      });
    });

  const confirm = (target: Preview) =>
    run("restoring", async () => {
      // A retry of this exact restore keeps its ID, so a lost reply is not applied twice.
      const key = `${target.snapshot.id}:${target.baseRevision}`;
      if (attempt.current?.key !== key)
        attempt.current = { key, id: crypto.randomUUID() };
      const result = await snapshotApi.restore(
        target.snapshot.id,
        target.baseRevision,
        attempt.current.id,
      );
      attempt.current = null;
      setPreview(null);
      setSyncing({
        revision: result.revision,
        done: { changed: result.changedCount, undo: result.undo },
      });
      void cloud.retry();
    });

  const waiting = !!syncing && !cloud.loadFailed;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="migration-dialog snapshot-dialog">
        <DialogHeader>
          <span className="dialog-icon">
            <ClockCounterClockwise size={22} />
          </span>
          <DialogTitle>云端备份与历史版本</DialogTitle>
          <DialogDescription>
            云端会在迁移前、恢复前和每天有改动时自动保存快照，也可以随时手动保存。误删或覆盖后，可以把足迹恢复到任一快照。
          </DialogDescription>
        </DialogHeader>

        {preview ? (
          <>
            <p className="unreadable-note">
              将恢复到 {when(preview.snapshot.createdAt)} 的快照（
              {reasonLabel[preview.snapshot.reason]}，{counts(preview.snapshot)}
              ）。恢复前会先自动保存现在的版本，需要时可以撤销本次恢复。
            </p>
            {planChanges(preview.plan) === 0 ? (
              <p className="account-sync" role="status">
                <Check size={16} />
                当前足迹与此快照完全一致，无需恢复。
              </p>
            ) : (
              <ul className="migration-summary" aria-label="恢复预览">
                <li>
                  <strong>
                    {preview.plan.addedVisits + preview.plan.addedMarks}
                  </strong>
                  <span>找回</span>
                </li>
                <li>
                  <strong>
                    {preview.plan.changedVisits + preview.plan.changedMarks}
                  </strong>
                  <span>改回快照内容</span>
                </li>
                <li>
                  <strong>
                    {preview.plan.removedVisits + preview.plan.removedMarks}
                  </strong>
                  <span>移除快照之后新增</span>
                </li>
                <li>
                  <strong>{preview.plan.unchanged}</strong>
                  <span>无需变动</span>
                </li>
              </ul>
            )}
            {preview.plan.addedTrips +
              preview.plan.changedTrips +
              preview.plan.removedTrips >
              0 && (
              <p className="migration-trips" role="status">
                其中旅行记录：找回 {preview.plan.addedTrips} 次、改回{" "}
                {preview.plan.changedTrips} 次、移除 {preview.plan.removedTrips}{" "}
                次（含被找回或移除的城市里的旅行）。
              </p>
            )}
            {!ready && (
              <p className="unreadable-note">
                {cloud.loadFailed
                  ? "云端未连接，暂时不能恢复，请稍后重试。"
                  : "正在确认云端最新数据，请稍候…"}
              </p>
            )}
            {error && (
              <p className="unreadable-note unreadable-error" role="alert">
                {error}
              </p>
            )}
            <div className="dialog-actions">
              <Button
                variant="outline"
                disabled={!!busy}
                onClick={() => {
                  setPreview(null);
                  setError("");
                }}
              >
                返回
              </Button>
              <Button
                disabled={!ready || !!busy || planChanges(preview.plan) === 0}
                onClick={() => void confirm(preview)}
              >
                <ArrowCounterClockwise size={16} />
                {busy === "restoring" ? "正在恢复…" : "确认恢复"}
              </Button>
            </div>
          </>
        ) : (
          <>
            {syncing && (
              <p className="account-sync" role="status">
                {waiting ? "已恢复，正在同步…" : "恢复已完成，同步未完成，请重试。"}
                {!waiting && (
                  <Button size="sm" onClick={() => void cloud.retry()}>
                    重试
                  </Button>
                )}
              </p>
            )}
            {done && (
              <div className="account-sync snapshot-done" role="status">
                <Check size={16} />
                <span>
                  {done.changed > 0
                    ? `已恢复，共调整 ${done.changed} 条记录。`
                    : "当前足迹已与快照一致，没有改动。"}
                </span>
                {done.changed > 0 && done.undo && (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!ready || !!busy}
                    onClick={() => {
                      const undo = done.undo;
                      if (undo) void choose(undo);
                    }}
                  >
                    撤销本次恢复
                  </Button>
                )}
              </div>
            )}
            {note && (
              <p className="account-sync" role="status">
                <Check size={16} />
                {note}
              </p>
            )}
            {cloud.pending && (
              <p className="unreadable-note">
                有尚未同步的修改，快照只包含已同步到云端的内容；同步完成后才能恢复。
              </p>
            )}
            {error && (
              <p className="unreadable-note unreadable-error" role="alert">
                {error}
              </p>
            )}
            <ul className="snapshot-list" aria-label="快照列表">
              {snapshots === null && <li className="snapshot-empty">加载中…</li>}
              {snapshots?.length === 0 && (
                <li className="snapshot-empty">还没有快照。</li>
              )}
              {snapshots?.map((s) => (
                <li key={s.id}>
                  <span>
                    <strong>{when(s.createdAt)}</strong>
                    <small>
                      {reasonLabel[s.reason]} · {counts(s)}
                    </small>
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!!busy || !!syncing || cloud.revision() === null}
                    onClick={() => void choose(s)}
                  >
                    预览并恢复
                  </Button>
                </li>
              ))}
            </ul>
            <p className="snapshot-retention">
              快照保留 30 天（至少保留最新 10 个，最多 50 个），不可修改；注销账号时一并删除。
            </p>
            <div className="dialog-actions">
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                关闭
              </Button>
              <Button disabled={!!busy || !!syncing} onClick={() => void save()}>
                <FloppyDisk size={16} />
                {busy === "saving" ? "正在保存…" : "立即保存快照"}
              </Button>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
