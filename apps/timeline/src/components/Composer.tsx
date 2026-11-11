import { useMemo, useState } from "react";
import { CloudArrowUp, SlidersHorizontal, WarningCircle, X } from "@phosphor-icons/react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import { Input } from "@kongken/ui/input";
import { ACCEPTED_TYPES, refuseFiles, uploadStatusText, type UploadItem } from "@/lib/attachments.ts";
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

/** One line of the attachment list: what was chosen, what happened to it, and whether it can still be dropped. */
type AttachmentRow = {
  key: string;
  name: string;
  status: string;
  tone: "muted" | "ok" | "error";
  remove?: () => void;
};

type ComposerProps = {
  open: boolean;
  initial: DraftEntry;
  categories: string[];
  saving: boolean;
  error: string | null;
  /** The files of the record being saved, with what happened to each: only set once a save has started. */
  uploads?: UploadItem[];
  onRetry?: () => void;
  onClose: () => void;
  onSubmit: (draft: DraftEntry, files: File[]) => void;
};

/**
 * 记一笔: the design's right-hand drawer. Title, date and time with the record's own time zone, category,
 * place, amount and currency, note, and attachments — the fields the records and media APIs actually have. The
 * record is written first, then each file is signed, uploaded straight to the object store, and completed; a file
 * that fails leaves the record saved and offers a retry, so a retry never writes the record twice.
 */
export function Composer({ open, initial, categories, saving, error, uploads = [], onRetry, onClose, onSubmit }: ComposerProps) {
  const [draft, setDraft] = useState<DraftEntry>(initial);
  const [errors, setErrors] = useState<DraftErrors>({});
  const [zoneEditing, setZoneEditing] = useState(false);
  const [files, setFiles] = useState<File[]>([]);
  // What is wrong with the chosen files, kept apart from the field errors: the note field's error slot is the
  // note's, and a file refusal printed there would point at the wrong box.
  const [fileError, setFileError] = useState<string | null>(null);

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
    const refusal = refuseFiles(files, 0);
    if (refusal !== null) {
      setFileError(refusal);
      return;
    }
    setFileError(null);
    onSubmit(draft, files);
  };

  const addFiles = (chosen: File[]) => {
    const merged = [...files, ...chosen].slice(0, 20);
    const refusal = refuseFiles(chosen, files.length);
    setFileError(refusal);
    if (refusal === null) setFiles(merged);
  };

  // While a save is running the list is the server's story (pending → uploading → done / failed); before that it is
  // simply what was chosen. Both are drawn the same way so the drawer does not jump when the save starts.
  const failed = uploads.filter((item) => item.status === "failed").length;
  const rows: AttachmentRow[] =
    uploads.length > 0
      ? uploads.map((item) => ({
          key: `${item.file.name}-${item.file.size}-${item.file.lastModified}`,
          name: item.file.name,
          status: uploadStatusText(item),
          tone: item.status === "failed" ? "error" : item.status === "done" ? "ok" : "muted",
        }))
      : files.map((file, index) => ({
          key: `${file.name}-${index}`,
          name: file.name,
          status: uploadStatusText({ file, status: "pending" }),
          tone: "muted",
          remove: () => setFiles((current) => current.filter((_unused, at) => at !== index)),
        }));

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
                <p className="mb-2 text-xs font-medium text-muted-foreground">附件（可选）</p>
                <label
                  htmlFor="timeline-attachments"
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={(event) => {
                    event.preventDefault();
                    addFiles(Array.from(event.dataTransfer.files));
                  }}
                  className="flex cursor-pointer flex-col items-center gap-1.5 rounded-md border border-dashed border-outline-variant bg-muted/60 px-4 py-6 text-center hover:border-primary"
                >
                  <CloudArrowUp size={19} className="text-primary" />
                  <p className="text-xs font-medium text-card-foreground">点击或拖拽文件添加附件</p>
                  <p className="text-[11px] text-muted-foreground">JPEG、PNG、WebP 或 PDF，单个不超过 10 MiB，最多 20 个</p>
                  <input
                    id="timeline-attachments"
                    type="file"
                    multiple
                    accept={ACCEPTED_TYPES.join(",")}
                    disabled={saving}
                    className="sr-only"
                    onChange={(event) => {
                      addFiles(Array.from(event.target.files ?? []));
                      // Clearing the input lets the same file be chosen again after a refusal.
                      event.target.value = "";
                    }}
                  />
                </label>
                {fileError !== null && <p className="mt-1.5 text-[11px] text-destructive">{fileError}</p>}
                {rows.length > 0 && (
                  <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
                    {rows.map((row) => (
                      <li key={row.key} className="flex items-center justify-between gap-2">
                        <span className="truncate">{row.name}</span>
                        <span className="flex shrink-0 items-center gap-1">
                          <span className={cn(row.tone === "error" && "text-destructive", row.tone === "ok" && "text-primary")}>{row.status}</span>
                          {row.remove !== undefined && !saving && (
                            <button
                              type="button"
                              onClick={row.remove}
                              aria-label={`移除附件「${row.name}」`}
                              className="rounded p-0.5 transition-colors hover:bg-error-container hover:text-destructive"
                            >
                              <X size={11} />
                            </button>
                          )}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
                {failed > 0 && onRetry !== undefined && (
                  <button
                    type="button"
                    onClick={onRetry}
                    disabled={saving}
                    className="mt-2 rounded-md border border-outline-variant bg-white px-3 py-1.5 text-xs font-medium text-primary transition-colors hover:bg-surface-container disabled:opacity-60"
                  >
                    重试上传（{failed} 个）
                  </button>
                )}
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

function Field({ label, required = false, error, children }: { label: string; required?: boolean; error?: string; children: React.ReactNode }) {  return (
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
