import { useEffect, useMemo, useState } from "react";
import { Check, DownloadSimple, UploadSimple } from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { snapshotApi } from "@/api/snapshots";
import type { Cloud } from "@/hooks/useCloud";
import { describeApiError } from "@/lib/errors";
import { hasFootprints } from "@/lib/snapshots";
import {
  isNoop,
  migratedSaved,
  migrationFits,
  migrationOps,
  planMigration,
  sameSaved,
  summarize,
  type MigrationChoices,
  type MigrationConflict,
} from "@/lib/migration";
import type { Saved, Trip, Visit } from "@/lib/travel";
import { tripCount } from "@/lib/trips";
import { MAX_BATCH_OPS } from "@/lib/sync";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Footprints saved on this device. */
  local: Saved;
  /** The account's current footprints. */
  cloudSaved: Saved;
  cloud: Cloud;
  provinceName: (code: string) => string;
  /** Hands the migrated state to the sync engine; false if it was refused. */
  apply: (target: Saved) => boolean;
  onExportCloud: () => void;
  /** Called once the migration is confirmed by the server. */
  onMigrated: () => void;
  /** The user chose to decide later. */
  onLater: () => void;
};

const clip = (note: string) =>
  note.length > 24 ? `${note.slice(0, 24)}…` : note;
const describeTrip = (t: Trip) =>
  `${t.date || "无日期"}${t.note ? ` · ${clip(t.note)}` : ""}`;
const describeCity = (v: Visit) =>
  `${v.trips.length} 次旅行${v.trips[0] ? ` · 如 ${describeTrip(v.trips[0])}` : ""}`;

function ConflictRow({
  conflict,
  choice,
  onChoose,
  provinceName,
}: {
  conflict: MigrationConflict;
  choice: "local" | "cloud";
  onChoose: (choice: "local" | "cloud") => void;
  provinceName: (code: string) => string;
}) {
  const title =
    conflict.kind === "visit"
      ? conflict.local.name
      : conflict.kind === "trip"
        ? `${conflict.city} · 一次旅行`
        : `${provinceName(conflict.province)}（省份标记）`;
  const side = (which: "local" | "cloud", label: string) => {
    const dot =
      conflict.kind === "visit"
        ? conflict[which].color
        : conflict.kind === "mark"
          ? conflict[which]
          : undefined;
    return (
      <button
        type="button"
        aria-pressed={choice === which}
        className={`migration-choice${choice === which ? " selected" : ""}`}
        onClick={() => onChoose(which)}
      >
        <strong>
          {dot && (
            <span className="migration-dot" style={{ background: dot }} />
          )}
          {label}
        </strong>
        {conflict.kind === "visit" && (
          <small>{describeCity(conflict[which])}</small>
        )}
        {conflict.kind === "trip" && (
          <small>{describeTrip(conflict[which])}</small>
        )}
      </button>
    );
  };
  return (
    <li className="migration-conflict">
      <span className="migration-conflict-name">{title}</span>
      <div>
        {side("cloud", "用云端")}
        {side("local", "用本机")}
      </div>
    </li>
  );
}

