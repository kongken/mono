import assert from "node:assert/strict";
import { test } from "node:test";
import catalog from "@kongken/geo-data/world-catalog.json" with { type: "json" };
import { coordinateText, emptyForm, formFromPlace, formLocation, newTrip, readForm } from "../src/lib/world-form.ts";
import { savePlace, worldCodeSet, emptyWorld } from "../src/lib/world.ts";

const codes = worldCodeSet(catalog);
const filled = (extra = {}) => ({ ...emptyForm(), name: "东京", country: "JP", lat: "35.6764", lng: "139.65", ...extra });
const error = (form) => {
  const read = readForm(form, codes);
  assert.ok("error" in read, "the form should be refused");
  return read.error;
};

test("a complete form becomes a draft with the name trimmed and the coordinates read", () => {
  const read = readForm(filled({ name: "  东京\u3000", trips: [{ id: "t1", date: "2026-03-01", note: "樱花" }] }), codes);
  assert.ok("draft" in read);
  assert.deepEqual(read.draft, {
    id: undefined,
    name: "东京",
    country: "JP",
    lat: 35.6764,
    lng: 139.65,
    trips: [{ id: "t1", date: "2026-03-01", note: "樱花" }],
  });
  // ...and the draft is accepted by the document.
  assert.equal(savePlace(emptyWorld(), read.draft, codes).places.length, 1);
});

test("each field is refused in the order the user sees it, with its own message", () => {
  assert.deepEqual(error(filled({ name: "" })), { field: "name", message: "请填写城市名称" });
  assert.equal(error(filled({ name: "\u00a0 \ufeff" })).field, "name");
  assert.match(error(filled({ name: "x".repeat(61) })).message, /最多 60 个字符/);
  assert.deepEqual(error(filled({ country: "" })), { field: "country", message: "请选择城市所在的国家或地区" });
  assert.match(error(filled({ country: "CN" })).message, /中国视图/);
  assert.match(error(filled({ country: "ZZ" })).message, /不在目录中/);
  assert.equal(error(filled({ lat: "" })).field, "lat");
  assert.match(error(filled({ lat: "91" })).message, /-90 到 90/);
  assert.equal(error(filled({ lng: "1e3" })).field, "lng");
  assert.match(error(filled({ lng: "-181" })).message, /-180 到 180/);
  // The first problem wins: a bad name hides a bad coordinate.
  assert.equal(error(filled({ name: "", lat: "x" })).field, "name");
});

test("a trip with an impossible date or a long note is refused, naming which trip", () => {
  const trips = [
    { id: "a", date: "2026-03-01", note: "" },
    { id: "b", date: "2026-02-30", note: "" },
  ];
  const bad = error(filled({ trips }));
  assert.deepEqual(bad.field, { trip: "b", part: "date" });
  assert.match(bad.message, /第 2 次旅行的日期不是真实存在的日期/);
  const long = error(filled({ trips: [{ id: "a", date: "", note: "x".repeat(1001) }] }));
  assert.deepEqual(long.field, { trip: "a", part: "note" });
  assert.match(long.message, /最多 1000 个字符/);
  // A trip without a date is fine, and so is the leap day.
  assert.ok("draft" in readForm(filled({ trips: [{ id: "a", date: "", note: "" }, { id: "b", date: "2024-02-29", note: "" }] }), codes));
});

test("a form made of a city comes back as the same city, and formLocation reads only complete coordinates", () => {
  const place = { id: "p1", name: "巴黎", country: "FR", lat: 48.8566, lng: 2.3522, trips: [{ id: "t1", date: "2026-05-01", note: "塞纳河" }] };
  const form = formFromPlace(place);
  assert.equal(form.countryChosen, true);
  const read = readForm(form, codes);
  assert.ok("draft" in read);
  assert.deepEqual(read.draft, place);
  assert.deepEqual(formLocation(form), { lat: 48.8566, lng: 2.3522 });
  assert.equal(formLocation({ lat: "48", lng: "" }), null);
  assert.equal(formLocation({ lat: "abc", lng: "1" }), null);
  assert.notEqual(newTrip().id, newTrip().id);
  assert.equal(coordinateText(35.123456789), "35.123457");
  assert.equal(coordinateText(-0.0000004), "0", "no negative zero");
});
