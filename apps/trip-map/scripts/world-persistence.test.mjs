import assert from "node:assert/strict";
import { test } from "node:test";
import catalog from "@kongken/geo-data/world-catalog.json" with { type: "json" };
import regions from "@kongken/geo-data/regions.json" with { type: "json" };
import { validateSaved } from "../src/lib/persistence.ts";
import {
  classifyBackup,
  createWorldWriter,
  exportWorldBackup,
  loadWorldLocal,
  overwriteAfterConflict,
  previewWorldImport,
  recoverWorld,
  WORLD_RECOVERY_MAX_RECORDS,
  resolveUnreadable,
  WORLD_BACKUP_PREFIX,
  WORLD_KEY,
} from "../src/lib/world-persistence.ts";
import { addPlace, addTrip, emptyWorld, toggleCountry, validateWorld, WORLD_MAX_IMPORT_BYTES, worldCodeSet, WorldValidationError } from "../src/lib/world.ts";

const codes = worldCodeSet(catalog);
const NOW = new Date(1_700_000_000_000);

/** A localStorage stand-in that records every write and can be told to refuse some. */
function memoryStorage(initial = {}, { refuse = () => false, failRead = false } = {}) {
  const map = new Map(Object.entries(initial));
  const log = { sets: [], gets: [] };
  return {
    map,
    log,
    getItem(key) {
      log.gets.push(key);
      if (failRead) throw new Error("storage blocked");
      return map.has(key) ? map.get(key) : null;
    },
    setItem(key, value) {
      if (refuse(key)) throw new Error("quota");
      log.sets.push(key);
      map.set(key, value);
    },
  };
}

const sample = () => {
  let world = toggleCountry(emptyWorld(), "JP", codes);
  world = addPlace(world, { name: "东京", country: "JP", lat: 35.6762, lng: 139.6503 }, codes, "p1");
  return addTrip(world, "p1", { date: "2026-04-01", note: "樱花" }, codes, "t1");
};
const sampleText = () => JSON.stringify(sample());
const chinaKeys = {
  "shanhai-travel-v2": '{"version":2,"visits":[],"provinces":{}}',
  "shanhai-cloud-v1": '{"anything":1}',
};

test("opening the page reads once and writes nothing, whatever it finds", () => {
  const cases = [
    {},
    { [WORLD_KEY]: sampleText() },
    { [WORLD_KEY]: "{not json" },
    { [WORLD_KEY]: JSON.stringify({ kind: "world", version: 9, countries: [], places: [] }) },
  ];
  for (const initial of cases) {
    const storage = memoryStorage({ ...chinaKeys, ...initial });
    const before = new Map(storage.map);
    loadWorldLocal(storage, codes);
    assert.deepEqual(storage.log.sets, []);
    assert.deepEqual(storage.map, before);
    assert.deepEqual(storage.log.gets, [WORLD_KEY], "only the world key is read");
  }
});

test("load: empty, ok, unavailable", () => {
  assert.deepEqual(loadWorldLocal(memoryStorage(), codes), { status: "empty", document: emptyWorld(), raw: null });
  assert.equal(loadWorldLocal(memoryStorage({ [WORLD_KEY]: "" }), codes).status, "empty");
  const ok = loadWorldLocal(memoryStorage({ [WORLD_KEY]: sampleText() }), codes);
  assert.equal(ok.status, "ok");
  assert.deepEqual(ok.document, sample());
  assert.equal(ok.raw, sampleText());
  assert.equal(loadWorldLocal(memoryStorage({}, { failRead: true }), codes).status, "unavailable");
});

test("load: unreadable text is kept as is and never becomes an empty document", () => {
  const damaged = [
    "{not json",
    JSON.stringify({ ...sample(), version: 2 }),
    JSON.stringify({ ...sample(), countries: [{ code: "CN", color: "#4b937d" }] }),
    JSON.stringify({ version: 2, visits: [], provinces: {} }),
    "null",
    "[]",
  ];
  for (const raw of damaged) {
    const load = loadWorldLocal(memoryStorage({ [WORLD_KEY]: raw }), codes);
    assert.equal(load.status, "unreadable", raw);
    assert.equal(load.raw, raw);
    assert.ok(load.reason.length > 0);
  }
});

