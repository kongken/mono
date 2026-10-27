import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import {
  WorldService,
  WorldDocumentSchema,
  type WorldDocument as ApiWorldDocument,
} from "@kongken/api-client/bff/tripmap/v1/world_pb";
import { transport } from "./transport";
import {
  fromMicroDegrees,
  toMicroDegrees,
  type WorldDocument,
} from "../lib/world";

const client = transport ? createClient(WorldService, transport) : null;

const requireClient = () => {
  if (!client) throw new Error("backend not configured");
  return client;
};

export type WorldCloudResponse = {
  applied: boolean;
  replayed: boolean;
  revision: number;
  cloudRevision: number;
  document: WorldDocument;
};

export const toApiWorld = (world: WorldDocument): ApiWorldDocument =>
  create(WorldDocumentSchema, {
    kind: world.kind,
    version: world.version,
    countries: world.countries.map((country) => ({ ...country })),
    places: world.places.map((place) => ({
      id: place.id,
      name: place.name,
      country: place.country,
      latE6: toMicroDegrees(place.lat),
      lngE6: toMicroDegrees(place.lng),
      trips: place.trips.map((trip) => ({ ...trip })),
    })),
  });

export const fromApiWorld = (world: ApiWorldDocument | undefined): WorldDocument => ({
  kind: "world",
  version: 1,
  countries: (world?.countries ?? []).map((country) => ({ code: country.code, color: country.color })),
  places: (world?.places ?? []).map((place) => ({
    id: place.id,
    name: place.name,
    country: place.country,
    lat: fromMicroDegrees(place.latE6),
    lng: fromMicroDegrees(place.lngE6),
    trips: place.trips.map((trip) => ({ id: trip.id, date: trip.date, note: trip.note })),
  })),
});

export async function getWorld(expectedUserId: string) {
  const response = await requireClient().getWorld({}, { headers: { "X-Kongken-User": expectedUserId } });
  return { revision: Number(response.revision), document: fromApiWorld(response.document) };
}

export async function replaceWorld(
  expectedUserId: string,
  baseRevision: number,
  requestId: string,
  document: WorldDocument,
): Promise<WorldCloudResponse> {
  try {
    const response = await requireClient().replaceWorld(
      { baseRevision: BigInt(baseRevision), requestId, document: toApiWorld(document) },
      { headers: { "X-Kongken-User": expectedUserId } },
    );
    return {
      applied: response.applied,
      replayed: response.replayed,
      revision: Number(response.revision),
      cloudRevision: Number(response.cloudRevision),
      document: fromApiWorld(response.document),
    };
  } catch (error) {
    if (error instanceof ConnectError && error.code === Code.AlreadyExists) {
      throw Object.assign(new Error("云端请求 ID 已存在"), { code: Code.AlreadyExists });
    }
    throw error;
  }
}
