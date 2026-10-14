import { useEffect, useMemo, useRef, useState } from "react";
import { MagnifyingGlass, X } from "@phosphor-icons/react";
import { Input } from "./ui/input";
import { Button } from "./ui/button";
import { searchCloudNotes, type NoteSearchPage } from "../api/search";
import {
  noteExcerpt,
  noteTerms,
  searchNotes,
  type NoteMatch,
} from "../lib/search";
import type { Saved } from "../lib/travel";
import "./search.css";

type Props = {
  saved: Saved;
  provinceCode: string;
  cloudReady: boolean;
  revision: number;
  onOpen: (match: NoteMatch) => void;
};

export default function NoteSearch({
  saved,
  provinceCode,
  cloudReady,
  revision,
  onOpen,
}: Props) {
  const [query, setQuery] = useState("");
  const [retry, setRetry] = useState(0);
  const [localLimit, setLocalLimit] = useState(20);
  const [state, setState] = useState<{
    key: string;
    page?: NoteSearchPage;
    failed?: boolean;
    loading?: boolean;
  }>({ key: "" });
  const controller = useRef<AbortController | null>(null);
  const terms = noteTerms(query);
  const valid = query.length <= 100 && terms.length > 0 && terms.length <= 8;
  const key = JSON.stringify([
    query,
    provinceCode,
    cloudReady,
    revision,
    retry,
  ]);
  const local = useMemo(
    () => searchNotes(saved, query, provinceCode),
    [saved, query, provinceCode],
  );

  useEffect(() => {
    const abort = new AbortController();
    controller.current = abort;
    if (!valid || !cloudReady) return () => abort.abort();
    const timer = setTimeout(() => {
      setState({ key, loading: true });
      searchCloudNotes(query, provinceCode, abort.signal, 0, BigInt(revision))
        .then((page) => {
          if (!abort.signal.aborted) setState({ key, page });
        })
        .catch(() => {
          if (!abort.signal.aborted) setState({ key, failed: true });
        });
    }, 300);
    return () => {
      clearTimeout(timer);
      abort.abort();
    };
  }, [key, valid, cloudReady, query, provinceCode, revision]);

  const current = state.key === key ? state : undefined;
  const page = current?.page;
  const matches = page ? page.matches : local.slice(0, localLimit);
  const more = page ? page.hasMore : local.length > localLimit;
  const loadMore = async () => {
    if (!page) {
      setLocalLimit((n) => n + 20);
      return;
    }
    const signal = controller.current?.signal;
    if (!signal || signal.aborted || current?.loading) return;
    setState({ key, page, loading: true });
    try {
      const next = await searchCloudNotes(
        query,
        provinceCode,
        signal,
        page.nextOffset,
        page.revision,
      );
      if (!signal.aborted)
        setState({
          key,
          page: { ...next, matches: [...page.matches, ...next.matches] },
        });
    } catch {
      // A revision change or network failure restarts from the current local
      // copy; never append a page from a different account or footprint version.
      if (!signal.aborted) setState({ key, failed: true });
    }
  };

  return (
    <section aria-label="旅行记忆搜索" className="note-search">
      <div className="search-wrap">
        <MagnifyingGlass size={17} />
        <Input
          aria-label="搜索旅行记忆"
          placeholder="搜索备注，如：西湖 徒步"
          maxLength={100}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setLocalLimit(20);
          }}
        />
        {query && (
          <button
            aria-label="清空记忆搜索"
            onClick={() => {
              setQuery("");
              setLocalLimit(20);
            }}
          >
            <X size={14} />
          </button>
        )}
      </div>
      <p className="search-help">
        仅搜索自己的完整备注，多个关键词需同时出现。
      </p>
      <div role="status" className="search-status">
        {!terms.length
          ? "输入关键词，找回旅行记忆。"
          : !valid
            ? "最多输入 8 个关键词。"
            : current?.failed
              ? "云端检索暂不可用，已检索本机缓存。"
              : page
                ? "云端检索结果"
                : cloudReady
                  ? "正在检索云端，先显示本机结果…"
                  : "本机检索结果（含未同步修改）"}
        {current?.failed && (
          <Button
            size="sm"
            variant="outline"
            onClick={() => setRetry((n) => n + 1)}
          >
            重试云端检索
          </Button>
        )}
      </div>
      {valid && (
        <>
          <div className="note-results">
            {matches.map((m) => (
              <button
                key={`${m.regionCode}:${m.tripId}`}
                className="note-result"
                onClick={() => onOpen(m)}
              >
                <strong>
                  {m.regionName}
                  <small>{m.date || "日期未记录"}</small>
                </strong>
                <span>{noteExcerpt(m.note, query)}</span>
                <small>查看旅行记录 · {m.tripId}</small>
              </button>
            ))}
            {!matches.length && (
              <p className="no-results">没有找到匹配的旅行记忆。</p>
            )}
          </div>
          {more && (
            <Button
              variant="outline"
              size="sm"
              disabled={!!current?.loading}
              onClick={() => void loadMore()}
            >
              {current?.loading ? "正在加载…" : "加载更多记忆"}
            </Button>
          )}
        </>
      )}
    </section>
  );
}
