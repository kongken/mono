package application

import (
	"context"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type collaborationGRPC struct {
	tripmapv1.UnimplementedCollaborationServiceServer
	*CollaborationService
}

// CollaborationGRPC adapts CollaborationService for the native gRPC server.
func CollaborationGRPC(s *CollaborationService) tripmapv1.CollaborationServiceServer {
	return collaborationGRPC{CollaborationService: s}
}

func (c collaborationGRPC) GetCollaborationOverview(ctx context.Context, req *tripmapv1.GetCollaborationOverviewRequest) (*tripmapv1.GetCollaborationOverviewResponse, error) {
	res, err := c.CollaborationService.GetCollaborationOverview(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) UpdateFriendSettings(ctx context.Context, req *tripmapv1.UpdateFriendSettingsRequest) (*tripmapv1.UpdateFriendSettingsResponse, error) {
	res, err := c.CollaborationService.UpdateFriendSettings(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) SendFriendRequest(ctx context.Context, req *tripmapv1.SendFriendRequestRequest) (*tripmapv1.SendFriendRequestResponse, error) {
	res, err := c.CollaborationService.SendFriendRequest(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) RespondFriendRequest(ctx context.Context, req *tripmapv1.RespondFriendRequestRequest) (*tripmapv1.RespondFriendRequestResponse, error) {
	res, err := c.CollaborationService.RespondFriendRequest(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) RemoveFriend(ctx context.Context, req *tripmapv1.RemoveFriendRequest) (*tripmapv1.RemoveFriendResponse, error) {
	res, err := c.CollaborationService.RemoveFriend(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) GetFriendFootprints(ctx context.Context, req *tripmapv1.GetFriendFootprintsRequest) (*tripmapv1.GetFriendFootprintsResponse, error) {
	res, err := c.CollaborationService.GetFriendFootprints(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) InviteToSharedTrip(ctx context.Context, req *tripmapv1.InviteToSharedTripRequest) (*tripmapv1.InviteToSharedTripResponse, error) {
	res, err := c.CollaborationService.InviteToSharedTrip(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) RespondSharedTripInvite(ctx context.Context, req *tripmapv1.RespondSharedTripInviteRequest) (*tripmapv1.RespondSharedTripInviteResponse, error) {
	res, err := c.CollaborationService.RespondSharedTripInvite(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) UpdateSharedTrip(ctx context.Context, req *tripmapv1.UpdateSharedTripRequest) (*tripmapv1.UpdateSharedTripResponse, error) {
	res, err := c.CollaborationService.UpdateSharedTrip(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) SetSharedTripMemberRole(ctx context.Context, req *tripmapv1.SetSharedTripMemberRoleRequest) (*tripmapv1.SetSharedTripMemberRoleResponse, error) {
	res, err := c.CollaborationService.SetSharedTripMemberRole(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) RemoveSharedTripMember(ctx context.Context, req *tripmapv1.RemoveSharedTripMemberRequest) (*tripmapv1.RemoveSharedTripMemberResponse, error) {
	res, err := c.CollaborationService.RemoveSharedTripMember(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) LeaveSharedTrip(ctx context.Context, req *tripmapv1.LeaveSharedTripRequest) (*tripmapv1.LeaveSharedTripResponse, error) {
	res, err := c.CollaborationService.LeaveSharedTrip(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) DeleteSharedTrip(ctx context.Context, req *tripmapv1.DeleteSharedTripRequest) (*tripmapv1.DeleteSharedTripResponse, error) {
	res, err := c.CollaborationService.DeleteSharedTrip(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) ListSharedTripMedia(ctx context.Context, req *tripmapv1.ListSharedTripMediaRequest) (*tripmapv1.ListSharedTripMediaResponse, error) {
	res, err := c.CollaborationService.ListSharedTripMedia(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}

func (c collaborationGRPC) ReadSharedTripMedia(ctx context.Context, req *tripmapv1.ReadSharedTripMediaRequest) (*tripmapv1.ReadSharedTripMediaResponse, error) {
	res, err := c.CollaborationService.ReadSharedTripMedia(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}
