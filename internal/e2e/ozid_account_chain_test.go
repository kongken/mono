package e2e_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"connectrpc.com/connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

// The central landing origin of account mail, as configured on usersrv in the harness
// (auth.apps.ozid). It is deliberately not any product origin.
const mailLandingOrigin = "https://id.example.com"

func (b *Browser) requestRegistrationFrom(ctx context.Context, email, app, path string) error {
	req := connect.NewRequest(&bffv1.RequestRegistrationRequest{Email: email, AppId: app, ReturnPath: path})
	b.Write(ctx, req)
	_, err := b.Auth.RequestRegistration(ctx, req)
	return err
}

func (b *Browser) completeRegistration(ctx context.Context, token, password, display string) (*bffv1.CompleteRegistrationResponse, error) {
	req := connect.NewRequest(&bffv1.CompleteRegistrationRequest{Token: token, Password: password, DisplayName: display})
	b.Write(ctx, req)
	res, err := b.Auth.CompleteRegistration(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) requestResetFrom(ctx context.Context, email, app, path string) error {
	req := connect.NewRequest(&bffv1.RequestPasswordResetRequest{Email: email, AppId: app, ReturnPath: path})
	b.Write(ctx, req)
	_, err := b.Auth.RequestPasswordReset(ctx, req)
	return err
}

func (b *Browser) resetPasswordWith(ctx context.Context, token, next string) (*bffv1.ResetPasswordResponse, error) {
	req := connect.NewRequest(&bffv1.ResetPasswordRequest{Token: token, NewPassword: next})
	b.Write(ctx, req)
	res, err := b.Auth.ResetPassword(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

var mailLink = regexp.MustCompile(`https://[^\s"<]+`)

// linkIn returns the link of the latest mail to email, and fails unless it is the fixed
// central one: configured OzID origin, the fixed path of the purpose, the token only in the
// fragment, nothing about where the user came from.
func linkIn(t *testing.T, h *ChainHarness, email, path string, leaks ...string) string {
	t.Helper()
	msgs := h.Inbox.To(email)
	if len(msgs) == 0 {
		t.Fatalf("no mail for %s", email)
	}
	text := msgs[len(msgs)-1].Text
	token := h.Inbox.TokenIn(t, email)
	want := mailLandingOrigin + path + "#token=" + token
	if got := mailLink.FindString(text); got != want {
		t.Fatalf("mail link %q, want %q", got, want)
	}
	if strings.Contains(strings.ReplaceAll(text, "#token="+token, ""), token) {
		t.Fatal("the token appears outside the fragment")
	}
	for _, leak := range leaks {
		if strings.Contains(text, leak) {
			t.Fatalf("the mail carries %q:\n%s", leak, text)
		}
	}
	return token
}

// The whole loop of #80: a product asks for registration with its own return context,
// the mail lands at OzID whichever app asked, another browser (that only has the mail)
// finishes it, signs in again and is sent back to exactly the validated place. Nothing
// signs in by completing, and an existing session is not touched.
func TestChainOzidRegistrationMailLoop(t *testing.T) {
	h, cookies := ozidHarness(t)
	ctx := context.Background()
	const email = "newcomer@example.com"

	// The request comes from OzID (the only origin that may ask for an account step), on behalf of the
	// metro product: the application is only the validated return context, with a path inside it.
	asker := h.FreshBrowser()
	asker.Origin = ozidOrigin
	if err := asker.requestRegistrationFrom(ctx, email, "metro", "/city/shanghai"); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	token := linkIn(t, h, email, "/account/verify-email", "metro", "shanghai", "localhost")

	// Another browser on another origin, with another account already signed in.
	other := h.FreshBrowser()
	other.Origin = ozidOrigin
	if _, err := other.Register(ctx, "bystander", "", "Bystander"); err != nil {
		t.Fatal(err)
	}
	bystander, err := other.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done, err := other.completeRegistration(ctx, token, E2EPassword, "New")
	if err != nil {
		t.Fatal(err)
	}
	// The bound context comes back, re-validated; no user, no session.
	if done.GetAppId() != "metro" || done.GetReturnPath() != "/city/shanghai" {
		t.Fatalf("completion returned %v", done)
	}
	for _, sc := range cookies.for_("/api/bff.v1.AuthService/CompleteRegistration") {
		t.Errorf("completion set a cookie: %s", sc)
	}
	if after, err := other.Me(ctx); err != nil || after.GetId() != bystander.GetId() {
		t.Fatalf("completing a registration touched the session of the browser: %v, %v", after, err)
	}

	// The new account exists, has to sign in, and is then sent back where it came from.
	fresh := h.FreshBrowser()
	fresh.Origin = ozidOrigin
	if _, err := fresh.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("an anonymous browser is signed in: %v", err)
	}
	if _, err := fresh.LoginWithEmail(ctx, email, E2EPassword); err != nil {
		t.Fatal(err)
	}
	target, err := fresh.resolve(ctx, done.GetAppId(), done.GetReturnPath())
	if err != nil || target.GetReturnUrl() != metroOrigin+"/city/shanghai" {
		t.Fatalf("return target %v, %v", target, err)
	}

	// The token is one-use: a replay is the same uniform failure and names no context.
	if _, err := h.FreshBrowser().completeRegistration(ctx, token, E2EPassword, "New"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("replay: %v", err)
	}
}

func TestChainOzidPasswordResetMailLoop(t *testing.T) {
	h, _ := ozidHarness(t)
	ctx := context.Background()
	reg := h.FreshBrowser()
	if _, err := reg.Register(ctx, "forgetful", "", "F"); err != nil {
		t.Fatal(err)
	}
	const email = "forgetful@example.com"
	oldSession := reg

	asker := h.FreshBrowser()
	asker.Origin = ozidOrigin
	if err := asker.requestResetFrom(ctx, email, "trip-map", ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	token := linkIn(t, h, email, "/account/reset-password", "trip", "localhost")

	// Another browser finishes it.
	other := h.FreshBrowser()
	other.Origin = ozidOrigin
	const next = "a completely new password"
	done, err := other.resetPasswordWith(ctx, token, next)
	if err != nil {
		t.Fatal(err)
	}
	// An app without a path returns to the app's default path.
	if done.GetAppId() != "trip-map" || done.GetReturnPath() != "/" {
		t.Fatalf("reset returned %v", done)
	}
	// Completing signs nobody in, and every existing session is gone.
	if _, err := other.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a reset signed the browser in: %v", err)
	}
	if _, err := oldSession.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a session survived the reset: %v", err)
	}
	if _, err := other.LoginWithEmail(ctx, email, E2EPassword); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the old password still works: %v", err)
	}
	if _, err := other.LoginWithEmail(ctx, email, next); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Me(ctx); err != nil {
		t.Fatal(err)
	}
	// Replay.
	if _, err := h.FreshBrowser().resetPasswordWith(ctx, token, "yet another long password"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("replay: %v", err)
	}
}

