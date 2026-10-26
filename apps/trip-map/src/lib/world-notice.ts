import type { WorldSource } from "./world-catalog.ts";

// Text for the world view's always-visible "地图来源与限制" entry
// (docs/trip-map/world-spec.md section 10). The UI only renders these constants.
export const WORLD_NOTICE_TITLE = "地图来源与限制";

export const WORLD_NOTICE_DISCLAIMER =
  "世界地图按数据源绘制，可能与任何国家或地区的官方地图不同。地图仅用于个人旅行记录，不代表对任何领土主权的立场。";

export const WORLD_NOTICE_CHINA =
  "中国范围使用本应用的中国地图（含香港、澳门、台湾），与中国视图一致，其他国家的边界不会改动它。";

/** Merges, exceptions and the known gaps of this map, as listed by the data task. */
export const WORLD_NOTICE_LIMITS = [
  "香港、澳门、台湾在世界视图里归入中国，不单独列出。",
  "中国地图与邻国边界来自不同来源：相接处可能有缝隙或重叠，重叠处以中国地图为准，缝隙不作填补。",
  "北塞浦路斯并入塞浦路斯，索马里兰并入索马里，澳大利亚的印度洋领地、阿什莫尔和卡捷群岛并入澳大利亚。",
  "锡亚琴冰川因归属存在争议，未归入任何国家，地图上不绘制。",
  "法属圭亚那、瓜德罗普、马提尼克、留尼汪、马约特归在法国的边界内，斯瓦尔巴归在挪威的边界内，荷属加勒比区归在荷兰的边界内，圣诞岛、科科斯（基林）群岛归在澳大利亚的边界内；它们没有单独的边界，只能通过列表选择。",
  "过小的国家和地区在地图上难以点中，请通过列表搜索选择。",
] as const;

/** One line naming the data, its version, its licence and what was changed. */
export function worldNoticeSource(source: WorldSource, precisionDecimals: number): string {
  return `边界数据：${source.name} ${source.version}（${source.dataset}），授权：${source.license}。已按上述规则归并，坐标精度保留 ${precisionDecimals} 位小数。`;
}
