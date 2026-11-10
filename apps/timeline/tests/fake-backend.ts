import type { Page, Route } from "@playwright/test";

/**
 * A small in-memory BFF for the Timeline application: the account procedures the page needs to learn who it is
 * and get a CSRF token, and the six record procedures. It is installed with page.route; nothing leaves the test.
 * The answers are Connect's JSON encoding, so a record's int64 fields are strings.
 */

export type EntryJson = {
  id: string;
  entryRef: string;
  title: string;
  date: string;
  time: string;
  timezone: string;
  category: string;
  location: string;
  amountMinor: string;
  currency: string;
  note: string;
  url: string;
  urlTitle: string;
  urlNote: string;
  revision: string;
  createdAt: string;
  updatedAt: string;
  deletedAt?: string;
};

export type MediaJson = { mediaRef: string; entryRef: string; filename: string; contentType: string; sizeBytes: string; status: string };

export type FakeTimeline = ReturnType<typeof fakeBackend>["state"];

/** A record with every field the server always sets, so a test only writes what it cares about. */
export function entryJson(fields: Partial<EntryJson> & { id: string; title: string; date: string }): EntryJson {
  const now = "2026-11-07T04:30:00Z";
  return {
    entryRef: `ref-${fields.id}`,
    time: "",
    timezone: "Asia/Shanghai",
    category: "",
    location: "",
    amountMinor: "0",
    currency: "",
    note: "",
    url: "",
    urlTitle: "",
    urlNote: "",
    revision: "1",
    createdAt: now,
    updatedAt: now,
    ...fields,
  };
}

/** The procedures that write: everything else in this fake is a read, even when it is a POST. */
const WRITES = new Set([
  "bff.timeline.v1.TimelineService/CreateEntry",
  "bff.timeline.v1.TimelineService/DeleteEntry",
  "bff.timeline.v1.TimelineService/RestoreEntry",
  "bff.timeline.v1.TimelineService/PurgeEntry",
]);

