import type { Entry } from "@kongken/api-client/bff/timeline/v1/timeline_pb";
import type { MediaInfo } from "@kongken/api-client/bff/timeline/v1/media_pb";
import { EntryCard } from "@/components/EntryCard.tsx";
import type { MonthGroup } from "@/lib/entries.ts";

type TimelineProps = {
  months: MonthGroup[];
  empty: boolean;
  loadingMore: boolean;
  canLoadMore: boolean;
  onLoadMore: () => void;
  onDelete: (entry: Entry) => void;
  onEdit: (entry: Entry) => void;
  media?: Record<string, MediaInfo[]>;
  onMediaDeleted?: (entryRef: string, mediaRef: string) => void;
  onMediaError?: (message: string) => void;
  children?: React.ReactNode;
};

/**
 * The list itself: a month rule, then a day rule (with 今天 on today), then that day's records. Groups follow the
 * order the server returned, so nothing is re-sorted here.
 */
export function Timeline({ months, empty, loadingMore, canLoadMore, onLoadMore, onDelete, onEdit, media = {}, onMediaDeleted, onMediaError, children }: TimelineProps) {
  if (empty) {
    return (
      <div className="mt-10 rounded-card border border-dashed border-outline-variant bg-card/70 px-8 py-16 text-center">
        <p className="text-[15px] font-medium text-card-foreground">时间线还是空的</p>
        <p className="mt-1.5 text-sm text-muted-foreground">写下第一件已经发生的小事，它就会在这里落位。</p>
      </div>
    );
  }

  return (
    <div className="mt-8 flex flex-col gap-2">
      {months.map((month) => (
        <section key={`${month.key}-${month.days[0]?.date ?? ""}`} aria-label={month.label}>
          <h2 className="mt-7 mb-1 text-[11px] font-semibold tracking-[0.18em] text-muted-foreground uppercase first:mt-0">{month.label}</h2>
          {month.days.map((day) => (
            <div key={day.date} className="mt-4">
              <div className="mb-2.5 flex items-center gap-2.5">
                <h3 className="text-sm font-semibold text-on-surface">{day.label}</h3>
                <span className="text-xs text-muted-foreground">{day.weekday}</span>
                {day.today && <span className="rounded-full bg-primary px-2 py-0.5 text-[10px] font-medium text-on-primary">今天</span>}
                <span className="h-px flex-1 bg-outline-variant/60" />
              </div>
              <div className="flex flex-col gap-2">
                {day.entries.map((entry) => (
                  <EntryCard
                    key={entry.entryRef}
                    entry={entry}
                    onDelete={onDelete}
                    onEdit={onEdit}
                    media={media[entry.entryRef]}
                    onMediaDeleted={(mediaRef) => onMediaDeleted?.(entry.entryRef, mediaRef)}
                    onMediaError={onMediaError}
                  />
                ))}
              </div>
            </div>
          ))}
        </section>
      ))}

      {children}

      {canLoadMore && (
        <button
          type="button"
          onClick={onLoadMore}
          disabled={loadingMore}
          className="mx-auto mt-8 rounded-md border border-outline-variant bg-card px-5 py-2 text-sm text-card-foreground transition-colors hover:bg-muted disabled:opacity-60"
        >
          {loadingMore ? "正在加载…" : "加载更多记录"}
        </button>
      )}
    </div>
  );
}
