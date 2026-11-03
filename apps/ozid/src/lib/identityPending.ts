export type Pending = {
  kind: "identity_link" | "identity_unlink";
  provider?: string;
  identityId?: string;
  challengeId: string;
  flowId?: string;
  mutationId: string;
};

export function readPending(storage: Pick<Storage, "getItem" | "setItem">): Pending | null {
  try {
    const value = JSON.parse(storage.getItem("ozid:identity-operation") ?? "null") as Partial<Pending> | null;
    if (!value) return null;
    if (value.mutationId) return value as Pending;
    const next = { ...value, mutationId: crypto.randomUUID() } as Pending;
    storage.setItem("ozid:identity-operation", JSON.stringify(next));
    return next;
  } catch {
    return null;
  }
}
