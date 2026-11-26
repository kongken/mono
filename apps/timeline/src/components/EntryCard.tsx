import {
  Bus,
  FirstAid,
  ForkKnife,
  MapPin,
  PawPrint,
  PencilSimple,
  PersonSimpleRun,
  Scissors,
  ShoppingBag,
  Sparkle,
  Tag,
  Trash,
} from "@phosphor-icons/react";
import type { Entry } from "@kongken/api-client/bff/timeline/v1/timeline_pb";
import type { MediaInfo } from "@kongken/api-client/bff/timeline/v1/media_pb";
import { Attachments } from "@/components/Attachments.tsx";
import { formatAmountCode } from "@/lib/entries.ts";
import { cn } from "@kongken/ui/utils";

/** The record's category as a thin line icon: a known word gets its own glyph, anything else the plain tag. */
function categoryIcon(category: string) {
  const text = category.toLowerCase();
  const props = { size: 13 } as const;
  if (/餐|饭|食|咖啡|tea|吃/.test(text)) return <ForkKnife {...props} />;
  if (/理发|剪|美容/.test(text)) return <Scissors {...props} />;
  if (/宠物|猫|狗/.test(text)) return <PawPrint {...props} />;
  if (/运动|跑步|健身|骑/.test(text)) return <PersonSimpleRun {...props} />;
  if (/清洁|打扫|整理/.test(text)) return <Sparkle {...props} />;
  if (/体检|医|药/.test(text)) return <FirstAid {...props} />;
  if (/交通|出行|打车|地铁/.test(text)) return <Bus {...props} />;
  if (/购物|买/.test(text)) return <ShoppingBag {...props} />;
  return <Tag {...props} />;
}

/** `12:30`, or nothing when the record is only a day. */
function clock(time: string): string {
  return time.slice(0, 5);
}

type EntryCardProps = {
  entry: Entry;
  onDelete?: (entry: Entry) => void;
  onEdit?: (entry: Entry) => void;
  onRestore?: (entry: Entry) => void;
  onPurge?: (entry: Entry) => void;
  media?: MediaInfo[];
  onMediaDeleted?: (mediaRef: string) => void;
  onMediaError?: (message: string) => void;
};

const iconButton = "grid size-7 place-items-center rounded-tag text-soft transition-colors";

/**
 * One row of the stream: the clock time (or 未指定) right-aligned left of the axis, the node on the axis — a hollow
 * ring when the record has a time, a small muted dot when it does not — and the flat entry card on a pale fill.
 * Amount sits top right; category and place are compact tags; attachments hang under the note.
 */
export function EntryCard({ entry, onDelete, onEdit, onRestore, onPurge, media = [], onMediaDeleted = () => {}, onMediaError = () => {} }: EntryCardProps) {
  const amount = formatAmountCode(entry.amountMinor, entry.currency);
  const timed = entry.time !== "";

  return (
    <div className="flex items-start gap-4">
      <div className="meta-numeric hidden w-[72px] shrink-0 pt-[15px] text-right text-soft sm:block">
        {timed ? clock(entry.time) : <span className="text-xs font-normal tracking-normal text-soft/70 italic">未指定</span>}
      </div>

      <div aria-hidden="true" className="flex w-7 shrink-0 justify-center pt-[19px]">
        {timed ? <span className="size-2.5 rounded-full bg-canvas ring-2 ring-leaf" /> : <span className="mt-[1px] size-2 rounded-full bg-line" />}
      </div>

      <article className="group flex min-w-0 flex-1 flex-col gap-2 rounded-card bg-paper p-3">
        <div className="flex items-start justify-between gap-2">
          <h4 className="min-w-0 text-base leading-6 font-medium break-words text-ink">
            <span className="meta-numeric mr-2 text-soft sm:hidden">{timed ? clock(entry.time) : "未指定"}</span>
            {entry.title}
          </h4>
          <div className="flex shrink-0 items-center gap-1">
            {amount !== "" && <span className="meta-numeric font-semibold text-ink">{amount}</span>}
            {onEdit !== undefined && (
              <button
                type="button"
                onClick={() => onEdit(entry)}
                aria-label={`详情 / 编辑「${entry.title}」`}
                title="详情 / 编辑"
                className={cn(iconButton, "opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 hover:bg-subtle hover:text-ink focus-visible:opacity-100")}
              >
                <PencilSimple size={15} />
              </button>
            )}
            {onDelete !== undefined && (
              <button
                type="button"
                onClick={() => onDelete(entry)}
                aria-label={`把「${entry.title}」移到回收站`}
                title="移到回收站"
                className={cn(iconButton, "opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 hover:bg-subtle hover:text-danger focus-visible:opacity-100")}
              >
                <Trash size={15} />
              </button>
            )}
          </div>
        </div>

        {(entry.category !== "" || entry.location !== "") && (
          <div className="flex flex-wrap items-center gap-1.5">
            {entry.category !== "" && (
              <span className="inline-flex h-6 items-center gap-1 rounded-tag bg-subtle px-2 text-xs text-ink">
                <span className="text-leaf">{categoryIcon(entry.category)}</span>
                {entry.category}
              </span>
            )}
            {entry.location !== "" && (
              <span className="inline-flex h-6 items-center gap-1 rounded-tag bg-subtle px-2 text-xs text-soft">
                <MapPin size={13} />
                {entry.location}
              </span>
            )}
          </div>
        )}

        {entry.note !== "" && <p className="text-sm leading-[22px] whitespace-pre-line text-ink/90">{entry.note}</p>}

        <Attachments items={media} readOnly={entry.deletedAt !== undefined} onDeleted={onMediaDeleted} onError={onMediaError} />

        {entry.url !== "" && (
          <a
            href={entry.url}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-block max-w-full truncate text-xs text-leaf underline-offset-4 hover:underline"
          >
            {entry.urlTitle === "" ? entry.url : entry.urlTitle}
          </a>
        )}

        {(onRestore !== undefined || onPurge !== undefined) && (
          <div className="flex items-center gap-2">
            {onRestore !== undefined && (
              <button
                type="button"
                onClick={() => onRestore(entry)}
                className="rounded-lg bg-subtle px-3 py-1 text-[13px] leading-[18px] font-medium text-ink transition-colors hover:bg-line"
              >
                恢复
              </button>
            )}
            {onPurge !== undefined && (
              <button
                type="button"
                onClick={() => onPurge(entry)}
                className="rounded-lg px-3 py-1 text-[13px] leading-[18px] font-medium text-danger transition-colors hover:bg-subtle"
              >
                彻底删除
              </button>
            )}
          </div>
        )}
      </article>
    </div>
  );
}
