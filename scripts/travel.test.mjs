import test from "node:test";
import assert from "node:assert/strict";
import { automaticColor, automaticProvinceColor, provinceColor, colors, toggleProvince, toggleVisit } from "../src/lib/travel.ts";
const initial = () => ({
  version: 1,
  visits: [],
  provinces: { 330000: colors[0].value },
});
const city = { code: "330100", name: "杭州市" };
test("one click creates a visit, another click removes it without affecting province flags", () => {
  const before = initial();
  const marked = toggleVisit(before, "330000", city, "2026-10-12");
  assert.equal(marked.visits.length, 1);
  assert.equal(marked.visits[0].date, "2026-10-12");
  assert.equal(marked.visits[0].note, "");
  assert.equal(before.visits.length, 0);
  const removed = toggleVisit(marked, "330000", city, "2026-10-12");
  assert.equal(removed.visits.length, 0);
  assert.deepEqual(removed.provinces, before.provinces);
});
test("automatic colors balance within each province and reuse the least-used color", () => {
  let state = initial();
  for (let i = 0; i < 5; i++)
    state = toggleVisit(
      state,
      "330000",
      { code: `city-${i}`, name: `城市${i}` },
      "",
    );
  assert.deepEqual(
    state.visits.map((v) => v.color),
    colors.map((c) => c.value),
  );
  state = toggleVisit(state, "330000", { code: "city-2", name: "城市2" }, "");
  assert.equal(automaticColor(state, "330000"), colors[2].value);
  assert.equal(automaticColor(state, "320000"), colors[1].value);
});
test("toggling one city preserves other cities and existing user-selected colors and notes", () => {
  const other = {
    code: "330200",
    name: "宁波市",
    province: "330000",
    color: colors[4].value,
    date: "2024-05-01",
    note: "保留这段记忆",
  };
  const before = { ...initial(), visits: [other] };
  const marked = toggleVisit(before, "330000", city, "2026-10-12");
  assert.deepEqual(marked.visits[0], other);
  assert.equal(marked.visits[1].color, colors[0].value);
  assert.deepEqual(toggleVisit(marked, "330000", city, "").visits, [other]);
});

test("first cities in different provinces balance national province colors", () => {
  let state = { version: 1, visits: [], provinces: {} };
  for (let i = 0; i < colors.length; i++) {
    state = toggleVisit(state, `province-${i}`, { code: `city-${i}`, name: `城市${i}` }, "");
  }
  assert.deepEqual(state.visits.map((visit) => visit.color), colors.map((color) => color.value));
});


test("independent province flags and city visits share national color allocation", () => {
  let state = { version: 1, visits: [], provinces: {} };
  state = toggleProvince(state, "330000");
  state = toggleVisit(state, "320000", { code: "320100", name: "南京市" }, "");
  state = toggleProvince(state, "340000");
  assert.equal(provinceColor(state, "330000"), colors[0].value);
  assert.equal(provinceColor(state, "320000"), colors[1].value);
  assert.equal(provinceColor(state, "340000"), colors[2].value);
  const flagged = toggleProvince(state, "320000");
  assert.equal(flagged.provinces["320000"], colors[1].value);
  assert.deepEqual(flagged.visits, state.visits);
  assert.equal(automaticProvinceColor(flagged, "350000"), colors[3].value);
  assert.deepEqual(toggleProvince(flagged, "320000"), state);
  const withCity = toggleVisit(state, "340000", { code: "340100", name: "合肥市" }, "");
  assert.equal(withCity.visits.at(-1).color, colors[2].value);
  assert.equal(provinceColor(toggleProvince(withCity, "340000"), "340000"), colors[2].value);
});

test("national allocation counts each explored province once and preserves existing colors", () => {
  const state = {
    version: 1,
    provinces: { "330000": colors[4].value },
    visits: [
      { code: "330100", name: "杭州市", province: "330000", color: colors[0].value, date: "", note: "" },
      { code: "330200", name: "宁波市", province: "330000", color: colors[1].value, date: "", note: "" },
    ],
  };
  const before = structuredClone(state);
  assert.equal(automaticProvinceColor(state, "320000"), colors[0].value);
  assert.equal(automaticProvinceColor(state, "330000"), colors[4].value);
  assert.deepEqual(state, before);
  let flags = { version: 1, visits: [], provinces: {} };
  for (let i = 0; i < 6; i++) flags = toggleProvince(flags, `province-${i}`);
  assert.deepEqual(Object.values(flags.provinces), [...colors.map((color) => color.value), colors[0].value]);
  flags = toggleProvince(flags, "province-2");
  assert.equal(automaticProvinceColor(flags, "new"), colors[2].value);
});
