import { useState } from "react";
import { DownloadSimple, Warning } from "@phosphor-icons/react";
import { Button } from "@kongken/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import type { Theirs } from "../hooks/useWorldLocal.ts";
import type { ImportDelta, WorldImportPreview, WorldRecovery } from "../lib/world-persistence.ts";
import { worldTripCount, type WorldDocument, type WorldPlace } from "../lib/world.ts";

// The dialogs that stand between the user and their data: unreadable stored text, another
// tab's change, deleting a city, and importing a file. They are styled like the F0 dialogs
// (LegacyDialog): nothing is replaced before the original can be exported, and every
// destructive step names what it will do.

const summary = (world: WorldDocument) =>
  `${world.countries.length} 个国家或地区标记、${world.places.length} 座城市、${worldTripCount(world)} 次旅行`;

type UnreadableProps = {
  open: boolean;
  reason: string;
  recovery: WorldRecovery | null;
  /** The browser could not keep a copy of the original: it must be exported first. */
  copyFailed: boolean;
  exported: boolean;
  onExport: () => void;
  onRecover: () => void;
  onEmpty: () => void;
  onClose: () => void;
};

export function UnreadableDialog({ open, reason, recovery, copyFailed, exported, onExport, onRecover, onEmpty, onClose }: UnreadableProps) {
  const [confirming, setConfirming] = useState(false);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setConfirming(false);
          onClose();
        }
      }}
    >
      <DialogContent className="world-protect-dialog">
        <DialogHeader>
          <span className="dialog-icon">
            <Warning size={25} weight="duotone" />
          </span>
          <DialogTitle>世界足迹无法读取</DialogTitle>
          <DialogDescription>
            这台设备上保存的世界足迹无法读取{reason ? `（${reason}）` : ""}。原文原样保留，没有被改动；你选择之前不会保存任何新的修改。
          </DialogDescription>
        </DialogHeader>
        {recovery ? (
          <p className="unreadable-note" role="status" data-testid="world-recovery-summary">
            可以恢复 {summary(recovery.document)}。无法识别而被丢弃的有 {recovery.discardedCountries} 个标记、{recovery.discardedPlaces} 座城市、
            {recovery.discardedTrips} 次旅行。恢复前，原文会先保留为浏览器内的副本。
          </p>
        ) : (
          <p className="unreadable-note" role="status" data-testid="world-recovery-summary">
            没有可以识别并安全恢复的记录（例如版本不明、内容损坏过多）。可以导出原文留存，或以空数据重新开始。
          </p>
        )}
        {copyFailed && (
          <p className="world-form-error" role="alert">
            浏览器没能在本机保留原文的副本。请先导出原文，导出之后才能继续{exported ? "：已导出，请再点一次你的选择" : ""}。
          </p>
        )}
        {confirming && (
          <p className="world-note" role="alert">
            以空数据重新开始，会用空白世界足迹替换这份无法读取的数据。原文会先保留为浏览器内的副本；建议先导出原文。再点一次“确认以空数据重新开始”才会执行。
          </p>
        )}
        <div className="dialog-actions unreadable-actions">
          <Button variant="outline" onClick={onExport}>
            <DownloadSimple size={17} />
            导出原文
          </Button>
          {recovery && <Button onClick={onRecover}>恢复可识别的记录</Button>}
          {confirming ? (
            <Button
              variant="destructive"
              onClick={() => {
                setConfirming(false);
                onEmpty();
              }}
            >
              确认以空数据重新开始
            </Button>
          ) : (
            <Button variant="outline" onClick={() => setConfirming(true)}>
              以空数据重新开始
            </Button>
          )}
          <Button variant="ghost" onClick={onClose}>
            稍后处理
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

type ConflictProps = {
  open: boolean;
  theirs: Theirs;
  mine: WorldDocument;
  copyFailed: boolean;
  exported: boolean;
  onExportMine: () => void;
  onExportTheirs: () => void;
  onKeepMine: () => void;
  onLoadTheirs: () => void;
  onClose: () => void;
  cloud?: boolean;
};

