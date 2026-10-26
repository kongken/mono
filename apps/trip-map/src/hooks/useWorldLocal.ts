import { useCallback, useRef, useState } from "react";
import {
  createWorldWriter,
  exportWorldBackup,
  loadWorldLocal,
  type WorldLoad,
} from "../lib/world-persistence.ts";
import { emptyWorld, toggleCountry, type WorldCodes, type WorldDocument } from "../lib/world.ts";

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
  | "conflict";

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

export function useWorldLocal(codes: WorldCodes) {
  const [start] = useState(() => {
    const storage = browserStorage();
    const load: WorldLoad = storage ? loadWorldLocal(storage, codes) : { status: "unavailable", document: emptyWorld() };
    return { load, writer: createWorldWriter(storage ?? refusing, load.status === "unavailable" ? null : load.raw) };
  });
  const { load, writer } = start;
  const [world, setWorld] = useState<WorldDocument>(load.document);
  const worldRef = useRef(world);
  const [phase, setPhase] = useState<WorldPhase>(
    load.status === "unreadable" ? "unreadable" : load.status === "unavailable" ? "unavailable" : "ok",
  );
  const [announcement, setAnnouncement] = useState("");
  const paused = phase === "unreadable" || phase === "conflict";

  const toggle = useCallback(
    (code: string, name: string) => {
      if (paused) return;
      let next: WorldDocument;
      try {
        next = toggleCountry(worldRef.current, code, codes);
      } catch (error) {
        setAnnouncement(error instanceof Error ? error.message : "无法修改");
        return;
      }
      const marking = next.countries.some((mark) => mark.code === code);
      const result = writer.write(next);
      if (result.status === "conflict") {
        setPhase("conflict");
        return;
      }
      worldRef.current = next;
      setWorld(next);
      setPhase(result.status === "ok" ? "ok" : "save-failed");
      setAnnouncement(`${marking ? "已标记" : "已取消标记"}${name}`);
    },
    [codes, paused, writer],
  );

  return {
    world,
    phase,
    paused,
    announcement,
    toggle,
    /** The stored text of an unreadable read, exactly as it was. */
    raw: load.status === "unreadable" ? load.raw : null,
    reason: load.status === "unreadable" ? load.reason : "",
    /** The world backup of what this page currently shows. */
    backup: useCallback((date: string) => exportWorldBackup(worldRef.current, date), []),
  };
}
