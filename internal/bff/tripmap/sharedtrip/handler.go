package sharedtrip

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

func (h *Handler) tripmap(ctx context.Context, req connect.AnyRequest) context.Context {
	return upstream.WithTripmapHeaders(ctx, req.Header())
}

func (h *Handler) InviteToSharedTrip(ctx context.Context, req *connect.Request[bfftripmapv1.InviteToSharedTripRequest]) (*connect.Response[bfftripmapv1.InviteToSharedTripResponse], error) {
	in := req.Msg
	out, err := h.client.InviteToSharedTrip(h.tripmap(ctx, req), &tripmapv1.InviteToSharedTripRequest{
		RegionCode: in.GetRegionCode(), TripId: in.GetTripId(), FriendUserId: in.GetFriendUserId(), Role: in.GetRole(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.InviteToSharedTripResponse{SharedTrip: mapSharedTrip(out.GetSharedTrip())}), nil
}

func (h *Handler) RespondSharedTripInvite(ctx context.Context, req *connect.Request[bfftripmapv1.RespondSharedTripInviteRequest]) (*connect.Response[bfftripmapv1.RespondSharedTripInviteResponse], error) {
	out, err := h.client.RespondSharedTripInvite(h.tripmap(ctx, req), &tripmapv1.RespondSharedTripInviteRequest{
		SharedTripId: req.Msg.GetSharedTripId(), Accept: req.Msg.GetAccept(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.RespondSharedTripInviteResponse{
		SharedTrip: mapSharedTrip(out.GetSharedTrip()), FootprintRevision: out.GetFootprintRevision(),
	}), nil
}

func (h *Handler) UpdateSharedTrip(ctx context.Context, req *connect.Request[bfftripmapv1.UpdateSharedTripRequest]) (*connect.Response[bfftripmapv1.UpdateSharedTripResponse], error) {
	in := req.Msg
	out, err := h.client.UpdateSharedTrip(h.tripmap(ctx, req), &tripmapv1.UpdateSharedTripRequest{
		SharedTripId: in.GetSharedTripId(), Date: in.GetDate(), Note: in.GetNote(), MutationId: in.GetMutationId(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.UpdateSharedTripResponse{
		SharedTrip: mapSharedTrip(out.GetSharedTrip()), FootprintRevision: out.GetFootprintRevision(),
	}), nil
}

func (h *Handler) SetSharedTripMemberRole(ctx context.Context, req *connect.Request[bfftripmapv1.SetSharedTripMemberRoleRequest]) (*connect.Response[bfftripmapv1.SetSharedTripMemberRoleResponse], error) {
	in := req.Msg
	out, err := h.client.SetSharedTripMemberRole(h.tripmap(ctx, req), &tripmapv1.SetSharedTripMemberRoleRequest{
		SharedTripId: in.GetSharedTripId(), MemberUserId: in.GetMemberUserId(), Role: in.GetRole(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.SetSharedTripMemberRoleResponse{SharedTrip: mapSharedTrip(out.GetSharedTrip())}), nil
}

func (h *Handler) RemoveSharedTripMember(ctx context.Context, req *connect.Request[bfftripmapv1.RemoveSharedTripMemberRequest]) (*connect.Response[bfftripmapv1.RemoveSharedTripMemberResponse], error) {
	in := req.Msg
	out, err := h.client.RemoveSharedTripMember(h.tripmap(ctx, req), &tripmapv1.RemoveSharedTripMemberRequest{
		SharedTripId: in.GetSharedTripId(), MemberUserId: in.GetMemberUserId(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.RemoveSharedTripMemberResponse{SharedTrip: mapSharedTrip(out.GetSharedTrip())}), nil
}

func (h *Handler) LeaveSharedTrip(ctx context.Context, req *connect.Request[bfftripmapv1.LeaveSharedTripRequest]) (*connect.Response[bfftripmapv1.LeaveSharedTripResponse], error) {
	out, err := h.client.LeaveSharedTrip(h.tripmap(ctx, req), &tripmapv1.LeaveSharedTripRequest{SharedTripId: req.Msg.GetSharedTripId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.LeaveSharedTripResponse{FootprintRevision: out.GetFootprintRevision()}), nil
}

func (h *Handler) DeleteSharedTrip(ctx context.Context, req *connect.Request[bfftripmapv1.DeleteSharedTripRequest]) (*connect.Response[bfftripmapv1.DeleteSharedTripResponse], error) {
	if _, err := h.client.DeleteSharedTrip(h.tripmap(ctx, req), &tripmapv1.DeleteSharedTripRequest{SharedTripId: req.Msg.GetSharedTripId()}); err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.DeleteSharedTripResponse{}), nil
}

func (h *Handler) ListSharedTripMedia(ctx context.Context, req *connect.Request[bfftripmapv1.ListSharedTripMediaRequest]) (*connect.Response[bfftripmapv1.ListSharedTripMediaResponse], error) {
	out, err := h.client.ListSharedTripMedia(h.tripmap(ctx, req), &tripmapv1.ListSharedTripMediaRequest{SharedTripId: req.Msg.GetSharedTripId()})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	return connect.NewResponse(&bfftripmapv1.ListSharedTripMediaResponse{Media: mapSharedTripMediaList(out.GetMedia())}), nil
}

func (h *Handler) ReadSharedTripMedia(ctx context.Context, req *connect.Request[bfftripmapv1.ReadSharedTripMediaRequest]) (*connect.Response[bfftripmapv1.ReadSharedTripMediaResponse], error) {
	in := req.Msg
	out, err := h.client.ReadSharedTripMedia(h.tripmap(ctx, req), &tripmapv1.ReadSharedTripMediaRequest{
		SharedTripId: in.GetSharedTripId(), MediaId: in.GetMediaId(), Thumbnail: in.GetThumbnail(),
	})
	if err != nil {
		return nil, upstream.ToConnect("tripmap", err)
	}
	res := connect.NewResponse(&bfftripmapv1.ReadSharedTripMediaResponse{
		Data: out.GetData(), ContentType: out.GetContentType(), Filename: out.GetFilename(), Url: out.GetUrl(),
	})
	res.Header().Set("Cache-Control", "no-store")
	return res, nil
}
