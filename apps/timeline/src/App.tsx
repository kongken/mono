import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { Plus, WarningCircle } from "@phosphor-icons/react";
import type { AccountProfile } from "@kongken/account/api";
import type { Entry } from "@kongken/api-client/bff/timeline/v1/timeline_pb";
import { Composer } from "@/components/Composer.tsx";
import { FilterBar } from "@/components/FilterBar.tsx";
import { Sidebar } from "@/components/Sidebar.tsx";
import { Timeline } from "@/components/Timeline.tsx";
import { TopBar } from "@/components/TopBar.tsx";
import { Trash } from "@/components/Trash.tsx";
import { resolveOzid } from "@/lib/config.ts";
import {
  EMPTY_FILTERS,
  emptyDraft,
  groupEntries,
  listRequest,
  localToday,
  validateDraft,
  type DraftEntry,
  type FilterState,
  type View,
} from "@/lib/entries.ts";
import { account, mutationID, setTransportAccount, timeline, write } from "@/lib/rpc.ts";

/** One sentence a person can act on, whatever the transport threw. */
function describeError(error: unknown, fallback: string): string {
  const e = ConnectError.from(error);
  switch (e.code) {
    case Code.Unauthenticated:
      return "登录已失效，请重新登录后再试。";
    case Code.PermissionDenied:
      return "请求被拒绝：可能是登录状态或来源校验过期，刷新页面后重试。";
    case Code.Aborted:
      return "这条记录刚在别处改过，刷新后再试。";
    case Code.NotFound:
      return "这条记录已经不在了。";
    default:
      return e.rawMessage === "" ? fallback : e.rawMessage;
  }
}

export default function App() {
  const [view, setView] = useState<View>("timeline");
  const [pending, setPending] = useState<FilterState>(EMPTY_FILTERS);
  const [applied, setApplied] = useState<FilterState>(EMPTY_FILTERS);
  const [entries, setEntries] = useState<Entry[]>([]);
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
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

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
        if (ConnectError.from(error).code === Code.Unauthenticated) {
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

  const categories = useMemo(() => [...new Set(entries.map((entry) => entry.category).filter((category) => category !== ""))].sort(), [entries]);
  const visible = useMemo(
    () => entries.filter((entry) => (view === "trash" ? entry.deletedAt !== undefined : entry.deletedAt === undefined)),
    [entries, view],
  );
  const months = useMemo(() => groupEntries(visible), [visible]);
  const today = localToday();

  const openComposer = () => {
    setComposerSeed(emptyDraft());
    setSaveError(null);
    setComposerOpen(true);
  };

  const submitDraft = async (draft: DraftEntry) => {
    const checked = validateDraft(draft);
    if (checked.ok === false) {
      setSaveError("请检查标红的字段。");
      return;
    }
    setSaving(true);
    setSaveError(null);
    try {
      await write(() =>
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
            currency: draft.amount.trim() === "" ? "" : draft.currency,
            note: draft.note.trim(),
          },
        }),
      );
      setComposerOpen(false);
      await load(view === "trash");
    } catch (error) {
      setSaveError(describeError(error, "保存失败"));
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
          id: entry.id,
          entryRef: entry.entryRef,
          expectedRevision: entry.revision,
          mutationId: mutationID(),
          permanent: false,
        }),
      () => load(view === "trash"),
    );

  const restoreEntry = (entry: Entry) =>
    mutate(
      () => timeline.restoreEntry({ id: entry.id, entryRef: entry.entryRef, expectedRevision: entry.revision, mutationId: mutationID() }),
      () => load(true),
    );

  const purgeEntry = (entry: Entry) =>
    mutate(
      () => timeline.purgeEntry({ id: entry.id, entryRef: entry.entryRef, expectedRevision: entry.revision, mutationId: mutationID() }),
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
          <div className="mx-auto w-full max-w-[860px]">
            <div className="flex items-start justify-between gap-6">
              <div>
                <p className="text-[11px] font-semibold tracking-[0.2em] text-muted-foreground uppercase">Timeline</p>
                <h1 className="mt-1.5 text-[32px] leading-tight font-semibold tracking-tight text-on-surface">
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

            {actionError !== null && (
              <p className="mt-6 flex items-start gap-1.5 rounded-md bg-error-container px-3 py-2 text-xs text-on-error-container">
                <WarningCircle size={14} className="mt-px shrink-0" />
                {actionError}
              </p>
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

            {!signedOut && !loading && view === "timeline" && (
              <Timeline
                months={months}
                empty={visible.length === 0}
                loadingMore={loadingMore}
                canLoadMore={nextToken !== ""}
                onLoadMore={() => void load(false, nextToken)}
                onDelete={(entry) => void removeEntry(entry)}
              />
            )}

            {!signedOut && !loading && view === "trash" && (
              <Trash entries={visible} onRestore={(entry) => void restoreEntry(entry)} onPurge={(entry) => void purgeEntry(entry)} />
            )}
          </div>
        </main>
      </div>

      {composerOpen && (
        <Composer
          open={composerOpen}
          initial={composerSeed}
          categories={categories}
          saving={saving}
          error={saveError}
          onClose={() => setComposerOpen(false)}
          onSubmit={(draft) => void submitDraft(draft)}
        />
      )}
    </div>
  );
}
