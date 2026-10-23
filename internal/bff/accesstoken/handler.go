package accesstoken

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler serves bff.v1.AccessTokenService. The ingress interceptor has already
// required a session with the metro-admin role (the methods declare it), so the
// handler only needs the caller's identity.
type Handler struct{ svc *Service }

// NewHandler returns the browser-facing handler over svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

var errUnauth = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))

func (h *Handler) CreateAccessToken(ctx context.Context, req *connect.Request[bffv1.CreateAccessTokenRequest]) (*connect.Response[bffv1.CreateAccessTokenResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	row, secret, err := h.svc.Create(ctx, bs, CreateInput{
		Name: req.Msg.GetName(), Audience: req.Msg.GetAudience(),
		Scopes: req.Msg.GetScopes(), ExpiresInDays: int(req.Msg.GetExpiresInDays()),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.CreateAccessTokenResponse{Token: toProto(row), Secret: secret}), nil
}

func (h *Handler) ListAccessTokens(ctx context.Context, req *connect.Request[bffv1.ListAccessTokensRequest]) (*connect.Response[bffv1.ListAccessTokensResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	rows, err := h.svc.List(ctx, bs.UserID, req.Msg.GetAllUsers())
	if err != nil {
		return nil, err
	}
	res := &bffv1.ListAccessTokensResponse{Tokens: make([]*bffv1.AccessToken, 0, len(rows))}
	for _, r := range rows {
		res.Tokens = append(res.Tokens, toProto(r))
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) RevokeAccessToken(ctx context.Context, req *connect.Request[bffv1.RevokeAccessTokenRequest]) (*connect.Response[bffv1.RevokeAccessTokenResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	if req.Msg.GetId() == "" {
		return nil, invalid("id is required")
	}
	if err := h.svc.Revoke(ctx, bs.UserID, req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.RevokeAccessTokenResponse{}), nil
}

func toProto(r *ent.AccessToken) *bffv1.AccessToken {
	out := &bffv1.AccessToken{
		Id: r.ID, UserId: r.UserID, Name: r.Name, Audience: r.Audience, Scopes: r.Scopes,
		CreatedAt: timestamppb.New(r.CreatedAt), ExpiresAt: timestamppb.New(r.ExpiresAt),
	}
	if r.LastUsedAt != nil {
		out.LastUsedAt = timestamppb.New(*r.LastUsedAt)
	}
	if r.RevokedAt != nil {
		out.RevokedAt = timestamppb.New(*r.RevokedAt)
	}
	return out
}
