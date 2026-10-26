package e2e_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	bffapp "github.com/kongken/mono/internal/bff/app"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/navigation"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

// accountOriginHarness is the registry of ozidHarness plus the console, with the old-mail transition
// of the scenario (none when it is the zero value).
func accountOriginHarness(t *testing.T, transition bffconfig.LegacyMailTransition) *ChainHarness {
	t.Helper()
	return NewChainHarness(t, ChainHTTPOptions{
		Configure: func(cfg *bffconfig.Config, baseURL string) {
			cfg.CORSAllowedOrigins = []string{ozidOrigin, tripOrigin, metroOrigin, adminOrigin}
			cfg.Navigation = navigation.Config{
				APIOrigin: baseURL,
				Apps: map[string]navigation.App{
					"ozid":     {Origin: ozidOrigin, Paths: []string{"/security"}},
					"admin":    {Origin: adminOrigin, Paths: []string{"/users"}},
					"trip-map": {Origin: tripOrigin},
					"metro":    {Origin: metroOrigin, Paths: []string{"/me"}},
				},
			}
			cfg.Auth.LegacyMailTransition = transition
		},
	})
}

// accountCall is one browser account procedure that changes something or shows a device list; the
// open ones (Me, the CSRF token, navigation, sign-out) are checked separately.
type accountCall struct {
	name string
	run  func(context.Context, *Browser) error
}

func accountCalls(email string) []accountCall {
	return []accountCall{
		{"RequestRegistration", func(ctx context.Context, b *Browser) error {
			_, err := b.RequestRegistration(ctx, "nobody-registers@example.com", "")
			return err
		}},
		{"LoginWithEmail", func(ctx context.Context, b *Browser) error {
			_, err := b.LoginWithEmail(ctx, email, E2EPassword)
			return err
		}},
		{"RequestPasswordReset", func(ctx context.Context, b *Browser) error {
			_, err := b.RequestPasswordReset(ctx, "nobody-resets@example.com", "")
			return err
		}},
		{"Reauthenticate", func(ctx context.Context, b *Browser) error { return b.Reauthenticate(ctx, E2EPassword) }},
		{"ChangePassword", func(ctx context.Context, b *Browser) error {
			return b.ChangePassword(ctx, E2EPassword, "A-new-pass-77!")
		}},
		{"UpdateMyProfile", func(ctx context.Context, b *Browser) error {
			_, err := b.UpdateMyProfile(ctx, "Renamed", "")
			return err
		}},
		{"ListSessions", func(ctx context.Context, b *Browser) error { _, err := b.ListSessions(ctx); return err }},
		{"RevokeSession", func(ctx context.Context, b *Browser) error { return b.RevokeSession(ctx, "no-such-session") }},
		{"RevokeOtherSessions", func(ctx context.Context, b *Browser) error { _, err := b.RevokeOtherSessions(ctx); return err }},
		{"GetAccountDeletion", func(ctx context.Context, b *Browser) error { _, err := b.GetAccountDeletion(ctx); return err }},
		{"RetryAccountDeletion", func(ctx context.Context, b *Browser) error { _, err := b.RetryAccountDeletion(ctx); return err }},
		{"CompleteRegistration", func(ctx context.Context, b *Browser) error {
			return b.CompleteRegistration(ctx, "not-a-token", E2EPassword, "X")
		}},
		{"ResetPassword", func(ctx context.Context, b *Browser) error {
			return b.ResetPassword(ctx, "not-a-token", "A-new-pass-77!")
		}},
	}
}

// rejected reports a refusal by the origin rule: PERMISSION_DENIED with the one neutral message.
func rejected(err error) bool {
	return connect.CodeOf(err) == connect.CodePermissionDenied && err != nil && err.Error() == "permission_denied: request rejected"
}

