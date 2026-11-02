package account_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"

	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/app"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/ent/externalidentity"
	"github.com/kongken/mono/internal/usersrv/ent/mailjob"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/externalprovider"
	"github.com/kongken/mono/internal/usersrv/mailer"
	"github.com/kongken/mono/internal/usersrv/outbox"
	"github.com/kongken/mono/internal/usersrv/ratelimit"
	"github.com/kongken/mono/internal/usersrv/testutil"
)

type coreProvider struct {
	name     externalprovider.ProviderName
	mu       sync.Mutex
	grants   map[string]externalprovider.ProviderIdentity
	calls    atomic.Int64
	exchange func(context.Context) error
}

func TestExternalCodeClaimIsSingleUseAndFailureIsTerminal(t *testing.T) {
	e := newSecurityEnv(t)
	p := e.providers[externalprovider.GoogleName]
	entered, release := make(chan struct{}), make(chan struct{})
	p.exchange = func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return externalprovider.ErrUnavailable
		}
	}
	ref := e.begin(externalprovider.GoogleName, uuid.Nil, 0)
	in := account.CompleteExternalInput{ChallengeRef: ref, Exchange: p.grant("single", "single@example.com", true)}
	result := make(chan error, 1)
	go func() { _, err := e.s.CompleteExternalLogin(ctx, in); result <- err }()
	<-entered
	if _, err := e.s.CompleteExternalLogin(ctx, in); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("another replica reclaimed exchanging code")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 1 {
		t.Fatal("code exchanged more than once")
	}
	p.exchange = func(context.Context) error { return externalprovider.ErrUnavailable }
	ref = e.begin(externalprovider.GoogleName, uuid.Nil, 0)
	in = account.CompleteExternalInput{ChallengeRef: ref, Exchange: p.grant("failed", "failed@example.com", true)}
	if _, err := e.s.CompleteExternalLogin(ctx, in); !errors.Is(err, externalprovider.ErrUnavailable) {
		t.Fatal(err)
	}
	row := e.db.AuthChallenge.GetX(ctx, ref.ID)
	if row.Status != authchallenge.StatusFailed || row.ConsumedAt == nil {
		t.Fatal("provider failure left code reclaimable")
	}
	if _, err := e.s.CompleteExternalLogin(ctx, in); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("failed callback retried exchange")
	}
}

func TestExternalSameEmailNeverAutoMergesAndEmailRaceHasOneWinner(t *testing.T) {
	e := newSecurityEnv(t)
	existing := create(t, e.s, "taken@example.com")
	ref, pending := e.complete(externalprovider.GoogleName, "unbound", "taken@example.com", true, uuid.Nil, 0)
	if pending.Account != nil || pending.Status != "registration_pending" {
		t.Fatal("email selected an existing account")
	}
	if _, err := e.s.CompleteExternalRegistration(ctx, account.RegistrationInput{ChallengeRef: ref, MutationID: "collision"}); !errors.Is(err, account.ErrRegistrationUnavailable) {
		t.Fatal("email conflict exposed ownership or merged")
	}
	if e.db.User.GetX(ctx, existing).AuthVersion != 1 || e.db.ExternalIdentity.Query().CountX(ctx) != 0 {
		t.Fatal("collision changed existing credentials")
	}
	one, _ := e.complete(externalprovider.GoogleName, "one", "race@example.com", true, uuid.Nil, 0)
	two, _ := e.complete(externalprovider.GoogleName, "two", "race@example.com", true, uuid.Nil, 0)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for n, ref := range []account.ChallengeRef{one, two} {
		go func(n int, ref account.ChallengeRef) {
			<-start
			_, err := e.s.CompleteExternalRegistration(ctx, account.RegistrationInput{ChallengeRef: ref, MutationID: fmt.Sprintf("race-%d", n)})
			errs <- err
		}(n, ref)
	}
	close(start)
	a, b := <-errs, <-errs
	if (a == nil) == (b == nil) || ((a != nil && !errors.Is(a, account.ErrRegistrationUnavailable)) || (b != nil && !errors.Is(b, account.ErrRegistrationUnavailable))) {
		t.Fatalf("email race: %v %v", a, b)
	}
	if e.db.User.Query().CountX(ctx) != 2 || e.db.ExternalIdentity.Query().CountX(ctx) != 1 {
		t.Fatal("email race left half user/identity")
	}
}

