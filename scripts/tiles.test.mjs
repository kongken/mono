import test from "node:test";
import assert from "node:assert/strict";
import { DEFAULT_TILE_URL, resolveTileConfig } from "../src/lib/tiles.ts";

test("defaults to OpenStreetMap tiles with attribution", () => {
  const cfg = resolveTileConfig({});
  assert.equal(cfg.url, DEFAULT_TILE_URL);
  assert.equal(cfg.maxZoom, 19);
  assert.match(cfg.attribution, /OpenStreetMap/);
});

test("accepts a custom tile URL, max zoom and extra attribution", () => {
  const cfg = resolveTileConfig({
    VITE_OSM_TILE_URL: " https://tiles.example.com/tile/{z}/{x}/{y}.png ",
    VITE_OSM_TILE_MAX_ZOOM: "17",
    VITE_OSM_TILE_ATTRIBUTION: "Tiles by Example",
  });
  assert.equal(cfg.url, "https://tiles.example.com/tile/{z}/{x}/{y}.png");
  assert.equal(cfg.maxZoom, 17);
  assert.match(cfg.attribution, /OpenStreetMap.*Tiles by Example/);
});

test("custom URL without an explicit max zoom uses a conservative 18", () => {
  const cfg = resolveTileConfig({
    VITE_OSM_TILE_URL: "https://tiles.example.com/{z}/{x}/{y}.png",
  });
  assert.equal(cfg.maxZoom, 18);
});

test("invalid values fall back to defaults", () => {
  const cfg = resolveTileConfig({
    VITE_OSM_TILE_URL: "https://tiles.example.com/tile.png",
    VITE_OSM_TILE_MAX_ZOOM: "99",
  });
  assert.equal(cfg.url, DEFAULT_TILE_URL);
  assert.equal(cfg.maxZoom, 19);
});
