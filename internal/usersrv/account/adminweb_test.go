package account_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/adminmutation"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/ent/userrole"
	"github.com/kongken/mono/internal/usersrv/identity"
)

// req builds one console request.
func req(operator uuid.UUID, a account.AdminAction, target uuid.UUID, version int64, key string) account.AdminChange {
	return account.AdminChange{Operator: operator, Action: a, Target: target, ExpectedVersion: version, MutationID: key,
		Reason: "support ticket 77", RequestID: "req-" + key}
}

func versionOf(t *testing.T, db *ent.Client, id uuid.UUID) int64 {
	t.Helper()
	return db.User.GetX(ctx, id).AuthVersion
}

func rolesOf(t *testing.T, db *ent.Client, id uuid.UUID) []string {
	t.Helper()
	var out []string
	for _, r := range db.UserRole.Query().Where(userrole.UserID(id)).AllX(ctx) {
		out = append(out, r.Role)
	}
	return out
}

func TestAdminChangesApplyAndAdvanceTheVersionOnlyWhenSomethingChanged(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "plain@example.com")
	v := versionOf(t, db, target)

	// Disable: changed, version advances, audit has the operator, reason and before/after.
	r, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v, "k-1"))
	if err != nil || !r.Changed || r.Replayed || r.Status != account.StatusDisabled || r.AuthVersion != v+1 {
		t.Fatalf("disable: %+v %v", r, err)
	}
	row := db.SecurityAudit.Query().Where(securityaudit.UserID(target), securityaudit.ActionEQ("admin.account.disable")).OnlyX(ctx)
	if row.Result != "ok" || row.Channel != "web" || row.ActorUserID == nil || *row.ActorUserID != admin || row.Actor != "user:"+admin.String() ||
		row.MutationID != "k-1" || row.RequestID != "req-k-1" || row.Reason != "support ticket 77" ||
		row.BeforeStatus != "active" || row.AfterStatus != "disabled" || row.BeforeRoles != "" || row.AfterRoles != "" {
		t.Fatalf("audit: %+v", row)
	}

	// Disabling again is a normal, classified "unchanged": nothing moves, not even the version.
	r, err = s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v+1, "k-2"))
	if err != nil || r.Changed || r.AuthVersion != v+1 || versionOf(t, db, target) != v+1 {
		t.Fatalf("disable again: %+v %v", r, err)
	}
	if got := db.SecurityAudit.Query().Where(securityaudit.MutationID("k-2")).OnlyX(ctx); got.Result != "unchanged" || got.BeforeStatus != "disabled" || got.AfterStatus != "disabled" {
		t.Fatalf("unchanged audit: %+v", got)
	}

	// Enabling advances the version again: the old session stays dead.
	r, err = s.ApplyAdminChange(ctx, req(admin, account.ActionEnable, target, v+1, "k-3"))
	if err != nil || !r.Changed || r.Status != account.StatusActive || r.AuthVersion != v+2 {
		t.Fatalf("enable: %+v %v", r, err)
	}

	// Grant, grant again (unchanged), revoke, revoke again (unchanged).
	if r, err = s.ApplyAdminChange(ctx, req(admin, account.ActionGrantMetroAdmin, target, v+2, "k-4")); err != nil || !r.Changed ||
		len(r.Roles) != 1 || r.Roles[0] != account.RoleMetroAdmin || r.AuthVersion != v+3 {
		t.Fatalf("grant: %+v %v", r, err)
	}
	if got := db.SecurityAudit.Query().Where(securityaudit.MutationID("k-4")).OnlyX(ctx); got.BeforeRoles != "" || got.AfterRoles != "metro-admin" {
		t.Fatalf("grant audit: %+v", got)
	}
	if r, err = s.ApplyAdminChange(ctx, req(admin, account.ActionGrantMetroAdmin, target, v+3, "k-5")); err != nil || r.Changed || r.AuthVersion != v+3 {
		t.Fatalf("grant again: %+v %v", r, err)
	}
	if r, err = s.ApplyAdminChange(ctx, req(admin, account.ActionRevokeMetroAdmin, target, v+3, "k-6")); err != nil || !r.Changed || len(r.Roles) != 0 || r.AuthVersion != v+4 {
		t.Fatalf("revoke: %+v %v", r, err)
	}
	if r, err = s.ApplyAdminChange(ctx, req(admin, account.ActionRevokeMetroAdmin, target, v+4, "k-7")); err != nil || r.Changed || r.AuthVersion != v+4 {
		t.Fatalf("revoke again: %+v %v", r, err)
	}
	// Granting again after the revocation moves forward from where it was: it does not
	// bring an older version back.
	if r, err = s.ApplyAdminChange(ctx, req(admin, account.ActionGrantMetroAdmin, target, v+4, "k-8")); err != nil || r.AuthVersion != v+5 {
		t.Fatalf("grant after revoke: %+v %v", r, err)
	}
	// Granting the role is granted_by the operator account, not a free name.
	if by := db.UserRole.Query().Where(userrole.UserID(target)).OnlyX(ctx).GrantedBy; by != "user:"+admin.String() {
		t.Fatalf("granted_by %q", by)
	}
}

