package application

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"github.com/kongken/mono/pkg/proto/tripmap/v1/tripmapv1connect"
)

var errFriendRPCMoved = connect.NewError(connect.CodeUnimplemented, errors.New("friend RPCs use bff.v1.FriendService"))

// CollaborationConnectSharedOnly serves shared-trip RPCs on the legacy Connect mount;
// friend RPCs were migrated to bff.v1.FriendService.
type CollaborationConnectSharedOnly struct {
	tripmapv1connect.UnimplementedCollaborationServiceHandler
	inner *CollaborationService
}

func NewCollaborationConnectSharedOnly(inner *CollaborationService) *CollaborationConnectSharedOnly {
	return &CollaborationConnectSharedOnly{inner: inner}
}

func (c *CollaborationConnectSharedOnly) GetCollaborationOverview(context.Context, *connect.Request[tripmapv1.GetCollaborationOverviewRequest]) (*connect.Response[tripmapv1.GetCollaborationOverviewResponse], error) {
	return nil, errFriendRPCMoved
}

func (c *CollaborationConnectSharedOnly) UpdateFriendSettings(context.Context, *connect.Request[tripmapv1.UpdateFriendSettingsRequest]) (*connect.Response[tripmapv1.UpdateFriendSettingsResponse], error) {
	return nil, errFriendRPCMoved
}

func (c *CollaborationConnectSharedOnly) SendFriendRequest(context.Context, *connect.Request[tripmapv1.SendFriendRequestRequest]) (*connect.Response[tripmapv1.SendFriendRequestResponse], error) {
	return nil, errFriendRPCMoved
}

func (c *CollaborationConnectSharedOnly) RespondFriendRequest(context.Context, *connect.Request[tripmapv1.RespondFriendRequestRequest]) (*connect.Response[tripmapv1.RespondFriendRequestResponse], error) {
	return nil, errFriendRPCMoved
}

func (c *CollaborationConnectSharedOnly) RemoveFriend(context.Context, *connect.Request[tripmapv1.RemoveFriendRequest]) (*connect.Response[tripmapv1.RemoveFriendResponse], error) {
	return nil, errFriendRPCMoved
}

func (c *CollaborationConnectSharedOnly) GetFriendFootprints(context.Context, *connect.Request[tripmapv1.GetFriendFootprintsRequest]) (*connect.Response[tripmapv1.GetFriendFootprintsResponse], error) {
	return nil, errFriendRPCMoved
}

func (c *CollaborationConnectSharedOnly) InviteToSharedTrip(ctx context.Context, req *connect.Request[tripmapv1.InviteToSharedTripRequest]) (*connect.Response[tripmapv1.InviteToSharedTripResponse], error) {
	return c.inner.InviteToSharedTrip(ctx, req)
}

func (c *CollaborationConnectSharedOnly) RespondSharedTripInvite(ctx context.Context, req *connect.Request[tripmapv1.RespondSharedTripInviteRequest]) (*connect.Response[tripmapv1.RespondSharedTripInviteResponse], error) {
	return c.inner.RespondSharedTripInvite(ctx, req)
}

func (c *CollaborationConnectSharedOnly) UpdateSharedTrip(ctx context.Context, req *connect.Request[tripmapv1.UpdateSharedTripRequest]) (*connect.Response[tripmapv1.UpdateSharedTripResponse], error) {
	return c.inner.UpdateSharedTrip(ctx, req)
}

func (c *CollaborationConnectSharedOnly) SetSharedTripMemberRole(ctx context.Context, req *connect.Request[tripmapv1.SetSharedTripMemberRoleRequest]) (*connect.Response[tripmapv1.SetSharedTripMemberRoleResponse], error) {
	return c.inner.SetSharedTripMemberRole(ctx, req)
}

func (c *CollaborationConnectSharedOnly) RemoveSharedTripMember(ctx context.Context, req *connect.Request[tripmapv1.RemoveSharedTripMemberRequest]) (*connect.Response[tripmapv1.RemoveSharedTripMemberResponse], error) {
	return c.inner.RemoveSharedTripMember(ctx, req)
}

func (c *CollaborationConnectSharedOnly) LeaveSharedTrip(ctx context.Context, req *connect.Request[tripmapv1.LeaveSharedTripRequest]) (*connect.Response[tripmapv1.LeaveSharedTripResponse], error) {
	return c.inner.LeaveSharedTrip(ctx, req)
}

func (c *CollaborationConnectSharedOnly) DeleteSharedTrip(ctx context.Context, req *connect.Request[tripmapv1.DeleteSharedTripRequest]) (*connect.Response[tripmapv1.DeleteSharedTripResponse], error) {
	return c.inner.DeleteSharedTrip(ctx, req)
}

func (c *CollaborationConnectSharedOnly) ListSharedTripMedia(ctx context.Context, req *connect.Request[tripmapv1.ListSharedTripMediaRequest]) (*connect.Response[tripmapv1.ListSharedTripMediaResponse], error) {
	return c.inner.ListSharedTripMedia(ctx, req)
}

func (c *CollaborationConnectSharedOnly) ReadSharedTripMedia(ctx context.Context, req *connect.Request[tripmapv1.ReadSharedTripMediaRequest]) (*connect.Response[tripmapv1.ReadSharedTripMediaResponse], error) {
	return c.inner.ReadSharedTripMedia(ctx, req)
}
