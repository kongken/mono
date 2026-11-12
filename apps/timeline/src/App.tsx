import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Plus, WarningCircle } from "@phosphor-icons/react";
import type { AccountProfile } from "@kongken/account/api";
import type { Entry } from "@kongken/api-client/bff/timeline/v1/timeline_pb";
import type { MediaInfo } from "@kongken/api-client/bff/timeline/v1/media_pb";
import { Composer } from "@/components/Composer.tsx";
import { CategoryPanel } from "@/components/CategoryPanel.tsx";
import { FilterBar } from "@/components/FilterBar.tsx";
import { Sidebar } from "@/components/Sidebar.tsx";
import { Timeline } from "@/components/Timeline.tsx";
import { TopBar } from "@/components/TopBar.tsx";
import { Trash } from "@/components/Trash.tsx";
import { SettingsPanel } from "@/components/SettingsPanel.tsx";
import { explainUploadFailure, type UploadItem } from "@/lib/attachments.ts";
import { resolveOzid } from "@/lib/config.ts";
import {
  EMPTY_FILTERS,
  currencyDigits,
  emptyDraft,
  groupEntries,
  listRequest,
  localToday,
  validateDraft,
  type DraftEntry,
  type FilterState,
  type View,
} from "@/lib/entries.ts";
import { describeError, isUnauthenticated } from "@/lib/errors.ts";
import { account, media, mutationID, setTransportAccount, timeline, write } from "@/lib/rpc.ts";

function uploadSessionKey(entryRef: string, file: File): string {
  return `${entryRef}:${file.name}:${file.size}:${file.lastModified}:${file.type}`;
}

function putUpload(url: string, file: File, headers: Record<string, string>, onProgress: (progress: number) => void): Promise<void> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open("PUT", url);
    for (const [name, value] of Object.entries(headers)) request.setRequestHeader(name, value);
    request.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress(Math.round((event.loaded / event.total) * 100));
    };
    request.onload = () => request.status >= 200 && request.status < 300 ? resolve() : reject(new Error(`上传失败（HTTP ${request.status}）`));
    request.onerror = () => reject(new Error("上传失败，请重试"));
    onProgress(1);
    request.send(file);
  });
}

