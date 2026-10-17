import assert from "node:assert/strict";
import { test } from "node:test";
import { execFileSync } from "node:child_process";
import regions from "@kongken/geo-data/regions.json" with { type: "json" };
import index from "@kongken/geo-data/search-index.json" with { type: "json" };
import {
  searchAreas,
  searchProvinces,
  searchNotes,
  noteExcerpt,
  oneEditAway,
  normalizeRegionQuery,
} from "../src/lib/search.ts";

const zhejiang = regions.find((p) => p.code === "330000");
test("all 531 region search keys are generated, current and retain source IDs", () => {
  assert.equal(Object.keys(index).length, 531);
  assert.ok(index["tw-09020"].includes("jinmen"));
  execFileSync(process.execPath, [
    "scripts/prepare-search-index.mjs",
    "--check",
  ]);
});
test("national search maps city names, pinyin and initials to the owning province", () => {
  for (const q of [
    "杭州",
    "háng zhōu",
    "HANGZHOU",
    "hz",
    "  ＨＡＮＧＺＨＯＵ  ",
  ]) {
    assert.ok(
      searchProvinces(regions, q).some((p) => p.code === "330000"),
      q,
    );
  }
  assert.deepEqual(
    searchProvinces(regions, "jinmen").map((p) => p.code),
    ["350000"],
  );
  assert.deepEqual(
    searchProvinces(regions, "chongqing").map((p) => p.code),
    ["500000"],
  );
  assert.ok(searchProvinces(regions, "cq").some((p) => p.code === "500000"));
  assert.deepEqual(
    searchProvinces(regions, "zhejiang").map((p) => p.code),
    ["330000"],
  );
  assert.equal(searchProvinces(regions, "不存在的目的地").length, 0);
});
test("bounded fuzzy correction supports one substitution, omission, insertion and transposition", () => {
  for (const q of ["hangzou", "hangzhpu", "hangzohu", "hangzzhou", "杭洲"]) {
    assert.equal(searchAreas(zhejiang.cities, q)[0].code, "330100", q);
  }
  assert.equal(searchAreas(zhejiang.cities, "hangzzhpu").length, 0);
  assert.equal(searchAreas(zhejiang.cities, "xx").length, 0);
  assert.deepEqual(
    searchAreas(zhejiang.cities, "宁波").map((c) => c.code),
    ["330200"],
  );
  assert.equal(
    searchAreas(regions.find((p) => p.code === "350000").cities, "台北").length,
    0,
  );
  assert.deepEqual(searchAreas(zhejiang.cities, " "), zhejiang.cities);
  assert.ok(oneEditAway("abcd", "abdc"));
  assert.ok(!oneEditAway("abcd", "axyd"));
  assert.equal(normalizeRegionQuery("Lǜ'an"), "lvan");
});
const saved = {
  version: 2,
  provinces: {},
  visits: [
    {
      code: "330100",
      name: "杭州市",
      province: "330000",
      color: "#4b937d",
      trips: [
        {
          id: "t-1",
          date: "2026-01-01",
          note: "西湖 徒步 100%_ <script>alert(1)</script> Café",
        },
        { id: "t-2", date: "2026-04-01", note: "西湖晚霞" },
      ],
    },
    {
      code: "tw-09020",
      name: "金门县",
      province: "350000",
      color: "#4b937d",
      trips: [{ id: "t-3", date: "", note: "海边徒步" }],
    },
  ],
};
test("private local notes search every trip, AND terms, literal punctuation and scope", () => {
  assert.deepEqual(
    searchNotes(saved, "西湖").map((m) => m.tripId),
    ["t-2", "t-1"],
  );
  assert.deepEqual(
    searchNotes(saved, "西湖 徒步").map((m) => m.tripId),
    ["t-1"],
  );
  assert.equal(searchNotes(saved, "CAFÉ").length, 1);
  assert.equal(searchNotes(saved, "100%_").length, 1);
  assert.equal(searchNotes(saved, " ").length, 0);
  assert.equal(searchNotes(saved, "x".repeat(101)).length, 0);
  assert.equal(searchNotes(saved, "a b c d e f g h i").length, 0);
  assert.deepEqual(
    searchNotes(saved, "徒步", "350000").map((m) => m.tripId),
    ["t-3"],
  );
  assert.equal(searchNotes(saved, "海边", "710000").length, 0);
  assert.equal(saved.visits[0].trips.length, 2);
});
test("note snippets expose plain text around matches late in the note", () => {
  const note = "起点".repeat(100) + "西湖 <img src=x>" + "终点".repeat(100);
  const excerpt = noteExcerpt(note, "西湖");
  assert.ok(excerpt.startsWith("…"));
  assert.ok(excerpt.includes("西湖 <img src=x>"));
  assert.ok(excerpt.endsWith("…"));
});
