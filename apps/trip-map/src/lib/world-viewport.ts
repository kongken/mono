// Zoom and pan of the world map, in the map's own drawing units (a W x H box). The view is
// `translate(x, y) scale(k)`; the map always covers the whole box, so it can never be dragged
// out of sight. Pure functions, unit-tested; the component only turns events into calls.

export type Viewport = { k: number; x: number; y: number };
export const VIEW_W = 1000;
export const VIEW_H = 500;
export const MIN_ZOOM = 1;
export const MAX_ZOOM = 40;
export const HOME: Viewport = { k: 1, x: 0, y: 0 };

const between = (value: number, low: number, high: number) => Math.min(high, Math.max(low, value));

export function clampViewport(v: Viewport, w = VIEW_W, h = VIEW_H): Viewport {
  const k = between(Number.isFinite(v.k) ? v.k : 1, MIN_ZOOM, MAX_ZOOM);
  return {
    k,
    x: between(Number.isFinite(v.x) ? v.x : 0, w - w * k, 0),
    y: between(Number.isFinite(v.y) ? v.y : 0, h - h * k, 0),
  };
}

/** Zooms by `factor` keeping the drawing point under (cx, cy), a point of the box, fixed. */
export function zoomAt(v: Viewport, factor: number, cx: number, cy: number, w = VIEW_W, h = VIEW_H): Viewport {
  const k = between(v.k * factor, MIN_ZOOM, MAX_ZOOM);
  const ratio = k / v.k;
  return clampViewport({ k, x: cx - (cx - v.x) * ratio, y: cy - (cy - v.y) * ratio }, w, h);
}

/** Moves the picture by (dx, dy) box units. */
export const panBy = (v: Viewport, dx: number, dy: number, w = VIEW_W, h = VIEW_H): Viewport =>
  clampViewport({ ...v, x: v.x + dx, y: v.y + dy }, w, h);

/** The part of the map that is on screen, as [x0, y0, x1, y1] in map units (before the view). */
export const visibleBox = (v: Viewport, w = VIEW_W, h = VIEW_H): [number, number, number, number] => [
  (0 - v.x) / v.k,
  (0 - v.y) / v.k,
  (w - v.x) / v.k,
  (h - v.y) / v.k,
];

/**
 * Pans, without changing the zoom, just enough to bring the centre of `bounds` ([[x0, y0],
 * [x1, y1]] in map units) into the middle half of the screen; unchanged if it is already there.
 */
export function revealCentre(v: Viewport, bounds: [[number, number], [number, number]], w = VIEW_W, h = VIEW_H): Viewport {
  const cx = (bounds[0][0] + bounds[1][0]) / 2;
  const cy = (bounds[0][1] + bounds[1][1]) / 2;
  const [x0, y0, x1, y1] = visibleBox(v, w, h);
  const inX = cx >= x0 + (x1 - x0) / 4 && cx <= x1 - (x1 - x0) / 4;
  const inY = cy >= y0 + (y1 - y0) / 4 && cy <= y1 - (y1 - y0) / 4;
  if (inX && inY) return v;
  return clampViewport({ k: v.k, x: inX ? v.x : w / 2 - cx * v.k, y: inY ? v.y : h / 2 - cy * v.k }, w, h);
}

/** A client-space point (relative to the element's box of `width` x `height` pixels) as a point of the drawing box. */
export const toBox = (px: number, py: number, width: number, height: number, w = VIEW_W, h = VIEW_H): [number, number] => [
  (px / Math.max(1, width)) * w,
  (py / Math.max(1, height)) * h,
];
