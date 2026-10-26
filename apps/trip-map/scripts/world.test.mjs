import assert from "node:assert/strict";
import { test } from "node:test";
import catalog from "@kongken/geo-data/world-catalog.json" with { type: "json" };
import regions from "@kongken/geo-data/regions.json" with { type: "json" };
import fixture from "@kongken/fixtures/world-documents.json" with { type: "json" };
import tripDates from "@kongken/fixtures/trip-dates.json" with { type: "json" };
import { validateSaved } from "../src/lib/persistence.ts";
import {
  addPlace,
  addTrip,
  emptyWorld,
  fromMicroDegrees,
  hasAtMostSixDecimals,
  newPlaceId,
  normalizeCoordinate,
  parseCoordinateInput,
  removePlace,
  removeTrip,
  sameWorld,
  savePlace,
  toggleCountry,
  toMicroDegrees,
  trimWorldName,
  updateTrip,
  validateWorld,
  WORLD_MAX_PLACES,
  WORLD_MAX_STRING_BYTES,
  WORLD_MAX_TRIPS,
  worldCodeSet,
  worldStringBytes,
  WorldValidationError,
} from "../src/lib/world.ts";
import { CHINA_UNREADABLE, DERIVED_COLOR, litCountries, summarizeChina, worldStats } from "../src/lib/world-stats.ts";

const codes = worldCodeSet(catalog);
const green = "#4b937d";
const clone = (value) => JSON.parse(JSON.stringify(value));
const rejects = (data, kind) =>
  assert.throws(
    () => validateWorld(data, codes),
    (error) => error instanceof WorldValidationError && (kind === undefined || error.kind === kind),
  );
const place = (id, country = "JP", extra = {}) => ({ id, name: id, country, lat: 1, lng: 2, trips: [], ...extra });
const doc = (countries = [], places = []) => ({ kind: "world", version: 1, countries, places });
const deepFreeze = (value) => {
  if (value && typeof value === "object" && !Object.isFrozen(value)) {
    Object.freeze(value);
    Object.values(value).forEach(deepFreeze);
  }
  return value;
};

test("the shared fixture's valid documents are accepted and come out canonical", () => {
  assert.ok(fixture.valid.length > 5);
  for (const c of fixture.valid) {
    const input = clone(c.document);
    assert.deepEqual(validateWorld(input, codes), c.canonical, c.name);
    assert.deepEqual(input, c.document, `${c.name}: the input is not modified`);
    assert.deepEqual(validateWorld(c.canonical, codes), c.canonical, `${c.name}: canonical form is stable`);
  }
});

test("the shared fixture's invalid documents are each rejected with their reason", () => {
  assert.ok(fixture.invalid.length > 50);
  const kinds = new Set();
  for (const c of fixture.invalid) {
    kinds.add(c.kind);
    assert.throws(
      () => validateWorld(clone(c.document), codes),
      (error) => error instanceof WorldValidationError && error.kind === c.kind,
      c.name,
    );
  }
  for (const kind of ["format", "kind", "version", "field", "country", "color", "duplicate", "id", "name", "coordinate", "date", "note"]) {
    assert.ok(kinds.has(kind), `no fixture case for ${kind}`);
  }
});

test("fixture scopes: only the cases that need a JSON text or a lone surrogate carry one, and they are the right ones", () => {
  const loneSurrogate = (text) => /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/.test(text);
  const hasLone = (value) =>
    typeof value === "string" ? loneSurrogate(value) : value && typeof value === "object" ? Object.values(value).some(hasLone) : false;
  for (const c of fixture.invalid) {
    assert.ok([undefined, "json-text", "utf16"].includes(c.scope), `${c.name}: unknown scope ${c.scope}`);
    assert.equal(c.scope === "utf16", hasLone(c.document), `${c.name}: utf16 exactly for documents with a lone surrogate`);
    if (c.scope === "json-text") assert.ok(["format", "field", "kind", "version", "country", "name", "coordinate", "date", "note"].includes(c.kind), c.name);
  }
  for (const c of fixture.valid) assert.equal(c.scope, undefined, `${c.name}: valid documents apply to both sides`);
  // Every case a Go test must run (no scope) is expressible in a Go JSON reader: none has a lone surrogate.
  assert.ok(fixture.invalid.filter((c) => !c.scope).length >= 40);
  assert.ok(!fixture.invalid.filter((c) => !c.scope).some((c) => hasLone(c.document)));
  assert.ok(fixture.invalid.filter((c) => c.scope === "utf16").length >= 2);
  assert.equal(fixture.coordinates.scope, "json-text");
  // A file of the other backup type is a file-level case: a typed decoder rejecting unknown fields would report `visits`, not the missing kind.
  const china = fixture.invalid.find((c) => c.name === "a China backup (no kind)");
  assert.equal(china.scope, "json-text");
  assert.equal(china.kind, "kind");
  assert.ok("visits" in china.document && !("kind" in china.document));
});

