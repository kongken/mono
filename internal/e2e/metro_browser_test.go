package e2e_test

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/navigation"
	"github.com/kongken/mono/internal/metro/ent/city"
	"github.com/kongken/mono/internal/metro/ent/riderline"
)

// TestMetroBrowserChain drives the metro product (apps/metro, built for these addresses) and OzID in a real
// Chromium against the real BFF, usersrv and metro over HTTPS, on sibling sub-domains of one reserved test
// site: the product has no account forms, sends the browser to OzID and gets it back, across origins, to
// the page it started on. It proves what the mocked-BFF specs cannot: the real cross-origin return, the
// host-only session Cookie, the real Me answer restoring the rider's own lines, the real CSRF double submit
// and the real X-Kongken-User guard when another tab replaces the token or switches the account.
//
// It builds both front ends, so it takes a while, and needs node with the workspace dependencies and
// Playwright's Chromium: it only runs with METRO_BROWSER_E2E=1. Every account is seeded for one scenario of
// one Playwright project (names below); nothing here is a real deployment name, address or credential.
func TestMetroBrowserChain(t *testing.T) {
	if os.Getenv("METRO_BROWSER_E2E") != "1" {
		t.Skip("set METRO_BROWSER_E2E=1 (needs node, the workspace's npm dependencies and Playwright's Chromium)")
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
	idO, apiO, metroO := origin("id"), origin("api"), origin("metro")

	stack0 := newCloseAccountStack(t, ChainHTTPOptions{
		MailOzidOrigin: idO,
		Configure: func(cfg *bffconfig.Config, _ string) {
			cfg.Auth.CookieSecure = true
			cfg.CORSAllowedOrigins = []string{idO, metroO}
			cfg.Navigation = navigation.Config{
				APIOrigin: apiO,
				Apps: map[string]navigation.App{
					"ozid": {Origin: idO, Paths: []string{"/security"}},
					// The start page, and the pages a sign-in may return to (the product asks the BFF about a page
					// before naming it; anything not listed is answered with the start page).
					"metro": {Origin: metroO, Paths: []string{"/me", "/cities/*", "/topics", "/topics/*", "/u/*"}},
				},
			}
		},
	})
	h := stack0.ChainHarness
	// A second line, so a scenario can mark one that the seeded account does not have.
	sh := h.MetroDB.City.Query().Where(city.Slug("shanghai")).OnlyX(t.Context())
	h.MetroDB.Line.Create().SetCode("sh-2").SetCityID(sh.ID).SetName("2号线").SetNumber(2).SetLengthKm(64).SetColor("#97D700").SaveX(t.Context())

	// One account per scenario and project (an account is only ever used by its own scenario). Each has the
	// seeded ride of the harness (sh-1) on the server, so a restored account is told apart from an empty one.
	type seeded struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
	}
	accounts := map[string]seeded{}
	for _, project := range []string{"desktop", "mobile"} {
		for _, kind := range []string{"index", "round", "mark", "me", "out", "net", "swapa", "swapb", "csrf", "csrfa", "csrfb", "fb"} {
			name := "mt-" + kind + "-" + project
			_, id, _ := stack0.account(name)
			accounts[name] = seeded{ID: id, Email: name + "@example.com", DisplayName: name}
		}
	}
	ids := map[string]bool{}
	for _, a := range accounts {
		ids[a.ID] = true
	}

	// The window into the server side: which lines are marked for a seeded account, to compare with what the
	// browser shows. It only answers for the accounts seeded above.
	controlMux := http.NewServeMux()
	controlMux.HandleFunc("/server", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		if !ids[user] {
			http.Error(w, "unknown test account", http.StatusNotFound)
			return
		}
		rides := []string{}
		for _, rl := range h.MetroDB.RiderLine.Query().Where(riderline.UserID(user)).WithLine().AllX(r.Context()) {
			rides = append(rides, rl.Edges.Line.Code)
		}
		sort.Strings(rides)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"rides": rides})
	})

	run(t, root, []string{"VITE_API_BASE_URL=" + apiO + "/api"}, "npm", "run", "build", "-w", "@kongken/ozid")
	run(t, root, []string{"VITE_API_BASE_URL=" + apiO + "/api", "VITE_OZID_ORIGIN=" + idO}, "npm", "run", "build", "-w", "@kongken/metro")
	ozidDist := filepath.Join(root, "apps", "ozid", "dist")
	metroDist := filepath.Join(root, "apps", "metro", "dist")

	bff, err := url.Parse(h.BFFURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(bff) // keeps the browser's Host and Origin
	ozidStatic := http.FileServer(http.Dir(ozidDist))
	metroStatic := http.FileServer(http.Dir(metroDist))
	spa := func(dist string, r *http.Request) {
		if _, err := os.Stat(filepath.Join(dist, filepath.Clean("/"+r.URL.Path))); err != nil {
			r.URL.Path = "/" // the single-page app's own routes
		}
	}
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		label, _, _ := strings.Cut(r.Host, ".")
		switch label {
		case "api":
			proxy.ServeHTTP(w, r)
		case "id":
			spa(ozidDist, r)
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Security-Policy", ozidCSP(apiO))
			w.Header().Set("X-Frame-Options", "DENY")
			ozidStatic.ServeHTTP(w, r)
		case "metro":
			spa(metroDist, r)
			w.Header().Set("Cache-Control", "no-store")
			metroStatic.ServeHTTP(w, r)
		case "control":
			controlMux.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	front := &http.Server{Handler: mux, TLSConfig: &tls.Config{Certificates: []tls.Certificate{throwawayCert(t)}}}
	go func() { _ = front.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = front.Close() })

	stackJSON, _ := json.Marshal(map[string]any{
		"id": idO, "api": apiO, "metro": metroO, "control": origin("control"),
		"password": E2EPassword, "accounts": accounts,
	})
	args := []string{"playwright", "test", "-c", "apps/metro/playwright.real.config.ts"}
	if only := os.Getenv("METRO_BROWSER_GREP"); only != "" { // while developing one scenario
		args = append(args, "-g", only)
	}
	run(t, root, []string{"METRO_REAL_STACK=" + string(stackJSON)}, "npx", args...)
}
