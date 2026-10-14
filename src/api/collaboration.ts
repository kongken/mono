import { createClient } from "@connectrpc/connect";
import {
  CollaborationService,
  type FriendshipInfo,
  type FriendUser,
  type FriendComparison,
  type SharedTripInfo,
  type SharedTripMediaInfo,
} from "@/gen/tripmap/v1/collaboration_pb";
import { parseShareContent, type ShareContent } from "@/lib/share";
import { normalizeFriendSettings, type FriendPrivacySettings } from "@/lib/collaboration";
import { transport } from "./transport";

const raw = transport ? createClient(CollaborationService, transport) : null;
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

export type {
  FriendshipInfo,
  FriendUser,
  FriendComparison,
  SharedTripInfo,
  SharedTripMediaInfo,
};

export type CollaborationOverview = {
  myFriendCode: string;
  friendSettings: FriendPrivacySettings;
  friends: FriendshipInfo[];
  sharedTrips: SharedTripInfo[];
};

export type FriendFootprintView = {
  friend: FriendUser | undefined;
  friendSettings: FriendPrivacySettings;
  content: ShareContent;
  comparison: FriendComparison | undefined;
};

export const collaborationApi = {
  getOverview: async (): Promise<CollaborationOverview> => {
    const r = await client().getCollaborationOverview({});
    return {
      myFriendCode: r.myFriendCode,
      friendSettings: normalizeFriendSettings({
        showCities: r.friendSettings?.showCities ?? true,
        showDates: r.friendSettings?.showDates ?? false,
        showNotes: r.friendSettings?.showNotes ?? false,
      }),
      friends: r.friends,
      sharedTrips: r.sharedTrips,
    };
  },
  updateFriendSettings: async (settings: FriendPrivacySettings): Promise<FriendPrivacySettings> => {
    const normalized = normalizeFriendSettings(settings);
    const r = await client().updateFriendSettings({ settings: normalized });
    return normalizeFriendSettings({
      showCities: r.settings?.showCities ?? normalized.showCities,
      showDates: r.settings?.showDates ?? normalized.showDates,
      showNotes: r.settings?.showNotes ?? normalized.showNotes,
    });
  },
  sendFriendRequest: async (target: string): Promise<FriendshipInfo | undefined> => {
    const r = await client().sendFriendRequest({ target });
    return r.friendship;
  },
  respondFriendRequest: async (friendshipId: string, accept: boolean): Promise<FriendshipInfo | undefined> => {
    const r = await client().respondFriendRequest({ friendshipId, accept });
    return r.friendship;
  },
  removeFriend: async (friendshipId: string): Promise<void> => {
    await client().removeFriend({ friendshipId });
  },
  getFriendFootprints: async (friendUserId: string): Promise<FriendFootprintView> => {
    const r = await client().getFriendFootprints({ friendUserId });
    return {
      friend: r.friend,
      friendSettings: normalizeFriendSettings({
        showCities: r.friendSettings?.showCities ?? true,
        showDates: r.friendSettings?.showDates ?? false,
        showNotes: r.friendSettings?.showNotes ?? false,
      }),
      content: parseShareContent(JSON.parse(r.contentJson)),
      comparison: r.comparison,
    };
  },
  inviteToSharedTrip: async (params: {
    regionCode: string;
    tripId: string;
    friendUserId: string;
    role: "editor" | "viewer";
  }): Promise<SharedTripInfo | undefined> => {
    const r = await client().inviteToSharedTrip(params);
    return r.sharedTrip;
  },
  respondSharedTripInvite: async (
    sharedTripId: string,
    accept: boolean,
  ): Promise<{ sharedTrip?: SharedTripInfo; footprintRevision: number }> => {
    const r = await client().respondSharedTripInvite({ sharedTripId, accept });
    return {
      sharedTrip: r.sharedTrip,
      footprintRevision: Number(r.footprintRevision),
    };
  },
  updateSharedTrip: async (params: {
    sharedTripId: string;
    date: string;
    note: string;
    mutationId: string;
  }): Promise<{ sharedTrip?: SharedTripInfo; footprintRevision: number }> => {
    const r = await client().updateSharedTrip(params);
    return {
      sharedTrip: r.sharedTrip,
      footprintRevision: Number(r.footprintRevision),
    };
  },
  setSharedTripMemberRole: async (params: {
    sharedTripId: string;
    memberUserId: string;
    role: "editor" | "viewer";
  }): Promise<SharedTripInfo | undefined> => {
    const r = await client().setSharedTripMemberRole(params);
    return r.sharedTrip;
  },
  removeSharedTripMember: async (params: {
    sharedTripId: string;
    memberUserId: string;
  }): Promise<SharedTripInfo | undefined> => {
    const r = await client().removeSharedTripMember(params);
    return r.sharedTrip;
  },
  leaveSharedTrip: async (sharedTripId: string): Promise<number> => {
    const r = await client().leaveSharedTrip({ sharedTripId });
    return Number(r.footprintRevision);
  },
  deleteSharedTrip: async (sharedTripId: string): Promise<void> => {
    await client().deleteSharedTrip({ sharedTripId });
  },
  listSharedTripMedia: async (sharedTripId: string): Promise<SharedTripMediaInfo[]> => {
    const r = await client().listSharedTripMedia({ sharedTripId });
    return r.media;
  },
  readSharedTripMedia: async (sharedTripId: string, mediaId: string, thumbnail = false) => {
    const r = await client().readSharedTripMedia({ sharedTripId, mediaId, thumbnail });
    return {
      blob: new Blob([new Uint8Array(r.data)], { type: r.contentType }),
      filename: r.filename,
    };
  },
};
