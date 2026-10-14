import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  DEFAULT_ESTIMATION_POLICY,
  buildItineraryInfo,
  loadStoredItineraries,
  moveStop,
  parseBackupItineraries,
  saveStoredItineraries,
  validateItineraryInput,
} from "../src/lib/itinerary.ts";
import { createRegionCatalog } from "../src/lib/region-catalog.ts";

const fixture = JSON.parse(readFileSync("scripts/fixtures/itinerary-routes.json", "utf8"));
const regions = JSON.parse(readFileSync("src/regions.json", "utf8"));

function createMemoryStorage() {
  const map = new Map();
  return {
    getItem(k) {
      return map.has(k) ? map.get(k) : null;
    },
    setItem(k, v) {
      map.set(k, String(v));
    },
    removeItem(k) {
      map.delete(k);
    },
  };
}

test("itinerary route estimation and policy match shared backend fixtures", () => {
  assert.equal(DEFAULT_ESTIMATION_POLICY.provider, fixture.policy.provider);
  assert.equal(DEFAULT_ESTIMATION_POLICY.externalApiEnabled, fixture.policy.externalApiEnabled);
  assert.equal(DEFAULT_ESTIMATION_POLICY.costPolicy, fixture.policy.costPolicy);
  assert.equal(DEFAULT_ESTIMATION_POLICY.cacheStrategy, fixture.policy.cacheStrategy);

  for (const tc of fixture.cases) {
    const info = buildItineraryInfo({
      id: tc.id,
      title: tc.title,
      note: tc.note,
      color: tc.color,
      version: tc.version,
      stops: tc.stops,
    });
    assert.equal(info.totalStraightDistanceKm, tc.expect.totalStraightDistanceKm, tc.name);
    assert.equal(info.totalEstimatedDistanceKm, tc.expect.totalEstimatedDistanceKm, tc.name);
    assert.equal(info.totalEstimatedDurationMinutes, tc.expect.totalEstimatedDurationMinutes, tc.name);
    assert.equal(info.cityCount, tc.expect.cityCount, tc.name);
    assert.equal(info.provinceCount, tc.expect.provinceCount, tc.name);
    assert.equal(info.legs.length, tc.expect.legs.length, tc.name);

    for (let i = 0; i < tc.expect.legs.length; i++) {
      const got = info.legs[i];
      const exp = tc.expect.legs[i];
      assert.equal(got.fromOrder, exp.fromOrder, `${tc.name} leg[${i}]`);
      assert.equal(got.toOrder, exp.toOrder, `${tc.name} leg[${i}]`);
      assert.equal(got.fromRegionCode, exp.fromRegionCode, `${tc.name} leg[${i}]`);
      assert.equal(got.fromRegionName, exp.fromRegionName, `${tc.name} leg[${i}]`);
      assert.equal(got.toRegionCode, exp.toRegionCode, `${tc.name} leg[${i}]`);
      assert.equal(got.toRegionName, exp.toRegionName, `${tc.name} leg[${i}]`);
      assert.equal(got.transport, exp.transport, `${tc.name} leg[${i}]`);
      assert.equal(got.straightDistanceKm, exp.straightDistanceKm, `${tc.name} leg[${i}]`);
      assert.equal(got.estimatedDistanceKm, exp.estimatedDistanceKm, `${tc.name} leg[${i}]`);
      assert.equal(got.estimatedDurationMinutes, exp.estimatedDurationMinutes, `${tc.name} leg[${i}]`);
    }
  }
});

test("stop reordering updates leg sequence and recalculates totals", () => {
  const stops = [
    { id: "s1", regionCode: "310101", date: "2026-04-01", note: "上海", transport: "train" },
    { id: "s2", regionCode: "330100", date: "2026-04-02", note: "杭州", transport: "train" },
    { id: "s3", regionCode: "350200", date: "2026-04-04", note: "厦门", transport: "car" },
  ];
  const moved = moveStop(stops, 2, -1);
  assert.deepEqual(
    moved.map((s) => s.id),
    ["s1", "s3", "s2"],
  );
  const info = buildItineraryInfo({
    id: "it-reorder",
    title: "调整顺序后路线",
    note: "",
    color: "#4b937d",
    version: 1,
    stops: moved,
  });
  assert.equal(info.stops[1].regionCode, "350200");
  assert.equal(info.stops[2].regionCode, "330100");
  assert.equal(info.legs[0].fromRegionCode, "310101");
  assert.equal(info.legs[0].toRegionCode, "350200");
  assert.equal(info.legs[1].fromRegionCode, "350200");
  assert.equal(info.legs[1].toRegionCode, "330100");
});

test("historical region codes in itinerary stops upgrade without losing route stops", () => {
  const catalog = createRegionCatalog(regions, {
    version: "2026.02-test",
    supportedVersions: ["2026.01"],
    mappings: [
      {
        fromCode: "330199",
        fromName: "旧钱塘新区",
        fromProvince: "330000",
        toCode: "330100",
      },
    ],
  });
  const record = validateItineraryInput(
    {
      id: "it-upgrade",
      title: "跨版本行程",
      color: "#4b937d",
      stops: [
        { id: "s1", regionCode: "310101", date: "2026-04-01", note: "起点", transport: "train" },
        { id: "s2", regionCode: "330199", date: "2026-04-02", note: "历史编码站点", transport: "train" },
      ],
    },
    catalog,
  );
  assert.equal(record.stops[1].regionCode, "330100");
  const info = buildItineraryInfo(record, catalog);
  assert.equal(info.stops[1].regionName, "杭州市");
});

test("itinerary persistence recovers itineraries and active route after reopen", () => {
  const storage = createMemoryStorage();
  const record = validateItineraryInput({
    id: "it-persist",
    title: "江南春行",
    note: "重开恢复验证",
    color: "#4b937d",
    stops: [
      { id: "s1", regionCode: "310101", date: "2026-03-10", note: "上海", transport: "train" },
      { id: "s2", regionCode: "330100", date: "2026-03-12", note: "杭州", transport: "car" },
    ],
  });
  saveStoredItineraries(storage, { activeId: "it-persist", itineraries: [record] }, "shanhai-itineraries-v1");

  const restored = loadStoredItineraries(storage, "shanhai-itineraries-v1");
  assert.equal(restored.activeId, "it-persist");
  assert.equal(restored.itineraries.length, 1);
  const info = buildItineraryInfo(restored.itineraries[0]);
  assert.equal(info.title, "江南春行");
  assert.equal(info.legs.length, 1);
  assert.equal(info.legs[0].straightDistanceKm, 241.6);

  const backupParsed = parseBackupItineraries(JSON.parse(storage.getItem("shanhai-itineraries-v1")).itineraries);
  assert.equal(backupParsed.length, 1);
  assert.equal(backupParsed[0].id, "it-persist");
});