func TestAdminChangeIsRefusedForEveryForbiddenTarget(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	other := platformAdmin(t, s, "root2@example.com")
	actions := []account.AdminAction{account.ActionDisable, account.ActionEnable, account.ActionGrantMetroAdmin, account.ActionRevokeMetroAdmin}

	t.Run("an account that holds the platform role, whatever its state", func(t *testing.T) {
		for _, self := range []bool{false, true} {
			target := other
			if self {
				target = admin // an administrator cannot change themselves either
			}
			for i, a := range actions {
				v := versionOf(t, db, target)
				before := db.User.GetX(ctx, target)
				_, err := s.ApplyAdminChange(ctx, req(admin, a, target, v, "prot-"+string(a)+map[bool]string{true: "-self", false: ""}[self]))
				if !errors.Is(err, account.ErrTargetProtected) {
					t.Fatalf("%s (self=%v, %d): %v", a, self, i, err)
				}
				after := db.User.GetX(ctx, target)
				if after.AuthVersion != before.AuthVersion || after.Status != before.Status || len(rolesOf(t, db, target)) != 1 {
					t.Fatalf("%s changed a platform administrator: %+v -> %+v", a, before, after)
				}
			}
		}
		if n := db.SecurityAudit.Query().Where(securityaudit.Result("refused_target_protected")).CountX(ctx); n != 8 {
			t.Fatalf("%d refusals audited", n)
		}
	})

	t.Run("a disabled platform administrator is still protected", func(t *testing.T) {
		if _, err := s.SetDisabled(ctx, op, other, true); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionEnable, other, versionOf(t, db, other), "prot-enable")); !errors.Is(err, account.ErrTargetProtected) {
			t.Fatalf("enable a disabled platform administrator: %v", err)
		}
	})

	t.Run("a deleting account can be neither enabled nor changed", func(t *testing.T) {
		target := create(t, s, "closing@example.com")
		if _, err := s.BeginDeletion(ctx, target, "m-del", "req-del"); err != nil {
			t.Fatal(err)
		}
		for _, a := range actions {
			v := versionOf(t, db, target)
			if _, err := s.ApplyAdminChange(ctx, req(admin, a, target, v, "del-"+string(a))); !errors.Is(err, account.ErrNotActive) {
				t.Fatalf("%s on a deleting account: %v", a, err)
			}
			u := db.User.GetX(ctx, target)
			if u.Status != user.StatusDeleting || u.AuthVersion != v || u.DeletionMutationID == nil {
				t.Fatalf("%s touched the deletion: %+v", a, u)
			}
		}
	})

	t.Run("a metro-admin role needs an active account", func(t *testing.T) {
		target := create(t, s, "off@example.com")
		if _, err := s.SetDisabled(ctx, op, target, true); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionGrantMetroAdmin, target, versionOf(t, db, target), "off-grant")); !errors.Is(err, account.ErrNotActive) {
			t.Fatalf("grant to a disabled account: %v", err)
		}
		if len(rolesOf(t, db, target)) != 0 {
			t.Fatal("a disabled account was given a role")
		}
	})

	t.Run("a missing account", func(t *testing.T) {
		if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, uuid.New(), 1, "ghost")); !errors.Is(err, account.ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
		if got := db.SecurityAudit.Query().Where(securityaudit.MutationID("ghost")).OnlyX(ctx); got.Result != "not_found" || got.UserID != nil {
			t.Fatalf("audit: %+v", got)
		}
	})
}

