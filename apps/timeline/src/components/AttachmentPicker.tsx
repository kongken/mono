import { FileArrowUp, FilePdf, ImageSquare, X, WarningCircle } from "@phosphor-icons/react";
import { useRef, useState } from "react";
import { attachmentKind, formatAttachmentSize, MAX_ATTACHMENTS, validateAttachment, type Attachment } from "@/lib/attachments.ts";

type AttachmentPickerProps = {
  value: Attachment[];
  onChange: (attachments: Attachment[]) => void;
  disabled?: boolean;
};

/** The UI seam for Timeline MediaService: selected files are validated here; the parent owns Prepare/direct PUT/Complete. */
export function AttachmentPicker({ value, onChange, disabled = false }: AttachmentPickerProps) {
  const input = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const add = (files: FileList | File[]) => {
    const incoming = [...files];
    if (value.length + incoming.length > MAX_ATTACHMENTS) {
      setError(`最多添加 ${MAX_ATTACHMENTS} 个附件。`);
      return;
    }
    const next = [...value];
    for (const file of incoming) {
      const reason = validateAttachment(file);
      if (reason !== undefined) {
        setError(`${file.name}：${reason}`);
        continue;
      }
      if (next.some((item) => item.filename === file.name && item.sizeBytes === file.size)) continue;
      next.push({ id: crypto.randomUUID(), filename: file.name, contentType: file.type, sizeBytes: file.size, status: "uploading", progress: 0, file });
    }
    setError(null);
    onChange(next);
  };

  return (
    <div>
      <div
        role="button"
        tabIndex={disabled ? -1 : 0}
        aria-label="添加附件"
        onClick={() => !disabled && input.current?.click()}
        onKeyDown={(event) => event.key === "Enter" && !disabled && input.current?.click()}
        onDragOver={(event) => { event.preventDefault(); if (!disabled) setDragging(true); }}
        onDragLeave={() => setDragging(false)}
        onDrop={(event) => { event.preventDefault(); setDragging(false); if (!disabled) add(event.dataTransfer.files); }}
        className={`flex cursor-pointer flex-col items-center gap-1.5 rounded-md border border-dashed px-4 py-5 text-center transition-colors ${dragging ? "border-primary bg-surface-container" : "border-outline-variant bg-muted/60"} ${disabled ? "cursor-not-allowed opacity-60" : "hover:border-primary"}`}
      >
        <FileArrowUp size={19} className="text-primary" />
        <p className="text-xs font-medium text-card-foreground">拖拽文件到这里，或点击选择</p>
        <p className="text-[11px] text-muted-foreground">JPEG、PNG、WebP 或 PDF，单个不超过 10 MiB，最多 20 个</p>
        <input ref={input} type="file" multiple accept="image/jpeg,image/png,image/webp,application/pdf" className="sr-only" disabled={disabled} onChange={(event) => { if (event.target.files) add(event.target.files); event.target.value = ""; }} />
      </div>
      {error !== null && <p className="mt-2 flex items-center gap-1 text-[11px] text-destructive"><WarningCircle size={13} />{error}</p>}
      {value.length > 0 && <div className="mt-2 grid gap-1.5" aria-label="附件列表">
        {value.map((file) => <AttachmentRow key={file.id} file={file} onRemove={() => onChange(value.filter((item) => item.id !== file.id))} />)}
      </div>}
    </div>
  );
}

function AttachmentRow({ file, onRemove }: { file: Attachment; onRemove: () => void }) {
  const Icon = attachmentKind(file.contentType) === "pdf" ? FilePdf : ImageSquare;
  return <div className="flex items-center gap-2 rounded-md border border-outline-variant bg-white px-2.5 py-2 text-xs">
    <Icon size={17} className="shrink-0 text-primary" />
    <span className="min-w-0 flex-1 truncate" title={file.filename}>{file.filename}<span className="ml-1 text-muted-foreground">{formatAttachmentSize(file.sizeBytes)}</span></span>
    {file.status === "uploading" && <span className="text-muted-foreground">{file.progress}%</span>}
    {file.status === "error" && <span className="text-destructive">失败</span>}
    <button type="button" aria-label={`移除附件 ${file.filename}`} onClick={onRemove} className="rounded p-1 text-muted-foreground hover:bg-muted"><X size={14} /></button>
  </div>;
}