export function ConflictDialog({ open, theirs, mine, copyFailed, exported, onExportMine, onExportTheirs, onKeepMine, onLoadTheirs, onClose, cloud = false }: ConflictProps) {
  const theirsText = theirs.text === null ? "已被清空" : theirs.document ? summary(theirs.document) : "内容无法识别，只能导出原文";
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="world-protect-dialog">
        <DialogHeader>
          <span className="dialog-icon">
            <Warning size={25} weight="duotone" />
          </span>
          <DialogTitle>{cloud ? "云端世界足迹发生冲突" : "其他标签页修改了世界足迹"}</DialogTitle>
          <DialogDescription>
            {cloud
              ? "另一台设备已经保存了不同的世界足迹。为避免静默覆盖，本机修改已暂停。请先导出两边的数据，再选择保留哪一份。"
              : "另一个标签页保存了不同的世界足迹。为避免覆盖对方的修改，本页已暂停保存。建议先导出两边的数据，再选择保留哪一份。"}
          </DialogDescription>
        </DialogHeader>
        <p className="unreadable-note" role="status" data-testid="world-conflict-summary">
            {cloud ? "本机数据" : "本页现有"}：{summary(mine)}。{cloud ? "云端数据" : "另一个标签页"}：{theirsText}。
        </p>
        {copyFailed && (
          <p className="world-form-error" role="alert">
            浏览器没能在本机保留对方的数据副本。请先导出另一个标签页的数据，导出之后才能用本页数据覆盖{exported ? "：已导出，请再点一次" : ""}。
          </p>
        )}
        <div className="dialog-actions unreadable-actions">
          <Button variant="outline" onClick={onExportMine}>
            <DownloadSimple size={17} />
            导出本页数据
          </Button>
          <Button variant="outline" onClick={onExportTheirs}>
            <DownloadSimple size={17} />
            {cloud ? "导出云端数据" : "导出另一个标签页的数据"}
          </Button>
          <Button onClick={onKeepMine}>{cloud ? "冲突以本机为准" : "用本页数据覆盖（对方数据保留为副本）"}</Button>
          <Button variant="outline" onClick={onLoadTheirs} disabled={!theirs.document}>
            {cloud ? "冲突以云端为准" : "载入另一个标签页的数据（本页未保存的修改将放弃）"}
          </Button>
          <Button variant="ghost" onClick={onClose}>
            稍后处理
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

type DeleteProps = {
  place: WorldPlace | null;
  countryName: string;
  /** The country or region is lit only by this city, so it goes dark. */
  darkens: boolean;
  onConfirm: () => void;
  onCancel: () => void;
};

/** The second confirmation of deleting a city: nothing is deleted before it. */
export function DeletePlaceDialog({ place, countryName, darkens, onConfirm, onCancel }: DeleteProps) {
  return (
    <Dialog open={place !== null} onOpenChange={(next) => !next && onCancel()}>
      <DialogContent className="world-protect-dialog">
        <DialogHeader>
          <DialogTitle>确定删除城市“{place?.name}”？</DialogTitle>
          <DialogDescription>
            这座城市的 {place?.trips.length ?? 0} 次旅行记录会一并删除，无法恢复。{countryName}的国家/地区标记不受影响
            {darkens ? `；${countryName}目前只因这座城市而点亮，删除后会变回未点亮` : ""}。
          </DialogDescription>
        </DialogHeader>
        <div className="dialog-actions">
          <Button variant="outline" onClick={onCancel}>
            保留城市
          </Button>
          <Button variant="destructive" onClick={onConfirm}>
            确认删除
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

const ROWS: [keyof Pick<WorldImportPreview, "countries" | "places" | "trips">, string][] = [
  ["countries", "国家或地区标记"],
  ["places", "海外城市"],
  ["trips", "旅行"],
];
const COLUMNS: [keyof ImportDelta, string][] = [
  ["added", "新增"],
  ["overwritten", "覆盖"],
  ["unchanged", "不变"],
  ["discarded", "丢弃"],
];

type ImportProps = {
  preview: WorldImportPreview | null;
  fileName: string;
  onExportCurrent: () => void;
  onConfirm: () => void;
  onCancel: () => void;
};

/** The preview of an import: it shows what will change and writes nothing until confirmed. */
export function ImportDialog({ preview, fileName, onExportCurrent, onConfirm, onCancel }: ImportProps) {
  return (
    <Dialog open={preview !== null} onOpenChange={(next) => !next && onCancel()}>
      <DialogContent className="world-protect-dialog">
        <DialogHeader>
          <DialogTitle>导入世界足迹备份</DialogTitle>
          <DialogDescription>
            “{fileName}”通过了检查。导入会用文件内容整体替换当前世界足迹，确认之前不会改动任何数据。
          </DialogDescription>
        </DialogHeader>
        {preview && (
          <>
            <table className="world-import-table" data-testid="world-import-preview">
              <caption className="sr-only">导入预览：新增、覆盖、不变、丢弃的数量</caption>
              <thead>
                <tr>
                  <th scope="col">记录</th>
                  {COLUMNS.map(([, label]) => (
                    <th key={label} scope="col">
                      {label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {ROWS.map(([key, label]) => (
                  <tr key={key} data-row={key}>
                    <th scope="row">{label}</th>
                    {COLUMNS.map(([column, name]) => (
                      <td key={column} data-column={column} aria-label={`${label}${name}`}>
                        {preview[key][column]}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="world-hint">
              新增：文件里有、当前没有。覆盖：两边都有但内容不同，以文件为准。丢弃：当前有、文件里没有，导入后不再保留。想保留当前数据，请先导出。
            </p>
          </>
        )}
        <div className="dialog-actions unreadable-actions">
          <Button variant="outline" onClick={onExportCurrent}>
            <DownloadSimple size={17} />
            先导出当前世界足迹
          </Button>
          <Button variant="outline" onClick={onCancel}>
            取消导入
          </Button>
          <Button onClick={onConfirm}>确认导入</Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
