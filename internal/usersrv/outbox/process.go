package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/mailjob"
	"github.com/kongken/mono/internal/usersrv/mailer"
)

// Stats counts outcomes since the process started. There is no metrics exporter
// in this repository yet; the counters feed the structured log lines and tests,
// and a future exporter can read Snapshot. Nothing in them identifies a mailbox.
type Stats struct {
	mu sync.Mutex
	m  map[string]int
}

func (s *Stats) add(name string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]int{}
	}
	s.m[name] += n
}

// Snapshot returns a copy of the counters.
func (s *Service) Snapshot() map[string]int {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	out := make(map[string]int, len(s.stats.m))
	for k, v := range s.stats.m {
		out[k] = v
	}
	return out
}

// claim takes the job with a compare-and-set on status, so of several replicas
// exactly one wins. The lease outlives the send timeout; a worker that dies
// leaves a job the sweep settles as uncertain.
func (s *Service) claim(ctx context.Context, id uuid.UUID) (*ent.MailJob, error) {
	now := s.now()
	timeout := s.mailer.Timeout()
	n, err := s.db.MailJob.Update().
		Where(mailjob.ID(id), mailjob.StatusEQ(mailjob.StatusPending), mailjob.NextAttemptAtLTE(now)).
		SetStatus(mailjob.StatusSending).SetLeaseOwner(s.owner).SetLeaseExpiresAt(now.Add(timeout + leaseMargin)).AddAttempts(1).
		Save(ctx)
	if err != nil || n != 1 {
		return nil, err
	}
	return s.db.MailJob.Get(ctx, id)
}

// finish records a final state and erases the secrets, guarded by the lease so a
// worker whose lease was already settled cannot overwrite the outcome.
func (s *Service) finish(ctx context.Context, job *ent.MailJob, status mailjob.Status, class string) bool {
	now := s.now()
	n, err := s.db.MailJob.Update().
		Where(mailjob.ID(job.ID), mailjob.StatusEQ(mailjob.StatusSending), mailjob.LeaseOwner(s.owner)).
		SetStatus(status).SetResultClass(class).ClearCiphertext().ClearKeyID().ClearLeaseOwner().ClearLeaseExpiresAt().
		SetFinishedAt(now).Save(ctx)
	if err != nil || n != 1 {
		slog.Error("usersrv mail job result not recorded", "job_id", job.ID, "status", status, "error", err)
		return false
	}
	return true
}

// retryLater puts a job back to pending with its ciphertext, after a temporary failure.
func (s *Service) retryLater(ctx context.Context, job *ent.MailJob, at time.Time, class string) bool {
	n, err := s.db.MailJob.Update().
		Where(mailjob.ID(job.ID), mailjob.StatusEQ(mailjob.StatusSending), mailjob.LeaseOwner(s.owner)).
		SetStatus(mailjob.StatusPending).SetResultClass(class).SetNextAttemptAt(at).ClearLeaseOwner().ClearLeaseExpiresAt().Save(ctx)
	if err != nil || n != 1 {
		slog.Error("usersrv mail job retry not recorded", "job_id", job.ID, "error", err)
		return false
	}
	return true
}

// record logs one job outcome. The fields are exactly the ones the spec allows:
// task ID, provider, template, status, error class, attempt count, duration.
func (s *Service) record(job *ent.MailJob, status, class, code string, started time.Time, alert string) {
	s.stats.add(status, 1)
	attrs := []any{"job_id", job.ID, "provider", job.Provider, "template", job.Template, "status", status,
		"class", class, "attempts", job.Attempts, "duration_ms", time.Since(started).Milliseconds()}
	if code != "" {
		attrs = append(attrs, "code", code)
	}
	if alert != "" {
		attrs = append(attrs, "alert", alert)
		slog.Error("usersrv mail job", attrs...)
		return
	}
	slog.Info("usersrv mail job", attrs...)
}