test("the 6-decimal tolerance rule at its edge: strictly below 1e-6 of a micro-degree passes", () => {
  const { accepted, rejected } = fixture.coordinates.tolerance;
  for (const { value, e6 } of accepted) {
    assert.ok(hasAtMostSixDecimals(value), String(value));
    assert.equal(toMicroDegrees(value), e6);
    assert.equal(validateWorld(doc([], [place("p1", "JP", { lat: value > 90 ? 0 : value, lng: value })]), codes).places[0].lng, fromMicroDegrees(e6), `canonical value of ${value}`);
  }
  for (const value of rejected) {
    assert.equal(hasAtMostSixDecimals(value), false, String(value));
    rejects(doc([], [place("p1", "JP", { lat: value })]), "coordinate");
  }
});

test("dates follow the shared trip-dates fixture, as the China footprints do", () => {
  const world = (date) => doc([], [place("p1", "JP", { trips: [{ id: "t1", date, note: "" }] })]);
  assert.ok(tripDates.valid.length > 5 && tripDates.invalid.length > 10);
  for (const date of tripDates.valid) assert.equal(validateWorld(world(date), codes).places[0].trips[0].date, date, `valid ${JSON.stringify(date)}`);
  for (const date of tripDates.invalid)
    assert.throws(() => validateWorld(world(date), codes), (e) => e instanceof WorldValidationError && e.kind === "date", `invalid ${JSON.stringify(date)}`);
});

// A generous bound: these inputs take milliseconds, and the quadratic code they replaced
// took tens of seconds, so a slow machine cannot turn them into false failures.
const FAST = 5000;
const elapsed = (run) => {
  const start = performance.now();
  const result = run();
  return { ms: performance.now() - start, result };
};

test("trimming is linear: a name with a huge run of inner whitespace costs milliseconds, not hours", () => {
  for (const [count, expected] of [[100_000, 100_002], [3_900_000, 3_900_002]]) {
    const { ms, result } = elapsed(() => trimWorldName(`a${" ".repeat(count)}b`));
    assert.equal(result.length, expected);
    assert.ok(ms < FAST, `${count} inner spaces took ${Math.round(ms)} ms`);
  }
  const edge = elapsed(() => trimWorldName(" ".repeat(3_900_000)));
  assert.equal(edge.result, "");
  assert.ok(edge.ms < FAST, `all-whitespace took ${Math.round(edge.ms)} ms`);
  // A file with one such name is refused quickly, as too long, rather than hanging the import.
  const hostile = doc([], [place("p1", "JP", { name: `a${" ".repeat(3_900_000)}b` })]);
  const refused = elapsed(() => assert.throws(() => validateWorld(hostile, codes), (e) => e.kind === "name"));
  assert.ok(refused.ms < FAST, `validating took ${Math.round(refused.ms)} ms`);
});

test("trimming agrees with the original anchored-regex definition on random text", () => {
  const reference = (name) => name.replace(/^[\p{White_Space}\uFEFF]+|[\p{White_Space}\uFEFF]+$/gu, "");
  const alphabet = [" ", "\t", "\u0085", "\u00a0", "\u3000", "\ufeff", "\u200b", "\u180e", "a", "日", "\ud83d\ude00", "\ud800", "\udc00", "-"];
  let seed = 12345;
  const random = (n) => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff), seed % n);
  for (let i = 0; i < 3000; i++) {
    const text = Array.from({ length: random(14) }, () => alphabet[random(alphabet.length)]).join("");
    assert.equal(trimWorldName(text), reference(text), JSON.stringify(text));
  }
});

