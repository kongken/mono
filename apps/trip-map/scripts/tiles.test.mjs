import test from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_TILE_URL,
  describeTileSource,
  mapboxFeedbackUrl,
  resolveTileConfig,
} from "../src/lib/tiles.ts";

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

const MAPBOX_PATH = "/api/tiles/{z}/{x}/{y}.png";

test("defaults to the OSM provider", () => {
  assert.equal(resolveTileConfig({}).provider, "osm");
});

test("mapbox provider accepts a same-origin relative tile path", () => {
  const cfg = resolveTileConfig({
    VITE_OSM_TILE_URL: MAPBOX_PATH,
    VITE_OSM_TILE_PROVIDER: "mapbox",
  });
  assert.equal(cfg.provider, "mapbox");
  assert.equal(cfg.url, MAPBOX_PATH);
  assert.equal(cfg.maxZoom, 18);
});

test("mapbox provider accepts an absolute BFF tile address and a zoom", () => {
  const cfg = resolveTileConfig({
    VITE_OSM_TILE_URL: "https://api.trip.example.com/api/tiles/{z}/{x}/{y}.png",
    VITE_OSM_TILE_MAX_ZOOM: "16",
    VITE_OSM_TILE_PROVIDER: " Mapbox ",
  });
  assert.equal(cfg.provider, "mapbox");
  assert.equal(cfg.url, "https://api.trip.example.com/api/tiles/{z}/{x}/{y}.png");
  assert.equal(cfg.maxZoom, 16);
});

test("mapbox attribution credits Mapbox, OpenStreetMap and the feedback link", () => {
  const cfg = resolveTileConfig({
    VITE_OSM_TILE_URL: MAPBOX_PATH,
    VITE_OSM_TILE_PROVIDER: "mapbox",
    VITE_OSM_TILE_ATTRIBUTION: "&copy; Maxar",
  });
  assert.match(cfg.attribution, /href="https:\/\/www\.mapbox\.com\/about\/maps\/"[^>]*>Mapbox<\/a>/);
  assert.match(cfg.attribution, /href="https:\/\/www\.openstreetmap\.org\/copyright"[^>]*>OpenStreetMap<\/a>/);
  assert.match(cfg.attribution, /data-mapbox-feedback[^>]*>Improve this map<\/a>/);
  assert.match(cfg.attribution, /Maxar/);
});

test("mapbox provider without a proxy URL falls back to OSM, not to Mapbox credits on OSM tiles", () => {
  for (const url of [undefined, "", "https://tiles.example.com/tile.png"]) {
    const cfg = resolveTileConfig({
      VITE_OSM_TILE_URL: url,
      VITE_OSM_TILE_PROVIDER: "mapbox",
    });
    assert.equal(cfg.provider, "osm");
    assert.equal(cfg.url, DEFAULT_TILE_URL);
    assert.doesNotMatch(cfg.attribution, /Mapbox/);
  }
});

test("a tile URL that carries a token is never used", () => {
  const cfg = resolveTileConfig({
    VITE_OSM_TILE_URL: "https://api.mapbox.com/styles/v1/u/s/tiles/256/{z}/{x}/{y}?access_token=pk.secret",
    VITE_OSM_TILE_PROVIDER: "mapbox",
  });
  assert.equal(cfg.url, DEFAULT_TILE_URL);
  assert.equal(cfg.provider, "osm");
  assert.doesNotMatch(JSON.stringify(cfg), /access_token|pk\./);
});

test("unknown providers behave like OSM", () => {
  const cfg = resolveTileConfig({
    VITE_OSM_TILE_URL: MAPBOX_PATH,
    VITE_OSM_TILE_PROVIDER: "other",
  });
  assert.equal(cfg.provider, "osm");
  assert.equal(cfg.url, MAPBOX_PATH);
  assert.doesNotMatch(cfg.attribution, /Mapbox/);
});

test("the feedback link carries the current view", () => {
  assert.equal(
    mapboxFeedbackUrl({ lng: 120.123456, lat: 30.25, zoom: 10.2512 }),
    "https://apps.mapbox.com/feedback/#/120.1235/30.25/10.25",
  );
});