func TestExternalLinkCollisionLimitAndIdempotentSameOwner(t *testing.T) {
	e := newSecurityEnv(t)
	one, two := create(t, e.s, "one@example.com"), create(t, e.s, "two@example.com")
	ops := []account.ChallengeRef{e.operation(one, 1, "identity_link", uuid.Nil, externalprovider.GoogleName), e.operation(two, 1, "identity_link", uuid.Nil, externalprovider.GoogleName)}
	p1, _ := e.complete(externalprovider.GoogleName, "contended", "provider@example.com", true, one, 1)
	p2, _ := e.complete(externalprovider.GoogleName, "contended", "provider@example.com", true, two, 1)
	start := make(chan struct{})
	results := make(chan struct {
		out account.MutationResult
		err error
		uid uuid.UUID
	}, 2)
	for n, p := range []account.ChallengeRef{p1, p2} {
		go func(n int, p account.ChallengeRef) {
			<-start
			out, err := e.s.LinkExternalIdentity(ctx, account.IdentityMutation{Operation: ops[n], ProviderProof: p, MutationID: "link"})
			results <- struct {
				out account.MutationResult
				err error
				uid uuid.UUID
			}{out, err, p.UserID}
		}(n, p)
	}
	close(start)
	a, b := <-results, <-results
	if (a.err == nil) == (b.err == nil) {
		t.Fatalf("key race: %v %v", a.err, b.err)
	}
	winner, loser := a, b
	if winner.err != nil {
		winner, loser = b, a
	}
	if !errors.Is(loser.err, account.ErrIdentityConflict) {
		t.Fatal(loser.err)
	}
	if e.db.ExternalIdentity.Query().CountX(ctx) != 1 || e.db.AuthNotification.Query().CountX(ctx) != 1 || e.db.User.GetX(ctx, loser.uid).AuthVersion != 1 {
		t.Fatal("identity collision changed losing account")
	}
	op := e.operation(winner.uid, 2, "identity_link", uuid.Nil, externalprovider.GoogleName)
	proof, _ := e.complete(externalprovider.GoogleName, "contended", "changed@example.com", true, winner.uid, 2)
	out, err := e.s.LinkExternalIdentity(ctx, account.IdentityMutation{Operation: op, ProviderProof: proof, MutationID: "same-owner"})
	if err != nil || out.Result != "already_applied" || out.AuthVersion != 2 || e.db.AuthNotification.Query().CountX(ctx) != 1 {
		t.Fatalf("same-owner idempotence: %v", err)
	}
	op = e.operation(winner.uid, 2, "identity_link", uuid.Nil, externalprovider.GoogleName)
	proof, _ = e.complete(externalprovider.GoogleName, "another-key", "another@example.com", true, winner.uid, 2)
	if _, err = e.s.LinkExternalIdentity(ctx, account.IdentityMutation{Operation: op, ProviderProof: proof, MutationID: "limit"}); !errors.Is(err, account.ErrIdentityLimitReached) {
		t.Fatalf("one provider limit: %v", err)
	}
}

func TestExternalOperationBindingExpiryAndProofErasure(t *testing.T) {
	e := newSecurityEnv(t)
	uid := create(t, e.s, "binding@example.com")
	op := e.operation(uid, 1, "identity_link", uuid.Nil, externalprovider.GoogleName)
	proof, _ := e.complete(externalprovider.GoogleName, "bound", "bound@example.com", true, uid, 1)
	in := account.IdentityMutation{Operation: op, ProviderProof: proof, MutationID: "bound"}
	for _, change := range []func(*account.IdentityMutation){func(m *account.IdentityMutation) { m.Operation.UserID = uuid.New() }, func(m *account.IdentityMutation) { m.Operation.ContextHash = bytes.Repeat([]byte{3}, 32) }, func(m *account.IdentityMutation) { m.Operation.SessionHash = bytes.Repeat([]byte{4}, 32) }, func(m *account.IdentityMutation) { m.Operation.AuthVersion++ }} {
		bad := in
		change(&bad)
		if _, err := e.s.LinkExternalIdentity(ctx, bad); err == nil {
			t.Fatal("operation proof escaped user/session/context/version")
		}
	}
	e.clock.Add(int64(16 * time.Minute))
	if _, err := e.s.LinkExternalIdentity(ctx, in); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatalf("expired proof: %v", err)
	}
	if err := e.s.PurgeSecurity(ctx); err != nil {
		t.Fatal(err)
	}
	c := e.db.AuthChallenge.GetX(ctx, proof.ID)
	if len(c.ProofCiphertext) != 0 || c.ProofKeyID != nil || c.Status != authchallenge.StatusFailed {
		t.Fatal("expired proof retained or reset to issued")
	}
	if e.db.User.GetX(ctx, uid).AuthVersion != 1 || e.db.ExternalIdentity.Query().CountX(ctx) != 0 {
		t.Fatal("invalid proof changed account")
	}
}

func TestOldUnlinkReceiptCannotDeleteReboundIdentityAndAuditIsRedacted(t *testing.T) {
	e := newSecurityEnv(t)
	uid := create(t, e.s, "private-owner@example.com")
	link := func(version int64, mutation string) account.MutationResult {
		op := e.operation(uid, version, "identity_link", uuid.Nil, externalprovider.GoogleName)
		proof, _ := e.complete(externalprovider.GoogleName, "private-provider-subject", "private-provider@example.com", true, uid, version)
		out, err := e.s.LinkExternalIdentity(ctx, account.IdentityMutation{Operation: op, ProviderProof: proof, MutationID: mutation})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := link(1, "first-link")
	op := e.operation(uid, 2, "identity_unlink", first.IdentityID, "")
	in := account.IdentityMutation{Operation: op, TargetID: first.IdentityID, MutationID: "first-unlink"}
	if _, err := e.s.UnlinkExternalIdentity(ctx, in); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(int64(time.Second))
	second := link(3, "relink")
	if first.IdentityID == second.IdentityID {
		t.Fatal("rebind reused immutable identity record")
	}
	if out, err := e.s.UnlinkExternalIdentity(ctx, in); err != nil || !out.Replayed || out.AuthVersion != 3 {
		t.Fatal("old bound receipt could not replay")
	}
	if !e.db.ExternalIdentity.Query().Where(externalidentity.ID(second.IdentityID)).ExistX(ctx) || e.db.User.GetX(ctx, uid).AuthVersion != 4 {
		t.Fatal("old unlink modified rebound credential")
	}
	for _, r := range e.db.SecurityAudit.Query().AllX(ctx) {
		for _, secret := range []string{"private-owner@example.com", "private-provider@example.com", "private-provider-subject"} {
			if strings.Contains(r.String(), secret) {
				t.Fatal("identity material leaked to audit")
			}
		}
	}
	if e.db.AuthNotification.Query().CountX(ctx) != 3 {
		t.Fatal("notification count disagrees with committed credential changes")
	}
}

func TestIdentityLinkAndDisableRespectUserLockInBothOrders(t *testing.T) {
	for _, disableFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(disableFirst), func(t *testing.T) {
			e := newSecurityEnv(t)
			uid := create(t, e.s, "race-disable@example.com")
			opRef := e.operation(uid, 1, "identity_link", uuid.Nil, externalprovider.GoogleName)
			proof, _ := e.complete(externalprovider.GoogleName, "disable-race", "race-disable@example.com", true, uid, 1)
			r := &race{t: t, s: e.s, db: e.db, sqldb: e.raw, userID: uid}
			link := func() error {
				_, err := e.s.LinkExternalIdentity(ctx, account.IdentityMutation{Operation: opRef, ProviderProof: proof, MutationID: "link-disable"})
				return err
			}
			disable := func() error { _, err := e.s.SetDisabled(ctx, op, uid, true); return err }
			if disableFirst {
				a, b := r.inOrder(disable, link)
				if a != nil || !errors.Is(b, account.ErrNotActive) {
					t.Fatalf("disable first: %v %v", a, b)
				}
				if e.db.ExternalIdentity.Query().CountX(ctx) != 0 || e.db.AuthNotification.Query().CountX(ctx) != 0 {
					t.Fatal("disabled owner got new credential")
				}
			} else {
				a, b := r.inOrder(link, disable)
				if a != nil || b != nil {
					t.Fatalf("link first: %v %v", a, b)
				}
				if e.db.ExternalIdentity.Query().CountX(ctx) != 1 || e.db.User.GetX(ctx, uid).AuthVersion != 3 {
					t.Fatal("disable lost committed identity/version")
				}
			}
		})
	}
}

