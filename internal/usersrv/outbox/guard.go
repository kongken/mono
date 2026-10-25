package outbox

import (
	"context"
	"errors"

	"github.com/kongken/mono/internal/usersrv/ratelimit"
)

// Decision is the outcome of the mail limits for one request.
type Decision int

const (
	// Allow: enqueue the mail.
	Allow Decision = iota
	// Suppress: an account-level limit (cooldown, hourly cap, daily-style cap per
	// mailbox) is hit. The caller answers with the same neutral success as for an
	// allowed request and sends nothing, so the response never tells whether the
	// mailbox exists or how much quota is left.
	Suppress
	// Limited: a public limit (per client address, or the service-wide cap) is
	// hit. The caller answers RESOURCE_EXHAUSTED.
	Limited
)

// Limiter counts attempts; *ratelimit.Limiter implements it.
type Limiter interface {
	Allow(ctx context.Context, rule ratelimit.Rule, subject ...string) (bool, error)
}

// Guard applies the mail limits of spec section 10. Every check is one atomic
// Redis increment, every attempt counts whether or not the address has an
// account, and when Redis cannot answer the request is refused with
// ratelimit.ErrUnavailable: it never falls back to sending.
type Guard struct {
	limiter Limiter
	rules   ratelimit.MailRules
}

// NewGuard returns the mail limiter.
func NewGuard(l Limiter, rules ratelimit.MailRules) (*Guard, error) {
	if l == nil {
		return nil, errors.New("outbox: limiter is required")
	}
	return &Guard{limiter: l, rules: rules}, nil
}

// Check counts one mail request for purpose (PurposeRegistration or
// PurposePasswordReset) by canonical email and client IP (empty when unknown).
// Order: client address, service total, then the mailbox cooldown, the mailbox
// per-purpose cap and the mailbox total cap. A request stopped by an earlier
// layer is not counted by the later ones.
func (g *Guard) Check(ctx context.Context, purpose, email, clientIP string) (Decision, error) {
	var perIP, cooldown, perEmail ratelimit.Rule
	switch purpose {
	case PurposeRegistration:
		perIP, cooldown, perEmail = g.rules.RegistrationPerIP, g.rules.RegistrationEmailCooldown, g.rules.RegistrationPerEmail
	case PurposePasswordReset:
		perIP, cooldown, perEmail = g.rules.ResetPerIP, g.rules.ResetEmailCooldown, g.rules.ResetPerEmail
	default:
		return Limited, errors.New("outbox: unknown purpose")
	}
	steps := []struct {
		rule    ratelimit.Rule
		subject string
		verdict Decision
		skip    bool
	}{
		{perIP, clientIP, Limited, clientIP == ""},
		{g.rules.MailGlobal, "global", Limited, false},
		{cooldown, email, Suppress, false},
		{perEmail, email, Suppress, false},
		{g.rules.MailPerEmail, email, Suppress, false},
	}
	for _, st := range steps {
		if st.skip {
			continue
		}
		ok, err := g.limiter.Allow(ctx, st.rule, st.subject)
		if err != nil {
			return Limited, err
		}
		if !ok {
			return st.verdict, nil
		}
	}
	return Allow, nil
}
