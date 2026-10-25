package application

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/identity"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// UserAdminService implements usersrv.v1.UserAdminService: the user management
// queries and the four narrow account changes. The rpcauth interceptors have already
// refused any credential without the declared role; the account core checks the role
// again against the database, inside the transaction that reads or writes and writes
// the audit row. The operator of a change is the credential's sub and nothing in the
// request body.
type UserAdminService struct {
	usersrvv1.UnimplementedUserAdminServiceServer
	accounts *account.Service
}

func NewUserAdminService(accounts *account.Service) *UserAdminService {
	return &UserAdminService{accounts: accounts}
}

func (s *UserAdminService) ListUsers(ctx context.Context, req *usersrvv1.ListUsersRequest) (*usersrvv1.ListUsersResponse, error) {
	actor, requestID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	var after *account.UserCursor
	if tok := req.GetPageToken(); tok != "" {
		if after, err = decodeUserCursor(tok); err != nil {
			return nil, toStatus("list_users", err)
		}
	}
	filter := account.UserFilter{
		Email: req.GetEmail(), UserID: req.GetUserId(), Nickname: req.GetNickname(),
		Status: accountStatus(req.GetStatus()), Role: req.GetRole(),
	}
	if req.GetStatus() != usersrvv1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED && filter.Status == "" {
		return nil, toStatus("list_users", &identity.ValidationError{Field: "status", Reason: "not a known status"})
	}
	page, err := s.accounts.ListUsers(ctx, actor, requestID, filter, int(req.GetPageSize()), after)
	if err != nil {
		return nil, toStatus("list_users", err)
	}
	out := &usersrvv1.ListUsersResponse{Users: make([]*usersrvv1.AdminUser, 0, len(page.Users))}
	for _, u := range page.Users {
		out.Users = append(out.Users, protoAdminUser(u))
	}
	if page.Next != nil {
		out.NextPageToken = encodeUserCursor(*page.Next)
	}
	return out, nil
}

func (s *UserAdminService) GetUser(ctx context.Context, req *usersrvv1.GetUserRequest) (*usersrvv1.GetUserResponse, error) {
	actor, requestID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, toStatus("get_user", &identity.ValidationError{Field: "user_id", Reason: "not a UUID"})
	}
	u, err := s.accounts.GetUser(ctx, actor, requestID, id)
	if err != nil {
		return nil, toStatus("get_user", err)
	}
	return &usersrvv1.GetUserResponse{User: protoAdminUser(u)}, nil
}

func (s *UserAdminService) DisableUser(ctx context.Context, req *usersrvv1.ChangeUserRequest) (*usersrvv1.ChangeUserResponse, error) {
	return s.change(ctx, account.ActionDisable, req)
}

func (s *UserAdminService) EnableUser(ctx context.Context, req *usersrvv1.ChangeUserRequest) (*usersrvv1.ChangeUserResponse, error) {
	return s.change(ctx, account.ActionEnable, req)
}

func (s *UserAdminService) GrantMetroAdmin(ctx context.Context, req *usersrvv1.ChangeUserRequest) (*usersrvv1.ChangeUserResponse, error) {
	return s.change(ctx, account.ActionGrantMetroAdmin, req)
}

func (s *UserAdminService) RevokeMetroAdmin(ctx context.Context, req *usersrvv1.ChangeUserRequest) (*usersrvv1.ChangeUserResponse, error) {
	return s.change(ctx, account.ActionRevokeMetroAdmin, req)
}

func (s *UserAdminService) change(ctx context.Context, action account.AdminAction, req *usersrvv1.ChangeUserRequest) (*usersrvv1.ChangeUserResponse, error) {
	op := "admin_" + string(action)
	operator, requestID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	target, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, toStatus(op, &identity.ValidationError{Field: "user_id", Reason: "not a UUID"})
	}
	res, err := s.accounts.ApplyAdminChange(ctx, account.AdminChange{
		Operator: operator, Action: action, Target: target, ExpectedVersion: req.GetExpectedAuthVersion(),
		MutationID: req.GetMutationId(), Reason: req.GetReason(), RequestID: requestID,
	})
	if err != nil {
		return nil, toStatus(op, err)
	}
	return &usersrvv1.ChangeUserResponse{
		UserId: target.String(), Status: protoStatus(res.Status), Roles: res.Roles,
		AuthVersion: res.AuthVersion, Changed: res.Changed, Replayed: res.Replayed,
	}, nil
}

func accountStatus(s usersrvv1.AccountStatus) account.Status {
	switch s {
	case usersrvv1.AccountStatus_ACCOUNT_STATUS_ACTIVE:
		return account.StatusActive
	case usersrvv1.AccountStatus_ACCOUNT_STATUS_DISABLED:
		return account.StatusDisabled
	case usersrvv1.AccountStatus_ACCOUNT_STATUS_DELETING:
		return account.StatusDeleting
	}
	return ""
}

func protoAdminUser(u account.AdminUser) *usersrvv1.AdminUser {
	return &usersrvv1.AdminUser{
		UserId: u.ID.String(), Email: u.Email, DisplayName: u.DisplayName,
		Status: protoStatus(u.Status), Roles: u.Roles, CreatedAt: timestamppb.New(u.CreatedAt), AuthVersion: u.AuthVersion,
	}
}

// The page token is an opaque position, "<unix microseconds>.<uuid>" in base64url.
// It carries no authority: a forged one only moves the reader to another position
// of a list it may read anyway.
func encodeUserCursor(c account.UserCursor) string {
	raw := strconv.FormatInt(c.CreatedAt.UnixMicro(), 10) + "." + c.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeUserCursor(tok string) (*account.UserCursor, error) {
	bad := &identity.ValidationError{Field: "page_token", Reason: "not a valid page token"}
	if len(tok) > 128 {
		return nil, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return nil, bad
	}
	micros, idText, ok := strings.Cut(string(raw), ".")
	if !ok {
		return nil, bad
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return nil, bad
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		return nil, bad
	}
	return &account.UserCursor{CreatedAt: time.UnixMicro(n).UTC(), ID: id}, nil
}
