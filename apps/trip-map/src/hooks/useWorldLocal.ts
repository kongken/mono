import { useCallback, useRef, useState } from "react";
import {
  createWorldWriter,
  exportWorldBackup,
  loadWorldLocal,
  overwriteAfterConflict,
  previewWorldImport,
  resolveUnreadable,
  type WorldImportPreview,
  type WorldLoad,
  type WorldWrite,
} from "../lib/world-persistence.ts";
import {
  emptyWorld,
  toggleCountry,
  validateWorld,
  WorldValidationError,
  type WorldCodes,
  type WorldDocument,
} from "../lib/world.ts";

// The world footprints of this device, for the world view of the local (signed-out) mode:
// read once when the view opens (after the catalog is in, because validating the stored text
// needs its country codes), written only after an edit, never over text this page has not seen.
// While the stored text is unreadable, or another tab changed it, nothing is written at all
// (docs/trip-map/world-spec.md section 7).

export type WorldPhase =
  | "ok"
  /** The browser would not give access to its storage: edits stay in memory and cannot be saved. */
  | "unavailable"
  /** Writing failed (storage full or refused): the edit is kept in memory and the user is told. */
  | "save-failed"
  /** The stored text cannot be read: it is kept as it is and saving is paused. */
  | "unreadable"
  /** Another tab changed the stored text: saving is paused so that it is not overwritten. */
  | "conflict"
  /** The account world document is being loaded for the first time. */
  | "cloud-loading"
  /** The account world document could not be loaded and has no local cache. */
  | "cloud-error"
  /** The account cache could not be parsed or validated. */
  | "cloud-unreadable"
  /** The account cache changed while this page was editing. */
  | "cloud-conflict"
  /** The browser refused an account-cache write. */
  | "cloud-save-failed";

function browserStorage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}
const refusing: Pick<Storage, "getItem" | "setItem"> = {
  getItem() {
    throw new Error("storage unavailable");
  },
  setItem() {
    throw new Error("storage unavailable");
  },
};

/** What a failed edit or import tells the user: the reason alone, never an internal path. */
export const describeWorldError = (error: unknown): string =>
  error instanceof WorldValidationError ? error.detail : error instanceof Error ? error.message : "无法完成";

export type EditResult = { ok: true } | { ok: false; error: string };
type Unreadable = Extract<WorldLoad, { status: "unreadable" }>;
/** Another tab's stored text, as far as it can be used: a valid document, nothing at all, or unreadable. */
export type Theirs = { text: string | null; document: WorldDocument | null };

