package app

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/navigation"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Every browser service is exactly one of: a management service (admin perimeter), an account
// service (the user center's origin), or a product service (every registered application). A new
// service that is none of them fails here instead of silently being open to every origin.
func TestEveryBrowserServiceHasASourceRule(t *testing.T) {
	product := func(name string) bool {
		return strings.HasPrefix(name, "bff.tripmap.") || strings.HasPrefix(name, "bff.metro.")
	}
	isAdmin := map[string]bool{}
	for _, s := range adminServices {
		isAdmin[s] = true
	}
	isAccount := map[string]bool{}
	for _, s := range accountServices {
		isAccount[s] = true
	}
	seen := 0
	protoregistry.GlobalFiles.RangeFiles(func(f protoreflect.FileDescriptor) bool {
		for i := 0; i < f.Services().Len(); i++ {
			name := string(f.Services().Get(i).FullName())
			if !strings.HasPrefix(name, "bff.") {
				continue
			}
			seen++
			n := 0
			for _, in := range []bool{isAdmin[name], isAccount[name], product(name) && !isAdmin[name]} {
				if in {
					n++
				}
			}
			if n != 1 {
				t.Errorf("%s has %d source rules, want exactly one (admin perimeter, account origin, or product)", name, n)
			}
		}
		return true
	})
	if seen == 0 {
		t.Fatal("no browser service in the registry")
	}
}

// The open account procedures are exactly the four a product needs, and each is a procedure that
// exists; the legacy mail pair exists and is not open.
func TestAccountOpenProceduresAreTheMinimalFour(t *testing.T) {
	want := []string{
		bffv1connect.AuthServiceMeProcedure, bffv1connect.AuthServiceIssueCSRFTokenProcedure,
		bffv1connect.AuthServiceResolveNavigationProcedure, bffv1connect.AuthServiceLogoutProcedure,
	}
	if len(accountOpenProcedures) != len(want) {
		t.Errorf("open account procedures: %v", accountOpenProcedures)
	}
	for _, p := range want {
		if !accountOpenProcedures[p] || !isAccountProcedure(p) {
			t.Errorf("%s must be an open account procedure", p)
		}
	}
	for p := range legacyMailProcedures {
		if accountOpenProcedures[p] || !isAccountProcedure(p) {
			t.Errorf("%s: a legacy mail procedure is an account procedure and not an open one", p)
		}
	}
}

func policyFor(t *testing.T, tr config.LegacyMailTransition, now time.Time) *accountOriginPolicy {
	t.Helper()
	cfg := &config.Config{}
	cfg.Navigation = navigation.Config{
		APIOrigin: "https://api.example.com",
		Apps: map[string]navigation.App{
			"ozid":     {Origin: "https://id.example.com"},
			"admin":    {Origin: "https://admin.example.com"},
			"trip-map": {Origin: "https://trip.example.com"},
			"metro":    {Origin: "https://metro.example.com"},
		},
	}
	cfg.Auth.LegacyMailTransition = tr
	p, err := newAccountOriginPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return now }
	return p
}

func originHeader(values ...string) http.Header {
	h := http.Header{}
	for _, v := range values {
		h.Add("Origin", v)
	}
	return h
}

