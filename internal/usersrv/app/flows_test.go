package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/app"
	"github.com/kongken/mono/internal/usersrv/application"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/accounttoken"
	"github.com/kongken/mono/internal/usersrv/ent/mailjob"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/mailer"
	"github.com/kongken/mono/internal/usersrv/outbox"
	"github.com/kongken/mono/internal/usersrv/ratelimit"
	"github.com/kongken/mono/internal/usersrv/testutil"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The tests in this file drive the real usersrv gRPC server (real PostgreSQL,
// Redis, outbox worker and rate limits) and replace only the far end of the
// mail: an inbox that records what a vendor would have been handed. Tokens are
// read out of the delivered mail exactly as a user would click them.

// inbox is the local fake mail sender.
type inbox struct {
	mu   sync.Mutex
	msgs []mailer.Message
}

func (i *inbox) Send(_ context.Context, msg mailer.Message) (mailer.Result, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.msgs = append(i.msgs, msg)
	return mailer.Result{}, nil
}

func (i *inbox) to(email string) []mailer.Message {
	i.mu.Lock()
	defer i.mu.Unlock()
	var out []mailer.Message
	for _, m := range i.msgs {
		if m.To == email {
			out = append(out, m)
		}
	}
	return out
}

func (i *inbox) total() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.msgs)
}

