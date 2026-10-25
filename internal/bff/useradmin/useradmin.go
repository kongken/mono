// Package useradmin serves bff.v1.UserAdminService: the user section of the unified
// admin console. It only adapts the internal usersrv contract for the browser; it
// reads no account storage of its own and decides nothing about who an account is.
//
// By the time a call arrives here the ingress has already applied the admin
// perimeter (network and Origin), authenticated the session and required the
// platform role. This package checks the role again, limits each operator's call
// rate, and writes a redacted log line per call. usersrv checks the role a third
// time and keeps the audit record that matters.
//
// The four writes add what the browser cannot be trusted with: the operator and the
// time of the password check come from the session record (never from a request field),
// the password must have been checked within SensitiveAuthMaxAge or the write is refused
// without being forwarded (and never replayed), and each operator has a separate, lower
// limit. CSRF and the console Origin were enforced by the ingress before this code ran.
// usersrv decides the target constraints (protected accounts, deleting accounts, the
// version, the idempotency receipt) on its own.
package useradmin

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	bffauth "github.com/kongken/mono/internal/bff/auth"
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
	DisableUser(ctx context.Context, operatorID string, in usersrv.UserChange) (usersrv.ChangeResult, error)
	EnableUser(ctx context.Context, operatorID string, in usersrv.UserChange) (usersrv.ChangeResult, error)
	GrantMetroAdmin(ctx context.Context, operatorID string, in usersrv.UserChange) (usersrv.ChangeResult, error)
	RevokeMetroAdmin(ctx context.Context, operatorID string, in usersrv.UserChange) (usersrv.ChangeResult, error)
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
	writes  int64
	now     func() time.Time
}

var _ bffv1connect.UserAdminServiceHandler = (*Handler)(nil)

// New returns the handler; perMinute is the read limit and writesPerMinute the
// account-change limit of one operator.
func New(dir Directory, limiter Limiter, perMinute, writesPerMinute int) *Handler {
	return &Handler{dir: dir, limiter: limiter, perMin: int64(perMinute), writes: int64(writesPerMinute), now: time.Now}
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
	bs, err := h.administrator(ctx)
	if err != nil {
		return "", err
	}
	if err := h.count(ctx, "admin:users:"+bs.UserID, h.perMin); err != nil {
		return "", err
	}
	return bs.UserID, nil
}

// administrator is the verified session of the call, which must hold the platform role.
func (h *Handler) administrator(ctx context.Context) (session.BrowserSession, error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok || bs.UserID == "" {
		return session.BrowserSession{}, errUnauth
	}
	if !bs.HasRole(Role) {
		return session.BrowserSession{}, errForbidden
	}
	return bs, nil
}

func (h *Handler) count(ctx context.Context, key string, limit int64) error {
	allowed, err := h.limiter.Allow(ctx, key, limit, time.Minute)
	if err != nil {
		slog.Warn("admin user limiter failed", "error", err)
		return errLimiter
	}
	if !allowed {
		return errBusy
	}
	return nil
}

// writer returns the operator of an account change. The password must have been checked
// on this session within SensitiveAuthMaxAge, by the session's own record of it: an
// older check is refused before anything is counted or forwarded.
func (h *Handler) writer(ctx context.Context) (string, error) {
	bs, err := h.administrator(ctx)
	if err != nil {
		return "", err
	}
	if h.now().Sub(bs.AuthenticatedAt) > session.SensitiveAuthMaxAge {
		return "", bffauth.ErrReauthRequired
	}
	if err := h.count(ctx, "admin:users:write:"+bs.UserID, h.writes); err != nil {
		return "", err
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

func (h *Handler) DisableUser(ctx context.Context, req *connect.Request[bffv1.ChangeUserRequest]) (*connect.Response[bffv1.ChangeUserResponse], error) {
	return h.change(ctx, "disable", req.Msg, h.dir.DisableUser)
}

func (h *Handler) EnableUser(ctx context.Context, req *connect.Request[bffv1.ChangeUserRequest]) (*connect.Response[bffv1.ChangeUserResponse], error) {
	return h.change(ctx, "enable", req.Msg, h.dir.EnableUser)
}

func (h *Handler) GrantMetroAdmin(ctx context.Context, req *connect.Request[bffv1.ChangeUserRequest]) (*connect.Response[bffv1.ChangeUserResponse], error) {
	return h.change(ctx, "grant_metro_admin", req.Msg, h.dir.GrantMetroAdmin)
}

func (h *Handler) RevokeMetroAdmin(ctx context.Context, req *connect.Request[bffv1.ChangeUserRequest]) (*connect.Response[bffv1.ChangeUserResponse], error) {
	return h.change(ctx, "revoke_metro_admin", req.Msg, h.dir.RevokeMetroAdmin)
}

func (h *Handler) change(ctx context.Context, action string, m *bffv1.ChangeUserRequest,
	call func(context.Context, string, usersrv.UserChange) (usersrv.ChangeResult, error)) (*connect.Response[bffv1.ChangeUserResponse], error) {
	operator, err := h.writer(ctx)
	if err != nil {
		return nil, err
	}
	res, err := call(ctx, operator, usersrv.UserChange{
		UserID: m.GetUserId(), ExpectedAuthVersion: m.GetExpectedAuthVersion(), MutationID: m.GetMutationId(), Reason: m.GetReason()})
	recordChange(ctx, operator, action, m, res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.ChangeUserResponse{UserId: res.UserID, Status: statusOut(res.Status), Roles: res.Roles,
		AuthVersion: res.AuthVersion, Changed: res.Changed, Replayed: res.Replayed}), nil
}

// recordChange is the BFF's log line of a change: operator, request, action, target and key,
// the result and whether anything changed. The reason text is not logged here; usersrv keeps
// it in the audit record.
func recordChange(ctx context.Context, operator, action string, m *bffv1.ChangeUserRequest, res usersrv.ChangeResult, err error) {
	result := "ok"
	if err != nil {
		result = connect.CodeOf(err).String()
	}
	slog.Info("admin user change", "request_id", rpcauth.CallFrom(ctx).RequestID, "operator", operator, "action", action,
		"target", m.GetUserId(), "mutation_id", m.GetMutationId(), "result", result, "changed", res.Changed, "replayed", res.Replayed)
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
		Roles: u.Roles, CreatedAt: timestamppb.New(u.CreatedAt), AuthVersion: u.AuthVersion}
}
