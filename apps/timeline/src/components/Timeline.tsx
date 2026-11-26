import type { Entry } from "@kongken/api-client/bff/timeline/v1/timeline_pb";
import type { MediaInfo } from "@kongken/api-client/bff/timeline/v1/media_pb";
import { ClockCounterClockwise } from "@phosphor-icons/react";
import { EntryCard } from "@/components/EntryCard.tsx";
import type { MonthGroup } from "@/lib/entries.ts";

type TimelineProps = {
  months: MonthGroup[];
  empty: boolean;
  emptyTitle?: string;
  emptyHint?: string;
  loadingMore?: boolean;
  canLoadMore?: boolean;
  onLoadMore?: () => void;
  onDelete?: (entry: Entry) => void;
  onEdit?: (entry: Entry) => void;
  onRestore?: (entry: Entry) => void;
  media?: Record<string, MediaInfo[]>;
  onMediaDeleted?: (entryRef: string, mediaRef: string) => void;
  onMediaError?: (message: string) => void;
};

/**
 * The stream itself, hung on one fine vertical axis: a month pill with its rule, a day heading (with 今天 on today),
 * then that day's records as time · node · card rows. Groups follow the order the server returned, so nothing is
 * re-sorted here. From 640px up the clock time sits in its own column left of the axis; below that the time moves into
 * the card and the axis hangs on the left edge.
 */
export function Timeline({
  months,
  empty,
  emptyTitle = "时间线还是空的",
  emptyHint = "写下第一件已经发生的小事，它就会在这里落位。",
  loadingMore = false,
  canLoadMore = false,
  onLoadMore,
  onDelete,
  onEdit,
  onRestore,
  media = {},
  onMediaDeleted,
  onMediaError,
}: TimelineProps) {
  if (empty) {
    return (
      <div className="rounded-card border border-dashed border-line bg-paper/60 px-8 py-16 text-center">
        <span className="mx-auto grid size-9 place-items-center rounded-lg bg-subtle text-leaf">
          <ClockCounterClockwise size={18} />
        </span>
        <p className="mt-3 text-base font-medium text-ink">{emptyTitle}</p>
        <p className="mt-1 text-sm text-soft">{emptyHint}</p>
      </div>
    );
  }

  return (
    <div className="relative flex flex-col gap-6 pb-8">
      {/* The continuous axis. Behind the headings, which carry their own background. */}
      <div aria-hidden="true" className="absolute top-6 bottom-14 left-[13.25px] z-0 w-[1.5px] bg-line sm:left-[101.25px]" />

      {months.map((month) => (
        <section key={`${month.key}-${month.days[0]?.date ?? ""}`} aria-label={month.label} className="relative z-10 flex flex-col gap-3">
          <div className="flex items-center gap-3">
            <h2 className="rounded-tag bg-line px-2.5 py-0.5 text-base leading-6 font-medium tracking-wide text-ink">{month.label}</h2>
            <span className="h-px flex-1 bg-line" />
          </div>

          {month.days.map((day) => (
            <div key={day.date} className="flex flex-col gap-3">
              <div className="flex items-center gap-3 pl-2">
                <h3 className="meta-numeric rounded-tag bg-canvas py-0.5 pr-2 font-semibold text-ink">
                  {day.label} {day.weekday}
                </h3>
                {day.today && <span className="rounded-tag bg-leaf px-1.5 text-[11px] leading-4 font-medium text-white">今天</span>}
              </div>
              {day.entries.map((entry) => (
                <EntryCard
                  key={entry.entryRef}
                  entry={entry}
                  onDelete={onDelete}
                  onEdit={onEdit}
                  onRestore={onRestore}
                  media={media[entry.entryRef]}
                  onMediaDeleted={(mediaRef) => onMediaDeleted?.(entry.entryRef, mediaRef)}
                  onMediaError={onMediaError}
                />
              ))}
            </div>
          ))}
        </section>
      ))}

      {canLoadMore && onLoadMore !== undefined && (
        <div className="relative z-10 flex justify-center">
          <button
            type="button"
            onClick={onLoadMore}
            disabled={loadingMore}
            className="flex items-center gap-1.5 rounded-card bg-subtle px-5 py-2 text-[13px] leading-[18px] font-medium text-ink transition-colors hover:bg-line disabled:opacity-60"
          >
            <ClockCounterClockwise size={18} />
            {loadingMore ? "正在加载…" : "加载更多记录"}
          </button>
        </div>
      )}
    </div>
  );
}
