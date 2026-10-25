package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/application"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var gate = account.Operator{Name: "ops", Reason: "ticket", RequestID: "cli"}

// withRoles is a BFF credential for id that claims roles.
func withRoles(id string, roles ...string) context.Context {
	return rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: id, Roles: roles, RequestID: "req-admin"})
}

// usersrv decides for itself: the credential's claim is necessary (rpcauth) and not sufficient (the
// database must say so now), and neither is skipped because the BFF already checked.
func TestUserAdminChecksTheRoleTwiceAndIndependently(t *testing.T) {
	s := start(t)
	admin := s.newUser(t, "root@example.com")
	plain := s.newUser(t, "plain@example.com")
	if _, err := s.accounts.GrantRole(context.Background(), gate, admin, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	admins := usersrvv1.NewUserAdminServiceClient(s.conn)
	list := func(ctx context.Context) error {
		_, err := admins.ListUsers(ctx, &usersrvv1.ListUsersRequest{})
		return err
	}

	// No role in the credential: refused by rpcauth before any handler runs.
	if err := list(as(admin, "")); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("credential without the role: %v", err)
	}
	// Another role in the credential is not the role.
	if err := list(withRoles(admin.String(), "metro-admin")); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("metro-admin claim: %v", err)
	}
	// A credential that claims the role for an account that does not hold it (a stale or forged
	// claim): the database refuses it.
	if err := list(withRoles(plain.String(), "ozid-admin")); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("claim without the role in the database: %v", err)
	}
	if err := list(withRoles(admin.String(), "ozid-admin")); err != nil {
		t.Fatalf("a real administrator: %v", err)
	}
	// Revoked a moment ago: the credential (signed per call, but carrying a claim made earlier by
	// the session) is refused by the database.
	if _, err := s.accounts.RevokeRole(context.Background(), gate, admin, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	if err := list(withRoles(admin.String(), "ozid-admin")); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("revoked administrator: %v", err)
	}
	// The anonymous credential cannot reach it at all.
	if _, err := admins.GetUser(rpcauth.WithCall(context.Background(), rpcauth.Call{}), &usersrvv1.GetUserRequest{UserId: admin.String()}); status.Code(err) == codes.OK {
		t.Fatal("an anonymous credential read an account")
	}
}

