package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/application"
	"github.com/kongken/mono/internal/usersrv/ent/externalidentity"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPasswordlessCapabilitiesAndEmailFirstPasswordOverSignedRPC(t *testing.T) {
	s := startMail(t, limits())
	ctx := context.Background()
	tx, err := s.db.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	u, err := tx.User.Create().SetEmailCanonical("external@example.com").SetEmailVerifiedAt(time.Now()).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.ExternalIdentity.Create().SetUserID(u.ID).SetProvider(externalidentity.ProviderGithub).
		SetIssuer("https://github.com").SetSubject("123456").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	me, err := s.account.GetMyAccount(as(u.ID, ""), &usersrvv1.GetMyAccountRequest{})
	if err != nil || me.GetHasPassword() || me.GetUserId() != u.ID.String() {
		t.Fatalf("own capabilities: %v %v", me, err)
	}
	st, err := s.account.GetAuthState(as(u.ID, ""), &usersrvv1.GetAuthStateRequest{})
	if err != nil || st.GetHasPassword() || st.GetAuthVersion() != 1 {
		t.Fatalf("auth state: %v %v", st, err)
	}
	_, err = s.account.AuthenticateWithEmail(anonCtx("203.0.113.77"), &usersrvv1.AuthenticateWithEmailRequest{Email: u.EmailCanonical, Password: password})
	if status.Code(err) != codes.Unauthenticated || reason(t, err) != application.ReasonInvalidCredentials {
		t.Fatalf("passwordless login must be neutral: %v", err)
	}
	_, err = s.account.VerifyPassword(as(u.ID, "203.0.113.77"), &usersrvv1.VerifyPasswordRequest{Password: password})
	if status.Code(err) != codes.Unauthenticated || reason(t, err) != application.ReasonInvalidCredentials {
		t.Fatalf("password reauthentication: %v", err)
	}
	// Registration and both providers are off; recovery is still available.
	s.accounts.SetRegistrationOpen(false)
	if err := s.requestReset(t, u.EmailCanonical, "203.0.113.78"); err != nil {
		t.Fatal(err)
	}
	s.deliver(t)
	mail := s.inbox.to(u.EmailCanonical)
	if len(mail) != 1 {
		t.Fatalf("first-password mail count: %d", len(mail))
	}
	msg := mail[0]
	token := tokenIn(t, msg, "/account/reset-password")
	_, err = s.account.ResetPassword(anonCtx("203.0.113.78"), &usersrvv1.ResetPasswordRequest{Token: token, NewPassword: password})
	if err != nil {
		t.Fatal(err)
	}
	st, err = s.account.GetAuthState(as(u.ID, ""), &usersrvv1.GetAuthStateRequest{})
	if err != nil || !st.GetHasPassword() || st.GetAuthVersion() != 2 {
		t.Fatalf("first password capability/version: %v %v", st, err)
	}
	stored := s.db.User.GetX(ctx, u.ID)
	if stored.PasswordSetAt == nil || stored.PasswordHash == nil {
		t.Fatal("first password did not store hash/time")
	}
	if s.db.ExternalIdentity.Query().Where(externalidentity.UserID(u.ID)).CountX(ctx) != 1 {
		t.Fatal("first password removed binding")
	}
	_, err = s.account.ResetPassword(anonCtx("203.0.113.78"), &usersrvv1.ResetPasswordRequest{Token: token, NewPassword: "another secure password"})
	if reason(t, err) != application.ReasonTokenInvalid || s.db.User.GetX(ctx, u.ID).AuthVersion != 2 {
		t.Fatal("reset replay changed account twice")
	}
	res, err := s.account.AuthenticateWithEmail(anonCtx("203.0.113.79"), &usersrvv1.AuthenticateWithEmailRequest{Email: u.EmailCanonical, Password: password})
	if err != nil || !res.GetHasPassword() || res.GetUserId() != u.ID.String() {
		t.Fatalf("new password login: %v %v", res, err)
	}
	if _, err := s.account.GetMyAccount(as(uuid.New(), ""), &usersrvv1.GetMyAccountRequest{}); status.Code(err) != codes.NotFound {
		t.Fatal("unknown account got capability")
	}
}
