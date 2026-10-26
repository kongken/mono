package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/pkg/proto/bff/metro/v1/bffmetrov1connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// The probes of TestGatewayDomainSets. Each states WHICH layer must answer, not only that something
// refused: a request that a gateway refuses must not appear at the BFF at all; one the BFF refuses
// (the gateway's own rule removed) must appear there with a refusal only, and metro's administration
// must not have run.

var (
	adminProcedures = []string{
		bffv1connect.UserAdminServiceListUsersProcedure,
		bffv1connect.AccessTokenServiceListAccessTokensProcedure,
		bffmetrov1connect.AdminServiceListAdminCitiesProcedure,
	}
	// accountWrites are the sensitive account procedures the user center alone may call.
	accountWrites = []string{
		bffv1connect.AuthServiceLoginWithEmailProcedure,
		bffv1connect.AuthServiceRequestRegistrationProcedure,
		bffv1connect.AuthServiceCompleteRegistrationProcedure,
		bffv1connect.AuthServiceRequestPasswordResetProcedure,
		bffv1connect.AuthServiceResetPasswordProcedure,
		bffv1connect.AuthServiceReauthenticateProcedure,
		bffv1connect.AuthServiceChangePasswordProcedure,
		bffv1connect.AuthServiceListSessionsProcedure,
		bffv1connect.AuthServiceRevokeSessionProcedure,
		bffv1connect.AuthServiceRevokeOtherSessionsProcedure,
		bffv1connect.AuthServiceDeleteAccountProcedure,
		bffv1connect.AuthServiceGetAccountDeletionProcedure,
		bffv1connect.AuthServiceRetryAccountDeletionProcedure,
		bffv1connect.UserServiceUpdateMyProfileProcedure,
	}
	// openProcedures stay available to every registered product.
	openProcedures = []string{
		bffv1connect.AuthServiceMeProcedure,
		bffv1connect.AuthServiceIssueCSRFTokenProcedure,
		bffv1connect.AuthServiceLogoutProcedure,
	}
)

// connectPost is one Connect JSON call.
func (g *gwRun) connectPost(port int, src, label, procedure, origin string, extra ...string) (int, http.Header, string) {
	g.t.Helper()
	return g.do(port, src, http.MethodPost, "https://"+g.set.host(label)+"/api"+procedure, `{}`, connectHeaders(origin, extra...)...)
}

func (g *gwRun) url(label, path string) string { return "https://" + g.set.host(label) + path }

// notAtBFF fails when anything reached the BFF since mark.
func (g *gwRun) notAtBFF(what string, mark int) {
	g.t.Helper()
	if hits := g.obs.since(mark); len(hits) != 0 {
		g.t.Errorf("%s reached the BFF: %+v", what, hits)
	}
}

// onlyRefusedAtBFF fails when something that was not a refusal reached the BFF since mark, or
// metro's administration ran.
func (g *gwRun) onlyRefusedAtBFF(what string, mark int, adminBefore int64) {
	g.t.Helper()
	for _, hit := range g.obs.since(mark) {
		if hit.status != http.StatusForbidden && hit.status != http.StatusNotFound {
			g.t.Errorf("%s was not refused at the BFF: %+v", what, hit)
		}
	}
	if n := g.metro.Load(); n != adminBefore {
		g.t.Errorf("%s ran metro's administration (%d calls -> %d)", what, adminBefore, n)
	}
}

func (g *gwRun) wantStatus(what string, got, want int) {
	g.t.Helper()
	if got != want {
		g.t.Errorf("%s: HTTP %d, want %d", what, got, want)
	}
}

// ---- the public edge: what each host serves, and what it does not ----------------------------------