func TestAdminChangeNeedsTheOperatorToHoldTheRoleNow(t *testing.T) {
	s, db := setup(t)
	target := create(t, s, "plain@example.com")
	plain := create(t, s, "nobody@example.com")
	metro := create(t, s, "metro@example.com")
	if _, err := s.GrantRole(ctx, op, metro, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	for name, who := range map[string]uuid.UUID{"ordinary": plain, "metro-admin": metro, "nobody": uuid.New()} {
		v := versionOf(t, db, target)
		if _, err := s.ApplyAdminChange(ctx, req(who, account.ActionDisable, target, v, "who-"+name)); !errors.Is(err, account.ErrForbidden) {
			t.Fatalf("%s: %v", name, err)
		}
		if versionOf(t, db, target) != v || db.User.GetX(ctx, target).Status != user.StatusActive {
			t.Fatalf("%s changed the account", name)
		}
	}
	// A revoked or disabled administrator is refused at once, whatever credential they hold.
	admin := platformAdmin(t, s, "root@example.com")
	if _, err := s.RevokeRole(ctx, op, admin, account.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, versionOf(t, db, target), "revoked")); !errors.Is(err, account.ErrForbidden) {
		t.Fatalf("revoked administrator: %v", err)
	}
	admin = platformAdmin(t, s, "root2@example.com")
	if _, err := s.SetDisabled(ctx, op, admin, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, versionOf(t, db, target), "disabled")); !errors.Is(err, account.ErrForbidden) {
		t.Fatalf("disabled administrator: %v", err)
	}
	if n := db.SecurityAudit.Query().Where(securityaudit.Result("refused_forbidden"), securityaudit.ChannelEQ("web")).CountX(ctx); n != 5 {
		t.Fatalf("%d forbidden attempts audited", n)
	}
	if n := db.AdminMutation.Query().CountX(ctx); n != 0 {
		t.Fatalf("a refused change left %d receipts", n)
	}
}

func TestAdminChangeValidatesItsInput(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "plain@example.com")
	v := versionOf(t, db, target)
	long := strings.Repeat("字", 201)
	bad := map[string]func(*account.AdminChange){
		"empty reason":      func(c *account.AdminChange) { c.Reason = "" },
		"blank reason":      func(c *account.AdminChange) { c.Reason = "   " },
		"long reason":       func(c *account.AdminChange) { c.Reason = long },
		"control reason":    func(c *account.AdminChange) { c.Reason = "a\nb" },
		"email reason":      func(c *account.AdminChange) { c.Reason = "asked by bob@example.com" },
		"token reason":      func(c *account.AdminChange) { c.Reason = "key " + strings.Repeat("a1", 20) },
		"mcp token reason":  func(c *account.AdminChange) { c.Reason = "used mcp_abc" },
		"no key":            func(c *account.AdminChange) { c.MutationID = "" },
		"long key":          func(c *account.AdminChange) { c.MutationID = strings.Repeat("k", 65) },
		"key characters":    func(c *account.AdminChange) { c.MutationID = "a b" },
		"no version":        func(c *account.AdminChange) { c.ExpectedVersion = 0 },
		"negative version":  func(c *account.AdminChange) { c.ExpectedVersion = -3 },
		"no target":         func(c *account.AdminChange) { c.Target = uuid.Nil },
		"unknown action":    func(c *account.AdminChange) { c.Action = "grant_ozid_admin" },
		"long request id":   func(c *account.AdminChange) { c.RequestID = strings.Repeat("r", 65) },
		"empty action name": func(c *account.AdminChange) { c.Action = "" },
	}
	for name, mutate := range bad {
		c := req(admin, account.ActionDisable, target, v, "val-1")
		mutate(&c)
		_, err := s.ApplyAdminChange(ctx, c)
		if _, ok := identity.IsValidation(err); !ok {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if versionOf(t, db, target) != v || db.AdminMutation.Query().CountX(ctx) != 0 {
		t.Fatal("invalid input changed something")
	}
	// A reason with spaces is trimmed, and a Chinese reason of 200 characters is fine.
	c := req(admin, account.ActionDisable, target, v, "val-2")
	c.Reason = "  " + strings.Repeat("字", 198) + "  "
	if _, err := s.ApplyAdminChange(ctx, c); err != nil {
		t.Fatalf("a valid reason: %v", err)
	}
}

func TestAdminChangeRefusesAStaleVersion(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "plain@example.com")
	v := versionOf(t, db, target)
	// Someone (the CLI) changes the account after the administrator looked at it.
	if _, err := s.GrantRole(ctx, op, target, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	_, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v, "stale-1"))
	if !errors.Is(err, account.ErrTargetChanged) {
		t.Fatalf("stale: %v", err)
	}
	if u := db.User.GetX(ctx, target); u.Status != user.StatusActive || u.AuthVersion != v+1 {
		t.Fatalf("a refused change changed the account: %+v", u)
	}
	if got := db.SecurityAudit.Query().Where(securityaudit.MutationID("stale-1")).OnlyX(ctx); got.Result != "refused_version_conflict" || got.AfterStatus != "active" {
		t.Fatalf("audit: %+v", got)
	}
	// A refusal leaves no receipt: after reading again, the same key may be used.
	if n := db.AdminMutation.Query().CountX(ctx); n != 0 {
		t.Fatalf("%d receipts", n)
	}
	if _, err = s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, v+1, "stale-1")); err != nil {
		t.Fatalf("after reading again: %v", err)
	}
}