test("the basemap hint names where tiles really come from", () => {
  const osm = describeTileSource(resolveTileConfig({}));
  assert.match(osm.hint, /openstreetmap\.org/);
  assert.equal(osm.name, "OpenStreetMap");
  assert.equal(osm.toggle, "OSM 底图");

  const proxied = describeTileSource(
    resolveTileConfig({ VITE_OSM_TILE_URL: MAPBOX_PATH, VITE_OSM_TILE_PROVIDER: "mapbox" }),
  );
  assert.equal(proxied.name, "Mapbox");
  assert.match(proxied.hint, /本站服务/);
  assert.doesNotMatch(proxied.hint, /openstreetmap\.org/);
  assert.notEqual(proxied.toggle, "OSM 底图");

  const cross = describeTileSource(
    resolveTileConfig({
      VITE_OSM_TILE_URL: "https://api.trip.example.com/api/tiles/{z}/{x}/{y}.png",
      VITE_OSM_TILE_PROVIDER: "mapbox",
    }),
  );
  assert.match(cross.hint, /api\.trip\.example\.com/);
  assert.doesNotMatch(cross.hint, /openstreetmap\.org|api\.mapbox\.com/);

  const custom = describeTileSource(
    resolveTileConfig({ VITE_OSM_TILE_URL: "https://tiles.example.com/{z}/{x}/{y}.png" }),
  );
  assert.match(custom.hint, /tiles\.example\.com/);
  assert.doesNotMatch(custom.hint, /openstreetmap\.org/);
});

test("mapbox provider appends the style version to the tile URL", () => {
  const cfg = resolveTileConfig({
    VITE_OSM_TILE_URL: MAPBOX_PATH,
    VITE_OSM_TILE_PROVIDER: "mapbox",
    VITE_OSM_TILE_VERSION: " 2026-10 ",
  });
  assert.equal(cfg.provider, "mapbox");
  assert.equal(cfg.url, "/api/tiles/{z}/{x}/{y}.png?v=2026-10");
  const absolute = resolveTileConfig({
    VITE_OSM_TILE_URL: "https://api.example.com/api/tiles/{z}/{x}/{y}.png",
    VITE_OSM_TILE_PROVIDER: "mapbox",
    VITE_OSM_TILE_VERSION: "v2_a",
  });
  assert.equal(absolute.url, "https://api.example.com/api/tiles/{z}/{x}/{y}.png?v=v2_a");
});

test("an unusable style version leaves the tile URL unversioned", () => {
  for (const version of ["", "  ", "a b", "a&b=1", "1.0", "x".repeat(33), "é"]) {
    const cfg = resolveTileConfig({
      VITE_OSM_TILE_URL: MAPBOX_PATH,
      VITE_OSM_TILE_PROVIDER: "mapbox",
      VITE_OSM_TILE_VERSION: version,
    });
    assert.equal(cfg.url, MAPBOX_PATH, JSON.stringify(version));
  }
  // A URL that already has a query is the deployer's own; the version is not added.
  const withQuery = resolveTileConfig({
    VITE_OSM_TILE_URL: "/api/tiles/{z}/{x}/{y}.png?x=1",
    VITE_OSM_TILE_PROVIDER: "mapbox",
    VITE_OSM_TILE_VERSION: "2",
  });
  assert.equal(withQuery.url, "/api/tiles/{z}/{x}/{y}.png?x=1");
});

test("the style version never changes non-Mapbox tile sources", () => {
  const custom = resolveTileConfig({
    VITE_OSM_TILE_URL: "https://tiles.example.com/{z}/{x}/{y}.png",
    VITE_OSM_TILE_VERSION: "2",
  });
  assert.equal(custom.url, "https://tiles.example.com/{z}/{x}/{y}.png");
  assert.equal(resolveTileConfig({ VITE_OSM_TILE_VERSION: "2" }).url, DEFAULT_TILE_URL);
  // A failed Mapbox declaration falls back to OSM without the version.
  const fallback = resolveTileConfig({
    VITE_OSM_TILE_PROVIDER: "mapbox",
    VITE_OSM_TILE_VERSION: "2",
  });
  assert.equal(fallback.url, DEFAULT_TILE_URL);
});

test("the Mapbox source description is unchanged by a version", () => {
  const cfg = resolveTileConfig({
    VITE_OSM_TILE_URL: MAPBOX_PATH,
    VITE_OSM_TILE_PROVIDER: "mapbox",
    VITE_OSM_TILE_VERSION: "2",
  });
  assert.match(describeTileSource(cfg).hint, /本站服务/);
});