func (g *gwRun) probeEdge() {
	t := g.t
	mark := g.obs.mark()
	in := func(method, label, path, body string, headers ...string) (int, http.Header, string) {
		return g.do(g.main, gwInsideClient, method, g.url(label, path), body, headers...)
	}

	// OzID: the image's own policy and landing headers, from the real Nginx behind the real Caddy.
	status, h, _ := in("GET", g.set.id, "/login", "")
	g.wantStatus("id /login", status, 200)
	if got, want := h.Get("Content-Security-Policy"), ozidCSP(g.apiO); got != want {
		t.Errorf("id CSP = %q, want %q", got, want)
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "connect-src 'self' "+g.apiO) || strings.Contains(h.Get("Content-Security-Policy"), "*") {
		t.Errorf("id CSP is not pinned to this set's API origin: %q", h.Get("Content-Security-Policy"))
	}
	if h.Get("X-Frame-Options") != "DENY" || h.Get("Server") != "" {
		t.Errorf("id headers: %v", h)
	}
	for _, path := range []string{"/account/verify-email", "/account/reset-password", "/account/verify-email/"} {
		_, h, _ = in("GET", g.set.id, path, "")
		for name, want := range ozidLandingHeaders(g.apiO) {
			if got := h.Get(name); got != want {
				t.Errorf("id %s: %s = %q, want %q", path, name, got, want)
			}
		}
	}
	if status, _, _ = in("GET", g.set.id, "/some/unknown/route", ""); status != 200 {
		t.Errorf("id single-page fallback: HTTP %d", status)
	}
	entries, _ := filepath.Glob(filepath.Join(g.dist["ozid"], "assets", "*.js"))
	if len(entries) == 0 {
		t.Fatal("the OzID build has no assets")
	}
	if _, h, _ = in("GET", g.set.id, "/assets/"+filepath.Base(entries[0]), ""); !strings.Contains(strings.Join(h.Values("Cache-Control"), ", "), "immutable") {
		t.Errorf("id asset Cache-Control = %q", h.Values("Cache-Control"))
	}

	// The retired entries and the closed same-origin API of both products, through the real Caddy.
	for _, label := range []string{g.set.trip, g.set.metro} {
		for _, path := range []string{"/auth/login", "/auth/callback/x", "/auth/"} {
			if status, _, _ = in("GET", label, path, ""); status != 404 {
				t.Errorf("%s %s: HTTP %d, want 404 (a retired login entry)", label, path, status)
			}
		}
		for _, procedure := range append(append(append([]string{}, adminProcedures...), accountWrites...), openProcedures...) {
			for _, origin := range []string{"", g.origin(label)} {
				if status, _, _ = in("POST", label, "/api"+procedure, `{}`, connectHeaders(origin)...); status != 404 {
					t.Errorf("%s same-origin /api%s: HTTP %d, want 404", label, procedure, status)
				}
			}
		}
		for _, path := range []string{"/api/", "/api/x", "/api/bff.v1.AuthService/Me"} {
			if status, _, _ = in("GET", label, path, ""); status != 404 {
				t.Errorf("%s %s: HTTP %d, want 404", label, path, status)
			}
		}
		// The product's own landing page: the old-mail entry, with its gateway headers.
		_, h, _ = in("GET", label, "/account/verify-email", "")
		if h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "no-store" || !strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self'") || h.Get("Server") != "" {
			t.Errorf("%s landing headers: %v", label, h)
		}
	}
	for _, label := range []string{g.set.trip, g.set.metro} {
		// A dot segment is resolved by the gateway before routing: it gets the single-page shell or a 404, never the BFF.
		in("GET", label, "/api/%2e%2e/x", "")
		in("GET", label, "/api/..%2fauth/login", "")
	}
	g.notAtBFF("a product host's closed paths", mark)
	for _, label := range []string{g.set.trip, g.set.metro} {
		if status, _, _ = in("GET", label, "/ping", ""); status != 200 {
			t.Errorf("%s /ping: HTTP %d", label, status)
		}
	}
}

func (g *gwRun) origin(label string) string { return g.set.origin(label) }

// ---- the console host: three layers, tried one at a time ---------------------------------------------

