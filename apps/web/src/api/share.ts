import type { Interceptor } from "@connectrpc/connect";
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { timestampMs } from "@bufbuild/protobuf/wkt";
import {
  ShareService,
  type ShareInfo,
} from "@trip-map/api-client/bff/v1/share_pb";
import { parseShareContent, type ShareContent } from "@/lib/share";
import { apiConfig } from "./transport";
import { csrfHeader } from "./csrf";
import { REGION_HEADER, REGION_VERSION } from "@/lib/region-catalog";

const withCredentials = (input: RequestInfo | URL, init?: RequestInit) =>
  fetch(input, { ...init, credentials: "include" });

const schemaHeaders: Interceptor = (next) => (req) => {
  req.header.set("X-Tripmap-Schema", "2");
  req.header.set(REGION_HEADER, REGION_VERSION);
  const csrf = csrfHeader();
  if (csrf) req.header.set("X-CSRF-Token", csrf);
  return next(req);
};

const shareTransport = !apiConfig.enabled
  ? null
  : createConnectTransport({
      baseUrl: apiConfig.baseUrl,
      interceptors: [schemaHeaders],
      fetch: withCredentials,
    });

const raw = shareTransport ? createClient(ShareService, shareTransport) : null;
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

export type Visibility = "private" | "link" | "public";
export type ShareSettings = {
  visibility: Visibility;
  cities: boolean;
  dates: boolean;
  notes: boolean;
  photos: boolean;
  photoIds: string[];
};
export type ShareState = {
  settings: ShareSettings;
  version: number;
  url: string;
  published: boolean;
  publishedAt: number | null;
  publishedRevision: number;
  currentRevision: number;
};

export const privateDraft: ShareSettings = {
  visibility: "private",
  cities: false,
  dates: false,
  notes: false,
  photos: false,
  photoIds: [],
};

const visibilities: Visibility[] = ["private", "link", "public"];

function state(info?: ShareInfo): ShareState {
  const s = info?.settings;
  return {
    settings: {
      visibility: visibilities.find((v) => v === s?.visibility) ?? "private",
      cities: s?.showCities ?? false,
      dates: s?.showDates ?? false,
      notes: s?.showNotes ?? false,
      photos: s?.showPhotos ?? false,
      photoIds: [...(s?.photoIds ?? [])],
    },
    version: Number(info?.version ?? 0),
    url: info?.url ?? "",
    published: info?.published ?? false,
    publishedAt: info?.publishedAt ? timestampMs(info.publishedAt) : null,
    publishedRevision: Number(info?.publishedRevision ?? 0),
    currentRevision: Number(info?.currentRevision ?? 0),
  };
}

const wire = (s: ShareSettings) => ({
  visibility: s.visibility,
  showCities: s.cities,
  showDates: s.dates,
  showNotes: s.notes,
  showPhotos: s.photos,
  photoIds: s.photoIds,
});

export type SharePreview = { content: ShareContent; revision: number };

export const shareApi = {
  get: async () => {
    const r = await client().getShare({});
    return { share: state(r.share), enabled: r.enabled };
  },
  preview: async (settings: ShareSettings): Promise<SharePreview> => {
    const r = await client().previewShare({ settings: wire(settings) });
    return { content: parseShareContent(JSON.parse(r.contentJson)), revision: Number(r.footprintRevision) };
  },
  save: async (request: {
    mutationId: string;
    expectedVersion: number;
    settings: ShareSettings;
    refresh: boolean;
    previewRevision: number;
  }) => {
    const r = await client().saveShare({
      mutationId: request.mutationId,
      expectedVersion: BigInt(request.expectedVersion),
      settings: wire(request.settings),
      refreshContent: request.refresh,
      previewRevision: BigInt(request.previewRevision),
    });
    return state(r.share);
  },
  revoke: async (mutationId: string) => state((await client().revokeShare({ mutationId })).share),
};