func TestFirstPasswordAndLastUnlinkRespectUserLockInBothOrders(t *testing.T) {
	for _, resetFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(resetFirst), func(t *testing.T) {
			e := newSecurityEnv(t)
			uid := passwordless(t, e.db, "reset-race@example.com")
			target := e.db.ExternalIdentity.Query().OnlyX(ctx).ID
			operation := e.operation(uid, 1, "identity_unlink", target, "")
			e.confirmOperation(operation)
			if err := e.s.RequestPasswordReset(ctx, "reset-race@example.com", account.ReturnContext{}, "", "race"); err != nil {
				t.Fatal(err)
			}
			token := e.token()
			reset := func() error { _, err := e.s.ResetPassword(ctx, token, "new secure password", "race"); return err }
			unlink := func() error {
				_, err := e.s.UnlinkExternalIdentity(ctx, account.IdentityMutation{Operation: operation, TargetID: target, MutationID: "unlink-reset"})
				return err
			}
			r := &race{t: t, s: e.s, db: e.db, sqldb: e.raw, userID: uid}
			if resetFirst {
				a, b := r.inOrder(reset, unlink)
				if a != nil || !errors.Is(b, account.ErrConflict) {
					t.Fatalf("reset first: %v %v", a, b)
				}
			} else {
				a, b := r.inOrder(unlink, reset)
				if !errors.Is(a, account.ErrLastCredential) || b != nil {
					t.Fatalf("unlink first: %v %v", a, b)
				}
			}
			u := e.db.User.GetX(ctx, uid)
			if u.PasswordHash == nil || u.AuthVersion != 2 || e.db.ExternalIdentity.Query().CountX(ctx) != 1 || e.db.AuthNotification.Query().CountX(ctx) != 1 {
				t.Fatal("reset/unlink race lost a login method or duplicated first-password notification")
			}
		})
	}
}

func TestOperationMailResendRevokesOldTokenAndDoesNotRenewFlow(t *testing.T) {
	e := newSecurityEnv(t)
	uid := passwordless(t, e.db, "resend@example.com")
	target := e.db.ExternalIdentity.Query().OnlyX(ctx).ID
	in := account.BeginOperationInput{FlowBinding: bindingFor(uid, 1), Kind: "identity_unlink", TargetID: target}
	one, err := e.s.BeginIdentityOperation(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	old := e.token()
	original := e.db.AuthChallenge.GetX(ctx, one).ExpiresAt
	e.clock.Add(int64(time.Minute))
	two, err := e.s.BeginIdentityOperation(ctx, in)
	if err != nil || two != one {
		t.Fatal("resend changed bound operation")
	}
	fresh := e.token()
	if old == fresh || !e.db.AuthChallenge.GetX(ctx, one).ExpiresAt.Equal(original) {
		t.Fatal("resend reused token or renewed browser lifetime")
	}
	if err = e.s.ConfirmIdentityOperation(ctx, one, old); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("resend left old confirmation usable")
	}
	if err = e.s.ConfirmIdentityOperation(ctx, one, fresh); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationRechecksEmailExpiryAfterAccountInsertionWait(t *testing.T) {
	e := newSecurityEnv(t)
	ref, _ := e.complete(externalprovider.GitHubName, "8989", "deadline@example.com", false, uuid.Nil, 0)
	if err := e.s.RequestExternalRegistrationEmail(ctx, ref, "deadline@example.com", "", "email"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.ConfirmExternalRegistrationEmail(ctx, ref.ID, e.token()); err != nil {
		t.Fatal(err)
	}
	e.db.User.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) {
				e.clock.Add(int64(11 * time.Minute))
			}
			return next.Mutate(ctx, m)
		})
	})
	if _, err := e.s.CompleteExternalRegistration(ctx, account.RegistrationInput{ChallengeRef: ref, MutationID: "late-email"}); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("final commit reused email proof that expired while inserting account")
	}
	if e.db.User.Query().CountX(ctx) != 0 || e.db.ExternalIdentity.Query().CountX(ctx) != 0 || e.db.AuthChallenge.GetX(ctx, ref.ID).ConsumedAt != nil {
		t.Fatal("expired email left partial registration")
	}
}

