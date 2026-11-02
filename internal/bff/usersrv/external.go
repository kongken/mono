package usersrv

import (
	"context"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
)

// These requests contain exclusively BFF-owned bindings. The browser-facing
// handler constructs them from durable state, never from browser proof fields.
func (c *Client) BeginExternal(ctx context.Context, uid string, req *usersrvv1.BeginExternalLoginRequest) (*usersrvv1.BeginExternalLoginResponse, error) {
	ctx = anonymous(ctx)
	if uid != "" {
		ctx = asUser(ctx, uid)
	}
	res, err := c.external.BeginExternalLogin(ctx, req)
	return res, MapError(err)
}
func (c *Client) CompleteExternal(ctx context.Context, uid string, req *usersrvv1.CompleteExternalLoginRequest) (*usersrvv1.CompleteExternalLoginResponse, error) {
	ctx = anonymous(ctx)
	if uid != "" {
		ctx = asUser(ctx, uid)
	}
	res, err := c.external.CompleteExternalLogin(ctx, req)
	return res, MapError(err)
}
func (c *Client) ExternalRegistration(ctx context.Context, ref *usersrvv1.ExternalChallengeReference) (*usersrvv1.ExternalPending, error) {
	res, err := c.external.GetExternalRegistration(anonymous(ctx), &usersrvv1.GetExternalRegistrationRequest{Challenge: ref})
	return res.GetPending(), MapError(err)
}
func (c *Client) RequestExternalEmail(ctx context.Context, ref *usersrvv1.ExternalChallengeReference, email string) error {
	_, err := c.external.RequestExternalRegistrationEmail(anonymous(ctx), &usersrvv1.RequestExternalRegistrationEmailRequest{Challenge: ref, Email: email})
	return MapError(err)
}
func (c *Client) ConfirmExternalEmail(ctx context.Context, id, token string) error {
	_, err := c.external.ConfirmExternalRegistrationEmail(anonymous(ctx), &usersrvv1.ConfirmExternalRegistrationEmailRequest{ChallengeId: id, Token: token})
	return MapError(err)
}
func (c *Client) CreateExternalAccount(ctx context.Context, req *usersrvv1.CompleteExternalRegistrationRequest) (string, error) {
	res, err := c.external.CompleteExternalRegistration(anonymous(ctx), req)
	return res.GetCreatedUserId(), MapError(err)
}
