package outbox

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/accounttoken"
	"github.com/kongken/mono/internal/usersrv/ent/mailjob"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/identity"
	"github.com/kongken/mono/internal/usersrv/mailer"
)

// The two challenge purposes (account_tokens.purpose).
const (
	PurposeRegistration  = "registration"
	PurposePasswordReset = "password_reset"
)

// Delivery rules (spec 7.3).
const (
	// MaxAttempts is the number of send attempts of one job.
	MaxAttempts = 3
	// RecordRetention is how long the status record of a finished job is kept.
	RecordRetention = 7 * 24 * time.Hour
	// leaseMargin is added to the send timeout to get the lease length.
	leaseMargin = 30 * time.Second
	// ResultOK is the result class of an accepted job.
	ResultOK = "ok"
)

// RetryBackoff are the waits after the first and the second temporary failure.
var RetryBackoff = []time.Duration{time.Minute, 5 * time.Minute}

// Settings are the service parameters that come from configuration.
type Settings struct {
	// RegistrationTTL and ResetTTL are the challenge lifetimes.
	RegistrationTTL time.Duration
	ResetTTL        time.Duration
	// BatchSize is how many due jobs one pass takes (default 20).
	BatchSize int
	// Concurrency is how many jobs of one pass are sent in parallel (default 4).
	Concurrency int
	// Backlog is the age of a waiting job that raises an alert (default 5 minutes).
	Backlog time.Duration
}

// Service is the outbox: issuing challenges with their mail, delivering them,
// and housekeeping.
type Service struct {
	db       *ent.Client
	keys     *Keyring
	links    *mailer.Links
	mailer   *mailer.Mailer
	settings Settings
	owner    string
	now      func() time.Time
	stats    Stats
}

// New returns the outbox service. The mailer decides the provider and the
// configuration generation that new jobs are pinned to.
func New(db *ent.Client, keys *Keyring, links *mailer.Links, m *mailer.Mailer, st Settings) (*Service, error) {
	if db == nil || keys == nil || links == nil || m == nil {
		return nil, errors.New("outbox: database, keys, links and mailer are required")
	}
	if st.RegistrationTTL <= 0 || st.ResetTTL <= 0 {
		return nil, errors.New("outbox: token lifetimes are required")
	}
	if st.BatchSize <= 0 {
		st.BatchSize = 20
	}
	if st.Concurrency <= 0 {
		st.Concurrency = 4
	}
	if st.Backlog <= 0 {
		st.Backlog = 5 * time.Minute
	}
	return &Service{db: db, keys: keys, links: links, mailer: m, settings: st,
		owner: uuid.NewString(), now: func() time.Time { return time.Now().UTC() }}, nil
}

// SetClock replaces the time source; tests use it.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// ErrUnknownApp is returned when the app ID is not in the link allow list.
var ErrUnknownApp = mailer.ErrUnknownApp

// HasApp reports whether appID is in the link allow list.
func (s *Service) HasApp(appID string) bool { return s.links.Has(appID) }

// IssueInput describes one challenge and its mail.
type IssueInput struct {
	// Purpose is PurposeRegistration or PurposePasswordReset.
	Purpose string
	// Email is the recipient (normalized here).
	Email string
	// UserID and AuthVersion are required for a password reset and must be nil
	// for a registration (the account does not exist yet).
	UserID      *uuid.UUID
	AuthVersion *int64
	// AppID selects the link origin; it must be a configured app.
	AppID string
}

// Issued is the outcome of Issue. The plaintext token is not returned: it exists
// only in the encrypted job and in the mail.
type Issued struct {
	ChallengeID uuid.UUID
	JobID       uuid.UUID
	ExpiresAt   time.Time
}

type payload struct {
	Version int    `json:"v"`
	To      string `json:"to"`
	AppID   string `json:"app_id"`
	Token   string `json:"token"`
}

func templateOf(purpose string) (mailer.Kind, bool) {
	switch purpose {
	case PurposeRegistration:
		return mailer.KindVerifyEmail, true
	case PurposePasswordReset:
		return mailer.KindResetPassword, true
	}
	return "", false
}