test("load: the reason an unreadable text gives is the reason alone, without the path of the record", () => {
  const raw = JSON.stringify({ ...sample(), countries: [{ code: "JP", color: "#4b937d" }, { code: "ZZ", color: "#4b937d" }] });
  const load = loadWorldLocal(memoryStorage({ [WORLD_KEY]: raw }), codes);
  assert.equal(load.status, "unreadable");
  assert.match(load.reason, /不在目录中/);
  assert.doesNotMatch(load.reason, /countries|\[\d+\]|\.code/);
  assert.match(loadWorldLocal(memoryStorage({ [WORLD_KEY]: "{not json" }), codes).reason, /JSON/);
});

test("recovery keeps only valid records, counts what it drops and never repairs", () => {
  const raw = JSON.stringify({
    kind: "world",
    version: 1,
    countries: [{ code: "JP", color: "#4b937d" }, { code: "CN", color: "#4b937d" }, { code: "JP", color: "#e2af5c" }, { code: "FR", color: "red" }],
    places: [
      { id: "p1", name: "东京", country: "JP", lat: 35, lng: 139, trips: [{ id: "t1", date: "2026-04-01", note: "" }, { id: "t2", date: "2026-02-30", note: "" }, { id: "t1", date: "", note: "dup" }] },
      { id: "p2", name: "", country: "JP", lat: 1, lng: 1, trips: [{ id: "t3", date: "", note: "" }] },
      { id: "p3", name: "x", country: "CN", lat: 1, lng: 1, trips: [] },
      { id: "p1", name: "again", country: "JP", lat: 1, lng: 1, trips: [] },
      { id: "p4", name: "Paris", country: "FR", lat: 48.8566, lng: 2.3522, extra: true, trips: [] },
    ],
  });
  const load = loadWorldLocal(memoryStorage({ [WORLD_KEY]: raw }), codes);
  assert.equal(load.status, "unreadable");
  const { recovery } = load;
  assert.deepEqual(recovery.document.countries, [{ code: "JP", color: "#4b937d" }]);
  assert.deepEqual(recovery.document.places.map((p) => [p.id, p.trips.map((t) => t.id)]), [["p1", ["t1"]]]);
  assert.deepEqual(
    [recovery.discardedCountries, recovery.discardedPlaces, recovery.discardedTrips],
    [3, 4, 3],
    "countries: CN, duplicate JP, bad colour; cities: p2, p3, repeated p1, p4 (unknown field); trips: t2, repeated t1, and p2's t3",
  );
  assert.deepEqual(validateWorld(recovery.document, codes), recovery.document);

  assert.equal(recoverWorld("{not json", codes), null);
  assert.equal(recoverWorld(JSON.stringify({ kind: "world", version: 2, countries: [{ code: "JP", color: "#4b937d" }], places: [] }), codes), null, "a newer version is never salvaged");
  assert.equal(recoverWorld(JSON.stringify({ kind: "world", version: 1, countries: [{ code: "ZZ", color: "#4b937d" }], places: [] }), codes), null, "nothing usable");
});

// Recovery runs on every unreadable start. The quadratic version took about 5 s for 40 000
// cities and grows with the square: 100 000 took about 40 s. Each case below takes about a
// second now, so this generous bound only fails for a real regression.
const FAST = 15_000;
const timed = (run) => {
  const start = performance.now();
  const result = run();
  return { ms: performance.now() - start, result };
};
const damaged = (places) => JSON.stringify({ kind: "world", version: 1, countries: [], places });