func (g *gwRun) probeConsoleAllowlist() {
	t := g.t
	admin := g.set.admin
	// Inside: the real console through the allowlist; its headers come from the container's template.
	status, h, body := g.do(g.main, gwInsideClient, "GET", g.url(admin, "/"), "")
	g.wantStatus("console from inside", status, 200)
	if !strings.Contains(h.Get("Content-Security-Policy"), "connect-src 'self' "+g.apiO) || h.Get("X-Frame-Options") == "" || !strings.Contains(body, "<div id=\"root\">") {
		t.Errorf("console page: headers %v", h)
	}
	if _, h, _ := g.do(g.main, gwInsideClient, "GET", g.url(admin, "/index.html"), ""); h.Get("Cache-Control") != "no-store" {
		t.Errorf("console shell Cache-Control = %q", h.Get("Cache-Control"))
	}
	if status, _, _ = g.do(g.main, gwInsideClient, "GET", g.url(admin, "/users/anything"), ""); status != 200 {
		t.Errorf("console single-page fallback from inside: HTTP %d", status)
	}

	// Outside, gateway rule in place: a bare 403 and no body for every kind of request.
	for _, path := range []string{"/", "/users", "/index.html", "/assets/x.js", "/healthz", "/nothing"} {
		mark := g.obs.mark()
		status, h, body := g.do(g.main, gwOutsideClient, "GET", g.url(admin, path), "")
		g.wantStatus("console "+path+" from outside (gateway)", status, 403)
		if body != "" || h.Get("Content-Security-Policy") != "" {
			t.Errorf("the gateway's refusal of %s says more than no: %q %v", path, body, h)
		}
		g.notAtBFF("console "+path+" from outside", mark)
	}
	// The shipped example's own documentation range admits nobody real: with nothing configured, closed.
	for _, src := range []string{gwInsideClient, gwOutsideClient} {
		status, _, _ = g.do(g.shipped, src, "GET", g.url(admin, "/"), "")
		g.wantStatus("console through the as-shipped gateway (no real network allowed) from "+src, status, 403)
	}

	// The gateway's own rule removed (as if forgotten): the container is the next layer, and then the BFF.
	for _, path := range []string{"/", "/users", "/index.html", "/assets/x.js", "/nothing"} {
		status, h, body := g.do(g.noCheck, gwOutsideClient, "GET", g.url(admin, path), "")
		g.wantStatus("console "+path+" from outside with no gateway rule", status, 403)
		if body != "forbidden\n" || h.Get("Cache-Control") != "no-store" {
			t.Errorf("the container's refusal of %s: %q %v", path, body, h)
		}
	}
	status, _, _ = g.do(g.noCheck, gwInsideClient, "GET", g.url(admin, "/"), "")
	g.wantStatus("console from inside with no gateway rule", status, 200)

	// The BFF alone, with the gateway rule removed: the management procedures from outside the list.
	for _, procedure := range adminProcedures {
		mark, before := g.obs.mark(), g.metro.Load()
		status, h, body := g.connectPost(g.noCheck, gwOutsideClient, g.set.api, procedure, g.adminO)
		g.wantStatus(procedure+" from outside, BFF layer", status, 403)
		if strings.TrimSpace(body) != "" && !strings.Contains(body, "request rejected") {
			t.Errorf("%s refusal body: %q", procedure, body)
		}
		if h.Get("Access-Control-Allow-Origin") == "*" {
			t.Errorf("%s: wildcard CORS", procedure)
		}
		if len(g.obs.since(mark)) == 0 {
			t.Errorf("%s was refused before the BFF with the gateway rule removed? the scenario is not testing the BFF layer", procedure)
		}
		g.onlyRefusedAtBFF(procedure+" from outside", mark, before)
	}
	// An allowed address with an Origin that is not the console's: the Origin rule refuses it by itself.
	for _, origin := range []string{"", g.idO, g.tripO, g.metroO, g.apiO, g.set.origin("sibling"), "https://" + g.set.host(g.set.admin) + ":8443", strings.Replace(g.adminO, "https", "http", 1)} {
		for _, procedure := range adminProcedures {
			mark, before := g.obs.mark(), g.metro.Load()
			status, _, _ := g.connectPost(g.main, gwInsideClient, g.set.api, procedure, origin)
			g.wantStatus(fmt.Sprintf("%s with Origin %q from an allowed address", procedure, origin), status, 403)
			g.onlyRefusedAtBFF(procedure, mark, before)
		}
	}
	// The sensitive account writes: only the user center. Every product, the console and an unregistered
	// sibling are refused; the open procedures still answer a product.
	for _, procedure := range accountWrites {
		for _, origin := range []string{"", g.tripO, g.metroO, g.adminO, g.set.origin("sibling")} {
			mark := g.obs.mark()
			status, _, _ := g.connectPost(g.main, gwInsideClient, g.set.api, procedure, origin)
			g.wantStatus(fmt.Sprintf("%s with Origin %q", procedure, origin), status, 403)
			for _, hit := range g.obs.since(mark) {
				if hit.status != http.StatusForbidden {
					t.Errorf("%s with Origin %q was not refused: %+v", procedure, origin, hit)
				}
			}
		}
	}
	// The procedures every registered application keeps (sign-in state, the CSRF token, an idempotent sign-out)
	// answer each of them, with the real CSRF exchange and no session.
	for _, origin := range []string{g.tripO, g.metroO, g.idO, g.adminO} {
		jar, _ := cookiejar.New(nil)
		hc := gwClient(gwInsideClient, g.main, "")
		hc.Jar = jar
		auth := bffv1connect.NewAuthServiceClient(hc, g.apiO+"/api")
		token := connect.NewRequest(&bffv1.IssueCSRFTokenRequest{})
		token.Header().Set("Origin", origin)
		res, err := auth.IssueCSRFToken(context.Background(), token)
		if err != nil {
			t.Errorf("IssueCSRFToken for %s: %v", origin, err)
			continue
		}
		me := connect.NewRequest(&bffv1.MeRequest{})
		me.Header().Set("Origin", origin)
		if _, err := auth.Me(context.Background(), me); connect.CodeOf(err) != connect.CodeUnauthenticated && err != nil {
			t.Errorf("Me for %s: %v", origin, err)
		}
		out := connect.NewRequest(&bffv1.LogoutRequest{})
		out.Header().Set("Origin", origin)
		out.Header().Set(session.CSRFHeader, res.Msg.GetToken())
		if _, err := auth.Logout(context.Background(), out); err != nil {
			t.Errorf("sign-out from %s without a session: %v", origin, err)
		}
	}
}

