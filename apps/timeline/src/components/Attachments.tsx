import { useEffect, useState } from "react";
import { ArrowSquareOut, FilePdf, ImageSquare, Paperclip, X } from "@phosphor-icons/react";
import type { MediaInfo } from "@kongken/api-client/bff/timeline/v1/media_pb";
import { MEDIA_REVISION, fileSize, isImage } from "@/lib/attachments.ts";
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
		await write(() => mediaRPC.deleteMedia({ mediaRef: item.mediaRef, mutationId: mutationID(), expectedRevision: item.revision === 0n ? MEDIA_REVISION : item.revision }));
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
    <div className="flex flex-col gap-1.5">
      <p className="text-[11px] leading-4 text-soft">
        <Paperclip size={11} className="mr-0.5 inline" />
        {items.length} 个附件
      </p>
      <ul className="flex flex-col gap-1.5">
        {items.map((item) => (
          <li key={item.mediaRef} className="flex max-w-xs items-center gap-1 rounded-field bg-subtle p-2">
            <button
              type="button"
              onClick={() => void open(item)}
              disabled={busy !== null}
              aria-label={`打开附件「${item.filename}」`}
              className="flex min-w-0 flex-1 items-center gap-2 text-left disabled:opacity-50"
            >
              <span className={`grid size-7 shrink-0 place-items-center rounded-tag bg-canvas ${isImage(item.contentType) ? "text-leaf" : "text-required"}`}>
                {isImage(item.contentType) ? <ImageSquare size={18} /> : <FilePdf size={18} />}
              </span>
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="truncate text-xs leading-4 text-ink">{item.filename}</span>
                <span className="text-[11px] leading-4 text-soft">{busy === item.mediaRef ? "读取中…" : fileSize(Number(item.sizeBytes))}</span>
              </span>
              {busy !== item.mediaRef && <ArrowSquareOut size={12} className="shrink-0 text-soft/60" />}
            </button>
            {!readOnly && (
              <button
                type="button"
                onClick={() => void remove(item)}
                disabled={busy !== null}
                aria-label={`删除附件「${item.filename}」`}
                className="grid size-6 shrink-0 place-items-center rounded-tag text-soft transition-colors hover:bg-canvas hover:text-danger disabled:opacity-50"
              >
                <X size={12} />
              </button>
            )}
          </li>
        ))}
      </ul>
      {preview !== null && (
        <figure className="overflow-hidden rounded-field bg-canvas p-1.5">
          <img src={preview.url} alt={preview.name} className="max-h-64 w-auto rounded-tag" />
          <figcaption className="mt-1 flex items-center justify-between px-1 text-[11px] text-soft">
            <span className="truncate">{preview.name}</span>
            <button type="button" onClick={() => setPreview(null)} className="rounded px-1 text-leaf hover:bg-subtle">
              收起
            </button>
          </figcaption>
        </figure>
      )}
    </div>
  );
}