test("recovering a huge damaged text is linear and gives up at once when it cannot fit", () => {
  const city = (i, name = "x") => ({ id: `p${i}`, name, country: "JP", lat: 1, lng: 2, trips: [] });
  const cases = {
    "100000 valid cities (over the limit)": damaged(Array.from({ length: 100_000 }, (_, i) => city(i))),
    "100000 invalid cities": damaged(Array.from({ length: 100_000 }, (_, i) => city(i, ""))),
    "one city with 100000 trips (over the limit)": damaged([{ ...city(1), trips: Array.from({ length: 100_000 }, (_, i) => ({ id: `t${i}`, date: "", note: "" })) }]),
  };
  for (const [label, raw] of Object.entries(cases)) {
    const { ms, result } = timed(() => loadWorldLocal(memoryStorage({ [WORLD_KEY]: raw }), codes));
    assert.equal(result.status, "unreadable", label);
    assert.equal(result.recovery, null, `${label}: nothing is truncated to fit, so no recovery is offered`);
    assert.equal(result.raw, raw, `${label}: the text is kept`);
    assert.ok(ms < FAST, `${label}: ${Math.round(ms)} ms`);
  }
  // Under the limit, with part of it damaged: recovered, and still fast.
  const mixed = damaged(Array.from({ length: 5000 }, (_, i) => city(i, i % 2 ? "" : "x")));
  const { ms, result } = timed(() => loadWorldLocal(memoryStorage({ [WORLD_KEY]: mixed }), codes));
  assert.equal(result.recovery.document.places.length, 2500);
  assert.equal(result.recovery.discardedPlaces, 2500);
  assert.ok(ms < FAST, `${Math.round(ms)} ms`);
});

test("recovery has a total record budget: over it nothing is scanned, the text is kept and can still be replaced", () => {
  const one = { id: "p1", name: "x", country: "JP", lat: 1, lng: 2 };
  // One valid city plus `n` unusable trips: the scan sees 1 + n records.
  const withTrips = (n) => damaged([{ ...one, trips: Array.from({ length: n }, () => ({})) }]);
  const atBudget = withTrips(WORLD_RECOVERY_MAX_RECORDS - 1);
  const { ms: budgetMs, result: kept } = timed(() => loadWorldLocal(memoryStorage({ [WORLD_KEY]: atBudget }), codes));
  assert.ok(budgetMs < FAST, `a full budget of unusable records took ${Math.round(budgetMs)} ms`);
  assert.equal(kept.recovery.document.places.length, 1, "exactly at the budget is still scanned");
  assert.equal(kept.recovery.discardedTrips, WORLD_RECOVERY_MAX_RECORDS - 1);

  const over = withTrips(WORLD_RECOVERY_MAX_RECORDS);
  const storage = memoryStorage({ ...chinaKeys, [WORLD_KEY]: over });
  const load = loadWorldLocal(storage, codes);
  assert.equal(load.status, "unreadable");
  assert.equal(load.recovery, null, "no truncated recovery is offered");
  assert.equal(load.raw, over, "the text is kept byte for byte");
  assert.deepEqual(storage.log.sets, [], "reading wrote nothing");

  // The budget counts marks, cities and trips together, and is checked before anything is validated.
  const shapes = {
    "marks": JSON.stringify({ kind: "world", version: 1, countries: Array(WORLD_RECOVERY_MAX_RECORDS + 1).fill(1), places: [] }),
    "cities": damaged(Array(WORLD_RECOVERY_MAX_RECORDS + 1).fill(1)),
    "marks, cities and trips together": JSON.stringify({
      kind: "world", version: 1, countries: Array(10_000).fill(1),
      places: [{ ...one, trips: Array(10_000).fill({}) }, ...Array(10_000).fill(1)],
    }),
    "half a million tiny records": JSON.stringify({ kind: "world", version: 1, countries: Array(500_000).fill(1), places: [] }),
  };
  for (const [label, raw] of Object.entries(shapes)) {
    const { ms, result } = timed(() => loadWorldLocal(memoryStorage({ [WORLD_KEY]: raw }), codes));
    assert.equal(result.status, "unreadable", label);
    assert.equal(result.recovery, null, label);
    assert.equal(result.raw, raw, label);
    assert.ok(ms < 2000, `${label}: scanning the counts took ${Math.round(ms)} ms`);
  }

  // The user can still start from empty: the oversized text is copied first.
  const resolved = resolveUnreadable(storage, load, "empty", NOW);
  assert.equal(resolved.status, "ok");
  assert.equal(storage.map.get(`${WORLD_KEY}-unreadable-${NOW.getTime()}`), over);
  assert.equal(storage.map.get(WORLD_KEY), over, "resolving alone does not replace it");
});

