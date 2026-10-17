import { createClient } from "@connectrpc/connect";
import { RegionService } from "@trip-map/api-client/bff/v1/region_pb";
import { REGION_VERSION, RegionCompatibilityError } from "@/lib/region-catalog";
import { transport } from "./transport";

const raw = transport ? createClient(RegionService, transport) : null;
const client = () => { if (!raw) throw new Error("backend not configured"); return raw; };
const assertVersion = (version: string) => {
  if (version !== REGION_VERSION) throw new RegionCompatibilityError("前后端区域索引版本不一致；旧记录及缓存已保留，请刷新页面或升级服务器后再迁移。");
};
export const regionApi = {
  preview: async () => {
    const index = await client().getRegionIndex({ knownVersion: REGION_VERSION });
    assertVersion(index.version);
    const p = await client().previewRegionUpgrade({});
    assertVersion(p.version);
    return { version: p.version, revision: Number(p.revision), changes: p.changes, blockers: p.blockers };
  },
  apply: async (version: string, revision: number, mutationId: string) => {
    assertVersion(version);
    const r = await client().applyRegionUpgrade({ version, baseRevision: BigInt(revision), mutationId });
    assertVersion(r.version);
    return { revision: Number(r.revision), changed: r.changedCount, snapshotId: r.snapshotId, replayed: r.replayed };
  },
};
