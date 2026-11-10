import { useMemo, useState } from "react";
import { Paperclip, SlidersHorizontal, WarningCircle, X } from "@phosphor-icons/react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import { Input } from "@kongken/ui/input";
import { amountPlaceholder, currencyDigits, timezoneLabel, validateDraft, type DraftEntry, type DraftErrors } from "@/lib/entries.ts";
import { cn } from "@kongken/ui/utils";

const CURRENCIES = ["CNY", "USD", "EUR", "JPY", "GBP", "HKD", "SGD", "TWD"];

const COMMON_ZONES = [
  "Asia/Shanghai",
  "Asia/Hong_Kong",
  "Asia/Taipei",
  "Asia/Tokyo",
  "Asia/Singapore",
  "Europe/London",
  "Europe/Paris",
  "America/New_York",
  "America/Los_Angeles",
  "Australia/Sydney",
  "UTC",
];

type ComposerProps = {
  open: boolean;
  initial: DraftEntry;
  categories: string[];
  saving: boolean;
  error: string | null;
  onClose: () => void;
  onSubmit: (draft: DraftEntry, files: File[]) => void;
  uploadStatus?: Record<string, { progress: number; error?: string }>;
};

/**
 * 记一笔: the design's right-hand drawer. Title, date and time with the record's own time zone, category,
 * place, amount and currency, note — the fields the records API actually has. The design's attachment drop zone
 * is drawn as it is but stays disabled: the records service has no attachment RPC yet, so a file dropped here
 * would have nowhere to go (docs/timeline/features.md).
 */