// ---- everything that must not get around it --------------------------------------------------------

func (g *gwRun) probeBypasses() {
	t := g.t
	admin := g.set.admin

	forged := [][]string{
		{"X-Forwarded-For", gwInsideClient},
		{"X-Forwarded-For", gwInsideClient + ", " + gwOutsideClient},
		{"X-Forwarded-For", gwOutsideClient + ", " + gwInsideClient},
		{"X-Forwarded-For", gwMachineClient},
		{"X-Forwarded-For", "::ffff:127.0.0.1"},
		{"X-Forwarded-For", "nonsense"},
		{"X-Real-IP", gwInsideClient},
		{"Forwarded", "for=" + gwInsideClient},
		{"X-Forwarded-Host", g.set.host(admin)},
		{"X-Original-Forwarded-For", gwInsideClient},
		{"X-Client-IP", gwInsideClient},
		{"True-Client-IP", gwInsideClient},
		{"CF-Connecting-IP", gwInsideClient},
	}
	// 1. The console host from outside, through each gateway mode, with forged forwarding headers and odd
	//    spellings of the path: always the layer that is meant to refuse.
	paths := []string{"/", "//", "/%2e%2e/", "/users/..%2f", "/%61ssets/x.js", "/assets/../index.html", "/..;/", "/index.html?x=1", "/users%00", "/healthz"}
	for _, f := range forged {
		for _, path := range paths {
			status, _, _ := g.do(g.main, gwOutsideClient, "GET", g.url(admin, path), "", f...)
			g.wantStatus(fmt.Sprintf("console %q with %v from outside (gateway)", path, f), status, 403)
			status, _, body := g.do(g.noCheck, gwOutsideClient, "GET", g.url(admin, path), "", f...)
			if status == 400 && (path == "/%2e%2e/" || path == "/users%00") {
				// The container rejects a malformed request target before it looks at the source; nothing is served.
				if strings.Contains(body, "<center><h1>400") {
					continue
				}
			}
			g.wantStatus(fmt.Sprintf("console %q with %v from outside (container)", path, f), status, 403)
			if body != "forbidden\n" {
				t.Errorf("console %q with %v: unexpected body %q", path, f, body)
			}
		}
	}
	// 2. Preflight and other methods on the console host never get an answer from outside.
	for _, method := range []string{"OPTIONS", "POST", "PUT", "DELETE", "HEAD", "PATCH"} {
		for _, port := range []int{g.main, g.noCheck} {
			status, h, _ := g.do(port, gwOutsideClient, method, g.url(admin, "/"), "", "Origin", g.idO, "Access-Control-Request-Method", "POST")
			g.wantStatus("console "+method+" from outside", status, 403)
			if h.Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("console %s from outside carries CORS: %v", method, h)
			}
		}
	}
	// 3. The BFF behind the open gateway: management procedures with forged headers from outside. The
	//    gateway replaces the forwarding header with the address it saw, so a forged value is never the
	//    client the BFF judges.
	for _, f := range forged {
		for _, procedure := range adminProcedures {
			mark, before := g.obs.mark(), g.metro.Load()
			status, _, _ := g.connectPost(g.noCheck, gwOutsideClient, g.set.api, procedure, g.adminO, f...)
			g.wantStatus(fmt.Sprintf("%s with %v from outside", procedure, f), status, 403)
			g.onlyRefusedAtBFF(procedure, mark, before)
		}
	}
	// 4. The API host serves only the BFF's own paths: no console, no MCP, no static files.
	for _, path := range []string{"/", "/users", "/mcp/metro", "/index.html", "/assets/x.js", "/account/verify-email", "/auth/login", "/metrics", "/debug/pprof/", "/api"} {
		mark := g.obs.mark()
		if status, _, _ := g.do(g.main, gwInsideClient, "POST", g.url(g.set.api, path), `{}`, "Content-Type", "application/json"); status != 404 {
			t.Errorf("api host %s: HTTP %d, want 404", path, status)
		}
		for _, hit := range g.obs.since(mark) {
			t.Errorf("api host %s reached the BFF: %+v", path, hit)
		}
	}
	// 5. CORS: the exact list, credentials only for it, never a wildcard.
	preflight := func(port int, origin string) http.Header {
		_, h, _ := g.do(port, gwInsideClient, "OPTIONS", g.url(g.set.api, "/api"+bffv1connect.AuthServiceMeProcedure), "",
			"Origin", origin, "Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "content-type,connect-protocol-version,"+strings.ToLower(session.CSRFHeader))
		return h
	}
	for _, origin := range []string{g.idO, g.adminO, g.tripO, g.metroO} {
		h := preflight(g.main, origin)
		if h.Get("Access-Control-Allow-Origin") != origin || h.Get("Access-Control-Allow-Credentials") != "true" {
			t.Errorf("preflight from the registered %s: %v", origin, h)
		}
	}
	for _, origin := range []string{
		g.set.origin("sibling"), "https://evil.example.com", strings.Replace(g.idO, "https", "http", 1), g.idO + ":8443",
		"null", "https://" + g.set.site, "https://x." + g.idO[len("https://"):], g.origin(g.set.id) + ".evil.example.com",
		// The other set's names (old or foreign): not registered with this deployment.
		g.otherSet().origin(g.otherSet().id), g.otherSet().origin(g.otherSet().trip),
	} {
		h := preflight(g.main, origin)
		if h.Get("Access-Control-Allow-Origin") != "" || h.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("preflight from the unregistered %q is answered: %v", origin, h)
		}
	}
	// 6. Other hosts, unknown names, the other set's names (an old mapping) and a name that does not match
	//    the TLS server name: nothing is served and nothing reaches the BFF.
	other := g.otherSet()
	names := []string{
		g.set.host("unknown"), g.set.host("www"), g.set.site, "example.com", g.set.host(admin),
		other.host(other.id), other.host(other.api), other.host(other.admin), other.host(other.metro), other.host(other.trip),
		"127.0.0.1", "localhost",
	}
	for _, name := range names {
		mark := g.obs.mark()
		for _, port := range []int{g.main, g.noCheck} {
			// With its own name as the TLS server name (what a browser sends) and with a name of this set.
			for _, sni := range []string{"", g.set.host(g.set.api)} {
				res, err := func() (int, error) {
					req, _ := http.NewRequest("POST", "https://"+name+"/api"+bffv1connect.AuthServiceMeProcedure, strings.NewReader(`{}`))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Connect-Protocol-Version", "1")
					req.Header.Set("Origin", g.idO)
					if sni != "" {
						req.Host = name // the Host header differs from the TLS name
					}
					resp, err := gwClient(gwOutsideClient, port, sni).Do(req)
					if err != nil {
						return 0, err
					}
					defer resp.Body.Close()
					buf := make([]byte, 64)
					n, _ := resp.Body.Read(buf)
					if n > 0 && resp.StatusCode < 300 {
						return resp.StatusCode, fmt.Errorf("%d bytes served", n)
					}
					return resp.StatusCode, nil
				}()
				// The gateway answers a Host that no site block has with an empty 200 (Caddy's own behaviour);
				// what matters is that nothing is served for it and nothing reaches the BFF.
				if err != nil && strings.Contains(err.Error(), "bytes served") {
					t.Errorf("host %q (SNI %q) was served content by the gateway: HTTP %d, %v", name, sni, res, err)
				}
				if err == nil && res >= 200 && res < 300 && name == g.set.host(admin) {
					t.Errorf("console host %q with SNI %q: HTTP %d from outside", name, sni, res)
				}
			}
		}
		g.notAtBFF("host "+name, mark)
	}
	// 7. The bare ports. The containers and the BFF are meant to be reachable from the gateway only; here a
	//    client from outside the lists dials them directly, with every trick the gateway would have stopped.
	bare := plainClient(gwOutsideClient)
	adminBare := fmt.Sprintf("http://127.0.0.1:%d", g.adminNginx.port)
	rawDo := func(method, rawURL, host string, hdr ...string) (int, string, error) {
		req, _ := http.NewRequest(method, rawURL, strings.NewReader(""))
		req.Host = host
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		res, err := bare.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer res.Body.Close()
		buf := make([]byte, 512)
		n, _ := res.Body.Read(buf)
		return res.StatusCode, string(buf[:n]), nil
	}
	for _, f := range append([][]string{nil}, forged...) {
		if status, body, err := rawDo("GET", adminBare+"/", g.set.host(admin), f...); err != nil || status != 403 || body != "forbidden\n" {
			t.Errorf("the console container's bare port from outside with %v: %d %q %v", f, status, body, err)
		}
	}
	// A host that is not the console's: dropped without an answer, or the health probe on the default server.
	for _, host := range []string{"127.0.0.1", fmt.Sprintf("127.0.0.1:%d", g.adminNginx.port), g.set.host(admin) + ":8443", "other.example.com", g.set.host(g.set.api)} {
		if status, _, err := rawDo("GET", adminBare+"/users", host); err == nil {
			t.Errorf("the console container answered Host %q with HTTP %d (it must close the connection)", host, status)
		}
	}
	if status, body, err := rawDo("GET", adminBare+"/healthz", "probe.invalid"); err != nil || status != 200 || body != "ok\n" {
		t.Errorf("the console container's health probe: %d %q %v", status, body, err)
	}
	// The BFF's own port from outside: the perimeter is the BFF's, and a forged header is only read from a
	// trusted peer.
	for _, f := range forged {
		for _, procedure := range adminProcedures {
			mark, before := g.obs.mark(), g.metro.Load()
			status, _, _ := g.do(0, gwOutsideClient, "POST", g.h.BFFURL+"/api"+procedure, `{}`, connectHeaders(g.adminO, f...)...)
			g.wantStatus(fmt.Sprintf("%s on the BFF's bare port with %v", procedure, f), status, 403)
			g.onlyRefusedAtBFF(procedure, mark, before)
		}
	}
	// The sensitive account writes on the bare port, from a product's Origin.
	for _, procedure := range accountWrites {
		status, _, _ := g.do(0, gwOutsideClient, "POST", g.h.BFFURL+"/api"+procedure, `{}`, connectHeaders(g.tripO)...)
		g.wantStatus(procedure+" on the BFF's bare port from a product", status, 403)
	}
}

