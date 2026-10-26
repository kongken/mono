import assert from "node:assert/strict";
import { test } from "node:test";
import catalog from "@kongken/geo-data/world-catalog.json" with { type: "json" };
import regions from "@kongken/geo-data/regions.json" with { type: "json" };
import fixture from "@kongken/fixtures/world-documents.json" with { type: "json" };
import { validateSaved } from "../src/lib/persistence.ts";
import {
  addPlace,
  addTrip,
  emptyWorld,
  fromMicroDegrees,
  hasAtMostSixDecimals,
  newPlaceId,
  normalizeCoordinate,
  removePlace,
  removeTrip,
  sameWorld,
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
