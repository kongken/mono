package accesstoken

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Origins are the two registered browser origins AccessTokenService answers, each
// under its own contract. The service has two source policies that the browser
// never chooses between: the request's Origin does.
//
//   - OzID is account self-service: any active account owner manages its own
//     Timeline tokens (audience timeline) and nothing else. It can neither create a
//     metro token nor list or revoke another account's tokens, whatever roles the
//     account holds, so a metro administrator cannot reach metro token management
//     from the user center.
//   - Console is the unified management console: metro token administration for
//     metro-admin callers behind the admin perimeter (source allowlist, exact Origin).
//     The role itself is demanded by ingress.Policy.RequiredRole before this handler
//     runs, and again here.
type Origins struct {
	OzID    string
	Console string
}

// Handler serves bff.v1.AccessTokenService.
type Handler struct {
	svc     *Service
	origins Origins
}

// NewHandler returns the browser-facing handler over svc.
func NewHandler(svc *Service, origins Origins) *Handler { return &Handler{svc: svc, origins: origins} }

type surface int

const (
	surfaceNone surface = iota
	surfaceSelfService
	surfaceConsole
)

// RequiredRole is the role ingress must demand of a call with header: the console
// surface is metro administration; the self-service surface asks none (its
// limits are enforced by the handler on what it may touch).
func (o Origins) RequiredRole(header http.Header) string {
	if o.surface(header) == surfaceConsole {
		return RoleMetroAdmin
	}
	return ""
}

// RoleMetroAdmin is the platform role that administers metro and its tokens.
const RoleMetroAdmin = "metro-admin"

// IsConsole and IsSelfService report which registered surface a request's headers
// name; a request naming neither (or several origins) is neither.
func (o Origins) IsConsole(header http.Header) bool { return o.surface(header) == surfaceConsole }
func (o Origins) IsSelfService(header http.Header) bool {
	return o.surface(header) == surfaceSelfService
}

func (o Origins) surface(header http.Header) surface {
	values := header.Values("Origin")
	if len(values) != 1 {
		return surfaceNone
	}
	switch origin := values[0]; {
	case origin != "" && origin == o.Console:
		return surfaceConsole
	case origin != "" && origin == o.OzID:
		return surfaceSelfService
	}
	return surfaceNone
}

var (
	errUnauth   = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))
	errRejected = connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
)

func (h *Handler) CreateAccessToken(ctx context.Context, req *connect.Request[bffv1.CreateAccessTokenRequest]) (*connect.Response[bffv1.CreateAccessTokenResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	audience := req.Msg.GetAudience()
	switch h.origins.surface(req.Header()) {
	case surfaceConsole:
		if !bs.HasRole(RoleMetroAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("metro-admin role required"))
		}
		// The console administers metro: an unnamed audience keeps meaning metro.
	case surfaceSelfService:
		// Self-service is Timeline only. An unnamed audience is Timeline whatever
		// roles the account holds; a metro token is never issued from here.
		if audience == "" {
			audience = AudienceTimeline
		}
		if audience != AudienceTimeline {
			return nil, errRejected
		}
	default:
		return nil, errRejected
	}
	row, secret, err := h.svc.Create(ctx, bs, CreateInput{
		Name: req.Msg.GetName(), Audience: audience,
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
	var (
		rows []*ent.AccessToken
		err  error
	)
	switch h.origins.surface(req.Header()) {
	case surfaceConsole:
		if !bs.HasRole(RoleMetroAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("metro-admin role required"))
		}
		rows, err = h.svc.List(ctx, bs.UserID, req.Msg.GetAllUsers())
	case surfaceSelfService:
		// Only the caller's own Timeline tokens: never another account's, and not the
		// metro tokens a metro administrator holds.
		if req.Msg.GetAllUsers() {
			return nil, errRejected
		}
		rows, err = h.svc.ListOwn(ctx, bs.UserID, AudienceTimeline)
	default:
		return nil, errRejected
	}
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
	var err error
	switch h.origins.surface(req.Header()) {
	case surfaceConsole:
		if !bs.HasRole(RoleMetroAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("metro-admin role required"))
		}
		err = h.svc.RevokeAny(ctx, bs.UserID, req.Msg.GetId())
	case surfaceSelfService:
		err = h.svc.RevokeOwn(ctx, bs.UserID, req.Msg.GetId(), AudienceTimeline)
	default:
		return nil, errRejected
	}
	if err != nil {
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
