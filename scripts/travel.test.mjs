import test from "node:test";
import assert from "node:assert/strict";
import { automaticColor, colors, toggleVisit } from "../src/lib/travel.ts";
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
  assert.equal(automaticColor(state, "320000"), colors[0].value);
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
