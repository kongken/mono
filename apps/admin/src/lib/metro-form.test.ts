import assert from "node:assert/strict";
import { test } from "node:test";
import { diffForms, display, isForm, problemFor, type FieldDef } from "./metro-form.ts";

const fields: FieldDef[] = [
  { key: "name", label: "名称", kind: "text" },
  { key: "status", label: "状态", kind: "select", options: [{ value: "1", label: "运营中" }, { value: "2", label: "在建" }] },
  { key: "isLoop", label: "环线", kind: "checkbox" },
  { key: "note", label: "备注", kind: "textarea" },
];

test("a conflict shows only the fields whose latest value differs from what was typed", () => {
  const latest = { name: "上海（已改）", status: "1", isLoop: false, note: "" };
  const mine = { name: "上海市", status: "2", isLoop: false, note: "" };
  assert.deepEqual(diffForms(fields, latest, mine), [
    { key: "name", label: "名称", latest: "上海（已改）", mine: "上海市" },
    { key: "status", label: "状态", latest: "运营中", mine: "在建" },
  ]);
  assert.deepEqual(diffForms(fields, mine, mine), []);
});

test("values are shown as a person reads them", () => {
  assert.equal(display(fields[2]!, true), "是");
  assert.equal(display(fields[2]!, undefined), "否");
  assert.equal(display(fields[3]!, ""), "（空）");
  assert.equal(display(fields[1]!, "9"), "9");
});

test("only a record of strings and booleans is a form", () => {
  assert.equal(isForm({ a: "x", b: true }), true);
  for (const bad of [null, "x", [], { a: 1 }, { a: { b: 1 } }, { a: null }]) assert.equal(isForm(bad), false, JSON.stringify(bad));
});

test("a server field path finds the form field it names, in snake_case or as a path", () => {
  const problems = [{ field: "city_slug", reason: "城市不存在" }, { field: "line.color", reason: "颜色" }, { field: "lines[3].rolling_stock_code", reason: "车型" }];
  assert.equal(problemFor(problems, "citySlug"), "城市不存在");
  assert.equal(problemFor(problems, "color"), "颜色");
  assert.equal(problemFor(problems, "rollingStockCode"), "车型");
  assert.equal(problemFor(problems, "name"), undefined);
  assert.equal(problemFor([{ field: "slug", reason: "x" }], "slug"), "x");
  // The server's own name for a field that differs from the form's key.
  assert.equal(problemFor([{ field: "lines[0].city", reason: "没有这个城市" }], "citySlug", "city"), "没有这个城市");
  assert.equal(problemFor([{ field: "lines[0].city", reason: "没有这个城市" }], "citySlug"), undefined);
  assert.equal(problemFor([{ field: "cars", reason: "x" }], "carsAddition"), undefined);
});
