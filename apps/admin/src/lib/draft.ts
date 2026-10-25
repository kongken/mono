// What is kept across the trip to OzID for a password confirmation, so that a long edit is not lost.
//
// It is one slot in sessionStorage (this tab only; gone when the tab closes), holding what the person had
// typed and nothing the server produced: no session, no CSRF token, no token secret. It belongs to one
// account and one place, expires after a short time, is removed when read or when another account
// reads it, and is never submitted by anything but the person pressing the button again.

export const DRAFT_KEY = "admin.draft.v1";
export const DRAFT_TTL_MS = 30 * 60_000;

type Slot = { v: 1; userId: string; scope: string; savedAt: number; data: unknown };

type Writer = Pick<Storage, "setItem">;
type Reader = Pick<Storage, "getItem" | "removeItem">;

/** Keeps `data` for `userId` at `scope`, replacing any earlier slot. False when the browser refuses to store it. */
export function saveDraft(store: Writer, d: { userId: string; scope: string; data: unknown }, now = Date.now()): boolean {
  try {
    const slot: Slot = { v: 1, userId: d.userId, scope: d.scope, savedAt: now, data: d.data };
    store.setItem(DRAFT_KEY, JSON.stringify(slot));
    return true;
  } catch {
    return false;
  }
}

/**
 * Takes the draft out of storage if it is this account's, still fresh, and its scope starts with `scopePrefix`.
 * Expired or foreign slots are removed whatever the prefix; a fresh slot of this account for another place stays.
 */
export function takeDraft(store: Reader, userId: string, scopePrefix: string, now = Date.now()): { scope: string; data: unknown } | undefined {
  let raw: string | null;
  try {
    raw = store.getItem(DRAFT_KEY);
  } catch {
    return undefined;
  }
  if (raw === null) return undefined;
  let slot: Slot | undefined;
  try {
    const parsed = JSON.parse(raw) as Partial<Slot>;
    if (parsed.v === 1 && typeof parsed.userId === "string" && typeof parsed.scope === "string" && typeof parsed.savedAt === "number") slot = parsed as Slot;
  } catch {
    slot = undefined;
  }
  const fresh = slot !== undefined && now - slot.savedAt >= 0 && now - slot.savedAt <= DRAFT_TTL_MS;
  if (!slot || !fresh || slot.userId !== userId) {
    forget(store);
    return undefined;
  }
  if (!slot.scope.startsWith(scopePrefix)) return undefined;
  forget(store);
  return { scope: slot.scope, data: slot.data };
}

/** Drops whatever is kept (sign-out, another account). */
export function clearDraft(store: Pick<Storage, "removeItem">) {
  forget(store);
}

function forget(store: Pick<Storage, "removeItem">) {
  try {
    store.removeItem(DRAFT_KEY);
  } catch {
    // Nothing to do: the slot expires with the tab.
  }
}