export default function MigrationDialog({
  open,
  onOpenChange,
  local,
  cloudSaved,
  cloud,
  provinceName,
  apply,
  onExportCloud,
  onMigrated,
  onLater,
}: Props) {
  const [stage, setStage] = useState<"review" | "applying" | "done">("review");
  const [choices, setChoices] = useState<MigrationChoices>({});
  const [frozen, setFrozen] = useState(() => ({ local, cloud: cloudSaved }));
  const [error, setError] = useState("");
  // True while the pre-migration cloud snapshot is being saved.
  const [saving, setSaving] = useState(false);

  // The preview is fixed when the dialog opens, so it cannot shift under the
  // user while their own migration is being written.
  useEffect(() => {
    if (!open) return;
    setFrozen({ local, cloud: cloudSaved });
    setChoices({});
    setStage("review");
    setError("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const plan = useMemo(
    () => planMigration(frozen.local, frozen.cloud),
    [frozen],
  );
  const summary = summarize(plan, choices);
  // One migration is one atomic batch: it is only started when it fits in one.
  const opCount = useMemo(
    () => migrationOps(frozen.cloud, plan, choices),
    [frozen, plan, choices],
  );
  const tooLarge = !migrationFits(opCount);
  const choiceOf = (key: string) => choices[key] ?? "cloud";
  const ready =
    cloud.fresh && cloud.canWrite && !cloud.pending && cloud.sync === "idle";

  // Done once the engine has confirmed everything; a conflict is handled by
  // the page's own conflict dialog.
  useEffect(() => {
    if (stage !== "applying") return;
    if (cloud.sync === "conflict") {
      onOpenChange(false);
      return;
    }
    if (!cloud.pending && cloud.sync === "idle" && !cloud.loadFailed) {
      setStage("done");
      onMigrated();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stage, cloud.pending, cloud.sync, cloud.loadFailed]);

  // The cloud changed since the preview was made (another device): the plan is
  // rebuilt from the data now confirmed and has to be looked at again.
  const staleCloud = () => {
    const now = cloud.confirmedSaved();
    if (!now || sameSaved(frozen.cloud, now)) return false;
    setFrozen({ local: frozen.local, cloud: now });
    setChoices({});
    setError("云端数据刚刚发生了变化，已按最新数据重新生成预览，请重新核对后再确认迁移。");
    return true;
  };

  const confirm = async () => {
    setError("");
    if (tooLarge) {
      setError(
        `这次迁移需要 ${opCount} 项变更，超过一次原子迁移的上限 ${MAX_BATCH_OPS} 项。为避免只迁移一半，没有开始，本机和云端的数据都没有改动。`,
      );
      return;
    }
    if (staleCloud()) return;
    // Keep a restorable copy of the cloud data before anything is changed.
    if (hasFootprints(frozen.cloud)) {
      setSaving(true);
      try {
        await snapshotApi.create("beforeMigration");
      } catch (e) {
        setError(`无法先保存云端快照，未开始迁移：${describeApiError(e)}`);
        return;
      } finally {
        setSaving(false);
      }
    }
    if (staleCloud()) return;
    const target = migratedSaved(frozen.cloud, plan, choices);
    if (!apply(target)) {
      setError("无法开始迁移，请先处理页面上的提示后重试");
      return;
    }
    setStage("applying");
  };

  const bulk = (choice: "local" | "cloud") =>
    setChoices(Object.fromEntries(plan.conflicts.map((c) => [c.key, choice])));

  const close = (next: boolean) => {
    // While the write is in flight the dialog stays; if the connection is
    // down it may be closed, and the saved write keeps syncing like any edit.
    if (!next && stage === "applying" && !cloud.loadFailed) return;
    if (!next && stage !== "done") onLater();
    onOpenChange(next);
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="migration-dialog">
        <DialogHeader>
          <span className="dialog-icon">
            <UploadSimple size={22} />
          </span>
          <DialogTitle>把本机足迹迁移到云端</DialogTitle>
          <DialogDescription>
            本机有 {frozen.local.visits.length} 条城市足迹、{tripCount(frozen.local)}{" "}
            次旅行、
            {Object.keys(frozen.local.provinces).length}{" "}
            个省份标记。迁移只会新增或覆盖云端记录，不会删除云端已有内容；本机副本始终保留。云端已有足迹时，迁移前会先自动保存快照，可在“账号 → 历史版本”中恢复。
          </DialogDescription>
        </DialogHeader>

        {stage === "done" ? (
          <>
            <p className="account-sync" role="status">
              <Check size={16} />
              迁移完成：新增 {summary.added} 条（含 {summary.addedTrips} 次旅行），覆盖{" "}
              {summary.overwritten} 条，保留云端 {summary.keptCloud} 条。本机足迹仍保留，退出登录后可继续使用。
            </p>
            <div className="dialog-actions">
              <Button onClick={() => onOpenChange(false)}>完成</Button>
            </div>
          </>
        ) : isNoop(plan) ? (
          <>
            <p className="account-sync" role="status">
              <Check size={16} />
              本机足迹已经全部在云端，无需迁移。
            </p>
            <div className="dialog-actions">
              <Button
                onClick={() => {
                  onMigrated();
                  onOpenChange(false);
                }}
              >
                知道了
              </Button>
            </div>
          </>
        ) : (
          <>
            <ul className="migration-summary" aria-label="迁移预览">
              <li>
                <strong>{summary.added}</strong>
                <span>新增到云端</span>
              </li>
              <li>
                <strong>{summary.addedTrips}</strong>
                <span>新增旅行记录</span>
              </li>
              <li>
                <strong>{summary.overwritten}</strong>
                <span>用本机覆盖</span>
              </li>
              <li>
                <strong>{summary.keptCloud}</strong>
                <span>保留云端</span>
              </li>
              <li>
                <strong>{summary.same + summary.cloudOnly}</strong>
                <span>无需变动</span>
              </li>
            </ul>
            {plan.remapped.length > 0 && (
              <p className="unreadable-note" role="status">
                有 {plan.remapped.length}{" "}
                次旅行的编号已被云端另一座城市的旅行占用，上传时会改用新的编号（内容不变，全部保留）：
                {plan.remapped
                  .slice(0, 3)
                  .map((r) => `${r.city}`)
                  .join("、")}
                {plan.remapped.length > 3 ? " 等" : ""}。
              </p>
            )}
            {plan.conflicts.length > 0 && (
              <>
                <div className="migration-bulk">
                  <span>
                    {plan.conflicts.length} 条记录两边内容不同，逐条选择保留哪一份：
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={stage !== "review"}
                    onClick={() => bulk("cloud")}
                  >
                    全部用云端
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={stage !== "review"}
                    onClick={() => bulk("local")}
                  >
                    全部用本机
                  </Button>
                </div>
                <ul className="migration-conflicts">
                  {plan.conflicts.map((c) => (
                    <ConflictRow
                      key={c.key}
                      conflict={c}
                      choice={choiceOf(c.key)}
                      provinceName={provinceName}
                      onChoose={(choice) =>
                        stage === "review" &&
                        setChoices((prev) => ({ ...prev, [c.key]: choice }))
                      }
                    />
                  ))}
                </ul>
              </>
            )}
            {!ready && stage === "review" && (
              <p className="unreadable-note">
                {cloud.loadFailed
                  ? "云端未连接，暂时不能迁移，请稍后重试。"
                  : "正在确认云端最新数据，请稍候…"}
              </p>
            )}
            {stage === "applying" && !cloud.loadFailed && (
              <p className="account-sync" role="status">
                正在迁移，请不要关闭页面…
              </p>
            )}
            {tooLarge && stage === "review" && (
              <p className="unreadable-note unreadable-error" role="alert">
                这次迁移需要 {opCount} 项变更，超过一次原子迁移的上限 {MAX_BATCH_OPS} 项，无法开始。为避免只迁移一半，不会拆分发送；本机和云端的数据都不会改动。请先导出本机备份。
              </p>
            )}
            {(error || (stage === "applying" && cloud.loadFailed)) && (
              <p className="unreadable-note unreadable-error" role="alert">
                {error ||
                  (cloud.loadError.includes("仍完整保留在本机")
                    ? cloud.loadError
                    : "同步未完成，迁移内容已保存在本机，联网后会继续；也可以点击重试。")}
              </p>
            )}
            <div className="dialog-actions unreadable-actions">
              <Button
                variant="outline"
                onClick={onExportCloud}
                disabled={stage === "applying"}
              >
                <DownloadSimple size={16} />
                导出云端备份
              </Button>
              {stage === "applying" && cloud.loadFailed ? (
                <Button onClick={() => void cloud.retry()}>重试</Button>
              ) : (
                <>
                  <Button
                    variant="outline"
                    disabled={stage === "applying" || saving}
                    onClick={() => close(false)}
                  >
                    稍后再说
                  </Button>
                  <Button
                    disabled={!ready || stage === "applying" || saving || tooLarge}
                    onClick={() => void confirm()}
                  >
                    {saving ? "正在保存快照…" : "确认迁移"}
                  </Button>
                </>
              )}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