export async function fakeBackend(page: Page, opts: { signedIn?: boolean; entries?: EntryJson[] } = {}) {
  const state = {
    user: opts.signedIn === false ? null : { id: "u1", displayName: "茶隐居士" },
    csrfIssued: 0,
    listFailures: 0,
    calls: [] as string[],
    /** Every write that reached the service: the CSRF header and the message. */
    writes: [] as { procedure: string; csrf: string | undefined; body: Record<string, any> }[],
    entries: [...(opts.entries ?? [])],
    media: [] as MediaJson[],
  };

  const failure = (status: number, code: string, message: string) => ({ status, body: { code, message } });
  const unauth = () => (state.user === null ? failure(401, "unauthenticated", "sign in required") : null);
  const matches = (row: EntryJson, body: Record<string, any>) => {
    if (body.includeDeleted !== true && row.deletedAt !== undefined) return false;
    if (body.category && row.category !== body.category) return false;
    if (body.fromDate && row.date < body.fromDate) return false;
    if (body.toDate && row.date > body.toDate) return false;
    if (body.query) {
      const needle = String(body.query).toLowerCase();
      if (![row.title, row.note, row.location].some((field) => field.toLowerCase().includes(needle))) return false;
    }
    return true;
  };

  const sorted = () => [...state.entries].sort((a, b) => (a.date === b.date ? (a.id < b.id ? -1 : 1) : a.date < b.date ? 1 : -1));

  const handlers: Record<string, (body: Record<string, any>) => { status?: number; body: unknown }> = {
    "bff.v1.AuthService/Me": () => unauth() ?? { body: { user: { id: state.user!.id, displayName: state.user!.displayName }, roles: [] } },
    "bff.v1.AuthService/IssueCSRFToken": () => ({ body: { token: `csrf-${++state.csrfIssued}` } }),
    "bff.timeline.v1.TimelineService/ListEntries": (body) => {
      if (state.listFailures > 0) {
        state.listFailures -= 1;
        return failure(503, "unavailable", "timeline service unavailable");
      }
      return unauth() ?? { body: { entries: sorted().filter((row) => matches(row, body)), nextPageToken: "" } };
    },
    "bff.timeline.v1.MediaService/PrepareUpload": (body) => {
      const item = { mediaRef: `med-${state.media.length + 1}`, entryRef: String(body.entryRef ?? ""), filename: String(body.filename ?? ""), contentType: String(body.contentType ?? ""), sizeBytes: String(body.sizeBytes ?? "0"), status: "pending" };
      state.media.push(item);
      return { body: { media: item, uploadUrl: `/api/upload/${item.mediaRef}`, headers: {} } };
    },
    "bff.timeline.v1.MediaService/CompleteUpload": (body) => { const item = state.media.find((candidate) => candidate.mediaRef === body.mediaRef); if (!item) return failure(404, "not_found", "media not found"); item.status = "ready"; return { body: { media: item } }; },
    "bff.timeline.v1.MediaService/ListMedia": (body) => unauth() ?? { body: { media: state.media.filter((item) => item.entryRef === body.entryRef && item.status === "ready") } },
    "bff.timeline.v1.MediaService/ReadMedia": (body) => { const item = state.media.find((candidate) => candidate.mediaRef === body.mediaRef && candidate.status === "ready"); return item ? { body: { data: "", contentType: item.contentType, filename: item.filename } } : failure(404, "not_found", "media not found"); },
    "bff.timeline.v1.MediaService/DeleteMedia": (body) => { const index = state.media.findIndex((candidate) => candidate.mediaRef === body.mediaRef); if (index < 0) return failure(404, "not_found", "media not found"); state.media.splice(index, 1); return { body: { receipt: { mutationId: body.mutationId ?? "", operation: "media_delete", resultRevision: "1", replayed: false } } }; },
    "bff.timeline.v1.TimelineService/CreateEntry": (body) => {
      const denied = unauth();
      if (denied) return denied;
      const input = body.entry ?? {};
      if (typeof input.title !== "string" || input.title.trim() === "") return failure(400, "invalid_argument", "title is required");
      const created = entryJson({
        id: `e${state.entries.length + 10}`,
        title: input.title,
        date: input.date ?? "2026-11-07",
        time: input.time ?? "",
        timezone: input.timezone ?? "Asia/Shanghai",
        category: input.category ?? "",
        location: input.location ?? "",
        amountMinor: String(input.amountMinor ?? "0"),
        currency: input.currency ?? "",
        note: input.note ?? "",
      });
      state.entries.push(created);
      return { body: { entry: created, receipt: { mutationId: body.mutationId ?? "", operation: "create", resultRevision: "1", replayed: false } } };
    },
    "bff.timeline.v1.TimelineService/DeleteEntry": (body) => {
      const denied = unauth();
      if (denied) return denied;
      const row = state.entries.find((candidate) => candidate.id === body.id);
      if (!row) return failure(404, "not_found", "entry not found");
      row.deletedAt = "2026-11-08T02:00:00Z";
      row.revision = String(Number(row.revision) + 1);
      return { body: { entry: row, receipt: { mutationId: body.mutationId ?? "", operation: "delete", resultRevision: row.revision, replayed: false } } };
    },
    "bff.timeline.v1.TimelineService/RestoreEntry": (body) => {
      const denied = unauth();
      if (denied) return denied;
      const row = state.entries.find((candidate) => candidate.id === body.id);
      if (!row) return failure(404, "not_found", "entry not found");
      delete row.deletedAt;
      row.revision = String(Number(row.revision) + 1);
      return { body: { entry: row, receipt: { mutationId: body.mutationId ?? "", operation: "restore", resultRevision: row.revision, replayed: false } } };
    },
    "bff.timeline.v1.TimelineService/PurgeEntry": (body) => {
      const denied = unauth();
      if (denied) return denied;
      state.entries = state.entries.filter((candidate) => candidate.id !== body.id);
      return { body: { receipt: { mutationId: body.mutationId ?? "", operation: "purge", resultRevision: "0", replayed: false } } };
    },
  };

  // Every procedure of the two services, by `service/method`, on the preview origin that doubles as the API origin.
  await page.route("**/api/**", (route) => answer(route));

  async function answer(route: Route) {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname.startsWith("/api/upload/")) {
      await route.fulfill({ status: 200, body: "" });
      return;
    }
    const match = /^\/api\/([\w.]+)\/(\w+)$/.exec(pathname);
    if (match === null) {
      await route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ code: "unimplemented", message: "not found" }) });
      return;
    }
    const procedure = `${match[1]}/${match[2]}`;
    const handler = handlers[procedure];
    state.calls.push(procedure);
    if (handler === undefined) {
      await route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ code: "unimplemented", message: procedure }) });
      return;
    }
    const body = (route.request().postDataJSON() ?? {}) as Record<string, any>;
    if (WRITES.has(procedure)) {
      state.writes.push({ procedure, csrf: route.request().headers()["x-csrf-token"], body });
    }
    const outcome = handler(body);
    await route.fulfill({ status: outcome.status ?? 200, contentType: "application/json", body: JSON.stringify(outcome.body) });
  }

  return { state };
}