func (g *gwRun) otherSet() gwSet {
	for _, s := range gatewaySets {
		if s.name != g.set.name {
			return s
		}
	}
	panic("only one domain set")
}

// ---- machine access ---------------------------------------------------------------------------------

// session signs in through the gateway like OzID would and returns a client that holds the cookie.
func (g *gwRun) session(email string) (*http.Client, string) {
	g.t.Helper()
	ctx := context.Background()
	jar, err := cookiejar.New(nil)
	if err != nil {
		g.t.Fatal(err)
	}
	hc := gwClient(gwInsideClient, g.main, "")
	hc.Jar = jar
	auth := bffv1connect.NewAuthServiceClient(hc, g.apiO+"/api")
	csrf := func() string {
		req := connect.NewRequest(&bffv1.IssueCSRFTokenRequest{})
		req.Header().Set("Origin", g.idO)
		res, err := auth.IssueCSRFToken(ctx, req)
		if err != nil {
			g.t.Fatalf("csrf: %v", err)
		}
		return res.Msg.GetToken()
	}
	login := connect.NewRequest(&bffv1.LoginWithEmailRequest{Email: email, Password: E2EPassword})
	login.Header().Set("Origin", g.idO)
	login.Header().Set(session.CSRFHeader, csrf())
	if _, err := auth.LoginWithEmail(ctx, login); err != nil {
		g.t.Fatalf("sign-in through the gateway: %v", err)
	}
	return hc, csrf()
}

