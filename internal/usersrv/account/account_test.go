package account_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/accounttoken"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/identity"
	"github.com/kongken/mono/internal/usersrv/testutil"
)

const password = "correct horse battery"

var ctx = context.Background()

func setup(t *testing.T) (*account.Service, *ent.Client) {
	t.Helper()
	db := testutil.DB(t)
	return account.NewService(db, testutil.Hasher(t)), db
}

func create(t *testing.T, s *account.Service, email string) uuid.UUID {
	t.Helper()
	id, err := s.CreateVerifiedUser(ctx, account.NewUser{Email: email, Password: password, DisplayName: "测试"})
	if err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	return id
}

var op = account.Operator{Name: "ops-1", Reason: "ticket 42", RequestID: "cli-1"}

func TestEmailCaseInsensitiveUniquenessAndDistinctAliases(t *testing.T) {
	s, db := setup(t)
	id := create(t, s, " Alice+tag@Example.com ")
	if _, err := s.CreateVerifiedUser(ctx, account.NewUser{Email: "alice+tag@example.com", Password: password}); !errors.Is(err, account.ErrEmailTaken) {
		t.Fatalf("same mailbox registered twice: %v", err)
	}
	// Spec scenario 1: the neighbours are different mailboxes.
	for _, e := range []string{"alice@example.com", "a.lice@example.com", "alice+other@example.com"} {
		create(t, s, e)
	}
	if n, _ := db.User.Query().Count(ctx); n != 4 {
		t.Fatalf("%d accounts, want 4", n)
	}
	u := db.User.GetX(ctx, id)
	if u.EmailCanonical != "alice+tag@example.com" || u.Status != user.StatusActive || u.AuthVersion != 1 || u.ProfileVersion != 1 || u.EmailVerifiedAt.IsZero() {
		t.Fatalf("new account: %+v", u)
	}
	if strings.Contains(u.PasswordHash, password) || !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatalf("password stored as %q", u.PasswordHash)
	}
}

func TestInvalidInputCreatesNothing(t *testing.T) {
	s, db := setup(t)
	for _, in := range []account.NewUser{
		{Email: "a@example.com, b@example.com", Password: password},
		{Email: "a@example.com\r\nBcc: x@example.com", Password: password},
		{Email: "用户@例え.jp", Password: password},
		{Email: strings.Repeat("a", 65) + "@example.com", Password: password},
		{Email: "ok@example.com", Password: "short"},
		{Email: "ok@example.com", Password: password, DisplayName: strings.Repeat("字", 41)},
	} {
		if _, err := s.CreateVerifiedUser(ctx, in); err == nil {
			t.Errorf("%+v accepted", in)
		} else if _, ok := identity.IsValidation(err); !ok {
			t.Errorf("%+v: not a validation error: %v", in, err)
		}
	}
	if n, _ := db.User.Query().Count(ctx); n != 0 {
		t.Fatalf("%d rows after invalid input", n)
	}
}

func TestDefaultNicknameAndNoEmailInAudit(t *testing.T) {
	s, db := setup(t)
	id, err := s.CreateVerifiedUser(ctx, account.NewUser{Email: "who@example.com", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	if got := db.User.GetX(ctx, id).DisplayName; got != "旅行者" {
		t.Fatalf("nickname %q", got)
	}
	rows := db.SecurityAudit.Query().AllX(ctx)
	if len(rows) != 1 || rows[0].Action != "account.create" || rows[0].UserID == nil || *rows[0].UserID != id {
		t.Fatalf("audit: %+v", rows)
	}
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Actor+r.Reason+r.RequestID+r.Result+r.Action), "who@") {
			t.Fatalf("email in audit row %+v", r)
		}
	}
}