// A request names its source, never the landing: an unknown app, an unregistered path
// and anything that is not a canonical pathname are refused before anything is counted
// or sent, whoever is asking.
func TestChainOzidRefusesAWrongSource(t *testing.T) {
	h, _ := ozidHarness(t)
	ctx := context.Background()
	reg := h.FreshBrowser()
	if _, err := reg.Register(ctx, "known", "", "K"); err != nil {
		t.Fatal(err)
	}
	base := h.Inbox.Total()
	b := h.FreshBrowser()
	b.Origin = ozidOrigin
	for _, bad := range []struct{ app, path string }{
		{"other", ""}, {"evil.example.com", ""}, {"http://localhost:5174", "/me"}, {"", "/orphan"},
		{"metro", "https://evil.example/"}, {"metro", "//evil.example"}, {"metro", "/city/../admin"}, {"metro", "/me?x=1"},
		{"metro", "/me#x"}, {"metro", "/city/%2e%2e/me"}, {"trip-map", "/secret"}, {"ozid", "/login"},
	} {
		if err := b.requestRegistrationFrom(ctx, "fresh@example.com", bad.app, bad.path); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("registration from %+v: %v", bad, err)
		}
		if err := b.requestResetFrom(ctx, "known@example.com", bad.app, bad.path); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("reset from %+v: %v", bad, err)
		}
	}
	h.DeliverMail()
	if h.Inbox.Total() != base {
		t.Fatal("a refused source sent mail")
	}
	n, err := h.UsersrvDB.AccountToken.Query().Count(ctx)
	if err != nil || n != 1 { // the one registration of "known"
		t.Fatalf("%d challenges after refused requests (%v)", n, err)
	}
}

