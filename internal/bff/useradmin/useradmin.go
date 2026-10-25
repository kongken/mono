// Package useradmin serves bff.v1.UserAdminService: the user section of the unified
// admin console. It only adapts the internal usersrv contract for the browser; it
// reads no account storage of its own and decides nothing about who an account is.
//
// By the time a call arrives here the ingress has already applied the admin
// perimeter (network and Origin), authenticated the session and required the
// platform role. This package checks the role again, limits each operator's call
// rate, and writes a redacted log line per call. usersrv checks the role a third
// time and keeps the audit record that matters.
package useradmin

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/usersrv"
	"github.com/kongken/mono/internal/platform/rpcauth"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Role is the platform role every method requires.
const Role = "ozid-admin"

// Directory is the part of the usersrv adapter this service uses.
type Directory interface {
	ListUsers(ctx context.Context, operatorID string, q usersrv.UserQuery) ([]usersrv.AdminUser, string, error)
	GetUser(ctx context.Context, operatorID, userID string) (usersrv.AdminUser, error)
}

// Limiter counts calls in a window; *ratelimit.Limiter implements it.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error)
}

// Handler implements bffv1connect.UserAdminServiceHandler.
type Handler struct {
	dir     Directory
	limiter Limiter
	perMin  int64
}

var _ bffv1connect.UserAdminServiceHandler = (*Handler)(nil)

// New returns the handler; perMinute is the call limit of one operator.
func New(dir Directory, limiter Limiter, perMinute int) *Handler {
	return &Handler{dir: dir, limiter: limiter, perMin: int64(perMinute)}
}

var (
	errUnauth    = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))
	errForbidden = connect.NewError(connect.CodePermissionDenied, errors.New("required role missing"))
	errBusy      = connect.NewError(connect.CodeResourceExhausted, errors.New("too many requests, try again later"))
	errLimiter   = connect.NewError(connect.CodeUnavailable, errors.New("temporarily unavailable"))
)

// operator returns the verified administrator of the call and counts the call against
// the operator's limit. A failing limiter refuses the call instead of waving it through.
func (h *Handler) operator(ctx context.Context) (string, error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok || bs.UserID == "" {
		return "", errUnauth
	}
	if !bs.HasRole(Role) {
		return "", errForbidden
	}
	allowed, err := h.limiter.Allow(ctx, "admin:users:"+bs.UserID, h.perMin, time.Minute)
	if err != nil {
		slog.Warn("admin user query limiter failed", "error", err)
		return "", errLimiter
	}
	if !allowed {
		return "", errBusy
	}
	return bs.UserID, nil
}

// record writes the BFF's own log line of a query. It names the operator, the request and
// what kind of query it was; it never carries an email address, a nickname or a user ID the
// operator looked for, nor anything from the answer except its size.
func record(ctx context.Context, operator, action, lookup string, count int, err error) {
	result := "ok"
	if err != nil {
		result = connect.CodeOf(err).String()
	}
	slog.Info("admin user query", "request_id", rpcauth.CallFrom(ctx).RequestID, "operator", operator,
		"action", action, "lookup", lookup, "count", count, "result", result)
}

func (h *Handler) ListUsers(ctx context.Context, req *connect.Request[bffv1.ListUsersRequest]) (*connect.Response[bffv1.ListUsersResponse], error) {
	operator, err := h.operator(ctx)
	if err != nil {
		return nil, err
	}
	m := req.Msg
	q := usersrv.UserQuery{
		Email: m.GetEmail(), UserID: m.GetUserId(), Nickname: m.GetNickname(), Role: m.GetRole(),
		Status: statusIn(m.GetStatus()), PageSize: m.GetPageSize(), PageToken: m.GetPageToken(),
	}
	users, next, err := h.dir.ListUsers(ctx, operator, q)
	record(ctx, operator, "list", lookupKind(q), len(users), err)
	if err != nil {
		return nil, err
	}
	out := &bffv1.ListUsersResponse{Users: make([]*bffv1.AdminUser, 0, len(users)), NextPageToken: next}
	for _, u := range users {
		out.Users = append(out.Users, userOut(u))
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) GetUser(ctx context.Context, req *connect.Request[bffv1.GetUserRequest]) (*connect.Response[bffv1.GetUserResponse], error) {
	operator, err := h.operator(ctx)
	if err != nil {
		return nil, err
	}
	u, err := h.dir.GetUser(ctx, operator, req.Msg.GetUserId())
	if errors.Is(err, usersrv.ErrNotFound) {
		err = connect.NewError(connect.CodeNotFound, errors.New("account not found"))
	}
	record(ctx, operator, "get", "user_id", 0, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.GetUserResponse{User: userOut(u)}), nil
}

func lookupKind(q usersrv.UserQuery) string {
	var kinds []string
	switch {
	case q.Email != "":
		kinds = append(kinds, "email")
	case q.UserID != "":
		kinds = append(kinds, "user_id")
	case q.Nickname != "":
		kinds = append(kinds, "nickname")
	}
	if q.Status != usersrv.StatusUnknown {
		kinds = append(kinds, "status")
	}
	if q.Role != "" {
		kinds = append(kinds, "role")
	}
	if len(kinds) == 0 {
		return "none"
	}
	return strings.Join(kinds, "+")
}

func statusIn(s bffv1.UserStatus) usersrv.Status {
	switch s {
	case bffv1.UserStatus_USER_STATUS_ACTIVE:
		return usersrv.StatusActive
	case bffv1.UserStatus_USER_STATUS_DISABLED:
		return usersrv.StatusDisabled
	case bffv1.UserStatus_USER_STATUS_DELETING:
		return usersrv.StatusDeleting
	}
	return usersrv.StatusUnknown
}

func statusOut(s usersrv.Status) bffv1.UserStatus {
	switch s {
	case usersrv.StatusActive:
		return bffv1.UserStatus_USER_STATUS_ACTIVE
	case usersrv.StatusDisabled:
		return bffv1.UserStatus_USER_STATUS_DISABLED
	case usersrv.StatusDeleting:
		return bffv1.UserStatus_USER_STATUS_DELETING
	}
	return bffv1.UserStatus_USER_STATUS_UNSPECIFIED
}

func userOut(u usersrv.AdminUser) *bffv1.AdminUser {
	return &bffv1.AdminUser{UserId: u.UserID, Email: u.Email, DisplayName: u.DisplayName, Status: statusOut(u.Status),
		Roles: u.Roles, CreatedAt: timestamppb.New(u.CreatedAt)}
}
