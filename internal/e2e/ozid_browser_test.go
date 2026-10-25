package e2e_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/navigation"
)

// The reserved test names of the browser scenario: sub-domains of one site that resolve to
// the local front (the browser is told so with --host-resolver-rules, see
// apps/ozid/playwright.real.config.ts). Nothing here is a real deployment name.
const ozidBrowserSite = "ozid-e2e.test"

// standInPage is a minimal "product": it only asks the BFF who is signed in, with
// credentials, the way a product's own frontend does, and shows the answer.
const standInPage = `<!doctype html><meta charset="utf-8"><title>%[1]s</title>
<p id="where"></p><p id="me">checking</p><p id="login-attempt">-</p>
<a id="login" href="%[3]s/login?app_id=%[1]s&return_path=%%2Fcity%%2Fshanghai">Sign in</a>
<script>
const api = %[2]q;
const call = (method, init) => fetch(api + "/api/bff.v1.AuthService/" + method, Object.assign({
  method: "POST", credentials: "include", body: "{}",
  headers: {"Content-Type": "application/json", "Connect-Protocol-Version": "1"}}, init));
document.getElementById("where").textContent = location.pathname;
(async () => {
  const me = document.getElementById("me");
  try {
    const r = await call("Me");
    const j = await r.json();
    me.textContent = r.ok ? "user:" + j.user.displayName : "anonymous:" + r.status;
    me.dataset.authenticatedAt = j.authenticatedAt || "";
  } catch (e) { me.textContent = "blocked"; }
  if (!location.hostname.startsWith("sibling.")) return;
  try {
    // The unregistered sibling tries to sign in against the BFF too.
    await call("LoginWithEmail", {body: JSON.stringify({email: "x@example.com", password: "x"})});
    document.getElementById("login-attempt").textContent = "sent";
  } catch (e) { document.getElementById("login-attempt").textContent = "blocked"; }
})();
</script>`

// ozidCSP reads the Content-Security-Policy out of deploy/ozid-web's nginx template, so that this
// scenario and the image cannot drift apart.
func ozidCSP(apiOrigin string) string {
	raw, err := os.ReadFile("../../deploy/ozid-web/default.conf.template")
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if _, rest, ok := strings.Cut(line, `add_header Content-Security-Policy "`); ok {
			policy, _, _ := strings.Cut(rest, `" always;`)
			return strings.ReplaceAll(policy, "${API_ORIGIN}", apiOrigin)
		}
	}
	panic("no Content-Security-Policy in the ozid-web template")
}

// ozidLandingHeaders are the response headers deploy/ozid-web's nginx template gives the mail
// landing pages (the location block for /account/verify-email and /account/reset-password),
// read from the template for the same reason.
func ozidLandingHeaders(apiOrigin string) map[string]string {
	raw, err := os.ReadFile("../../deploy/ozid-web/default.conf.template")
	if err != nil {
		panic(err)
	}
	text := string(raw)
	start := strings.Index(text, "location ~ ^/account/")
	if start < 0 {
		panic("no landing location in the ozid-web template")
	}
	end := strings.Index(text[start:], "\n    }")
	headers := map[string]string{}
	for _, line := range strings.Split(text[start:start+end], "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "add_header ")
		if !ok {
			continue
		}
		name, value, _ := strings.Cut(rest, " ")
		value = strings.TrimSuffix(strings.TrimSuffix(value, " always;"), ";")
		headers[name] = strings.ReplaceAll(strings.Trim(value, `"`), "${API_ORIGIN}", apiOrigin)
	}
	if len(headers) == 0 {
		panic("the landing location of the ozid-web template sets no headers")
	}
	return headers
}

var landingPath = regexp.MustCompile(`^/account/(verify-email|reset-password)/?$`)

// mailbox is the browser scenario's far end of the mail vendor: it runs the real outbox and
// answers with the link of the latest mail to an address, waiting for the count-th one.
func mailbox(h *ChainHarness) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		to := r.URL.Query().Get("to")
		want, _ := strconv.Atoi(r.URL.Query().Get("count"))
		deadline := time.Now().Add(15 * time.Second)
		for len(h.Inbox.To(to)) < want {
			if time.Now().After(deadline) {
				http.Error(w, "no mail", http.StatusGatewayTimeout)
				return
			}
			if _, err := h.UsersrvRes.Mail.RunOnce(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		msgs := h.Inbox.To(to)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"link": mailLink.FindString(msgs[len(msgs)-1].Text)})
	})
}

func throwawayCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ozid e2e"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"*." + ozidBrowserSite},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func run(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	t.Logf("%s %s\n%s", name, strings.Join(args, " "), out)
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
}