test("the recovered document is checked as a whole: over the byte limit it is not offered, and it comes out in canonical order", () => {
  const one = { id: "p1", name: "x", country: "JP", lat: 1, lng: 2 };
  // 900 usable notes of 1000 three-byte characters are 2.7 MB of strings, over the 2,500,000 limit, although
  // there are few cities and trips and each record is valid on its own; one more city is unusable.
  const heavy = { ...one, trips: Array.from({ length: 900 }, (_, i) => ({ id: `t${i}`, date: "", note: "日".repeat(1000) })) };
  const raw = damaged([heavy, { ...one, id: "p2", name: "" }]);
  const load = loadWorldLocal(memoryStorage({ [WORLD_KEY]: raw }), codes);
  assert.equal(load.status, "unreadable");
  assert.equal(load.recovery, null, "nothing is truncated to fit the byte limit");
  assert.equal(load.raw, raw);
  // The same cities with lighter notes recover fine, so it is the byte limit that refused the first.
  const light = damaged([{ ...heavy, trips: heavy.trips.map((t) => ({ ...t, note: "n" })) }, { ...one, id: "p2", name: "" }]);
  assert.equal(loadWorldLocal(memoryStorage({ [WORLD_KEY]: light }), codes).recovery.document.places[0].trips.length, 900);

  // Records arrive in any order and leave sorted by code and id, as every other document does.
  const scrambled = JSON.stringify({
    kind: "world",
    version: 1,
    countries: [{ code: "US", color: "#e2af5c" }, { code: "JP", color: "#4b937d" }, { code: "ZZ", color: "x" }, { code: "FR", color: "#719fc6" }],
    places: [
      { ...one, id: "pb", trips: [{ id: "t9", date: "", note: "" }, { id: "t10", date: "", note: "" }, { id: "T1", date: "", note: "" }] },
      { ...one, id: "pa", trips: [{ id: "t2", date: "", note: "" }, { id: "bad", date: "2026-02-30", note: "" }, { id: "t1", date: "", note: "" }] },
      { ...one, id: "p0", name: "" },
    ],
  });
  const { document } = loadWorldLocal(memoryStorage({ [WORLD_KEY]: scrambled }), codes).recovery;
  assert.deepEqual(document.countries.map((m) => m.code), ["FR", "JP", "US"]);
  assert.deepEqual(document.places.map((p) => [p.id, p.trips.map((t) => t.id)]), [["pa", ["t1", "t2"]], ["pb", ["T1", "t10", "t9"]]]);
  assert.deepEqual(validateWorld(JSON.parse(JSON.stringify(document)), codes), document, "it is already canonical");
});

test("a damaged text can be recovered or replaced only after a copy of it is kept", () => {
  const raw = JSON.stringify({ ...sample(), places: [...sample().places, { id: "p9", name: "", country: "JP", lat: 1, lng: 1, trips: [] }] });
  for (const choice of ["recover", "empty"]) {
    const storage = memoryStorage({ ...chinaKeys, [WORLD_KEY]: raw });
    const load = loadWorldLocal(storage, codes);
    const resolution = resolveUnreadable(storage, load, choice, NOW);
    assert.equal(resolution.status, "ok");
    assert.equal(storage.map.get(`${WORLD_KEY}-unreadable-${NOW.getTime()}`), raw, "the original is kept byte for byte");
    assert.equal(storage.map.get(WORLD_KEY), raw, "resolving alone does not replace it");
    assert.equal(resolution.document.places.length, choice === "recover" ? 1 : 0);
    assert.deepEqual(resolution.writer.write(resolution.document), { status: "ok" });
    assert.equal(storage.map.get(WORLD_KEY), JSON.stringify(resolution.document));
    assert.equal(storage.map.get(`${WORLD_KEY}-unreadable-${NOW.getTime()}`), raw);
  }
});

test("when the copy cannot be kept, nothing is replaced", () => {
  const raw = "{broken";
  const storage = memoryStorage({ [WORLD_KEY]: raw }, { refuse: (key) => key.includes("-unreadable-") });
  const load = loadWorldLocal(storage, codes);
  assert.deepEqual(resolveUnreadable(storage, load, "empty", NOW), { status: "copy-failed" });
  assert.equal(storage.map.get(WORLD_KEY), raw);
  assert.deepEqual(storage.log.sets, []);
});

