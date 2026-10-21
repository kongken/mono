package e2e_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/oauth/provider"
)

// Runs Chromium against real BFF -> TLS gRPC -> tripmap and real isolated
// PostgreSQL/Redis. Only the external OAuth issuer is controlled by this test.
func TestOAuthHTTPSCrossOriginBrowser(t *testing.T) {
	if os.Getenv("E2E_OAUTH_BROWSER") != "1" {
		t.Skip("set E2E_OAUTH_BROWSER=1 with Playwright Chromium and test stores")
	}
	app := func() *httptest.Server {
		return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<!doctype html><html lang="zh"><title>受控 Web App</title><body>旅行应用</body></html>`)
		}))
	}
	a, b := app(), app()
	defer a.Close()
	defer b.Close()
	localhost := func(raw string) string { u, _ := url.Parse(raw); return "https://localhost:" + u.Port() }
	originA, originB := strings.Replace(localhost(a.URL), "localhost", "a.example.test", 1), strings.Replace(localhost(b.URL), "localhost", "b.example.test", 1)
	var mu sync.Mutex
	codes := map[string]string{}
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oidc/auth":
			q := r.URL.Query()
			if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
				http.Error(w, "PKCE required", 400)
				return
			}
			code := q.Get("state")
			mu.Lock()
			codes[code] = q.Get("code_challenge")
			mu.Unlock()
			target, _ := url.Parse(q.Get("redirect_uri"))
			v := target.Query()
			v.Set("code", code)
			v.Set("state", code)
			target.RawQuery = v.Encode()
			http.Redirect(w, r, target.String(), 302)
		case "/oidc/token":
			r.ParseForm()
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			mu.Lock()
			expected, ok := codes[r.Form.Get("code")]
			delete(codes, r.Form.Get("code"))
			mu.Unlock()
			if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != expected {
				http.Error(w, "bad verifier", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "controlled-access", "token_type": "Bearer"})
		case "/oidc/me":
			if r.Header.Get("Authorization") != "Bearer controlled-access" {
				http.Error(w, "unauthorized", 401)
				return
			}
			fmt.Fprint(w, `{"sub":"browser-user","name":"Browser User"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	reg := provider.NewRegistry()
	preflights, successes, failures := 0, 0, 0
	h := NewChainHarness(t, reg, ChainHTTPOptions{TLS: true, Observe: func(r *http.Request, h http.Header, status int) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if origin == originA || origin == originB {
			if h.Get("Access-Control-Allow-Origin") != origin || h.Get("Access-Control-Allow-Credentials") != "true" || !strings.Contains(h.Get("Vary"), "Origin") {
				t.Errorf("credentialed CORS: %d %v", status, h)
			}
			if r.Method == "OPTIONS" {
				preflights++
				if status != 204 {
					t.Errorf("preflight: %d", status)
				}
			} else if status >= 400 {
				failures++
			} else {
				successes++
			}
		} else if h.Get("Access-Control-Allow-Origin") != "" || h.Get("Access-Control-Allow-Credentials") != "" {
			t.Error("unlisted origin received CORS grant")
		}
	}, Configure: func(cfg *bffconfig.Config, base string) {
		base = strings.Replace(base, "localhost", "api.example.test", 1)
		cfg.Auth.CookieSecure = true
		cfg.Auth.OAuthDevelopment = true // controlled loopback HTTP issuer only
		cfg.CORSAllowedOrigins = []string{originA, originB}
		cfg.Auth.OAuthApps = map[string]bffconfig.OAuthApp{
			"a": {Origin: originA, DefaultPath: "/", AllowedPaths: []string{"/"}},
			"b": {Origin: originB, DefaultPath: "/map", AllowedPaths: []string{"/map"}},
		}
		cfg.Auth.OAuthProviders = map[string]bffconfig.OAuthProvider{"logto": {IssuerURL: issuer.URL + "/oidc", ClientID: "test", ClientSecret: "test", RedirectURL: base + "/auth/callback/logto"}}
		reg.Register(provider.NewLogto(provider.LogtoConfig{IssuerURL: issuer.URL + "/oidc", ClientID: "test", ClientSecret: "test", RedirectURL: base + "/auth/callback/logto"}))
	}})
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(root, "apps/trip-map/scripts/oauth-browser.mjs"), strings.Replace(h.BFFURL, "localhost", "api.example.test", 1), originA, originB)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser: %v\n%s", err, output)
	}
	mu.Lock()
	defer mu.Unlock()
	if preflights == 0 || successes == 0 || failures == 0 {
		t.Fatalf("missing real browser CORS traffic: OPTIONS=%d successes=%d errors=%d", preflights, successes, failures)
	}
	t.Logf("Browser OPTIONS=%d successes=%d errors=%d", preflights, successes, failures)
	t.Log(string(output))
}