func (g *gwRun) mintToken(name string, scopes ...string) string {
	g.t.Helper()
	ctx := context.Background()
	hc, csrf := g.session(emailFor("gw-token-" + g.set.name))
	req := connect.NewRequest(&bffv1.CreateAccessTokenRequest{Name: name, Scopes: scopes, ExpiresInDays: 30})
	req.Header().Set("Origin", g.adminO)
	req.Header().Set(session.CSRFHeader, csrf)
	res, err := bffv1connect.NewAccessTokenServiceClient(hc, g.apiO+"/api").CreateAccessToken(ctx, req)
	if err != nil {
		g.t.Fatalf("create a management token through the gateway: %v", err)
	}
	return res.Msg.GetSecret()
}

func (g *gwRun) mcp(port int, src string, step protocolStep, token string, extra ...string) (int, http.Header, string) {
	g.t.Helper()
	headers := []string{"Accept", "application/json, text/event-stream"}
	if step.body != "" {
		headers = append(headers, "Content-Type", "application/json")
	}
	if token != "" {
		headers = append(headers, "Authorization", "Bearer "+token)
	}
	return g.do(port, src, step.method, g.url(g.set.metro, "/mcp/metro"), step.body, append(headers, extra...)...)
}

func (g *gwRun) probeMCP() {
	t := g.t
	ctx := context.Background()
	write := g.mintToken("gateway-write", "metro:write")
	read := g.mintToken("gateway-read", "metro:read")

	// The machine, from its own network: the whole protocol through the gateway.
	for _, step := range protocolSteps("gw-" + g.set.name) {
		if status, _, body := g.mcp(g.main, gwMachineClient, step, write); status != step.ok {
			t.Errorf("MCP %s from the machine network: HTTP %d, want %d (%s)", step.name, status, step.ok, body)
		}
	}
	cities := func() int { n, _ := g.h.MetroDB.City.Query().Count(ctx); return n }
	usedTokens := func() int {
		list, err := g.operatorTokens(ctx)
		if err != nil {
			t.Fatalf("list tokens: %v", err)
		}
		return list
	}
	baseCities, baseUsed, baseAdmin := cities(), usedTokens(), g.metro.Load()

	// Everyone else is refused with the same bare 403: a browser address (the console's allowlist is a
	// different list), an outside address, and the machine's address as a forged forwarding value.
	ping := protocolSteps("x")[2]
	for _, c := range []struct {
		what, src string
		headers   []string
	}{
		{"the console's browser address", gwInsideClient, nil},
		{"an outside address", gwOutsideClient, nil},
		{"an outside address forging the machine's address", gwOutsideClient, []string{"X-Forwarded-For", gwMachineClient}},
		{"an outside address forging a chain", gwOutsideClient, []string{"X-Forwarded-For", gwMachineClient + ", " + gwOutsideClient}},
		{"an outside address with X-Real-IP", gwOutsideClient, []string{"X-Real-IP", gwMachineClient}},
		{"an outside address with Forwarded", gwOutsideClient, []string{"Forwarded", "for=" + gwMachineClient}},
		{"the browser address with a session cookie and the console's Origin", gwInsideClient, []string{"Origin", g.adminO, "Cookie", "kongken_sid=x"}},
	} {
		for _, step := range protocolSteps("denied") {
			status, h, body := g.mcp(g.main, c.src, step, write, c.headers...)
			if status != http.StatusForbidden || strings.TrimSpace(body) != "request rejected" {
				t.Errorf("MCP %s from %s: HTTP %d %q", step.name, c.what, status, body)
			}
			if h.Get("Access-Control-Allow-Origin") != "" || h.Get("WWW-Authenticate") != "" {
				t.Errorf("MCP refusal from %s says more than no: %v", c.what, h)
			}
		}
	}
	// A refusal is not an authentication failure: with no or a wrong token from outside it is the same 403.
	for _, token := range []string{"", "mcp_" + strings.Repeat("0", 64)} {
		if status, _, _ := g.mcp(g.main, gwOutsideClient, ping, token); status != http.StatusForbidden {
			t.Errorf("MCP from outside with token %q: HTTP %d", token, status)
		}
	}
	// On the machine network, a missing or wrong credential, and a browser session in place of a token, are
	// authentication failures and nothing else.
	for _, c := range []struct{ what, token string }{{"no token", ""}, {"a wrong token", "mcp_" + strings.Repeat("0", 64)}} {
		if status, _, _ := g.mcp(g.main, gwMachineClient, ping, c.token); status != http.StatusUnauthorized {
			t.Errorf("MCP from the machine network with %s: HTTP %d, want 401", c.what, status)
		}
	}
	if status, _, _ := g.mcp(g.main, gwMachineClient, ping, "", "Cookie", "kongken_sid=whatever"); status != http.StatusUnauthorized {
		t.Errorf("MCP with only a cookie: HTTP %d, want 401", status)
	}
	// The product hosts' other paths to it: the API host and the console host never carry MCP.
	for _, label := range []string{g.set.api, g.set.trip, g.set.id, g.set.admin} {
		status, _, _ := g.do(g.main, gwMachineClient, "POST", g.url(label, "/mcp/metro"), ping.body, "Authorization", "Bearer "+write, "Content-Type", "application/json")
		if status == 200 {
			t.Errorf("MCP is served on %s", label)
		}
	}
	if baseUsed2, baseCities2, baseAdmin2 := usedTokens(), cities(), g.metro.Load(); baseCities2 != baseCities || baseUsed2 != baseUsed || baseAdmin2 != baseAdmin {
		t.Errorf("refused or unauthenticated MCP requests ran something: cities %d->%d, tokens used %d->%d, metro admin %d->%d", baseCities, baseCities2, baseUsed, baseUsed2, baseAdmin, baseAdmin2)
	}
	_ = read
}