test("the name rule's White_Space set is exactly the fixture's list, plus U+FEFF", () => {
  const listed = new Set(fixture.whitespace.map((s) => Number.parseInt(s.slice(2), 16)));
  const matched = new Set();
  for (let cp = 0; cp <= 0x10ffff; cp++) {
    if (cp >= 0xd800 && cp <= 0xdfff) continue;
    if (trimWorldName(String.fromCodePoint(cp) + "x") === "x") matched.add(cp);
  }
  assert.deepEqual([...matched].sort((a, b) => a - b), [...listed].sort((a, b) => a - b));
  assert.ok(listed.has(0xfeff) && listed.has(0x3000) && !listed.has(0x200b) && !listed.has(0x180e));
  assert.equal(trimWorldName("\u3000 东京 \ufeff"), "东京");
  assert.equal(trimWorldName("a b"), "a b");
});

test("coordinates: 6 decimals pass, more are rejected, normalising rounds half away from zero", () => {
  for (const c of fixture.coordinates.valid) {
    assert.ok(hasAtMostSixDecimals(c.value), String(c.value));
    assert.equal(toMicroDegrees(c.value), c.e6);
    assert.equal(fromMicroDegrees(c.e6), c.value);
  }
  for (const value of fixture.coordinates.invalid) {
    assert.equal(hasAtMostSixDecimals(value), false, String(value));
    rejects(doc([], [place("p1", "JP", { lat: value })]), "coordinate");
    rejects(doc([], [place("p1", "JP", { lng: value })]), "coordinate");
  }
  for (const c of fixture.coordinates.normalize) {
    assert.equal(toMicroDegrees(c.input), c.e6, String(c.input));
    assert.ok(hasAtMostSixDecimals(normalizeCoordinate(c.input)));
  }
  assert.ok(Object.is(toMicroDegrees(-0.0000004), 0), "never negative zero");
});

test("errors name the record that is wrong", () => {
  const data = doc([], [place("p1"), place("p2", "JP", { lat: 91 })]);
  assert.throws(() => validateWorld(data, codes), (e) => e.path === "places[1](p2).lat" && /p2/.test(e.message));
  const trips = doc([], [place("p1", "JP", { trips: [{ id: "t1", date: "", note: "" }, { id: "t2", date: "2026-02-30", note: "" }] })]);
  assert.throws(() => validateWorld(trips, codes), (e) => e.path === "places[0](p1).trips[1](t2).date" && e.kind === "date");
  assert.throws(() => validateWorld(doc([{ code: "CN", color: green }]), codes), (e) => e.path === "countries[0].code");
});

test("the documented limits: 5000 cities, 10000 trips, 2,500,000 UTF-8 bytes of strings", () => {
  assert.deepEqual(
    [fixture.limits.maxPlaces, fixture.limits.maxTrips, fixture.limits.maxStringBytes],
    [WORLD_MAX_PLACES, WORLD_MAX_TRIPS, WORLD_MAX_STRING_BYTES],
  );
  const places = (n) => Array.from({ length: n }, (_, i) => place(`p${i}`));
  assert.equal(validateWorld(doc([], places(5000)), codes).places.length, 5000);
  rejects(doc([], places(5001)), "limit");

  const withTrips = (n) => {
    const list = places(100);
    for (let i = 0; i < n; i++) list[i % 100].trips.push({ id: `t${i}`, date: "", note: "" });
    return doc([], list);
  };
  assert.equal(validateWorld(withTrips(10000), codes).places.length, 100);
  rejects(withTrips(10001), "limit");
});

