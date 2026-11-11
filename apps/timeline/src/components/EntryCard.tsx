import {
  Bus,
  FirstAid,
  ForkKnife,
  MapPin,
  PawPrint,
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
import { formatAmount } from "@/lib/entries.ts";

/** The record's category as an icon: a known word gets its own glyph, anything else the plain tag. */
function categoryIcon(category: string) {
  const text = category.toLowerCase();
  if (/餐|饭|食|咖啡|tea|吃/.test(text)) return <ForkKnife size={15} />;
  if (/理发|剪|美容/.test(text)) return <Scissors size={15} />;
  if (/宠物|猫|狗/.test(text)) return <PawPrint size={15} />;
  if (/运动|跑步|健身|骑/.test(text)) return <PersonSimpleRun size={15} />;
  if (/清洁|打扫|整理/.test(text)) return <Sparkle size={15} />;
  if (/体检|医|药/.test(text)) return <FirstAid size={15} />;
  if (/交通|出行|打车|地铁/.test(text)) return <Bus size={15} />;
  if (/购物|买/.test(text)) return <ShoppingBag size={15} />;
  return <Tag size={15} />;
}

/** `12:30`, or nothing when the record is only a day. */
function clock(time: string): string {
  return time.slice(0, 5);
}

type EntryCardProps = {
  entry: Entry;
  onDelete?: (entry: Entry) => void;
  onRestore?: (entry: Entry) => void;
  onPurge?: (entry: Entry) => void;
  media?: MediaInfo[];
  onMediaDeleted?: (mediaRef: string) => void;
  onMediaError?: (message: string) => void;
};

export function EntryCard({ entry, onDelete, onRestore, onPurge, media = [], onMediaDeleted = () => {}, onMediaError = () => {} }: EntryCardProps) {
  const amount = formatAmount(entry.amountMinor, entry.currency);

  return (
    <article className="group flex gap-4 rounded-card bg-card px-5 py-4 shadow-[0_1px_3px_rgba(24,61,27,0.06)] transition-shadow hover:shadow-[0_4px_16px_rgba(24,61,27,0.10)]">
      <div className="flex w-[76px] shrink-0 flex-col items-start gap-1.5 pt-0.5">
        <span className="tabular text-xs text-muted-foreground">{entry.time === "" ? "全天" : clock(entry.time)}</span>
        <span className="grid size-7 place-items-center rounded-full bg-surface-container text-primary">{categoryIcon(entry.category)}</span>
      </div>

      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-3">
          <h3 className="truncate text-[15px] font-semibold text-card-foreground">{entry.title}</h3>
          {amount !== "" && <span className="tabular ml-auto shrink-0 text-[15px] font-semibold text-primary">{amount}</span>}
        </div>
        {(entry.category !== "" || entry.location !== "") && (
          <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
            {entry.category !== "" && <span className="rounded-full bg-surface-container/70 px-2 py-0.5 text-primary">{entry.category}</span>}
            {entry.location !== "" && (
              <span className="inline-flex items-center gap-1">
                <MapPin size={12} />
                {entry.location}
              </span>
            )}
          </div>
        )}
        {entry.note !== "" && <p className="mt-2 text-sm leading-relaxed whitespace-pre-line text-on-surface-variant">{entry.note}</p>}
        <Attachments items={media} readOnly={entry.deletedAt !== undefined} onDeleted={onMediaDeleted} onError={onMediaError} />
        {entry.url !== "" && (
          <a
            href={entry.url}
            target="_blank"
            rel="noreferrer noopener"
            className="mt-2 inline-block max-w-full truncate text-xs text-primary underline-offset-4 hover:underline"
          >
            {entry.urlTitle === "" ? entry.url : entry.urlTitle}
          </a>
        )}
      </div>

      <div className="flex shrink-0 items-start gap-1">
        {onDelete !== undefined && (
          <button
            type="button"
            onClick={() => onDelete(entry)}
            aria-label={`把「${entry.title}」移到回收站`}
            className="rounded-md p-1.5 text-muted-foreground/70 opacity-0 transition-all group-hover:opacity-100 hover:bg-error-container hover:text-destructive focus-visible:opacity-100"
          >
            <Trash size={15} />
          </button>
        )}
        {onRestore !== undefined && (
          <button
            type="button"
            onClick={() => onRestore(entry)}
            className="rounded-md px-2 py-1 text-xs font-medium text-primary transition-colors hover:bg-surface-container"
          >
            恢复
          </button>
        )}
        {onPurge !== undefined && (
          <button
            type="button"
            onClick={() => onPurge(entry)}
            className="rounded-md px-2 py-1 text-xs font-medium text-destructive transition-colors hover:bg-error-container"
          >
            彻底删除
          </button>
        )}
      </div>
    </article>
  );
}