// operatorTokens counts the management tokens that have been used.
func (g *gwRun) operatorTokens(ctx context.Context) (int, error) {
	hc, csrf := g.session(emailFor("gw-token-" + g.set.name))
	req := connect.NewRequest(&bffv1.ListAccessTokensRequest{})
	req.Header().Set("Origin", g.adminO)
	req.Header().Set(session.CSRFHeader, csrf)
	res, err := bffv1connect.NewAccessTokenServiceClient(hc, g.apiO+"/api").ListAccessTokens(ctx, req)
	if err != nil {
		return 0, err
	}
	used := 0
	for _, tk := range res.Msg.Tokens {
		if tk.LastUsedAt != nil {
			used++
		}
	}
	return used, nil
}

// ---- the product images' templates, and what the bundles contain ---------------------------------------

func (g *gwRun) probeProductImages(bins gwBinaries) {
	t := g.t
	root := g.root
	bff := mustPort(t, g.h.BFFURL)
	upstream := fmt.Sprintf("127.0.0.1:%d", bff)
	for _, product := range []struct {
		name, dir, template, dockerfile string
	}{
		{"trip-map", g.dist["trip-map"], "deploy/trip-map-web/default.conf.template", "deploy/trip-map-web/Dockerfile"},
		{"metro", g.dist["metro"], "deploy/metro-web/default.conf.template", "deploy/metro-web/Dockerfile"},
	} {
		port := freePort(t)
		conf := renderTemplate(t, root, product.template, map[string]string{"BACKEND_UPSTREAM": upstream}, dockerEnvFilter(t, root, product.dockerfile))
		conf = mustReplace(t, product.name+" template", conf, "listen 80;", fmt.Sprintf("listen 127.0.0.1:%d;", port))
		conf = mustReplace(t, product.name+" template", conf, "/usr/share/nginx/html", product.dir)
		conf = strings.ReplaceAll(conf, "resolver 127.0.0.11 valid=10s;", "") // Docker's resolver; the upstream here is an address
		startNginx(t, bins, product.name+"-"+g.set.name, port, func(string) string { return conf })
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		bare := plainClient(gwOutsideClient)
		do := func(method, path, body string, hdr ...string) (int, http.Header, string) {
			req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
			for i := 0; i+1 < len(hdr); i += 2 {
				req.Header.Set(hdr[i], hdr[i+1])
			}
			res, err := bare.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			defer res.Body.Close()
			buf := make([]byte, 1024)
			n, _ := res.Body.Read(buf)
			return res.StatusCode, res.Header, string(buf[:n])
		}
		mark := g.obs.mark()
		for _, path := range []string{"/auth/login", "/auth/callback/x", "/auth/", "/api/", "/api/x"} {
			if status, _, _ := do("GET", path, ""); status != 404 {
				t.Errorf("%s image %s: HTTP %d, want 404", product.name, path, status)
			}
		}
		for _, procedure := range append(append(append([]string{}, adminProcedures...), accountWrites...), openProcedures...) {
			if status, _, _ := do("POST", "/api"+procedure, `{}`, connectHeaders(g.tripO)...); status != 404 {
				t.Errorf("%s image /api%s: HTTP %d, want 404", product.name, procedure, status)
			}
		}
		g.notAtBFF(product.name+" image's closed paths", mark)
		_, h, _ := do("GET", "/account/verify-email", "")
		if h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "no-store" || !strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Errorf("%s image landing headers: %v", product.name, h)
		}
		// The paths that must still reach the BFF do (the image forwards them).
		wants := map[string][]string{"trip-map": {"/ping", "/s/abc", "/api/share/abc/x", "/api/notifications/unsubscribe", "/api/tiles/0/0/0"}, "metro": {"/ping"}}[product.name]
		for _, path := range wants {
			mark := g.obs.mark()
			do("GET", path, "")
			if len(g.obs.since(mark)) == 0 {
				t.Errorf("%s image: %s no longer reaches the BFF", product.name, path)
			}
		}
		if product.name == "metro" {
			// A client that is behind the image from outside the machine list: the image's own address is
			// the peer the BFF sees (not a trusted proxy here), so a forged forwarded address cannot admit it.
			mark := g.obs.mark()
			status, _, body := do("POST", "/mcp/metro", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "X-Forwarded-For", gwMachineClient, "Content-Type", "application/json")
			if status != 403 || strings.TrimSpace(body) != "request rejected" {
				t.Errorf("metro image /mcp/metro with a forged address: HTTP %d %q", status, body)
			}
			if hits := g.obs.since(mark); len(hits) != 1 || hits[0].status != 403 {
				t.Errorf("metro image /mcp/metro: %+v", hits)
			}
		}
	}

	// The bundles of the products and the user center hold no management code; the console's does.
	// (The bundles carry the clients' minified method names, not the service names.)
	forbidden := []string{"listUsers", "createAccessToken", "listAdminCities", "/audit/users"}
	for _, name := range []string{"ozid", "trip-map", "metro"} {
		for _, hit := range scanDist(t, g.dist[name], forbidden) {
			t.Errorf("the %s bundle contains %q (%s): management code does not belong in it", name, hit.needle, hit.file)
		}
	}
	if hits := scanDist(t, g.dist["admin"], forbidden); len(hits) == 0 {
		t.Error("the console bundle does not contain the management services: the scan is not looking at the right thing")
	}
	// Every bundle is built for exactly this set: its API origin is in it, and nothing of the other set.
	other := g.otherSet()
	for _, name := range []string{"ozid", "trip-map", "metro", "admin"} {
		if len(scanDist(t, g.dist[name], []string{g.apiO})) == 0 {
			t.Errorf("the %s bundle does not point at %s", name, g.apiO)
		}
		for _, hit := range scanDist(t, g.dist[name], []string{other.site}) {
			t.Errorf("the %s bundle contains the other set's site (%s)", name, hit.file)
		}
	}
}

type distHit struct{ needle, file string }

func scanDist(t *testing.T, dir string, needles []string) []distHit {
	t.Helper()
	var hits []distHit
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !(strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".html")) {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, n := range needles {
			if strings.Contains(string(raw), n) {
				rel, _ := filepath.Rel(dir, path)
				hits = append(hits, distHit{n, rel})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hits
}