test("the byte limit is UTF-8 bytes of all string fields, exactly, not UTF-16 length or JSON size", () => {
  // The measure, spelled out independently of the implementation.
  const sample = validateWorld(fixture.valid[1].document, codes);
  const strings = [
    ...sample.countries.flatMap((m) => [m.code, m.color]),
    ...sample.places.flatMap((p) => [p.id, p.name, p.country, ...p.trips.flatMap((t) => [t.id, t.date, t.note])]),
  ];
  assert.equal(worldStringBytes(sample), strings.reduce((n, s) => n + Buffer.byteLength(s), 0));

  // Fill one city's trips with notes until the total is exactly the limit, then one more byte.
  const base = doc([], [place("p1")]);
  const trips = [];
  let i = 0;
  const total = () => worldStringBytes({ ...base, places: [{ ...base.places[0], trips }] });
  let running = total();
  while (WORLD_MAX_STRING_BYTES - running > 1100) {
    const id = `t${i++}`;
    trips.push({ id, date: "", note: "n".repeat(1000) });
    running += id.length + 1000;
  }
  const last = { id: `t${i}`, date: "", note: "" };
  trips.push(last);
  last.note = "n".repeat(WORLD_MAX_STRING_BYTES - total());
  assert.equal(total(), WORLD_MAX_STRING_BYTES);
  assert.ok(last.note.length <= 1000);
  assert.equal(validateWorld({ ...base, places: [{ ...base.places[0], trips }] }, codes).places.length, 1);
  last.note += "n";
  assert.equal(total(), WORLD_MAX_STRING_BYTES + 1);
  rejects({ ...base, places: [{ ...base.places[0], trips }] }, "limit");

  // 900 notes of 1000 two-or-three-byte characters: short in UTF-16, long in UTF-8.
  const wide = (char) => doc([], [place("p1", "JP", { trips: Array.from({ length: 900 }, (_, k) => ({ id: `t${k}`, date: "", note: char.repeat(1000) })) })]);
  assert.equal(validateWorld(wide("a"), codes).places.length, 1);
  rejects(wide("日"), "limit");
  assert.equal(worldStringBytes(wide("é")) - worldStringBytes(wide("a")), 900 * 1000);
});

test("edits: mark colours are the least used, ties in palette order; CN and unknown codes are refused", () => {
  let world = emptyWorld();
  world = toggleCountry(world, "JP", codes);
  assert.equal(world.countries[0].color, green, "first mark takes the first palette colour");
  world = toggleCountry(toggleCountry(world, "FR", codes), "US", codes);
  const colorOf = (code) => world.countries.find((m) => m.code === code).color;
  assert.deepEqual([colorOf("JP"), colorOf("FR"), colorOf("US")], [green, "#e2af5c", "#d88370"]);
  const again = toggleCountry(world, "JP", codes);
  assert.deepEqual(again.countries.map((m) => m.code), ["FR", "US"], "toggling a marked country unmarks it");
  assert.throws(() => toggleCountry(world, "CN", codes), WorldValidationError);
  assert.throws(() => toggleCountry(world, "ZZ", codes), WorldValidationError);
  assert.throws(() => toggleCountry(world, "TW", codes), WorldValidationError);
});

test("edits: a city is normalised on the way in, and a rejected edit changes nothing", () => {
  const world = deepFreeze(emptyWorld());
  const next = addPlace(world, { name: "  东京\u3000", country: "JP", lat: 35.67620049, lng: 139.6503125 }, codes, "p1");
  assert.deepEqual(next.places, [{ id: "p1", name: "东京", country: "JP", lat: 35.6762, lng: 139.650313, trips: [] }]);
  assert.deepEqual(world, emptyWorld());
  for (const bad of [
    { name: "", country: "JP", lat: 1, lng: 1 },
    { name: "x", country: "CN", lat: 1, lng: 1 },
    { name: "x", country: "JP", lat: 91, lng: 1 },
    { name: "x", country: "JP", lat: Number.NaN, lng: 1 },
    { name: "a\tb", country: "JP", lat: 1, lng: 1 },
  ]) assert.throws(() => addPlace(next, bad, codes, "p2"), WorldValidationError);
  assert.equal(next.places.length, 1);
  assert.match(newPlaceId(1_700_000_000_000), /^p[0-9a-z]{17}$/);
  assert.ok(newPlaceId() !== newPlaceId());
  assert.throws(() => addPlace(next, { name: "x", country: "JP", lat: 1, lng: 1 }, codes, "p1"), (e) => e.kind === "duplicate");
});

test("edits: trips are added, changed and removed with the same rules as files", () => {
  const world = deepFreeze(addPlace(emptyWorld(), { name: "东京", country: "JP", lat: 35, lng: 139 }, codes, "p1"));
  const one = addTrip(world, "p1", { date: "2026-04-01", note: "樱花" }, codes, "t1");
  const two = addTrip(one, "p1", { date: "", note: "" }, codes, "t0");
  assert.deepEqual(two.places[0].trips.map((t) => t.id), ["t0", "t1"], "stored in id order");
  assert.equal(updateTrip(two, "p1", "t1", { note: "改" }, codes).places[0].trips[1].note, "改");
  assert.throws(() => updateTrip(two, "p1", "t1", { date: "2026-02-30" }, codes), (e) => e.kind === "date");
  assert.throws(() => addTrip(two, "p1", { date: "", note: "x" }, codes, "t1"), (e) => e.kind === "duplicate");
  assert.throws(() => addTrip(two, "nope", { date: "", note: "" }, codes), WorldValidationError);
  assert.equal(removeTrip(two, "p1", "t0", codes).places[0].trips.length, 1);
  assert.throws(() => removeTrip(two, "p1", "zzz", codes), WorldValidationError);
  assert.throws(() => removePlace(two, "nope", codes), WorldValidationError);
  assert.ok(sameWorld(two, validateWorld(clone(two), codes)));
  assert.equal(sameWorld(one, two), false);
});

