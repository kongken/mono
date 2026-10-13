import test from "node:test";
import assert from "node:assert/strict";
import { applyDelta, createIncrementalReader } from "../src/lib/delta.ts";
const empty = { saved: { version: 1, visits: [], provinces: {} }, revision: 0 };
const visit = {
  code: "330100",
  name: "杭州市",
  province: "330000",
  color: "#4b937d",
  date: "",
  note: "",
};
test("incremental pages apply upserts and explicit deletion tombstones in order", () => {
  const added = applyDelta(empty, {
    changes: [{ revision: 1, op: { type: "upsertVisit", visit } }],
    nextRevision: 1,
    currentRevision: 2,
    hasMore: true,
    resyncRequired: false,
  });
  const removed = applyDelta(added, {
    changes: [{ revision: 2, op: { type: "deleteVisit", code: visit.code } }],
    nextRevision: 2,
    currentRevision: 2,
    hasMore: false,
    resyncRequired: false,
  });
  assert.equal(removed.saved.visits.length, 0);
  assert.equal(removed.revision, 2);
  assert.equal(empty.saved.visits.length, 0);
  assert.throws(() =>
    applyDelta(empty, {
      changes: [{ revision: 2, op: { type: "upsertVisit", visit } }],
      nextRevision: 2,
      currentRevision: 2,
      hasMore: false,
      resyncRequired: false,
    }),
  );
});
test("failed pagination keeps the prior cursor; unavailable history fetches a full snapshot", async () => {
  let calls = [],
    fail = true;
  const reader = createIncrementalReader(
    {
      full: async () => ({ ...empty, revision: 10 }),
      delta: async (rev) => {
        calls.push(rev);
        if (rev === 1 && fail) throw new Error("network");
        return rev === 0
          ? {
              changes: [{ revision: 1, op: { type: "upsertVisit", visit } }],
              nextRevision: 1,
              currentRevision: 2,
              hasMore: true,
              resyncRequired: false,
            }
          : {
              changes: [],
              nextRevision: rev,
              currentRevision: 10,
              hasMore: false,
              resyncRequired: true,
            };
      },
    },
    empty,
  );
  await assert.rejects(reader.get());
  fail = false;
  assert.equal((await reader.get()).revision, 10);
  assert.deepEqual(calls, [0, 1, 0, 1]);
});
