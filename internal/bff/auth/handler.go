package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/platform/httpx"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

type Handler struct {
	svc             *Service
	allowedOrigins  []string
	csrfCookieTTL   time.Duration
}

func NewHandler(svc *Service, allowedOrigins []string, csrfTTL time.Duration) *Handler {
	if csrfTTL <= 0 {
		csrfTTL = 24 * time.Hour
	}
	return &Handler{svc: svc, allowedOrigins: allowedOrigins, csrfCookieTTL: csrfTTL}
}

func (h *Handler) Register(ctx context.Context, req *connect.Request[bffv1.RegisterRequest]) (*connect.Response[bffv1.RegisterResponse], error) {
	if err := session.CheckWrite(req.Header(), h.allowedOrigins, req.Header().Get("Host")); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
	}
	msg, expires, token, err := h.svc.Register(ctx, httpx.ClientAddr("", req.Header(), req.Peer().Addr), req.Msg)
	if err != nil {
		return nil, err
	}
	res := connect.NewResponse(msg)
	session.SetSessionCookie(res.Header(), h.svc.cookies, token, expires)
	return res, nil
}

func (h *Handler) Login(ctx context.Context, req *connect.Request[bffv1.LoginRequest]) (*connect.Response[bffv1.LoginResponse], error) {
	if err := session.CheckWrite(req.Header(), h.allowedOrigins, req.Header().Get("Host")); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
	}
	msg, expires, token, err := h.svc.Login(ctx, httpx.ClientAddr("", req.Header(), req.Peer().Addr), req.Msg)
	if err != nil {
		return nil, err
	}
	res := connect.NewResponse(msg)
	session.SetSessionCookie(res.Header(), h.svc.cookies, token, expires)
	return res, nil
}

func (h *Handler) Logout(ctx context.Context, req *connect.Request[bffv1.LogoutRequest]) (*connect.Response[bffv1.LogoutResponse], error) {
	if err := session.CheckWrite(req.Header(), h.allowedOrigins, req.Header().Get("Host")); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
	}
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

func (h *Handler) IssueCSRFToken(_ context.Context, req *connect.Request[bffv1.IssueCSRFTokenRequest]) (*connect.Response[bffv1.IssueCSRFTokenResponse], error) {
	token, err := h.svc.IssueCSRF()
	if err != nil {
		return nil, err
	}
	expires := time.Now().UTC().Add(h.csrfCookieTTL)
	res := connect.NewResponse(&bffv1.IssueCSRFTokenResponse{Token: token})
	session.SetCSRFCookie(res.Header(), h.svc.cookies, token, expires)
	// Allow subsequent cross-origin POSTs when configured.
	res.Header().Set("Vary", "Origin")
	if origin := req.Header().Get("Origin"); origin != "" {
		for _, o := range h.allowedOrigins {
			if o == origin {
				res.Header().Set("Access-Control-Allow-Origin", origin)
				res.Header().Set("Access-Control-Allow-Credentials", "true")
				break
			}
		}
	}
	return res, nil
}

// Authenticate implements ingress session resolution.
func (h *Handler) Authenticate(ctx context.Context, header http.Header) (session.BrowserSession, error) {
	return h.svc.ResolveSession(ctx, session.TokenFromRequest(header))
}