func TestAccountOriginPolicy(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	tr := config.LegacyMailTransition{
		Origins: []string{"https://trip.example.com", "https://metro.example.com"},
		Until:   now.Add(time.Hour).Format(time.RFC3339),
	}
	const (
		login    = bffv1connect.AuthServiceLoginWithEmailProcedure
		change   = bffv1connect.AuthServiceChangePasswordProcedure
		profile  = bffv1connect.UserServiceUpdateMyProfileProcedure
		complete = bffv1connect.AuthServiceCompleteRegistrationProcedure
		reset    = bffv1connect.AuthServiceResetPasswordProcedure
		me       = bffv1connect.AuthServiceMeProcedure
		logout   = bffv1connect.AuthServiceLogoutProcedure
		csrf     = bffv1connect.AuthServiceIssueCSRFTokenProcedure
		resolve  = bffv1connect.AuthServiceResolveNavigationProcedure
		users    = bffv1connect.UserAdminServiceListUsersProcedure
		other    = "/bff.tripmap.v1.FootprintService/GetFootprints"
	)
	for _, tc := range []struct {
		name      string
		policy    *accountOriginPolicy
		procedure string
		header    http.Header
		ok        bool
	}{
		{"ozid login", policyFor(t, config.LegacyMailTransition{}, now), login, originHeader("https://id.example.com"), true},
		{"ozid change password", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader("https://id.example.com"), true},
		{"ozid profile (UserService)", policyFor(t, config.LegacyMailTransition{}, now), profile, originHeader("https://id.example.com"), true},
		{"product login", policyFor(t, config.LegacyMailTransition{}, now), login, originHeader("https://trip.example.com"), false},
		{"product change password", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader("https://metro.example.com"), false},
		{"product profile", policyFor(t, config.LegacyMailTransition{}, now), profile, originHeader("https://metro.example.com"), false},
		{"console change password", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader("https://admin.example.com"), false},
		{"sibling", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader("https://evil.example.com"), false},
		{"scheme differs", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader("http://id.example.com"), false},
		{"port differs", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader("https://id.example.com:8443"), false},
		{"suffix of the origin", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader("https://id.example.com.evil.test"), false},
		{"case differs", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader("https://ID.example.com"), false},
		{"null", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader("null"), false},
		{"no origin", policyFor(t, config.LegacyMailTransition{}, now), change, http.Header{}, false},
		{"same-origin fetch metadata is not an origin", policyFor(t, config.LegacyMailTransition{}, now), change, http.Header{"Sec-Fetch-Site": {"same-origin"}}, false},
		{"two origins", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader("https://id.example.com", "https://trip.example.com"), false},
		{"empty origin value", policyFor(t, config.LegacyMailTransition{}, now), change, originHeader(""), false},

		{"product Me", policyFor(t, config.LegacyMailTransition{}, now), me, originHeader("https://trip.example.com"), true},
		{"product CSRF token", policyFor(t, config.LegacyMailTransition{}, now), csrf, originHeader("https://metro.example.com"), true},
		{"product sign-out", policyFor(t, config.LegacyMailTransition{}, now), logout, originHeader("https://metro.example.com"), true},
		{"product navigation", policyFor(t, config.LegacyMailTransition{}, now), resolve, originHeader("https://trip.example.com"), true},
		{"console Me", policyFor(t, config.LegacyMailTransition{}, now), me, originHeader("https://admin.example.com"), true},

		{"product business call is not this rule", policyFor(t, config.LegacyMailTransition{}, now), other, originHeader("https://trip.example.com"), true},
		{"management procedure is the admin perimeter's", policyFor(t, config.LegacyMailTransition{}, now), users, originHeader("https://trip.example.com"), true},

		{"transition: product completes registration", policyFor(t, tr, now), complete, originHeader("https://trip.example.com"), true},
		{"transition: product resets password", policyFor(t, tr, now), reset, originHeader("https://metro.example.com"), true},
		{"transition: product still cannot sign in", policyFor(t, tr, now), login, originHeader("https://trip.example.com"), false},
		{"transition: product still cannot change password", policyFor(t, tr, now), change, originHeader("https://trip.example.com"), false},
		{"transition: console is not a transition origin", policyFor(t, tr, now), complete, originHeader("https://admin.example.com"), false},
		{"transition: sibling is not a transition origin", policyFor(t, tr, now), reset, originHeader("https://evil.example.com"), false},
		{"transition: ozid still completes", policyFor(t, tr, now), complete, originHeader("https://id.example.com"), true},
		{"transition ends exactly at until", policyFor(t, tr, now.Add(time.Hour)), complete, originHeader("https://trip.example.com"), false},
		{"transition over: product reset refused", policyFor(t, tr, now.Add(2*time.Hour)), reset, originHeader("https://metro.example.com"), false},
		{"transition over: ozid unaffected", policyFor(t, tr, now.Add(2*time.Hour)), reset, originHeader("https://id.example.com"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Check(tc.procedure, tc.header)
			if (err == nil) != tc.ok {
				t.Fatalf("Check(%s, %v) = %v, want ok=%v", tc.procedure, tc.header, err, tc.ok)
			}
		})
	}
}

// Without a registered OzID there is no origin to name: nothing is restricted (and the BFF logs it).
func TestAccountOriginPolicyIsOffWithoutARegistry(t *testing.T) {
	p, err := newAccountOriginPolicy(&config.Config{})
	if err != nil || p != nil {
		t.Fatalf("policy = %v, %v", p, err)
	}
	if err := p.Check(bffv1connect.AuthServiceChangePasswordProcedure, http.Header{}); err != nil {
		t.Errorf("a nil policy refused: %v", err)
	}
}

func TestLegacyExpiredIsReported(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	tr := config.LegacyMailTransition{Origins: []string{"https://trip.example.com"}, Until: now.Add(-time.Minute).Format(time.RFC3339)}
	if p := policyFor(t, tr, now); !p.legacyExpired() || p.legacyActive() {
		t.Error("a past until must report expired and inactive")
	}
	if p := policyFor(t, config.LegacyMailTransition{}, now); p.legacyExpired() || p.legacyActive() {
		t.Error("no transition is neither expired nor active")
	}
}
