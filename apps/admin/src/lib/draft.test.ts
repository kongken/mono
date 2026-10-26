import assert from "node:assert/strict";
import { test } from "node:test";
import { clearDraft, DRAFT_KEY, DRAFT_TTL_MS, saveDraft, takeDraft } from "./draft.ts";

const store = () => {
  const m = new Map<string, string>();
  return Object.assign(m, {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
  });
};

test("a draft comes back once, to the same account at the same place", () => {
  const s = store();
  assert.equal(saveDraft(s, { userId: "u1", scope: "metro:cities:create", data: { name: "x" } }, 1000), true);
  assert.deepEqual(takeDraft(s, "u1", "metro:cities:", 2000), { scope: "metro:cities:create", data: { name: "x" } });
  assert.equal(takeDraft(s, "u1", "metro:cities:", 2000), undefined);
});

test("another place leaves it where it is; another account removes it", () => {
  const s = store();
  saveDraft(s, { userId: "u1", scope: "metro:import", data: { text: "{}" } }, 1000);
  assert.equal(takeDraft(s, "u1", "metro:cities:", 1500), undefined);
  assert.ok(s.has(DRAFT_KEY));
  assert.equal(takeDraft(s, "u2", "metro:import", 1500), undefined);
  assert.ok(!s.has(DRAFT_KEY));
});

test("an old draft is not restored and is removed", () => {
  const s = store();
  saveDraft(s, { userId: "u1", scope: "metro:import", data: {} }, 1000);
  assert.equal(takeDraft(s, "u1", "metro:import", 1000 + DRAFT_TTL_MS + 1), undefined);
  assert.ok(!s.has(DRAFT_KEY));
});

test("a damaged or foreign value is dropped, a refusing browser is survived", () => {
  const s = store();
  s.set(DRAFT_KEY, "not json");
  assert.equal(takeDraft(s, "u1", "", 1), undefined);
  assert.ok(!s.has(DRAFT_KEY));
  s.set(DRAFT_KEY, JSON.stringify({ v: 2, userId: "u1", scope: "x", savedAt: 1, data: {} }));
  assert.equal(takeDraft(s, "u1", "", 2), undefined);
  assert.equal(saveDraft({ setItem: () => { throw new Error("quota"); } }, { userId: "u1", scope: "x", data: {} }), false);
  assert.equal(takeDraft({ getItem: () => { throw new Error("blocked"); }, removeItem: () => {} }, "u1", ""), undefined);
});

test("clearing removes the slot", () => {
  const s = store();
  saveDraft(s, { userId: "u1", scope: "x", data: {} });
  clearDraft(s);
  assert.ok(!s.has(DRAFT_KEY));
});