// Requests answer the same for every well-formed address: an address that can register
// and one that cannot, a known and an unknown account, get the same neutral text.
func TestChainOzidRequestsAreNeutral(t *testing.T) {
	h, _ := ozidHarness(t)
	ctx := context.Background()
	reg := h.FreshBrowser()
	if _, err := reg.Register(ctx, "taken", "", "T"); err != nil {
		t.Fatal(err)
	}
	b := h.FreshBrowser()
	b.Origin = ozidOrigin
	ask := func(address string, reset bool) string {
		t.Helper()
		if reset {
			req := connect.NewRequest(&bffv1.RequestPasswordResetRequest{Email: address, AppId: "metro"})
			b.Write(ctx, req)
			res, err := b.Auth.RequestPasswordReset(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			return res.Msg.GetMessage()
		}
		req := connect.NewRequest(&bffv1.RequestRegistrationRequest{Email: address, AppId: "metro"})
		b.Write(ctx, req)
		res, err := b.Auth.RequestRegistration(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetMessage()
	}
	if a, c := ask("taken@example.com", false), ask("nobody@example.com", false); a != c || a == "" {
		t.Fatalf("registration answers differ: %q vs %q", a, c)
	}
	if a, c := ask("taken@example.com", true), ask("nobody@example.com", true); a != c || a == "" {
		t.Fatalf("reset answers differ: %q vs %q", a, c)
	}
	h.DeliverMail()
	// One registration mail for the free address, one reset mail for the existing one.
	if len(h.Inbox.To("nobody@example.com")) != 1 || len(h.Inbox.To("taken@example.com")) != 2 {
		t.Fatalf("mails: nobody=%d taken=%d", len(h.Inbox.To("nobody@example.com")), len(h.Inbox.To("taken@example.com")))
	}
}

// Expiry, and states the code never produces but a migration or an operator could:
// an expired challenge, a challenge without a context (every challenge issued before this
// change), and one whose app has left the registry since the mail was requested.
func TestChainOzidExpiredLegacyAndRetiredChallenges(t *testing.T) {
	h, _ := ozidHarness(t)
	ctx := context.Background()
	request := func(address, app, path string) string {
		t.Helper()
		b := h.FreshBrowser()
		b.Origin = ozidOrigin
		if err := b.requestRegistrationFrom(ctx, address, app, path); err != nil {
			t.Fatal(err)
		}
		h.DeliverMail()
		return linkIn(t, h, address, "/account/verify-email")
	}
	complete := func(token string) (*bffv1.CompleteRegistrationResponse, error) {
		b := h.FreshBrowser()
		b.Origin = ozidOrigin
		return b.completeRegistration(ctx, token, E2EPassword, "X")
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := h.UsersrvSQL.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	// Expired: the account is not created and nothing is returned.
	expired := request("expired@example.com", "metro", "/me")
	exec(`UPDATE account_tokens SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 hour' WHERE email_canonical = 'expired@example.com'`)
	if _, err := complete(expired); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expired token: %v", err)
	}
	b := h.FreshBrowser()
	b.Origin = ozidOrigin
	if _, err := b.LoginWithEmail(ctx, "expired@example.com", E2EPassword); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("an expired link created an account: %v", err)
	}

	// A challenge issued before the context existed: both columns NULL. It completes, and
	// returns to no app (OzID's own overview) instead of guessing one.
	legacy := request("legacy@example.com", "metro", "/me")
	exec(`UPDATE account_tokens SET return_app_id = NULL, return_path = NULL WHERE email_canonical = 'legacy@example.com'`)
	done, err := complete(legacy)
	if err != nil || done.GetAppId() != "" || done.GetReturnPath() != "" {
		t.Fatalf("legacy challenge: %v, %v", done, err)
	}

	// The app left the registry (or was never one the registry knows) after the request.
	// The account step has already succeeded; the target is silently dropped, never an error.
	retired := request("retired@example.com", "metro", "/me")
	exec(`UPDATE account_tokens SET return_app_id = 'retired-app' WHERE email_canonical = 'retired@example.com'`)
	done, err = complete(retired)
	if err != nil || done.GetAppId() != "" || done.GetReturnPath() != "" {
		t.Fatalf("retired target: %v, %v", done, err)
	}
	if _, err := b.LoginWithEmail(ctx, "retired@example.com", E2EPassword); err != nil {
		t.Fatalf("the account of a retired target was not created: %v", err)
	}

	// A path the registry no longer allows is dropped the same way.
	narrowed := request("narrowed@example.com", "metro", "/me")
	exec(`UPDATE account_tokens SET return_path = '/was-allowed-once' WHERE email_canonical = 'narrowed@example.com'`)
	done, err = complete(narrowed)
	if err != nil || done.GetAppId() != "" || done.GetReturnPath() != "" {
		t.Fatalf("narrowed target: %v, %v", done, err)
	}
}

// Without a navigation registry (every deployment that has not configured the user center
// yet) a request without a context works as before, stores no context and completes
// returning to no app. A request that carries a context is refused (next test).
func TestChainWithoutARegistryAContextFreeFlowStillWorks(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	const email = "noreg@example.com"
	if err := b.requestRegistrationFrom(ctx, email, "", ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	token := linkIn(t, h, email, "/account/verify-email")
	n, err := h.UsersrvDB.AccountToken.Query().Count(ctx)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if tok := h.UsersrvDB.AccountToken.Query().OnlyX(ctx); tok.ReturnAppID != nil || tok.ReturnPath != nil {
		t.Fatalf("a context was stored for a context-free request: %+v", tok)
	}
	done, err := b.completeRegistration(ctx, token, E2EPassword, "N")
	if err != nil || done.GetAppId() != "" || done.GetReturnPath() != "" {
		t.Fatalf("completion: %v, %v", done, err)
	}
}