// Acceptance scenarios of #97 (1-3), at the data layer.
const tokyoWorld = () => {
  let world = toggleCountry(emptyWorld(), "JP", codes);
  world = addPlace(world, { name: "东京", country: "JP", lat: 35.6762, lng: 139.6503 }, codes, "p1");
  world = addTrip(world, "p1", { date: "2026-04-01", note: "" }, codes, "t1");
  return addTrip(world, "p1", { date: "2026-10-01", note: "" }, codes, "t2");
};
const noChina = { status: "ok", lit: false };

test("scenario: Japan marked, Tokyo added with two trips gives 1 country, 1 city, 2 trips", () => {
  const world = tokyoWorld();
  assert.deepEqual(worldStats(world, noChina), { countries: 1, places: 1, trips: 2 });
  assert.deepEqual(litCountries(world, noChina), [{ code: "JP", color: world.countries[0].color, sources: ["mark", "place"] }]);
});

test("scenario: unmarking Japan keeps Tokyo and Japan stays lit; deleting the city leaves the marks alone", () => {
  const world = tokyoWorld();
  const unmarked = toggleCountry(world, "JP", codes);
  assert.deepEqual(unmarked.countries, []);
  assert.equal(unmarked.places.length, 1);
  assert.deepEqual(litCountries(unmarked, noChina), [{ code: "JP", color: DERIVED_COLOR, sources: ["place"] }]);
  assert.deepEqual(worldStats(unmarked, noChina), { countries: 1, places: 1, trips: 2 });

  const removed = removePlace(world, "p1", codes);
  assert.deepEqual(removed.countries, world.countries, "the independent mark is untouched");
  assert.deepEqual(worldStats(removed, noChina), { countries: 1, places: 0, trips: 0 });
  const bothGone = removePlace(unmarked, "p1", codes);
  assert.deepEqual(litCountries(bothGone, noChina), [], "no mark and no city: the country goes dark");
});

test("scenario: China lights CN from Hangzhou or a province mark, and CN never enters the world data", () => {
  const hangzhou = { version: 2, visits: [{ code: "330100", name: "杭州市", province: "330000", color: green, trips: [] }], provinces: {} };
  const province = { version: 2, visits: [], provinces: { 330000: green } };
  const taiwan = { version: 2, visits: [], provinces: { 710000: green } };
  const world = tokyoWorld();
  for (const saved of [hangzhou, province, taiwan]) {
    const lit = litCountries(world, summarizeChina(saved));
    assert.deepEqual(lit.map((c) => c.code), ["CN", "JP"]);
    assert.deepEqual(lit[0], { code: "CN", color: DERIVED_COLOR, sources: ["china"] });
    assert.deepEqual(worldStats(world, summarizeChina(saved)), { countries: 2, places: 1, trips: 2 });
  }
  assert.ok(!JSON.stringify(world).includes("CN"), "the stored document has no derived CN entry");
  assert.deepEqual(litCountries(world, summarizeChina({ visits: [], provinces: {} })).map((c) => c.code), ["JP"]);
  // The China footprints are read only: frozen input is neither changed nor does it throw.
  const frozen = deepFreeze(clone(hangzhou));
  litCountries(world, summarizeChina(frozen));
  assert.deepEqual(frozen, hangzhou);
});

test("scenario: China footprints under read protection do not light CN", () => {
  const lit = litCountries(tokyoWorld(), CHINA_UNREADABLE);
  assert.deepEqual(lit.map((c) => c.code), ["JP"]);
  assert.deepEqual(worldStats(tokyoWorld(), CHINA_UNREADABLE), { countries: 1, places: 1, trips: 2 });
});

