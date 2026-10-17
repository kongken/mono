import { createClient } from "@connectrpc/connect";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import {
  ItineraryService,
  type ItineraryEstimationPolicy as ProtoPolicy,
  type ItineraryInfo as ProtoItineraryInfo,
  type ItineraryLeg as ProtoLeg,
  type ItineraryStop as ProtoStop,
} from "@trip-map/api-client/bff/v1/itinerary_pb";
import {
  DEFAULT_ESTIMATION_POLICY,
  normalizeTransport,
  type ItineraryEstimationPolicy,
  type ItineraryInfo,
  type ItineraryLeg,
  type ItineraryStop,
  type ItineraryStopInput,
} from "@/lib/itinerary";
import { transport } from "./transport";

const raw = transport ? createClient(ItineraryService, transport) : null;
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

function mapStop(s: ProtoStop): ItineraryStop {
  return {
    id: s.id,
    order: s.order,
    regionCode: s.regionCode,
    regionName: s.regionName,
    provinceCode: s.provinceCode,
    provinceName: s.provinceName,
    date: s.date,
    note: s.note,
    transport: normalizeTransport(s.transport),
    tripId: s.tripId,
    longitude: s.longitude,
    latitude: s.latitude,
    wgsLongitude: s.wgsLongitude,
    wgsLatitude: s.wgsLatitude,
  };
}

function mapLeg(l: ProtoLeg): ItineraryLeg {
  return {
    fromOrder: l.fromOrder,
    toOrder: l.toOrder,
    fromRegionCode: l.fromRegionCode,
    fromRegionName: l.fromRegionName,
    toRegionCode: l.toRegionCode,
    toRegionName: l.toRegionName,
    transport: normalizeTransport(l.transport),
    straightDistanceKm: l.straightDistanceKm,
    estimatedDistanceKm: l.estimatedDistanceKm,
    estimatedDurationMinutes: l.estimatedDurationMinutes,
    fromCache: l.fromCache,
  };
}

function mapPolicy(p?: ProtoPolicy): ItineraryEstimationPolicy {
  if (!p || !p.provider) return DEFAULT_ESTIMATION_POLICY;
  return {
    provider: p.provider,
    providerLabel: p.providerLabel || DEFAULT_ESTIMATION_POLICY.providerLabel,
    externalApiEnabled: p.externalApiEnabled,
    costPolicy: p.costPolicy || DEFAULT_ESTIMATION_POLICY.costPolicy,
    cacheStrategy: p.cacheStrategy || DEFAULT_ESTIMATION_POLICY.cacheStrategy,
    disclaimer: p.disclaimer || DEFAULT_ESTIMATION_POLICY.disclaimer,
  };
}

export function mapItineraryInfo(info: ProtoItineraryInfo): ItineraryInfo {
  const createdAt = info.createdAt ? timestampDate(info.createdAt).toISOString() : undefined;
  const updatedAt = info.updatedAt ? timestampDate(info.updatedAt).toISOString() : undefined;
  return {
    id: info.id,
    title: info.title,
    note: info.note,
    color: info.color,
    version: Number(info.version) || 1,
    stops: info.stops.map(mapStop),
    legs: info.legs.map(mapLeg),
    totalStraightDistanceKm: info.totalStraightDistanceKm,
    totalEstimatedDistanceKm: info.totalEstimatedDistanceKm,
    totalEstimatedDurationMinutes: info.totalEstimatedDurationMinutes,
    cityCount: info.cityCount,
    provinceCount: info.provinceCount,
    fromCache: info.fromCache,
    ...(createdAt ? { createdAt } : {}),
    ...(updatedAt ? { updatedAt } : {}),
  };
}

export const itineraryApi = {
  listItineraries: async (): Promise<{
    itineraries: ItineraryInfo[];
    policy: ItineraryEstimationPolicy;
  }> => {
    const res = await client().listItineraries({});
    return {
      itineraries: res.itineraries.map(mapItineraryInfo),
      policy: mapPolicy(res.policy),
    };
  },

  getItinerary: async (
    id: string,
  ): Promise<{
    itinerary: ItineraryInfo;
    policy: ItineraryEstimationPolicy;
  }> => {
    const res = await client().getItinerary({ id });
    if (!res.itinerary) throw new Error("未找到该行程");
    return {
      itinerary: mapItineraryInfo(res.itinerary),
      policy: mapPolicy(res.policy),
    };
  },

  saveItinerary: async (input: {
    id: string;
    title: string;
    note: string;
    color: string;
    stops: ItineraryStopInput[];
    expectedVersion: number;
    mutationId?: string;
  }): Promise<{
    itinerary: ItineraryInfo;
    policy: ItineraryEstimationPolicy;
    replayed: boolean;
  }> => {
    const res = await client().saveItinerary({
      id: input.id,
      title: input.title,
      note: input.note,
      color: input.color,
      stops: input.stops.map((s) => ({
        id: s.id,
        regionCode: s.regionCode,
        date: s.date,
        note: s.note,
        transport: s.transport,
        tripId: s.tripId ?? "",
      })),
      expectedVersion: BigInt(Math.max(0, input.expectedVersion)),
      mutationId: input.mutationId ?? "",
    });
    if (!res.itinerary) throw new Error("保存行程失败");
    return {
      itinerary: mapItineraryInfo(res.itinerary),
      policy: mapPolicy(res.policy),
      replayed: res.replayed,
    };
  },

  deleteItinerary: async (id: string, expectedVersion = 0): Promise<boolean> => {
    const res = await client().deleteItinerary({
      id,
      expectedVersion: BigInt(Math.max(0, expectedVersion)),
    });
    return res.deleted;
  },
};