// The final source rule: with the registry in place, the account procedures take the user center's
// origin only. A registered product, the console, an unregistered same-site sibling, a foreign origin,
// "null", no Origin and two Origins are all refused before anything runs, and what they tried to
// change did not change. OzID's own calls (the same ones) are not refused by this rule.
func TestChainAccountProceduresTakeOnlyTheUserCenterOrigin(t *testing.T) {
	h := accountOriginHarness(t, bffconfig.LegacyMailTransition{})
	ctx := context.Background()
	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "origin-rule", "", "Origin Rule"); err != nil {
		t.Fatal(err)
	}
	const email = "origin-rule@example.com"
	before := len(h.Inbox.To(email))

	for _, origin := range []string{tripOrigin, metroOrigin, adminOrigin, siblingOrigin, "https://localhost:5175", "http://localhost.evil.test:5175", "null", ""} {
		b.Origin = origin
		for _, call := range accountCalls(email) {
			if err := call.run(ctx, b); !rejected(err) {
				t.Errorf("%s from origin %q: %v, want the origin rule's refusal", call.name, origin, err)
			}
		}
	}
	// Nothing happened: still the same password, name and session; no mail went out.
	h.DeliverMail()
	if got := len(h.Inbox.To("nobody-registers@example.com")) + len(h.Inbox.To("nobody-resets@example.com")); got != 0 {
		t.Errorf("a refused request still queued %d mails", got)
	}
	if len(h.Inbox.To(email)) != before {
		t.Error("a refused request still mailed the account")
	}
	b.Origin = h.BFFURL // the harness default: the account clients then speak as OzID
	if u, err := b.Me(ctx); err != nil || u.GetDisplayName() != "Origin Rule" {
		t.Fatalf("after the refusals Me = %v, %v", u, err)
	}
	if _, err := h.FreshBrowser().LoginWithEmail(ctx, email, E2EPassword); err != nil {
		t.Fatalf("the password changed although the change was refused: %v", err)
	}

	// Two Origin headers are no origin either.
	dup := h.FreshBrowser()
	dup.Origin = ozidOrigin
	req := connect.NewRequest(&bffv1.ReauthenticateRequest{Password: E2EPassword})
	dup.Write(ctx, req)
	req.Header().Add("Origin", tripOrigin)
	if _, err := dup.Auth.Reauthenticate(ctx, req); !rejected(err) {
		t.Errorf("two Origin headers: %v", err)
	}

	// OzID itself: the same calls get through the rule (and succeed where the data allows).
	b.Origin = ozidOrigin
	if _, err := b.RevokeOtherSessions(ctx); err != nil {
		t.Errorf("RevokeOtherSessions from OzID: %v", err)
	}
	if _, err := b.ListSessions(ctx); err != nil {
		t.Errorf("ListSessions from OzID: %v", err)
	}
	if _, err := b.UpdateMyProfile(ctx, "Renamed By OzID", ""); err != nil {
		t.Errorf("UpdateMyProfile from OzID: %v", err)
	}
	if err := b.ChangePassword(ctx, E2EPassword, "A-new-pass-77!"); err != nil {
		t.Errorf("ChangePassword from OzID: %v", err)
	}
}

// What a registered application keeps: who is signed in, the CSRF token, where to send the browser
// and sign-out, from every registered origin, with the same Cookie. Sign-out is idempotent and ends
// only that session.
func TestChainRegisteredApplicationsKeepIdentityAndSignOut(t *testing.T) {
	h := accountOriginHarness(t, bffconfig.LegacyMailTransition{})
	ctx := context.Background()
	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "open-procedures", "", "Open"); err != nil {
		t.Fatal(err)
	}
	other := h.FreshBrowser()
	if err := other.Login(ctx, "open-procedures", "", ""); err != nil {
		t.Fatal(err)
	}

	for _, origin := range []string{tripOrigin, metroOrigin, adminOrigin, ozidOrigin} {
		b.Origin = origin
		if u, err := b.Me(ctx); err != nil || u.GetDisplayName() != "Open" {
			t.Errorf("Me from %s: %v, %v", origin, u, err)
		}
		if _, err := b.resolve(ctx, "metro", "/me"); err != nil {
			t.Errorf("ResolveNavigation from %s: %v", origin, err)
		}
	}
	b.Origin = metroOrigin
	if err := b.Logout(ctx); err != nil {
		t.Fatalf("Logout from a product: %v", err)
	}
	if err := b.Logout(ctx); err != nil {
		t.Errorf("Logout again (idempotent): %v", err)
	}
	if signedIn(ctx, b) {
		t.Error("the session survived the sign-out")
	}
	if !signedIn(ctx, other) {
		t.Error("signing out one browser ended another browser's session")
	}
}

