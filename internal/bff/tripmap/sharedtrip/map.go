package sharedtrip

import (
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

func mapFriendUser(in *tripmapv1.FriendUser) *bfftripmapv1.FriendUser {
	if in == nil {
		return &bfftripmapv1.FriendUser{}
	}
	return &bfftripmapv1.FriendUser{
		Id: in.GetId(), DisplayName: in.GetDisplayName(), AvatarUrl: in.GetAvatarUrl(),
	}
}

func mapSharedTrip(in *tripmapv1.SharedTripInfo) *bfftripmapv1.SharedTripInfo {
	if in == nil {
		return &bfftripmapv1.SharedTripInfo{}
	}
	out := &bfftripmapv1.SharedTripInfo{
		Id: in.GetId(), RegionCode: in.GetRegionCode(), RegionName: in.GetRegionName(),
		ProvinceCode: in.GetProvinceCode(), TripId: in.GetTripId(), Date: in.GetDate(), Note: in.GetNote(),
		Owner: mapFriendUser(in.GetOwner()), MyRole: in.GetMyRole(), MyStatus: in.GetMyStatus(), UpdatedAt: in.GetUpdatedAt(),
	}
	for _, m := range in.GetMembers() {
		out.Members = append(out.Members, &bfftripmapv1.SharedTripMember{
			User: mapFriendUser(m.GetUser()), Role: m.GetRole(), Status: m.GetStatus(), JoinedAt: m.GetJoinedAt(),
		})
	}
	return out
}

func mapMedia(in *tripmapv1.MediaInfo) *bfftripmapv1.MediaInfo {
	if in == nil {
		return nil
	}
	return &bfftripmapv1.MediaInfo{
		Id: in.GetId(), RegionCode: in.GetRegionCode(), Filename: in.GetFilename(),
		ContentType: in.GetContentType(), SizeBytes: in.GetSizeBytes(),
		Width: in.GetWidth(), Height: in.GetHeight(), Status: in.GetStatus(),
		CreatedAt: in.GetCreatedAt(), TripId: in.GetTripId(),
	}
}

func mapSharedTripMediaList(in []*tripmapv1.SharedTripMediaInfo) []*bfftripmapv1.SharedTripMediaInfo {
	out := make([]*bfftripmapv1.SharedTripMediaInfo, 0, len(in))
	for _, item := range in {
		out = append(out, &bfftripmapv1.SharedTripMediaInfo{
			Media: mapMedia(item.GetMedia()), Uploader: mapFriendUser(item.GetUploader()), Mine: item.GetMine(),
		})
	}
	return out
}
