import assert from "node:assert/strict";
import { test } from "node:test";
import { clearRegistrationMutation, readRegistrationMutation } from "./registrationPending.ts";

function storage(initial: string | null = null) {
  let value = initial;
  return {
    getItem: () => value,
    setItem: (_key: string, next: string) => { value = next; },
    removeItem: () => { value = null; },
    current: () => value,
  };
}

test("registration mutation survives refresh and response-lost retry reuses the receipt", async () => {
  const firstStorage = storage();
  const first = readRegistrationMutation("flow-1", firstStorage);
  assert.ok(first);
  const refreshedStorage = storage(firstStorage.current());
  const retry = readRegistrationMutation("flow-1", refreshedStorage);
  assert.equal(retry, first);

  const calls: string[] = [];
  async function complete(mutationId: string) {
    calls.push(mutationId);
    if (calls.length === 1) throw new Error("response lost after commit");
    return { replayed: true };
  }
  await assert.rejects(() => complete(first!));
  const result = await complete(readRegistrationMutation("flow-1", refreshedStorage)!);
  assert.deepEqual(result, { replayed: true });
  assert.deepEqual(calls, [first, first]);
});

test("a new flow gets a separate receipt and completed flow can clear it", () => {
  const s = storage();
  const first = readRegistrationMutation("flow-1", s);
  const second = readRegistrationMutation("flow-2", s);
  assert.notEqual(second, first);
  clearRegistrationMutation("flow-2", s);
  assert.equal(s.current(), null);
});
