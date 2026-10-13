import test from "node:test";
import assert from "node:assert/strict";
import { SyncEngine, applyOp, RevisionChanged } from "../src/lib/sync.ts";
const empty = () => ({ version: 1, visits: [], provinces: {} });
const v = (n) => ({
  code: "330100",
  name: "杭州市",
  province: "330000",
  color: "#4b937d",
  date: "",
  note: n,
});
test("a lost batch reply survives reload and newer local edits without a false self-conflict", async () => {
  let server = empty(),
    revision = 0,
    lost = true,
    receipts = new Map(),
    persisted;
  const api = {
    get: async () => ({ saved: server, revision }),
    apply: async () => {
      throw new Error("legacy path");
    },
    applyBatch: async (ops, base, id) => {
      if (receipts.has(id)) return receipts.get(id);
      if (base !== revision) throw new RevisionChanged();
      for (const op of ops) {
        server = applyOp(server, op);
        revision++;
      }
      receipts.set(id, revision);
      if (lost) {
        lost = false;
        throw new Error("reply lost");
      }
      return revision;
    },
  };
  const a = new SyncEngine(
    api,
    { saved: empty(), revision: 0 },
    { onChange: (c) => (persisted = structuredClone(c)) },
  );
  a.push({ ...empty(), visits: [v("first")] });
  await a.drain();
  assert.equal(a.status, "error");
  assert.ok(persisted.pendingBatch);
  a.stop();
  persisted.saved = { ...empty(), visits: [v("second")] };
  const b = new SyncEngine(
    api,
    { saved: persisted.confirmed, revision: persisted.revision },
    {},
    persisted.saved,
    persisted.pendingBatch,
  );
  b.retry();
  await b.drain();
  assert.equal(b.status, "idle");
  assert.equal(server.visits[0].note, "second");
  assert.equal(revision, 2);
  assert.equal(b.pending, false);
});
test("pending request is persisted before sending and refusal blocks cloud writes", async () => {
  let calls = 0;
  const engine = new SyncEngine(
    {
      get: async () => ({ saved: empty(), revision: 0 }),
      apply: async () => 0,
      applyBatch: async () => {
        calls++;
        return 1;
      },
    },
    { saved: empty(), revision: 0 },
    {
      onBeforeSend: () => {
        throw new Error("quota");
      },
    },
  );
  engine.push({ ...empty(), visits: [v("pending")] });
  await engine.drain();
  assert.equal(calls, 0);
  assert.equal(engine.status, "error");
  assert.equal(engine.pending, true);
});

test("malformed pending batch is protected instead of silently discarded", async () => {
  const { loadCloudCache, clearOtherCaches, cacheKey } =
    await import("../src/lib/session.ts");
  const data = new Map([
    [
      cacheKey("bad"),
      JSON.stringify({
        ...empty(),
        saved: empty(),
        confirmed: empty(),
        revision: 0,
        pendingBatch: false,
      }),
    ],
  ]);
  const storage = {
    getItem: (k) => data.get(k) ?? null,
    removeItem: (k) => data.delete(k),
    get length() {
      return data.size;
    },
    key: (i) => [...data.keys()][i] ?? null,
  };
  assert.throws(() => loadCloudCache(storage, "bad", (x) => x));
  clearOtherCaches(storage, "different", (x) => x);
  assert.equal(data.size, 1);
});
