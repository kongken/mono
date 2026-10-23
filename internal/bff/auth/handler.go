package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

type Handler struct {
	svc           *Service
	csrfCookieTTL time.Duration
}

func NewHandler(svc *Service, _ []string, csrfTTL time.Duration) *Handler {
	if csrfTTL <= 0 {
		csrfTTL = 24 * time.Hour
	}
	return &Handler{svc: svc, csrfCookieTTL: csrfTTL}
}

func (h *Handler) Register(ctx context.Context, req *connect.Request[bffv1.RegisterRequest]) (*connect.Response[bffv1.RegisterResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("password accounts are retired; use Logto"))
}

func (h *Handler) Login(ctx context.Context, req *connect.Request[bffv1.LoginRequest]) (*connect.Response[bffv1.LoginResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("password accounts are retired; use Logto"))
}

func (h *Handler) Logout(ctx context.Context, req *connect.Request[bffv1.LogoutRequest]) (*connect.Response[bffv1.LogoutResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	if err := h.svc.Logout(ctx, bs.SessionID); err != nil {
		return nil, err
	}
	res := connect.NewResponse(&bffv1.LogoutResponse{})
	session.ClearSessionCookie(res.Header(), h.svc.cookies)
	return res, nil
}

func (h *Handler) DeleteAccount(ctx context.Context, req *connect.Request[bffv1.DeleteAccountRequest]) (*connect.Response[bffv1.DeleteAccountResponse], error) {
	token := session.TokenFromRequest(req.Header())
	if h.svc.CompletionReceiptValid(ctx, token) {
		res := connect.NewResponse(&bffv1.DeleteAccountResponse{})
		session.ClearSessionCookie(res.Header(), h.svc.cookies)
		return res, nil
	}
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	if err := h.svc.DeleteAccount(ctx, bs, req.Msg); err != nil {
		return nil, err
	}
	res := connect.NewResponse(&bffv1.DeleteAccountResponse{})
	session.ClearSessionCookie(res.Header(), h.svc.cookies)
	return res, nil
}

func (h *Handler) Me(ctx context.Context, _ *connect.Request[bffv1.MeRequest]) (*connect.Response[bffv1.MeResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	msg, err := h.svc.Me(ctx, bs.UserID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(msg), nil
}

func (h *Handler) ListSessions(ctx context.Context, _ *connect.Request[bffv1.ListSessionsRequest]) (*connect.Response[bffv1.ListSessionsResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	msg, err := h.svc.ListSessions(ctx, bs.UserID, bs.SessionID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(msg), nil
}

func (h *Handler) RevokeSession(ctx context.Context, req *connect.Request[bffv1.RevokeSessionRequest]) (*connect.Response[bffv1.RevokeSessionResponse], error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return nil, errUnauth
	}
	if err := h.svc.RevokeSession(ctx, bs.UserID, req.Msg.GetId()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.RevokeSessionResponse{}), nil
}

func (h *Handler) ListOAuthProviders(ctx context.Context, _ *connect.Request[bffv1.ListOAuthProvidersRequest]) (*connect.Response[bffv1.ListOAuthProvidersResponse], error) {
	msg, err := h.svc.ListOAuthProviders(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(msg), nil
}

func (h *Handler) BeginOAuthFlow(ctx context.Context, req *connect.Request[bffv1.BeginOAuthFlowRequest]) (*connect.Response[bffv1.BeginOAuthFlowResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("use GET /auth/login"))
}

func (h *Handler) CompleteOAuthFlow(ctx context.Context, req *connect.Request[bffv1.CompleteOAuthFlowRequest]) (*connect.Response[bffv1.CompleteOAuthFlowResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("use BFF HTTP callback"))
}

func (h *Handler) IssueCSRFToken(_ context.Context, req *connect.Request[bffv1.IssueCSRFTokenRequest]) (*connect.Response[bffv1.IssueCSRFTokenResponse], error) {
	token, err := h.svc.IssueCSRF()
	if err != nil {
		return nil, err
	}
	expires := time.Now().UTC().Add(h.csrfCookieTTL)
	res := connect.NewResponse(&bffv1.IssueCSRFTokenResponse{Token: token})
	session.SetCSRFCookie(res.Header(), h.svc.cookies, token, expires)
	return res, nil
}

// Authenticate implements ingress session resolution.
func (h *Handler) Authenticate(ctx context.Context, header http.Header) (session.BrowserSession, error) {
	return h.svc.ResolveSession(ctx, session.TokenFromRequest(header))
}