// The old-mail transition: while it lasts, the two registered products' own mail landing pages may
// complete a token (CompleteRegistration, ResetPassword) and nothing else; the user center and the console are never
// in it; once it has ended the products are refused like anyone else.
func TestChainLegacyMailTransitionIsNarrowAndEnds(t *testing.T) {
	ctx := context.Background()
	active := bffconfig.LegacyMailTransition{
		Origins: []string{tripOrigin, metroOrigin},
		Until:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}
	h := accountOriginHarness(t, active)

	// A mail issued by OzID before the cut-over: its link would have gone to the product's own page.
	asker := h.FreshBrowser()
	asker.Origin = ozidOrigin
	const email = "old-mail@example.com"
	if err := asker.requestRegistrationFrom(ctx, email, "", ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	token := h.Inbox.TokenIn(t, email)

	product := h.FreshBrowser()
	product.Origin = tripOrigin
	// Everything else the product might still try is refused, even in the transition.
	for _, call := range accountCalls(email) {
		if call.name == "CompleteRegistration" || call.name == "ResetPassword" {
			continue
		}
		if err := call.run(ctx, product); !rejected(err) {
			t.Errorf("%s from a product during the transition: %v", call.name, err)
		}
	}
	// The user center and the console are not transition origins: the console is refused the token calls.
	console := h.FreshBrowser()
	console.Origin = adminOrigin
	if err := console.CompleteRegistration(ctx, token, E2EPassword, "Console"); !rejected(err) {
		t.Errorf("CompleteRegistration from the console: %v", err)
	}
	// An unregistered sibling is refused too.
	sibling := h.FreshBrowser()
	sibling.Origin = siblingOrigin
	if err := sibling.CompleteRegistration(ctx, token, E2EPassword, "Sibling"); !rejected(err) {
		t.Errorf("CompleteRegistration from an unregistered sibling: %v", err)
	}
	// The product's old landing page finishes the mail.
	product.Origin = metroOrigin
	if err := product.CompleteRegistration(ctx, token, E2EPassword, "Old Mail"); err != nil {
		t.Fatalf("CompleteRegistration from a transition origin: %v", err)
	}
	if _, err := h.FreshBrowser().LoginWithEmail(ctx, email, E2EPassword); err != nil {
		t.Fatalf("the account completed through the transition cannot sign in: %v", err)
	}
	reset := h.FreshBrowser()
	reset.Origin = ozidOrigin
	if _, err := reset.RequestPasswordReset(ctx, email, ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	product.Origin = tripOrigin
	if err := product.ResetPassword(ctx, h.Inbox.TokenIn(t, email), "A-new-pass-77!"); err != nil {
		t.Errorf("ResetPassword from a transition origin: %v", err)
	}

	// After the end the same origins are refused (the section is only left over).
	ended := accountOriginHarness(t, bffconfig.LegacyMailTransition{
		Origins: active.Origins,
		Until:   time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	})
	late := ended.FreshBrowser()
	late.Origin = tripOrigin
	if err := late.CompleteRegistration(ctx, "not-a-token", E2EPassword, "Late"); !rejected(err) {
		t.Errorf("CompleteRegistration after the transition ended: %v", err)
	}
	if err := late.ResetPassword(ctx, "not-a-token", "A-new-pass-77!"); !rejected(err) {
		t.Errorf("ResetPassword after the transition ended: %v", err)
	}
}

// bootstrapWith starts another BFF over the harness's persisted state with the configuration changed by
// mutate, and returns the start error. A BFF that did start is shut down at the end of the test.
func (h *ChainHarness) bootstrapWith(mutate func(*bffconfig.Config)) error {
	h.T.Helper()
	cfg := *h.bffCfg
	cfg.Upstreams = maps.Clone(h.bffCfg.Upstreams)
	cfg.GRPC.Services = maps.Clone(h.bffCfg.GRPC.Services)
	cfg.Auth.AccountCleanup = slices.Clone(h.bffCfg.Auth.AccountCleanup)
	cfg.CORSAllowedOrigins = slices.Clone(h.bffCfg.CORSAllowedOrigins)
	cfg.Navigation.Apps = maps.Clone(h.bffCfg.Navigation.Apps)
	mutate(&cfg)
	next := bffapp.New(&cfg)
	res := *h.bffRes
	next.SetResources(&res)
	if err := next.Bootstrap(); err != nil {
		return err
	}
	h.T.Cleanup(func() { _ = next.Shutdown() })
	return nil
}

// A BFF that serves the account procedures (usersrv) does not start without the navigation registry
// and the OzID origin, with plain and with secure cookies, and no setting makes it start anyway. The
// configuration that is complete starts in both modes.
func TestChainAccountsNeedTheRegistryToStart(t *testing.T) {
	h := accountOriginHarness(t, bffconfig.LegacyMailTransition{})
	secure := func(c *bffconfig.Config) {
		c.Auth.CookieSecure = true
		c.CORSAllowedOrigins = []string{"https://id.example.com", "https://trip.example.com"}
		c.Navigation = navigation.Config{
			APIOrigin: "https://api.example.com",
			Apps: map[string]navigation.App{
				"ozid":     {Origin: "https://id.example.com"},
				"trip-map": {Origin: "https://trip.example.com"},
			},
		}
	}
	for _, mode := range []struct {
		name  string
		apply func(*bffconfig.Config)
	}{
		{"plain cookies", func(*bffconfig.Config) {}},
		{"secure cookies", secure},
	} {
		t.Run(mode.name, func(t *testing.T) {
			if err := h.bootstrapWith(mode.apply); err != nil {
				t.Fatalf("a complete configuration did not start: %v", err)
			}
			for name, drop := range map[string]func(*bffconfig.Config){
				"no navigation":      func(c *bffconfig.Config) { c.Navigation = navigation.Config{} },
				"no api_origin":      func(c *bffconfig.Config) { c.Navigation.APIOrigin = "" },
				"no apps":            func(c *bffconfig.Config) { c.Navigation.Apps = nil },
				"no ozid app":        func(c *bffconfig.Config) { delete(c.Navigation.Apps, "ozid") },
				"ozid has no origin": func(c *bffconfig.Config) { c.Navigation.Apps["ozid"] = navigation.App{} },
			} {
				err := h.bootstrapWith(func(c *bffconfig.Config) { mode.apply(c); drop(c) })
				if err == nil {
					t.Errorf("%s: a BFF that serves accounts started without a registry", name)
				} else if !strings.Contains(err.Error(), "navigation") {
					t.Errorf("%s: the refusal does not name the missing setting: %v", name, err)
				}
			}
		})
	}
	// Secure cookies need https origins: plain http registry is a start failure as well.
	if err := h.bootstrapWith(func(c *bffconfig.Config) {
		secure(c)
		c.Navigation.Apps["ozid"] = navigation.App{Origin: "http://id.example.com"}
		c.CORSAllowedOrigins = append(c.CORSAllowedOrigins, "http://id.example.com")
	}); err == nil {
		t.Error("secure cookies with an http OzID origin started")
	}
}
