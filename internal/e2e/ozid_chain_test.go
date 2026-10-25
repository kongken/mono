package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	bffsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/navigation"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// The OzID origin and the product origins of the registry in these scenarios. All of
// them are one site (localhost) as in a real deployment; the BFF is a sixth origin.
const (
	ozidOrigin  = "http://localhost:5175"
	tripOrigin  = "http://localhost:5173"
	metroOrigin = "http://localhost:5174"
	// A same-site sibling that is not registered.
	siblingOrigin = "http://sibling.localhost:5175"
)

// cookieLog records the Set-Cookie headers the BFF sent, by procedure.
type cookieLog struct {
	mu   sync.Mutex
	sets map[string][]string
}

func (c *cookieLog) observe(r *http.Request, h http.Header, _ int) {
	if v := h.Values("Set-Cookie"); len(v) > 0 {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.sets == nil {
			c.sets = map[string][]string{}
		}
		c.sets[r.URL.Path] = append(c.sets[r.URL.Path], v...)
	}
}

func (c *cookieLog) for_(procedure string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sets[procedure]...)
}

func ozidHarness(t *testing.T) (*ChainHarness, *cookieLog) {
	t.Helper()
	log := &cookieLog{}
	h := NewChainHarness(t, ChainHTTPOptions{
		Observe: log.observe,
		Configure: func(cfg *bffconfig.Config, baseURL string) {
			cfg.CORSAllowedOrigins = []string{ozidOrigin, tripOrigin, metroOrigin}
			cfg.Navigation = navigation.Config{
				APIOrigin: baseURL,
				Apps: map[string]navigation.App{
					"ozid":     {Origin: ozidOrigin, Paths: []string{"/security"}},
					"trip-map": {Origin: tripOrigin},
					"metro":    {Origin: metroOrigin, Paths: []string{"/me", "/city/*"}},
				},
			}
		},
	})
	return h, log
}

func (b *Browser) resolve(ctx context.Context, app, path string) (*bffv1.ResolveNavigationResponse, error) {
	req := connect.NewRequest(&bffv1.ResolveNavigationRequest{AppId: app, ReturnPath: path})
	req.Header().Set("Origin", b.Origin)
	res, err := b.Auth.ResolveNavigation(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// A product sends the browser to OzID with an app ID and a path; OzID resolves it
// anonymously, signs in at the BFF with its own Origin, and only the validated URL is
// ever returned. Another registered app sees the same identity through the one Cookie.
func TestChainOzidSignInAndSafeReturn(t *testing.T) {
	h, cookies := ozidHarness(t)
	ctx := context.Background()
	reg := h.FreshBrowser()
	if _, err := reg.Register(ctx, "ozid-user", "", "Oz"); err != nil {
		t.Fatal(err)
	}

	b := h.FreshBrowser()
	b.Origin = ozidOrigin
	// Resolving needs no session and no CSRF token, and sets no cookie.
	got, err := b.resolve(ctx, "metro", "/city/shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if got.GetReturnUrl() != metroOrigin+"/city/shanghai" || got.GetOzidOrigin() != ozidOrigin {
		t.Fatalf("resolved %v", got)
	}
	if b.SessionCookie() != "" || len(cookies.for_("/api/bff.v1.AuthService/ResolveNavigation")) != 0 {
		t.Fatal("resolving a target established something")
	}
	for _, bad := range []struct{ app, path string }{
		{"other", "/"}, {"metro", "https://evil.example/"}, {"metro", "//evil.example"},
		{"metro", "/city/%2e%2e/me"}, {"metro", "/me?x=1"}, {"metro", "/me#x"}, {"metro", "/city/../admin"},
		{"trip-map", "/secret"}, {"http://localhost:5174", "/me"},
	} {
		if _, err := b.resolve(ctx, bad.app, bad.path); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%q %q: %v", bad.app, bad.path, err)
		}
	}

	// Sign in as OzID does: its own registered Origin, the BFF's CSRF token.
	if _, err := b.LoginWithEmail(ctx, "ozid-user@example.com", E2EPassword); err != nil {
		t.Fatal(err)
	}
	// Registering signed in once already; this is the latest login's cookie.
	set := cookies.for_("/api/bff.v1.AuthService/LoginWithEmail")
	set = set[len(set)-1:]
	for _, want := range []string{session.CookieName + "=", "HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(set[0], want) {
			t.Errorf("session cookie lacks %q: %s", want, set[0])
		}
	}
	if strings.Contains(strings.ToLower(set[0]), "domain=") {
		t.Errorf("the session cookie names a Domain: %s", set[0])
	}
	// Another registered app, its own Origin, same Cookie: the same identity.
	product := h.FreshBrowser()
	product.HTTP, product.Origin = b.HTTP, tripOrigin
	product.Auth = bffv1connect.NewAuthServiceClient(b.HTTP, product.APIURL)
	meOz, err := b.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	meTrip, err := product.Me(ctx)
	if err != nil || meTrip.GetId() != meOz.GetId() {
		t.Fatalf("product sees %v, %v; OzID sees %v", meTrip, err, meOz)
	}
}