// Issue creates the challenge and its encrypted mail job inside tx, so either
// both exist or neither does. The caller owns the transaction and commits it
// together with whatever else must be atomic with it. It does not wait for, or
// perform, any sending.
func (s *Service) Issue(ctx context.Context, tx *ent.Tx, in IssueInput) (Issued, error) {
	kind, ok := templateOf(in.Purpose)
	if !ok {
		return Issued{}, errors.New("outbox: unknown purpose")
	}
	email, err := identity.NormalizeEmail(in.Email)
	if err != nil {
		return Issued{}, err
	}
	if !s.links.Has(in.AppID) {
		return Issued{}, ErrUnknownApp
	}
	ttl := s.settings.RegistrationTTL
	switch in.Purpose {
	case PurposeRegistration:
		if in.UserID != nil || in.AuthVersion != nil {
			return Issued{}, errors.New("outbox: a registration challenge binds no user")
		}
	case PurposePasswordReset:
		ttl = s.settings.ResetTTL
		if in.UserID == nil || in.AuthVersion == nil || *in.AuthVersion < 1 {
			return Issued{}, errors.New("outbox: a reset challenge binds a user and an auth version")
		}
	}
	rawToken := make([]byte, 32)
	if _, err = rand.Read(rawToken); err != nil {
		return Issued{}, fmt.Errorf("outbox: random: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	digest, _ := identity.TokenDigest(token)

	now := s.now()
	expires := now.Add(ttl)
	challengeID, jobID := uuid.New(), uuid.New()

	plain, err := json.Marshal(payload{Version: 1, To: email, AppID: in.AppID, Token: token})
	if err != nil {
		return Issued{}, err
	}
	ciphertext, keyID, err := s.keys.seal(jobID.String(), string(kind), plain)
	if err != nil {
		return Issued{}, fmt.Errorf("outbox: seal: %w", err)
	}

	create := tx.AccountToken.Create().SetID(challengeID).SetTokenHash(digest).SetPurpose(accounttoken.Purpose(in.Purpose)).
		SetEmailCanonical(email).SetAppID(in.AppID).SetExpiresAt(expires).SetCreatedAt(now)
	if in.UserID != nil {
		create.SetUserID(*in.UserID).SetIssuedAuthVersion(*in.AuthVersion)
	}
	if err = create.Exec(ctx); err != nil {
		return Issued{}, err
	}
	err = tx.MailJob.Create().SetID(jobID).SetChallengeID(challengeID).SetProvider(s.mailer.Provider()).
		SetGeneration(s.mailer.Generation()).SetTemplate(string(kind)).SetCiphertext(ciphertext).SetKeyID(keyID).
		SetNextAttemptAt(now).SetExpiresAt(expires).SetCreatedAt(now).SetUpdatedAt(now).Exec(ctx)
	if err != nil {
		return Issued{}, err
	}
	return Issued{ChallengeID: challengeID, JobID: jobID, ExpiresAt: expires}, nil
}

// IssueStandalone runs Issue in a transaction of its own.
func (s *Service) IssueStandalone(ctx context.Context, in IssueInput) (Issued, error) {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return Issued{}, err
	}
	out, err := s.Issue(ctx, tx, in)
	if err != nil {
		_ = tx.Rollback()
		return Issued{}, err
	}
	if err = tx.Commit(); err != nil {
		return Issued{}, err
	}
	return out, nil
}

// Preflight refuses to run when a waiting job was encrypted with a key that is
// no longer configured. Dropping such a job silently would lose a mail; the
// operator must restore the key (or cancel the jobs on purpose).
func (s *Service) Preflight(ctx context.Context) error {
	ids, err := s.db.MailJob.Query().Where(mailjob.StatusIn(mailjob.StatusPending, mailjob.StatusSending)).
		Where(mailjob.KeyIDNotNil()).GroupBy(mailjob.FieldKeyID).Strings(ctx)
	if err != nil {
		return fmt.Errorf("outbox preflight: %w", err)
	}
	var missing []string
	for _, id := range ids {
		if !s.keys.Has(id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		slog.Error("usersrv mail outbox: queued jobs need decryption keys that are not configured", "alert", "mail_key_missing", "key_ids", missing)
		return fmt.Errorf("outbox preflight: queued mail jobs need encryption keys that are not configured: %v", missing)
	}
	return nil
}

// RunOnce is one worker pass: settle lost leases, send the jobs that are due,
// report a backlog. It returns the number of jobs it took.
func (s *Service) RunOnce(ctx context.Context) (int, error) {
	if err := s.sweepLeases(ctx); err != nil {
		return 0, err
	}
	now := s.now()
	due, err := s.db.MailJob.Query().Where(mailjob.StatusEQ(mailjob.StatusPending), mailjob.NextAttemptAtLTE(now)).
		Order(ent.Asc(mailjob.FieldNextAttemptAt)).Limit(s.settings.BatchSize).IDs(ctx)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, s.settings.Concurrency)
	var taken int
	var mu sync.Mutex
	for _, id := range due {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("usersrv mail job panic", "job_id", id, "panic", r)
				}
			}()
			if s.process(ctx, id) {
				mu.Lock()
				taken++
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	s.checkBacklog(ctx, now)
	return taken, nil
}

// checkBacklog raises an alert when a due job has waited too long.
func (s *Service) checkBacklog(ctx context.Context, now time.Time) {
	n, err := s.db.MailJob.Query().Where(mailjob.StatusEQ(mailjob.StatusPending),
		mailjob.NextAttemptAtLT(now.Add(-s.settings.Backlog))).Count(ctx)
	if err == nil && n > 0 {
		slog.Warn("usersrv mail outbox backlog", "alert", "mail_backlog", "waiting_jobs", n, "older_than", s.settings.Backlog.String())
	}
}

// sweepLeases settles jobs whose worker vanished. The send may have happened, so
// the job becomes uncertain: it is never put back to pending.
func (s *Service) sweepLeases(ctx context.Context) error {
	now := s.now()
	n, err := s.db.MailJob.Update().
		Where(mailjob.StatusEQ(mailjob.StatusSending), mailjob.LeaseExpiresAtLT(now)).
		SetStatus(mailjob.StatusUncertain).SetResultClass("lease_expired").
		ClearCiphertext().ClearKeyID().ClearLeaseOwner().ClearLeaseExpiresAt().SetFinishedAt(now).Save(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		s.stats.add("uncertain", n)
		slog.Error("usersrv mail jobs lost their worker; marked uncertain, not re-sent", "alert", "mail_uncertain", "jobs", n, "reason", "lease_expired")
	}
	return nil
}

// Purge deletes the records of jobs that finished more than RecordRetention ago.
func (s *Service) Purge(ctx context.Context) (int, error) {
	return s.db.MailJob.Delete().Where(mailjob.FinishedAtLT(s.now().Add(-RecordRetention))).Exec(ctx)
}

// Counts returns the number of jobs per status; operators and tests read it.
func (s *Service) Counts(ctx context.Context) (map[string]int, error) {
	var rows []struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	if err := s.db.MailJob.Query().GroupBy(mailjob.FieldStatus).Aggregate(ent.Count()).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Status] = r.Count
	}
	return out, nil
}

