import assert from "node:assert/strict";
import { test } from "node:test";
import { HOME, MAX_ZOOM, MIN_ZOOM, VIEW_H, VIEW_W, clampViewport, panBy, revealCentre, toBox, visibleBox, zoomAt } from "../src/lib/world-viewport.ts";

const near = (a, b, message) => assert.ok(Math.abs(a - b) < 1e-9, `${message}: ${a} vs ${b}`);

test("zoom stays between the limits and keeps the point under the pointer fixed", () => {
  let v = HOME;
  for (let i = 0; i < 40; i++) v = zoomAt(v, 1.6, 700, 200);
  assert.equal(v.k, MAX_ZOOM);
  for (let i = 0; i < 60; i++) v = zoomAt(v, 1 / 1.6, 300, 100);
  assert.deepEqual(v, HOME, "zooming all the way out returns home");
  const z = zoomAt(HOME, 4, 700, 200);
  // The map point that was at (700, 200) is still there.
  near((700 - z.x) / z.k, 700, "x");
  near((200 - z.y) / z.k, 200, "y");
});

test("the map always covers the box: it cannot be panned off and a bad value is repaired", () => {
  const z = zoomAt(HOME, 3, 500, 250);
  for (const [dx, dy] of [[1e6, 1e6], [-1e6, -1e6], [0, 0]]) {
    const p = panBy(z, dx, dy);
    assert.ok(p.x <= 0 && p.x >= VIEW_W - VIEW_W * p.k, `x ${p.x}`);
    assert.ok(p.y <= 0 && p.y >= VIEW_H - VIEW_H * p.k, `y ${p.y}`);
  }
  assert.deepEqual(panBy(HOME, 50, 50), HOME, "nothing to pan at home zoom");
  assert.deepEqual(clampViewport({ k: NaN, x: Infinity, y: -Infinity }), HOME);
  assert.equal(clampViewport({ k: 0.1, x: 0, y: 0 }).k, MIN_ZOOM);
});

test("revealCentre pans only as far as needed and keeps the zoom", () => {
  const z = zoomAt(HOME, 4, 0, 0);
  const [x0, y0, x1, y1] = visibleBox(z);
  assert.deepEqual([x0, y0, x1, y1], [0, 0, VIEW_W / 4, VIEW_H / 4]);
  const inside = [[100, 50], [140, 70]];
  assert.equal(revealCentre(z, inside), z, "already visible: unchanged");
  const far = revealCentre(z, [[800, 400], [840, 420]]);
  assert.equal(far.k, z.k);
  const [a, b, c, d] = visibleBox(far);
  assert.ok(820 >= a && 820 <= c && 410 >= b && 410 <= d, "the centre is on screen");
  near((a + c) / 2, 820, "centred horizontally");
  const half = revealCentre(z, [[100, 400], [140, 420]]);
  assert.equal(half.x, z.x, "only the axis that was off screen moves");
  assert.notEqual(half.y, z.y);
});

test("client pixels map onto the drawing box", () => {
  assert.deepEqual(toBox(0, 0, 500, 250), [0, 0]);
  assert.deepEqual(toBox(250, 125, 500, 250), [500, 250]);
  assert.deepEqual(toBox(10, 10, 0, 0), [10000, 5000], "an empty element does not divide by zero");
});