var linkToken = regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`)

// tokenIn returns the token of the verification or reset link in a mail, and
// checks the link is the fixed one: configured origin, fixed path, token only
// in the fragment.
func tokenIn(t *testing.T, msg mailer.Message, wantPath string) string {
	t.Helper()
	m := linkToken.FindStringSubmatch(msg.Text)
	if m == nil {
		t.Fatalf("no link in mail:\n%s", msg.Text)
	}
	if !strings.Contains(msg.Text, "https://id.example.com"+wantPath+"#token="+m[1]) {
		t.Fatalf("link is not origin + %s + #token: %s", wantPath, msg.Text)
	}
	if strings.Contains(strings.ReplaceAll(msg.Text, "#token="+m[1], ""), m[1]) {
		t.Fatal("token appears outside the fragment")
	}
	return m[1]
}

// generous raises every limit so flow tests are not stopped by the defaults;
// limit tests use limits().
func generous() application.Limits {
	l := limits()
	for _, r := range []*ratelimit.Rule{&l.PasswordPerAccount, &l.PasswordPerIP, &l.ProfileWritePerUser, &l.DeletionPerUser,
		&l.TokenSubmitPerIP, &l.TokenSubmitPerToken, &l.Mail.RegistrationPerIP, &l.Mail.ResetPerIP, &l.Mail.RegistrationEmailCooldown,
		&l.Mail.RegistrationPerEmail, &l.Mail.ResetEmailCooldown, &l.Mail.ResetPerEmail, &l.Mail.MailPerEmail, &l.Mail.MailGlobal} {
		r.Limit = 10000
	}
	return l
}

type mailStack struct {
	*stack
	inbox *inbox
}

func startMail(t *testing.T, lim application.Limits) *mailStack {
	t.Helper()
	in := &inbox{}
	s := startWith(t, lim, func(db *ent.Client) *outbox.Service {
		m, err := mailer.NewWithSender(mailer.Config{Provider: "sendgrid", FromEmail: "no-reply@example.com", SendGrid: mailer.SendGridConfig{APIKey: "SG.key"}}, in)
		if err != nil {
			t.Fatal(err)
		}
		links, err := mailer.NewLinks(map[string]string{"ozid": "https://id.example.com"}, false)
		if err != nil {
			t.Fatal(err)
		}
		keys, err := (outbox.EncryptionConfig{ActiveKeyID: "k1", Keys: []outbox.KeyConfig{
			{ID: "k1", Key: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("e", 32)))}}}).Keyring()
		if err != nil {
			t.Fatal(err)
		}
		svc, err := outbox.New(db, keys, links, m, outbox.Settings{RegistrationTTL: 30 * time.Minute, ResetTTL: 15 * time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	})
	// The tests run the outbox by hand (deliver), so a background pass cannot
	// deliver or settle a job between two steps of a scenario.
	for _, w := range s.res.Workers {
		w.Stop()
	}
	return &mailStack{stack: s, inbox: in}
}

// deliver runs the outbox until no job is waiting or being sent.
func (s *mailStack) deliver(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := s.res.Mail.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		n, err := s.db.MailJob.Query().Where(mailjob.StatusIn(mailjob.StatusPending, mailjob.StatusSending)).Count(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d mail jobs never settled", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// anonCtx is a call with no user, as the BFF makes for the anonymous methods.
func anonCtx(ip string) context.Context {
	return rpcauth.WithCall(context.Background(), rpcauth.Call{ClientIP: ip, RequestID: "req-anon"})
}

func (s *mailStack) requestRegistration(t *testing.T, email, ip string) error {
	t.Helper()
	_, err := s.account.RequestRegistration(anonCtx(ip), &usersrvv1.RequestRegistrationRequest{Email: email, AppId: "trip-map"})
	return err
}

func (s *mailStack) requestReset(t *testing.T, email, ip string) error {
	t.Helper()
	_, err := s.account.RequestPasswordReset(anonCtx(ip), &usersrvv1.RequestPasswordResetRequest{Email: email, AppId: "metro"})
	return err
}

func (s *mailStack) complete(token, pw, name, ip string) error {
	_, err := s.account.CompleteRegistration(anonCtx(ip), &usersrvv1.CompleteRegistrationRequest{Token: token, Password: pw, DisplayName: name})
	return err
}

func (s *mailStack) reset(token, pw, ip string) error {
	_, err := s.account.ResetPassword(anonCtx(ip), &usersrvv1.ResetPasswordRequest{Token: token, NewPassword: pw})
	return err
}

func (s *mailStack) login(email, pw, ip string) (*usersrvv1.AuthenticateWithEmailResponse, error) {
	return s.account.AuthenticateWithEmail(anonCtx(ip), &usersrvv1.AuthenticateWithEmailRequest{Email: email, Password: pw})
}

// register runs the whole registration of one address and returns its user ID.
func (s *mailStack) register(t *testing.T, email, pw string) uuid.UUID {
	t.Helper()
	if err := s.requestRegistration(t, email, "203.0.113.1"); err != nil {
		t.Fatal(err)
	}
	s.deliver(t)
	msgs := s.inbox.to(email)
	token := tokenIn(t, msgs[len(msgs)-1], "/account/verify-email")
	if err := s.complete(token, pw, "新用户", "203.0.113.1"); err != nil {
		t.Fatal(err)
	}
	res, err := s.login(email, pw, "203.0.113.1")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := uuid.Parse(res.UserId)
	return id
}

func (s *mailStack) resetToken(t *testing.T, email string) string {
	t.Helper()
	before := len(s.inbox.to(email))
	if err := s.requestReset(t, email, "203.0.113.2"); err != nil {
		t.Fatal(err)
	}
	s.deliver(t)
	msgs := s.inbox.to(email)
	if len(msgs) != before+1 {
		t.Fatalf("%d reset mails for %s, want %d", len(msgs), email, before+1)
	}
	return tokenIn(t, msgs[len(msgs)-1], "/account/reset-password")
}

func wantReason(t *testing.T, err error, code codes.Code, reasonWanted string) {
	t.Helper()
	if status.Code(err) != code || reason(t, err) != reasonWanted {
		t.Fatalf("got %v (reason %q), want %v %q", err, reason(t, err), code, reasonWanted)
	}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (s *mailStack) users(t *testing.T) int {
	t.Helper()
	n, err := s.db.User.Query().Count(context.Background())
	ok(t, err)
	return n
}

func (s *mailStack) authVersion(t *testing.T, id uuid.UUID) int64 {
	t.Helper()
	return s.db.User.GetX(context.Background(), id).AuthVersion
}

// Spec scenarios 2, 5, 6 and the closed loop of the task: request, mail,
// verify (password chosen only now), login, reset, login again.
func TestRegistrationLoginResetClosedLoop(t *testing.T) {
	s := startMail(t, generous())
	const email = "Alice+Tag@Example.com"
	const canonical = "alice+tag@example.com"

	// Requesting creates no account and no session, and stores no password.
	ok(t, s.requestRegistration(t, email, "203.0.113.1"))
	if s.users(t) != 0 {
		t.Fatal("a request created an account")
	}
	if _, err := s.login(email, password, ""); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("an unverified address can log in: %v", err)
	}
	s.deliver(t)
	msgs := s.inbox.to(canonical)
	if len(msgs) != 1 {
		t.Fatalf("%d mails", len(msgs))
	}
	token := tokenIn(t, msgs[0], "/account/verify-email")
	if tok := s.db.AccountToken.Query().OnlyX(context.Background()); tok.ConsumedAt != nil || s.users(t) != 0 {
		t.Fatal("delivering the mail consumed the token or created an account (a scanner GET must not)")
	}

	// A weak password is a field error and does not burn the token.
	err := s.complete(token, "short", "新用户", "")
	if status.Code(err) != codes.InvalidArgument || reason(t, err) != application.ReasonInvalidArgument {
		t.Fatalf("weak password: %v", err)
	}
	// Verification sets the password and the nickname and signs nobody in.
	resp, err := s.account.CompleteRegistration(anonCtx(""), &usersrvv1.CompleteRegistrationRequest{Token: token, Password: password, DisplayName: " 小明 "})
	ok(t, err)
	// The response carries nothing but the return context bound to the challenge: no
	// user, no session, no token.
	if resp.GetAppId() != "trip-map" || resp.GetReturnPath() != "" || len(resp.ProtoReflect().GetUnknown()) != 0 {
		t.Fatalf("completion returned %q", resp.String())
	}
	if s.users(t) != 1 {
		t.Fatal("no account")
	}
	u := s.db.User.Query().OnlyX(context.Background())
	if u.EmailCanonical != canonical || u.DisplayName != "小明" || u.Status != user.StatusActive || u.AuthVersion != 1 || u.EmailVerifiedAt.IsZero() ||
		!strings.HasPrefix(u.PasswordHash, "$argon2id$") || strings.Contains(u.PasswordHash, password) {
		t.Fatalf("new account: %+v", u)
	}
	// The same link is dead now.
	wantReason(t, s.complete(token, password, "", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)

	// Login.
	res, err := s.login(" ALICE+tag@example.com ", password, "203.0.113.9")
	ok(t, err)
	if res.UserId != u.ID.String() || res.Profile.DisplayName != "小明" || res.AuthVersion != 1 || len(res.Roles) != 0 {
		t.Fatalf("login: %+v", res)
	}

	// Reset: asking changes nothing about the account.
	resetTok := s.resetToken(t, canonical)
	if v := s.authVersion(t, u.ID); v != 1 {
		t.Fatalf("a reset request moved auth_version to %d", v)
	}
	if _, err = s.login(canonical, password, ""); err != nil {
		t.Fatalf("a reset request broke the current password: %v", err)
	}
	// Weak new password: field error, token stays usable.
	if err = s.reset(resetTok, "short", ""); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("weak reset password: %v", err)
	}
	const newPassword = "a much better password"
	rresp, err := s.account.ResetPassword(anonCtx(""), &usersrvv1.ResetPasswordRequest{Token: resetTok, NewPassword: newPassword})
	ok(t, err)
	if rresp.GetAppId() != "metro" || rresp.GetReturnPath() != "" || len(rresp.ProtoReflect().GetUnknown()) != 0 {
		t.Fatalf("reset returned %q", rresp.String())
	}
	if v := s.authVersion(t, u.ID); v != 2 {
		t.Fatalf("auth_version after reset = %d, want 2", v)
	}
	// The old session version is stale; the old password fails; the new one works; nothing logged in.
	st, err := s.account.GetAuthState(as(u.ID, ""), &usersrvv1.GetAuthStateRequest{})
	if err != nil || st.AuthVersion != 2 {
		t.Fatalf("state: %+v %v", st, err)
	}
	if _, err = s.login(canonical, password, ""); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("old password still works: %v", err)
	}
	res, err = s.login(canonical, newPassword, "")
	ok(t, err)
	if res.AuthVersion != 2 {
		t.Fatalf("relogin: %+v", res)
	}
	// Replaying the used link fails.
	wantReason(t, s.reset(resetTok, "yet another password", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)
	if _, err = s.login(canonical, newPassword, ""); err != nil {
		t.Fatalf("replay changed the password: %v", err)
	}

	// Own-account methods through the same stack: change password, reauthentication, profile.
	ctx := as(u.ID, "203.0.113.3")
	if _, err = s.account.VerifyPassword(ctx, &usersrvv1.VerifyPasswordRequest{Password: "wrong wrong wrong"}); reason(t, err) != application.ReasonInvalidCredentials {
		t.Fatalf("reauth with a wrong password: %v", err)
	}
	if v, err := s.account.VerifyPassword(ctx, &usersrvv1.VerifyPasswordRequest{Password: newPassword}); err != nil || v.AuthVersion != 2 {
		t.Fatalf("reauth: %+v %v", v, err)
	}
	pr, err := s.account.UpdateMyProfile(ctx, &usersrvv1.UpdateMyProfileRequest{DisplayName: "改名了"})
	if err != nil || pr.Profile.ProfileVersion != 2 {
		t.Fatalf("profile: %+v %v", pr, err)
	}
	if v := s.authVersion(t, u.ID); v != 2 {
		t.Fatalf("a profile write moved auth_version to %d", v)
	}
	cp, err := s.account.ChangePassword(ctx, &usersrvv1.ChangePasswordRequest{CurrentPassword: newPassword, NewPassword: "the third password!"})
	if err != nil || cp.AuthVersion != 3 {
		t.Fatalf("change password: %+v %v", cp, err)
	}
	if _, err = s.login(canonical, newPassword, ""); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("old password after change: %v", err)
	}
	if _, err = s.login(canonical, "the third password!", ""); err != nil {
		t.Fatal(err)
	}
}

// Spec scenario 3: a well-formed address gets the same answer whatever it is.
func TestRequestsAnswerTheSameForEveryAddress(t *testing.T) {
	s := startMail(t, generous())
	active := s.register(t, "active@example.com", password)
	disabled := s.register(t, "disabled@example.com", password)
	deleting := s.register(t, "deleting@example.com", password)
	op := account.Operator{Name: "ops", Reason: "t"}
	_, err := s.accounts.SetDisabled(context.Background(), op, disabled, true)
	ok(t, err)
	_, err = s.account.BeginDeletion(as(deleting, ""), &usersrvv1.BeginDeletionRequest{MutationId: "m1"})
	ok(t, err)
	_ = active
	s.deliver(t)
	base := s.inbox.total()

	addrs := []string{"active@example.com", "disabled@example.com", "deleting@example.com", "nobody@example.com"}
	for _, a := range addrs {
		reg, err1 := s.account.RequestRegistration(anonCtx("198.51.100.1"), &usersrvv1.RequestRegistrationRequest{Email: a, AppId: "trip-map"})
		rst, err2 := s.account.RequestPasswordReset(anonCtx("198.51.100.1"), &usersrvv1.RequestPasswordResetRequest{Email: a, AppId: "trip-map"})
		if err1 != nil || err2 != nil || reg.String() != "" || rst.String() != "" {
			t.Fatalf("%s: %v %v", a, err1, err2)
		}
	}
	s.deliver(t)
	// Registered, disabled and deleting addresses get no registration link; only the
	// unknown one does. Only the active account gets a reset link.
	got := map[string]int{}
	for _, a := range addrs {
		got[a] = len(s.inbox.to(a))
	}
	if s.inbox.total()-base != 2 {
		t.Fatalf("%d new mails, want 2 (%v)", s.inbox.total()-base, got)
	}
	// Each address has the one mail of its own registration; the unknown address got a
	// registration link, the active account got a reset link, nobody else got anything.
	want := map[string]int{"active@example.com": 2, "disabled@example.com": 1, "deleting@example.com": 1, "nobody@example.com": 1}
	for a, n := range want {
		if got[a] != n {
			t.Fatalf("mails per address: %v, want %v", got, want)
		}
	}
	// Both the disabled and the deleting account were never given any challenge after the state change.
	for _, id := range []uuid.UUID{disabled, deleting} {
		n, err := s.db.AccountToken.Query().Where(accounttoken.UserID(id)).Count(context.Background())
		ok(t, err)
		if n != 0 {
			t.Fatalf("a non-active account got a reset token")
		}
	}
	// Malformed addresses and malformed return contexts are field errors and change nothing.
	for _, bad := range []string{"", "a@b", "x@example.com, y@example.com", "a@example.com\r\nBcc: v@example.com", "用户@example.com"} {
		_, err := s.account.RequestRegistration(anonCtx("198.51.100.2"), &usersrvv1.RequestRegistrationRequest{Email: bad, AppId: "trip-map"})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%q accepted: %v", bad, err)
		}
		_, err = s.account.RequestPasswordReset(anonCtx("198.51.100.2"), &usersrvv1.RequestPasswordResetRequest{Email: bad, AppId: "trip-map"})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("reset %q accepted: %v", bad, err)
		}
	}
	// A malformed return context is a field error too: nothing is counted or queued.
	for _, ctxIn := range []struct{ app, path string }{
		{"Trip-Map", ""}, {"https://evil.example", ""}, {"", "/orphan-path"}, {"metro", "https://evil.example/"}, {"metro", "//evil.example"},
		{"metro", "/a/../b"}, {"metro", "/a?x=1"}, {"metro", "/a#frag"}, {"metro", "/%2e%2e/"}, {"metro", "no-slash"}, {"metro", "/" + strings.Repeat("a", 300)},
		{strings.Repeat("a", 65), ""},
	} {
		_, err := s.account.RequestRegistration(anonCtx(""), &usersrvv1.RequestRegistrationRequest{Email: "fresh@example.com", AppId: ctxIn.app, ReturnPath: ctxIn.path})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("context %+v: %v", ctxIn, err)
		}
		_, err = s.account.RequestPasswordReset(anonCtx(""), &usersrvv1.RequestPasswordResetRequest{Email: "active@example.com", AppId: ctxIn.app, ReturnPath: ctxIn.path})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("reset context %+v: %v", ctxIn, err)
		}
	}
	s.deliver(t)
	if s.inbox.total()-base != 2 {
		t.Fatal("a rejected request queued mail")
	}
}

// Spec scenario 4 and the unusable-link rules.
func TestUnusableRegistrationTokens(t *testing.T) {
	s := startMail(t, generous())
	newToken := func(email string) string {
		ok(t, s.requestRegistration(t, email, ""))
		s.deliver(t)
		msgs := s.inbox.to(email)
		return tokenIn(t, msgs[len(msgs)-1], "/account/verify-email")
	}
	tooLate := func() { s.accounts.SetClock(func() time.Time { return time.Now().Add(31 * time.Minute) }) }
	notTooLate := func() { s.accounts.SetClock(func() time.Time { return time.Now() }) }

	// Malformed and unknown tokens.
	for _, tok := range []string{"", "short", strings.Repeat("A", 43), strings.Repeat("!", 43), strings.Repeat("A", 44), "a b"} {
		wantReason(t, s.complete(tok, password, "", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)
	}

	// Expired.
	expired := newToken("expired@example.com")
	tooLate()
	wantReason(t, s.complete(expired, password, "", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)
	notTooLate()
	if s.users(t) != 0 {
		t.Fatal("an expired link created an account")
	}

	// A reset token is not a registration token and vice versa; a failed use changes nothing.
	s.register(t, "victim@example.com", password)
	resetTok := s.resetToken(t, "victim@example.com")
	wantReason(t, s.complete(resetTok, "attacker chosen password", "", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)
	regTok := newToken("other@example.com")
	wantReason(t, s.reset(regTok, "attacker chosen password", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)
	if _, err := s.login("victim@example.com", password, ""); err != nil {
		t.Fatalf("a wrong-purpose token changed the password: %v", err)
	}
	if s.users(t) != 1 {
		t.Fatalf("%d accounts", s.users(t))
	}

	// An attacker who requested a link for someone else's address cannot preset a
	// password: the account only exists once the mailbox owner completes it, with
	// the owner's own token, and the token cannot be reused afterwards.
	ownerToken := newToken("owner@example.com")
	ok(t, s.complete(ownerToken, password, "", ""))
	wantReason(t, s.complete(ownerToken, "attacker chosen password", "", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)
	if _, err := s.login("owner@example.com", password, ""); err != nil {
		t.Fatal(err)
	}

	// An older link for an address that has since been registered is unusable and
	// overwrites neither the password nor the roles.
	first := newToken("race@example.com")
	second := newToken("race@example.com")
	ok(t, s.complete(second, password, "", ""))
	wantReason(t, s.complete(first, "attacker chosen password", "", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)
	if _, err := s.login("race@example.com", password, ""); err != nil {
		t.Fatal(err)
	}
	// Both tokens are consumed together, the whole address's pending mail is cancelled.
	n, err := s.db.AccountToken.Query().Where(accounttoken.EmailCanonical("race@example.com"), accounttoken.ConsumedAtIsNil()).Count(context.Background())
	ok(t, err)
	if n != 0 {
		t.Fatalf("%d registration tokens of a registered address stay usable", n)
	}
	// Once the account exists, a request for the address yields no new link.
	before := s.inbox.total()
	ok(t, s.requestRegistration(t, "race@example.com", ""))
	s.deliver(t)
	if s.inbox.total() != before {
		t.Fatal("a registered address was mailed a registration link")
	}
}

// A token whose mail is still queued when the account is created is cancelled
// before it is sent, and the link a vendor already accepted is dead.
func TestPendingMailIsCancelledWhenRegistrationCompletes(t *testing.T) {
	s := startMail(t, generous())
	ok(t, s.requestRegistration(t, "queued@example.com", ""))
	s.deliver(t)
	tok := tokenIn(t, s.inbox.to("queued@example.com")[0], "/account/verify-email")
	// A second request whose mail is not sent yet.
	ok(t, s.requestRegistration(t, "queued@example.com", ""))
	ok(t, s.complete(tok, password, "", ""))
	s.deliver(t)
	if n := len(s.inbox.to("queued@example.com")); n != 1 {
		t.Fatalf("%d mails; the queued duplicate should have been cancelled", n)
	}
	cancelled := s.db.MailJob.Query().Where(mailjob.StatusEQ(mailjob.StatusCancelled)).CountX(context.Background())
	if cancelled != 1 {
		t.Fatalf("%d cancelled jobs", cancelled)
	}
	if j := s.db.MailJob.Query().Where(mailjob.StatusEQ(mailjob.StatusCancelled)).OnlyX(context.Background()); j.Ciphertext != nil || j.KeyID != nil {
		t.Fatal("a cancelled job keeps its secrets")
	}
}

// Concurrent completions create exactly one account and one winner.
func TestConcurrentRegistrationCompletion(t *testing.T) {
	s := startMail(t, generous())
	race := func(n int, call func(i int) error) (okCount int, errs []error) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				err := call(i)
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					okCount++
				} else {
					errs = append(errs, err)
				}
			}()
		}
		close(start)
		wg.Wait()
		return
	}

	// One token, many submissions with different passwords.
	ok(t, s.requestRegistration(t, "one@example.com", ""))
	s.deliver(t)
	token := tokenIn(t, s.inbox.to("one@example.com")[0], "/account/verify-email")
	won, errs := race(8, func(i int) error {
		return s.complete(token, fmt.Sprintf("password number %d here", i), "", "")
	})
	if won != 1 || s.users(t) != 1 {
		t.Fatalf("%d winners, %d accounts: %v", won, s.users(t), errs)
	}
	for _, err := range errs {
		wantReason(t, err, codes.FailedPrecondition, application.ReasonTokenInvalid)
	}

	// Two tokens of one address racing: still one account.
	ok(t, s.requestRegistration(t, "two@example.com", ""))
	ok(t, s.requestRegistration(t, "two@example.com", ""))
	s.deliver(t)
	var tokens []string
	for _, m := range s.inbox.to("two@example.com") {
		tokens = append(tokens, tokenIn(t, m, "/account/verify-email"))
	}
	if len(tokens) != 2 {
		t.Fatalf("%d tokens", len(tokens))
	}
	won, errs = race(8, func(i int) error {
		return s.complete(tokens[i%2], fmt.Sprintf("password number %d here", i), "", "")
	})
	if won != 1 {
		t.Fatalf("%d winners for one address: %v", won, errs)
	}
	for _, err := range errs {
		wantReason(t, err, codes.FailedPrecondition, application.ReasonTokenInvalid)
	}
	if n := s.db.User.Query().Where(user.EmailCanonical("two@example.com")).CountX(context.Background()); n != 1 {
		t.Fatalf("%d accounts for one address", n)
	}
}

// Spec scenario 6: concurrent resets, one wins; the loser overwrites nothing.
func TestConcurrentPasswordResets(t *testing.T) {
	s := startMail(t, generous())
	id := s.register(t, "reset@example.com", password)

	race := func(tokens []string) (winner int, errs []error) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		results := make([]error, len(tokens))
		start := make(chan struct{})
		for i := range tokens {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				err := s.reset(tokens[i], fmt.Sprintf("new password number %d", i), "")
				mu.Lock()
				results[i] = err
				mu.Unlock()
			}()
		}
		close(start)
		wg.Wait()
		winner = -1
		for i, err := range results {
			if err == nil {
				if winner >= 0 {
					t.Fatalf("two resets succeeded")
				}
				winner = i
			} else {
				errs = append(errs, err)
			}
		}
		return
	}

	same := s.resetToken(t, "reset@example.com")
	winner, errs := race([]string{same, same, same, same})
	if winner < 0 || len(errs) != 3 {
		t.Fatalf("winner %d, errors %v", winner, errs)
	}
	for _, err := range errs {
		wantReason(t, err, codes.FailedPrecondition, application.ReasonTokenInvalid)
	}
	if v := s.authVersion(t, id); v != 2 {
		t.Fatalf("auth_version = %d, want exactly one bump", v)
	}
	if _, err := s.login("reset@example.com", fmt.Sprintf("new password number %d", winner), ""); err != nil {
		t.Fatalf("the winner's password is not the one stored: %v", err)
	}

	// Two different valid tokens of one account: one wins, the other is refused.
	a, b := s.resetToken(t, "reset@example.com"), s.resetToken(t, "reset@example.com")
	winner, errs = race([]string{a, b})
	if winner < 0 || len(errs) != 1 {
		t.Fatalf("winner %d, errors %v", winner, errs)
	}
	wantReason(t, errs[0], codes.FailedPrecondition, application.ReasonTokenInvalid)
	if v := s.authVersion(t, id); v != 3 {
		t.Fatalf("auth_version = %d, want 3", v)
	}
	if _, err := s.login("reset@example.com", fmt.Sprintf("new password number %d", winner), ""); err != nil {
		t.Fatal(err)
	}
}

// A reset token is bound to the account's state at issue time.
func TestResetTokenIsBoundToAccountAndVersion(t *testing.T) {
	s := startMail(t, generous())
	op := account.Operator{Name: "ops", Reason: "t"}

	// Changing the password voids outstanding reset links.
	idA := s.register(t, "a@example.com", password)
	tokA := s.resetToken(t, "a@example.com")
	_, err := s.account.ChangePassword(as(idA, ""), &usersrvv1.ChangePasswordRequest{CurrentPassword: password, NewPassword: "changed password 1"})
	ok(t, err)
	wantReason(t, s.reset(tokA, "attacker chosen password", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)

	// A role change (any auth_version bump) voids them too.
	idB := s.register(t, "b@example.com", password)
	tokB := s.resetToken(t, "b@example.com")
	_, err = s.accounts.GrantRole(context.Background(), op, idB, account.RoleMetroAdmin)
	ok(t, err)
	wantReason(t, s.reset(tokB, "attacker chosen password", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)

	// Disabled accounts: the link fails the same way, and re-enabling does not revive it.
	idC := s.register(t, "c@example.com", password)
	tokC := s.resetToken(t, "c@example.com")
	_, err = s.accounts.SetDisabled(context.Background(), op, idC, true)
	ok(t, err)
	wantReason(t, s.reset(tokC, "attacker chosen password", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)
	_, err = s.accounts.SetDisabled(context.Background(), op, idC, false)
	ok(t, err)
	wantReason(t, s.reset(tokC, "attacker chosen password", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)
	if _, err = s.login("c@example.com", password, ""); err != nil {
		t.Fatalf("a refused reset changed the password: %v", err)
	}

	// An account in deletion.
	idD := s.register(t, "d@example.com", password)
	tokD := s.resetToken(t, "d@example.com")
	_, err = s.account.BeginDeletion(as(idD, ""), &usersrvv1.BeginDeletionRequest{MutationId: "m"})
	ok(t, err)
	wantReason(t, s.reset(tokD, "attacker chosen password", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)

	// An expired token.
	s.register(t, "e@example.com", password)
	tokE := s.resetToken(t, "e@example.com")
	s.accounts.SetClock(func() time.Time { return time.Now().Add(16 * time.Minute) })
	wantReason(t, s.reset(tokE, "attacker chosen password", ""), codes.FailedPrecondition, application.ReasonTokenInvalid)
	s.accounts.SetClock(func() time.Time { return time.Now() })
	// ...and the same token, still inside its lifetime, works.
	ok(t, s.reset(tokE, "this one is accepted", ""))
	if _, err = s.login("e@example.com", "this one is accepted", ""); err != nil {
		t.Fatal(err)
	}

	// The reset mail of a disabled or deleting account is not even sent: the job is
	// re-checked right before delivery.
	idF := s.register(t, "f@example.com", password)
	ok(t, s.requestReset(t, "f@example.com", ""))
	_, err = s.accounts.SetDisabled(context.Background(), op, idF, true)
	ok(t, err)
	before := len(s.inbox.to("f@example.com"))
	s.deliver(t)
	if len(s.inbox.to("f@example.com")) != before {
		t.Fatal("a reset mail was sent for a disabled account")
	}
}

// Spec scenarios 3 and 5: every failed login looks the same, the audit trail
// records attempts against real accounts without any address or password.
func TestLoginIsUniformAndAudited(t *testing.T) {
	s := startMail(t, generous())
	op := account.Operator{Name: "ops", Reason: "t"}
	s.register(t, "real@example.com", password)
	disabled := s.register(t, "off@example.com", password)
	deleting := s.register(t, "going@example.com", password)
	_, err := s.accounts.SetDisabled(context.Background(), op, disabled, true)
	ok(t, err)
	_, err = s.account.BeginDeletion(as(deleting, ""), &usersrvv1.BeginDeletionRequest{MutationId: "m"})
	ok(t, err)

	type outcome struct {
		code    codes.Code
		message string
		reason  string
	}
	seen := map[string]outcome{}
	for name, c := range map[string][2]string{
		"unknown":        {"nobody@example.com", password},
		"wrong password": {"real@example.com", "not the password!!"},
		"disabled":       {"off@example.com", password},
		"deleting":       {"going@example.com", password},
	} {
		_, err := s.login(c[0], c[1], "")
		st, _ := status.FromError(err)
		seen[name] = outcome{st.Code(), st.Message(), reason(t, err)}
	}
	want := outcome{codes.Unauthenticated, "invalid credentials", application.ReasonInvalidCredentials}
	for name, got := range seen {
		if got != want {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
	// Format errors may be field errors (spec 5.2) and name only the field.
	if _, err = s.login("not an email", password, ""); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("malformed email: %v", err)
	}
	if _, err = s.login("real@example.com", "short", ""); status.Code(err) != codes.Unauthenticated && status.Code(err) != codes.InvalidArgument {
		t.Fatalf("short password: %v", err)
	}

	// Audit: rows exist for attempts on real accounts and carry neither address nor secret.
	rows := s.db.SecurityAudit.Query().Where(securityaudit.Action("login")).AllX(context.Background())
	results := map[string]int{}
	for _, r := range rows {
		results[r.Result]++
	}
	if results["ok"] < 3 || results["wrong_password"] != 2 || results["inactive"] != 2 {
		t.Fatalf("login audit results: %v", results)
	}
	for _, r := range s.db.SecurityAudit.Query().AllX(context.Background()) {
		all := strings.Join([]string{r.Actor, r.Action, r.Result, r.Reason, r.RequestID}, " ")
		if strings.Contains(all, "@") || strings.Contains(all, password) || strings.Contains(all, "not the password") {
			t.Fatalf("audit row leaks an address or a password: %+v", r)
		}
	}
}

// State and credential changes write their audit row in the same transaction.
func TestFlowsAuditStateChanges(t *testing.T) {
	s := startMail(t, generous())
	id := s.register(t, "audit@example.com", password)
	tok := s.resetToken(t, "audit@example.com")
	ok(t, s.reset(tok, "a different password", ""))
	count := func(action, result string) int {
		return s.db.SecurityAudit.Query().Where(securityaudit.Action(action), securityaudit.Result(result)).CountX(context.Background())
	}
	for _, c := range [][2]string{{"account.create", "ok"}, {"password.reset", "ok"}, {"registration.request", "issued"}, {"password_reset.request", "issued"}} {
		if count(c[0], c[1]) != 1 {
			t.Errorf("audit %v: %d rows", c, count(c[0], c[1]))
		}
	}
	r := s.db.SecurityAudit.Query().Where(securityaudit.Action("password.reset")).OnlyX(context.Background())
	if r.UserID == nil || *r.UserID != id || r.Actor != "user:"+id.String() {
		t.Fatalf("reset audit: %+v", r)
	}
	// A request for an address that cannot register or reset leaves only an anonymous,
	// address-free "ignored" row.
	ok(t, s.requestRegistration(t, "audit@example.com", ""))
	ok(t, s.requestReset(t, "nobody@example.com", ""))
	if count("registration.request", "ignored") != 1 || count("password_reset.request", "ignored") != 1 {
		t.Fatal("missing ignored rows")
	}
	if n := s.db.SecurityAudit.Query().Where(securityaudit.Result("ignored"), securityaudit.UserIDNotNil()).CountX(context.Background()); n != 0 {
		t.Fatalf("%d ignored rows carry a user", n)
	}
}

// Registration can be switched off: requests and completions are refused with
// the same answer for everybody; login and reset keep working.
func TestRegistrationCanBeClosed(t *testing.T) {
	s := startMail(t, generous())
	s.register(t, "early@example.com", password)
	ok(t, s.requestRegistration(t, "late@example.com", ""))
	s.deliver(t)
	pending := tokenIn(t, s.inbox.to("late@example.com")[0], "/account/verify-email")

	s.accounts.SetRegistrationOpen(false)
	for _, email := range []string{"new@example.com", "early@example.com", "late@example.com"} {
		wantReason(t, s.requestRegistration(t, email, ""), codes.FailedPrecondition, application.ReasonRegistrationClosed)
	}
	wantReason(t, s.complete(pending, password, "", ""), codes.FailedPrecondition, application.ReasonRegistrationClosed)
	if s.users(t) != 1 {
		t.Fatal("an account was created while registration is closed")
	}
	// Existing users are unaffected.
	if _, err := s.login("early@example.com", password, ""); err != nil {
		t.Fatal(err)
	}
	tok := s.resetToken(t, "early@example.com")
	ok(t, s.reset(tok, "a new password here", ""))

	// Reopening makes the still-valid link work again.
	s.accounts.SetRegistrationOpen(true)
	ok(t, s.complete(pending, password, "", ""))
}

// Stored secrets: only digests, no plaintext token or address in the outbox
// columns, ciphertext gone after delivery.
func TestTokensAreStoredOnlyAsDigests(t *testing.T) {
	s := startMail(t, generous())
	ok(t, s.requestRegistration(t, "digest@example.com", ""))
	// Before delivery: the job holds ciphertext, but no plaintext address or token.
	job := s.db.MailJob.Query().OnlyX(context.Background())
	if len(job.Ciphertext) == 0 || job.KeyID == nil {
		t.Fatal("a queued job has no ciphertext")
	}
	if strings.Contains(string(job.Ciphertext), "digest@example.com") {
		t.Fatal("the recipient is stored in plaintext")
	}
	s.deliver(t)
	token := tokenIn(t, s.inbox.to("digest@example.com")[0], "/account/verify-email")
	sum := sha256.Sum256([]byte(token))
	tok := s.db.AccountToken.Query().OnlyX(context.Background())
	if string(tok.TokenHash) != string(sum[:]) || strings.Contains(string(tok.TokenHash), token) {
		t.Fatal("the stored value is not the SHA-256 digest of the token")
	}
	if tok.Purpose != accounttoken.PurposeRegistration || tok.UserID != nil || tok.IssuedAuthVersion != nil || tok.AppID != "ozid" {
		t.Fatalf("token binding: %+v", tok)
	}
	if ttl := tok.ExpiresAt.Sub(tok.CreatedAt); ttl != 30*time.Minute {
		t.Fatalf("registration token lifetime %v", ttl)
	}
	job = s.db.MailJob.Query().OnlyX(context.Background())
	if job.Status != mailjob.StatusAccepted || job.Ciphertext != nil || job.KeyID != nil {
		t.Fatalf("delivered job: %+v", job)
	}
	// A reset token lives 15 minutes and binds user and version.
	id := s.register(t, "bound@example.com", password)
	s.resetToken(t, "bound@example.com")
	rt := s.db.AccountToken.Query().Where(accounttoken.PurposeEQ(accounttoken.PurposePasswordReset)).OnlyX(context.Background())
	if rt.UserID == nil || *rt.UserID != id || rt.IssuedAuthVersion == nil || *rt.IssuedAuthVersion != 1 || rt.ExpiresAt.Sub(rt.CreatedAt) != 15*time.Minute {
		t.Fatalf("reset token binding: %+v", rt)
	}
}

// Section 10: limits count every attempt, hide quota and account existence.
func TestFlowRateLimits(t *testing.T) {
	s := startMail(t, limits())

	// Login: per mailbox, known or not, 10 per window; then RESOURCE_EXHAUSTED for
	// everybody asking about that mailbox, without a password check.
	for i := 1; i <= 12; i++ {
		_, err := s.login("ghost@example.com", "some wrong password", fmt.Sprintf("198.51.100.%d", i))
		want := codes.Unauthenticated
		if i > 10 {
			want = codes.ResourceExhausted
		}
		if status.Code(err) != want {
			t.Fatalf("login attempt %d: %v", i, err)
		}
	}
	// Per client address: 40.
	for i := 1; i <= 42; i++ {
		_, err := s.login(fmt.Sprintf("user%d@example.com", i), "some wrong password", "198.51.100.99")
		want := codes.Unauthenticated
		if i > 40 {
			want = codes.ResourceExhausted
		}
		if status.Code(err) != want {
			t.Fatalf("ip login attempt %d: %v", i, err)
		}
	}

	// Token submissions: 5 per token digest (even a token that does not exist), successes included.
	fake := strings.Repeat("A", 43)
	for i := 1; i <= 7; i++ {
		err := s.complete(fake, password, "", "198.51.100.50")
		if i <= 5 {
			wantReason(t, err, codes.FailedPrecondition, application.ReasonTokenInvalid)
		} else {
			wantReason(t, err, codes.ResourceExhausted, application.ReasonRateLimited)
		}
	}
	// A different token has its own counter, but the client address counts all of them: 30 per IP.
	for i := 0; i < 30; i++ {
		_ = s.reset(base64.RawURLEncoding.EncodeToString(append([]byte{byte(i), 1}, make([]byte, 30)...)), password, "198.51.100.51")
	}
	wantReason(t, s.reset(strings.Repeat("B", 43), password, "198.51.100.51"), codes.ResourceExhausted, application.ReasonRateLimited)

	// Mail requests: the per-address cooldown silently suppresses, so the answer is
	// the neutral success and no second mail exists; neither the quota nor the
	// mailbox's existence shows.
	ok(t, s.requestRegistration(t, "cool@example.com", "198.51.100.60"))
	ok(t, s.requestRegistration(t, "cool@example.com", "198.51.100.60"))
	s.deliver(t)
	if n := len(s.inbox.to("cool@example.com")); n != 1 {
		t.Fatalf("%d mails inside the cooldown", n)
	}
	// Suppression is the same for an address that does not exist and one that does.
	s.register(t, "known@example.com", password) // uses 203.0.113.1 once
	ok(t, s.requestReset(t, "known@example.com", "198.51.100.61"))
	ok(t, s.requestReset(t, "known@example.com", "198.51.100.61"))
	ok(t, s.requestReset(t, "unknown@example.com", "198.51.100.61"))
	ok(t, s.requestReset(t, "unknown@example.com", "198.51.100.61"))
	// Requests per client address: 20 per hour, then RESOURCE_EXHAUSTED regardless of address.
	for i := 0; i < 20; i++ {
		_ = s.requestRegistration(t, fmt.Sprintf("flood%d@example.com", i), "198.51.100.70")
	}
	wantReason(t, s.requestRegistration(t, "flood-last@example.com", "198.51.100.70"), codes.ResourceExhausted, application.ReasonRateLimited)
	ok(t, s.requestReset(t, "known@example.com", "198.51.100.71")) // another client address is not affected
}

// Redis down: no mail is queued, no password is checked, nothing is let through.
func TestFlowsFailClosedWithoutRedis(t *testing.T) {
	s := startMail(t, generous())
	s.register(t, "down@example.com", password)
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer dead.Close()
	limiter, err := ratelimit.New(dead, "x:", []byte(strings.Repeat("h", 32)))
	ok(t, err)
	guard, err := outbox.NewGuard(limiter, limits().Mail)
	ok(t, err)
	accounts := account.NewService(s.db, testutil.Hasher(t))
	accounts.SetMail(app.AccountMail(s.res.Mail, guard))
	svc := application.NewAccountService(accounts, limiter, limits())

	tokensBefore := s.db.AccountToken.Query().CountX(context.Background())
	jobsBefore := s.db.MailJob.Query().CountX(context.Background())
	ctx := anonCtx("198.51.100.80")
	if _, err = svc.RequestRegistration(ctx, &usersrvv1.RequestRegistrationRequest{Email: "new@example.com", AppId: "trip-map"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("registration request with Redis down: %v", err)
	}
	if _, err = svc.RequestPasswordReset(ctx, &usersrvv1.RequestPasswordResetRequest{Email: "down@example.com", AppId: "trip-map"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("reset request with Redis down: %v", err)
	}
	if _, err = svc.AuthenticateWithEmail(ctx, &usersrvv1.AuthenticateWithEmailRequest{Email: "down@example.com", Password: password}); status.Code(err) != codes.Unavailable {
		t.Fatalf("login with Redis down: %v", err)
	}
	if _, err = svc.CompleteRegistration(ctx, &usersrvv1.CompleteRegistrationRequest{Token: strings.Repeat("A", 43), Password: password}); status.Code(err) != codes.Unavailable {
		t.Fatalf("completion with Redis down: %v", err)
	}
	if _, err = svc.ResetPassword(ctx, &usersrvv1.ResetPasswordRequest{Token: strings.Repeat("A", 43), NewPassword: password}); status.Code(err) != codes.Unavailable {
		t.Fatalf("reset with Redis down: %v", err)
	}
	if s.db.AccountToken.Query().CountX(context.Background()) != tokensBefore || s.db.MailJob.Query().CountX(context.Background()) != jobsBefore {
		t.Fatal("a refused request left a token or a job")
	}
}

// Without a configured outbox the request methods refuse rather than pretend.
func TestRequestsRefuseWithoutMail(t *testing.T) {
	s := start(t) // no mail
	_, err := s.account.RequestRegistration(anonCtx(""), &usersrvv1.RequestRegistrationRequest{Email: "a@example.com", AppId: "trip-map"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("%v", err)
	}
	_, err = s.account.RequestPasswordReset(anonCtx(""), &usersrvv1.RequestPasswordResetRequest{Email: "a@example.com", AppId: "trip-map"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("%v", err)
	}
}

// The return context is bound to the challenge in the database, not carried by the
// link, so another browser that only has the mail still gets it back; it is handed
// back by the completion that consumes the token and by nothing else.
func TestReturnContextTravelsWithTheChallenge(t *testing.T) {
	s := startMail(t, generous())
	reg := func(email string, in *usersrvv1.RequestRegistrationRequest) string {
		in.Email = email
		_, err := s.account.RequestRegistration(anonCtx("203.0.113.1"), in)
		ok(t, err)
		s.deliver(t)
		msgs := s.inbox.to(email)
		return tokenIn(t, msgs[len(msgs)-1], "/account/verify-email")
	}

	// App and path.
	token := reg("ctx@example.com", &usersrvv1.RequestRegistrationRequest{AppId: "metro", ReturnPath: "/stations/7"})
	resp, err := s.account.CompleteRegistration(anonCtx(""), &usersrvv1.CompleteRegistrationRequest{Token: token, Password: password})
	ok(t, err)
	if resp.GetAppId() != "metro" || resp.GetReturnPath() != "/stations/7" {
		t.Fatalf("context %q", resp.String())
	}
	// The mail itself says nothing about where the user came from.
	if txt := s.inbox.to("ctx@example.com")[0].Text; strings.Contains(txt, "stations") || strings.Contains(txt, "metro") {
		t.Fatalf("the mail leaks the context:\n%s", txt)
	}

	// App only: the default path applies later, so no path is stored.
	token = reg("app-only@example.com", &usersrvv1.RequestRegistrationRequest{AppId: "trip-map"})
	resp, err = s.account.CompleteRegistration(anonCtx(""), &usersrvv1.CompleteRegistrationRequest{Token: token, Password: password})
	ok(t, err)
	if resp.GetAppId() != "trip-map" || resp.GetReturnPath() != "" {
		t.Fatalf("context %q", resp.String())
	}

	// No context at all, and a challenge issued before the columns existed (both NULL).
	token = reg("none@example.com", &usersrvv1.RequestRegistrationRequest{})
	resp, err = s.account.CompleteRegistration(anonCtx(""), &usersrvv1.CompleteRegistrationRequest{Token: token, Password: password})
	ok(t, err)
	if resp.GetAppId() != "" || resp.GetReturnPath() != "" {
		t.Fatalf("context %q", resp.String())
	}

	// Password reset: bound the same way, returned only by the reset that consumes it.
	_, err = s.account.RequestPasswordReset(anonCtx("203.0.113.2"), &usersrvv1.RequestPasswordResetRequest{Email: "ctx@example.com", AppId: "metro", ReturnPath: "/stations/7"})
	ok(t, err)
	s.deliver(t)
	msgs := s.inbox.to("ctx@example.com")
	rtoken := tokenIn(t, msgs[len(msgs)-1], "/account/reset-password")
	if tok := s.db.AccountToken.Query().Where(accounttoken.PurposeEQ(accounttoken.PurposePasswordReset)).OnlyX(context.Background()); tok.AppID != "ozid" ||
		tok.ReturnAppID == nil || *tok.ReturnAppID != "metro" || tok.ReturnPath == nil || *tok.ReturnPath != "/stations/7" {
		t.Fatalf("reset challenge %+v", tok)
	}
	rr, err := s.account.ResetPassword(anonCtx(""), &usersrvv1.ResetPasswordRequest{Token: rtoken, NewPassword: "another correct horse"})
	ok(t, err)
	if rr.GetAppId() != "metro" || rr.GetReturnPath() != "/stations/7" {
		t.Fatalf("context %q", rr.String())
	}
	// A replay learns nothing: the same uniform failure, no context.
	_, err = s.account.ResetPassword(anonCtx(""), &usersrvv1.ResetPasswordRequest{Token: rtoken, NewPassword: "yet another horse"})
	wantReason(t, err, codes.FailedPrecondition, application.ReasonTokenInvalid)

	// A row that carries an unusable context (a corrupted or hand-edited row) returns none
	// instead of passing it on.
	token = reg("bad-row@example.com", &usersrvv1.RequestRegistrationRequest{AppId: "metro", ReturnPath: "/ok"})
	_, err = s.raw.ExecContext(context.Background(), `UPDATE account_tokens SET return_path = $1 WHERE email_canonical = 'bad-row@example.com'`, "https://evil.example/")
	ok(t, err)
	resp, err = s.account.CompleteRegistration(anonCtx(""), &usersrvv1.CompleteRegistrationRequest{Token: token, Password: password})
	ok(t, err)
	if resp.GetAppId() != "" || resp.GetReturnPath() != "" {
		t.Fatalf("an unusable stored context was passed on: %q", resp.String())
	}
}

// The database refuses a path without an app and an oversized context.
func TestReturnContextColumnsAreConstrained(t *testing.T) {
	s := startMail(t, generous())
	n := 0
	for name, set := range map[string]string{
		"path without app": `return_app_id = NULL, return_path = '/x'`,
		"empty app":        `return_app_id = ''`,
		"long app":         `return_app_id = '` + strings.Repeat("a", 65) + `'`,
		"empty path":       `return_app_id = 'metro', return_path = ''`,
		"long path":        `return_app_id = 'metro', return_path = '/` + strings.Repeat("a", 256) + `'`,
	} {
		n++
		email := fmt.Sprintf("constraint%d@example.com", n)
		ok(t, s.requestRegistration(t, email, "203.0.113.1"))
		_, err := s.raw.ExecContext(context.Background(), `UPDATE account_tokens SET `+set+` WHERE email_canonical = $1`, email)
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
