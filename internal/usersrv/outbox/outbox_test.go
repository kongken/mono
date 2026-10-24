package outbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/accounttoken"
	"github.com/kongken/mono/internal/usersrv/ent/mailjob"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/mailer"
	"github.com/kongken/mono/internal/usersrv/testutil"
)

var ctx = context.Background()

func keyOf(c byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(c), 32)))
}

func testKeys(t *testing.T, active string, ids ...string) *Keyring {
	t.Helper()
	cfg := EncryptionConfig{ActiveKeyID: active}
	for _, id := range ids {
		cfg.Keys = append(cfg.Keys, KeyConfig{ID: id, Key: keyOf(id[0])})
	}
	k, err := cfg.Keyring()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

type env struct {
	db    *ent.Client
	raw   *sql.DB
	now   time.Time
	clock func() time.Time
	mu    sync.Mutex
}

func (e *env) advance(d time.Duration) { e.mu.Lock(); e.now = e.now.Add(d); e.mu.Unlock() }

func newEnv(t *testing.T) *env {
	t.Helper()
	raw, _ := testutil.Schema(t)
	db := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, raw)))
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := db.Schema.Create(cctx); err != nil {
		t.Fatal(err)
	}
	e := &env{db: db, raw: raw, now: time.Now().UTC().Truncate(time.Millisecond)}
	e.clock = func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
	return e
}

type fakeSender struct {
	mu    sync.Mutex
	sent  []mailer.Message
	fn    func(n int, msg mailer.Message) error
	calls atomic.Int32
}

func (f *fakeSender) Send(_ context.Context, msg mailer.Message) (mailer.Result, error) {
	n := int(f.calls.Add(1))
	f.mu.Lock()
	f.sent = append(f.sent, msg)
	f.mu.Unlock()
	if f.fn != nil {
		if err := f.fn(n, msg); err != nil {
			return mailer.Result{}, err
		}
	}
	return mailer.Result{}, nil
}

func (f *fakeSender) messages() []mailer.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mailer.Message(nil), f.sent...)
}

func mailCfg(provider string, generation int) mailer.Config {
	c := mailer.Config{Provider: provider, Generation: generation, FromEmail: "no-reply@example.com", FromName: "山海足迹", Timeout: "5s"}
	switch provider {
	case "sendgrid":
		c.SendGrid = mailer.SendGridConfig{APIKey: "SG.key"}
	case "smtp2go":
		c.SMTP2GO = mailer.SMTP2GOConfig{APIKey: "key"}
	}
	return c
}

func newService(t *testing.T, e *env, sender mailer.Sender, keys *Keyring) *Service {
	t.Helper()
	return newServiceCfg(t, e, sender, keys, mailCfg("sendgrid", 1))
}