export function Composer({ open, initial, categories, saving, error, onClose, onSubmit, uploadStatus = {} }: ComposerProps) {
  const [draft, setDraft] = useState<DraftEntry>(initial);
  const [errors, setErrors] = useState<DraftErrors>({});
  const [zoneEditing, setZoneEditing] = useState(false);
  const [files, setFiles] = useState<File[]>([]);

  const zone = useMemo(() => timezoneLabel(draft.timezone), [draft.timezone]);
  const digits = currencyDigits(draft.currency);
  const set = (patch: Partial<DraftEntry>) => {
    setDraft((current) => ({ ...current, ...patch }));
    setErrors((current) => {
      const next = { ...current };
      for (const key of Object.keys(patch) as Array<keyof DraftEntry>) delete next[key];
      return next;
    });
  };

  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    const checked = validateDraft(draft);
    if (checked.ok === false) {
      setErrors(checked.errors);
      return;
    }
    const invalid = files.find((file) => !["image/jpeg", "image/png", "image/webp", "application/pdf"].includes(file.type) || file.size > 10 * 1024 * 1024);
    if (invalid !== undefined || files.length > 20) {
      setErrors((current) => ({ ...current, note: "附件必须是 JPEG、PNG、WebP 或 PDF，单个不超过 10 MiB，最多 20 个。" }));
      return;
    }
    onSubmit(draft, files);
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent
        showCloseButton={false}
        className="top-0 right-0 left-auto flex h-full max-h-none w-full max-w-[420px] translate-x-0 translate-y-0 flex-col gap-0 rounded-none rounded-l-[10px] border-0 border-l border-outline-variant bg-card p-0 shadow-[-12px_0_40px_rgba(24,61,27,0.12)] sm:max-w-[420px] data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right"
      >
        <DialogHeader className="flex-row items-center justify-between border-b border-outline-variant/60 px-5 py-4">
          <div>
            <DialogTitle className="text-base font-semibold text-card-foreground">记一笔</DialogTitle>
            <p className="mt-0.5 text-[11px] text-muted-foreground">写下已经发生的小事，保存后进入时间线</p>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭" className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-muted">
            <X size={16} />
          </button>
        </DialogHeader>

        <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col">
          <div className="flex-1 overflow-y-auto px-5 py-4">
            <div className="flex flex-col gap-4">
              <Field label="今天发生了什么？" required error={errors.title}>
                <Input
                  value={draft.title}
                  onChange={(event) => set({ title: event.target.value })}
                  placeholder="和朋友吃午饭"
                  maxLength={200}
                  autoFocus
                  aria-invalid={errors.title !== undefined}
                  className="border-outline-variant bg-white text-sm text-card-foreground"
                />
              </Field>

              <div className="grid grid-cols-2 gap-3">
                <Field label="日期" required error={errors.date}>
                  <Input
                    type="date"
                    value={draft.date}
                    onChange={(event) => set({ date: event.target.value })}
                    aria-invalid={errors.date !== undefined}
                    className="border-outline-variant bg-white text-sm text-card-foreground"
                  />
                </Field>
                <Field label="时间（可留空）" error={errors.time}>
                  <Input
                    type="time"
                    value={draft.time}
                    onChange={(event) => set({ time: event.target.value })}
                    aria-invalid={errors.time !== undefined}
                    className="border-outline-variant bg-white text-sm text-card-foreground"
                  />
                </Field>
              </div>

              <Field label="时区" required error={errors.timezone}>
                <div className="flex items-center justify-between gap-2 rounded-md border border-outline-variant bg-muted px-3 py-2 text-sm text-card-foreground">
                  <span className="tabular truncate">{zone}</span>
                  <button
                    type="button"
                    onClick={() => setZoneEditing((editing) => !editing)}
                    className="inline-flex shrink-0 items-center gap-1 text-xs font-medium text-primary hover:underline"
                  >
                    <SlidersHorizontal size={12} />
                    {zoneEditing ? "收起" : "修改"}
                  </button>
                </div>
                {zoneEditing && (
                  <select
                    value={COMMON_ZONES.includes(draft.timezone) ? draft.timezone : ""}
                    onChange={(event) => {
                      if (event.target.value !== "") set({ timezone: event.target.value });
                    }}
                    aria-label="选择时区"
                    className="mt-2 h-9 w-full rounded-md border border-outline-variant bg-white px-2.5 text-sm text-card-foreground"
                  >
                    <option value="">选择常用时区（当前：{draft.timezone}）</option>
                    {COMMON_ZONES.map((item) => (
                      <option key={item} value={item}>
                        {timezoneLabel(item)}
                      </option>
                    ))}
                  </select>
                )}
              </Field>

              <Field label="分类" error={errors.category}>
                <Input
                  value={draft.category}
                  onChange={(event) => set({ category: event.target.value })}
                  list="timeline-categories"
                  placeholder="其他"
                  maxLength={100}
                  className="border-outline-variant bg-white text-sm text-card-foreground"
                />
                <datalist id="timeline-categories">
                  {[...new Set(["外出就餐", "理发", "宠物开销", "运动", "清洁", "体检", "其他", ...categories])].map((category) => (
                    <option key={category} value={category} />
                  ))}
                </datalist>
              </Field>

              <Field label="地点／店名（可选）" error={errors.location}>
                <Input
                  value={draft.location}
                  onChange={(event) => set({ location: event.target.value })}
                  placeholder="巷口小馆"
                  maxLength={300}
                  className="border-outline-variant bg-white text-sm text-card-foreground"
                />
              </Field>

              <Field label="金额与币种（可选）" error={errors.amount ?? errors.currency}>
                <div className="flex gap-2">
                  <Input
                    value={draft.amount}
                    onChange={(event) => set({ amount: event.target.value })}
                    inputMode={digits === 0 ? "numeric" : "decimal"}
                    placeholder={amountPlaceholder(draft.currency)}
                    aria-invalid={errors.amount !== undefined}
                    className="tabular flex-1 border-outline-variant bg-white text-sm text-card-foreground"
                  />
                  <select
                    value={draft.currency}
                    onChange={(event) => set({ currency: event.target.value })}
                    aria-label="币种"
                    className="h-9 w-[92px] rounded-md border border-outline-variant bg-white px-2 text-sm text-card-foreground"
                  >
                    {CURRENCIES.map((currency) => (
                      <option key={currency} value={currency}>
                        {currency}
                      </option>
                    ))}
                  </select>
                </div>
              </Field>

              <Field label="备注（可选）" error={errors.note}>
                <textarea
                  value={draft.note}
                  onChange={(event) => set({ note: event.target.value })}
                  rows={3}
                  maxLength={10000}
                  placeholder="想留下的细节"
                  className="w-full resize-y rounded-md border border-outline-variant bg-white px-3 py-2 text-sm text-card-foreground outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/40"
                />
              </Field>

              <div>
                <p className="mb-2 text-xs font-medium text-muted-foreground">附件</p>
                <label
                  htmlFor="timeline-attachments"
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={(event) => {
                    event.preventDefault();
                    setFiles((current) => [...current, ...Array.from(event.dataTransfer.files)].slice(0, 20));
                  }}
                  className="flex cursor-pointer flex-col items-center gap-1.5 rounded-md border border-dashed border-outline-variant bg-muted/60 px-4 py-6 text-center hover:border-primary"
                >
                  <Paperclip size={18} className="text-primary" />
                  <p className="text-xs text-muted-foreground">拖拽或选择图片 / PDF，单个不超过 10 MiB，最多 20 个</p>
                  <input id="timeline-attachments" type="file" multiple accept="image/jpeg,image/png,image/webp,application/pdf" className="sr-only" onChange={(event) => setFiles((current) => [...current, ...Array.from(event.target.files ?? [])].slice(0, 20))} />
                </label>
                {files.length > 0 && <ul className="mt-2 space-y-1 text-xs text-muted-foreground">{files.map((file, index) => { const status = uploadStatus[file.name]; return <li key={`${file.name}-${index}`} className="flex items-center gap-2"><span className="min-w-0 flex-1 truncate">{file.name}</span><span>{status?.error ?? (status === undefined ? `${Math.ceil(file.size / 1024)} KiB` : `${status.progress}%`)}</span></li>; })}</ul>}
              </div>
            </div>
          </div>

          <div className="border-t border-outline-variant/60 px-5 py-3.5">
            {error !== null && (
              <p className="mb-2.5 flex items-start gap-1.5 rounded-md bg-error-container px-2.5 py-2 text-xs text-on-error-container">
                <WarningCircle size={14} className="mt-px shrink-0" />
                {error}
              </p>
            )}
            <div className="flex items-center justify-end gap-2">
              <button
                type="button"
                onClick={onClose}
                className="rounded-md bg-muted px-4 py-2 text-sm font-medium text-card-foreground transition-colors hover:bg-surface-container"
              >
                取消
              </button>
              <button
                type="submit"
                disabled={saving}
                className="rounded-md bg-primary-container px-4 py-2 text-sm font-semibold text-on-primary transition-colors hover:bg-primary disabled:opacity-60"
              >
                {saving ? "保存中…" : "保存记录"}
              </button>
            </div>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Field({ label, required = false, error, children }: { label: string; required?: boolean; error?: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-xs font-medium text-muted-foreground">
        {label}
        {required && <span className="ml-0.5 text-destructive">*</span>}
      </span>
      {children}
      {error !== undefined && <span className={cn("text-[11px] text-destructive")}>{error}</span>}
    </label>
  );
}
