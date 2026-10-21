package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	"github.com/kongken/mono/internal/bff/session"
)

func TestOAuthHTTPBrowserBindingAndConcurrentApps(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider{name: "fake", claims: &provider.Claims{ExternalID: "http-42", Name: "HTTP User"}})
	cfg := &config.AuthConfig{CookieSecure: true, OAuthDevelopment: true, OAuthProviders: map[string]config.OAuthProvider{"fake": {RedirectURL: "http://localhost/auth/callback/fake"}}, OAuthApps: map[string]config.OAuthApp{
		"a": {Origin: "https://a.example.com", DefaultPath: "/", AllowedPaths: []string{"/"}},
		"b": {Origin: "https://b.example.com", DefaultPath: "/map", AllowedPaths: []string{"/map"}},
	}}
	_, h := oauthSvc(t, reg, cfg)
	call := func(path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeOAuthHTTP(w, r, "")
		return w
	}
	for _, q := range []string{"app=unknown", "app=a&return_to=//evil.example", "app=a&return_to=%2F%255cevil", "app=a&return_to=/other", "app=a&return_to=https%3A%2F%2Fevil.example", "app=a&return_to=%2F%2Fevil.example", "app=a&return_to=%2F..%2Fother", "app=a&return_to=%2F%5Cevil.example", "app=a&callback=https%3A%2F%2Fevil.example"} {
		if w := call("/auth/login?provider=fake&"+q, nil); w.Code != 400 {
			t.Fatalf("bad target accepted: %s: %d", q, w.Code)
		}
	}
	a := call("/auth/login?provider=fake&app=a", nil)
	b := call("/auth/login?provider=fake&app=b", nil)
	state := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		if w.Code != 302 {
			t.Fatalf("begin: %d %s", w.Code, w.Body)
		}
		u, _ := url.Parse(w.Header().Get("Location"))
		return u.Query().Get("state")
	}
	sa, sb := state(a), state(b)
	if a.Result().Cookies()[0].Name == b.Result().Cookies()[0].Name {
		t.Fatal("concurrent flows share binding cookie")
	}
	callback := func(s string) string { return "/auth/callback/fake?code=good-code&state=" + s }
	if w := call(callback(sa), nil); w.Code != 400 || len(w.Result().Cookies()) > 1 {
		t.Fatal("missing browser binding accepted")
	}
	// The rejected state is consumed. A new attempt still works independently of b.
	a = call("/auth/login?provider=fake&app=a", nil)
	sa = state(a)
	for _, flow := range []struct {
		s      string
		begin  *httptest.ResponseRecorder
		target string
	}{{sb, b, "https://b.example.com/map"}, {sa, a, "https://a.example.com/"}} {
		w := call(callback(flow.s), flow.begin.Result().Cookies())
		if w.Code != 303 || w.Header().Get("Location") != flow.target {
			t.Fatalf("callback: %d %s %s", w.Code, w.Header(), w.Body)
		}
		found := false
		for _, c := range w.Result().Cookies() {
			if c.Name == session.CookieName && c.Value != "" && c.HttpOnly && c.Domain == "" {
				found = true
			}
		}
		if !found {
			t.Fatal("missing host-only HttpOnly session")
		}
		if w := call(callback(flow.s), flow.begin.Result().Cookies()); w.Code != 400 {
			t.Fatal("replay accepted")
		}
	}
}

func TestOAuthHTTPFailuresNeverCreateSession(t *testing.T) {
	for _, scenario := range []string{"binding mismatch", "provider mismatch", "cancel", "missing code", "bad code", "forged state", "expired state", "closed registration"} {
		t.Run(scenario, func(t *testing.T) {
			reg := provider.NewRegistry()
			reg.Register(&fakeProvider{name: "fake", claims: &provider.Claims{ExternalID: "failure-user"}})
			cfg := &config.AuthConfig{CookieSecure: true, OAuthApps: map[string]config.OAuthApp{"a": {Origin: "https://a.example.com", DefaultPath: "/", AllowedPaths: []string{"/"}}}, OAuthProviders: map[string]config.OAuthProvider{"fake": {RedirectURL: "https://api.example.com/auth/callback/fake"}}}
			if scenario == "closed registration" {
				cfg.RegistrationDisabled = true
			}
			_, h, rdb, prefix := oauthSvcBacking(t, reg, cfg)
			call := func(path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", path, nil)
				for _, c := range cookies {
					r.AddCookie(c)
				}
				w := httptest.NewRecorder()
				h.ServeOAuthHTTP(w, r, "")
				return w
			}
			begin := call("/auth/login?app=a&provider=fake", nil)
			u, _ := url.Parse(begin.Header().Get("Location"))
			state := u.Query().Get("state")
			cookies := begin.Result().Cookies()
			query := "?code=good-code&state=" + state
			path := "/auth/callback/fake"
			switch scenario {
			case "binding mismatch":
				cookies[0].Value = "another-browser"
			case "provider mismatch":
				path = "/auth/callback/other"
			case "cancel":
				query = "?error=access_denied&state=" + state
			case "missing code":
				query = "?state=" + state
			case "bad code":
				query = "?code=bad-code&state=" + state
			case "forged state":
				query = "?code=good-code&state=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			case "expired state":
				if ttl, err := rdb.TTL(context.Background(), prefix+"oauth:"+state).Result(); err != nil || ttl <= 0 || ttl > 10*time.Minute {
					t.Fatalf("state TTL: %v %v", ttl, err)
				}
				if err := rdb.PExpire(context.Background(), prefix+"oauth:"+state, -time.Second).Err(); err != nil {
					t.Fatal(err)
				}
			}
			w := call(path+query, cookies)
			if w.Code < 400 {
				t.Fatalf("accepted failure: %d", w.Code)
			}
			for _, c := range w.Result().Cookies() {
				if c.Name == session.CookieName && c.Value != "" {
					t.Fatal("failed callback created session")
				}
			}
			if w.Header().Get("Location") != "" {
				t.Fatal("error redirected to untrusted destination")
			}
		})
	}
}
