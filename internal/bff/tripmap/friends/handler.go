package friends

import (
	"context"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/upstream"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client tripmapv1.CollaborationServiceClient
}

func New(client tripmapv1.CollaborationServiceClient) *Handler {
	return &Handler{client: client}
}

func (h *Handler) GetCollaborationOverview(ctx context.Context, req *connect.Request[bfftripmapv1.GetCollaborationOverviewRequest]) (*connect.Response[bfftripmapv1.GetCollaborationOverviewResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.GetCollaborationOverview(ctx, &tripmapv1.GetCollaborationOverviewRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapOverview(out)), nil
}

func (h *Handler) UpdateFriendSettings(ctx context.Context, req *connect.Request[bfftripmapv1.UpdateFriendSettingsRequest]) (*connect.Response[bfftripmapv1.UpdateFriendSettingsResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	in := req.Msg.GetSettings()
	out, err := h.client.UpdateFriendSettings(ctx, &tripmapv1.UpdateFriendSettingsRequest{
		Settings: &tripmapv1.FriendShareSettings{
			ShowCities: in.GetShowCities(), ShowDates: in.GetShowDates(), ShowNotes: in.GetShowNotes(),
		},
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.UpdateFriendSettingsResponse{Settings: mapFriendSettings(out.GetSettings())}), nil
}

func (h *Handler) SendFriendRequest(ctx context.Context, req *connect.Request[bfftripmapv1.SendFriendRequestRequest]) (*connect.Response[bfftripmapv1.SendFriendRequestResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.SendFriendRequest(ctx, &tripmapv1.SendFriendRequestRequest{Target: req.Msg.GetTarget()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.SendFriendRequestResponse{Friendship: mapFriendship(out.GetFriendship())}), nil
}

func (h *Handler) RespondFriendRequest(ctx context.Context, req *connect.Request[bfftripmapv1.RespondFriendRequestRequest]) (*connect.Response[bfftripmapv1.RespondFriendRequestResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.RespondFriendRequest(ctx, &tripmapv1.RespondFriendRequestRequest{
		FriendshipId: req.Msg.GetFriendshipId(), Accept: req.Msg.GetAccept(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.RespondFriendRequestResponse{Friendship: mapFriendship(out.GetFriendship())}), nil
}

func (h *Handler) RemoveFriend(ctx context.Context, req *connect.Request[bfftripmapv1.RemoveFriendRequest]) (*connect.Response[bfftripmapv1.RemoveFriendResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	if _, err := h.client.RemoveFriend(ctx, &tripmapv1.RemoveFriendRequest{FriendshipId: req.Msg.GetFriendshipId()}); err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.RemoveFriendResponse{}), nil
}

func (h *Handler) GetFriendFootprints(ctx context.Context, req *connect.Request[bfftripmapv1.GetFriendFootprintsRequest]) (*connect.Response[bfftripmapv1.GetFriendFootprintsResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.GetFriendFootprints(ctx, &tripmapv1.GetFriendFootprintsRequest{FriendUserId: req.Msg.GetFriendUserId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapFriendFootprints(out)), nil
}

// map helpers unchanged below

func mapOverview(in *tripmapv1.GetCollaborationOverviewResponse) *bfftripmapv1.GetCollaborationOverviewResponse {
	out := &bfftripmapv1.GetCollaborationOverviewResponse{
		MyFriendCode: in.GetMyFriendCode(), FriendSettings: mapFriendSettings(in.GetFriendSettings()),
	}
	for _, f := range in.GetFriends() {
		out.Friends = append(out.Friends, mapFriendship(f))
	}
	for _, st := range in.GetSharedTrips() {
		out.SharedTrips = append(out.SharedTrips, mapSharedTrip(st))
	}
	return out
}

func mapFriendSettings(in *tripmapv1.FriendShareSettings) *bfftripmapv1.FriendShareSettings {
	if in == nil {
		return &bfftripmapv1.FriendShareSettings{}
	}
	return &bfftripmapv1.FriendShareSettings{
		ShowCities: in.GetShowCities(), ShowDates: in.GetShowDates(), ShowNotes: in.GetShowNotes(),
	}
}

func mapFriendUser(in *tripmapv1.FriendUser) *bfftripmapv1.FriendUser {
	if in == nil {
		return &bfftripmapv1.FriendUser{}
	}
	return &bfftripmapv1.FriendUser{
		Id: in.GetId(), Username: in.GetUsername(), DisplayName: in.GetDisplayName(), AvatarUrl: in.GetAvatarUrl(),
	}
}

func mapFriendship(in *tripmapv1.FriendshipInfo) *bfftripmapv1.FriendshipInfo {
	if in == nil {
		return &bfftripmapv1.FriendshipInfo{}
	}
	return &bfftripmapv1.FriendshipInfo{
		Id: in.GetId(), Friend: mapFriendUser(in.GetFriend()), Status: in.GetStatus(), Direction: in.GetDirection(),
		CreatedAt: in.GetCreatedAt(), AcceptedAt: in.GetAcceptedAt(),
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

func mapFriendFootprints(in *tripmapv1.GetFriendFootprintsResponse) *bfftripmapv1.GetFriendFootprintsResponse {
	out := &bfftripmapv1.GetFriendFootprintsResponse{
		Friend: mapFriendUser(in.GetFriend()), FriendSettings: mapFriendSettings(in.GetFriendSettings()),
		ContentJson: in.GetContentJson(),
	}
	if c := in.GetComparison(); c != nil {
		out.Comparison = &bfftripmapv1.FriendComparison{
			CommonProvinces: c.GetCommonProvinces(), OnlyMeProvinces: c.GetOnlyMeProvinces(),
			OnlyFriendProvinces: c.GetOnlyFriendProvinces(), MyOnlyCityCount: c.GetMyOnlyCityCount(),
			FriendOnlyCityCount: c.GetFriendOnlyCityCount(), CityComparisonAvailable: c.GetCityComparisonAvailable(),
		}
		for _, city := range c.GetCommonCities() {
			out.Comparison.CommonCities = append(out.Comparison.CommonCities, &bfftripmapv1.FriendSharedCity{
				Code: city.GetCode(), Name: city.GetName(), Province: city.GetProvince(),
				MyColor: city.GetMyColor(), FriendColor: city.GetFriendColor(),
			})
		}
	}
	return out
}