export function useWorldLocal(codes: WorldCodes, enabled = true) {
  const [start] = useState(() => {
    const storage = browserStorage();
    const load: WorldLoad = !enabled
      ? { status: "empty", document: emptyWorld(), raw: null }
      : storage
        ? loadWorldLocal(storage, codes)
        : { status: "unavailable", document: emptyWorld() };
    return { storage, load, writer: createWorldWriter(storage ?? refusing, load.status === "unavailable" ? null : load.raw) };
  });
  const { load, storage } = start;
  const writer = useRef(start.writer);
  const [world, setWorld] = useState<WorldDocument>(load.document);
  const worldRef = useRef(world);
  const [unreadable, setUnreadable] = useState<Unreadable | null>(load.status === "unreadable" ? load : null);
  const [phase, setPhase] = useState<WorldPhase>(
    load.status === "unreadable" ? "unreadable" : load.status === "unavailable" ? "unavailable" : "ok",
  );
  const [theirs, setTheirs] = useState<Theirs | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const paused = phase === "unreadable" || phase === "conflict";

  const show = useCallback((next: WorldDocument) => {
    worldRef.current = next;
    setWorld(next);
  }, []);
  /** Applies the outcome of a write: the edit stays on the page whatever happened. */
  const settle = useCallback((result: WorldWrite) => {
    if (result.status === "conflict") {
      let document: WorldDocument | null = null;
      if (result.other !== null) {
        try {
          document = validateWorld(JSON.parse(result.other), codes);
        } catch {
          document = null;
        }
      } else document = emptyWorld();
      setTheirs({ text: result.other, document });
      setPhase("conflict");
    } else setPhase(result.status === "ok" ? "ok" : "save-failed");
  }, [codes]);

  /** One edit: validated, kept on the page, and written unless saving is paused. */
  const edit = useCallback(
    (change: (world: WorldDocument) => WorldDocument, message: string): EditResult => {
      if (paused) return { ok: false, error: "保存已暂停，请先处理页面上方的提示" };
      let next: WorldDocument;
      try {
        next = change(worldRef.current);
      } catch (error) {
        return { ok: false, error: describeWorldError(error) };
      }
      const result = writer.current.write(next);
      show(next);
      settle(result);
      setAnnouncement(result.status === "conflict" ? "其他标签页修改了世界足迹，保存已暂停，请选择如何处理" : message);
      return { ok: true };
    },
    [paused, settle, show],
  );

  const toggle = useCallback(
    (code: string, name: string) => {
      if (paused) return;
      const marking = !worldRef.current.countries.some((mark) => mark.code === code);
      const result = edit((w) => toggleCountry(w, code, codes), `${marking ? "已标记" : "已取消标记"}${name}`);
      if (!result.ok) setAnnouncement(result.error);
    },
    [codes, edit, paused],
  );

  /**
   * Recover the recognisable records or start empty after an unreadable read. The original
   * is copied inside the browser first; if that is refused, nothing changes unless the user
   * has already exported it (`exported`).
   */
  const resolve = useCallback(
    (choice: "recover" | "empty", exported: boolean): "ok" | "copy-failed" => {
      if (!unreadable || !storage) return "ok";
      let resolution = resolveUnreadable(storage, unreadable, choice);
      if (resolution.status === "copy-failed") {
        if (!exported) return "copy-failed";
        resolution = {
          status: "ok",
          document: choice === "recover" && unreadable.recovery ? unreadable.recovery.document : emptyWorld(),
          writer: createWorldWriter(storage, unreadable.raw),
        };
      }
      writer.current = resolution.writer;
      const result = resolution.writer.write(resolution.document);
      show(resolution.document);
      setUnreadable(null);
      settle(result);
      setAnnouncement(choice === "recover" ? "已恢复可识别的记录" : "已用空数据开始，原文已保留");
      return "ok";
    },
    [settle, show, storage, unreadable],
  );

  /** After a conflict: keep this page's data (the other tab's text is copied first) or take theirs. */
  const settleConflict = useCallback(
    (choice: "mine" | "theirs", exported: boolean): "ok" | "copy-failed" | "unusable" => {
      if (!theirs || !storage) return "ok";
      if (choice === "theirs") {
        if (!theirs.document) return "unusable";
        writer.current.adopt(theirs.text);
        show(theirs.document);
        setTheirs(null);
        setPhase("ok");
        setAnnouncement("已载入另一个页面的数据，本页未保存的修改已放弃");
        return "ok";
      }
      let result = overwriteAfterConflict(storage, writer.current, worldRef.current, theirs.text);
      if (result.status === "copy-failed") {
        if (!exported) return "copy-failed";
        writer.current.adopt(theirs.text);
        result = writer.current.write(worldRef.current);
      }
      setTheirs(null);
      settle(result);
      setAnnouncement("已用本页数据覆盖，另一个页面的数据保留为浏览器内的副本或已导出");
      return "ok";
    },
    [settle, show, storage, theirs],
  );

  /** Validates an import file against what the page shows; throws a readable reason. Writes nothing. */
  const previewImport = useCallback(
    (text: string): WorldImportPreview => {
      try {
        return previewWorldImport(text, worldRef.current, codes);
      } catch (error) {
        throw new Error(error instanceof WorldValidationError ? error.message : describeWorldError(error));
      }
    },
    [codes],
  );
  const confirmImport = useCallback(
    (preview: WorldImportPreview): EditResult => edit(() => preview.document, "已导入世界足迹备份"),
    [edit],
  );

  return {
    world,
    phase,
    paused,
    announcement,
    announce: setAnnouncement,
    edit,
    toggle,
    previewImport,
    confirmImport,
    resolve,
    settleConflict,
    /** The stored text of an unreadable read, exactly as it was. */
    raw: unreadable?.raw ?? null,
    reason: unreadable?.reason ?? "",
    recovery: unreadable?.recovery ?? null,
    theirs,
    /** The world backup of what this page currently shows. */
    backup: useCallback((date: string) => exportWorldBackup(worldRef.current, date), []),
  };
}
