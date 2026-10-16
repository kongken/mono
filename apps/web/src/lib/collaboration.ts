import regions from "@trip-map/geo-data/regions.json" with { type: "json" };

export type FriendPrivacySettings = {
  showCities: boolean;
  showDates: boolean;
  showNotes: boolean;
};

export function normalizeFriendSettings(input: FriendPrivacySettings): FriendPrivacySettings {
  const showCities = Boolean(input.showCities);
  return {
    showCities,
    showDates: showCities && Boolean(input.showDates),
    showNotes: showCities && Boolean(input.showNotes),
  };
}

export function canEditSharedTrip(myRole: string, myStatus: string): boolean {
  return myStatus === "active" && (myRole === "owner" || myRole === "editor");
}

export function canManageSharedTripMembers(myRole: string, myStatus: string): boolean {
  return myStatus === "active" && myRole === "owner";
}

export function roleLabel(role: string): string {
  switch (role) {
    case "owner":
      return "创建者";
    case "editor":
      return "共同编辑";
    case "viewer":
      return "只读成员";
    default:
      return "成员";
  }
}

export function friendLabel(
  user?: { displayName?: string; username?: string; id?: string } | null,
): string {
  if (!user) return "未知用户";
  return user.displayName || user.username || user.id || "未知用户";
}

const provinceMap = new Map<string, string>(
  regions.map((r) => [r.code, r.name]),
);

export function provinceNameByCode(code: string): string {
  return provinceMap.get(code) ?? code;
}

export function friendComparisonColors(comparison: {
  commonProvinces: readonly string[];
  onlyMeProvinces: readonly string[];
  onlyFriendProvinces: readonly string[];
}): Record<string, string> {
  const out: Record<string, string> = {};
  for (const code of comparison.onlyFriendProvinces) {
    out[code] = "#e2af5c";
  }
  for (const code of comparison.onlyMeProvinces) {
    out[code] = "#719fc6";
  }
  for (const code of comparison.commonProvinces) {
    out[code] = "#4b937d";
  }
  return out;
}

export function indexActiveSharedTripsByTripId<
  T extends { regionCode: string; tripId: string; myStatus: string },
>(sharedTrips: readonly T[]): Map<string, T> {
  const map = new Map<string, T>();
  for (const st of sharedTrips) {
    if (st.myStatus === "active" && st.regionCode && st.tripId) {
      map.set(`${st.regionCode}:${st.tripId}`, st);
    }
  }
  return map;
}
