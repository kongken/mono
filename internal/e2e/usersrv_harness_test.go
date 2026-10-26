package e2e_test

import (
	"context"
	"encoding/base64"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/application"
	"github.com/kongken/mono/internal/usersrv/ent/mailjob"
	"github.com/kongken/mono/internal/usersrv/mailer"
	"github.com/kongken/mono/internal/usersrv/outbox"
	usersrvratelimit "github.com/kongken/mono/internal/usersrv/ratelimit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// E2EPassword is the password every harness account registers with. It meets
// usersrv's length rule; the legacy "password1" fixtures are not valid passwords.
const E2EPassword = "correct horse battery"

// Inbox is the fake far end of account mail: it records what a vendor would have
// been handed. Tokens are read out of the delivered mail exactly as a user clicks
// them; nothing else of the mail path is replaced.
type Inbox struct {
	mu   sync.Mutex
	msgs []mailer.Message
	fail func(mailer.Message) error
}

// FailWith makes the vendor refuse every message for which fn returns an error (nil
// restores delivery). The refused message is not recorded.
func (i *Inbox) FailWith(fn func(mailer.Message) error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.fail = fn
}

func (i *Inbox) Send(_ context.Context, msg mailer.Message) (mailer.Result, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.fail != nil {
		if err := i.fail(msg); err != nil {
			return mailer.Result{}, err
		}
	}
	i.msgs = append(i.msgs, msg)
	return mailer.Result{}, nil
}

// To lists the mail delivered to email, oldest first.
func (i *Inbox) To(email string) []mailer.Message {
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

// Total is how many messages were delivered to anyone.
func (i *Inbox) Total() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.msgs)
}

var linkToken = regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`)

// TokenIn returns the token of the verification or reset link in the latest mail
// to email.
func (i *Inbox) TokenIn(t testing.TB, email string) string {
	t.Helper()
	msgs := i.To(email)
	if len(msgs) == 0 {
		t.Fatalf("no mail for %s", email)
	}
	m := linkToken.FindStringSubmatch(msgs[len(msgs)-1].Text)
	if m == nil {
		t.Fatalf("no link in mail:\n%s", msgs[len(msgs)-1].Text)
	}
	return m[1]
}

// usersrvLimits are limits high enough that scenarios never meet them by accident.
func usersrvLimits() application.Limits {
	rule := func(name string) usersrvratelimit.Rule {
		return usersrvratelimit.Rule{Name: name, Limit: 100000, Window: time.Hour}
	}
	return application.Limits{
		PasswordPerAccount: rule("password_per_account"), PasswordPerIP: rule("password_per_ip"),
		ProfileWritePerUser: rule("profile_write_per_user"), DeletionPerUser: rule("deletion_per_user"),
		TokenSubmitPerIP: rule("token_submit_per_ip"), TokenSubmitPerToken: rule("token_submit_per_token"),
		Mail: usersrvratelimit.MailRules{
			RegistrationPerIP: rule("registration_mail_per_ip"), ResetPerIP: rule("reset_mail_per_ip"),
			RegistrationEmailCooldown: rule("registration_mail_cooldown"), RegistrationPerEmail: rule("registration_mail_per_email"),
			ResetEmailCooldown: rule("reset_mail_cooldown"), ResetPerEmail: rule("reset_mail_per_email"),
			MailPerEmail: rule("mail_per_email"), MailGlobal: rule("mail_global"),
		},
	}
}

// ChainUsersrvLimits replaces the harness' generous usersrv limits.
type ChainUsersrvLimits application.Limits

// usersrvFault lets a test make usersrv fail a call before the account core sees
// it, to model an outage or a lost response.
type usersrvFault struct {
	fail atomic.Pointer[func(method string) error]
	// lose names a method whose next successful call has its response replaced by
	// UNAVAILABLE, after the account core committed: the response-lost case.
	lose atomic.Pointer[string]
}

func (f *usersrvFault) interceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if fn := f.fail.Load(); fn != nil {
			if err := (*fn)(info.FullMethod); err != nil {
				return nil, err
			}
		}
		if lose := f.lose.Load(); lose != nil && *lose == info.FullMethod {
			res, err := next(ctx, req)
			if err != nil {
				return res, err
			}
			f.lose.Store(nil)
			return nil, status.Error(codes.Unavailable, "response lost")
		}
		return next(ctx, req)
	}
}

// LoseUsersrvResponse makes the next successful call of method (such as
// "/usersrv.v1.AccountService/BeginDeletion") commit and then fail UNAVAILABLE.
func (h *ChainHarness) LoseUsersrvResponse(method string) { h.usersrvFault.lose.Store(&method) }

// FailUsersrv makes every usersrv call for which fn returns an error fail with
// it; nil restores normal service.
func (h *ChainHarness) FailUsersrv(fn func(method string) error) {
	if fn == nil {
		h.usersrvFault.fail.Store(nil)
		return
	}
	h.usersrvFault.fail.Store(&fn)
}

// DeliverMail runs the usersrv mail outbox until nothing is waiting to be sent.
func (h *ChainHarness) DeliverMail() {
	h.T.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := h.UsersrvRes.Mail.RunOnce(context.Background()); err != nil {
			h.T.Fatal(err)
		}
		n, err := h.UsersrvDB.MailJob.Query().Where(mailjob.StatusIn(mailjob.StatusPending, mailjob.StatusSending)).Count(context.Background())
		if err != nil {
			h.T.Fatal(err)
		}
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			h.T.Fatalf("%d mail jobs never settled", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func mailKeyring(t *testing.T) *outbox.Keyring {
	t.Helper()
	keys, err := (outbox.EncryptionConfig{ActiveKeyID: "k1", Keys: []outbox.KeyConfig{
		{ID: "k1", Key: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("e", 32)))}}}).Keyring()
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// emailFor turns a fixture label into an address.
func emailFor(subject string) string {
	if strings.Contains(subject, "@") {
		return strings.ToLower(subject)
	}
	return strings.ToLower(subject) + "@example.com"
}

// userUUID parses id or fails the test.
func userUUID(t testing.TB, id string) uuid.UUID {
	t.Helper()
	u, err := uuid.Parse(id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
