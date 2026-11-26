import { useEffect, useMemo, useState } from "react";
import { CaretUpDown, CloudArrowUp, MapPin, NotePencil, SquaresFour, WarningCircle, X } from "@phosphor-icons/react";
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
  /** 0–100 while the file is on its way to the store; undefined otherwise. */
  progress?: number;
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
  mode?: "create" | "edit";
};

/**
 * 记一笔: the design's right-hand panel (420px, beside the timeline from 1280px up; a slide-over below that). Title,
 * date and time with the record's own time zone, category, place, amount and currency, note, and attachments — the
 * fields the records and media APIs actually have. The record is written first, then each file is signed, uploaded
 * straight to the object store, and completed; a file that fails leaves the record saved and offers a retry, so a
 * retry never writes the record twice. It is a non-modal dialog: the timeline stays readable beside it.
 */
export function Composer({ open, initial, categories, saving, error, uploads = [], onRetry, onClose, onSubmit, mode = "create" }: ComposerProps) {
  const [draft, setDraft] = useState<DraftEntry>(initial);
  const [errors, setErrors] = useState<DraftErrors>({});
  const [zoneEditing, setZoneEditing] = useState(false);
  const [files, setFiles] = useState<File[]>([]);
  // What is wrong with the chosen files, kept apart from the field errors: the note field's error slot is the
  // note's, and a file refusal printed there would point at the wrong box.
  const [fileError, setFileError] = useState<string | null>(null);
  useEffect(() => {
    if (!open) return;
    setDraft(initial);
    setErrors({});
    setFiles([]);
    setFileError(null);
  }, [initial, open]);

  useEffect(() => {
    if (!open) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", escape);
    return () => document.removeEventListener("keydown", escape);
  }, [open, onClose]);

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
  // simply what was chosen. Both are drawn the same way so the panel does not jump when the save starts.
  const failed = uploads.filter((item) => item.status === "failed").length;
  const rows: AttachmentRow[] =
    uploads.length > 0
      ? uploads.map((item) => ({
          key: `${item.file.name}-${item.file.size}-${item.file.lastModified}`,
          name: item.file.name,
          status: uploadStatusText(item),
          tone: item.status === "failed" ? "error" : item.status === "done" ? "ok" : "muted",
          progress: item.status === "uploading" ? (item.progress ?? 0) : undefined,
        }))
      : files.map((file, index) => ({
          key: `${file.name}-${index}`,
          name: file.name,
          status: uploadStatusText({ file, status: "pending" }),
          tone: "muted",
          remove: () => setFiles((current) => current.filter((_unused, at) => at !== index)),
        }));

  if (!open) return null;

  return (
    <>
      <div aria-hidden="true" onClick={onClose} className="fixed inset-0 z-40 bg-ink/20 xl:hidden" />
      <aside
        role="dialog"
        aria-label={mode === "edit" ? "编辑记录" : "记一笔"}
        className="fixed inset-y-0 right-0 z-50 flex w-full max-w-[420px] flex-col overflow-y-auto bg-canvas p-5 shadow-float xl:sticky xl:top-[72px] xl:z-auto xl:max-h-[calc(100vh-88px)] xl:w-[420px] xl:max-w-none xl:shrink-0 xl:rounded-card"
      >
        <div className="flex items-center justify-between pb-3">
          <div className="flex items-center gap-2">
            <span className="grid size-6 place-items-center rounded-tag bg-subtle text-leaf">
              <NotePencil size={16} />
            </span>
            <span className="text-lg leading-[26px] font-semibold text-ink">{mode === "edit" ? "编辑记录" : "记一笔"}</span>
          </div>
          <button
            type="button"
            onClick={onClose}
            title="收起面板"
            aria-label="关闭"
            className="grid size-7 place-items-center rounded-tag text-soft transition-colors hover:bg-subtle hover:text-ink"
          >
            <X size={18} />
          </button>
        </div>

        <form onSubmit={submit} noValidate className="flex flex-col gap-3.5">
          <Field id="timeline-title" label="今天发生了什么？" required error={errors.title}>
            <input
              id="timeline-title"
              value={draft.title}
              onChange={(event) => set({ title: event.target.value })}
              placeholder="输入手账标题..."
              maxLength={200}
              autoFocus
              aria-invalid={errors.title !== undefined}
              className="field h-9 px-3"
            />
          </Field>

          <div className="flex flex-col gap-1">
            <span className="text-[13px] leading-[18px] font-medium text-ink">日期与时间</span>
            <div className="grid grid-cols-2 gap-2">
              <input
                type="date"
                value={draft.date}
                onChange={(event) => set({ date: event.target.value })}
                aria-label="日期"
                aria-invalid={errors.date !== undefined}
                className="field meta-numeric h-9 px-2.5"
              />
              <input
                type="time"
                value={draft.time}
                onChange={(event) => set({ time: event.target.value })}
                aria-label="时间（可留空）"
                aria-invalid={errors.time !== undefined}
                className="field meta-numeric h-9 px-2.5"
              />
            </div>
            {(errors.date ?? errors.time) !== undefined && <span className="text-[11px] text-required">{errors.date ?? errors.time}</span>}
            <div className="flex items-center justify-between gap-2 px-1 text-[11px] leading-4 text-soft">
              <span className="min-w-0 truncate">
                时区：<span className="tabular">{zone}</span> (
                <button type="button" onClick={() => setZoneEditing((editing) => !editing)} className="underline hover:text-ink">
                  {zoneEditing ? "收起" : "修改"}
                </button>
                )
              </span>
              <span className="shrink-0 text-soft/80">时间可选留空</span>
            </div>
            {errors.timezone !== undefined && <span className="text-[11px] text-required">{errors.timezone}</span>}
            {zoneEditing && (
              <select
                value={COMMON_ZONES.includes(draft.timezone) ? draft.timezone : ""}
                onChange={(event) => {
                  if (event.target.value !== "") set({ timezone: event.target.value });
                }}
                aria-label="选择时区"
                className="field h-9 px-2.5 text-sm"
              >
                <option value="">选择常用时区（当前：{draft.timezone}）</option>
                {COMMON_ZONES.map((item) => (
                  <option key={item} value={item}>
                    {timezoneLabel(item)}
                  </option>
                ))}
              </select>
            )}
          </div>

          <Field id="timeline-category" label="分类" error={errors.category}>
            <div className="relative">
              <SquaresFour size={16} className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-leaf" />
              <input
                id="timeline-category"
                value={draft.category}
                onChange={(event) => set({ category: event.target.value })}
                list="timeline-categories"
                placeholder="其他"
                maxLength={100}
                aria-invalid={errors.category !== undefined}
                className="field h-9 pr-8 pl-8"
              />
              <CaretUpDown size={16} className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 text-soft" />
              <datalist id="timeline-categories">
                {[...new Set(["外出就餐", "理发", "宠物开销", "运动", "清洁", "体检", "其他", ...categories])].map((category) => (
                  <option key={category} value={category} />
                ))}
              </datalist>
            </div>
          </Field>

          <Field id="timeline-location" label="地点／店名" optional error={errors.location}>
            <div className="relative">
              <MapPin size={16} className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-soft" />
              <input
                id="timeline-location"
                value={draft.location}
                onChange={(event) => set({ location: event.target.value })}
                placeholder="例如：梧桐树下咖啡馆"
                maxLength={300}
                aria-invalid={errors.location !== undefined}
                className="field h-9 pr-3 pl-8"
              />
            </div>
          </Field>

          <Field id="timeline-amount" label="金额与币种" optional error={errors.amount ?? errors.currency}>
            <div className="flex gap-2">
              <select
                value={draft.currency}
                onChange={(event) => set({ currency: event.target.value })}
                aria-label="币种"
                className="field meta-numeric h-9 w-24 shrink-0 px-2"
              >
                {CURRENCIES.map((currency) => (
                  <option key={currency} value={currency}>
                    {currency}
                  </option>
                ))}
              </select>
              <input
                id="timeline-amount"
                value={draft.amount}
                onChange={(event) => set({ amount: event.target.value })}
                inputMode={digits === 0 ? "numeric" : "decimal"}
                placeholder={amountPlaceholder(draft.currency)}
                aria-invalid={errors.amount !== undefined}
                className="field meta-numeric h-9 flex-1 px-3"
              />
            </div>
          </Field>

          <Field id="timeline-note" label="备注" error={errors.note}>
            <textarea
              id="timeline-note"
              value={draft.note}
              onChange={(event) => set({ note: event.target.value })}
              rows={3}
              maxLength={10000}
              placeholder="写下这一刻的心情、细节或随想..."
              aria-invalid={errors.note !== undefined}
              className="field resize-none p-2.5"
            />
          </Field>

          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between gap-2">
              <span className="text-[13px] leading-[18px] font-medium text-ink">附件区</span>
              <span className="text-[11px] text-soft">图片或 PDF，≤10 MiB，最多20个</span>
            </div>
            <label
              htmlFor="timeline-attachments"
              onDragOver={(event) => event.preventDefault()}
              onDrop={(event) => {
                event.preventDefault();
                addFiles(Array.from(event.dataTransfer.files));
              }}
              className="flex cursor-pointer flex-col items-center justify-center gap-1 rounded-field bg-paper p-3 transition-colors hover:bg-subtle"
            >
              <CloudArrowUp size={20} className="text-leaf" />
              <span className="text-xs text-soft">点击或拖拽文件添加附件</span>
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
            {fileError !== null && <p className="text-[11px] text-required">{fileError}</p>}
            {rows.length > 0 && (
              <ul className="flex flex-col gap-1.5 pt-0.5">
                {rows.map((row) => (
                  <li key={row.key} className="flex flex-col gap-1 rounded-tag bg-subtle p-2">
                    <div className="flex items-center justify-between gap-2 text-[11px] leading-4">
                      <span className="min-w-0 truncate text-ink">{row.name}</span>
                      <span className="flex shrink-0 items-center gap-1">
                        <span className={cn("text-soft", row.tone === "error" && "text-required", row.tone === "ok" && "text-leaf")}>{row.status}</span>
                        {row.remove !== undefined && !saving && (
                          <button
                            type="button"
                            onClick={row.remove}
                            aria-label={`移除附件「${row.name}」`}
                            className="grid size-4 place-items-center rounded-full text-soft transition-colors hover:bg-required-container hover:text-required"
                          >
                            <X size={10} />
                          </button>
                        )}
                      </span>
                    </div>
                    {row.progress !== undefined && (
                      <div className="h-1 w-full overflow-hidden rounded-full bg-line">
                        <div className="h-full rounded-full bg-leaf transition-[width]" style={{ width: `${row.progress}%` }} />
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            )}
            {failed > 0 && onRetry !== undefined && (
              <button
                type="button"
                onClick={onRetry}
                disabled={saving}
                className="self-start rounded-lg bg-subtle px-3 py-1.5 text-xs font-medium text-ink transition-colors hover:bg-line disabled:opacity-60"
              >
                重试上传（{failed} 个）
              </button>
            )}
          </div>

          <div className="flex flex-col gap-2 pt-1">
            {error !== null && (
              <p className="flex items-start gap-1.5 rounded-field bg-required-container px-3 py-2 text-xs text-required" role="alert">
                <WarningCircle size={14} className="mt-px shrink-0" />
                {error}
              </p>
            )}
            <div className="flex items-center justify-end gap-2.5">
              <button
                type="button"
                onClick={onClose}
                className="rounded-card bg-subtle px-4 py-2 text-[13px] leading-[18px] font-medium text-soft transition-colors hover:bg-line"
              >
                取消
              </button>
              <button
                type="submit"
                disabled={saving}
                className="rounded-card bg-leaf px-5 py-2 text-[13px] leading-[18px] font-medium text-white transition-colors hover:bg-leaf-hover disabled:opacity-60"
              >
                {saving ? "保存中…" : "保存记录"}
              </button>
            </div>
          </div>
        </form>
      </aside>
    </>
  );
}

function Field({
  id,
  label,
  required = false,
  optional = false,
  error,
  children,
}: {
  id: string;
  label: string;
  required?: boolean;
  optional?: boolean;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="flex items-center justify-between text-[13px] leading-[18px] font-medium text-ink">
        <span>
          {label}
          {optional && <span className="ml-1 text-xs font-normal text-soft">(可选)</span>}
        </span>
        {required && <span className="text-xs font-semibold text-required">必填</span>}
      </label>
      {children}
      {error !== undefined && <span className="text-[11px] text-required">{error}</span>}
    </div>
  );
}