func TestExternalGateClosureWaitsForAdmittedCommit(t *testing.T) {
	e := newSecurityEnv(t)
	ref, _ := e.complete(externalprovider.GoogleName, "drain", "drain@example.com", true, uuid.Nil, 0)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.db.User.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) {
				once.Do(func() { close(entered); <-release })
			}
			return next.Mutate(ctx, m)
		})
	})
	committed := make(chan error, 1)
	go func() {
		_, err := e.s.CompleteExternalRegistration(ctx, account.RegistrationInput{ChallengeRef: ref, MutationID: "commit"})
		committed <- err
	}()
	<-entered
	closing, closed := make(chan struct{}), make(chan struct{})
	go func() { close(closing); e.s.SetRegistrationOpen(false); close(closed) }()
	<-closing
	select {
	case <-closed:
		t.Fatal("gate reported closure before admitted commit drained")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	<-closed
	if e.db.User.Query().CountX(ctx) != 1 {
		t.Fatal("admitted transaction lost")
	}
	// A later flow may still start for existing login, but an unknown identity
	// cannot pass callback or create another account after closure returned.
	late := e.begin(externalprovider.GoogleName, uuid.Nil, 0)
	if _, err := e.s.CompleteExternalLogin(ctx, account.CompleteExternalInput{ChallengeRef: late, Exchange: e.providers[externalprovider.GoogleName].grant("late", "late@example.com", true)}); !errors.Is(err, account.ErrRegistrationUnavailable) {
		t.Fatal("late callback used old registration policy")
	}
}

func (p *coreProvider) Name() externalprovider.ProviderName { return p.name }
func (p *coreProvider) ValidateConfiguration() error        { return nil }
func (p *coreProvider) AuthorizationURL(context.Context, externalprovider.AuthorizationRequest) (string, error) {
	return "https://" + string(p.name) + ".example.com/authorize", nil
}
func (p *coreProvider) ExchangeCode(ctx context.Context, r externalprovider.CodeExchangeRequest) (externalprovider.ProviderIdentity, error) {
	p.calls.Add(1)
	if p.exchange != nil {
		if err := p.exchange(ctx); err != nil {
			return externalprovider.ProviderIdentity{}, err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i, ok := p.grants[r.Code]
	if !ok {
		return i, externalprovider.ErrRejected
	}
	delete(p.grants, r.Code)
	return i, nil
}
func (p *coreProvider) grant(subject, email string, trusted bool) externalprovider.CodeExchangeRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	code := uuid.NewString()
	issuer := externalprovider.GoogleIssuer
	trust := externalprovider.ProviderVerified
	if p.name == externalprovider.GitHubName {
		issuer = externalprovider.GitHubIssuer
	}
	if trusted {
		trust = externalprovider.GoogleAuthoritative
	}
	p.grants[code] = externalprovider.ProviderIdentity{Provider: p.name, Issuer: issuer, Subject: subject, Email: email, EmailVerified: true, EmailTrust: trust, DisplayName: "Provider name", AuthenticatedAt: time.Now()}
	return externalprovider.CodeExchangeRequest{Provider: p.name, Code: code, Verifier: strings.Repeat("v", 64), CodeChallenge: strings.Repeat("c", 43), Nonce: strings.Repeat("n", 43), ConfigVersion: 1}
}

type capturedSecuritySender struct {
	mu       sync.Mutex
	messages []mailer.Message
	fail     error
}

func (s *capturedSecuritySender) Send(_ context.Context, m mailer.Message) (mailer.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	return mailer.Result{}, s.fail
}
func (s *capturedSecuritySender) last(t *testing.T) mailer.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		t.Fatal("no captured mail")
	}
	return s.messages[len(s.messages)-1]
}

type allowSecurityMail struct{}

func (allowSecurityMail) Allow(context.Context, ratelimit.Rule, ...string) (bool, error) {
	return true, nil
}

type securityEnv struct {
	t         *testing.T
	s         *account.Service
	db        *ent.Client
	raw       *sql.DB
	mail      *outbox.Service
	sender    *capturedSecuritySender
	providers map[externalprovider.ProviderName]*coreProvider
	settings  account.ExternalSettings
	clock     atomic.Int64
}