// Only the registered origins may call: a same-site sibling holds the Cookie in a real
// browser but is refused by CORS and by the Origin check on writes.
func TestChainOzidRefusesUnregisteredSameSiteOrigins(t *testing.T) {
	h, _ := ozidHarness(t)
	ctx := context.Background()
	reg := h.FreshBrowser()
	if _, err := reg.Register(ctx, "sibling-user", "", "S"); err != nil {
		t.Fatal(err)
	}

	for _, origin := range []string{siblingOrigin, "http://localhost:5176", "https://localhost:5175", "null", "http://localhost.evil.test:5175"} {
		pre, err := http.NewRequest(http.MethodOptions, h.BFFURL+"/api/bff.v1.AuthService/LoginWithEmail", nil)
		if err != nil {
			t.Fatal(err)
		}
		pre.Header.Set("Origin", origin)
		pre.Header.Set("Access-Control-Request-Method", "POST")
		res, err := http.DefaultClient.Do(pre)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.Header.Get("Access-Control-Allow-Origin") != "" || res.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("preflight from %q was allowed: %v", origin, res.Header)
		}

		b := h.FreshBrowser()
		b.Origin = origin
		if _, err := b.LoginWithEmail(ctx, "sibling-user@example.com", E2EPassword); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("login from %q: %v", origin, err)
		}
		if b.SessionCookie() != "" {
			t.Errorf("login from %q left a session", origin)
		}
	}
	// The registered origin, for contrast.
	pre, _ := http.NewRequest(http.MethodOptions, h.BFFURL+"/api/bff.v1.AuthService/LoginWithEmail", nil)
	pre.Header.Set("Origin", ozidOrigin)
	res, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.Header.Get("Access-Control-Allow-Origin") != ozidOrigin || res.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("the registered origin was refused: %v", res.Header)
	}
}

// Spec §6.2/§9.4: confirming the password renews the real authentication time of this
// session and nothing else; an identity that is no longer valid cannot be upgraded.
func TestChainOzidReauthenticationDoesNotUpgradeAnInvalidIdentity(t *testing.T) {
	h, _ := ozidHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	b.Origin = ozidOrigin
	id, err := b.Register(ctx, "reauth-ozid", "", "R")
	if err != nil {
		t.Fatal(err)
	}
	row := func(br *Browser) (int64, time.Time) {
		t.Helper()
		s := h.BFFDB.Session.Query().Where(bffsession.TokenHash(session.HashToken(br.SessionCookie()))).OnlyX(ctx)
		if s.AuthenticatedAt == nil {
			t.Fatal("no authentication time")
		}
		return s.AuthVersion, *s.AuthenticatedAt
	}

	// An old password check is renewed by the real password, and by nothing else.
	h.ageSession(b, 2*time.Hour)
	versionBefore, at := row(b)
	if err := b.Reauthenticate(ctx, "not the password!!"); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong password: %v", err)
	}
	if _, now := row(b); !now.Equal(at) {
		t.Fatal("a failed confirmation moved the authentication time")
	}
	if err := b.Reauthenticate(ctx, E2EPassword); err != nil {
		t.Fatal(err)
	}
	version, now := row(b)
	if !now.After(at) || time.Since(now) > time.Minute || version != versionBefore {
		t.Fatalf("reauthentication: version %d→%d, time %v→%v", versionBefore, version, at, now)
	}

	// The account changes under the session: a password change elsewhere. The session
	// is no longer valid; the (even correct, even new) password does not revive it.
	other := h.FreshBrowser()
	if err := other.Login(ctx, "reauth-ozid", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := other.ChangePassword(ctx, E2EPassword, "a brand new password"); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{E2EPassword, "a brand new password"} {
		if err := b.Reauthenticate(ctx, password); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("reauthenticating a stale session with %q: %v", password, err)
		}
	}
	if _, err := b.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a stale session still reads Me: %v", err)
	}

	// A role change (auth_version) invalidates a live session the same way.
	c := h.FreshBrowser()
	c.Origin = ozidOrigin
	if err := c.Login(ctx, "reauth-ozid", "", ""); err == nil {
		t.Fatal("the old password logged in")
	}
	if _, err := c.LoginWithEmail(ctx, "reauth-ozid@example.com", "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Accounts.GrantRole(ctx, operator, userUUID(t, id), "metro-admin"); err != nil {
		t.Fatal(err)
	}
	if err := c.Reauthenticate(ctx, "a brand new password"); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a session of an older auth_version was upgraded: %v", err)
	}
}
