import { regionCatalog, RegionCompatibilityError } from "./region-catalog.ts";
import type { Op } from "./sync.ts";
import { legacyTripId } from "./trips.ts";

/** Only a read/acknowledgement projection. The queued request, mutation ID and
 * revision stay byte-for-byte compatible with the server's old receipt contract.
 */
export function projectRegionOps(ops: Op[], catalog = regionCatalog): Op[] {
  return ops.flatMap((op): Op[] => {
    if (op.type === "upsertVisit" || op.type === "legacyUpsertVisit") {
      const city = catalog.city(op.visit.code, op.visit.name, op.visit.province);
      if (!city) throw new RegionCompatibilityError("待同步请求包含无法映射的区域；原请求与缓存已保留，请先导出备份");
      const upsert: Op = { type: "upsertVisit", visit: { ...city, color: op.visit.color } };
      return op.type === "legacyUpsertVisit" ? [upsert, { type: "upsertTrip", code: city.code, trip: { id: legacyTripId(op.visit.code), date: op.visit.date, note: op.visit.note } }] : [upsert];
    }
    if (op.type === "deleteVisit" || op.type === "upsertTrip" || op.type === "deleteTrip") {
      const code = catalog.code(op.code);
      if (!code) throw new RegionCompatibilityError("待同步请求包含无法映射的区域；原请求与缓存已保留，请先导出备份");
      return [{ ...op, code }];
    }
    return [op];
  });
}
