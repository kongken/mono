import { useEffect, useState } from "react";
import { ArrowSquareOut, FilePdf, ImageSquare, Paperclip, X } from "@phosphor-icons/react";
import type { MediaInfo } from "@kongken/api-client/bff/timeline/v1/media_pb";
import { MEDIA_REVISION, isImage } from "@/lib/attachments.ts";
import { describeError } from "@/lib/errors.ts";
import { media as mediaRPC, mutationID, write } from "@/lib/rpc.ts";

type Preview = { url: string; name: string; contentType: string };

type AttachmentsProps = {
  items: MediaInfo[];
  /** True for a record in 回收站: an attachment of a deleted record is read-only. */
  readOnly?: boolean;
  onDeleted: (mediaRef: string) => void;
  onError: (message: string) => void;
};

/**
 * A record's attachments. Bytes are read through the BFF with the session Cookie — the object store stays private —
 * so opening one makes an object URL that lives only as long as the preview, and a PDF is handed to the browser as a
 * download rather than embedded. Deleting is a server write with a mutation ID, like every other write on this page.
 */
export function Attachments({ items, readOnly = false, onDeleted, onError }: AttachmentsProps) {
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  // A preview's object URL is memory the page owns: release it when the preview changes or the card goes away.
  useEffect(() => {
    return () => {
      if (preview !== null) URL.revokeObjectURL(preview.url);
    };
  }, [preview]);

  const open = async (item: MediaInfo) => {
    setBusy(item.mediaRef);
    try {
      const answer = await mediaRPC.readMedia({ mediaRef: item.mediaRef });
      const contentType = answer.contentType === "" ? item.contentType : answer.contentType;
      const url = URL.createObjectURL(new Blob([new Uint8Array(answer.data)], { type: contentType }));
      if (isImage(contentType)) {
        setPreview((current) => {
          if (current !== null) URL.revokeObjectURL(current.url);
          return { url, name: answer.filename === "" ? item.filename : answer.filename, contentType };
        });
      } else {
        const link = document.createElement("a");
        link.href = url;
        link.download = answer.filename === "" ? item.filename : answer.filename;
        document.body.append(link);
        link.click();
        link.remove();
        URL.revokeObjectURL(url);
      }
    } catch (error) {
      onError(describeError(error, "读取附件失败"));
    } finally {
      setBusy(null);
    }
  };

  const remove = async (item: MediaInfo) => {
    setBusy(item.mediaRef);
    try {
      await write(() => mediaRPC.deleteMedia({ mediaRef: item.mediaRef, mutationId: mutationID(), expectedRevision: MEDIA_REVISION }));
      setPreview(null);
      onDeleted(item.mediaRef);
    } catch (error) {
      onError(describeError(error, "删除附件失败"));
    } finally {
      setBusy(null);
    }
  };

  if (items.length === 0) return null;

  return (
    <div className="mt-2.5">
      <ul className="flex flex-wrap items-center gap-1.5">
        {items.map((item) => (
          <li key={item.mediaRef}>
            <span className="inline-flex items-center gap-1 rounded-md bg-surface-container/80 py-1 pr-1 pl-2 text-xs text-primary">
              <button
                type="button"
                onClick={() => void open(item)}
                disabled={busy !== null}
                aria-label={`打开附件「${item.filename}」`}
                className="inline-flex max-w-[220px] items-center gap-1 disabled:opacity-50"
              >
                {isImage(item.contentType) ? <ImageSquare size={13} /> : <FilePdf size={13} />}
                <span className="truncate">{item.filename}</span>
                {busy === item.mediaRef ? <span className="text-[10px] text-muted-foreground">读取中…</span> : <ArrowSquareOut size={11} className="shrink-0 opacity-60" />}
              </button>
              {!readOnly && (
                <button
                  type="button"
                  onClick={() => void remove(item)}
                  disabled={busy !== null}
                  aria-label={`删除附件「${item.filename}」`}
                  className="rounded p-0.5 text-muted-foreground/70 transition-colors hover:bg-error-container hover:text-destructive disabled:opacity-50"
                >
                  <X size={11} />
                </button>
              )}
            </span>
          </li>
        ))}
        <li className="text-[11px] text-muted-foreground">
          <Paperclip size={11} className="mr-0.5 inline" />
          {items.length} 个附件
        </li>
      </ul>
      {preview !== null && (
        <figure className="mt-2 overflow-hidden rounded-md border border-outline-variant bg-muted/40 p-1.5">
          <img src={preview.url} alt={preview.name} className="max-h-64 w-auto rounded" />
          <figcaption className="mt-1 flex items-center justify-between px-1 text-[11px] text-muted-foreground">
            <span className="truncate">{preview.name}</span>
            <button type="button" onClick={() => setPreview(null)} className="rounded px-1 text-primary hover:bg-surface-container">
              收起
            </button>
          </figcaption>
        </figure>
      )}
    </div>
  );
}
