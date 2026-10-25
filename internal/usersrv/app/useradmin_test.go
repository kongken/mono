package app_test

import (
	"context"
	"strings"
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

// ListSecurityAudit over the real RPC: the role is checked twice like every other method, the filters and
// the page token are validated with browser-safe errors that never echo a value, and an entry carries
// identifiers and classifications only.
func TestListSecurityAuditOverRPC(t *testing.T) {
	s := start(t)
	admin := s.newUser(t, "root@example.com")
	target := s.newUser(t, "target@example.com")
	metro := s.newUser(t, "metro@example.com")
	bg := context.Background()
	if _, err := s.accounts.GrantRole(bg, gate, admin, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.accounts.GrantRole(bg, gate, metro, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	admins := usersrvv1.NewUserAdminServiceClient(s.conn)
	ctx := withRoles(admin.String(), "ozid-admin")
	if _, err := admins.DisableUser(ctx, &usersrvv1.ChangeUserRequest{UserId: target.String(), ExpectedAuthVersion: 1, MutationId: "rpc-1", Reason: "ticket 5"}); err != nil {
		t.Fatal(err)
	}
	list := func(c context.Context, r *usersrvv1.ListSecurityAuditRequest) (*usersrvv1.ListSecurityAuditResponse, error) {
		return admins.ListSecurityAudit(c, r)
	}

	// The role is required in the credential and in the database; metro-admin alone is not it.
	for name, c := range map[string]context.Context{
		"no role claim":   as(admin, ""),
		"metro-admin":     withRoles(metro.String(), "metro-admin"),
		"forged claim":    withRoles(target.String(), "ozid-admin"),
		"claim for metro": withRoles(metro.String(), "ozid-admin"),
	} {
		if res, err := list(c, &usersrvv1.ListSecurityAuditRequest{}); status.Code(err) != codes.PermissionDenied || res != nil {
			t.Fatalf("%s: %+v %v", name, res, err)
		}
	}

	got, err := list(ctx, &usersrvv1.ListSecurityAuditRequest{UserId: target.String(), Action: "admin.account.disable", Channel: usersrvv1.AuditChannel_AUDIT_CHANNEL_WEB})
	if err != nil || len(got.Entries) != 1 {
		t.Fatalf("list: %+v %v", got, err)
	}
	e := got.Entries[0]
	if e.OperatorUserId != admin.String() || e.UserId != target.String() || e.Result != "ok" || e.Reason != "ticket 5" || e.MutationId != "rpc-1" ||
		e.BeforeStatus != "active" || e.AfterStatus != "disabled" || e.Channel != usersrvv1.AuditChannel_AUDIT_CHANNEL_WEB || e.RequestId != "req-admin" ||
		e.CreatedAt == nil || e.OperatorLabel != "" || e.OriginId != "" || e.Origin != nil {
		t.Fatalf("entry: %+v", e)
	}
	if text := got.String(); containsAny(text, "@example.com", "argon2") {
		t.Fatalf("the answer holds an address or a hash: %s", text)
	}

	// The CLI's rows are labelled, not attributed to an account.
	cli, err := list(ctx, &usersrvv1.ListSecurityAuditRequest{Channel: usersrvv1.AuditChannel_AUDIT_CHANNEL_CLI, Action: "role.grant:metro-admin"})
	if err != nil || len(cli.Entries) != 1 || cli.Entries[0].OperatorLabel != "ops" || cli.Entries[0].OperatorUserId != "" {
		t.Fatalf("cli rows: %+v %v", cli, err)
	}

	// Paging over the RPC: the token is opaque and bad ones are refused without echoing anything.
	page, err := list(ctx, &usersrvv1.ListSecurityAuditRequest{PageSize: 2})
	if err != nil || len(page.Entries) != 2 || page.NextPageToken == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	next, err := list(ctx, &usersrvv1.ListSecurityAuditRequest{PageSize: 2, PageToken: page.NextPageToken})
	if err != nil || len(next.Entries) == 0 || next.Entries[0].Id == page.Entries[0].Id || next.Entries[0].Id == page.Entries[1].Id {
		t.Fatalf("second page: %+v %v", next, err)
	}
	for _, token := range []string{"x", "!!!", "MTIzLmJhZA", string(make([]byte, 200))} {
		if _, err = list(ctx, &usersrvv1.ListSecurityAuditRequest{PageToken: token}); status.Code(err) != codes.InvalidArgument || reason(t, err) != application.ReasonInvalidArgument {
			t.Errorf("page token %q: %v", token, err)
		}
	}
	secret := "secret-key-value"
	for name, r := range map[string]*usersrvv1.ListSecurityAuditRequest{
		"user id":   {UserId: "not-a-uuid-" + secret},
		"channel":   {Channel: usersrvv1.AuditChannel(42)},
		"action":    {Action: "NOT OK " + secret},
		"mutation":  {MutationId: "bad key " + secret},
		"page size": {PageSize: -1},
	} {
		_, err := list(ctx, r)
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
		if containsAny(status.Convert(err).Message(), secret) {
			t.Fatalf("%s: the error echoes the value: %v", name, err)
		}
	}
	// Revoked a moment ago: the claim made earlier no longer reads anything.
	if _, err = s.accounts.RevokeRole(bg, gate, admin, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	if res, err := list(ctx, &usersrvv1.ListSecurityAuditRequest{}); status.Code(err) != codes.PermissionDenied || res != nil {
		t.Fatalf("revoked administrator: %+v %v", res, err)
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