test("writing: round trips, and refuses to overwrite what this page has not seen", () => {
  const storage = memoryStorage({ ...chinaKeys });
  const writer = createWorldWriter(storage, null);
  assert.deepEqual(writer.write(sample()), { status: "ok" });
  assert.deepEqual(loadWorldLocal(storage, codes).document, sample());

  // Another tab changes the stored text behind this page's back.
  const theirs = JSON.stringify(toggleCountry(emptyWorld(), "FR", codes));
  storage.map.set(WORLD_KEY, theirs);
  const setsBefore = storage.log.sets.length;
  assert.deepEqual(writer.write(emptyWorld()), { status: "conflict", other: theirs });
  assert.equal(storage.log.sets.length, setsBefore, "a conflict writes nothing");
  assert.equal(storage.map.get(WORLD_KEY), theirs);

  // A page that started on an empty store also detects another tab's first write.
  const fresh = memoryStorage();
  const early = createWorldWriter(fresh, null);
  fresh.map.set(WORLD_KEY, theirs);
  assert.equal(early.write(sample()).status, "conflict");
});

test("conflict: keeping this page's data first copies the other tab's text", () => {
  const storage = memoryStorage({ ...chinaKeys });
  const writer = createWorldWriter(storage, null);
  const theirs = JSON.stringify(toggleCountry(emptyWorld(), "FR", codes));
  storage.map.set(WORLD_KEY, theirs);
  const attempt = writer.write(sample());
  assert.equal(attempt.status, "conflict");
  assert.deepEqual(overwriteAfterConflict(storage, writer, sample(), attempt.other, NOW), { status: "ok" });
  assert.equal(storage.map.get(`${WORLD_KEY}-conflict-${NOW.getTime()}`), theirs);
  assert.equal(storage.map.get(WORLD_KEY), JSON.stringify(sample()));
  assert.deepEqual(writer.write(sample()), { status: "ok" }, "the baseline moved on");

  const guarded = memoryStorage({}, { refuse: (key) => key.includes("-conflict-") });
  const w = createWorldWriter(guarded, null);
  guarded.map.set(WORLD_KEY, theirs);
  assert.deepEqual(overwriteAfterConflict(guarded, w, sample(), theirs, NOW), { status: "copy-failed" });
  assert.equal(guarded.map.get(WORLD_KEY), theirs, "their text is untouched when it cannot be kept");
});

test("a refused write reports the error and leaves the stored text alone", () => {
  const storage = memoryStorage({ [WORLD_KEY]: sampleText() }, { refuse: (key) => key === WORLD_KEY });
  const writer = createWorldWriter(storage, sampleText());
  const world = toggleCountry(sample(), "FR", codes);
  assert.deepEqual(writer.write(world), { status: "error" });
  assert.equal(storage.map.get(WORLD_KEY), sampleText());
  assert.ok(exportWorldBackup(world, "2026-10-26").text.includes('"FR"'), "the caller still has the data to export");
  const blocked = createWorldWriter(memoryStorage({}, { failRead: true }), null);
  assert.deepEqual(blocked.write(world), { status: "error" });
});

test("the world only ever touches its own keys and leaves the China footprints alone", () => {
  const storage = memoryStorage({ ...chinaKeys, [WORLD_KEY]: "{broken" });
  const load = loadWorldLocal(storage, codes);
  const resolution = resolveUnreadable(storage, load, "empty", NOW);
  resolution.writer.write(sample());
  const theirs = "x";
  storage.map.set(WORLD_KEY, theirs);
  const attempt = resolution.writer.write(emptyWorld());
  overwriteAfterConflict(storage, resolution.writer, emptyWorld(), attempt.other, NOW);
  for (const key of [...storage.log.sets, ...storage.log.gets]) assert.ok(key.startsWith("shanhai-world-v1"), key);
  for (const [key, value] of Object.entries(chinaKeys)) assert.equal(storage.map.get(key), value);
});

test("backup: filename, content and an exact round trip", () => {
  const { filename, text } = exportWorldBackup(sample(), "2026-10-26");
  assert.equal(filename, "山海足迹-世界-2026-10-26.json");
  assert.ok(filename.startsWith(WORLD_BACKUP_PREFIX));
  assert.notEqual(filename, "山海足迹-2026-10-26.json", "distinct from the China backup's name");
  assert.ok(text.endsWith("\n"));
  const preview = previewWorldImport(text, emptyWorld(), codes);
  assert.deepEqual(preview.document, sample());
  assert.deepEqual(preview.countries, { added: 1, overwritten: 0, unchanged: 0, discarded: 0 });
  assert.deepEqual(preview.places, { added: 1, overwritten: 0, unchanged: 0, discarded: 0 });
  assert.deepEqual(preview.trips, { added: 1, overwritten: 0, unchanged: 0, discarded: 0 });
});

