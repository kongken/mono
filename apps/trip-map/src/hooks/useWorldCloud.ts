import { useCallback, useEffect, useRef, useState } from "react";
import { Code } from "@connectrpc/connect";
import { getWorld, replaceWorld } from "../api/world";
import {
  emptyWorld,
  sameWorld,
  validateWorld,
  type WorldCodes,
  type WorldDocument,
} from "../lib/world";
import { exportWorldBackup, previewWorldImport, type WorldImportPreview } from "../lib/world-persistence";
import { toggleCountry } from "../lib/world";

export const WORLD_CLOUD_PREFIX = "shanhai-world-cloud-v1:";
export const worldCloudKey = (userId: string) => WORLD_CLOUD_PREFIX + userId;

export type WorldCloudPending = { request_id: string; base_revision: number; document: WorldDocument };
export type WorldCloudCache = {
  schema: 1;
  saved: WorldDocument;
  confirmed: { revision: number; document: WorldDocument };
  pending?: WorldCloudPending;
};

type CloudPhase = "ok" | "cloud-loading" | "cloud-error" | "cloud-save-failed" | "cloud-conflict" | "cloud-unreadable";
type CloudTheirs = { text: string | null; document: WorldDocument | null; revision: number; source: "storage" | "server" };

const browserStorage = (): Storage | null => {
  try { return window.localStorage; } catch { return null; }
};
const randomId = () => crypto.randomUUID().replace(/[^A-Za-z0-9_-]/g, "").slice(0, 64) || `world-${Date.now()}`;
const parseCache = (raw: string, codes: WorldCodes): WorldCloudCache => {
  const data = JSON.parse(raw) as Partial<WorldCloudCache>;
  if (data.schema !== 1 || !data.saved || !data.confirmed || !Number.isSafeInteger(data.confirmed.revision)) throw new Error("账号世界缓存无法识别");
  const saved = validateWorld(data.saved, codes);
  const confirmed = { revision: data.confirmed.revision, document: validateWorld(data.confirmed.document, codes) };
  let pending: WorldCloudPending | undefined;
  if (data.pending !== undefined) {
    if (!data.pending || typeof data.pending.request_id !== "string" || !/^[A-Za-z0-9_-]{1,64}$/.test(data.pending.request_id) || !Number.isSafeInteger(data.pending.base_revision)) throw new Error("账号世界缓存中的待同步请求无法识别");
    pending = { request_id: data.pending.request_id, base_revision: data.pending.base_revision, document: validateWorld(data.pending.document, codes) };
  }
  return pending ? { schema: 1, saved, confirmed, pending } : { schema: 1, saved, confirmed };
};

export type WorldCloudResult = { ok: true } | { ok: false; error: string };