func TestAdminChangeIsIdempotentPerOperatorFor24Hours(t *testing.T) {
	s, db := setup(t)
	now := time.Now().UTC()
	clock := now
	s.SetClock(func() time.Time { return clock })
	a := platformAdmin(t, s, "root-a@example.com")
	b := platformAdmin(t, s, "root-b@example.com")
	target := create(t, s, "plain@example.com")
	other := create(t, s, "other@example.com")
	v := versionOf(t, db, target)

	first, err := s.ApplyAdminChange(ctx, req(a, account.ActionDisable, target, v, "same"))
	if err != nil || !first.Changed || first.Replayed {
		t.Fatalf("first: %+v %v", first, err)
	}

	// The response was lost; the client retries the identical request. The account has even moved on
	// in between (the CLI enabled it again) - the retry still gets the first answer back.
	if _, err = s.SetDisabled(ctx, op, target, false); err != nil {
		t.Fatal(err)
	}
	moved := versionOf(t, db, target)
	again, err := s.ApplyAdminChange(ctx, req(a, account.ActionDisable, target, v, "same"))
	if err != nil || !again.Replayed || !again.Changed || again.Status != account.StatusDisabled || again.AuthVersion != first.AuthVersion {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if u := db.User.GetX(ctx, target); u.Status != user.StatusActive || u.AuthVersion != moved {
		t.Fatalf("a replay changed the account again: %+v", u)
	}
	rows := db.SecurityAudit.Query().Where(securityaudit.MutationID("same")).Order(ent.Asc(securityaudit.FieldCreatedAt)).AllX(ctx)
	if len(rows) != 2 || rows[0].Result != "ok" || rows[1].Result != "replayed" || rows[1].AfterStatus != "disabled" {
		t.Fatalf("audit rows: %+v", rows)
	}
	if n := db.AdminMutation.Query().Where(adminmutation.OperatorID(a)).CountX(ctx); n != 1 {
		t.Fatalf("%d receipts", n)
	}

	// The same key with different content is refused, whatever differs - and does nothing.
	diff := map[string]func(*account.AdminChange){
		"action":  func(c *account.AdminChange) { c.Action = account.ActionEnable },
		"target":  func(c *account.AdminChange) { c.Target = other; c.ExpectedVersion = versionOf(t, db, other) },
		"version": func(c *account.AdminChange) { c.ExpectedVersion = v + 5 },
		"reason":  func(c *account.AdminChange) { c.Reason = "a different reason" },
	}
	for name, mutate := range diff {
		c := req(a, account.ActionDisable, target, v, "same")
		mutate(&c)
		if _, err = s.ApplyAdminChange(ctx, c); !errors.Is(err, account.ErrMutationReused) {
			t.Fatalf("%s differs: %v", name, err)
		}
	}
	if db.User.GetX(ctx, other).Status != user.StatusActive || versionOf(t, db, target) != moved {
		t.Fatal("a reused key changed an account")
	}
	if n := db.SecurityAudit.Query().Where(securityaudit.Result("refused_mutation_reuse")).CountX(ctx); n != 4 {
		t.Fatalf("%d reuse refusals audited", n)
	}

	// Another operator's receipts are separate: the same key neither replays a's answer nor is
	// blocked by it. b's request is judged on its own (here its version is stale -> conflict).
	if _, err = s.ApplyAdminChange(ctx, req(b, account.ActionDisable, target, v, "same")); !errors.Is(err, account.ErrTargetChanged) {
		t.Fatalf("another operator with the same key: %v", err)
	}
	if r, err := s.ApplyAdminChange(ctx, req(b, account.ActionDisable, other, versionOf(t, db, other), "same")); err != nil || r.Replayed || !r.Changed {
		t.Fatalf("another operator's own request under the same key: %+v %v", r, err)
	}
	if n := db.AdminMutation.Query().Where(adminmutation.MutationID("same")).CountX(ctx); n != 2 {
		t.Fatalf("%d receipts under the key", n)
	}

	// After 24 hours the receipt answers nothing: the key is free again, and purge removes it.
	clock = now.Add(account.AdminMutationTTL + time.Minute)
	c := req(a, account.ActionEnable, target, versionOf(t, db, target), "same")
	if r, err := s.ApplyAdminChange(ctx, c); err != nil || r.Replayed {
		t.Fatalf("after expiry: %+v %v", r, err)
	}
	clock = clock.Add(account.AdminMutationTTL + time.Minute)
	res, err := s.PurgeExpired(ctx)
	if err != nil || res.Mutations != 2 {
		t.Fatalf("purge: %+v %v", res, err)
	}
	if n := db.AdminMutation.Query().CountX(ctx); n != 0 {
		t.Fatalf("%d receipts left", n)
	}
}

func TestAdminChangeAuditsHoldNoSecrets(t *testing.T) {
	s, db := setup(t)
	admin := platformAdmin(t, s, "root@example.com")
	target := create(t, s, "plain@example.com")
	if _, err := s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, versionOf(t, db, target), "hyg-1")); err != nil {
		t.Fatal(err)
	}
	for _, row := range db.SecurityAudit.Query().Where(securityaudit.ChannelEQ("web")).AllX(ctx) {
		text := strings.Join([]string{row.Actor, row.Action, row.RequestID, row.Result, row.Reason, row.MutationID, row.BeforeStatus,
			row.AfterStatus, row.BeforeRoles, row.AfterRoles}, "|")
		for _, secret := range []string{"@example.com", password, "argon2"} {
			if strings.Contains(text, secret) {
				t.Fatalf("audit row holds %q: %s", secret, text)
			}
		}
	}
	rec := db.AdminMutation.Query().OnlyX(ctx)
	if len(rec.RequestHash) != 32 || strings.Contains(rec.Roles+rec.Status+rec.Action, "@") {
		t.Fatalf("receipt: %+v", rec)
	}
}