func newServiceCfg(t *testing.T, e *env, sender mailer.Sender, keys *Keyring, cfg mailer.Config) *Service {
	t.Helper()
	m, err := mailer.NewWithSender(cfg, sender)
	if err != nil {
		t.Fatal(err)
	}
	links, err := mailer.NewLinks(map[string]string{"trip-map": "https://map.example.com", "metro": "https://metro.example.com"}, false)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(e.db, keys, links, m, Settings{RegistrationTTL: 30 * time.Minute, ResetTTL: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetClock(e.clock)
	return svc
}

func issueReg(t *testing.T, s *Service, email string) Issued {
	t.Helper()
	out, err := s.IssueStandalone(ctx, IssueInput{Purpose: PurposeRegistration, Email: email, AppID: "trip-map"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func makeUser(t *testing.T, e *env, email string) (uuid.UUID, int64) {
	t.Helper()
	acc := account.NewService(e.db, testutil.Hasher(t))
	id, err := acc.CreateVerifiedUser(ctx, account.NewUser{Email: email, Password: "correct horse battery"})
	if err != nil {
		t.Fatal(err)
	}
	return id, 1
}

func issueReset(t *testing.T, s *Service, id uuid.UUID, version int64) Issued {
	t.Helper()
	out, err := s.IssueStandalone(ctx, IssueInput{Purpose: PurposePasswordReset, Email: "bob@example.com", UserID: &id, AuthVersion: &version, AppID: "metro"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func job(t *testing.T, e *env, id uuid.UUID) *ent.MailJob {
	t.Helper()
	j, err := e.db.MailJob.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func assertFinal(t *testing.T, j *ent.MailJob, status mailjob.Status, class string) {
	t.Helper()
	if j.Status != status || j.ResultClass != class {
		t.Fatalf("status/class = %s/%s, want %s/%s", j.Status, j.ResultClass, status, class)
	}
	if len(j.Ciphertext) != 0 || j.KeyID != nil || j.LeaseOwner != nil || j.LeaseExpiresAt != nil || j.FinishedAt == nil {
		t.Fatalf("a final job keeps no secret or lease: %+v", j)
	}
}

func tokenOf(t *testing.T, s *Service, j *ent.MailJob) (payload, string) {
	t.Helper()
	plain, err := s.keys.open(deref(j.KeyID), j.ID.String(), j.Template, j.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	var p payload
	if err = json.Unmarshal(plain, &p); err != nil {
		t.Fatal(err)
	}
	return p, p.Token
}

// ---- issuing and encryption ----------------------------------------------------

func TestIssueIsAtomicAndStoresNoPlaintext(t *testing.T) {
	e := newEnv(t)
	s := newService(t, e, &fakeSender{}, testKeys(t, "k1", "k1"))

	// Rolled back: neither the challenge nor the job exists.
	tx, _ := e.db.Tx(ctx)
	if _, err := s.Issue(ctx, tx, IssueInput{Purpose: PurposeRegistration, Email: "alice@example.com", AppID: "trip-map"}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if a, _ := e.db.AccountToken.Query().Count(ctx); a != 0 {
		t.Fatalf("%d tokens after rollback", a)
	}
	if b, _ := e.db.MailJob.Query().Count(ctx); b != 0 {
		t.Fatalf("%d jobs after rollback", b)
	}

	tx, _ = e.db.Tx(ctx)
	out, err := s.Issue(ctx, tx, IssueInput{Purpose: PurposeRegistration, Email: "alice@example.com", AppID: "trip-map"})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	j := job(t, e, out.JobID)
	tok := e.db.AccountToken.GetX(ctx, out.ChallengeID)
	pl, token := tokenOf(t, s, j)
	if pl.To != "alice@example.com" || pl.AppID != "trip-map" || len(token) != 43 {
		t.Fatalf("payload %+v", pl)
	}
	digest := sha256.Sum256([]byte(token))
	if string(tok.TokenHash) != string(digest[:]) || tok.Purpose != accounttoken.PurposeRegistration || tok.UserID != nil {
		t.Fatalf("challenge %+v", tok)
	}
	if j.Status != mailjob.StatusPending || j.Attempts != 0 || j.ChallengeID != out.ChallengeID || j.Provider != "sendgrid" || j.Generation != 1 ||
		j.Template != "verify_email" || *j.KeyID != "k1" || !j.ExpiresAt.Equal(out.ExpiresAt) || !tok.ExpiresAt.Equal(out.ExpiresAt) {
		t.Fatalf("job %+v", j)
	}

	// No plaintext in the outbox row, and no plaintext token in the challenge row.
	dump := func(table string) string {
		var d string
		if err := e.raw.QueryRow(fmt.Sprintf(`SELECT string_agg(row_to_json(t)::text, ' ') FROM %s t`, table)).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	jobs := dump("mail_jobs")
	for _, secret := range []string{token, "alice", "example.com", "/account/", "map.example", "山海"} {
		if strings.Contains(jobs, secret) {
			t.Fatalf("mail_jobs contains %q", secret)
		}
	}
	if challenges := dump("account_tokens"); strings.Contains(challenges, token) {
		t.Fatal("account_tokens contains the plaintext token")
	}
	if strings.Contains(string(j.Ciphertext), token) || strings.Contains(string(j.Ciphertext), "alice") {
		t.Fatal("ciphertext is not encrypted")
	}
}

func TestIssueValidation(t *testing.T) {
	e := newEnv(t)
	s := newService(t, e, &fakeSender{}, testKeys(t, "k1", "k1"))
	id, v := uuid.New(), int64(1)
	zero := int64(0)
	cases := map[string]IssueInput{
		"unknown app":         {Purpose: PurposeRegistration, Email: "a@example.com", AppID: "evil"},
		"unknown purpose":     {Purpose: "invite", Email: "a@example.com", AppID: "metro"},
		"bad email":           {Purpose: PurposeRegistration, Email: "a@example.com\r\nBcc: x@example.com", AppID: "metro"},
		"registration + user": {Purpose: PurposeRegistration, Email: "a@example.com", AppID: "metro", UserID: &id},
		"registration + ver":  {Purpose: PurposeRegistration, Email: "a@example.com", AppID: "metro", AuthVersion: &v},
		"reset without user":  {Purpose: PurposePasswordReset, Email: "a@example.com", AppID: "metro", AuthVersion: &v},
		"reset without ver":   {Purpose: PurposePasswordReset, Email: "a@example.com", AppID: "metro", UserID: &id},
		"reset version zero":  {Purpose: PurposePasswordReset, Email: "a@example.com", AppID: "metro", UserID: &id, AuthVersion: &zero},
	}
	for name, in := range cases {
		if _, err := s.IssueStandalone(ctx, in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if !errors.Is(func() error { _, err := s.IssueStandalone(ctx, cases["unknown app"]); return err }(), ErrUnknownApp) {
		t.Fatal("unknown app must be ErrUnknownApp")
	}
	if n, _ := e.db.MailJob.Query().Count(ctx); n != 0 {
		t.Fatalf("%d jobs from invalid input", n)
	}
	// The address is normalized to its canonical form, so limits and lookups share one key.
	out, err := s.IssueStandalone(ctx, IssueInput{Purpose: PurposeRegistration, Email: " Alice+Tag@Example.COM ", AppID: "metro"})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.db.AccountToken.GetX(ctx, out.ChallengeID).EmailCanonical; got != "alice+tag@example.com" {
		t.Fatalf("stored %q", got)
	}
}

func TestEncryptionBindsJobAndTemplateAndSupportsRotation(t *testing.T) {
	k1 := testKeys(t, "k1", "k1")
	ct, id, err := k1.seal("job-1", "verify_email", []byte("secret payload"))
	if err != nil || id != "k1" {
		t.Fatal(err)
	}
	if got, err := k1.open("k1", "job-1", "verify_email", ct); err != nil || string(got) != "secret payload" {
		t.Fatalf("%q %v", got, err)
	}
	// The ciphertext cannot be moved to another job or re-labelled.
	if _, err = k1.open("k1", "job-2", "verify_email", ct); err == nil {
		t.Fatal("decrypted under another job id")
	}
	if _, err = k1.open("k1", "job-1", "reset_password", ct); err == nil {
		t.Fatal("decrypted under another template")
	}
	// Tampering, truncation and the wrong key fail; none of them is a guess.
	flipped := append([]byte(nil), ct...)
	flipped[len(flipped)-1] ^= 1
	if _, err = k1.open("k1", "job-1", "verify_email", flipped); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, err = k1.open("k1", "job-1", "verify_email", ct[:10]); err == nil {
		t.Fatal("truncated ciphertext accepted")
	}
	if _, err = k1.open("zz", "job-1", "verify_email", ct); !errors.Is(err, errUnknownKey) {
		t.Fatalf("unknown key: %v", err)
	}
	other := EncryptionConfig{ActiveKeyID: "k1", Keys: []KeyConfig{{ID: "k1", Key: keyOf('x')}}}
	ko, _ := other.Keyring()
	if _, err = ko.open("k1", "job-1", "verify_email", ct); err == nil {
		t.Fatal("decrypted with another key of the same id")
	}
	// Two encryptions of the same plaintext differ (random nonce).
	ct2, _, _ := k1.seal("job-1", "verify_email", []byte("secret payload"))
	if string(ct) == string(ct2) {
		t.Fatal("nonce reuse")
	}

	// Rotation: k2 becomes active, k1 stays for jobs it already encrypted.
	rotated := testKeys(t, "k2", "k1", "k2")
	if got, err := rotated.open("k1", "job-1", "verify_email", ct); err != nil || string(got) != "secret payload" {
		t.Fatalf("old job after rotation: %v", err)
	}
	if _, id, _ = rotated.seal("j", "verify_email", []byte("x")); id != "k2" {
		t.Fatalf("new jobs use %s", id)
	}
}

func TestKeyringConfigIsStrict(t *testing.T) {
	good := KeyConfig{ID: "k1", Key: keyOf('a')}
	bad := map[string]EncryptionConfig{
		"no keys":          {ActiveKeyID: "k1"},
		"active missing":   {ActiveKeyID: "k2", Keys: []KeyConfig{good}},
		"no active":        {Keys: []KeyConfig{good}},
		"duplicate id":     {ActiveKeyID: "k1", Keys: []KeyConfig{good, good}},
		"short key":        {ActiveKeyID: "k1", Keys: []KeyConfig{{ID: "k1", Key: base64.StdEncoding.EncodeToString([]byte("short"))}}},
		"not base64":       {ActiveKeyID: "k1", Keys: []KeyConfig{{ID: "k1", Key: "%%%"}}},
		"bad id":           {ActiveKeyID: "k 1", Keys: []KeyConfig{{ID: "k 1", Key: keyOf('a')}}},
		"key and file":     {ActiveKeyID: "k1", Keys: []KeyConfig{{ID: "k1", Key: keyOf('a'), KeyFile: "/x"}}},
		"neither":          {ActiveKeyID: "k1", Keys: []KeyConfig{{ID: "k1"}}},
		"missing key file": {ActiveKeyID: "k1", Keys: []KeyConfig{{ID: "k1", KeyFile: "/nonexistent/key"}}},
	}
	for name, c := range bad {
		if _, err := c.Keyring(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := (EncryptionConfig{ActiveKeyID: "k1", Keys: []KeyConfig{good}}).Keyring(); err != nil {
		t.Fatal(err)
	}
}

// ---- delivery ----------------------------------------------------------------

func TestDeliveryAcceptsAndErasesTheSecrets(t *testing.T) {
	e := newEnv(t)
	sender := &fakeSender{}
	s := newService(t, e, sender, testKeys(t, "k1", "k1"))
	out := issueReg(t, s, "alice@example.com")
	pl, token := tokenOf(t, s, job(t, e, out.JobID))

	if n, err := s.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	msgs := sender.messages()
	if len(msgs) != 1 {
		t.Fatalf("%d messages", len(msgs))
	}
	m := msgs[0]
	wantLink := "https://map.example.com/account/verify-email#token=" + token
	if m.To != "alice@example.com" || m.ID != out.JobID.String() || !strings.Contains(m.Subject, "山海足迹") ||
		!strings.Contains(m.Text, wantLink) || !strings.Contains(m.HTML, wantLink) || !strings.Contains(m.Text, "30 分钟") {
		t.Fatalf("message %+v", m)
	}
	if m.From.Email != "no-reply@example.com" || pl.To != m.To {
		t.Fatalf("envelope %+v", m)
	}
	j := job(t, e, out.JobID)
	assertFinal(t, j, mailjob.StatusAccepted, ResultOK)
	if j.Attempts != 1 {
		t.Fatalf("attempts %d", j.Attempts)
	}
	// A second pass finds nothing to do and nothing is re-sent.
	if n, _ := s.RunOnce(ctx); n != 0 || sender.calls.Load() != 1 {
		t.Fatalf("resent: n=%d calls=%d", n, sender.calls.Load())
	}
	if snap := s.Snapshot(); snap["accepted"] != 1 {
		t.Fatalf("stats %v", snap)
	}
}

func TestResetMailUsesTheResetLinkOfTheApp(t *testing.T) {
	e := newEnv(t)
	sender := &fakeSender{}
	s := newService(t, e, sender, testKeys(t, "k1", "k1"))
	id, v := makeUser(t, e, "bob@example.com")
	out := issueReset(t, s, id, v)
	_, token := tokenOf(t, s, job(t, e, out.JobID))
	if _, err := s.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	m := sender.messages()[0]
	if !strings.Contains(m.Text, "https://metro.example.com/account/reset-password#token="+token) || !strings.Contains(m.Subject, "重置密码") ||
		!strings.Contains(m.Text, "15 分钟") {
		t.Fatalf("%+v", m)
	}
	tok := e.db.AccountToken.GetX(ctx, out.ChallengeID)
	if tok.UserID == nil || *tok.UserID != id || *tok.IssuedAuthVersion != v || tok.Purpose != accounttoken.PurposePasswordReset {
		t.Fatalf("%+v", tok)
	}
}

func TestTemporaryFailuresRetryWithBackoffAndTheSameToken(t *testing.T) {
	e := newEnv(t)
	sender := &fakeSender{fn: func(int, mailer.Message) error {
		return &mailer.Error{Class: mailer.ClassTemporary, Provider: "sendgrid", Code: "http_503"}
	}}
	s := newService(t, e, sender, testKeys(t, "k1", "k1"))
	out := issueReg(t, s, "alice@example.com")
	_, token := tokenOf(t, s, job(t, e, out.JobID))

	run := func() {
		t.Helper()
		_, err := s.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}

	run() // attempt 1
	j := job(t, e, out.JobID)
	if j.Status != mailjob.StatusPending || j.Attempts != 1 || !j.NextAttemptAt.Equal(e.now.Add(time.Minute)) || len(j.Ciphertext) == 0 || j.LeaseOwner != nil {
		t.Fatalf("after 1st failure: %+v", j)
	}
	run() // not due yet
	if sender.calls.Load() != 1 {
		t.Fatal("retried before the backoff")
	}
	e.advance(time.Minute)
	run() // attempt 2
	j = job(t, e, out.JobID)
	if j.Status != mailjob.StatusPending || j.Attempts != 2 || !j.NextAttemptAt.Equal(e.now.Add(5*time.Minute)) {
		t.Fatalf("after 2nd failure: %+v", j)
	}
	e.advance(4 * time.Minute)
	run()
	if sender.calls.Load() != 2 {
		t.Fatal("retried before the 5 minute backoff")
	}
	e.advance(time.Minute)
	run() // attempt 3: the last
	assertFinal(t, job(t, e, out.JobID), mailjob.StatusFailed, "temporary_exhausted")
	if j = job(t, e, out.JobID); j.Attempts != 3 {
		t.Fatalf("attempts %d", j.Attempts)
	}
	e.advance(time.Hour)
	run()
	if sender.calls.Load() != 3 {
		t.Fatalf("%d sends, want exactly 3", sender.calls.Load())
	}
	// Every attempt carried the very same token: a retry never mints a new one.
	for _, m := range sender.messages() {
		if !strings.Contains(m.Text, "#token="+token) {
			t.Fatal("a retry used a different token")
		}
	}
	if n, _ := e.db.AccountToken.Query().Count(ctx); n != 1 {
		t.Fatalf("%d challenges", n)
	}
}

func TestRetryStopsAtTheTokenDeadline(t *testing.T) {
	e := newEnv(t)
	sender := &fakeSender{fn: func(int, mailer.Message) error {
		return &mailer.Error{Class: mailer.ClassTemporary, Provider: "sendgrid", Code: "network"}
	}}
	m, _ := mailer.NewWithSender(mailCfg("sendgrid", 1), sender)
	links, _ := mailer.NewLinks(map[string]string{"metro": "https://metro.example.com"}, false)
	s, err := New(e.db, testKeys(t, "k1", "k1"), links, m, Settings{RegistrationTTL: 2 * time.Minute, ResetTTL: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(e.clock)
	out, err := s.IssueStandalone(ctx, IssueInput{Purpose: PurposeRegistration, Email: "a@example.com", AppID: "metro"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.RunOnce(ctx) // fails, next at +1m (< 2m): retried
	if j := job(t, e, out.JobID); j.Status != mailjob.StatusPending {
		t.Fatalf("%+v", j)
	}
	e.advance(time.Minute)
	_, _ = s.RunOnce(ctx) // fails, next would be +5m, past the deadline: stop
	assertFinal(t, job(t, e, out.JobID), mailjob.StatusExpired, "token_expired")
}

func TestPermanentAndUncertainResultsAreNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status mailjob.Status
		class  string
	}{
		{"permanent", &mailer.Error{Class: mailer.ClassPermanent, Provider: "sendgrid", Code: "http_400"}, mailjob.StatusFailed, "permanent"},
		{"auth", &mailer.Error{Class: mailer.ClassPermanent, Provider: "sendgrid", Code: "http_401", Auth: true}, mailjob.StatusFailed, "permanent"},
		{"uncertain", &mailer.Error{Class: mailer.ClassUncertain, Provider: "sendgrid", Code: "timeout"}, mailjob.StatusUncertain, "uncertain"},
		{"unclassified", errors.New("something odd"), mailjob.StatusUncertain, "uncertain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			sender := &fakeSender{fn: func(int, mailer.Message) error { return tc.err }}
			s := newService(t, e, sender, testKeys(t, "k1", "k1"))
			out := issueReg(t, s, "alice@example.com")
			for i := 0; i < 4; i++ {
				_, _ = s.RunOnce(ctx)
				e.advance(10 * time.Minute)
			}
			assertFinal(t, job(t, e, out.JobID), tc.status, tc.class)
			if sender.calls.Load() != 1 {
				t.Fatalf("%d sends", sender.calls.Load())
			}
		})
	}
}

func TestLostWorkerBecomesUncertainAndIsNotResent(t *testing.T) {
	e := newEnv(t)
	sender := &fakeSender{}
	s := newService(t, e, sender, testKeys(t, "k1", "k1"))
	out := issueReg(t, s, "alice@example.com")
	// A worker claimed the job and died before recording a result.
	n, err := e.db.MailJob.Update().Where(mailjob.ID(out.JobID)).SetStatus(mailjob.StatusSending).AddAttempts(1).
		SetLeaseOwner("dead-worker").SetLeaseExpiresAt(e.now.Add(40 * time.Second)).Save(ctx)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	// Lease still valid: another replica leaves it alone.
	_, _ = s.RunOnce(ctx)
	if j := job(t, e, out.JobID); j.Status != mailjob.StatusSending || sender.calls.Load() != 0 {
		t.Fatalf("job taken while its lease was valid: %+v", j)
	}
	e.advance(time.Minute)
	_, _ = s.RunOnce(ctx)
	assertFinal(t, job(t, e, out.JobID), mailjob.StatusUncertain, "lease_expired")
	e.advance(time.Hour)
	_, _ = s.RunOnce(ctx)
	if sender.calls.Load() != 0 {
		t.Fatal("a job with a lost worker was sent again")
	}
}

func TestSeveralWorkersNeverSendTheSameJobTwice(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	perJob := map[string]int{}
	var inFlight, maxInFlight int
	sender := &fakeSender{fn: func(_ int, m mailer.Message) error {
		mu.Lock()
		perJob[m.ID]++
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	}}
	keys := testKeys(t, "k1", "k1")
	const jobs = 30
	first := newService(t, e, sender, keys)
	for i := 0; i < jobs; i++ {
		issueReg(t, first, fmt.Sprintf("user%d@example.com", i))
	}
	// Four replicas over the same database, all polling at once.
	var replicas []*Service
	for i := 0; i < 4; i++ {
		r := newService(t, e, sender, keys)
		r.settings.BatchSize = jobs
		replicas = append(replicas, r)
	}
	var wg sync.WaitGroup
	for round := 0; round < 3; round++ {
		for _, r := range replicas {
			wg.Add(1)
			go func(r *Service) { defer wg.Done(); _, _ = r.RunOnce(ctx) }(r)
		}
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(perJob) != jobs {
		t.Fatalf("%d jobs sent of %d", len(perJob), jobs)
	}
	for id, n := range perJob {
		if n != 1 {
			t.Fatalf("job %s was sent %d times", id, n)
		}
	}
	if left, _ := first.Pending(ctx); left != 0 {
		t.Fatalf("%d jobs left", left)
	}
}

// ---- pre-send re-check ---------------------------------------------------------

func TestJobsAreCancelledWhenTheChallengeOrAccountNoLongerAllowThem(t *testing.T) {
	type setup func(t *testing.T, e *env, s *Service) Issued
	cases := []struct {
		name   string
		setup  setup
		status mailjob.Status
		class  string
	}{
		{"challenge consumed", func(t *testing.T, e *env, s *Service) Issued {
			o := issueReg(t, s, "alice@example.com")
			e.db.AccountToken.UpdateOneID(o.ChallengeID).SetConsumedAt(e.now).ExecX(ctx)
			return o
		}, mailjob.StatusCancelled, "challenge_consumed"},
		{"challenge deleted", func(t *testing.T, e *env, s *Service) Issued {
			o := issueReg(t, s, "alice@example.com")
			e.db.AccountToken.DeleteOneID(o.ChallengeID).ExecX(ctx)
			return o
		}, mailjob.StatusCancelled, "challenge_missing"},
		{"challenge expired", func(t *testing.T, e *env, s *Service) Issued {
			o := issueReg(t, s, "alice@example.com")
			e.advance(31 * time.Minute)
			return o
		}, mailjob.StatusExpired, "job_expired"},
		{"email already registered", func(t *testing.T, e *env, s *Service) Issued {
			o := issueReg(t, s, "bob@example.com")
			makeUser(t, e, "bob@example.com")
			return o
		}, mailjob.StatusCancelled, "email_registered"},
		{"account disabled", func(t *testing.T, e *env, s *Service) Issued {
			id, v := makeUser(t, e, "bob@example.com")
			o := issueReset(t, s, id, v)
			e.db.User.UpdateOneID(id).SetStatus(user.StatusDisabled).AddAuthVersion(1).ExecX(ctx)
			return o
		}, mailjob.StatusCancelled, "account_inactive"},
		{"auth version changed", func(t *testing.T, e *env, s *Service) Issued {
			id, v := makeUser(t, e, "bob@example.com")
			o := issueReset(t, s, id, v)
			e.db.User.UpdateOneID(id).AddAuthVersion(1).ExecX(ctx)
			return o
		}, mailjob.StatusCancelled, "version_mismatch"},
		{"account deleted", func(t *testing.T, e *env, s *Service) Issued {
			id, v := makeUser(t, e, "bob@example.com")
			o := issueReset(t, s, id, v)
			e.db.User.DeleteOneID(id).ExecX(ctx)
			return o
		}, mailjob.StatusCancelled, "account_gone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			sender := &fakeSender{}
			s := newService(t, e, sender, testKeys(t, "k1", "k1"))
			o := tc.setup(t, e, s)
			_, _ = s.RunOnce(ctx)
			if sender.calls.Load() != 0 {
				t.Fatal("a mail went out for a challenge that is no longer valid")
			}
			assertFinal(t, job(t, e, o.JobID), tc.status, tc.class)
		})
	}
}

func TestStillValidChallengesAreSentForResetOfActiveAccount(t *testing.T) {
	e := newEnv(t)
	sender := &fakeSender{}
	s := newService(t, e, sender, testKeys(t, "k1", "k1"))
	id, v := makeUser(t, e, "bob@example.com")
	issueReset(t, s, id, v)
	_, _ = s.RunOnce(ctx)
	if sender.calls.Load() != 1 {
		t.Fatal("valid reset mail not sent")
	}
}

func TestBeginDeletionCancelsWaitingMailAndErasesItsCiphertext(t *testing.T) {
	e := newEnv(t)
	sender := &fakeSender{}
	s := newService(t, e, sender, testKeys(t, "k1", "k1"))
	acc := account.NewService(e.db, testutil.Hasher(t))
	id, v := makeUser(t, e, "bob@example.com")
	reset := issueReset(t, s, id, v)
	other := issueReg(t, s, "carol@example.com") // another address: untouched

	if _, err := acc.BeginDeletion(ctx, id, "mutation-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	assertFinal(t, job(t, e, reset.JobID), mailjob.StatusCancelled, "account_closed")
	if _, err := e.db.AccountToken.Get(ctx, reset.ChallengeID); !ent.IsNotFound(err) {
		t.Fatal("the challenge survived")
	}
	if j := job(t, e, other.JobID); j.Status != mailjob.StatusPending || len(j.Ciphertext) == 0 {
		t.Fatalf("an unrelated job was touched: %+v", j)
	}
	_, _ = s.RunOnce(ctx)
	if got := sender.messages(); len(got) != 1 || got[0].To != "carol@example.com" {
		t.Fatalf("sent %+v", got)
	}
}

// ---- configuration pinning and key problems -------------------------------------------

func TestJobsArePinnedToTheirProviderAndGeneration(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  mailer.Config
	}{
		{"other provider", mailCfg("smtp2go", 1)},
		{"other generation", mailCfg("sendgrid", 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			keys := testKeys(t, "k1", "k1")
			old := newService(t, e, &fakeSender{}, keys) // sendgrid, generation 1
			out := issueReg(t, old, "alice@example.com")

			sender := &fakeSender{}
			switched := newServiceCfg(t, e, sender, keys, tc.now)
			_, _ = switched.RunOnce(ctx)
			if sender.calls.Load() != 0 {
				t.Fatal("a queued job was sent through a different setup")
			}
			assertFinal(t, job(t, e, out.JobID), mailjob.StatusCancelled, "config_changed")
		})
	}
}

func TestDecryptionFailureFailsTheJobWithoutSending(t *testing.T) {
	e := newEnv(t)
	sender := &fakeSender{}
	keys := testKeys(t, "k1", "k1")
	s := newService(t, e, sender, keys)
	out := issueReg(t, s, "alice@example.com")
	j := job(t, e, out.JobID)
	bad := append([]byte(nil), j.Ciphertext...)
	bad[len(bad)-1] ^= 0xff
	e.db.MailJob.UpdateOneID(out.JobID).SetCiphertext(bad).ExecX(ctx)
	_, _ = s.RunOnce(ctx)
	assertFinal(t, job(t, e, out.JobID), mailjob.StatusFailed, "decrypt_failed")
	if sender.calls.Load() != 0 {
		t.Fatal("sent")
	}
}

func TestStartupRefusesWhenAQueuedJobNeedsAMissingKey(t *testing.T) {
	e := newEnv(t)
	s := newService(t, e, &fakeSender{}, testKeys(t, "k1", "k1"))
	out := issueReg(t, s, "alice@example.com")

	if err := s.Preflight(ctx); err != nil {
		t.Fatal(err)
	}
	// k1 was removed from the configuration while its job is still queued.
	lost := newService(t, e, &fakeSender{}, testKeys(t, "k2", "k2"))
	err := lost.Preflight(ctx)
	if err == nil || !strings.Contains(err.Error(), "k1") {
		t.Fatalf("preflight accepted a missing key: %v", err)
	}
	if strings.Contains(err.Error(), keyOf('k')) {
		t.Fatal("key material in the error")
	}
	// The job was not dropped.
	if j := job(t, e, out.JobID); j.Status != mailjob.StatusPending || len(j.Ciphertext) == 0 {
		t.Fatalf("%+v", j)
	}
	// Keeping both keys (rotation) starts fine, and finished jobs no longer need their key.
	if err = newService(t, e, &fakeSender{}, testKeys(t, "k2", "k1", "k2")).Preflight(ctx); err != nil {
		t.Fatal(err)
	}
	_, _ = s.RunOnce(ctx)
	if err = lost.Preflight(ctx); err != nil {
		t.Fatalf("a finished job still pins its key: %v", err)
	}
}

// ---- retention and constraints ----------------------------------------------------------

func TestPurgeKeepsRecordsForSevenDays(t *testing.T) {
	e := newEnv(t)
	s := newService(t, e, &fakeSender{}, testKeys(t, "k1", "k1"))
	old := issueReg(t, s, "old@example.com")
	recent := issueReg(t, s, "recent@example.com")
	waiting := issueReg(t, s, "waiting@example.com")
	finish := func(id uuid.UUID, at time.Time) {
		e.db.MailJob.UpdateOneID(id).SetStatus(mailjob.StatusAccepted).SetResultClass("ok").ClearCiphertext().ClearKeyID().SetFinishedAt(at).ExecX(ctx)
	}
	finish(old.JobID, e.now.Add(-8*24*time.Hour))
	finish(recent.JobID, e.now.Add(-6*24*time.Hour))
	n, err := s.Purge(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purged %d %v", n, err)
	}
	if _, err = e.db.MailJob.Get(ctx, old.JobID); !ent.IsNotFound(err) {
		t.Fatal("old record kept")
	}
	for _, id := range []uuid.UUID{recent.JobID, waiting.JobID} {
		if _, err = e.db.MailJob.Get(ctx, id); err != nil {
			t.Fatalf("record %s lost: %v", id, err)
		}
	}
}

func TestDatabaseRefusesSecretsInFinalJobsAndMissingSecretsInLiveOnes(t *testing.T) {
	e := newEnv(t)
	s := newService(t, e, &fakeSender{}, testKeys(t, "k1", "k1"))
	out := issueReg(t, s, "alice@example.com")
	exec := func(q string, args ...any) error { _, err := e.raw.ExecContext(ctx, q, args...); return err }
	// A final state must not keep ciphertext.
	if err := exec(`UPDATE mail_jobs SET status='accepted', finished_at=now() WHERE id=$1`, out.JobID); err == nil {
		t.Fatal("an accepted job may keep its ciphertext")
	}
	// A live job must have it.
	if err := exec(`UPDATE mail_jobs SET ciphertext=NULL, key_id=NULL WHERE id=$1`, out.JobID); err == nil {
		t.Fatal("a pending job without ciphertext")
	}
	// Unknown status and provider, too many attempts.
	if err := exec(`UPDATE mail_jobs SET status='lost' WHERE id=$1`, out.JobID); err == nil {
		t.Fatal("unknown status")
	}
	if err := exec(`UPDATE mail_jobs SET attempts=4 WHERE id=$1`, out.JobID); err == nil {
		t.Fatal("4 attempts")
	}
	if err := exec(`UPDATE mail_jobs SET status='sending' WHERE id=$1`, out.JobID); err == nil {
		t.Fatal("sending without a lease")
	}
}

// ---- end to end with a real adapter over a controlled endpoint -----------------------------

func TestEndToEndThroughARealAdapter(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	var bodies []string
	srv := newVendor(t, func(body string) int { mu.Lock(); bodies = append(bodies, body); mu.Unlock(); return 202 })
	cfg := mailCfg("sendgrid", 1)
	m, err := mailer.New(cfg, mailer.WithTransport(srv.transport()))
	if err != nil {
		t.Fatal(err)
	}
	links, _ := mailer.NewLinks(map[string]string{"trip-map": "https://map.example.com"}, false)
	s, err := New(e.db, testKeys(t, "k1", "k1"), links, m, Settings{RegistrationTTL: 30 * time.Minute, ResetTTL: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.IssueStandalone(ctx, IssueInput{Purpose: PurposeRegistration, Email: "alice@example.com", AppID: "trip-map"})
	if err != nil {
		t.Fatal(err)
	}
	_, token := tokenOf(t, s, job(t, e, out.JobID))
	if _, err = s.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "https://map.example.com/account/verify-email#token="+token) || !strings.Contains(bodies[0], "alice@example.com") {
		t.Fatalf("vendor saw %v", bodies)
	}
	assertFinal(t, job(t, e, out.JobID), mailjob.StatusAccepted, ResultOK)
}

// vendor is a controlled HTTP endpoint: the transport sends every request to it.
type vendor struct{ srv *httptest.Server }

func newVendor(t *testing.T, handle func(body string) int) *vendor {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(handle(string(b)))
	}))
	t.Cleanup(srv.Close)
	return &vendor{srv: srv}
}

func (v *vendor) transport() http.RoundTripper {
	base := http.DefaultTransport.(*http.Transport).Clone()
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		c := req.Clone(req.Context())
		c.URL.Scheme, c.URL.Host = "http", v.srv.Listener.Addr().String()
		return base.RoundTrip(c)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// ---- logs ----------------------------------------------------------------------------

// Whatever happens to a job, the structured log carries the task ID, provider,
// template, status, error class, attempt count and duration, and never an
// address, a token, a link, a key or vendor text (spec 7.3).
func TestLogsNeverContainAddressesTokensLinksOrKeys(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	e := newEnv(t)
	outcomes := []error{
		nil,
		&mailer.Error{Class: mailer.ClassTemporary, Provider: "sendgrid", Code: "http_503", Detail: "vendor said alice@example.com"},
		&mailer.Error{Class: mailer.ClassPermanent, Provider: "sendgrid", Code: "http_401", Auth: true, Detail: "bad key SG.key for alice@example.com"},
		&mailer.Error{Class: mailer.ClassUncertain, Provider: "sendgrid", Code: "timeout"},
	}
	sender := &fakeSender{fn: func(n int, _ mailer.Message) error { return outcomes[(n-1)%len(outcomes)] }}
	s := newService(t, e, sender, testKeys(t, "k1", "k1"))
	var tokens []string
	var ids []uuid.UUID
	for i := 0; i < 4; i++ {
		out := issueReg(t, s, fmt.Sprintf("alice%d@example.com", i))
		ids = append(ids, out.JobID)
		_, tok := tokenOf(t, s, job(t, e, out.JobID))
		tokens = append(tokens, tok)
	}
	// A job whose ciphertext was tampered with produces the decrypt alert.
	bad := issueReg(t, s, "mallory@example.com")
	e.db.MailJob.UpdateOneID(bad.JobID).SetCiphertext([]byte("garbage-garbage-garbage-garbage")).ExecX(ctx)
	_, _ = s.RunOnce(ctx)
	e.advance(10 * time.Minute)
	_, _ = s.RunOnce(ctx)
	// A purge and a sweep run too.
	_, _ = s.Purge(ctx)

	logs := buf.String()
	if !strings.Contains(logs, "job_id=") || !strings.Contains(logs, "provider=sendgrid") || !strings.Contains(logs, "template=verify_email") ||
		!strings.Contains(logs, "attempts=") || !strings.Contains(logs, "duration_ms=") || !strings.Contains(logs, "class=") {
		t.Fatalf("the job log lacks its fields:\n%s", logs)
	}
	for _, alert := range []string{"mail_vendor_auth_failed", "mail_uncertain", "mail_decrypt_failed"} {
		if !strings.Contains(logs, alert) {
			t.Errorf("no %s alert in the log", alert)
		}
	}
	forbidden := append([]string{"alice", "mallory", "example.com", "/account/", "#token", "SG.key", "vendor said", keyOf('k'), "garbage-garbage"}, tokens...)
	for _, f := range forbidden {
		if strings.Contains(logs, f) {
			t.Fatalf("the log contains %q:\n%s", f, logs)
		}
	}
	_ = ids
}
