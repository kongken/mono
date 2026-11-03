package usersrv

import (
	"context"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
)

func (c *Client) GetExternalIdentities(ctx context.Context, uid string) (*usersrvv1.GetExternalIdentitiesResponse, error) {
	res, err := c.external.GetExternalIdentities(asUser(ctx, uid), &usersrvv1.GetExternalIdentitiesRequest{})
	return res, MapError(err)
}
func (c *Client) BeginIdentityOperation(ctx context.Context, uid string, req *usersrvv1.BeginIdentityOperationRequest) (*usersrvv1.BeginIdentityOperationResponse, error) {
	res, err := c.external.BeginIdentityOperation(asUser(ctx, uid), req)
	return res, MapError(err)
}
func (c *Client) ConfirmIdentityOperation(ctx context.Context, req *usersrvv1.ConfirmIdentityOperationRequest) error {
	_, err := c.external.ConfirmIdentityOperation(anonymous(ctx), req)
	return MapError(err)
}
func (c *Client) GetIdentityOperation(ctx context.Context, uid string, req *usersrvv1.GetIdentityOperationRequest) (*usersrvv1.GetIdentityOperationResponse, error) {
	res, err := c.external.GetIdentityOperation(asUser(ctx, uid), req)
	return res, MapError(err)
}
func (c *Client) LinkExternalIdentity(ctx context.Context, uid string, req *usersrvv1.LinkExternalIdentityRequest) (*usersrvv1.LinkExternalIdentityResponse, error) {
	res, err := c.external.LinkExternalIdentity(asUser(ctx, uid), req)
	return res, MapError(err)
}
func (c *Client) UnlinkExternalIdentity(ctx context.Context, uid string, req *usersrvv1.UnlinkExternalIdentityRequest) (*usersrvv1.UnlinkExternalIdentityResponse, error) {
	res, err := c.external.UnlinkExternalIdentity(asUser(ctx, uid), req)
	return res, MapError(err)
}
