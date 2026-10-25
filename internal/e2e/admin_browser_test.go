package e2e_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kongken/mono/internal/bff/adminguard"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/navigation"
)

// adminCSP reads the Content-Security-Policy out of deploy/admin-web's shared header include, so this
// scenario and the image cannot drift apart.
func adminCSP(apiOrigin string) string {
	raw, err := os.ReadFile("../../deploy/admin-web/templates/inc/headers.inc.template")
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if _, rest, ok := strings.Cut(line, `add_header Content-Security-Policy "`); ok {
			policy, _, _ := strings.Cut(rest, `" always;`)
			return strings.ReplaceAll(policy, "${API_ORIGIN}", apiOrigin)
		}
	}
	panic("no Content-Security-Policy in the admin-web headers")
}

// TestAdminBrowserChain drives the unified management console in a real Chromium against the real BFF
// and usersrv over HTTPS, on sibling sub-domains for OzID, the API, the console and a foreign product.
// It builds both frontends for those addresses, so it takes a while, and needs node with the workspace
// dependencies and Playwright's Chromium: it only runs with ADMIN_BROWSER_E2E=1.
//
// The "gateway" in front of the console and the API is a stand-in for the real one (deploy/admin-web's
// Nginx and the operator's proxy): it applies the same source allowlist to every console response,
// and sends the client address to the BFF in X-Forwarded-For exactly as a trusted proxy does. The
// test changes the pretended client address through a control host, which is how the spec moves a
// browser "inside" and "outside" the allowlist. What this cannot prove (DNS, TLS, the real source
// addresses, the real proxy chain) is listed in docs/admin/features.md.
func TestAdminBrowserChain(t *testing.T) {
	if os.Getenv("ADMIN_BROWSER_E2E") != "1" {
		t.Skip("set ADMIN_BROWSER_E2E=1 (needs node, the workspace's npm dependencies and Playwright's Chromium)")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	origin := func(label string) string { return fmt.Sprintf("https://%s.%s:%d", label, ozidBrowserSite, port) }
	idO, apiO, adminO, tripO, controlO := origin("id"), origin("api"), origin("admin"), origin("trip"), origin("control")

	const inside, outside = "203.0.113.9", "198.51.100.7"
	perimeter := adminguard.Config{AllowedCIDRs: []string{"203.0.113.0/24"}, TrustedProxies: []string{"127.0.0.0/8"}}
	var source atomic.Value // the client address the gateway pretends to have seen
	source.Store(inside)

	h := NewChainHarness(t, ChainHTTPOptions{
		MailOzidOrigin: idO,
		Configure: func(cfg *bffconfig.Config, _ string) {
			cfg.Auth.CookieSecure = true
			// The foreign product is in the CORS list on purpose: its calls get an answer the page can read,
			// so what refuses them is the BFF's own Origin rule, not the browser.
			cfg.CORSAllowedOrigins = []string{idO, adminO, tripO}
			cfg.Navigation = navigation.Config{
				APIOrigin: apiO,
				Apps: map[string]navigation.App{
					"ozid":     {Origin: idO, Paths: []string{"/security"}},
					"admin":    {Origin: adminO, Paths: []string{"/users", "/users/*"}},
					"trip-map": {Origin: tripO},
				},
			}
			cfg.Admin = perimeter
		},
	})

	ctx := context.Background()
	type account struct {
		Kind        string `json:"kind"`
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
	}
	var accounts []account
	for _, u := range []struct {
		kind, subject string
		roles         []string
	}{
		{"operator", "console_operator", []string{"ozid-admin"}},
		{"plain", "console_plain", nil},
		{"metro", "console_metro", []string{"metro-admin"}},
		{"other", "console_member_a", nil},
		{"other", "console_member_b", nil},
	} {
		if err := h.FreshBrowser().LoginWithRoles(ctx, u.subject, u.roles...); err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, account{Kind: u.kind, Email: emailFor(u.subject), DisplayName: u.subject})
	}

	buildEnv := func(extra ...string) []string {
		return append([]string{"VITE_API_BASE_URL=" + apiO + "/api"}, extra...)
	}
	run(t, root, buildEnv(), "npm", "run", "build", "-w", "@kongken/ozid")
	run(t, root, buildEnv("VITE_OZID_ORIGIN="+idO), "npm", "run", "build", "-w", "@kongken/admin")
	idDist := filepath.Join(root, "apps", "ozid", "dist")
	adminDist := filepath.Join(root, "apps", "admin", "dist")

	bff, err := url.Parse(h.BFFURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(bff) // keeps the browser's Host and Origin
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		// A gateway states who it saw; whatever the client wrote is replaced. (The proxy then appends
		// its own address to the right, which the BFF skips as a trusted hop.)
		r.Header.Set("X-Forwarded-For", source.Load().(string))
	}
	gate, err := adminguard.New(adminguard.Config{AllowedCIDRs: perimeter.AllowedCIDRs}, adminO)
	if err != nil {
		t.Fatal(err)
	}
	allowed := func() bool { return gate.AllowedIP(http.Header{}, net.JoinHostPort(source.Load().(string), "1")) }
	serveDist := func(dist string, w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(filepath.Join(dist, filepath.Clean("/"+r.URL.Path))); err != nil {
			r.URL.Path = "/" // the single-page app's own routes
		}
		http.FileServer(http.Dir(dist)).ServeHTTP(w, r)
	}
	idStatic := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", ozidCSP(apiO))
		w.Header().Set("X-Frame-Options", "DENY")
		serveDist(idDist, w, r)
	})
	adminStatic := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every response of the console, a 404 included, is behind the allowlist, and says nothing.
		if !allowed() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", adminCSP(apiO))
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		serveDist(adminDist, w, r)
	})
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		label, _, _ := strings.Cut(r.Host, ".")
		switch label {
		case "api":
			proxy.ServeHTTP(w, r)
		case "id":
			idStatic.ServeHTTP(w, r)
		case "admin":
			adminStatic.ServeHTTP(w, r)
		case "control":
			// GET /source?ip=...: where the gateway pretends the client is. (Test scaffolding only.)
			if ip := r.URL.Query().Get("ip"); net.ParseIP(ip) != nil {
				source.Store(ip)
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = fmt.Fprintln(w, source.Load().(string))
		case "trip":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, standInPage, "trip-map", apiO, idO)
		default:
			http.NotFound(w, r)
		}
	})
	front := &http.Server{Handler: mux, TLSConfig: &tls.Config{Certificates: []tls.Certificate{throwawayCert(t)}}}
	go func() { _ = front.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = front.Close() })

	stack, _ := json.Marshal(map[string]any{
		"id": idO, "api": apiO, "admin": adminO, "trip": tripO, "control": controlO,
		"inside": inside, "outside": outside,
		"password": E2EPassword, "accounts": accounts,
		"csp": adminCSP(apiO),
	})
	run(t, root, []string{"ADMIN_REAL_STACK=" + string(stack)},
		"npx", "playwright", "test", "-c", "apps/admin/playwright.real.config.ts")
}
