import { useMemo } from "react";
import { DownloadSimple, Warning } from "@phosphor-icons/react";
import { Button } from "@trip-map/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@trip-map/ui/dialog";
import { planMigration, summarize } from "@/lib/migration";
import type { Saved } from "@/lib/travel";
import { tripCount } from "@/lib/trips";

type Props = {
  open: boolean;
  /** Whether the data is this device's footprints or the account's cached copy. */
  kind: "local" | "cloud";
  /** What the older page stored, exactly as stored. */
  raw: string;
  /** Its footprints, or null when they cannot be read (then only export is offered). */
  saved: Saved | null;
  /** What this page holds now. */
  current: Saved;
  onExport: () => void;
  /** Adds what is new; false when it could not be done. */
  onMerge: () => boolean;
  /** Leave it where it is and stop asking about this state. */
  onKeep: () => void;
  /** Decide later: the data stays untouched and is offered again on the next start. */
  onClose: () => void;
};

/**
 * An older page of the app is still open and wrote to the key it knows. That key
 * is not this version's, so nothing is overwritten, but its changes are not in this
 * page either: they are shown here and handled only when the user chooses.
 */
export default function LegacyDialog({
  open,
  kind,
  raw,
  saved,
  current,
  onExport,
  onMerge,
  onKeep,
  onClose,
}: Props) {
  const plan = useMemo(
    () => (saved ? planMigration(saved, current) : null),
    [saved, current],
  );
  const summary = plan ? summarize(plan, {}) : null;
  const subject = kind === "local" ? "本机足迹" : "账号缓存";
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent>
        <DialogHeader>
          <span className="dialog-icon">
            <Warning size={25} weight="duotone" />
          </span>
          <DialogTitle>旧版页面修改了{subject}</DialogTitle>
          <DialogDescription>
            另一个打开着的旧版本页面在它自己的存储位置写入了数据。当前版本的数据没有被覆盖，旧版页面里的改动也不在本页中。
            {saved
              ? `旧数据里有 ${saved.visits.length} 条城市足迹、${tripCount(saved)} 次旅行。`
              : "旧数据无法识别，只能导出原始内容。"}
          </DialogDescription>
        </DialogHeader>
        {summary && (
          <p className="unreadable-note" role="status">
            合并只会新增本页没有的内容：新增 {summary.added} 条（含{" "}
            {summary.addedTrips} 次旅行）；两边都有但内容不同的{" "}
            {summary.keptCloud} 条保留当前内容；不会删除任何记录。
            {summary.added === 0 && summary.keptCloud === 0
              ? "旧数据里没有需要合并的内容。"
              : ""}
          </p>
        )}
        <p className="unreadable-note">
          不合并时，旧数据继续保存在浏览器的旧位置，不会被清除；也可以先导出再决定。
        </p>
        <div className="dialog-actions unreadable-actions">
          <Button
            variant="outline"
            onClick={onExport}
            disabled={raw === ""}
          >
            <DownloadSimple size={16} />
            导出旧版页面数据
          </Button>
          <Button variant="outline" onClick={onKeep}>
            不合并，保留在旧位置
          </Button>
          <Button
            disabled={!saved}
            onClick={() => {
              onMerge();
            }}
          >
            合并新增内容
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
