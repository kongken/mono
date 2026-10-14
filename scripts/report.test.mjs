import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { buildReport } from "../src/lib/report.ts";

const fixture = JSON.parse(readFileSync("scripts/fixtures/report-stats.json", "utf8"));
const regions = JSON.parse(readFileSync("src/regions.json", "utf8"));
const cityNameByCode = new Map(regions.flatMap(p => p.cities.map(c => [c.code, c.name])));

test("report computation matches shared backend fixtures", () => {
  for (const tc of fixture.cases) {
    const saved = {
      version: 2,
      provinces: Object.fromEntries(tc.marks.map(code => [code, "#4b937d"])),
      visits: tc.cities.map(city => ({
        code: city.code,
        name: cityNameByCode.get(city.code) ?? city.code,
        province: city.province,
        trips: city.trips.map(t => ({ id: t.id, date: t.date ?? "", note: t.note ?? "" })),
      })),
    };
    const report = buildReport(saved, {
      timezone: tc.timezone,
      now: new Date(tc.now),
    });
    assert.equal(report.timezone, tc.expect.timezone, tc.name);
    assert.equal(report.generatedOn, tc.expect.generatedOn, tc.name);
    assert.equal(report.currentYear, tc.expect.currentYear, tc.name);
    assert.equal(report.selectedYear, tc.expect.selectedYear, tc.name);
    assert.deepEqual(report.availableYears, tc.expect.availableYears, tc.name);
    assert.equal(report.overview.exploredProvinces, tc.expect.overview.exploredProvinces, tc.name);
    assert.equal(report.overview.totalProvinces, tc.expect.overview.totalProvinces, tc.name);
    assert.equal(report.overview.visitedCities, tc.expect.overview.visitedCities, tc.name);
    assert.equal(report.overview.totalCities, tc.expect.overview.totalCities, tc.name);
    assert.equal(report.overview.visitedRegions, tc.expect.overview.visitedRegions, tc.name);
    assert.equal(report.overview.totalRegions, tc.expect.overview.totalRegions, tc.name);
    assert.equal(report.overview.totalTrips, tc.expect.overview.totalTrips, tc.name);
    assert.equal(report.overview.datedTrips, tc.expect.overview.datedTrips, tc.name);
    assert.equal(report.overview.undatedTrips, tc.expect.overview.undatedTrips, tc.name);

    if (tc.expect.topProvinceCode) {
      assert.equal(report.overview.provinceProgress[0]?.provinceCode, tc.expect.topProvinceCode, tc.name);
    }

    if (tc.expect.year2026) {
      const y2026 = report.annualReports.find(r => r.year === 2026);
      assert.ok(y2026, `${tc.name}: missing 2026 report`);
      assert.equal(y2026.tripCount, tc.expect.year2026.tripCount, tc.name);
      assert.equal(y2026.cityCount, tc.expect.year2026.cityCount, tc.name);
      assert.equal(y2026.regionCount, tc.expect.year2026.regionCount, tc.name);
      assert.equal(y2026.newCityCount, tc.expect.year2026.newCityCount, tc.name);
      assert.equal(y2026.newRegionCount, tc.expect.year2026.newRegionCount, tc.name);
      assert.equal(y2026.provinceCount, tc.expect.year2026.provinceCount, tc.name);
      assert.equal(y2026.newProvinceCount, tc.expect.year2026.newProvinceCount, tc.name);
      assert.equal(y2026.spanDays, tc.expect.year2026.spanDays, tc.name);
      assert.equal(y2026.busiestMonth, tc.expect.year2026.busiestMonth, tc.name);
      assert.equal(y2026.busiestMonthTrips, tc.expect.year2026.busiestMonthTrips, tc.name);
      assert.equal(y2026.earliestTrip?.tripId, tc.expect.year2026.earliestTripId, tc.name);
      assert.equal(y2026.latestTrip?.tripId, tc.expect.year2026.latestTripId, tc.name);
      assert.equal(y2026.longestNoteTrip?.tripId, tc.expect.year2026.longestNoteTripId, tc.name);
      assert.equal(y2026.mostVisitedCity?.regionCode, tc.expect.year2026.mostVisitedCityCode, tc.name);
    }

    const unlocked = report.achievements.filter(a => a.unlocked);
    assert.equal(unlocked.length, tc.expect.unlockedAchievements.length, tc.name);
    for (const exp of tc.expect.unlockedAchievements) {
      const got = unlocked.find(a => a.id === exp.id);
      assert.ok(got, `${tc.name}: expected unlocked achievement ${exp.id}`);
      assert.equal(got.current, exp.current, `${tc.name}/${exp.id}`);
      assert.equal(got.target, exp.target, `${tc.name}/${exp.id}`);
      assert.equal(got.unlockedAt, exp.unlockedAt, `${tc.name}/${exp.id}`);
      assert.ok(got.evidence.length > 0, `${tc.name}/${exp.id} must have traceable evidence`);
      if (exp.firstEvidenceCode) {
        assert.equal(got.evidence[0].provinceCode, exp.firstEvidenceCode, `${tc.name}/${exp.id}`);
      }
      if (exp.firstEvidenceTripId) {
        assert.equal(got.evidence[0].tripId, exp.firstEvidenceTripId, `${tc.name}/${exp.id}`);
      }
    }
  }
});

test("marking all 34 provinces and 391 cities unlocks every milestone achievement", () => {
  const visits = regions.flatMap((p, pi) =>
    p.cities.map((c, ci) => ({
      code: c.code,
      name: c.name,
      province: p.code,
      trips: [
        {
          id: `t-${c.code}-1`,
          date: `2026-${String((pi % 12) + 1).padStart(2, "0")}-${String((ci % 28) + 1).padStart(2, "0")}`,
          note: "走遍山海的完整旅行手账记录",
        },
        ...(c.code === "330100"
          ? [
              { id: "t-330100-2", date: "2026-06-01", note: "第二次来到杭州西湖" },
              { id: "t-330100-3", date: "2026-09-01", note: "第三次来到杭州西湖" },
            ]
          : []),
      ],
    })),
  );
  const report = buildReport(
    { version: 2, provinces: {}, visits },
    { timezone: "Asia/Shanghai", now: new Date("2026-10-14T08:00:00Z") },
  );
  assert.equal(report.overview.exploredProvinces, 34);
  assert.equal(report.overview.visitedCities, 391);
  assert.equal(report.overview.visitedRegions, 497);
  for (const ach of report.achievements) {
    assert.equal(ach.unlocked, true, `expected ${ach.id} to be unlocked`);
    assert.ok(ach.evidence.length > 0, `expected ${ach.id} to have evidence`);
    assert.ok(ach.unlockedAt.startsWith("2026-"), `expected ${ach.id} unlockedAt to be in 2026, got ${ach.unlockedAt}`);
  }
});