// The target row lock orders the console's change against the operator CLI, in both directions: a
// platform role the CLI grants first protects the account from the web disable, and a web disable
// that gets the lock first leaves an account the CLI can no longer grant to. Either way no account
// is left both disabled by the console and holding the platform role.
func TestAdminChangeAndCLIPlatformGrantTakeTheRowLockInEitherOrder(t *testing.T) {
	setupRace := func(t *testing.T, email string) (*race, uuid.UUID) {
		r := newRace(t, email)
		return r, platformAdmin(t, r.s, "root-"+email)
	}
	t.Run("the CLI grant takes the lock first: the web disable is refused", func(t *testing.T) {
		r, admin := setupRace(t, "grant-first@example.com")
		v := versionOf(t, r.db, r.userID)
		web := func() error {
			_, err := r.s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, r.userID, v, "race-1"))
			return err
		}
		grantErr, webErr := r.inOrder(r.grant, web)
		if grantErr != nil || !errors.Is(webErr, account.ErrTargetProtected) {
			t.Fatalf("grant: %v, web: %v", grantErr, webErr)
		}
		if u := r.db.User.GetX(ctx, r.userID); u.Status != user.StatusActive {
			t.Fatalf("a platform administrator was disabled by the console: %+v", u)
		}
	})
	t.Run("the web disable takes the lock first: the CLI grant is refused", func(t *testing.T) {
		r, admin := setupRace(t, "web-first@example.com")
		v := versionOf(t, r.db, r.userID)
		web := func() error {
			_, err := r.s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, r.userID, v, "race-2"))
			return err
		}
		webErr, grantErr := r.inOrder(web, r.grant)
		if webErr != nil || !errors.Is(grantErr, account.ErrNotActive) {
			t.Fatalf("web: %v, grant: %v", webErr, grantErr)
		}
		if _, holds := r.invariant(); holds {
			t.Fatal("the disabled account holds the platform role")
		}
	})
	t.Run("a metro-admin revoke behind a CLI grant is refused too", func(t *testing.T) {
		r, admin := setupRace(t, "revoke-behind@example.com")
		if _, err := r.s.GrantRole(ctx, op, r.userID, account.RoleMetroAdmin); err != nil {
			t.Fatal(err)
		}
		v := versionOf(t, r.db, r.userID)
		web := func() error {
			_, err := r.s.ApplyAdminChange(ctx, req(admin, account.ActionRevokeMetroAdmin, r.userID, v, "race-3"))
			return err
		}
		grantErr, webErr := r.inOrder(r.grant, web)
		if grantErr != nil || !errors.Is(webErr, account.ErrTargetProtected) {
			t.Fatalf("grant: %v, web: %v", grantErr, webErr)
		}
		if got := rolesOf(t, r.db, r.userID); len(got) != 2 {
			t.Fatalf("roles %v", got)
		}
	})
}

