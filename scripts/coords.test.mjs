import test from "node:test";
import assert from "node:assert/strict";
import {
  gcj02ToWgs84,
  geometryGcj02ToWgs84,
  wgs84ToGcj02,
} from "../src/lib/coords.ts";

test("WGS-84 to GCJ-02 offsets mainland points by a few hundred meters", () => {
  const [lng, lat] = [120.1551, 30.2741];
  const [gLng, gLat] = wgs84ToGcj02(lng, lat);
  const meters = Math.hypot(
    (gLng - lng) * 111320 * Math.cos((lat * Math.PI) / 180),
    (gLat - lat) * 110540,
  );
  assert.ok(meters > 100 && meters < 800, `offset was ${meters}`);
});

test("GCJ-02 to WGS-84 reverses the offset within one meter", () => {
  const [lng, lat] = [113.2644, 23.1291];
  const [wLng, wLat] = gcj02ToWgs84(lng, lat);
  const [bLng, bLat] = wgs84ToGcj02(wLng, wLat);
  assert.ok(Math.abs(bLng - lng) < 1e-5);
  assert.ok(Math.abs(bLat - lat) < 1e-5);
});

test("points outside China are unchanged", () => {
  assert.deepEqual(gcj02ToWgs84(2.35, 48.85), [2.35, 48.85]);
});

test("geometry conversion keeps polygon structure", () => {
  const polygon = {
    type: "Polygon",
    coordinates: [
      [
        [120, 30],
        [121, 30],
        [121, 31],
        [120, 30],
      ],
    ],
  };
  const converted = geometryGcj02ToWgs84(polygon);
  assert.equal(converted.type, "Polygon");
  assert.equal(converted.coordinates[0].length, 4);
  assert.notDeepEqual(converted.coordinates, polygon.coordinates);
  const multi = geometryGcj02ToWgs84({
    type: "MultiPolygon",
    coordinates: [polygon.coordinates],
  });
  assert.equal(multi.coordinates[0][0].length, 4);
});
