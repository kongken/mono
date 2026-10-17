import { createClient } from "@connectrpc/connect";
import { SearchService } from "@kongken/api-client/bff/tripmap/v1/search_pb";
import type { NoteMatch } from "@/lib/search";
import { transport } from "./transport";

const raw = transport ? createClient(SearchService, transport) : null;
export type NoteSearchPage = {
  matches: NoteMatch[];
  revision: bigint;
  nextOffset: number;
  hasMore: boolean;
};
export async function searchCloudNotes(
  query: string,
  provinceCode: string,
  signal: AbortSignal,
  offset = 0,
  revision?: bigint,
): Promise<NoteSearchPage> {
  if (!raw) throw new Error("backend not configured");
  const res = await raw.searchNotes(
    { query, provinceCode, offset, limit: 20, expectedRevision: revision },
    { signal },
  );
  return {
    matches: res.matches.map((m) => ({
      regionCode: m.regionCode,
      regionName: m.regionName,
      provinceCode: m.provinceCode,
      tripId: m.tripId,
      date: m.date,
      note: m.note,
    })),
    revision: res.revision,
    nextOffset: res.nextOffset,
    hasMore: res.hasMore,
  };
}
