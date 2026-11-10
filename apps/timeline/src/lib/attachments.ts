export const MAX_ATTACHMENT_BYTES = 10 * 1024 * 1024;
export const MAX_ATTACHMENTS = 20;
export const ACCEPTED_ATTACHMENT_TYPES = ["image/jpeg", "image/png", "image/webp", "application/pdf"] as const;

export type Attachment = {
  id: string;
  filename: string;
  contentType: string;
  sizeBytes: number;
  status: "uploading" | "ready" | "error" | "deleting";
  progress: number;
  error?: string;
  file?: File;
};

export function validateAttachment(file: File): string | undefined {
  if (!(ACCEPTED_ATTACHMENT_TYPES as readonly string[]).includes(file.type)) return "仅支持 JPEG、PNG、WebP 或 PDF 文件。";
  if (file.size > MAX_ATTACHMENT_BYTES) return "单个附件不能超过 10 MiB。";
  if (file.size === 0) return "不能上传空文件。";
  return undefined;
}

export function attachmentKind(contentType: string): "image" | "pdf" {
  return contentType === "application/pdf" ? "pdf" : "image";
}

export function formatAttachmentSize(bytes: number): string {
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}
