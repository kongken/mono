import { LineStatus } from "@kongken/api-client/bff/metro/v1/catalog_pb";

export { statusLabel, statusLabels } from "@kongken/metro-core/status";

/** Lines a rider may newly mark: operating or suspended ones. */
export const markable = (s: LineStatus) => s === LineStatus.OPERATING || s === LineStatus.SUSPENDED;
