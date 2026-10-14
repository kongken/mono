import test from "node:test";
import assert from "node:assert/strict";
import { draftChanged, mergeDraft } from "../src/lib/draft.ts";

const green = "#4b937d";
const gold = "#e2af5c";
const blue = "#719fc6";
const trip = (id, date = "", note = "") => ({ id, date, note });
const visit = (trips, extra = {}) => ({ code: "c", name: "城", province: "p", color: green, trips, ...extra });
const base = (trips, extra = {}) => ({ code: "c", existed: true, color: green, trips, ...extra });
const draft = (trips, extra = {}) => ({ code: "c", name: "城", province: "p", color: green, trips, ...extra });

test("an untouched draft changes nothing and keeps everything that arrived meanwhile", () => {
  const b = base([trip("a", "2026-01-01", "x")]);
  const live = visit([trip("a", "2026-01-01", "x"), trip("remote", "2026-02-02", "别处新增")]);
  assert.equal(draftChanged(b, draft(b.trips)), false);
  const r = mergeDraft(b, draft(b.trips), live);
  assert.equal(r.kind, "set");
  assert.deepEqual(r.visit.trips.map((t) => t.id), ["a", "remote"]);
});

test("the user's edit applies and unrelated remote additions and edits survive", () => {
  const b = base([trip("a", "2026-01-01", "x"), trip("b", "2026-01-02", "y")]);
  const mine = draft([trip("a", "2026-01-01", "我改的"), trip("b", "2026-01-02", "y"), trip("new", "2026-03-03", "新")]);
  const live = visit([trip("a", "2026-01-01", "x"), trip("b", "2026-01-02", "别处改了 b"), trip("remote", "", "r")]);
  const r = mergeDraft(b, mine, live);
  assert.equal(r.kind, "set");
  const byId = Object.fromEntries(r.visit.trips.map((t) => [t.id, t]));
  assert.equal(byId.a.note, "我改的");
  assert.equal(byId.b.note, "别处改了 b", "a trip the user did not touch keeps its newest content");
  assert.equal(byId.remote.note, "r");
  assert.equal(byId.new.note, "新");
});

test("deleting a trip the user deleted and the remote left alone; remote edits to other trips stay", () => {
  const b = base([trip("a", "", "x"), trip("b", "", "y")]);
  const r = mergeDraft(b, draft([trip("b", "", "y")]), visit([trip("a", "", "x"), trip("b", "", "y2")]));
  assert.equal(r.kind, "set");
  assert.deepEqual(r.visit.trips, [trip("b", "", "y2")]);
});

test("the same trip changed on both sides asks, and nothing is decided without a choice", () => {
  const b = base([trip("a", "", "x")]);
  const mine = draft([trip("a", "", "我的")]);
  const live = visit([trip("a", "", "别处的")]);
  const ask = mergeDraft(b, mine, live);
  assert.equal(ask.kind, "ask");
  assert.deepEqual(ask.conflicts.map((c) => [c.kind, c.mine?.note, c.latest?.note]), [["trip", "我的", "别处的"]]);
  assert.equal(mergeDraft(b, mine, live, { "trip:a": "mine" }).visit.trips[0].note, "我的");
  assert.equal(mergeDraft(b, mine, live, { "trip:a": "latest" }).visit.trips[0].note, "别处的");
});

test("an edit of a trip deleted remotely, and a delete of a trip edited remotely, are conflicts", () => {
  const b = base([trip("a", "", "x")]);
  const edit = mergeDraft(b, draft([trip("a", "", "我改")]), visit([]));
  assert.equal(edit.kind, "ask");
  assert.deepEqual([edit.conflicts[0].mine?.note, edit.conflicts[0].latest], ["我改", null]);
  assert.deepEqual(mergeDraft(b, draft([trip("a", "", "我改")]), visit([]), { "trip:a": "mine" }).visit.trips, [trip("a", "", "我改")]);
  assert.deepEqual(mergeDraft(b, draft([trip("a", "", "我改")]), visit([]), { "trip:a": "latest" }).visit.trips, []);

  const del = mergeDraft(b, draft([]), visit([trip("a", "", "别处改过")]));
  assert.equal(del.kind, "ask");
  assert.equal(del.conflicts[0].mine, null);
  assert.deepEqual(mergeDraft(b, draft([]), visit([trip("a", "", "别处改过")]), { "trip:a": "latest" }).visit.trips, [trip("a", "", "别处改过")]);
  assert.deepEqual(mergeDraft(b, draft([]), visit([trip("a", "", "别处改过")]), { "trip:a": "mine" }).visit.trips, []);
});

test("a trip already deleted remotely that the user also deleted is not a conflict", () => {
  const b = base([trip("a", "", "x")]);
  const r = mergeDraft(b, draft([]), visit([]));
  assert.equal(r.kind, "set");
  assert.deepEqual(r.visit.trips, []);
});

test("color: the user's change applies unless the color also changed remotely", () => {
  const b = base([]);
  assert.equal(mergeDraft(b, draft([], { color: gold }), visit([])).visit.color, gold);
  assert.equal(mergeDraft(b, draft([]), visit([], { color: blue })).visit.color, blue, "an untouched color keeps the newest");
  const clash = mergeDraft(b, draft([], { color: gold }), visit([], { color: blue }));
  assert.equal(clash.kind, "ask");
  assert.equal(mergeDraft(b, draft([], { color: gold }), visit([], { color: blue }), { color: "mine" }).visit.color, gold);
  assert.equal(mergeDraft(b, draft([], { color: gold }), visit([], { color: blue }), { color: "latest" }).visit.color, blue);
});

test("a city cancelled meanwhile asks before it comes back with the draft", () => {
  const b = base([trip("a", "", "x")]);
  const mine = draft([trip("a", "", "我改")]);
  assert.equal(mergeDraft(b, mine, undefined).kind, "ask");
  const back = mergeDraft(b, mine, undefined, { city: "mine" });
  assert.equal(back.kind, "set");
  assert.deepEqual(back.visit.trips, [trip("a", "", "我改")]);
  assert.equal(mergeDraft(b, mine, undefined, { city: "latest" }).kind, "noop");
  // Nothing edited: nothing to restore, so no question either.
  assert.equal(mergeDraft(b, draft(b.trips), undefined).kind, "noop");
});

test("a new city is created from the draft, and joins a city that appeared meanwhile", () => {
  const b = base([], { existed: false });
  const mine = draft([trip("n", "2026-01-01", "新")]);
  assert.deepEqual(mergeDraft(b, mine, undefined).visit.trips, [trip("n", "2026-01-01", "新")]);
  const live = visit([trip("other", "", "已有")]);
  const r = mergeDraft(b, mine, live);
  assert.equal(r.kind, "set");
  assert.deepEqual(r.visit.trips.map((t) => t.id).sort(), ["n", "other"]);
});
