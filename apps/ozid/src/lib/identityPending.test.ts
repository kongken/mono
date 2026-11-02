import assert from "node:assert/strict";
import { test } from "node:test";
import { readPending } from "./identityPending.ts";

function storage(initial: string) {
  let value: string | null = initial;
  return {
    getItem: () => value,
    setItem: (_key: string, next: string) => { value = next; },
    current: () => value,
  };
}

test("legacy pending state is upgraded once and survives page refresh", () => {
  const legacy = JSON.stringify({ kind: "identity_unlink", identityId: "identity-1", challengeId: "challenge-1" });
  const firstStorage = storage(legacy);
  const first = readPending(firstStorage);
  assert.ok(first?.mutationId);
  assert.equal(JSON.parse(firstStorage.current()!).mutationId, first.mutationId);

  const secondStorage = storage(firstStorage.current()!);
  const second = readPending(secondStorage);
  assert.equal(second?.mutationId, first.mutationId);
  assert.equal(secondStorage.current(), firstStorage.current());
});
