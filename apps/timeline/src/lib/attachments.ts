/**
 * The attachment rules the media service enforces (internal/timeline/media/service.go), kept free of React and of
 * the network so the drawer's refusal and the server's refusal are the same sentence, asserted by a Node test.
 */

/** The four types the service accepts, exactly: `validate` refuses anything else before it signs an upload. */
export const ACCEPTED_TYPES = ["image/jpeg", "image/png", "image/webp", "application/pdf"] as const;

/** Ten mebibytes per file, the service's own ceiling. */
export const MAX_FILE_BYTES = 10 * 1024 * 1024;

/** Twenty attachments on one record, the service's own ceiling. */
export const MAX_FILES_PER_ENTRY = 20;

/**
 * A finished attachment is immutable in this slice, so its revision is always 1 — what `media.Delete` accepts.
 * The list response does not carry a revision yet; when the server grows one this constant goes away.
 */
export const MEDIA_REVISION = 1n;

/** The types a preview can show as an image; everything else is handed to the browser as a download. */
export function isImage(contentType: string): boolean {
  return contentType.startsWith("image/");
}

/** Why these files cannot be attached, or null when they can. The first offending file is named. */
export function refuseFiles(chosen: File[], alreadyAttached: number): string | null {
  const total = alreadyAttached + chosen.length;
  if (total > MAX_FILES_PER_ENTRY) {
    return `一条记录最多 ${MAX_FILES_PER_ENTRY} 个附件，这次会变成 ${total} 个。`;
  }
  for (const file of chosen) {
    if (!ACCEPTED_TYPES.includes(file.type as (typeof ACCEPTED_TYPES)[number])) {
      return `「${file.name}」不是 JPEG / PNG / WebP / PDF。`;
    }
    if (file.size === 0) return `「${file.name}」是空文件。`;
    if (file.size > MAX_FILE_BYTES) return `「${file.name}」超过 10 MiB。`;
  }
  return null;
}

/** One file on its way to the record: what the drawer shows while the record is being saved. */
export type UploadItem = {
  file: File;
  status: "pending" | "uploading" | "done" | "failed";
  message?: string;
};

/** `823 KiB` / `1.4 MiB`, so the drawer says a size the way a person reads one. */
export function fileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

/** The line under a file: its size while it waits, what happened to it once it has an outcome. */
export function uploadStatusText(item: UploadItem): string {
  if (item.status === "uploading") return "上传中…";
  if (item.status === "done") return "已附上";
  if (item.status === "failed") return item.message === undefined ? "没传上去" : `没传上去：${item.message}`;
  return `${fileSize(item.file.size)} · 待上传`;
}

/** The service's own codes, said in the drawer's words: this is what the person does next. */
export function explainUploadFailure(message: string): string {
  if (/UPLOAD_EXPIRED|expire/i.test(message)) return "上传链接已过期，请重试；文件不会重复保存。";
  if (/413|too large|size/i.test(message)) return "文件大小和服务端记录不一致。";
  if (/403|denied|signature/i.test(message)) return "上传链接失效，请重试。";
  return message;
}
