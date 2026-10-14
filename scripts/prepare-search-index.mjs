import fs from "node:fs";
import { pinyin } from "pinyin-pro";

// Generate only at development time: no dictionary/library or remote service
// is needed by the deployed app. Word-level readings handle polyphonic names.
const regions = JSON.parse(
  fs.readFileSync(new URL("../src/regions.json", import.meta.url)),
);
const overrides = { 重庆: "chong qing", 长安: "chang an", 六安: "lu an" };
const suffix =
  /(?:特别行政区|维吾尔自治区|壮族自治区|回族自治区|自治区|自治州|自治县|省|市|区|县)$/u;
function keys(name) {
  const base = name.replace(suffix, "");
  return [
    ...new Set(
      [name, base].flatMap((text) => {
        let reading = pinyin(text, {
          toneType: "none",
          type: "array",
          v: true,
        });
        for (const [word, pronunciation] of Object.entries(overrides)) {
          if (text.startsWith(word))
            reading = [
              ...pronunciation.split(" "),
              ...reading.slice(word.length),
            ];
        }
        return [reading.join(""), reading.map((s) => s[0]).join("")];
      }),
    ),
  ];
}
const index = Object.fromEntries(
  regions.flatMap((p) => [
    [p.code, [...new Set([...keys(p.name), ...keys(p.short)])]],
    ...p.cities.map((c) => [c.code, keys(c.name)]),
  ]),
);
const output = JSON.stringify(index, null, 2) + "\n";
const target = new URL("../src/search-index.json", import.meta.url);
if (process.argv.includes("--check")) {
  if (fs.readFileSync(target, "utf8") !== output)
    throw new Error("Search index is stale: npm run prepare:search");
} else fs.writeFileSync(target, output);
