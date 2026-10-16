import test from "node:test";
import assert from "node:assert/strict";
import {
  SyncEngine,
  RevisionChanged,
  rebaseSaved,
  applyOp,
} from "../src/lib/sync.ts";

const green = "#4b937d";
const visit = (code, note = "") => ({
  code,
  name: code,
  province: "330000",
  color: green,
  trips: [{ id: `legacy-${code}`, date: "", note }],
});
// What recording a city with its trip sends: the city, then the trip.
const upsert = (v) => [
  { type: "upsertVisit", visit: { code: v.code, name: v.name, province: v.province, color: v.color } },
  ...v.trips.map((trip) => ({ type: "upsertTrip", code: v.code, trip })),
];
const saved = (visits = [], provinces = {}) => ({
  version: 2,
  visits,
  provinces,
});
function server(initial = saved(), revision = 0) {
  let state = initial;
  let online = true;
  let loseReply = false;
  const writes = [];
  return {
    writes,
    offline: () => {
      online = false;
    },
    online: () => {
      online = true;
    },
    loseReply: () => {
      loseReply = true;
    },
    change: (op) => {
      state = applyOp(state, op);
      revision++;
    },
    changeAll: (ops) => {
      for (const op of ops) {
        state = applyOp(state, op);
        revision++;
      }
    },
    get: async () => {
      if (!online) throw new Error("offline");
      return { saved: state, revision };
    },
    apply: async (op, expected) => {
      if (!online) throw new Error("offline");
      if (expected !== revision) throw new RevisionChanged();
      writes.push(op);
      state = applyOp(state, op);
      revision++;
      if (loseReply) {
        loseReply = false;
        throw new Error("reply lost");
      }
      return revision;
    },
  };
}

test("offline add/edit/delete and province marks survive reload and reconnect", async () => {
  const initial = saved([visit("remove"), visit("edit")], { 330000: green });
  const api = server(initial, 8);
  api.offline();
  let cache;
  const engine = new SyncEngine(
    api,
    { saved: initial, revision: 8 },
    {
      onChange: (value) => {
        cache = structuredClone(value);
      },
    },
  );
  engine.push(
    saved([visit("edit", "new note"), visit("add")], { 110000: green }),
  );
  await engine.drain();
  assert.equal(engine.status, "error");
  assert.equal(cache.confirmed.visits.length, 2);
  engine.stop();
  api.online();
  api.changeAll(upsert(visit("another-device")));
  const restored = new SyncEngine(
    api,
    { saved: cache.confirmed, revision: cache.revision },
    {},
    cache.saved,
  );
  restored.retry();
  await restored.drain();
  const result = await api.get();
  assert.deepEqual(result.saved.visits.map((v) => v.code).sort(), [
    "add",
    "another-device",
    "edit",
  ]);
  assert.equal(
    result.saved.visits.find((v) => v.code === "edit").trips[0].note,
    "new note",
  );
  assert.deepEqual(result.saved.provinces, { 110000: green });
  assert.equal(restored.pending, false);
});

for (const choice of ["local", "cloud"]) {
  test(`same-record edit/delete conflict waits for ${choice} choice; unrelated records merge`, async () => {
    const initial = saved([visit("a", "old"), visit("b")]);
    const api = server(initial, 2);
    const engine = new SyncEngine(api, { saved: initial, revision: 2 });
    api.change({ type: "deleteVisit", code: "a" });
    api.changeAll(upsert(visit("remote")));
    engine.push(saved([visit("a", "local"), visit("b"), visit("new")]));
    await engine.drain();
    assert.equal(engine.status, "conflict");
    assert.equal(engine.conflicts.length, 1);
    assert.equal(api.writes.length, 0);
    engine.resolve(choice);
    await engine.drain();
    const result = (await api.get()).saved;
    assert.equal(
      result.visits.some((v) => v.code === "a"),
      choice === "local",
    );
    assert.ok(result.visits.some((v) => v.code === "remote"));
    assert.ok(result.visits.some((v) => v.code === "new"));
    assert.equal(engine.pending, false);
  });
}

test("lost success response is reconciled without replaying the write", async () => {
  const api = server();
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 });
  api.loseReply();
  engine.push(saved([visit("a")]));
  await engine.drain();
  assert.equal(engine.pending, true);
  engine.retry();
  await engine.drain();
  // The city and its trip are two writes; each went out exactly once.
  assert.deepEqual(api.writes.map((w) => w.type), ["upsertVisit", "upsertTrip"]);
  assert.equal(engine.pending, false);
});

test("revision race refetches, merges unrelated records and retries", async () => {
  const api = server();
  const originalApply = api.apply;
  let once = true;
  api.apply = async (op, expected) => {
    if (once) {
      once = false;
      api.changeAll(upsert(visit("remote")));
    }
    return originalApply(op, expected);
  };
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 });
  engine.push(saved([visit("local")]));
  await engine.drain();
  assert.equal(engine.status, "idle");
  assert.deepEqual((await api.get()).saved.visits.map((v) => v.code).sort(), [
    "local",
    "remote",
  ]);
});

test("same-record race from initial revision zero is not silently overwritten", async () => {
  const api = server();
  const originalApply = api.apply;
  api.apply = async (op, expected) => {
    api.changeAll(upsert(visit("a", "remote")));
    return originalApply(op, expected);
  };
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 });
  engine.push(saved([visit("a", "local")]));
  await engine.drain();
  assert.equal(engine.status, "conflict");
  assert.equal((await api.get()).saved.visits[0].trips[0].note, "remote");
});

test("province conflicts and city changes remain independent", () => {
  const base = saved([visit("a")], { 330000: green });
  const local = saved([], { 330000: "#e2af5c" });
  const remote = saved([visit("a")], { 330000: "#719fc6" });
  const result = rebaseSaved(base, local, remote);
  assert.equal(result.conflicts.length, 1);
  assert.equal(result.conflicts[0].type, "upsertMark");
  assert.equal(result.saved.visits.length, 0);
});

test("detached engine does not send with the next account's token", async () => {
  let finish;
  const api = server();
  const get = api.get;
  api.get = () =>
    new Promise((resolve) => {
      finish = async () => resolve(await get());
    });
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 });
  engine.push(saved([visit("a")]));
  await new Promise((resolve) => setTimeout(resolve, 0));
  engine.stop();
  await finish();
  await engine.drain();
  assert.equal(api.writes.length, 0);
});

test("continuous contention is bounded and keeps unsent edits", async () => {
  const api = server();
  api.apply = async () => {
    throw new RevisionChanged();
  };
  const engine = new SyncEngine(api, { saved: saved(), revision: 0 });
  engine.push(saved([visit("a")]));
  await engine.drain();
  assert.equal(engine.status, "error");
  assert.equal(engine.cache.saved.visits[0].code, "a");
});