func newSecurityEnv(t *testing.T) *securityEnv {
	t.Helper()
	db, raw := testutil.DBWithSQL(t)
	s := account.NewService(db, testutil.Hasher(t))
	e := &securityEnv{t: t, s: s, db: db, raw: raw, sender: &capturedSecuritySender{}, providers: map[externalprovider.ProviderName]*coreProvider{}}
	e.clock.Store(time.Now().UTC().Truncate(time.Microsecond).UnixNano())
	clock := func() time.Time { return time.Unix(0, e.clock.Load()).UTC() }
	s.SetClock(clock)
	keys, err := (outbox.EncryptionConfig{ActiveKeyID: "k1", Keys: []outbox.KeyConfig{{ID: "k1", Key: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))}}}).Keyring()
	if err != nil {
		t.Fatal(err)
	}
	m, err := mailer.NewWithSender(mailer.Config{Provider: "sendgrid", FromEmail: "no-reply@example.com", SendGrid: mailer.SendGridConfig{APIKey: "test-key"}}, e.sender)
	if err != nil {
		t.Fatal(err)
	}
	links, err := mailer.NewLinks(map[string]string{"ozid": "https://id.example.com"}, false)
	if err != nil {
		t.Fatal(err)
	}
	e.mail, err = outbox.New(db, keys, links, m, outbox.Settings{RegistrationTTL: 30 * time.Minute, ResetTTL: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	e.mail.SetClock(clock)
	guard, err := outbox.NewGuard(allowSecurityMail{}, ratelimit.MailRules{})
	if err != nil {
		t.Fatal(err)
	}
	mail, limits := app.AccountMail(e.mail, guard)
	s.SetMail(mail, limits)
	securityMail, ok := mail.(account.SecurityMail)
	if !ok {
		t.Fatal("mail adapter lacks security queue")
	}
	s.SetSecurityMail(securityMail)
	codec, err := keys.ProofCipher("k1")
	if err != nil {
		t.Fatal(err)
	}
	e.settings = account.ExternalSettings{Providers: map[externalprovider.ProviderName]externalprovider.ExternalProvider{}, Policies: map[externalprovider.ProviderName]account.ProviderPolicy{}, Codec: codec}
	for _, name := range []externalprovider.ProviderName{externalprovider.GoogleName, externalprovider.GitHubName} {
		p := &coreProvider{name: name, grants: map[string]externalprovider.ProviderIdentity{}}
		e.providers[name] = p
		e.settings.Providers[name] = p
		e.settings.Policies[name] = account.ProviderPolicy{Enabled: true, ConfigVersion: 1, RedirectURI: "https://api.example.com/api/identity/callback/" + string(name)}
	}
	s.ConfigureExternal(e.settings)
	return e
}
func bindingFor(uid uuid.UUID, version int64) account.FlowBinding {
	c := sha256.Sum256([]byte(uuid.NewString()))
	b := account.FlowBinding{FlowID: uuid.New(), UserID: uid, ContextHash: c[:], AuthVersion: version}
	if uid != uuid.Nil {
		h := sha256.Sum256([]byte("session:" + uid.String()))
		b.SessionHash = h[:]
	}
	return b
}
func (e *securityEnv) begin(name externalprovider.ProviderName, uid uuid.UUID, version int64) account.ChallengeRef {
	e.t.Helper()
	b := bindingFor(uid, version)
	intent := "login"
	if uid != uuid.Nil {
		intent = "link"
	}
	out, err := e.s.BeginExternalLogin(ctx, account.BeginExternalInput{FlowBinding: b, Intent: intent, Provider: name, RedirectURI: e.settings.Policies[name].RedirectURI, Authorization: externalprovider.AuthorizationRequest{State: strings.Repeat("s", 43), Nonce: strings.Repeat("n", 43), CodeChallenge: strings.Repeat("c", 43)}})
	if err != nil {
		e.t.Fatal(err)
	}
	return account.ChallengeRef{ID: out.ChallengeID, FlowBinding: b}
}
func (e *securityEnv) complete(name externalprovider.ProviderName, subject, email string, trusted bool, uid uuid.UUID, version int64) (account.ChallengeRef, account.ExternalResult) {
	e.t.Helper()
	ref := e.begin(name, uid, version)
	out, err := e.s.CompleteExternalLogin(ctx, account.CompleteExternalInput{ChallengeRef: ref, Exchange: e.providers[name].grant(subject, email, trusted)})
	if err != nil {
		e.t.Fatal(err)
	}
	return ref, out
}
func (e *securityEnv) operation(uid uuid.UUID, version int64, kind string, target uuid.UUID, provider externalprovider.ProviderName) account.ChallengeRef {
	e.t.Helper()
	b := bindingFor(uid, version)
	b.FlowID = uuid.Nil
	id, err := e.s.BeginIdentityOperation(ctx, account.BeginOperationInput{FlowBinding: b, Kind: kind, TargetID: target, Provider: provider, Password: password})
	if err != nil {
		e.t.Fatal(err)
	}
	return account.ChallengeRef{ID: id, FlowBinding: b}
}

var securityTokenPattern = regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`)

func (e *securityEnv) token() string {
	e.t.Helper()
	if _, err := e.mail.RunOnce(ctx); err != nil {
		e.t.Fatal(err)
	}
	e.sender.mu.Lock()
	defer e.sender.mu.Unlock()
	for n := len(e.sender.messages) - 1; n >= 0; n-- {
		m := e.sender.messages[n]
		match := securityTokenPattern.FindStringSubmatch(m.Text)
		if len(match) == 2 {
			if !strings.Contains(m.Text, "https://id.example.com/account/") {
				e.t.Fatal("mail left fixed OzID origin/path")
			}
			return match[1]
		}
	}
	e.t.Fatal("mail has no fragment token")
	return ""
}
func (e *securityEnv) confirmOperation(ref account.ChallengeRef) {
	e.t.Helper()
	if err := e.s.ConfirmIdentityOperation(ctx, ref.ID, e.token()); err != nil {
		e.t.Fatal(err)
	}
}
func (e *securityEnv) setEnabled(name externalprovider.ProviderName, enabled bool) {
	p := e.settings.Policies[name]
	p.Enabled = enabled
	e.settings.Policies[name] = p
	e.s.ConfigureExternal(e.settings)
}

func TestExternalRegistrationAtomicConcurrencyAndBoundReceipts(t *testing.T) {
	e := newSecurityEnv(t)
	ref, pending := e.complete(externalprovider.GoogleName, "stable-google-sub", "new@example.com", true, uuid.Nil, 0)
	if pending.Account != nil || pending.Status != "registration_pending" || !pending.EmailConfirmed || e.db.User.Query().CountX(ctx) != 0 {
		t.Fatal("callback created an account or granted authority")
	}
	row := e.db.AuthChallenge.GetX(ctx, ref.ID)
	if bytes.Contains(row.ProofCiphertext, []byte("new@example.com")) || bytes.Contains(row.ProofCiphertext, []byte("stable-google-sub")) {
		t.Fatal("plaintext proof persisted")
	}
	in := account.RegistrationInput{ChallengeRef: ref, MutationID: "create-1", DisplayName: "Owner"}
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 8)
	errs := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); id, err := e.s.CompleteExternalRegistration(ctx, in); ids <- id; errs <- err }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var uid uuid.UUID
	for id := range ids {
		if uid == uuid.Nil {
			uid = id
		}
		if uid != id {
			t.Fatal("registration replay changed UUID")
		}
	}
	if e.db.User.Query().CountX(ctx) != 1 || e.db.ExternalIdentity.Query().CountX(ctx) != 1 || e.db.SecurityAudit.Query().Where(securityaudit.Action("account.create")).CountX(ctx) != 1 {
		t.Fatal("concurrent registration duplicated account/identity/audit")
	}
	u := e.db.User.GetX(ctx, uid)
	if u.PasswordHash != nil || u.PasswordSetAt != nil || u.AuthVersion != 1 || e.db.UserRole.Query().CountX(ctx) != 0 {
		t.Fatal("new external account got fabricated password/role/version")
	}
	row = e.db.AuthChallenge.GetX(ctx, ref.ID)
	if len(row.ProofCiphertext) != 0 || row.ProofKeyID != nil || row.ConsumedAt == nil {
		t.Fatal("successful registration retained proof")
	}
	in.DisplayName = "changed"
	if _, err := e.s.CompleteExternalRegistration(ctx, in); !errors.Is(err, account.ErrMutationReplayMismatch) {
		t.Fatalf("replay mismatch: %v", err)
	}
	in.DisplayName = "Owner"
	e.s.SetRegistrationOpen(false)
	e.setEnabled(externalprovider.GoogleName, false)
	if id, err := e.s.CompleteExternalRegistration(ctx, in); err != nil || id != uid {
		t.Fatal("closed gate denied bound receipt view")
	}
	bad := in
	bad.ContextHash = bytes.Repeat([]byte{99}, 32)
	if _, err := e.s.CompleteExternalRegistration(ctx, bad); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("cross-browser receipt was accepted")
	}
	e.clock.Add(int64(25 * time.Hour))
	if _, err := e.s.CompleteExternalRegistration(ctx, in); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("expired receipt accepted")
	}
}

func TestExternalEmailHandoffEditingAndNoPrematureAccount(t *testing.T) {
	e := newSecurityEnv(t)
	ref, _ := e.complete(externalprovider.GitHubName, "123456789", "provider@example.com", false, uuid.Nil, 0)
	in := account.RegistrationInput{ChallengeRef: ref, MutationID: "register"}
	if _, err := e.s.CompleteExternalRegistration(ctx, in); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("GitHub email skipped OzID confirmation")
	}
	if err := e.s.RequestExternalRegistrationEmail(ctx, ref, "provider@example.com", "", "request"); err != nil {
		t.Fatal(err)
	}
	old := e.token()
	before := e.db.AuthChallenge.GetX(ctx, ref.ID)
	view, err := e.s.GetExternalRegistration(ctx, ref)
	if err != nil || view.EmailConfirmed || e.db.AuthChallenge.GetX(ctx, ref.ID).ConsumedAt != nil || !bytes.Equal(before.EmailTokenHash, e.db.AuthChallenge.GetX(ctx, ref.ID).EmailTokenHash) {
		t.Fatal("GET/view consumed an email")
	}
	if err := e.s.ConfirmIdentityOperation(ctx, ref.ID, old); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("registration mail used as operation proof")
	}
	if err := e.s.RequestExternalRegistrationEmail(ctx, ref, "edited@example.com", "", "edit"); err != nil {
		t.Fatal(err)
	}
	fresh := e.token()
	if old == fresh {
		t.Fatal("resend reused token")
	}
	if err := e.s.ConfirmExternalRegistrationEmail(ctx, ref.ID, old); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("edit retained old token")
	}
	if err := e.s.ConfirmExternalRegistrationEmail(ctx, ref.ID, fresh); err != nil {
		t.Fatal(err)
	}
	if e.db.User.Query().CountX(ctx) != 0 || e.db.ExternalIdentity.Query().CountX(ctx) != 0 {
		t.Fatal("email confirmation created formal user")
	}
	if err := e.s.ConfirmExternalRegistrationEmail(ctx, ref.ID, fresh); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("email confirmation replay succeeded")
	}
	bad := in
	bad.ContextHash = bytes.Repeat([]byte{88}, 32)
	if _, err := e.s.CompleteExternalRegistration(ctx, bad); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("another device finalized registration")
	}
	id, err := e.s.CompleteExternalRegistration(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if e.db.User.GetX(ctx, id).EmailCanonical != "edited@example.com" {
		t.Fatal("contact edit not bound to final account")
	}
}

func TestExternalGatesAtCallbackMailAndFinalCommit(t *testing.T) {
	for _, name := range []externalprovider.ProviderName{externalprovider.GoogleName, externalprovider.GitHubName} {
		t.Run(string(name), func(t *testing.T) {
			e := newSecurityEnv(t)
			ref := e.begin(name, uuid.Nil, 0)
			e.s.SetRegistrationOpen(false)
			_, err := e.s.CompleteExternalLogin(ctx, account.CompleteExternalInput{ChallengeRef: ref, Exchange: e.providers[name].grant("777", "candidate@example.com", name == externalprovider.GoogleName)})
			if !errors.Is(err, account.ErrRegistrationUnavailable) || e.db.User.Query().CountX(ctx) != 0 || e.db.MailJob.Query().CountX(ctx) != 0 {
				t.Fatal("callback bypassed closed registration")
			}
			e.s.SetRegistrationOpen(true)
			ref, _ = e.complete(name, "888", "next@example.com", false, uuid.Nil, 0)
			if err = e.s.RequestExternalRegistrationEmail(ctx, ref, "next@example.com", "", "mail"); err != nil {
				t.Fatal(err)
			}
			token := e.token()
			e.s.SetRegistrationOpen(false)
			if err = e.s.ConfirmExternalRegistrationEmail(ctx, ref.ID, token); !errors.Is(err, account.ErrRegistrationUnavailable) {
				t.Fatal("mail confirmation bypassed closed registration")
			}
			e.s.SetRegistrationOpen(true)
			if err = e.s.ConfirmExternalRegistrationEmail(ctx, ref.ID, token); err != nil {
				t.Fatal(err)
			}
			e.s.SetRegistrationOpen(false)
			in := account.RegistrationInput{ChallengeRef: ref, MutationID: "final"}
			if _, err = e.s.CompleteExternalRegistration(ctx, in); !errors.Is(err, account.ErrRegistrationUnavailable) {
				t.Fatal("final transaction reused gate snapshot")
			}
			e.setEnabled(name, false)
			if _, err = e.s.CompleteExternalRegistration(ctx, in); !errors.Is(err, account.ErrProviderDisabled) {
				t.Fatal("provider gate should precede registration gate")
			}
			if e.db.User.Query().CountX(ctx) != 0 || e.db.ExternalIdentity.Query().CountX(ctx) != 0 {
				t.Fatal("closed gates left a half account")
			}
		})
	}
}

func TestExistingIdentityLoginAndExplicitLinkWhenRegistrationClosed(t *testing.T) {
	e := newSecurityEnv(t)
	ref, _ := e.complete(externalprovider.GoogleName, "owner", "owner@example.com", true, uuid.Nil, 0)
	uid, err := e.s.CompleteExternalRegistration(ctx, account.RegistrationInput{ChallengeRef: ref, MutationID: "new"})
	if err != nil {
		t.Fatal(err)
	}
	e.s.SetRegistrationOpen(false)
	_, login := e.complete(externalprovider.GoogleName, "owner", "changed-provider@example.com", true, uuid.Nil, 0)
	if login.Account == nil || login.Account.ID != uid || login.Account.Email != "owner@example.com" || login.Account.AuthVersion != 1 {
		t.Fatal("stable identity login changed owner/email/version")
	}
	op := e.operation(uid, 1, "identity_link", uuid.Nil, externalprovider.GitHubName)
	e.confirmOperation(op)
	proof, pending := e.complete(externalprovider.GitHubName, "991", "github@example.com", false, uid, 1)
	if pending.Status != "link_pending" || pending.Account != nil {
		t.Fatal("link went through registration/login")
	}
	in := account.IdentityMutation{Operation: op, ProviderProof: proof, MutationID: "link-1"}
	out, err := e.s.LinkExternalIdentity(ctx, in)
	if err != nil || out.AuthVersion != 2 || out.Replayed {
		t.Fatalf("link with closed registration: %v", err)
	}
	if replay, err := e.s.LinkExternalIdentity(ctx, in); err != nil || !replay.Replayed || replay.IdentityID != out.IdentityID || replay.AuthVersion != 2 {
		t.Fatalf("link lost response replay: %v", err)
	}
	if e.db.AuthNotification.Query().CountX(ctx) != 1 || e.db.AuthOperationReceipt.Query().CountX(ctx) != 1 || e.db.User.GetX(ctx, uid).AuthVersion != 2 {
		t.Fatal("replay duplicated event/version")
	}
	bad := in
	bad.Operation.SessionHash = bytes.Repeat([]byte{3}, 32)
	bad.ProviderProof.SessionHash = bad.Operation.SessionHash
	if _, err = e.s.LinkExternalIdentity(ctx, bad); !errors.Is(err, account.ErrMutationReplayMismatch) {
		t.Fatal("receipt transferred to another session")
	}
	identities, err := e.s.GetExternalIdentities(ctx, uid)
	if err != nil || len(identities) != 2 {
		t.Fatal("identity summary missing")
	}
	e.setEnabled(externalprovider.GoogleName, false)
	e.setEnabled(externalprovider.GitHubName, false)
	unlink := e.operation(uid, 2, "identity_unlink", out.IdentityID, "")
	e.confirmOperation(unlink)
	un := account.IdentityMutation{Operation: unlink, TargetID: out.IdentityID, MutationID: "unlink"}
	res, err := e.s.UnlinkExternalIdentity(ctx, un)
	if err != nil || res.AuthVersion != 3 {
		t.Fatalf("offline unlink: %v", err)
	}
	if _, err = e.s.UnlinkExternalIdentity(ctx, un); err != nil {
		t.Fatal("unlink receipt lost after target deletion")
	}
}

func TestIdentityMutationRollbackOnOutboxFailure(t *testing.T) {
	e := newSecurityEnv(t)
	uid := create(t, e.s, "owner@example.com")
	op := e.operation(uid, 1, "identity_link", uuid.Nil, externalprovider.GoogleName)
	proof, _ := e.complete(externalprovider.GoogleName, "key", "owner@example.com", true, uid, 1)
	var fail atomic.Bool
	fail.Store(true)
	e.db.MailJob.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if fail.Load() {
				return nil, account.ErrUnavailable
			}
			return next.Mutate(ctx, m)
		})
	})
	in := account.IdentityMutation{Operation: op, ProviderProof: proof, MutationID: "atomic"}
	if _, err := e.s.LinkExternalIdentity(ctx, in); !errors.Is(err, account.ErrUnavailable) {
		t.Fatalf("outbox failure: %v", err)
	}
	if e.db.ExternalIdentity.Query().CountX(ctx) != 0 || e.db.AuthOperationReceipt.Query().CountX(ctx) != 0 || e.db.AuthNotification.Query().CountX(ctx) != 0 || e.db.User.GetX(ctx, uid).AuthVersion != 1 || e.db.AuthChallenge.GetX(ctx, op.ID).ConsumedAt != nil || e.db.AuthChallenge.GetX(ctx, proof.ID).ConsumedAt != nil {
		t.Fatal("partial mutation survived rollback")
	}
	fail.Store(false)
	if _, err := e.s.LinkExternalIdentity(ctx, in); err != nil {
		t.Fatal(err)
	}
	e.sender.fail = &mailer.Error{Class: mailer.ClassUncertain, Provider: "sendgrid", Code: "timeout"}
	if _, err := e.mail.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if e.db.MailJob.Query().Where(mailjob.StatusEQ(mailjob.StatusUncertain)).CountX(ctx) != 1 || e.db.User.GetX(ctx, uid).AuthVersion != 2 {
		t.Fatal("delivery failure rolled back account or retried uncertainty")
	}
}

func TestPasswordlessLastCredentialAndConcurrentUnlink(t *testing.T) {
	e := newSecurityEnv(t)
	uid := passwordless(t, e.db, "passwordless@example.com")
	first := e.db.ExternalIdentity.Query().OnlyX(ctx)
	last := e.operation(uid, 1, "identity_unlink", first.ID, "")
	e.confirmOperation(last)
	if _, err := e.s.UnlinkExternalIdentity(ctx, account.IdentityMutation{Operation: last, TargetID: first.ID, MutationID: "last"}); !errors.Is(err, account.ErrLastCredential) {
		t.Fatal("last credential removed")
	}
	second := e.db.ExternalIdentity.Create().SetUserID(uid).SetProvider(externalidentity.ProviderGithub).SetIssuer(externalprovider.GitHubIssuer).SetSubject("789").SaveX(ctx)
	one := e.operation(uid, 1, "identity_unlink", first.ID, "")
	e.confirmOperation(one)
	two := e.operation(uid, 1, "identity_unlink", second.ID, "")
	e.confirmOperation(two)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for n, ref := range []account.ChallengeRef{one, two} {
		target := first.ID
		if n == 1 {
			target = second.ID
		}
		go func(ref account.ChallengeRef, id uuid.UUID) {
			<-start
			_, err := e.s.UnlinkExternalIdentity(ctx, account.IdentityMutation{Operation: ref, TargetID: id, MutationID: id.String()})
			errs <- err
		}(ref, target)
	}
	close(start)
	a, b := <-errs, <-errs
	if (a == nil) == (b == nil) || ((a != nil && !errors.Is(a, account.ErrConflict)) || (b != nil && !errors.Is(b, account.ErrConflict))) {
		t.Fatalf("concurrent unlink: %v %v", a, b)
	}
	if e.db.ExternalIdentity.Query().CountX(ctx) != 1 || e.db.User.GetX(ctx, uid).AuthVersion != 2 || e.db.AuthNotification.Query().CountX(ctx) != 1 {
		t.Fatal("concurrent unlink removed all credentials or duplicated change")
	}
}

func TestPasswordlessRecoveryFirstPasswordAndDeletionHandoff(t *testing.T) {
	e := newSecurityEnv(t)
	uid := passwordless(t, e.db, "recovery@example.com")
	e.s.SetRegistrationOpen(false)
	e.setEnabled(externalprovider.GoogleName, false)
	e.setEnabled(externalprovider.GitHubName, false)
	op := e.operation(uid, 1, "identity_unlink", e.db.ExternalIdentity.Query().OnlyX(ctx).ID, "")
	e.confirmOperation(op)
	if err := e.s.RequestPasswordReset(ctx, "recovery@example.com", account.ReturnContext{}, "", "reset"); err != nil {
		t.Fatal(err)
	}
	token := e.token()
	if _, err := e.s.ResetPassword(ctx, token, "newly set secure password", "first"); err != nil {
		t.Fatal(err)
	}
	u := e.db.User.GetX(ctx, uid)
	if u.PasswordHash == nil || u.PasswordSetAt == nil || u.AuthVersion != 2 || e.db.ExternalIdentity.Query().CountX(ctx) != 1 || e.db.AuthNotification.Query().CountX(ctx) != 1 {
		t.Fatal("first password failed atomic state/notice")
	}
	if _, err := e.s.ResetPassword(ctx, token, "newly set secure password", "repeat"); !errors.Is(err, account.ErrTokenInvalid) {
		t.Fatal("reset replay succeeded")
	}
	if _, err := e.s.UnlinkExternalIdentity(ctx, account.IdentityMutation{Operation: op, TargetID: e.db.ExternalIdentity.Query().OnlyX(ctx).ID, MutationID: "stale"}); !errors.Is(err, account.ErrConflict) {
		t.Fatal("reset left old operation usable")
	}
	if _, _, err := e.s.Login(ctx, "recovery@example.com", "newly set secure password", "login"); err != nil {
		t.Fatal(err)
	}
	other := passwordless(t, e.db, "delete@example.com")
	deletion := e.operation(other, 1, "account_deletion", uuid.Nil, "")
	e.confirmOperation(deletion)
	if e.db.User.GetX(ctx, other).Status != user.StatusActive {
		t.Fatal("mail confirmation began deletion")
	}
	if _, err := e.s.BeginDeletion(ctx, other, "bypass", "direct"); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("legacy deletion bypassed passwordless proof")
	}
	if _, err := e.s.BeginConfirmedDeletion(ctx, deletion, "delete", "wrong"); !errors.Is(err, account.ErrChallengeInvalid) {
		t.Fatal("missing confirmation word accepted")
	}
	if version, err := e.s.BeginConfirmedDeletion(ctx, deletion, "delete", "注销"); err != nil || version != 2 {
		t.Fatalf("confirmed deletion: %v", err)
	}
	if _, err := e.s.BeginConfirmedDeletion(ctx, deletion, "delete", "注销"); err != nil {
		t.Fatal(err)
	}
}