export default function App() {
  const [view, setView] = useState<View>("timeline");
  const [pending, setPending] = useState<FilterState>(EMPTY_FILTERS);
  const [applied, setApplied] = useState<FilterState>(EMPTY_FILTERS);
  const [entries, setEntries] = useState<Entry[]>([]);
  const [categoryNames, setCategoryNames] = useState<string[]>([]);
  const [attachments, setAttachments] = useState<Record<string, MediaInfo[]>>({});
  const [nextToken, setNextToken] = useState("");
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [profile, setProfile] = useState<AccountProfile | null>(null);
  const [signedOut, setSignedOut] = useState(false);
  const [signingOut, setSigningOut] = useState(false);
  const [composerOpen, setComposerOpen] = useState(false);
  const [composerSeed, setComposerSeed] = useState<DraftEntry>(() => emptyDraft());
  const [editingEntry, setEditingEntry] = useState<Entry | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  // The files of the record currently being saved, and which record they belong to: a retry uploads the failed ones
  // into the record already written instead of writing a second one.
  const [uploads, setUploads] = useState<UploadItem[]>([]);
  const pendingEntryRef = useRef<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const uploadSessions = useRef(new Map<string, { mutationId: string; mediaRef: string }>());

  // Only the newest list answer may land: typing in the search box must not let an older page overwrite a newer one.
  const sequence = useRef(0);
  const signedIn = profile !== null;
  const ozid = useMemo(() => resolveOzid({ VITE_OZID_ORIGIN: import.meta.env.VITE_OZID_ORIGIN as string | undefined }), []);

  // The filter bar reacts at once, the list a beat later, so a search word is not one request per keystroke.
  const debounce = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (debounce.current !== null) clearTimeout(debounce.current);
    },
    [],
  );
  const changeFilters = (next: FilterState) => {
    setPending(next);
    if (debounce.current !== null) clearTimeout(debounce.current);
    if (next.query !== applied.query) debounce.current = setTimeout(() => setApplied(next), 250);
    else setApplied(next);
  };

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const me = await account.me();
        if (cancelled) return;
        setProfile(me);
        setSignedOut(false);
        setTransportAccount(me.user.id);
      } catch (error) {
        if (cancelled) return;
        if (isUnauthenticated(error)) {
          setSignedOut(true);
          setProfile(null);
          setEntries([]);
          return;
        }
        setListError(describeError(error, "读取账号失败"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!signedIn) return;
    void timeline.listCategories({}).then((response) => setCategoryNames(response.categories.filter((category) => category.active).map((category) => category.name))).catch(() => undefined);
  }, [signedIn]);

  const load = useCallback(
    async (includeDeleted: boolean, pageToken = "") => {
      const mine = ++sequence.current;
      if (pageToken === "") setLoading(true);
      else setLoadingMore(true);
      setListError(null);
      try {
        const response = await timeline.listEntries({ ...listRequest(applied, includeDeleted), pageToken, pageSize: 100 });
        if (mine !== sequence.current) return;
        setEntries((current) => (pageToken === "" ? response.entries : [...current, ...response.entries]));
        // One list per record; a failed media read is reported as "no attachments" for that record rather than
        // blanking the timeline: the records are the page, the attachments hang off them.
        const listed = await Promise.all(
          response.entries.map(async (entry) => {
            try {
              const answer = await media.listMedia({ entryRef: entry.entryRef });
              return [entry.entryRef, answer.media] as const;
            } catch {
              return [entry.entryRef, [] as MediaInfo[]] as const;
            }
          }),
        );
        setAttachments((current) => ({ ...current, ...Object.fromEntries(listed) }));
        setNextToken(response.nextPageToken);
      } catch (error) {
        if (mine === sequence.current) setListError(describeError(error, "读取时间线失败"));
      } finally {
        if (mine === sequence.current) {
          setLoading(false);
          setLoadingMore(false);
        }
      }
    },
    [applied],
  );

  useEffect(() => {
    if (!signedIn) return;
    // A new view or filter means a new request: the loading flag flips now, before the first await.
    // oxlint-disable-next-line react/set-state-in-effect
    void load(view === "trash");
  }, [load, signedIn, view]);

  const categories = useMemo(() => [...new Set([...categoryNames, ...entries.map((entry) => entry.category).filter((category) => category !== "")])].sort(), [categoryNames, entries]);
  const visible = useMemo(
    () => entries.filter((entry) => (view === "trash" ? entry.deletedAt !== undefined : entry.deletedAt === undefined)),
    [entries, view],
  );
  const months = useMemo(() => groupEntries(visible), [visible]);
  // A failed read is an error, not an empty record book: with nothing loaded the error is the whole page; a
  // refresh that fails over records already on screen keeps them and shows the error above.
  const readFailed = listError !== null && visible.length === 0;

  const today = localToday();

  const openComposer = () => {
    setEditingEntry(null);
    setComposerSeed(emptyDraft());
    setSaveError(null);
    setUploads([]);
    pendingEntryRef.current = null;
    uploadSessions.current.clear();
    setComposerOpen(true);
  };

  const openEditor = (entry: Entry) => {
    const digits = currencyDigits(entry.currency);
    const scale = 10n ** BigInt(digits);
    const whole = entry.amountMinor / scale;
    const fraction = (entry.amountMinor % scale).toString().padStart(digits, "0").replace(/0+$/, "");
    setEditingEntry(entry);
    setComposerSeed({
      title: entry.title,
      date: entry.date,
      time: entry.time,
      timezone: entry.timezone,
      category: entry.category,
      location: entry.location,
      amount: entry.amountPresent ? `${whole}${fraction === "" ? "" : `.${fraction}`}` : "",
      currency: entry.currency || "CNY",
      note: entry.note,
    });
    setSaveError(null);
    setUploads([]);
    pendingEntryRef.current = entry.entryRef;
    uploadSessions.current.clear();
    setComposerOpen(true);
  };

  const closeComposer = () => {
    setComposerOpen(false);
    setUploads([]);
    pendingEntryRef.current = null;
    uploadSessions.current.clear();
  };

  const uploadOne = async (entryRef: string, file: File, index: number) => {
    const key = uploadSessionKey(entryRef, file);
    const session = uploadSessions.current.get(key);
    const uploadMutationId = session?.mutationId ?? mutationID();
    const prepared = await write(() =>
      media.prepareUpload({ entryRef, filename: file.name, contentType: file.type, sizeBytes: BigInt(file.size), mutationId: uploadMutationId }),
    );
    if (prepared.uploadUrl === "" || prepared.media === undefined) throw new Error("服务端没有给出上传地址");
    uploadSessions.current.set(key, { mutationId: uploadMutationId, mediaRef: prepared.media.mediaRef });
    const setProgress = (progress: number) => setUploads((current) => current.map((item, at) => (at === index ? { ...item, progress } : item)));
    await putUpload(prepared.uploadUrl, file, { "Content-Type": file.type, ...prepared.headers }, setProgress);
    await write(() => media.completeUpload({ mediaRef: prepared.media!.mediaRef, mutationId: uploadMutationId }));
  };



  /**
   * Runs the queue of files against an already-written record, one at a time, and reports how many failed. The
   * record is never written again: a retry only re-signs the files that did not make it.
   */
  const runUploads = async (entryRef: string, items: UploadItem[], queue: number[]): Promise<number> => {
    setUploads(items);
    let failed = 0;
    for (const index of queue) {
      setUploads((current) => current.map((item, at) => (at === index ? { ...item, status: "uploading", progress: 0, message: undefined } : item)));
      try {
        await uploadOne(entryRef, items[index]!.file, index);
        setUploads((current) => current.map((item, at) => (at === index ? { ...item, status: "done" } : item)));
      } catch (error) {
        failed += 1;
        const message = explainUploadFailure(describeError(error, "上传失败"));
        setUploads((current) => current.map((item, at) => (at === index ? { ...item, status: "failed", message } : item)));
      }
    }
    return failed;
  };

  const submitDraft = async (draft: DraftEntry, files: File[] = []) => {
    const checked = validateDraft(draft);
    if (checked.ok === false) {
      setSaveError("请检查标红的字段。");
      return;
    }
    setSaving(true);
    setSaveError(null);
    setNotice(null);
    try {
      let entryRef = pendingEntryRef.current;
      if (entryRef === null) {
        const created = await write(() =>
          timeline.createEntry({
            mutationId: mutationID(),
            entry: {
              title: draft.title.trim(),
              date: draft.date,
              time: draft.time,
              timezone: draft.timezone,
              category: draft.category.trim(),
              location: draft.location.trim(),
              amountMinor: checked.amountMinor,
              amountPresent: draft.amount.trim() !== "",
              currency: draft.amount.trim() === "" ? "" : draft.currency,
              note: draft.note.trim(),
            },
          }),
        );
        entryRef = created.entry?.entryRef ?? "";
        if (entryRef === "") throw new Error("记录已保存，但没有拿到它的引用");
        pendingEntryRef.current = entryRef;
      } else if (editingEntry !== null) {
        await write(() =>
          timeline.updateEntry({
            mutationId: mutationID(),
            expectedRevision: editingEntry.revision,
            entry: {
				entryRef: editingEntry.entryRef,
              title: draft.title.trim(), date: draft.date, time: draft.time, timezone: draft.timezone,
              category: draft.category.trim(), location: draft.location.trim(), amountMinor: checked.amountMinor,
              amountPresent: draft.amount.trim() !== "",
              currency: draft.amount.trim() === "" ? "" : draft.currency, note: draft.note.trim(),
            },
          }),
        );
      }

      const items: UploadItem[] = files.map((file) => ({ file, status: "pending" }));
      const failed = await runUploads(entryRef, items, items.map((_item, index) => index));
      if (failed > 0) {
        setSaveError(`记录已经保存，但有 ${failed} 个附件没传上去。点「重试上传」只补传这些附件，记录不会再写一次。`);
        return;
      }
      closeComposer();
      await load(view === "trash");
    } catch (error) {
      setSaveError(describeError(error, "保存失败"));
    } finally {
      setSaving(false);
    }
  };

  /** Retries only the files that failed, against the record the first attempt already wrote. */
  const retryUploads = async () => {
    const entryRef = pendingEntryRef.current;
    if (entryRef === null) return;
    const queue = uploads.flatMap((item, index) => (item.status === "failed" ? [index] : []));
    if (queue.length === 0) return;
    setSaving(true);
    setSaveError(null);
    try {
      const items = uploads.map((item) => (item.status === "failed" ? { ...item, status: "pending" as const, message: undefined } : item));
      const failed = await runUploads(entryRef, items, queue);
      if (failed > 0) {
        setSaveError(`还有 ${failed} 个附件没传上去。可以再试一次，或者先关掉这里，记录已经保存了。`);
        return;
      }
      closeComposer();
      await load(view === "trash");
    } finally {
      setSaving(false);
    }
  };

  /** One intent, one mutation ID: a repeated click keeps it, so the server replays instead of writing twice. */
  const mutate = async (call: () => Promise<unknown>, onDone: () => Promise<void>) => {
    setActionError(null);
    try {
      await write(call);
      await onDone();
    } catch (error) {
      setActionError(describeError(error, "操作失败"));
    }
  };

  const removeEntry = (entry: Entry) =>
    mutate(
      () =>
        timeline.deleteEntry({
          entryRef: entry.entryRef,
          expectedRevision: entry.revision,
          mutationId: mutationID(),
          permanent: false,
        }),
      () => load(view === "trash"),
    );

  const restoreEntry = (entry: Entry) =>
    mutate(
      () => timeline.restoreEntry({ entryRef: entry.entryRef, expectedRevision: entry.revision, mutationId: mutationID() }),
      () => load(true),
    );

  const signOut = async () => {
    setSigningOut(true);
    setActionError(null);
    try {
      await write(() => account.logout());
      setProfile(null);
      setSignedOut(true);
      setEntries([]);
      setTransportAccount(undefined);
    } catch (error) {
      setActionError(describeError(error, "退出登录失败"));
    } finally {
      setSigningOut(false);
    }
  };

  return (
    <div className="flex min-h-screen bg-surface">
      <Sidebar
        view={view}
        onView={setView}
        profile={profile}
        signedOut={signedOut}
        loginUrl={ozid.ok ? ozid.login : null}
        signingOut={signingOut}
        onSignOut={() => void signOut()}
      />

      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar query={pending.query} onQuery={(query) => changeFilters({ ...pending, query })} onWrite={openComposer} total={visible.length} />

        <main className="flex-1 px-10 pt-8 pb-16">
          <div className="mx-auto w-full max-w-[740px]">
            <div className="flex items-start justify-between gap-6">
              <div>
                <p className="text-[11px] font-semibold tracking-[0.2em] text-muted-foreground uppercase">Timeline</p>
                <h1 className="mt-1.5 text-[22px] leading-8 font-semibold tracking-tight text-on-surface">
                  {view === "trash" ? "回收站" : `${today.slice(0, 4)} 年 阳历记录本`}
                </h1>
                <p className="mt-2 text-sm text-muted-foreground">
                  {view === "trash" ? "删除的记录先放在这里，可以恢复，也可以彻底删除。" : "记录生活中已经发生的小事，在时间流里重逢。"}
                </p>
              </div>
              {view === "timeline" && (
                <button
                  type="button"
                  onClick={openComposer}
                  className="mt-1 inline-flex shrink-0 items-center gap-1.5 rounded-md bg-primary-container px-4 py-2.5 text-sm font-semibold text-on-primary shadow-[0_2px_8px_rgba(58,96,58,0.24)] transition-colors hover:bg-primary"
                >
                  <Plus size={15} weight="bold" />
                  记一笔
                </button>
              )}
            </div>

            {view === "timeline" && <FilterBar filters={pending} onChange={changeFilters} categories={categories} />}

            {view === "categories" && <CategoryPanel />}
            {view === "settings" && <SettingsPanel />}

            {actionError !== null && (
              <p className="mt-6 flex items-start gap-1.5 rounded-md bg-error-container px-3 py-2 text-xs text-on-error-container">
                <WarningCircle size={14} className="mt-px shrink-0" />
                {actionError}
              </p>
            )}

            {notice !== null && (
              <p className="mt-6 rounded-md bg-surface-container px-3 py-2 text-xs text-primary">{notice}</p>
            )}

            {signedOut && (
              <div className="mt-10 rounded-card border border-outline-variant bg-card px-8 py-12 text-center">
                <p className="text-[15px] font-medium text-card-foreground">请先登录</p>
                <p className="mt-1.5 text-sm text-muted-foreground">Timeline 的记录属于你的 OzID 账号。</p>
                {ozid.ok && (
                  <a
                    href={ozid.login}
                    className="mt-5 inline-flex rounded-md bg-primary-container px-4 py-2 text-sm font-semibold text-on-primary transition-colors hover:bg-primary"
                  >
                    去 OzID 登录
                  </a>
                )}
              </div>
            )}

            {!signedOut && listError !== null && (
              <div className="mt-6 flex items-center gap-3 rounded-md bg-error-container px-3 py-2 text-xs text-on-error-container">
                <WarningCircle size={14} className="shrink-0" />
                <span className="flex-1">{listError}</span>
                <button type="button" onClick={() => void load(view === "trash")} className="rounded-md bg-destructive/10 px-2 py-1 font-medium text-destructive">
                  重试
                </button>
              </div>
            )}

            {!signedOut && loading && visible.length === 0 && <p className="mt-10 text-sm text-muted-foreground">正在读取记录…</p>}

            {!signedOut && !loading && !readFailed && view === "timeline" && (
              <Timeline
                months={months}
                empty={visible.length === 0}
                loadingMore={loadingMore}
                canLoadMore={nextToken !== ""}
                onLoadMore={() => void load(false, nextToken)}
                media={attachments}
                onDelete={(entry) => void removeEntry(entry)}
                onEdit={openEditor}
                onMediaDeleted={(entryRef, mediaRef) => {
                  setAttachments((current) => ({ ...current, [entryRef]: (current[entryRef] ?? []).filter((item) => item.mediaRef !== mediaRef) }));
                  setNotice("附件已删除，存储空间会在后台回收。");
                }}
                onMediaError={setActionError}
              />
            )}

            {!signedOut && !loading && !readFailed && view === "trash" && (
              <Trash entries={visible} onRestore={(entry) => void restoreEntry(entry)} />
            )}
          </div>
        </main>
      </div>

      {composerOpen && (
        <Composer
          open={composerOpen}
          initial={composerSeed}
          mode={editingEntry === null ? "create" : "edit"}
          categories={categories}
          saving={saving}
          error={saveError}
          uploads={uploads}
          onRetry={() => void retryUploads()}
          onClose={closeComposer}
          onSubmit={(draft, files) => void submitDraft(draft, files)}
        />
      )}
    </div>
  );
}
