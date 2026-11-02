package account_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/externalidentity"
)

// Seed the future registration result in one transaction. S2 deliberately has
// no public passwordless creation/Link path: those require the S3 proofs.
func passwordless(t *testing.T, db *ent.Client, email string) uuid.UUID {
	t.Helper()
	tx, err := db.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	u, err := tx.User.Create().SetEmailCanonical(email).SetEmailVerifiedAt(time.Now()).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.ExternalIdentity.Create().SetUserID(u.ID).SetProvider(externalidentity.ProviderGoogle).
		SetIssuer("https://accounts.google.com").SetSubject(uuid.NewString()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestPasswordlessAccountHasNoPasswordAndAllPasswordChecksFailNeutrally(t *testing.T) {
	s, db := setup(t)
	id := passwordless(t, db, "external@example.com")
	st, err := s.GetAuthState(ctx, id)
	if err != nil || st.HasPassword || st.AuthVersion != 1 || len(st.Roles) != 0 {
		t.Fatalf("auth state: %+v %v", st, err)
	}
	a, err := s.GetAccount(ctx, id)
	if err != nil || a.HasPassword || a.ID != id {
		t.Fatalf("account: %+v %v", a, err)
	}
	if _, _, err := s.Login(ctx, "external@example.com", password, "login"); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("login: %v", err)
	}
	if _, _, err := s.Login(ctx, "unknown@example.com", password, "unknown"); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("unknown login: %v", err)
	}
	if _, err := s.VerifyPassword(ctx, id, password, "verify"); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("verify: %v", err)
	}
	if _, err := s.ChangePassword(ctx, id, password, "another secure password", "change"); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("change bypassed email proof: %v", err)
	}
	u := db.User.GetX(ctx, id)
	if u.PasswordHash != nil || u.PasswordSetAt != nil || u.AuthVersion != 1 {
		t.Fatal("password checks mutated passwordless account")
	}
	if n := db.ExternalIdentity.Query().Where(externalidentity.UserID(id)).CountX(ctx); n != 1 {
		t.Fatal("password failure removed external identity")
	}
	// Public account tools continue requiring a real password; empty does not
	// silently choose a passwordless registration path.
	if _, err := s.CreateVerifiedUser(ctx, account.NewUser{Email: "unsafe@example.com"}); err == nil {
		t.Fatal("tool created an unproved passwordless account")
	}
}

func TestPasswordSetTimeIsWrittenOnlyOnRealPasswordWrites(t *testing.T) {
	s, db := setup(t)
	id := create(t, s, "password@example.com")
	u := db.User.GetX(ctx, id)
	if u.PasswordSetAt == nil {
		t.Fatal("new password has no set time")
	}
	// Simulate an upgraded historical row; reads/login must not fabricate the
	// unknown password time. The next genuine password write establishes it.
	db.User.UpdateOneID(id).ClearPasswordSetAt().ExecX(ctx)
	if _, st, err := s.Authenticate(ctx, u.EmailCanonical, password); err != nil || !st.HasPassword {
		t.Fatalf("old password login: %v", err)
	}
	if db.User.GetX(ctx, id).PasswordSetAt != nil {
		t.Fatal("login fabricated historical password time")
	}
	if _, err := s.ChangePassword(ctx, id, password, "another secure password", "change"); err != nil {
		t.Fatal(err)
	}
	u = db.User.GetX(ctx, id)
	if u.PasswordSetAt == nil || u.AuthVersion != 2 {
		t.Fatal("password change omitted time/version")
	}
}