func TestConcurrentCreationYieldsOneAccount(t *testing.T) {
	s, db := setup(t)
	var ok, taken atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			email := "Race@example.com"
			if i%2 == 1 {
				email = " race@EXAMPLE.com"
			}
			switch _, err := s.CreateVerifiedUser(ctx, account.NewUser{Email: email, Password: password}); {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, account.ErrEmailTaken):
				taken.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || taken.Load() != 15 {
		t.Fatalf("created %d, taken %d", ok.Load(), taken.Load())
	}
	if n, _ := db.User.Query().Count(ctx); n != 1 {
		t.Fatalf("%d rows", n)
	}
	// The losers wrote nothing: one create audit row.
	if n, _ := db.SecurityAudit.Query().Where(securityaudit.Action("account.create")).Count(ctx); n != 1 {
		t.Fatalf("%d create audit rows", n)
	}
}

func TestAuthenticateGivesOneAnswerForEveryFailure(t *testing.T) {
	s, _ := setup(t)
	id := create(t, s, "login@example.com")
	gotID, state, err := s.Authenticate(ctx, " LOGIN@example.com ", password)
	if err != nil || gotID != id || state.Status != account.StatusActive || state.AuthVersion != 1 || state.Profile.DisplayName != "测试" {
		t.Fatalf("login: %v %+v %v", gotID, state, err)
	}
	disabled := create(t, s, "off@example.com")
	if _, err = s.SetDisabled(ctx, op, disabled, true); err != nil {
		t.Fatal(err)
	}
	deleting := create(t, s, "going@example.com")
	if _, err = s.BeginDeletion(ctx, deleting, "m-1", ""); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string][2]string{
		"wrong password": {"login@example.com", "wrong password here"},
		"unknown":        {"nobody@example.com", password},
		"disabled":       {"off@example.com", password},
		"deleting":       {"going@example.com", password},
	} {
		if _, _, err := s.Authenticate(ctx, c[0], c[1]); !errors.Is(err, account.ErrInvalidCredentials) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A format error is a field error, and does not touch the account store.
	if _, _, err := s.Authenticate(ctx, "not an email", password); err == nil {
		t.Fatal("bad format accepted")
	} else if _, ok := identity.IsValidation(err); !ok {
		t.Fatalf("bad format: %v", err)
	}
}

func TestVerifyAndChangePassword(t *testing.T) {
	s, db := setup(t)
	id := create(t, s, "pw@example.com")

	v, err := s.VerifyPassword(ctx, id, password, "req-1")
	if err != nil || v != 1 {
		t.Fatalf("verify: %d %v", v, err)
	}
	if _, err = s.VerifyPassword(ctx, id, "not the password", "req-2"); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err = s.ChangePassword(ctx, id, "not the password", "new password is long", "req-3"); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("change with wrong current: %v", err)
	}
	if _, err = s.ChangePassword(ctx, id, password, "short", ""); err == nil {
		t.Fatal("weak new password accepted")
	} else if _, ok := identity.IsValidation(err); !ok {
		t.Fatal(err)
	}
	if got := db.User.GetX(ctx, id).AuthVersion; got != 1 {
		t.Fatalf("failed attempts moved auth_version to %d", got)
	}

	nv, err := s.ChangePassword(ctx, id, password, "new password is long", "req-4")
	if err != nil || nv != 2 {
		t.Fatalf("change: %d %v", nv, err)
	}
	if _, err = s.VerifyPassword(ctx, id, password, ""); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("old password still works: %v", err)
	}
	if v, err = s.VerifyPassword(ctx, id, "new password is long", ""); err != nil || v != 2 {
		t.Fatalf("new password: %d %v", v, err)
	}

	// Failed checks and the change are audited with the user and request, never a secret.
	rows := db.SecurityAudit.Query().Where(securityaudit.UserID(id), securityaudit.ActionIn("password.verify", "password.change")).AllX(ctx)
	results := map[string]int{}
	for _, r := range rows {
		results[r.Action+"/"+r.Result]++
		if strings.Contains(r.Reason+r.Actor, "password") {
			t.Fatalf("secret-looking text in audit: %+v", r)
		}
	}
	if results["password.verify/wrong_password"] != 2 || results["password.change/wrong_password"] != 1 || results["password.change/ok"] != 1 {
		t.Fatalf("audit results: %v", results)
	}
}

