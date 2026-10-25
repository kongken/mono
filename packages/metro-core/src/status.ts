import { LineStatus } from "@kongken/api-client/bff/metro/v1/catalog_pb";

export const statusLabels: Record<number, string> = {
  [LineStatus.PLANNED]: "规划中",
  [LineStatus.UNDER_CONSTRUCTION]: "在建",
  [LineStatus.OPERATING]: "运营中",
  [LineStatus.SUSPENDED]: "暂停运营",
  [LineStatus.RETIRED]: "已停运",
};

export const statusLabel = (s: LineStatus) => statusLabels[s] ?? "未知";