// process claims and handles one job. It reports whether this worker took it.
func (s *Service) process(ctx context.Context, id uuid.UUID) bool {
	job, err := s.claim(ctx, id)
	if err != nil {
		slog.Error("usersrv mail job claim failed", "job_id", id, "error", err)
		return false
	}
	if job == nil {
		return false // another replica took it
	}
	started := time.Now()
	// Recording the outcome and the vendor call run detached from shutdown: a
	// stopping worker lets the send in flight finish (bounded by the send timeout)
	// instead of abandoning it half-way.
	work := context.WithoutCancel(ctx)
	write, cancel := context.WithTimeout(work, 10*time.Second)
	defer cancel()
	now := s.now()

	switch {
	case !job.ExpiresAt.After(now):
		s.finish(write, job, mailjob.StatusExpired, "job_expired")
		s.record(job, "expired", "job_expired", "", started, "")
		return true
	case job.Provider != s.mailer.Provider() || job.Generation != s.mailer.Generation():
		// Pinned to a setup that is no longer active. Never re-route to another vendor.
		s.finish(write, job, mailjob.StatusCancelled, "config_changed")
		s.record(job, "cancelled", "config_changed", "", started, "")
		return true
	}

	var pl payload
	plain, err := s.keys.open(deref(job.KeyID), job.ID.String(), job.Template, job.Ciphertext)
	if err == nil {
		err = json.Unmarshal(plain, &pl)
		if err == nil && pl.Version != 1 {
			err = errors.New("unsupported payload")
		}
	}
	if err != nil {
		s.finish(write, job, mailjob.StatusFailed, "decrypt_failed")
		s.record(job, "failed", "decrypt_failed", "", started, "mail_decrypt_failed")
		return true
	}

	if sk, err := s.recheck(write, job, now); err != nil {
		// The database could not answer: give the job back untouched, without
		// counting this as a send attempt that failed at the vendor.
		s.release(write, job)
		slog.Warn("usersrv mail job recheck failed", "job_id", job.ID, "error", err)
		return true
	} else if sk != nil {
		s.finish(write, job, sk.status, sk.class)
		s.record(job, string(sk.status), sk.class, "", started, "")
		return true
	}

	kind := mailer.Kind(job.Template)
	// New mails carry the central app. A job queued before the central landing existed
	// names a product origin; it keeps that link for its short life, and one whose
	// origin is no longer configured lands at OzID, which accepts the same token.
	linkApp := pl.AppID
	if !s.links.Has(linkApp) {
		linkApp = mailer.CentralApp
	}
	link, err := s.links.Build(kind, linkApp, pl.Token)
	if err != nil {
		s.finish(write, job, mailjob.StatusCancelled, "link_unavailable")
		s.record(job, "cancelled", "link_unavailable", "", started, "")
		return true
	}
	tok, err := s.db.AccountToken.Get(write, job.ChallengeID)
	if err != nil {
		s.release(write, job)
		return true
	}
	content, err := mailer.Render(kind, mailer.TemplateData{AppName: mailer.AppName(linkApp), Link: link, ExpiresIn: tok.ExpiresAt.Sub(tok.CreatedAt)})
	if err != nil {
		s.finish(write, job, mailjob.StatusFailed, "render_failed")
		s.record(job, "failed", "render_failed", "", started, "mail_render_failed")
		return true
	}
	msg := s.mailer.Compose(job.ID.String(), pl.To, content)

	// The network call holds no database transaction.
	_, sendErr := s.mailer.Send(work, msg)
	settleCtx, settleCancel := context.WithTimeout(work, 10*time.Second)
	defer settleCancel()
	s.settle(settleCtx, job, sendErr, started)
	return true
}

// release returns a claimed job to pending unchanged and undoes the attempt count.
func (s *Service) release(ctx context.Context, job *ent.MailJob) {
	_, err := s.db.MailJob.Update().
		Where(mailjob.ID(job.ID), mailjob.StatusEQ(mailjob.StatusSending), mailjob.LeaseOwner(s.owner)).
		SetStatus(mailjob.StatusPending).AddAttempts(-1).SetNextAttemptAt(s.now().Add(30 * time.Second)).
		ClearLeaseOwner().ClearLeaseExpiresAt().Save(ctx)
	if err != nil {
		slog.Error("usersrv mail job release failed", "job_id", job.ID, "error", err)
	}
}

// settle turns the result of the vendor call into the job's next state.
func (s *Service) settle(ctx context.Context, job *ent.MailJob, sendErr error, started time.Time) {
	if sendErr == nil {
		if s.finish(ctx, job, mailjob.StatusAccepted, ResultOK) {
			s.record(job, "accepted", ResultOK, "", started, "")
		}
		return
	}
	var me *mailer.Error
	code, auth := "unknown", false
	if errors.As(sendErr, &me) {
		code, auth = me.Code, me.Auth
	}
	alertAuth := ""
	if auth {
		alertAuth = "mail_vendor_auth_failed"
	}
	switch mailer.ClassOf(sendErr) {
	case mailer.ClassPermanent:
		if s.finish(ctx, job, mailjob.StatusFailed, string(mailer.ClassPermanent)) {
			alert := "mail_permanent_failure"
			if alertAuth != "" {
				alert = alertAuth
			}
			s.record(job, "failed", string(mailer.ClassPermanent), code, started, alert)
		}
	case mailer.ClassTemporary:
		s.settleTemporary(ctx, job, code, started, alertAuth)
	default:
		// The message may have been accepted: never send it again.
		if s.finish(ctx, job, mailjob.StatusUncertain, string(mailer.ClassUncertain)) {
			s.record(job, "uncertain", string(mailer.ClassUncertain), code, started, "mail_uncertain")
		}
	}
}

func (s *Service) settleTemporary(ctx context.Context, job *ent.MailJob, code string, started time.Time, alert string) {
	now := s.now()
	if job.Attempts >= MaxAttempts {
		if s.finish(ctx, job, mailjob.StatusFailed, "temporary_exhausted") {
			if alert == "" {
				alert = "mail_permanent_failure"
			}
			s.record(job, "failed", "temporary_exhausted", code, started, alert)
		}
		return
	}
	next := now.Add(RetryBackoff[job.Attempts-1])
	if !next.Before(job.ExpiresAt) {
		// The next attempt would fall after the token's deadline: stop here.
		if s.finish(ctx, job, mailjob.StatusExpired, "token_expired") {
			s.record(job, "expired", "token_expired", code, started, "")
		}
		return
	}
	if s.retryLater(ctx, job, next, string(mailer.ClassTemporary)) {
		s.record(job, "retry", string(mailer.ClassTemporary), code, started, "")
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Pending reports whether any job still waits or is being sent; shutdown code
// and tests use it to know the queue is drained.
func (s *Service) Pending(ctx context.Context) (int, error) {
	return s.db.MailJob.Query().Where(mailjob.StatusIn(mailjob.StatusPending, mailjob.StatusSending)).Count(ctx)
}