func TestUserAdminAnswersAndTypedErrors(t *testing.T) {
	s := start(t)
	admin := s.newUser(t, "root@example.com")
	target := s.newUser(t, "target@example.com")
	if _, err := s.accounts.GrantRole(context.Background(), gate, admin, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	ctx := withRoles(admin.String(), "ozid-admin")
	admins := usersrvv1.NewUserAdminServiceClient(s.conn)

	got, err := admins.GetUser(ctx, &usersrvv1.GetUserRequest{UserId: target.String()})
	if err != nil || got.User.UserId != target.String() || got.User.Email != "target@example.com" ||
		got.User.Status != usersrvv1.AccountStatus_ACCOUNT_STATUS_ACTIVE || got.User.CreatedAt == nil {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err = admins.GetUser(ctx, &usersrvv1.GetUserRequest{UserId: "not-a-uuid"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("malformed id: %v", err)
	}
	if _, err = admins.GetUser(ctx, &usersrvv1.GetUserRequest{UserId: "00000000-0000-0000-0000-0000000000aa"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown id: %v", err)
	}
	page, err := admins.ListUsers(ctx, &usersrvv1.ListUsersRequest{PageSize: 1})
	if err != nil || len(page.Users) != 1 || page.NextPageToken == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	next, err := admins.ListUsers(ctx, &usersrvv1.ListUsersRequest{PageSize: 1, PageToken: page.NextPageToken})
	if err != nil || len(next.Users) != 1 || next.Users[0].UserId == page.Users[0].UserId || next.NextPageToken != "" {
		t.Fatalf("second page: %+v %v", next, err)
	}
	for _, token := range []string{"x", "!!!", "MTIzLmJhZA", string(make([]byte, 200))} {
		if _, err = admins.ListUsers(ctx, &usersrvv1.ListUsersRequest{PageToken: token}); status.Code(err) != codes.InvalidArgument || reason(t, err) != application.ReasonInvalidArgument {
			t.Errorf("page token %q: %v", token, err)
		}
	}
	if _, err = admins.ListUsers(ctx, &usersrvv1.ListUsersRequest{Status: usersrvv1.AccountStatus(99)}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown status: %v", err)
	}
}

// The platform role guards its account from the ordinary deletion call, over the real RPC.
func TestPlatformAdministratorDeletionIsRefusedOverRPC(t *testing.T) {
	s := start(t)
	admin := s.newUser(t, "root@example.com")
	if _, err := s.accounts.GrantRole(context.Background(), gate, admin, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	_, err := s.account.BeginDeletion(as(admin, ""), &usersrvv1.BeginDeletionRequest{MutationId: "wf-1"})
	if status.Code(err) != codes.FailedPrecondition || reason(t, err) != application.ReasonPlatformAdminProtected {
		t.Fatalf("begin: %v", err)
	}
	st, err := s.account.GetAuthState(as(admin, ""), &usersrvv1.GetAuthStateRequest{})
	if err != nil || st.Status != usersrvv1.AccountStatus_ACCOUNT_STATUS_ACTIVE || len(st.Roles) != 1 || st.Roles[0] != "ozid-admin" {
		t.Fatalf("state after the refusal: %+v %v", st, err)
	}
	if _, err = s.account.FinishDeletion(as(admin, ""), &usersrvv1.FinishDeletionRequest{MutationId: "wf-1"}); status.Code(err) == codes.OK {
		t.Fatal("FinishDeletion removed a platform administrator that never began a deletion")
	}
}

// Every account change is authorised like the reads: by the role in the credential and again by the
// database, and the operator is the credential's account, not anything in the request.
func TestUserAdminWritesCheckTheRoleTwiceAndUseTheCredentialAsOperator(t *testing.T) {
	s := start(t)
	admin := s.newUser(t, "root@example.com")
	plain := s.newUser(t, "plain@example.com")
	target := s.newUser(t, "target@example.com")
	if _, err := s.accounts.GrantRole(context.Background(), gate, admin, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	admins := usersrvv1.NewUserAdminServiceClient(s.conn)
	calls := map[string]func(context.Context, *usersrvv1.ChangeUserRequest) error{
		"DisableUser": func(c context.Context, r *usersrvv1.ChangeUserRequest) error {
			_, err := admins.DisableUser(c, r)
			return err
		},
		"EnableUser": func(c context.Context, r *usersrvv1.ChangeUserRequest) error {
			_, err := admins.EnableUser(c, r)
			return err
		},
		"GrantMetroAdmin": func(c context.Context, r *usersrvv1.ChangeUserRequest) error {
			_, err := admins.GrantMetroAdmin(c, r)
			return err
		},
		"RevokeMetroAdmin": func(c context.Context, r *usersrvv1.ChangeUserRequest) error {
			_, err := admins.RevokeMetroAdmin(c, r)
			return err
		},
	}
	for name, call := range calls {
		req := func(v int64) *usersrvv1.ChangeUserRequest {
			return &usersrvv1.ChangeUserRequest{UserId: target.String(), ExpectedAuthVersion: v, MutationId: name, Reason: "ticket 7"}
		}
		if err := call(as(admin, ""), req(1)); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s: a credential without the role: %v", name, err)
		}
		if err := call(withRoles(admin.String(), "metro-admin"), req(1)); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s: a metro-admin claim: %v", name, err)
		}
		if err := call(withRoles(plain.String(), "ozid-admin"), req(1)); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s: a claim the database does not back: %v", name, err)
		}
		if err := call(rpcauth.WithCall(context.Background(), rpcauth.Call{}), req(1)); status.Code(err) == codes.OK {
			t.Fatalf("%s: an anonymous credential changed an account", name)
		}
	}
	if st, _ := s.account.GetAuthState(as(target, ""), &usersrvv1.GetAuthStateRequest{}); st.AuthVersion != 1 || len(st.Roles) != 0 {
		t.Fatalf("a refused call changed the account: %+v", st)
	}

	ctx := withRoles(admin.String(), "ozid-admin")
	res, err := admins.DisableUser(ctx, &usersrvv1.ChangeUserRequest{UserId: target.String(), ExpectedAuthVersion: 1, MutationId: "ok-1", Reason: "ticket 7"})
	if err != nil || !res.Changed || res.Replayed || res.AuthVersion != 2 || res.Status != usersrvv1.AccountStatus_ACCOUNT_STATUS_DISABLED || res.UserId != target.String() {
		t.Fatalf("disable: %+v %v", res, err)
	}
	// The same request again is the stored answer.
	again, err := admins.DisableUser(ctx, &usersrvv1.ChangeUserRequest{UserId: target.String(), ExpectedAuthVersion: 1, MutationId: "ok-1", Reason: "ticket 7"})
	if err != nil || !again.Replayed || again.AuthVersion != 2 || !again.Changed {
		t.Fatalf("replay: %+v %v", again, err)
	}
	// The read model carries the version a write must name.
	got, err := admins.GetUser(ctx, &usersrvv1.GetUserRequest{UserId: target.String()})
	if err != nil || got.User.AuthVersion != 2 {
		t.Fatalf("get: %+v %v", got, err)
	}
}

func TestUserAdminWriteErrorsAreTyped(t *testing.T) {
	s := start(t)
	admin := s.newUser(t, "root@example.com")
	other := s.newUser(t, "root2@example.com")
	target := s.newUser(t, "target@example.com")
	for _, id := range []uuid.UUID{admin, other} {
		if _, err := s.accounts.GrantRole(context.Background(), gate, id, account.RolePlatformAdmin); err != nil {
			t.Fatal(err)
		}
	}
	ctx := withRoles(admin.String(), "ozid-admin")
	admins := usersrvv1.NewUserAdminServiceClient(s.conn)
	disable := func(id string, version int64, key, why string) error {
		_, err := admins.DisableUser(ctx, &usersrvv1.ChangeUserRequest{UserId: id, ExpectedAuthVersion: version, MutationId: key, Reason: why})
		return err
	}
	check := func(name string, err error, code codes.Code, why string) {
		t.Helper()
		if status.Code(err) != code || (why != "" && reason(t, err) != why) {
			t.Errorf("%s: %v (reason %q)", name, err, why)
		}
	}
	check("a platform administrator", disable(other.String(), 2, "e-1", "ticket"), codes.FailedPrecondition, application.ReasonTargetProtected)
	check("a stale version", disable(target.String(), 7, "e-2", "ticket"), codes.Aborted, application.ReasonTargetChanged)
	check("a missing account", disable("00000000-0000-0000-0000-0000000000aa", 1, "e-3", "ticket"), codes.NotFound, "")
	check("a malformed id", disable("nope", 1, "e-4", "ticket"), codes.InvalidArgument, application.ReasonInvalidArgument)
	check("no reason", disable(target.String(), 1, "e-5", ""), codes.InvalidArgument, application.ReasonInvalidArgument)
	check("an email in the reason", disable(target.String(), 1, "e-6", "ask bob@example.com"), codes.InvalidArgument, application.ReasonInvalidArgument)
	check("no key", disable(target.String(), 1, "", "ticket"), codes.InvalidArgument, application.ReasonInvalidArgument)
	check("no version", disable(target.String(), 0, "e-7", "ticket"), codes.InvalidArgument, application.ReasonInvalidArgument)
	check("the first one", disable(target.String(), 1, "e-8", "ticket"), codes.OK, "")
	check("the key for something else", disable(target.String(), 2, "e-8", "ticket"), codes.AlreadyExists, application.ReasonMutationReused)
	st, _ := s.account.GetAuthState(as(target, ""), &usersrvv1.GetAuthStateRequest{})
	if st.AuthVersion != 2 || st.Status != usersrvv1.AccountStatus_ACCOUNT_STATUS_DISABLED {
		t.Fatalf("state: %+v", st)
	}
}
