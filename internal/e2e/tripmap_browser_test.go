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
	"github.com/kongken/mono/internal/tripmap/ent/cityvisit"
	"github.com/kongken/mono/internal/tripmap/ent/footprintchange"
	"github.com/kongken/mono/internal/tripmap/ent/syncreceipt"
)

// TestTripMapBrowserChain drives the trip-map product (apps/trip-map, built for these addresses) and
// OzID in a real Chromium against the real BFF, usersrv and tripmap over HTTPS, on sibling
// sub-domains of one reserved test site: the product has no account forms, sends the browser to OzID
// and gets it back across origins. It proves what the mocked-BFF specs cannot: the real cross-origin
// return, the host-only session Cookie, the real Me answer's user UUID restoring the original cache,
// and an interrupted batch replayed under its original mutation ID without applying it twice.
//
// It builds both front ends, so it takes a while, and needs node with the workspace dependencies and
// Playwright's Chromium: it only runs with TRIPMAP_BROWSER_E2E=1. Every account is seeded for
// one scenario of one Playwright project (names below); nothing here is a real deployment name,
// address or credential.
func TestTripMapBrowserChain(t *testing.T) {
	if os.Getenv("TRIPMAP_BROWSER_E2E") != "1" {
		t.Skip("set TRIPMAP_BROWSER_E2E=1 (needs node, the workspace's npm dependencies and Playwright's Chromium)")
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
	idO, apiO, tripO := origin("id"), origin("api"), origin("trip")

	stack0 := newCloseAccountStack(t, ChainHTTPOptions{
		MailOzidOrigin: idO,
		Configure: func(cfg *bffconfig.Config, _ string) {
			cfg.Auth.CookieSecure = true
			cfg.CORSAllowedOrigins = []string{idO, tripO}
			cfg.Navigation = navigation.Config{
				APIOrigin: apiO,
				Apps: map[string]navigation.App{
					"ozid":     {Origin: idO, Paths: []string{"/security"}},
					"trip-map": {Origin: tripO},
				},
			}
		},
	})
	h := stack0.ChainHarness

	// One account per scenario and project (an account is only ever used by its own scenario). Each
	// has the seeded visit of the harness (Hangzhou) on the server, so a restored account is told apart
	// from an empty one.
	type seeded struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	accounts := map[string]seeded{}
	for _, project := range []string{"desktop", "mobile"} {
		for _, kind := range []string{"round", "lost", "a", "b", "out", "net"} {
			name := "tm-" + kind + "-" + project
			_, id, _ := stack0.account(name)
			accounts[name] = seeded{ID: id, Email: name + "@example.com"}
		}
	}
	ids := map[string]bool{}
	for _, a := range accounts {
		ids[a.ID] = true
	}

	// The window into the server side: what is stored for a seeded account, to compare with what
	// the browser shows. It only answers for the accounts seeded above.
	holds := newResponseHold()
	controlMux := http.NewServeMux()
	holds.mount(controlMux)
	controlMux.HandleFunc("/server", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		if !ids[user] {
			http.Error(w, "unknown test account", http.StatusNotFound)
			return
		}
		ctx := r.Context()
		visits := []string{}
		for _, v := range h.TripmapDB.CityVisit.Query().Where(cityvisit.UserID(user)).AllX(ctx) {
			visits = append(visits, v.RegionCode)
		}
		sort.Strings(visits)
		receipts := []string{}
		for _, rc := range h.TripmapDB.SyncReceipt.Query().Where(syncreceipt.UserID(user)).AllX(ctx) {
			receipts = append(receipts, rc.MutationID)
		}
		sort.Strings(receipts)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"visits":   visits,
			"receipts": receipts,
			"changes":  h.TripmapDB.FootprintChange.Query().Where(footprintchange.UserID(user)).CountX(ctx),
		})
	})

	run(t, root, []string{"VITE_API_BASE_URL=" + apiO + "/api"}, "npm", "run", "build", "-w", "@kongken/ozid")
	run(t, root, []string{"VITE_API_BASE_URL=" + apiO + "/api", "VITE_OZID_ORIGIN=" + idO}, "npm", "run", "build", "-w", "@kongken/trip-map")
	ozidDist := filepath.Join(root, "apps", "ozid", "dist")
	tripDist := filepath.Join(root, "apps", "trip-map", "dist")

	bff, err := url.Parse(h.BFFURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(bff) // keeps the browser's Host and Origin
	ozidStatic := http.FileServer(http.Dir(ozidDist))
	tripStatic := http.FileServer(http.Dir(tripDist))
	spa := func(dist string, r *http.Request) {
		if _, err := os.Stat(filepath.Join(dist, filepath.Clean("/"+r.URL.Path))); err != nil {
			r.URL.Path = "/" // the single-page app's own routes
		}
	}
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		label, _, _ := strings.Cut(r.Host, ".")
		switch label {
		case "api":
			holds.serve(proxy, w, r) // a call the scenario armed is held or its answer dropped; others pass
		case "id":
			spa(ozidDist, r)
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Security-Policy", ozidCSP(apiO))
			w.Header().Set("X-Frame-Options", "DENY")
			ozidStatic.ServeHTTP(w, r)
		case "trip":
			spa(tripDist, r)
			w.Header().Set("Cache-Control", "no-store")
			tripStatic.ServeHTTP(w, r)
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
		"id": idO, "api": apiO, "trip": tripO, "control": origin("control"),
		"password": E2EPassword, "accounts": accounts,
	})
	args := []string{"playwright", "test", "-c", "apps/trip-map/playwright.real.config.ts"}
	if only := os.Getenv("TRIPMAP_BROWSER_GREP"); only != "" { // while developing one scenario
		args = append(args, "-g", only)
	}
	run(t, root, []string{"TRIPMAP_REAL_STACK=" + string(stackJSON)}, "npx", args...)
}
