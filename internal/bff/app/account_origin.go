package app

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/navigation"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// accountServices are the browser services of the centralised account: sign-in, registration,
// password reset, the security settings, the profile and the account closing. They belong to the
// user center, and only its registered origin may call them (accountOriginPolicy), whatever the
// method. A service is listed here by name, so a procedure added to one of them later is covered
// without being listed anywhere; a test fails when a browser service is in neither this list nor
// the admin and product lists.
var accountServices = []string{
	bffv1connect.AuthServiceName,
	bffv1connect.UserServiceName,
}

// accountOpenProcedures are the account procedures every registered application keeps. They are
// what a product needs to know who is signed in and to end its own use of the session: reading
// the identity, getting a CSRF token, resolving where to send the browser, and signing out
// (idempotent). None of them changes a password, a profile, another device or the account.
var accountOpenProcedures = map[string]bool{
	bffv1connect.AuthServiceMeProcedure:                true,
	bffv1connect.AuthServiceIssueCSRFTokenProcedure:    true,
	bffv1connect.AuthServiceResolveNavigationProcedure: true,
	bffv1connect.AuthServiceLogoutProcedure:            true,
}

// legacyMailProcedures are the two account procedures a product's old mail landing page calls
// (the pages that only exist for mails sent before the central landing). They are the only account
// procedures a transition origin may use, and only until the transition ends.
var legacyMailProcedures = map[string]bool{
	bffv1connect.AuthServiceCompleteRegistrationProcedure: true,
	bffv1connect.AuthServiceResetPasswordProcedure:        true,
}

// isAccountProcedure reports whether procedure ("/bff.v1.AuthService/ChangePassword") belongs to
// an account service.
func isAccountProcedure(procedure string) bool {
	for _, name := range accountServices {
		if strings.HasPrefix(procedure, "/"+name+"/") {
			return true
		}
	}
	return false
}

// accountOriginPolicy is the final source rule of the account procedures. Before it, any origin in
// cors_allowed_origins that passed the Origin/CSRF check could call them; now:
//
//   - the account procedures need the user center's own Origin, exactly (the registered
//     navigation.apps.ozid.origin), except the open ones, which every registered application keeps;
//   - CompleteRegistration and ResetPassword are also taken from the configured product origins,
//     while the old-mail transition lasts (auth.legacy_mail_transition), and not after it;
//   - the management procedures keep the admin perimeter (adminguard), unchanged.
//
// Nothing else changes: the CSRF check, the session and the business rules apply after this. It
// reads no session and no body, so it refuses before anything is looked up.
type accountOriginPolicy struct {
	ozid   string
	legacy []string
	until  time.Time
	now    func() time.Time
}

// newAccountOriginPolicy builds the rule from the validated configuration. accounts says whether this
// BFF serves the account procedures at all (a usersrv upstream): when it does, the registered OzID
// origin is mandatory and its absence is an error, never an unrestricted policy. The policy is nil
// only when no account procedure is served, so there is nothing to restrict.
func newAccountOriginPolicy(cfg *config.Config, accounts bool) (*accountOriginPolicy, error) {
	if !accounts {
		return nil, nil
	}
	ozid := cfg.Navigation.Apps[navigation.AppOzID].Origin
	if ozid == "" {
		return nil, errors.New("navigation.apps.ozid.origin is required while the account procedures are served")
	}
	p := &accountOriginPolicy{ozid: ozid, now: time.Now}
	tr := cfg.Auth.LegacyMailTransition
	if len(tr.Origins) > 0 {
		until, err := tr.UntilTime()
		if err != nil {
			return nil, err
		}
		p.legacy, p.until = slices.Clone(tr.Origins), until
	}
	return p, nil
}

// errAccountOrigin is the single refusal, the same words as every other refused call.
var errAccountOrigin = errors.New("request rejected")

// Check admits the call or refuses it. Procedures outside the account services are not its
// business.
func (p *accountOriginPolicy) Check(procedure string, h http.Header) error {
	if p == nil || !isAccountProcedure(procedure) || accountOpenProcedures[procedure] {
		return nil
	}
	origins := h.Values("Origin")
	if len(origins) != 1 {
		return errAccountOrigin
	}
	origin := origins[0]
	if origin == p.ozid {
		return nil
	}
	if legacyMailProcedures[procedure] && p.legacyActive() && slices.Contains(p.legacy, origin) {
		return nil
	}
	return errAccountOrigin
}

func (p *accountOriginPolicy) legacyActive() bool {
	return len(p.legacy) > 0 && p.now().Before(p.until)
}

// legacyExpired reports a configured transition whose end has passed: the origins are no longer
// accepted, and the section is only left over.
func (p *accountOriginPolicy) legacyExpired() bool {
	return p != nil && len(p.legacy) > 0 && !p.now().Before(p.until)
}