test("import is a whole replacement, previewed by what it adds, overwrites and discards", () => {
  const current = addTrip(
    addPlace(addPlace(toggleCountry(toggleCountry(emptyWorld(), "JP", codes), "FR", codes), { name: "东京", country: "JP", lat: 35.6762, lng: 139.6503 }, codes, "p1"), { name: "巴黎", country: "FR", lat: 48.8566, lng: 2.3522 }, codes, "p2"),
    "p1", { date: "2026-04-01", note: "樱花" }, codes, "t1",
  );
  const incoming = addTrip(
    addTrip(
      addPlace(toggleCountry(emptyWorld(), "JP", codes), { name: "东京都", country: "JP", lat: 35.6762, lng: 139.6503 }, codes, "p1"),
      "p1", { date: "2026-04-01", note: "樱花" }, codes, "t1",
    ),
    "p1", { date: "2027-01-01", note: "" }, codes, "t2",
  );
  incoming.countries[0].color = "#d88370";
  const preview = previewWorldImport(JSON.stringify(incoming), current, codes);
  assert.deepEqual(preview.document, incoming, "the imported document replaces the current one");
  assert.deepEqual(preview.countries, { added: 0, overwritten: 1, unchanged: 0, discarded: 1 });
  assert.deepEqual(preview.places, { added: 0, overwritten: 1, unchanged: 0, discarded: 1 });
  assert.deepEqual(preview.trips, { added: 1, overwritten: 0, unchanged: 1, discarded: 0 });
});

test("previewing an import reads and writes nothing", () => {
  const storage = memoryStorage({ ...chinaKeys, [WORLD_KEY]: sampleText() });
  const before = new Map(storage.map);
  previewWorldImport(exportWorldBackup(emptyWorld(), "2026-10-26").text, sample(), codes);
  assert.deepEqual(storage.map, before);
  assert.deepEqual([storage.log.sets, storage.log.gets], [[], []]);
});

test("import refuses a China backup, other files and invalid world files, with a reason", () => {
  const china = JSON.stringify({ version: 2, visits: [], provinces: {} });
  const oldChina = JSON.stringify({ version: 1, visits: [], provinces: {} });
  const refused = (text, kind, pattern) =>
    assert.throws(
      () => previewWorldImport(text, emptyWorld(), codes),
      (error) => error instanceof WorldValidationError && error.kind === kind && pattern.test(error.message),
      text.slice(0, 40),
    );
  refused(china, "kind", /中国足迹备份/);
  refused(oldChina, "kind", /中国足迹备份/);
  refused('{"hello":1}', "kind", /世界备份/);
  refused("[]", "kind", /世界备份/);
  refused("{broken", "format", /JSON/);
  refused(JSON.stringify({ ...sample(), version: 2 }), "version", /version/);
  refused(JSON.stringify({ ...sample(), countries: [{ code: "CN", color: "#4b937d" }] }), "country", /中国/);
  refused(" ".repeat(WORLD_MAX_IMPORT_BYTES) + "{}", "limit", /4000000/);
  assert.equal(classifyBackup(JSON.parse(china)), "china");
  assert.equal(classifyBackup(sample()), "world");
  assert.equal(classifyBackup(null), "unknown");
  // The byte limit counts UTF-8 bytes, not characters.
  refused(" ".repeat(WORLD_MAX_IMPORT_BYTES - 1) + "日", "limit", /4000000/);
  assert.throws(() => previewWorldImport(" ".repeat(WORLD_MAX_IMPORT_BYTES - 2) + "{}", emptyWorld(), codes), (error) => error.kind !== "limit");
});

test("the China backup is still read by the China validator, and a world backup is not", () => {
  assert.doesNotThrow(() => validateSaved({ version: 2, visits: [], provinces: {} }, regions));
  assert.throws(() => validateSaved(JSON.parse(exportWorldBackup(sample(), "2026-10-26").text), regions));
});