export function useWorldCloud(codes: WorldCodes, userId: string | null) {
  const enabled = !!userId;
  const [world, setWorld] = useState(emptyWorld);
  const worldRef = useRef(world);
  const [phase, setPhase] = useState<CloudPhase>(enabled ? "cloud-loading" : "ok");
  const [announcement, setAnnouncement] = useState("");
  const [cacheFailed, setCacheFailed] = useState(false);
  const [raw, setRaw] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [theirs, setTheirs] = useState<CloudTheirs | null>(null);
  const cacheRef = useRef<WorldCloudCache | null>(null);
  const rawRef = useRef<string | null>(null);
  const running = useRef(false);
  const userRef = useRef(userId);
  const stopped = !enabled;

  const show = useCallback((next: WorldDocument) => { worldRef.current = next; setWorld(next); }, []);
  const preserve = (storage: Storage, text: string | null) => {
    if (text === null) return true;
    try { storage.setItem(`${WORLD_CLOUD_PREFIX}conflict:${userRef.current}-${Date.now()}`, text); return true; } catch { return false; }
  };
  const write = useCallback((cache: WorldCloudCache): "ok" | "conflict" | "error" => {
    const storage = browserStorage();
    if (!storage || !userRef.current) return "error";
    let current: string | null;
    try { current = storage.getItem(worldCloudKey(userRef.current)); } catch { return "error"; }
    if (current !== rawRef.current) {
      preserve(storage, current);
      setTheirs({ text: current, document: current ? (() => { try { return parseCache(current, codes).saved; } catch { return null; } })() : emptyWorld(), revision: cacheRef.current?.confirmed.revision ?? 0, source: "storage" });
      setPhase("cloud-conflict");
      return "conflict";
    }
    try {
      const text = JSON.stringify(cache);
      storage.setItem(worldCloudKey(userRef.current), text);
      rawRef.current = text;
      setRaw(text);
      cacheRef.current = cache;
      setCacheFailed(false);
      return "ok";
    } catch { setCacheFailed(true); setPhase("cloud-save-failed"); return "error"; }
  }, [codes]);

  const load = useCallback(async () => {
    const id = userRef.current;
    if (!id) return;
    const storage = browserStorage();
    let cache: WorldCloudCache | null = null;
    let storedRaw: string | null = null;
    try {
      storedRaw = storage?.getItem(worldCloudKey(id)) ?? null;
      if (storedRaw) cache = parseCache(storedRaw, codes);
    } catch (error) {
      setRaw(storedRaw);
      rawRef.current = storedRaw;
      setReason(error instanceof Error ? error.message : "账号世界缓存无法读取");
      setPhase("cloud-unreadable");
      return;
    }
    if (cache) {
      cacheRef.current = cache; rawRef.current = storedRaw; setRaw(storedRaw);
      show(cache.saved); setPhase("ok");
    } else {
      setPhase("cloud-loading");
    }
    try {
      const remote = await getWorld(id);
      if (userRef.current !== id) return;
      if (!cache) {
        const next: WorldCloudCache = { schema: 1, saved: remote.document, confirmed: remote };
        if (write(next) === "error") return;
        cacheRef.current = next; show(remote.document); setPhase("ok");
      } else if (!cache.pending && sameWorld(cache.saved, cache.confirmed.document)) {
        const next: WorldCloudCache = { schema: 1, saved: remote.document, confirmed: remote };
        if (write(next) === "ok") show(remote.document);
      }
      void sync();
    } catch (error) {
      if (!cache) { setReason(error instanceof Error ? error.message : "无法加载云端世界足迹"); setPhase("cloud-error"); }
      else { setAnnouncement("本机已保存，待同步"); setPhase("ok"); }
    }
  }, [codes, show, write]);

  const sync = useCallback(async () => {
    const id = userRef.current;
    if (!id || running.current || stopped || phase === "cloud-conflict" || phase === "cloud-unreadable") return;
    const cache = cacheRef.current;
    if (!cache) return;
    if (!cache.pending && sameWorld(cache.saved, cache.confirmed.document)) return;
    running.current = true;
    try {
      let current = cacheRef.current;
      if (!current) return;
      if (!current.pending) {
        current = { ...current, pending: { request_id: randomId(), base_revision: current.confirmed.revision, document: current.saved } };
        cacheRef.current = current;
        if (write(current) !== "ok") return;
      }
      for (;;) {
        if (userRef.current !== id) return;
        const pending = cacheRef.current?.pending;
        if (!pending) return;
        let response;
        try { response = await replaceWorld(id, pending.base_revision, pending.request_id, pending.document); }
        catch (error) {
          if ((error as { code?: unknown }).code === Code.AlreadyExists) {
            const retry = { ...pending, request_id: randomId() };
            const next = { ...cacheRef.current!, pending: retry };
            cacheRef.current = next; if (write(next) !== "ok") return;
            continue;
          }
          setAnnouncement("本机已保存，待同步"); setPhase("ok"); return;
        }
        const remote = { revision: response.cloudRevision || response.revision, document: response.document };
        const currentCache = cacheRef.current!;
        if (!response.applied) {
          if (sameWorld(remote.document, pending.document) || sameWorld(remote.document, currentCache.saved)) {
            const next = { schema: 1 as const, saved: currentCache.saved, confirmed: remote };
            cacheRef.current = next; if (write(next) !== "ok") return;
            show(next.saved);
            if (sameWorld(next.saved, next.confirmed.document)) return;
            continue;
          }
          setTheirs({ text: JSON.stringify(remote.document), document: remote.document, revision: remote.revision, source: "server" });
          setPhase("cloud-conflict");
          return;
        }
        const next: WorldCloudCache = { schema: 1, saved: currentCache.saved, confirmed: { revision: response.revision, document: response.document } };
        cacheRef.current = next;
        if (write(next) !== "ok") return;
        if (response.cloudRevision > response.revision && !sameWorld(next.saved, next.confirmed.document)) {
          const fresh = await getWorld(id);
          const withFresh = { schema: 1 as const, saved: next.saved, confirmed: fresh };
          cacheRef.current = withFresh; if (write(withFresh) !== "ok") return;
          continue;
        }
        if (sameWorld(next.saved, next.confirmed.document)) { setAnnouncement("已同步到云端"); setPhase("ok"); return; }
      }
    } finally { running.current = false; }
  }, [phase, show, stopped, write]);

  useEffect(() => { userRef.current = userId; cacheRef.current = null; rawRef.current = null; setTheirs(null); setCacheFailed(false); setReason(""); if (userId) void load(); else { setWorld(emptyWorld()); setPhase("ok"); } }, [userId]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!enabled) return;
    const retry = () => void load().then(() => sync());
    const visible = () => { if (document.visibilityState === "visible") retry(); };
    const storage = (event: StorageEvent) => {
      if (event.key !== worldCloudKey(userRef.current!)) return;
      const current = event.newValue;
      if (current === rawRef.current) return;
      const s = browserStorage(); if (s) preserve(s, current);
      setTheirs({ text: current, document: current ? (() => { try { return parseCache(current, codes).saved; } catch { return null; } })() : emptyWorld(), revision: cacheRef.current?.confirmed.revision ?? 0, source: "storage" });
      setPhase("cloud-conflict");
    };
    window.addEventListener("online", retry); document.addEventListener("visibilitychange", visible); window.addEventListener("storage", storage);
    return () => { window.removeEventListener("online", retry); document.removeEventListener("visibilitychange", visible); window.removeEventListener("storage", storage); };
  }, [codes, enabled, load, sync]);

  const edit = useCallback((change: (world: WorldDocument) => WorldDocument, message: string): WorldCloudResult => {
    if (!enabled || phase !== "ok" || !cacheRef.current) return { ok: false, error: phase === "cloud-error" ? "云端数据尚未加载，请先重试" : "世界足迹暂时不能编辑，请先处理同步提示" };
    let next: WorldDocument;
    try { next = change(worldRef.current); } catch (error) { return { ok: false, error: error instanceof Error ? error.message : "无法完成" }; }
    const updated = { ...cacheRef.current, saved: next };
    cacheRef.current = updated;
    if (write(updated) !== "ok") { cacheRef.current = { ...updated, saved: worldRef.current }; return { ok: false, error: "账号缓存保存失败，请先导出备份" }; }
    show(next); setAnnouncement(message); void sync(); return { ok: true };
  }, [enabled, phase, show, sync, write]);
  const toggle = useCallback((code: string, name: string) => { const marked = worldRef.current.countries.some((m) => m.code === code); const result = edit((w) => toggleCountry(w, code, codes), `${marked ? "已取消标记" : "已标记"}${name}`); if (!result.ok) setAnnouncement(result.error); }, [codes, edit]);
  const resolve = useCallback((_choice: "recover" | "empty", _exported: boolean) => "copy-failed" as const, []);
  const settleConflict = useCallback((choice: "mine" | "theirs", exported: boolean): "ok" | "copy-failed" | "unusable" => {
    if (!theirs || !cacheRef.current) return "ok";
    if (theirs.source === "storage") {
      rawRef.current = theirs.text;
      setRaw(theirs.text);
    }
    if (choice === "theirs") { if (!theirs.document) return "unusable"; const next = { schema: 1 as const, saved: theirs.document, confirmed: { revision: theirs.revision, document: theirs.document } }; cacheRef.current = next; if (write(next) !== "ok") return "copy-failed"; show(next.saved); setTheirs(null); setPhase("ok"); return "ok"; }
    if (!exported && !preserve(browserStorage()!, theirs.text)) return "copy-failed";
    const next = { schema: 1 as const, saved: worldRef.current, confirmed: { revision: theirs.revision, document: theirs.document ?? emptyWorld() }, pending: { request_id: randomId(), base_revision: theirs.revision, document: worldRef.current } };
    cacheRef.current = next; if (write(next) !== "ok") return "copy-failed"; setTheirs(null); setPhase("ok"); void sync(); return "ok";
  }, [show, sync, theirs, write]);
  return {
    world, phase, paused: phase !== "ok", announcement, announce: setAnnouncement, edit, toggle,
    previewImport: useCallback((text: string): WorldImportPreview => previewWorldImport(text, worldRef.current, codes), [codes]),
    confirmImport: useCallback((preview: WorldImportPreview) => edit(() => preview.document, "已导入世界足迹备份"), [edit]),
    resolve, settleConflict, raw, reason, recovery: null, theirs,
    backup: useCallback((date: string) => exportWorldBackup(worldRef.current, date), []),
    retry: () => void load().then(() => sync()),
    syncPending: !!cacheRef.current?.pending || (!!cacheRef.current && !sameWorld(cacheRef.current.saved, cacheRef.current.confirmed.document)),
    cacheFailed,
  };
}
