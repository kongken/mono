import type { Entry } from "@kongken/api-client/bff/timeline/v1/timeline_pb";
import { EntryCard } from "@/components/EntryCard.tsx";
import { groupEntries } from "@/lib/entries.ts";

type TrashProps = {
  entries: Entry[];
  onRestore: (entry: Entry) => void;
};

/** 回收站: the same records, from the same list, that the server has marked deleted. */
export function Trash({ entries, onRestore }: TrashProps) {
  if (entries.length === 0) {
    return (
      <div className="mt-10 rounded-card border border-dashed border-outline-variant bg-card/70 px-8 py-16 text-center">
        <p className="text-[15px] font-medium text-card-foreground">回收站是空的</p>
        <p className="mt-1.5 text-sm text-muted-foreground">时间线上删除的记录会先落到这里，可以恢复。</p>
      </div>
    );
  }

  return (
    <div className="mt-8 flex flex-col gap-2">
      {groupEntries(entries).map((month) => (
        <section key={`${month.key}-${month.days[0]?.date ?? ""}`} aria-label={month.label}>
          <h2 className="mt-7 mb-1 text-[11px] font-semibold tracking-[0.18em] text-muted-foreground uppercase first:mt-0">{month.label}</h2>
          {month.days.map((day) => (
            <div key={day.date} className="mt-4">
              <div className="mb-2.5 flex items-center gap-2.5">
                <h3 className="text-sm font-semibold text-on-surface">{day.label}</h3>
                <span className="text-xs text-muted-foreground">{day.weekday}</span>
                <span className="h-px flex-1 bg-outline-variant/60" />
              </div>
              <div className="flex flex-col gap-2">
                {day.entries.map((entry) => (
                  <EntryCard key={entry.entryRef} entry={entry} onRestore={onRestore} />
                ))}
              </div>
            </div>
          ))}
        </section>
      ))}
    </div>
  );
}