test("a marked country shows its mark colour; one lit only by a city or China uses the fixed green", () => {
  let world = toggleCountry(emptyWorld(), "FR", codes);
  world = toggleCountry(world, "US", codes);
  world = addPlace(world, { name: "x", country: "DE", lat: 1, lng: 1 }, codes, "p1");
  world = addPlace(world, { name: "y", country: "US", lat: 1, lng: 1 }, codes, "p2");
  const lit = Object.fromEntries(litCountries(world, { status: "ok", lit: true }).map((c) => [c.code, c]));
  assert.equal(lit.FR.color, world.countries.find((m) => m.code === "FR").color);
  assert.equal(lit.US.color, world.countries.find((m) => m.code === "US").color);
  assert.deepEqual(lit.US.sources, ["mark", "place"]);
  assert.equal(lit.DE.color, DERIVED_COLOR);
  assert.equal(lit.CN.color, DERIVED_COLOR);
  assert.equal(DERIVED_COLOR, "#4b937d");
});

test("a world document is not a China backup, and the China validator refuses it", () => {
  assert.throws(() => validateSaved(tokyoWorld(), regions));
  assert.throws(() => validateSaved({ kind: "world", version: 1, countries: [], places: [] }, regions));
});

test("savePlace adds or replaces a city with its trips in one step, and a rejected draft changes nothing", () => {
  const start = deepFreeze(toggleCountry(emptyWorld(), "JP", codes));
  const trips = [
    { id: "t2", date: "2026-03-01", note: "春" },
    { id: "t1", date: "", note: "" },
  ];
  const added = savePlace(start, { name: " 东京 ", country: "JP", lat: 35.6764, lng: 139.65, trips }, codes);
  assert.equal(added.places.length, 1);
  assert.match(added.places[0].id, /^p[0-9a-z]+$/);
  assert.deepEqual(added.places[0].trips.map((t) => t.id), ["t1", "t2"]);
  assert.equal(added.places[0].name, "东京");
  assert.deepEqual(added.countries, start.countries, "country marks are untouched");

  const id = added.places[0].id;
  const edited = savePlace(added, { id, name: "东京都", country: "JP", lat: 35.7, lng: 139.7, trips: [trips[0]] }, codes);
  assert.equal(edited.places.length, 1);
  assert.equal(edited.places[0].name, "东京都");
  assert.deepEqual(edited.places[0].trips, [trips[0]]);

  const frozen = deepFreeze(clone(edited));
  for (const bad of [
    { name: "", country: "JP", lat: 1, lng: 1, trips: [] },
    { name: "x", country: "CN", lat: 1, lng: 1, trips: [] },
    { name: "x", country: "JP", lat: 91, lng: 1, trips: [] },
    { name: "x", country: "JP", lat: 1, lng: 1, trips: [{ id: "a", date: "2026-02-30", note: "" }] },
    { id, name: "x", country: "JP", lat: 1, lng: 1, trips: [{ id: "a", date: "", note: "" }, { id: "a", date: "", note: "" }] },
  ])
    assert.throws(() => savePlace(frozen, bad, codes), WorldValidationError);
  assert.throws(() => savePlace(frozen, { id: "missing", name: "x", country: "JP", lat: 1, lng: 1, trips: [] }, codes), WorldValidationError);
  assert.ok(sameWorld(frozen, edited));
});

test("coordinates typed into a form: plain decimals in range, each rejection says what is wrong", () => {
  assert.equal(parseCoordinateInput(" 35.6764 ", "lat"), 35.6764);
  assert.equal(parseCoordinateInput("-139", "lng"), -139);
  assert.equal(parseCoordinateInput("+.5", "lat"), 0.5);
  for (const [text, axis, words] of [
    ["", "lat", "十进制"],
    ["35,6", "lat", "十进制"],
    ["1e2", "lng", "十进制"],
    ["NaN", "lat", "十进制"],
    ["91", "lat", "-90 到 90"],
    ["-180.5", "lng", "-180 到 180"],
  ])
    assert.throws(
      () => parseCoordinateInput(text, axis),
      (error) => error instanceof WorldValidationError && error.kind === "coordinate" && error.detail.includes(words),
    );
});

test("an error's detail is the reason alone; its message names the record too", () => {
  assert.throws(
    () => validateWorld(doc([], [place("p1", "JP", { lat: 99 })]), codes),
    (error) => error.path.includes("p1") && error.message.includes(error.detail) && !error.detail.includes("p1"),
  );
});
