package account_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	"github.com/kongken/mono/internal/usersrv/identity"
)

func platformAdmin(t *testing.T, s *account.Service, email string) uuid.UUID {
	t.Helper()
	id := create(t, s, email)
	if _, err := s.GrantRole(ctx, op, id, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPlatformRoleIsGrantedOnlyByTheOperatorAndIsIndependent(t *testing.T) {
	s, _ := setup(t)
	first := create(t, s, "first@example.com")
	if st, _ := s.GetAuthState(ctx, first); len(st.Roles) != 0 {
		t.Fatalf("the first account was given a role: %+v", st)
	}
	r, err := s.GrantRole(ctx, op, first, account.RolePlatformAdmin)
	if err != nil || !r.Changed || r.AuthVersion != 2 || len(r.Roles) != 1 || r.Roles[0] != "ozid-admin" {
		t.Fatalf("grant: %+v %v", r, err)
	}
	// The two roles are independent: holding one never shows up as the other, and
	// revoking one leaves the other.
	if r, err = s.GrantRole(ctx, op, first, account.RoleMetroAdmin); err != nil || len(r.Roles) != 2 {
		t.Fatalf("both: %+v %v", r, err)
	}
	if r, err = s.RevokeRole(ctx, op, first, account.RoleMetroAdmin); err != nil || len(r.Roles) != 1 || r.Roles[0] != "ozid-admin" || r.AuthVersion != 4 {
		t.Fatalf("revoke metro: %+v %v", r, err)
	}
	if r, err = s.RevokeRole(ctx, op, first, account.RolePlatformAdmin); err != nil || len(r.Roles) != 0 || r.AuthVersion != 5 {
		t.Fatalf("revoke platform: %+v %v", r, err)
	}
	if _, err = s.GrantRole(ctx, op, first, "root"); err == nil {
		t.Fatal("an unknown role was granted")
	}
}

func TestPlatformAdministratorCannotBeginDeletion(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	if _, err := s.BeginDeletion(ctx, admin, "m-1", "req-1"); !errors.Is(err, account.ErrPlatformAdmin) {
		t.Fatalf("begin deletion of a platform administrator: %v", err)
	}
	st, _ := s.GetAuthState(ctx, admin)
	if st.Status != account.StatusActive || st.AuthVersion != 2 {
		t.Fatalf("a refused deletion changed the account: %+v", st)
	}
	row := db.SecurityAudit.Query().Where(securityaudit.UserID(admin), securityaudit.ActionEQ("deletion.begin")).OnlyX(ctx)
	if row.Result != "refused_platform_admin" || row.RequestID != "req-1" {
		t.Fatalf("audit: %+v", row)
	}
	// The role is the only thing standing in the way: after revoking it the
	// ordinary workflow works, and a metro-admin was never protected.
	if _, err := s.RevokeRole(ctx, op, admin, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginDeletion(ctx, admin, "m-1", "req-2"); err != nil {
		t.Fatalf("after revoke: %v", err)
	}
	metro := create(t, s, "metro@example.com")
	if _, err := s.GrantRole(ctx, op, metro, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginDeletion(ctx, metro, "m-9", "req-3"); err != nil {
		t.Fatalf("metro-admin deletion: %v", err)
	}
}

func TestListUsersRequiresTheRoleNowAndAuditsEveryRead(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	plain := create(t, s, "plain@example.com")
	metro := create(t, s, "metro@example.com")
	if _, err := s.GrantRole(ctx, op, metro, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	for name, actor := range map[string]uuid.UUID{"plain": plain, "metro only": metro, "unknown": uuid.New()} {
		if _, err := s.ListUsers(ctx, actor, "r", account.UserFilter{}, 0, nil); !errors.Is(err, account.ErrForbidden) {
			t.Errorf("%s list: %v", name, err)
		}
		if _, err := s.GetUser(ctx, actor, "r", admin); !errors.Is(err, account.ErrForbidden) {
			t.Errorf("%s get: %v", name, err)
		}
	}
	// A disabled administrator and a revoked one are refused although the role row
	// (or a credential signed a moment ago) may still say otherwise.
	if _, err := s.SetDisabled(ctx, op, admin, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListUsers(ctx, admin, "r", account.UserFilter{}, 0, nil); !errors.Is(err, account.ErrForbidden) {
		t.Fatalf("disabled administrator: %v", err)
	}
	if _, err := s.SetDisabled(ctx, op, admin, false); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListUsers(ctx, admin, "req-list", account.UserFilter{Email: " Plain@Example.COM "}, 0, nil)
	if err != nil || len(page.Users) != 1 || page.Users[0].ID != plain || page.Users[0].Email != "plain@example.com" {
		t.Fatalf("email lookup: %+v %v", page, err)
	}
	if _, err = s.RevokeRole(ctx, op, admin, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetUser(ctx, admin, "r", plain); !errors.Is(err, account.ErrForbidden) {
		t.Fatalf("revoked administrator: %v", err)
	}

	rows := db.SecurityAudit.Query().Where(securityaudit.ActionHasPrefix("admin.users.")).AllX(ctx)
	byResult := map[string]int{}
	for _, row := range rows {
		byResult[row.Action+"="+row.Result]++
		if strings.Contains(row.Reason, "example.com") || strings.Contains(row.Actor, "@") {
			t.Errorf("an audit row carries a filter value or an email: %+v", row)
		}
	}
	if byResult["admin.users.list=ok"] != 1 || byResult["admin.users.list=refused_forbidden"] != 4 || byResult["admin.users.get=refused_forbidden"] != 4 {
		t.Fatalf("audit: %v", byResult)
	}
	ok := db.SecurityAudit.Query().Where(securityaudit.ActionEQ("admin.users.list"), securityaudit.ResultEQ("ok")).OnlyX(ctx)
	if ok.RequestID != "req-list" || ok.Actor != "user:"+admin.String() || ok.Reason != "lookup=email" {
		t.Fatalf("list audit row: %+v", ok)
	}
}

func TestListUsersFiltersAndStablePaging(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	var ids []uuid.UUID
	for i := 0; i < 7; i++ {
		id, err := s.CreateVerifiedUser(ctx, account.NewUser{Email: fmt.Sprintf("u%d@example.com", i), Password: password, DisplayName: fmt.Sprintf("User_%d%%", i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// Accounts created in the same instant: the ID has to break the tie, or a page
	// boundary inside the group would repeat or skip rows.
	same := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	for i := 0; i < 5; i++ {
		db.User.Create().SetEmailCanonical(fmt.Sprintf("tie%d@example.com", i)).SetEmailVerifiedAt(same).SetPasswordHash("$argon2id$x").
			SetDisplayName("Tie").SetCreatedAt(same).SaveX(ctx)
	}
	if _, err := s.SetDisabled(ctx, op, ids[2], true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantRole(ctx, op, ids[3], account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}

	// Paging: 3 per page walks all 13 accounts once, in a stable order.
	seen := map[uuid.UUID]int{}
	var cursor *account.UserCursor
	pages := 0
	for {
		page, err := s.ListUsers(ctx, admin, "r", account.UserFilter{}, 3, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, u := range page.Users {
			seen[u.ID]++
		}
		if page.Next == nil {
			break
		}
		if len(page.Users) != 3 {
			t.Fatalf("a short page with a next cursor: %d", len(page.Users))
		}
		cursor = page.Next
	}
	if len(seen) != 13 || pages != 5 {
		t.Fatalf("paging saw %d accounts in %d pages", len(seen), pages)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s listed %d times", id, n)
		}
	}

	count := func(f account.UserFilter) int {
		t.Helper()
		page, err := s.ListUsers(ctx, admin, "r", f, 100, nil)
		if err != nil {
			t.Fatalf("%+v: %v", f, err)
		}
		return len(page.Users)
	}
	if n := count(account.UserFilter{Nickname: "user_"}); n != 7 {
		t.Errorf("literal nickname search: %d", n)
	}
	if n := count(account.UserFilter{Nickname: "USER_3%"}); n != 1 {
		t.Errorf("case-insensitive literal search with %% : %d", n)
	}
	if n := count(account.UserFilter{Nickname: "%"}); n != 7 {
		t.Errorf("a bare %% must only match nicknames that contain it: %d", n)
	}
	if n := count(account.UserFilter{Nickname: "_"}); n != 7 {
		t.Errorf("a bare _ must be literal: %d", n)
	}
	if n := count(account.UserFilter{Nickname: "u_s"}); n != 0 {
		t.Errorf("_ acted as a wildcard: %d", n)
	}
	if n := count(account.UserFilter{Status: account.StatusDisabled}); n != 1 {
		t.Errorf("status filter: %d", n)
	}
	if n := count(account.UserFilter{Status: account.StatusActive}); n != 12 {
		t.Errorf("status filter: %d", n)
	}
	if n := count(account.UserFilter{Role: account.RoleMetroAdmin}); n != 1 {
		t.Errorf("role filter: %d", n)
	}
	if n := count(account.UserFilter{Role: account.RolePlatformAdmin}); n != 1 {
		t.Errorf("role filter: %d", n)
	}
	if n := count(account.UserFilter{Role: account.RoleMetroAdmin, Status: account.StatusDisabled}); n != 0 {
		t.Errorf("combined filters: %d", n)
	}
	if n := count(account.UserFilter{UserID: ids[4].String()}); n != 1 {
		t.Errorf("uuid lookup: %d", n)
	}
	if n := count(account.UserFilter{Email: "nobody@example.com"}); n != 0 {
		t.Errorf("an empty result is a normal answer: %d", n)
	}

	// Sizes: the default is 50, the cap is 100, negative is refused.
	if page, _ := s.ListUsers(ctx, admin, "r", account.UserFilter{}, 0, nil); len(page.Users) != 13 || page.Next != nil {
		t.Errorf("default size: %d", len(page.Users))
	}
	if _, err := s.ListUsers(ctx, admin, "r", account.UserFilter{}, -1, nil); err == nil {
		t.Error("a negative page size was accepted")
	}
	for name, f := range map[string]account.UserFilter{
		"two lookups":     {Email: "a@example.com", Nickname: "x"},
		"bad email":       {Email: "not-an-email"},
		"bad uuid":        {UserID: "123"},
		"empty-ish uuid":  {UserID: uuid.Nil.String()},
		"long nickname":   {Nickname: strings.Repeat("a", 41)},
		"control in nick": {Nickname: "a\nb"},
		"unknown status":  {Status: "banned"},
		"unknown role":    {Role: "root"},
	} {
		_, err := s.ListUsers(ctx, admin, "r", f, 0, nil)
		if _, ok := identity.IsValidation(err); !ok {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}
}

func TestGetUserReturnsOnlyNecessaryFields(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "target@example.com")
	if _, err := s.GrantRole(ctx, op, target, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	u, err := s.GetUser(ctx, admin, "req-get", target)
	if err != nil || u.ID != target || u.Email != "target@example.com" || u.DisplayName != "测试" || u.Status != account.StatusActive ||
		len(u.Roles) != 1 || u.Roles[0] != "metro-admin" || u.CreatedAt.IsZero() {
		t.Fatalf("get: %+v %v", u, err)
	}
	if _, err = s.GetUser(ctx, admin, "req-get", uuid.New()); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err = s.GetUser(ctx, admin, "req-get", uuid.Nil); err == nil {
		t.Fatal("nil UUID accepted")
	}
	got := db.SecurityAudit.Query().Where(securityaudit.ActionEQ("admin.users.get")).Order(ent.Asc(securityaudit.FieldCreatedAt)).AllX(ctx)
	if len(got) != 2 || got[0].Result != "ok" || got[1].Result != "not_found" || got[0].UserID == nil || *got[0].UserID != target {
		t.Fatalf("audit: %+v", got)
	}
}
