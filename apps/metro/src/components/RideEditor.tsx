import { useRef, useState } from "react";
import type { Line } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { Button } from "@kongken/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import { Input } from "@kongken/ui/input";
import { FloppyDisk, Trash, X } from "@phosphor-icons/react";
import { describeError } from "@/lib/errors";
import { formatKm, parseYear } from "@/lib/format";
import { Alert, Field, LineBadge } from "./extras";

type RideTarget = Pick<Line, "code" | "name" | "color"> & Partial<Pick<Line, "cityName" | "lengthKm" | "stationNames">>;

/** One editor for city, topic and personal archives; the caller owns the saved snapshot. */
export function RideEditor({ line, year, ridden, onSave, onRemove, onClose }: {
  line: RideTarget;
  year?: number;
  ridden: boolean;
  onSave: (year: number | undefined) => Promise<void>;
  onRemove: () => Promise<void>;
  onClose: () => void;
}) {
  const [value, setValue] = useState(year ? String(year) : "");
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();
  const [error, setError] = useState<string>();
  const input = useRef<HTMLInputElement>(null);
  const opener = useRef(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  const [thisYear] = useState(() => new Date().getFullYear());
  const endpoints = line.stationNames?.length ? `${line.stationNames[0]} ⇄ ${line.stationNames.at(-1)}` : "";
  const act = async (operation: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    setError(undefined);
    try {
      await operation();
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <DialogContent
        showCloseButton={false}
        className="top-auto bottom-0 left-0 max-h-[90dvh] max-w-none translate-x-0 translate-y-0 overflow-y-auto overscroll-contain rounded-t-2xl rounded-b-none border-0 bg-surface-lowest p-5 pb-[max(1.25rem,env(safe-area-inset-bottom))] sm:top-1/2 sm:bottom-auto sm:left-1/2 sm:max-w-lg sm:-translate-x-1/2 sm:-translate-y-1/2 sm:rounded-xl sm:p-6"
        onOpenAutoFocus={(e) => { e.preventDefault(); input.current?.focus(); }}
        onCloseAutoFocus={(e) => {
          // A filtered list may remove the trigger after a save/removal.
          e.preventDefault();
          const trigger = opener.current;
          if (trigger?.isConnected && !trigger.matches(":disabled")) trigger.focus();
          else document.getElementById("main-content")?.focus();
        }}
        onEscapeKeyDown={(e) => { if (busy) e.preventDefault(); }}
        onPointerDownOutside={(e) => { if (busy) e.preventDefault(); }}
      >
        <DialogHeader className="pr-10 text-left">
          <DialogTitle className="font-headline text-xl font-bold text-ink">{ridden ? "编辑乘坐记录" : "标记乘坐记录"}</DialogTitle>
          <DialogDescription className="break-words">{[line.cityName, line.name].filter(Boolean).join(" · ")}{endpoints ? ` · ${endpoints}` : ""}</DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-3 rounded-lg bg-surface-low p-3 text-sm text-on-variant">
          <LineBadge color={line.color} label={line.name} />
          {line.lengthKm !== undefined ? <span>{formatKm(line.lengthKm)}</span> : null}
          <span className="ml-auto">{ridden ? "已坐过" : "未坐过"}</span>
        </div>
        {error ? <Alert>{error}</Alert> : null}
        <form noValidate aria-busy={busy} onSubmit={(e) => {
          e.preventDefault();
          const parsed = parseYear(value);
          if (Number.isNaN(parsed)) {
            setProblem(`填写 1900 到 ${thisYear} 之间的整数年份，或留空。`);
            input.current?.focus();
            return;
          }
          setProblem(undefined);
          void act(() => onSave(parsed));
        }} className="grid gap-4">
          <Field label="乘坐年份（可选）" hint={`1900—${thisYear}；留空表示不记录具体年份。`} error={problem}>
            {(a) => <Input {...a} ref={input} inputMode="numeric" value={value} placeholder="例如：2019" disabled={busy} className="h-11 bg-surface-low text-base" onChange={(e) => setValue(e.target.value)} />}
          </Field>
          <div className="flex flex-wrap gap-2" role="group" aria-label="快捷填写年份">
            {[thisYear, thisYear - 1].map((y) => <Button key={y} type="button" variant="outline" className="h-10 rounded-full" disabled={busy} onClick={() => { setValue(String(y)); setProblem(undefined); }}>{y === thisYear ? `今年 ${y}` : y}</Button>)}
            <Button type="button" variant="outline" className="h-10 rounded-full" disabled={busy} onClick={() => { setValue(""); setProblem(undefined); }}>清空年份</Button>
          </div>
          <Button type="submit" className="h-12 rounded-lg" disabled={busy}><FloppyDisk aria-hidden />{busy ? "正在处理…" : "保存记录"}</Button>
          <div className="flex flex-wrap items-center justify-between gap-2">
            {ridden ? <Button type="button" variant="ghost" className="min-h-11 text-destructive" disabled={busy} onClick={() => void act(onRemove)}><Trash aria-hidden />移除此线路足迹</Button> : <span />}
            <Button type="button" variant="outline" className="min-h-11" disabled={busy} onClick={onClose}>取消</Button>
          </div>
        </form>
        <Button type="button" variant="ghost" className="absolute top-2 right-2 size-11" aria-label="关闭" disabled={busy} onClick={onClose}><X aria-hidden /></Button>
      </DialogContent>
    </Dialog>
  );
}
