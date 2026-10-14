import { createClient } from "@connectrpc/connect";
import { MediaService, type MediaInfo } from "@/gen/tripmap/v1/media_pb";
import { transport } from "./transport";

const raw = transport ? createClient(MediaService, transport) : null;
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};
export type { MediaInfo };
export const mediaApi = {
  list: async (regionCode = "", tripId = "") =>
    (await client().listMedia({ regionCode, tripId })).media,
  prepare: async (regionCode: string, file: File, uploadId: string, tripId = "") =>
    client().prepareUpload({
      regionCode,
      tripId,
      filename: file.name,
      contentType: file.type,
      sizeBytes: BigInt(file.size),
      uploadId,
    }),
  complete: async (id: string) => {
    const r = await client().completeUpload({ id });
    if (!r.media) throw new Error("incomplete media response");
    return r.media;
  },
  read: async (id: string, thumbnail = false) => {
    const r = await client().readMedia({ id, thumbnail });
    return {
      blob: new Blob([new Uint8Array(r.data)], { type: r.contentType }),
      filename: r.filename,
    };
  },
  delete: async (id: string) => {
    await client().deleteMedia({ id });
  },
};