func TestConcurrentPasswordChangesOnlyOneWins(t *testing.T) {
	s, db := setup(t)
	id := create(t, s, "race-pw@example.com")
	var wins, losses atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ChangePassword(ctx, id, password, "another long password "+string(rune('a'+i)), "")
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, account.ErrConflict), errors.Is(err, account.ErrInvalidCredentials):
				losses.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d changes succeeded from one old password (losses %d)", wins.Load(), losses.Load())
	}
	if v := db.User.GetX(ctx, id).AuthVersion; v != 2 {
		t.Fatalf("auth_version %d, want exactly one bump", v)
	}
}

func TestProfileUpdate(t *testing.T) {
	s, db := setup(t)
	id := create(t, s, "prof@example.com")
	p, err := s.UpdateProfile(ctx, id, "  新昵称 ", "https://cdn.example.com/a.png")
	if err != nil || p.DisplayName != "新昵称" || p.AvatarURL != "https://cdn.example.com/a.png" || p.Version != 2 {
		t.Fatalf("update: %+v %v", p, err)
	}
	// Writing the same values again changes nothing, including the version.
	if p, err = s.UpdateProfile(ctx, id, "新昵称", "https://cdn.example.com/a.png"); err != nil || p.Version != 2 {
		t.Fatalf("same values: %+v %v", p, err)
	}
	if p, err = s.UpdateProfile(ctx, id, "新昵称", ""); err != nil || p.AvatarURL != "" || p.Version != 3 {
		t.Fatalf("clear avatar: %+v %v", p, err)
	}
	if v := db.User.GetX(ctx, id).AuthVersion; v != 1 {
		t.Fatalf("profile write moved auth_version to %d", v)
	}
	for _, bad := range [][2]string{{"", ""}, {"ok", "http://example.com/a.png"}, {"ok", "https://u:p@example.com/a"}, {"a\nb", ""}, {strings.Repeat("字", 41), ""}} {
		if _, err = s.UpdateProfile(ctx, id, bad[0], bad[1]); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err = s.UpdateProfile(ctx, uuid.New(), "x", ""); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	if _, err = s.SetDisabled(ctx, op, id, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProfile(ctx, id, "while disabled", ""); !errors.Is(err, account.ErrNotActive) {
		t.Fatalf("disabled profile write: %v", err)
	}
}

func TestSelfServiceRefusesInactiveAccounts(t *testing.T) {
	s, _ := setup(t)
	id := create(t, s, "inactive@example.com")
	if _, err := s.SetDisabled(ctx, op, id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyPassword(ctx, id, password, ""); !errors.Is(err, account.ErrNotActive) {
		t.Fatalf("verify: %v", err)
	}
	if _, err := s.ChangePassword(ctx, id, password, "new password is long", ""); !errors.Is(err, account.ErrNotActive) {
		t.Fatalf("change: %v", err)
	}
	st, err := s.GetAuthState(ctx, id)
	if err != nil || st.Status != account.StatusDisabled {
		t.Fatalf("state of a disabled account must be readable so the BFF can deny it: %+v %v", st, err)
	}
	if _, err = s.GetAuthState(ctx, uuid.New()); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestRolesAndStatusBumpTheVersionAndAreAudited(t *testing.T) {
	s, db := setup(t)
	id := create(t, s, "admin@example.com")
	state := func() account.AuthState {
		st, err := s.GetAuthState(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := state(); len(st.Roles) != 0 || st.AuthVersion != 1 {
		t.Fatalf("a new account has roles or a high version: %+v", st)
	}

	r, err := s.GrantRole(ctx, op, id, account.RoleMetroAdmin)
	if err != nil || !r.Changed || r.AuthVersion != 2 || len(r.Roles) != 1 {
		t.Fatalf("grant: %+v %v", r, err)
	}
	// Granting again is a no-op: no version bump, but still audited.
	if r, err = s.GrantRole(ctx, op, id, account.RoleMetroAdmin); err != nil || r.Changed || r.AuthVersion != 2 {
		t.Fatalf("second grant: %+v %v", r, err)
	}
	if st := state(); st.AuthVersion != 2 || len(st.Roles) != 1 || st.Roles[0] != "metro-admin" {
		t.Fatalf("state: %+v", st)
	}
	// Revoking and granting again never returns to an old version: a token issued
	// at version 2 stays invalid.
	if r, err = s.RevokeRole(ctx, op, id, account.RoleMetroAdmin); err != nil || !r.Changed || r.AuthVersion != 3 || len(r.Roles) != 0 {
		t.Fatalf("revoke: %+v %v", r, err)
	}
	if r, err = s.RevokeRole(ctx, op, id, account.RoleMetroAdmin); err != nil || r.Changed || r.AuthVersion != 3 {
		t.Fatalf("second revoke: %+v %v", r, err)
	}
	if r, err = s.GrantRole(ctx, op, id, account.RoleMetroAdmin); err != nil || r.AuthVersion != 4 {
		t.Fatalf("regrant: %+v %v", r, err)
	}
	if r, err = s.SetDisabled(ctx, op, id, true); err != nil || !r.Changed || r.Status != account.StatusDisabled || r.AuthVersion != 5 {
		t.Fatalf("disable: %+v %v", r, err)
	}
	if r, err = s.SetDisabled(ctx, op, id, true); err != nil || r.Changed || r.AuthVersion != 5 {
		t.Fatalf("disable twice: %+v %v", r, err)
	}
	if r, err = s.SetDisabled(ctx, op, id, false); err != nil || !r.Changed || r.Status != account.StatusActive || r.AuthVersion != 6 {
		t.Fatalf("enable: %+v %v", r, err)
	}

	rows := db.SecurityAudit.Query().Where(securityaudit.UserID(id), securityaudit.ActorEQ("ops-1")).Order(ent.Asc(securityaudit.FieldCreatedAt)).AllX(ctx)
	var got []string
	for _, row := range rows {
		got = append(got, row.Action+"="+row.Result)
		if row.Reason != "ticket 42" || row.RequestID != "cli-1" {
			t.Errorf("operator context lost: %+v", row)
		}
	}
	want := []string{
		"role.grant:metro-admin=ok", "role.grant:metro-admin=unchanged", "role.revoke:metro-admin=ok", "role.revoke:metro-admin=unchanged",
		"role.grant:metro-admin=ok", "account.disable=ok", "account.disable=unchanged", "account.enable=ok",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit trail:\n got %v\nwant %v", got, want)
	}
}

func TestRoleGrantNeedsAnActiveAccountAndKnownTargets(t *testing.T) {
	s, db := setup(t)
	disabled := create(t, s, "d@example.com")
	if _, err := s.SetDisabled(ctx, op, disabled, true); err != nil {
		t.Fatal(err)
	}
	deleting := create(t, s, "x@example.com")
	if _, err := s.BeginDeletion(ctx, deleting, "m-1", ""); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]uuid.UUID{"disabled": disabled, "deleting": deleting} {
		if _, err := s.GrantRole(ctx, op, id, account.RoleMetroAdmin); !errors.Is(err, account.ErrNotActive) {
			t.Errorf("%s: %v", name, err)
		}
		if n, _ := db.UserRole.Query().Count(ctx); n != 0 {
			t.Fatalf("%s: role granted anyway", name)
		}
	}
	// A deleting account cannot be enabled or disabled either.
	if _, err := s.SetDisabled(ctx, op, deleting, false); !errors.Is(err, account.ErrNotActive) {
		t.Fatalf("enable deleting: %v", err)
	}
	if _, err := s.SetDisabled(ctx, op, deleting, true); !errors.Is(err, account.ErrNotActive) {
		t.Fatalf("disable deleting: %v", err)
	}
	// The refused attempts and the unknown target are in the audit log with their result.
	if _, err := s.GrantRole(ctx, op, uuid.New(), account.RoleMetroAdmin); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	if n, _ := db.SecurityAudit.Query().Where(securityaudit.ResultEQ("refused_not_active")).Count(ctx); n != 4 {
		t.Fatalf("%d refused audit rows, want 4", n)
	}
	row := db.SecurityAudit.Query().Where(securityaudit.ResultEQ("not_found")).OnlyX(ctx)
	if row.UserID != nil || row.Actor != "ops-1" {
		t.Fatalf("not_found row: %+v", row)
	}
	// Roles outside the whitelist and malformed operators are rejected before any write.
	if _, err := s.GrantRole(ctx, op, disabled, "superuser"); err == nil {
		t.Fatal("unknown role accepted")
	}
	for _, bad := range []account.Operator{{}, {Name: "ops", Reason: ""}, {Name: "has space", Reason: "x"}, {Name: "ops", Reason: "line\nbreak"}, {Name: strings.Repeat("a", 65), Reason: "x"}} {
		if _, err := s.GrantRole(ctx, bad, disabled, account.RoleMetroAdmin); err == nil {
			t.Errorf("operator %+v accepted", bad)
		}
	}
}

func TestRevokeWorksOnAnyStateAndRoleDeletesWithUser(t *testing.T) {
	s, db := setup(t)
	id := create(t, s, "rv@example.com")
	if _, err := s.GrantRole(ctx, op, id, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDisabled(ctx, op, id, true); err != nil {
		t.Fatal(err)
	}
	if r, err := s.RevokeRole(ctx, op, id, account.RoleMetroAdmin); err != nil || !r.Changed {
		t.Fatalf("revoke on disabled: %+v %v", r, err)
	}
	if _, err := s.SetDisabled(ctx, op, id, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantRole(ctx, op, id, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginDeletion(ctx, id, "m-9", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishDeletion(ctx, id, "m-9", ""); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.UserRole.Query().Count(ctx); n != 0 {
		t.Fatalf("%d role rows survive the account", n)
	}
}

func TestResolveEmail(t *testing.T) {
	s, _ := setup(t)
	id := create(t, s, "find@example.com")
	if got, err := s.ResolveEmail(ctx, " FIND@example.com"); err != nil || got != id {
		t.Fatalf("resolve: %v %v", got, err)
	}
	if _, err := s.ResolveEmail(ctx, "nobody@example.com"); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.ResolveEmail(ctx, "a@b@c"); err == nil {
		t.Fatal("malformed accepted")
	}
}

func token(t *testing.T, db *ent.Client, purpose accounttoken.Purpose, email string, userID *uuid.UUID, expires time.Time) {
	t.Helper()
	sum := sha256.Sum256([]byte(uuid.NewString()))
	c := db.AccountToken.Create().SetTokenHash(sum[:]).SetPurpose(purpose).SetEmailCanonical(email).SetAppID("trip-map").
		SetCreatedAt(expires.Add(-time.Minute)).SetExpiresAt(expires)
	if userID != nil {
		c.SetUserID(*userID).SetIssuedAuthVersion(1)
	}
	c.ExecX(ctx)
}

func TestDeletionLifecycle(t *testing.T) {
	s, db := setup(t)
	id := create(t, s, "bye@example.com")
	other := create(t, s, "stay@example.com")
	if _, err := s.GrantRole(ctx, op, id, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(10 * time.Minute)
	token(t, db, accounttoken.PurposePasswordReset, "bye@example.com", &id, soon)
	token(t, db, accounttoken.PurposeRegistration, "bye@example.com", nil, soon)
	token(t, db, accounttoken.PurposePasswordReset, "stay@example.com", &other, soon)

	// FinishDeletion cannot skip BeginDeletion.
	if _, err := s.FinishDeletion(ctx, id, "m-1", "r"); !errors.Is(err, account.ErrDeletionMismatch) {
		t.Fatalf("finish before begin: %v", err)
	}
	v, err := s.BeginDeletion(ctx, id, "m-1", "r")
	if err != nil || v != 3 { // 1 -> grant 2 -> begin 3
		t.Fatalf("begin: %d %v", v, err)
	}
	if v2, err := s.BeginDeletion(ctx, id, "m-1", "r"); err != nil || v2 != v {
		t.Fatalf("begin is not idempotent: %d %v", v2, err)
	}
	if _, err = s.BeginDeletion(ctx, id, "m-2", "r"); !errors.Is(err, account.ErrDeletionMismatch) {
		t.Fatalf("second workflow: %v", err)
	}
	st, _ := s.GetAuthState(ctx, id)
	if st.Status != account.StatusDeleting || st.AuthVersion != 3 {
		t.Fatalf("state: %+v", st)
	}
	// All normal access is closed while deleting.
	if _, err = s.VerifyPassword(ctx, id, password, ""); !errors.Is(err, account.ErrNotActive) {
		t.Fatalf("verify while deleting: %v", err)
	}
	if _, err = s.UpdateProfile(ctx, id, "x", ""); !errors.Is(err, account.ErrNotActive) {
		t.Fatalf("profile write while deleting: %v", err)
	}
	if _, _, err = s.Authenticate(ctx, "bye@example.com", password); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("login while deleting: %v", err)
	}
	// The user's tokens are void, another user's are not.
	if n, _ := db.AccountToken.Query().Where(accounttoken.EmailCanonical("bye@example.com")).Count(ctx); n != 0 {
		t.Fatalf("%d tokens survive BeginDeletion", n)
	}
	if n, _ := db.AccountToken.Query().Where(accounttoken.EmailCanonical("stay@example.com")).Count(ctx); n != 1 {
		t.Fatalf("another user's token touched: %d", n)
	}
	// A wrong workflow or another user cannot finish it.
	if _, err = s.FinishDeletion(ctx, id, "m-2", "r"); !errors.Is(err, account.ErrDeletionMismatch) {
		t.Fatalf("finish with the wrong workflow: %v", err)
	}
	if _, err = s.FinishDeletion(ctx, other, "m-1", "r"); !errors.Is(err, account.ErrDeletionMismatch) {
		t.Fatalf("finish of an active account: %v", err)
	}
	if _, err = db.User.Get(ctx, other); err != nil {
		t.Fatal("another account was touched")
	}

	removed, err := s.FinishDeletion(ctx, id, "m-1", "r")
	if err != nil || !removed {
		t.Fatalf("finish: %v %v", removed, err)
	}
	if _, err = s.GetAuthState(ctx, id); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("account still exists: %v", err)
	}
	if n, _ := db.UserRole.Query().Count(ctx); n != 0 {
		t.Fatalf("%d roles survive", n)
	}
	// Replay of the lost response: same user and workflow succeed, nothing else does.
	if removed, err = s.FinishDeletion(ctx, id, "m-1", "r"); err != nil || removed {
		t.Fatalf("replay: %v %v", removed, err)
	}
	if _, err = s.FinishDeletion(ctx, id, "other-workflow", "r"); !errors.Is(err, account.ErrDeletionMismatch) {
		t.Fatalf("replay by another workflow: %v", err)
	}
	if _, err = s.FinishDeletion(ctx, uuid.New(), "m-1", "r"); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("a missing user is not a success: %v", err)
	}
	if _, err = s.BeginDeletion(ctx, id, "m-1", "r"); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("begin after finish: %v", err)
	}
	// The tombstone holds no email or credential: only the user, the workflow and times.
	tomb := db.DeletionTombstone.GetX(ctx, id)
	if tomb.MutationID != "m-1" || !tomb.ExpiresAt.After(tomb.CompletedAt.Add(23*time.Hour)) {
		t.Fatalf("tombstone: %+v", tomb)
	}

	// The same address registers again as a new account without roles or history.
	again := create(t, s, "bye@example.com")
	if again == id {
		t.Fatal("user ID reused")
	}
	if st, _ = s.GetAuthState(ctx, again); st.AuthVersion != 1 || len(st.Roles) != 0 || st.Status != account.StatusActive {
		t.Fatalf("new account inherited state: %+v", st)
	}
	// The deletion audit rows carry the user UUID but no email.
	for _, row := range db.SecurityAudit.Query().Where(securityaudit.UserID(id)).AllX(ctx) {
		if strings.Contains(row.Actor+row.Reason+row.RequestID, "@") {
			t.Fatalf("email-like text in audit: %+v", row)
		}
	}
}

func TestTombstoneExpires(t *testing.T) {
	s, _ := setup(t)
	clock := time.Now().UTC()
	s.SetClock(func() time.Time { return clock })
	id := create(t, s, "old@example.com")
	if _, err := s.BeginDeletion(ctx, id, "m-1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishDeletion(ctx, id, "m-1", ""); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(account.TombstoneTTL - time.Minute)
	if removed, err := s.FinishDeletion(ctx, id, "m-1", ""); err != nil || removed {
		t.Fatalf("within 24h: %v %v", removed, err)
	}
	clock = clock.Add(2 * time.Minute)
	if _, err := s.FinishDeletion(ctx, id, "m-1", ""); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("after 24h: %v", err)
	}
}

func TestConcurrentBeginDeletionIsIdempotent(t *testing.T) {
	s, db := setup(t)
	id := create(t, s, "twice@example.com")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.BeginDeletion(ctx, id, "m-1", ""); err != nil {
				t.Errorf("begin: %v", err)
			}
		}()
	}
	wg.Wait()
	if v := db.User.GetX(ctx, id).AuthVersion; v != 2 {
		t.Fatalf("auth_version %d, want one bump", v)
	}
	if n, _ := db.SecurityAudit.Query().Where(securityaudit.Action("deletion.begin")).Count(ctx); n != 1 {
		t.Fatalf("%d begin audit rows", n)
	}
}

func TestPurgeExpired(t *testing.T) {
	s, db := setup(t)
	now := time.Now().UTC()
	clock := now
	s.SetClock(func() time.Time { return clock })
	id := create(t, s, "purge@example.com")
	// Tokens: stale expired, recently expired, still valid.
	token(t, db, accounttoken.PurposeRegistration, "a@example.com", nil, now.Add(-10*24*time.Hour))
	token(t, db, accounttoken.PurposeRegistration, "b@example.com", nil, now.Add(-time.Hour))
	token(t, db, accounttoken.PurposeRegistration, "c@example.com", nil, now.Add(time.Hour))
	// An old and a fresh audit row.
	db.SecurityAudit.Create().SetAction("old").SetResult("ok").SetCreatedAt(now.Add(-91 * 24 * time.Hour)).ExecX(ctx)
	// A finished deletion whose tombstone has expired.
	gone := uuid.New()
	db.DeletionTombstone.Create().SetID(gone).SetMutationID("m").SetCompletedAt(now.Add(-48 * time.Hour)).SetExpiresAt(now.Add(-24 * time.Hour)).ExecX(ctx)
	db.DeletionTombstone.Create().SetID(uuid.New()).SetMutationID("m").SetCompletedAt(now).SetExpiresAt(now.Add(24 * time.Hour)).ExecX(ctx)

	res, err := s.PurgeExpired(ctx)
	if err != nil || res.Tokens != 1 || res.Tombstones != 1 || res.Audit != 1 {
		t.Fatalf("purge: %+v %v", res, err)
	}
	if n, _ := db.AccountToken.Query().Count(ctx); n != 2 {
		t.Fatalf("%d tokens left", n)
	}
	if n, _ := db.DeletionTombstone.Query().Count(ctx); n != 1 {
		t.Fatalf("%d tombstones left", n)
	}
	if _, err = db.User.Get(ctx, id); err != nil {
		t.Fatal("purge removed an account")
	}
	if res, err = s.PurgeExpired(ctx); err != nil || res != (account.PurgeResult{}) {
		t.Fatalf("second purge: %+v %v", res, err)
	}
}

func TestStoreOutageIsUnavailableNotInternal(t *testing.T) {
	s, db := setup(t)
	id := create(t, s, "out@example.com")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.GetAuthState(cancelled, id); !errors.Is(err, account.ErrUnavailable) {
		t.Fatalf("cancelled: %v", err)
	}
	if _, err := s.VerifyPassword(cancelled, id, password, ""); !errors.Is(err, account.ErrUnavailable) {
		t.Fatalf("cancelled verify: %v", err)
	}
	_ = db.Close()
	if _, err := s.GetAuthState(ctx, id); err == nil {
		t.Fatal("closed database answered")
	}
}
