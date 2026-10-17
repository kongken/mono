import { readFile, writeFile } from "node:fs/promises";
import { prepareProvinceNeighbors } from "./prepare-province-neighbors.mjs";
import { prepareTaiwan } from "./prepare-taiwan.mjs";
import { splitFujianRegions } from "./split-fujian-regions.mjs";
const china = JSON.parse(await readFile("public/maps/100000.json", "utf8"));
const provinces = china.features.filter((f) => f.properties.name);
let cursor = 0;
await Promise.all(
  Array.from({ length: 5 }, async () => {
    while (cursor < provinces.length) {
      const province = provinces[cursor++];
      const code = province.properties.adcode;
      if (code === 710000) {
        const response = await fetch(
          "https://raw.githubusercontent.com/ronnywang/twgeojson/master/twcounty2010.2.json",
        );
        if (!response.ok) throw new Error("Taiwan map download failed");
        await writeFile(
          "public/maps/710000.json",
          JSON.stringify(prepareTaiwan(await response.json())),
        );
        continue;
      }
      const suffix = province.properties.childrenNum ? "_full" : "";
      const response = await fetch(
        `https://geo.datav.aliyun.com/areas_v3/bound/${code}${suffix}.json`,
      );
      if (!response.ok) throw new Error(`${code}: ${response.status}`);
      const data = await response.json();
      await writeFile(`public/maps/${code}.json`, JSON.stringify(data));
      console.log(
        `${province.properties.name}: ${data.features.length} regions`,
      );
    }
  }),
);
// Both downloads must finish before splitting the supplemental island dataset.
const split = splitFujianRegions(
  JSON.parse(await readFile("public/maps/710000.json", "utf8")),
  JSON.parse(await readFile("public/maps/350000.json", "utf8")),
);
await writeFile("public/maps/710000.json", JSON.stringify(split.taiwan));
await writeFile("public/maps/350000.json", JSON.stringify(split.fujian));
const index = await Promise.all(
  provinces.map(async (f) => {
    const p = f.properties;
    const map = JSON.parse(
      await readFile(`public/maps/${p.adcode}.json`, "utf8"),
    );
    return {
      code: String(p.adcode),
      name: p.name,
      short: p.name.replace(
        /省|市|壮族自治区|回族自治区|维吾尔自治区|自治区|特别行政区/g,
        "",
      ),
      cities: map.features
        .filter((c) => c.properties.name)
        .map((c) => ({
          code: String(c.properties.adcode),
          name: c.properties.name,
        })),
    };
  }),
);
await writeFile("../../packages/geo-data/data/regions.json", JSON.stringify(index));
// The Go backend validates region IDs against its own embedded copy.
await writeFile("../../internal/tripmap/regions/regions.json", JSON.stringify(index));

await prepareProvinceNeighbors();
