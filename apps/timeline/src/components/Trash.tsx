import type { Entry } from "@kongken/api-client/bff/timeline/v1/timeline_pb";
import { Timeline } from "@/components/Timeline.tsx";
import { groupEntries } from "@/lib/entries.ts";

type TrashProps = {
  entries: Entry[];
  onRestore: (entry: Entry) => void;
};

/** 回收站: the same records, from the same list, that the server has marked deleted, on the same axis. */
export function Trash({ entries, onRestore }: TrashProps) {
  return (
    <Timeline
      months={groupEntries(entries)}
      empty={entries.length === 0}
      emptyTitle="回收站是空的"
      emptyHint="时间线上删除的记录会先落到这里，可以恢复。"
      onRestore={onRestore}
    />
  );
}