// skip describes why a job is not sent: the final status and the result class.
type skip struct {
	status mailjob.Status
	class  string
}

// recheck confirms, right before sending, that the challenge is still usable and
// the account still allows the mail (spec 7.2): a consumed, expired or missing
// challenge, a disabled or deleted account, or a changed auth version cancels
// the job. The mail that is already in flight cannot be recalled; its link is
// dead by then because the same rules are enforced when it is used.
func (s *Service) recheck(ctx context.Context, job *ent.MailJob, now time.Time) (*skip, error) {
	tok, err := s.db.AccountToken.Get(ctx, job.ChallengeID)
	if ent.IsNotFound(err) {
		return &skip{mailjob.StatusCancelled, "challenge_missing"}, nil
	}
	if err != nil {
		return nil, err
	}
	switch {
	case tok.ConsumedAt != nil:
		return &skip{mailjob.StatusCancelled, "challenge_consumed"}, nil
	case !tok.ExpiresAt.After(now):
		return &skip{mailjob.StatusExpired, "challenge_expired"}, nil
	}
	switch string(tok.Purpose) {
	case PurposeRegistration:
		if job.Template != string(mailer.KindVerifyEmail) {
			return &skip{mailjob.StatusCancelled, "template_mismatch"}, nil
		}
		taken, err := s.db.User.Query().Where(user.EmailCanonical(tok.EmailCanonical)).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if taken {
			return &skip{mailjob.StatusCancelled, "email_registered"}, nil
		}
	case PurposePasswordReset:
		if job.Template != string(mailer.KindResetPassword) || tok.UserID == nil || tok.IssuedAuthVersion == nil {
			return &skip{mailjob.StatusCancelled, "template_mismatch"}, nil
		}
		u, err := s.db.User.Get(ctx, *tok.UserID)
		if ent.IsNotFound(err) {
			return &skip{mailjob.StatusCancelled, "account_gone"}, nil
		}
		if err != nil {
			return nil, err
		}
		switch {
		case u.Status != user.StatusActive:
			return &skip{mailjob.StatusCancelled, "account_inactive"}, nil
		case u.AuthVersion != *tok.IssuedAuthVersion:
			return &skip{mailjob.StatusCancelled, "version_mismatch"}, nil
		}
	default:
		return &skip{mailjob.StatusCancelled, "template_mismatch"}, nil
	}
	return nil, nil
}