// TestOzidBrowserChain drives OzID in a real Chromium against the real BFF and usersrv over
// HTTPS, on separate sibling sub-domains for OzID, the API and the products. It builds the
// OzID frontend for those addresses, so it takes a while, and needs node with the workspace
// dependencies and Playwright's Chromium: it only runs with OZID_BROWSER_E2E=1.
func TestOzidBrowserChain(t *testing.T) {
	if os.Getenv("OZID_BROWSER_E2E") != "1" {
		t.Skip("set OZID_BROWSER_E2E=1 (needs node, the workspace's npm dependencies and Playwright's Chromium)")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	// The front's port is chosen first: the origins are part of the BFF's configuration.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	origin := func(label string) string { return fmt.Sprintf("https://%s.%s:%d", label, ozidBrowserSite, port) }
	idO, apiO, metroO, tripO, evilO, mailO := origin("id"), origin("api"), origin("metro"), origin("trip"), origin("sibling"), origin("mailbox")

	// The stack is the real BFF and usersrv with both products, tripmap's media in an in-memory object
	// store and a switchable metro outage: closing an account in the browser runs through all of it.
	stack0 := newCloseAccountStack(t, ChainHTTPOptions{
		MailOzidOrigin: idO,
		Configure: func(cfg *bffconfig.Config, _ string) {
			cfg.Auth.CookieSecure = true
			cfg.CORSAllowedOrigins = []string{idO, metroO, tripO}
			cfg.Navigation = navigation.Config{
				APIOrigin: apiO,
				Apps: map[string]navigation.App{
					"ozid":     {Origin: idO, Paths: []string{"/security"}},
					"trip-map": {Origin: tripO},
					"metro":    {Origin: metroO, Paths: []string{"/me", "/city/*"}},
				},
			}
		},
	})
	h := stack0.ChainHarness
	const subject, display = "browser-user", "Oz Browser"
	if _, err := h.FreshBrowser().Register(context.Background(), subject, "", display); err != nil {
		t.Fatal(err)
	}
	// Isolated accounts for the close-account scenarios, one set per Playwright project (each
	// account can be closed once), each with a photo, a visit and a ride; the bystander is only
	// ever checked, never closed.
	seeds := map[string]closeAccountSeed{}
	seedNames := []string{"close-main-desktop", "close-main-mobile", "close-bystander"}
	for _, project := range []string{"desktop", "mobile"} {
		// guard: another account signs in over an unfinished deletion; late: a sign-in response that
		// arrives after the deletion began; lreq: a sign-out request that arrives after it began;
		// lost1 / lost2: the first (recovery Cookie) / the second (the start) response is lost;
		// rst: a restarted BFF finishes it; open and swap are never closed (a sign-out response
		// that arrives late; another account's sign-in in another tab).
		for _, kind := range []string{"guard", "late", "lreq", "lost1", "lost2", "rst", "open", "swap"} {
			seedNames = append(seedNames, "close-"+kind+"-"+project)
		}
	}
	for _, name := range seedNames {
		_, id, key := stack0.account(name)
		seeds[name] = closeAccountSeed{ID: id, Key: key}
	}
	for _, name := range []string{"close-admin-desktop", "close-admin-mobile"} {
		if err := h.FreshBrowser().LoginWithRoles(context.Background(), name, "ozid-admin"); err != nil {
			t.Fatal(err)
		}
	}
	holds := newResponseHold()
	controlMux := http.NewServeMux()
	holds.mount(controlMux)
	controlMux.Handle("/", stack0.control(seeds))
	var control http.Handler = controlMux

	// The OzID bundle for exactly these addresses.
	run(t, root, []string{"VITE_API_BASE_URL=" + apiO + "/api"}, "npm", "run", "build", "-w", "@kongken/ozid")
	dist := filepath.Join(root, "apps", "ozid", "dist")

	bff, err := url.Parse(h.BFFURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(bff) // keeps the browser's Host and Origin
	static := http.FileServer(http.Dir(dist))
	landing := ozidLandingHeaders(apiO)
	mail := mailbox(h)
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		label, _, _ := strings.Cut(r.Host, ".")
		switch label {
		case "api":
			holds.serve(proxy, w, r)
		case "id":
			isLanding := landingPath.MatchString(r.URL.Path) // before the single-page fallback rewrites it
			if _, err := os.Stat(filepath.Join(dist, filepath.Clean("/"+r.URL.Path))); err != nil {
				r.URL.Path = "/" // the single-page app's own routes
			}
			w.Header().Set("Cache-Control", "no-store")
			// The policy of deploy/ozid-web (API_ORIGIN = the API's origin), so the real bundle is
			// proven to run under it.
			w.Header().Set("Content-Security-Policy", ozidCSP(apiO))
			w.Header().Set("X-Frame-Options", "DENY")
			if isLanding {
				// The mail landing pages carry what the image's landing location gives them.
				for name, value := range landing {
					w.Header().Set(name, value)
				}
			}
			static.ServeHTTP(w, r)
		case "mailbox":
			mail.ServeHTTP(w, r)
		case "control":
			control.ServeHTTP(w, r)
		case "metro", "trip", "sibling":
			app := map[string]string{"metro": "metro", "trip": "trip-map", "sibling": "trip-map"}[label]
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, standInPage, app, apiO, idO)
		default:
			http.NotFound(w, r)
		}
	})
	front := &http.Server{Handler: mux, TLSConfig: &tls.Config{Certificates: []tls.Certificate{throwawayCert(t)}}}
	go func() { _ = front.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = front.Close() })

	stack, _ := json.Marshal(map[string]any{
		"id": idO, "api": apiO, "metro": metroO, "trip": tripO, "evil": evilO, "mailbox": mailO, "control": origin("control"),
		"landingHeaders": landing,
		"email":          subject + "@example.com", "password": E2EPassword, "displayName": display,
	})
	run(t, root, []string{"OZID_REAL_STACK=" + string(stack)},
		"npx", "playwright", "test", "-c", "apps/ozid/playwright.real.config.ts")
}
