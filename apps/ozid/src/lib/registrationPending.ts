export type RegistrationPending = { flowId: string; mutationId: string };

const KEY = "ozid:external-registration";
type StorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem">;

function valid(value: unknown, flowId: string): value is RegistrationPending {
  return !!value && typeof value === "object" && (value as RegistrationPending).flowId === flowId && typeof (value as RegistrationPending).mutationId === "string" && (value as RegistrationPending).mutationId.length > 0;
}

/** Reads the registration receipt for this browser flow, creating it once if needed. */
export function readRegistrationMutation(flowId: string, storage: StorageLike = sessionStorage): string | null {
  if (!flowId) return null;
  try {
    const parsed = JSON.parse(storage.getItem(KEY) ?? "null") as unknown;
    if (valid(parsed, flowId)) return parsed.mutationId;
    const pending = { flowId, mutationId: crypto.randomUUID() } satisfies RegistrationPending;
    storage.setItem(KEY, JSON.stringify(pending));
    return pending.mutationId;
  } catch {
    return null;
  }
}

export function clearRegistrationMutation(flowId: string, storage: StorageLike = sessionStorage): void {
  try {
    const parsed = JSON.parse(storage.getItem(KEY) ?? "null") as unknown;
    if (valid(parsed, flowId)) storage.removeItem(KEY);
  } catch {
    // Storage can be unavailable; the server remains authoritative.
  }
}

export const registrationMutationKey = KEY;
