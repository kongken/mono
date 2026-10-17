package friends

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/upstream"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type Handler struct {
	client         tripmapv1.CollaborationServiceClient
	allowedOrigins []string
}

func New(client tripmapv1.CollaborationServiceClient, allowedOrigins []string) *Handler {
	return &Handler{client: client, allowedOrigins: allowedOrigins}
}

func (h *Handler) checkWrite(header http.Header) error {
	if err := session.CheckWrite(header, h.allowedOrigins, header.Get("Host")); err != nil {
		return connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
	}
	return nil
}

func (h *Handler) GetCollaborationOverview(ctx context.Context, req *connect.Request[bffv1.GetCollaborationOverviewRequest]) (*connect.Response[bffv1.GetCollaborationOverviewResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.GetCollaborationOverview(ctx, &tripmapv1.GetCollaborationOverviewRequest{})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapOverview(out)), nil
}

func (h *Handler) UpdateFriendSettings(ctx context.Context, req *connect.Request[bffv1.UpdateFriendSettingsRequest]) (*connect.Response[bffv1.UpdateFriendSettingsResponse], error) {
	if err := h.checkWrite(req.Header()); err != nil {
		return nil, err
	}
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
	return connect.NewResponse(&bffv1.UpdateFriendSettingsResponse{Settings: mapFriendSettings(out.GetSettings())}), nil
}

func (h *Handler) SendFriendRequest(ctx context.Context, req *connect.Request[bffv1.SendFriendRequestRequest]) (*connect.Response[bffv1.SendFriendRequestResponse], error) {
	if err := h.checkWrite(req.Header()); err != nil {
		return nil, err
	}
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.SendFriendRequest(ctx, &tripmapv1.SendFriendRequestRequest{Target: req.Msg.GetTarget()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.SendFriendRequestResponse{Friendship: mapFriendship(out.GetFriendship())}), nil
}

func (h *Handler) RespondFriendRequest(ctx context.Context, req *connect.Request[bffv1.RespondFriendRequestRequest]) (*connect.Response[bffv1.RespondFriendRequestResponse], error) {
	if err := h.checkWrite(req.Header()); err != nil {
		return nil, err
	}
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.RespondFriendRequest(ctx, &tripmapv1.RespondFriendRequestRequest{
		FriendshipId: req.Msg.GetFriendshipId(), Accept: req.Msg.GetAccept(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.RespondFriendRequestResponse{Friendship: mapFriendship(out.GetFriendship())}), nil
}

func (h *Handler) RemoveFriend(ctx context.Context, req *connect.Request[bffv1.RemoveFriendRequest]) (*connect.Response[bffv1.RemoveFriendResponse], error) {
	if err := h.checkWrite(req.Header()); err != nil {
		return nil, err
	}
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	if _, err := h.client.RemoveFriend(ctx, &tripmapv1.RemoveFriendRequest{FriendshipId: req.Msg.GetFriendshipId()}); err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bffv1.RemoveFriendResponse{}), nil
}

func (h *Handler) GetFriendFootprints(ctx context.Context, req *connect.Request[bffv1.GetFriendFootprintsRequest]) (*connect.Response[bffv1.GetFriendFootprintsResponse], error) {
	ctx = upstream.WithTripmapHeaders(ctx, req.Header())
	out, err := h.client.GetFriendFootprints(ctx, &tripmapv1.GetFriendFootprintsRequest{FriendUserId: req.Msg.GetFriendUserId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(mapFriendFootprints(out)), nil
}

func mapOverview(in *tripmapv1.GetCollaborationOverviewResponse) *bffv1.GetCollaborationOverviewResponse {
	out := &bffv1.GetCollaborationOverviewResponse{
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

func mapFriendSettings(in *tripmapv1.FriendShareSettings) *bffv1.FriendShareSettings {
	if in == nil {
		return &bffv1.FriendShareSettings{}
	}
	return &bffv1.FriendShareSettings{
		ShowCities: in.GetShowCities(), ShowDates: in.GetShowDates(), ShowNotes: in.GetShowNotes(),
	}
}

func mapFriendUser(in *tripmapv1.FriendUser) *bffv1.FriendUser {
	if in == nil {
		return &bffv1.FriendUser{}
	}
	return &bffv1.FriendUser{
		Id: in.GetId(), Username: in.GetUsername(), DisplayName: in.GetDisplayName(), AvatarUrl: in.GetAvatarUrl(),
	}
}

func mapFriendship(in *tripmapv1.FriendshipInfo) *bffv1.FriendshipInfo {
	if in == nil {
		return &bffv1.FriendshipInfo{}
	}
	return &bffv1.FriendshipInfo{
		Id: in.GetId(), Friend: mapFriendUser(in.GetFriend()), Status: in.GetStatus(), Direction: in.GetDirection(),
		CreatedAt: in.GetCreatedAt(), AcceptedAt: in.GetAcceptedAt(),
	}
}

func mapSharedTrip(in *tripmapv1.SharedTripInfo) *bffv1.SharedTripInfo {
	if in == nil {
		return &bffv1.SharedTripInfo{}
	}
	out := &bffv1.SharedTripInfo{
		Id: in.GetId(), RegionCode: in.GetRegionCode(), RegionName: in.GetRegionName(),
		ProvinceCode: in.GetProvinceCode(), TripId: in.GetTripId(), Date: in.GetDate(), Note: in.GetNote(),
		Owner: mapFriendUser(in.GetOwner()), MyRole: in.GetMyRole(), MyStatus: in.GetMyStatus(), UpdatedAt: in.GetUpdatedAt(),
	}
	for _, m := range in.GetMembers() {
		out.Members = append(out.Members, &bffv1.SharedTripMember{
			User: mapFriendUser(m.GetUser()), Role: m.GetRole(), Status: m.GetStatus(), JoinedAt: m.GetJoinedAt(),
		})
	}
	return out
}

func mapFriendFootprints(in *tripmapv1.GetFriendFootprintsResponse) *bffv1.GetFriendFootprintsResponse {
	out := &bffv1.GetFriendFootprintsResponse{
		Friend: mapFriendUser(in.GetFriend()), FriendSettings: mapFriendSettings(in.GetFriendSettings()),
		ContentJson: in.GetContentJson(),
	}
	if c := in.GetComparison(); c != nil {
		out.Comparison = &bffv1.FriendComparison{
			CommonProvinces: c.GetCommonProvinces(), OnlyMeProvinces: c.GetOnlyMeProvinces(),
			OnlyFriendProvinces: c.GetOnlyFriendProvinces(), MyOnlyCityCount: c.GetMyOnlyCityCount(),
			FriendOnlyCityCount: c.GetFriendOnlyCityCount(), CityComparisonAvailable: c.GetCityComparisonAvailable(),
		}
		for _, city := range c.GetCommonCities() {
			out.Comparison.CommonCities = append(out.Comparison.CommonCities, &bffv1.FriendSharedCity{
				Code: city.GetCode(), Name: city.GetName(), Province: city.GetProvince(),
				MyColor: city.GetMyColor(), FriendColor: city.GetFriendColor(),
			})
		}
	}
	return out
}
