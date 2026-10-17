import { createClient } from "@connectrpc/connect";
import {
  FriendService,
  type FriendshipInfo as BffFriendshipInfo,
  type FriendUser as BffFriendUser,
  type FriendComparison as BffFriendComparison,
  type SharedTripInfo as BffSharedTripInfo,
} from "@trip-map/api-client/bff/v1/friends_pb";
import {
  SharedTripService,
  type SharedTripMediaInfo as BffSharedTripMediaInfo,
} from "@trip-map/api-client/bff/v1/shared_trip_pb";
import { parseShareContent, type ShareContent } from "@/lib/share";
import { normalizeFriendSettings, type FriendPrivacySettings } from "@/lib/collaboration";
import { transport } from "./transport";

const friendRaw = transport ? createClient(FriendService, transport) : null;
const sharedTripRaw = transport ? createClient(SharedTripService, transport) : null;

const friendClient = () => {
  if (!friendRaw) throw new Error("backend not configured");
  return friendRaw;
};

const sharedTripClient = () => {
  if (!sharedTripRaw) throw new Error("backend not configured");
  return sharedTripRaw;
};

export type FriendshipInfo = BffFriendshipInfo;
export type FriendUser = BffFriendUser;
export type FriendComparison = BffFriendComparison;
export type SharedTripInfo = BffSharedTripInfo;
export type SharedTripMediaInfo = BffSharedTripMediaInfo;

export type CollaborationOverview = {
  myFriendCode: string;
  friendSettings: FriendPrivacySettings;
  friends: FriendshipInfo[];
  sharedTrips: BffSharedTripInfo[];
};

export type FriendFootprintView = {
  friend: FriendUser | undefined;
  friendSettings: FriendPrivacySettings;
  content: ShareContent;
  comparison: FriendComparison | undefined;
};

export const collaborationApi = {
  getOverview: async (): Promise<CollaborationOverview> => {
    const r = await friendClient().getCollaborationOverview({});
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
    const r = await friendClient().updateFriendSettings({ settings: normalized });
    return normalizeFriendSettings({
      showCities: r.settings?.showCities ?? normalized.showCities,
      showDates: r.settings?.showDates ?? normalized.showDates,
      showNotes: r.settings?.showNotes ?? normalized.showNotes,
    });
  },
  sendFriendRequest: async (target: string): Promise<FriendshipInfo | undefined> => {
    const r = await friendClient().sendFriendRequest({ target });
    return r.friendship;
  },
  respondFriendRequest: async (friendshipId: string, accept: boolean): Promise<FriendshipInfo | undefined> => {
    const r = await friendClient().respondFriendRequest({ friendshipId, accept });
    return r.friendship;
  },
  removeFriend: async (friendshipId: string): Promise<void> => {
    await friendClient().removeFriend({ friendshipId });
  },
  getFriendFootprints: async (friendUserId: string): Promise<FriendFootprintView> => {
    const r = await friendClient().getFriendFootprints({ friendUserId });
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
    const r = await sharedTripClient().inviteToSharedTrip(params);
    return r.sharedTrip;
  },
  respondSharedTripInvite: async (
    sharedTripId: string,
    accept: boolean,
  ): Promise<{ sharedTrip?: SharedTripInfo; footprintRevision: number }> => {
    const r = await sharedTripClient().respondSharedTripInvite({ sharedTripId, accept });
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
    const r = await sharedTripClient().updateSharedTrip(params);
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
    const r = await sharedTripClient().setSharedTripMemberRole(params);
    return r.sharedTrip;
  },
  removeSharedTripMember: async (params: {
    sharedTripId: string;
    memberUserId: string;
  }): Promise<SharedTripInfo | undefined> => {
    const r = await sharedTripClient().removeSharedTripMember(params);
    return r.sharedTrip;
  },
  leaveSharedTrip: async (sharedTripId: string): Promise<number> => {
    const r = await sharedTripClient().leaveSharedTrip({ sharedTripId });
    return Number(r.footprintRevision);
  },
  deleteSharedTrip: async (sharedTripId: string): Promise<void> => {
    await sharedTripClient().deleteSharedTrip({ sharedTripId });
  },
  listSharedTripMedia: async (sharedTripId: string): Promise<SharedTripMediaInfo[]> => {
    const r = await sharedTripClient().listSharedTripMedia({ sharedTripId });
    return r.media;
  },
  readSharedTripMedia: async (sharedTripId: string, mediaId: string, thumbnail = false) => {
    const r = await sharedTripClient().readSharedTripMedia({ sharedTripId, mediaId, thumbnail });
    return {
      blob: new Blob([new Uint8Array(r.data)], { type: r.contentType }),
      filename: r.filename,
    };
  },
};