// Two requests that race for one account judge the version one after the other.
func TestConcurrentAdminChangesAreSerialisedByTheVersion(t *testing.T) {
	t.Run("two operators with the same version: one wins, the other must read again", func(t *testing.T) {
		r := newRace(t, "two-ops@example.com")
		a, b := platformAdmin(t, r.s, "root-a@example.com"), platformAdmin(t, r.s, "root-b@example.com")
		v := versionOf(t, r.db, r.userID)
		aErr, bErr := r.inOrder(
			func() error {
				_, err := r.s.ApplyAdminChange(ctx, req(a, account.ActionDisable, r.userID, v, "a-1"))
				return err
			},
			func() error {
				_, err := r.s.ApplyAdminChange(ctx, req(b, account.ActionGrantMetroAdmin, r.userID, v, "b-1"))
				return err
			},
		)
		if aErr != nil || !errors.Is(bErr, account.ErrTargetChanged) {
			t.Fatalf("a: %v, b: %v", aErr, bErr)
		}
		if u := r.db.User.GetX(ctx, r.userID); u.AuthVersion != v+1 || u.Status != user.StatusDisabled || len(rolesOf(t, r.db, r.userID)) != 0 {
			t.Fatalf("account: %+v", u)
		}
	})
	t.Run("the same request twice at once: one change, one replay", func(t *testing.T) {
		r := newRace(t, "twice@example.com")
		admin := platformAdmin(t, r.s, "root@example.com")
		v := versionOf(t, r.db, r.userID)
		var got [2]account.AdminResult
		call := func(i int) func() error {
			return func() (err error) {
				got[i], err = r.s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, r.userID, v, "dup"))
				return err
			}
		}
		e1, e2 := r.inOrder(call(0), call(1))
		if e1 != nil || e2 != nil {
			t.Fatalf("errors: %v %v", e1, e2)
		}
		if got[0].Replayed || !got[1].Replayed || got[0].AuthVersion != v+1 || got[1].AuthVersion != v+1 || !got[1].Changed {
			t.Fatalf("results: %+v", got)
		}
		if u := r.db.User.GetX(ctx, r.userID); u.AuthVersion != v+1 {
			t.Fatalf("version advanced %d times", u.AuthVersion-v)
		}
		if n := r.db.AdminMutation.Query().CountX(ctx); n != 1 {
			t.Fatalf("%d receipts", n)
		}
	})
	t.Run("one key for two targets at once: one runs, the other is refused as a reuse", func(t *testing.T) {
		r := newRace(t, "key-a@example.com")
		other := create(t, r.s, "key-b@example.com")
		admin := platformAdmin(t, r.s, "root@example.com")
		results := make(chan error, 2)
		start := make(chan struct{})
		for _, target := range []uuid.UUID{r.userID, other} {
			go func() {
				<-start
				_, err := r.s.ApplyAdminChange(ctx, req(admin, account.ActionDisable, target, 1, "shared-key"))
				results <- err
			}()
		}
		close(start)
		var ok, reused int
		for range 2 {
			switch err := <-results; {
			case err == nil:
				ok++
			case errors.Is(err, account.ErrMutationReused):
				reused++
			default:
				t.Fatalf("unexpected: %v", err)
			}
		}
		if ok != 1 || reused != 1 {
			t.Fatalf("ok %d, reused %d", ok, reused)
		}
		if n := r.db.AdminMutation.Query().CountX(ctx); n != 1 {
			t.Fatalf("%d receipts", n)
		}
	})
}
