package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kongken/mono/internal/bff/adminguard"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/navigation"
	"sync/atomic"
)

// TestGatewayDomainSets is the T12 acceptance of the configurable deployment: the whole stack (OzID,
// the management console, trip-map, metro, the BFF, usersrv, PostgreSQL and Redis) behind the REAL
// gateways, on two completely different sets of reserved same-site host names, from a real browser.
// Nothing in front of the stack is a stand-in written for the test: see gateway_stack_test.go for what
// is real (Caddy with the shipped example; Nginx with the shipped container templates and entrypoint
// scripts) and what is adapted (names, ports, the loopback addresses that stand in for networks).
//
// For each set it
//  1. builds the four front ends for that set's addresses with the documented build arguments (no change
//     to any source file, no wildcard CORS),
//  2. starts the BFF (registry, CORS, mail origin, admin and MCP allowlists, trusted proxies and the
//     old-mail transition from configuration only) and the gateways,
//  3. probes the gateways from the outside the way the spec lists them (every exposed path, other hosts,
//     the bare ports, preflights, forged forwarding headers, old names) and proves from the BFF's own
//     view, the metro service and the token table that no refused request reached a business handler,
//  4. exercises MCP through the gateway against its own machine allowlist, and
//  5. runs Chromium (e2e/gateway) through the same gateway: register by mail, the old mail page of a
//     product, sign-in from both products and return, the shared session, the console from inside and
//     (a second, closed gateway) from outside the allowlist, and the old set's session on the new names.
//
// It needs nginx, caddy, envsubst, node with the workspace dependencies and Playwright's Chromium, and
// PostgreSQL and Redis: it only runs with GATEWAY_E2E=1, and with it a missing prerequisite is a
// failure, never a skip (CI runs it with `-v` and rejects any SKIP).
func TestGatewayDomainSets(t *testing.T) {
	if os.Getenv("GATEWAY_E2E") != "1" {
		t.Skip("set GATEWAY_E2E=1 (needs nginx, caddy, envsubst, node with the workspace's npm dependencies, Playwright's Chromium, PostgreSQL and Redis)")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bins := gatewayBinaries(t)
	stateDir := t.TempDir()
	previous := ""
	for _, set := range gatewaySets {
		t.Run(set.name, func(t *testing.T) {
			previous = runGatewaySet(t, root, bins, set, stateDir, previous)
		})
		if t.Failed() {
			return
		}
	}
}

// gwRun is everything the probes and the browser run share for one set.
type gwRun struct {
	t     *testing.T
	root  string
	set   gwSet
	h     *ChainHarness
	obs   *bffObserver
	metro *atomic.Int64 // calls that reached metro's administration service

	idO, apiO, tripO, metroO, adminO string
	main, shipped, noCheck           int // HTTPS ports of the three gateways
	ozidNginx, adminNginx            *nginxApp
	dist                             map[string]string
}

// bffObserver records what reached the BFF's HTTP edge.
type bffObserver struct {
	mu   sync.Mutex
	seen []bffHit
}
type bffHit struct {
	method, path, host string
	status             int
}

func (o *bffObserver) observe(r *http.Request, _ http.Header, status int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seen = append(o.seen, bffHit{r.Method, r.URL.Path, r.Host, status})
}
func (o *bffObserver) mark() int { o.mu.Lock(); defer o.mu.Unlock(); return len(o.seen) }
func (o *bffObserver) since(mark int) []bffHit {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]bffHit(nil), o.seen[mark:]...)
}

func runGatewaySet(t *testing.T, root string, bins gwBinaries, set gwSet, stateDir, previousState string) string {
	t.Helper()
	g := &gwRun{t: t, root: root, set: set, obs: &bffObserver{}, metro: &atomic.Int64{}}
	g.idO, g.apiO, g.tripO, g.metroO, g.adminO = set.origin(set.id), set.origin(set.api), set.origin(set.trip), set.origin(set.metro), set.origin(set.admin)

	// ---- the BFF and the services behind it, configured for this set only from configuration ----
	g.h = NewChainHarness(t, ChainMetro{}, ChainMetroUnary{countMetroAdmin(g.metro)}, ChainHTTPOptions{
		MailOzidOrigin: g.idO,
		Observe:        g.obs.observe,
		Configure: func(cfg *bffconfig.Config, _ string) {
			cfg.Auth.CookieSecure = true
			// The exact list. A same-site sibling that is not on it (sibling.<site>) must get nothing.
			cfg.CORSAllowedOrigins = []string{g.idO, g.adminO, g.tripO, g.metroO}
			cfg.Navigation = navigation.Config{
				APIOrigin: g.apiO,
				Apps: map[string]navigation.App{
					"ozid":     {Origin: g.idO, Paths: []string{"/security"}},
					"admin":    {Origin: g.adminO, Paths: []string{"/users", "/users/*", "/audit/users", "/metro", "/metro/*"}},
					"trip-map": {Origin: g.tripO, Paths: []string{"/status"}},
					"metro":    {Origin: g.metroO, Paths: []string{"/me", "/cities/*", "/topics/*", "/u/*"}},
				},
			}
			// The browser console list and the machine list are separate: the gateway is a trusted proxy
			// for both, and is in neither.
			cfg.Admin = adminguard.Config{AllowedCIDRs: []string{gwInsideClient + "/32"}, TrustedProxies: []string{gwGatewayAddress + "/32"}}
			cfg.MCP.AllowedCIDRs = []string{gwMachineClient + "/32"}
			cfg.MCP.TrustedProxies = []string{gwGatewayAddress + "/32"}
			// The old-mail transition: mails sent before the central landing finish on the product's own page.
			cfg.Auth.LegacyMailTransition = bffconfig.LegacyMailTransition{
				Origins: []string{g.tripO, g.metroO},
				Until:   time.Now().Add(6 * time.Hour).UTC().Format(time.RFC3339),
			}
			if err := cfg.Validate([]string{"tripmap", "usersrv", "metro"}); err != nil {
				t.Fatalf("the scenario's own BFF configuration is not valid: %v", err)
			}
		},
	})
	ctx := context.Background()
	// The metro catalogue the products show: Shanghai with two lines.
	sh := g.h.MetroDB.City.Create().SetSlug("shanghai").SetName("上海").SaveX(ctx)
	g.h.MetroDB.Line.Create().SetCode("sh-1").SetCityID(sh.ID).SetName("1号线").SetNumber(1).SetLengthKm(36.9).SetColor("#E4002B").SaveX(ctx)
	g.h.MetroDB.Line.Create().SetCode("sh-2").SetCityID(sh.ID).SetName("2号线").SetNumber(2).SetLengthKm(64).SetColor("#97D700").SaveX(ctx)

	// ---- seeded accounts: one per scenario and project (an account is used by one scenario only) ----
	type seeded struct {
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
	}
	accounts := map[string]seeded{}
	for _, project := range []string{"desktop", "mobile"} {
		for _, k := range []struct {
			kind  string
			roles []string
		}{{"operator", []string{"ozid-admin"}}, {"plain", nil}, {"metro", []string{"metro-admin"}}, {"rider", nil}, {"trip", nil}, {"out", nil}} {
			name := "gw-" + k.kind + "-" + project
			if err := g.h.FreshBrowser().LoginWithRoles(ctx, name, k.roles...); err != nil {
				t.Fatal(err)
			}
			accounts[name] = seeded{Email: emailFor(name), DisplayName: name}
		}
	}
	if err := g.h.FreshBrowser().LoginWithRoles(ctx, "gw-token-"+set.name, "metro-admin"); err != nil {
		t.Fatal(err)
	}

	// ---- the four front ends for this set's addresses (documented build arguments only) ----
	buildDir := t.TempDir()
	g.dist = map[string]string{"ozid": filepath.Join(buildDir, "ozid"), "admin": filepath.Join(buildDir, "admin"),
		"trip-map": filepath.Join(buildDir, "trip-map"), "metro": filepath.Join(buildDir, "metro")}
	api := g.apiO + "/api"
	builds := []struct {
		name, workspace string
		env             []string
	}{
		{"ozid", "@kongken/ozid", []string{"VITE_API_BASE_URL=" + api}},
		{"admin", "@kongken/admin", []string{"VITE_API_BASE_URL=" + api, "VITE_OZID_ORIGIN=" + g.idO}},
		{"trip-map", "@kongken/trip-map", []string{"VITE_API_BASE_URL=" + api, "VITE_OZID_ORIGIN=" + g.idO}},
		{"metro", "@kongken/metro", []string{"VITE_API_BASE_URL=" + api, "VITE_OZID_ORIGIN=" + g.idO}},
	}
	var wg sync.WaitGroup
	errs := make([]error, len(builds))
	for i, b := range builds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command("npm", "run", "build", "-w", b.workspace, "--", "--outDir", g.dist[b.name], "--emptyOutDir")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), b.env...)
			if out, err := cmd.CombinedOutput(); err != nil {
				errs[i] = fmt.Errorf("build %s: %v\n%s", b.name, err, tail(string(out), 3000))
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	// ---- Nginx: the two containers' templates, with the image's own rules ----
	g.ozidNginx = startOzidNginx(t, root, bins, g, freePort(t))
	g.adminNginx = startAdminNginx(t, root, bins, g, freePort(t))

	// ---- Caddy: the shipped example, three instances of it (see caddyMode) ----
	bffPort := mustPort(t, g.h.BFFURL)
	up := gwUpstreams{bff: bffPort, ozid: g.ozidNginx.port, admin: g.adminNginx.port, trip: g.dist["trip-map"], metro: g.dist["metro"]}
	start := func(mode caddyMode) int {
		https, http := freePort(t), freePort(t)
		startCaddy(t, bins, renderCaddyfile(t, root, set, up, mode, https, http), https)
		return https
	}
	g.main, g.shipped, g.noCheck = start(caddyAllowlist), start(caddyAsShipped), start(caddyNoCheck)

	// The test's own mail window: the real outbox is run on demand and the link of the latest mail is
	// returned (the mail vendor is the only stand-in). It is not behind any gateway.
	mailSrv := httptest.NewServer(mailbox(g.h))
	t.Cleanup(mailSrv.Close)

	// ---- what a gateway must refuse, from outside, and how the BFF and metro saw it ----
	t.Run("perimeter", func(t *testing.T) { g.t = t; g.probeEdge(); g.probeConsoleAllowlist(); g.probeBypasses() })
	t.Run("mcp", func(t *testing.T) { g.t = t; g.probeMCP() })
	t.Run("products", func(t *testing.T) { g.t = t; g.probeProductImages(bins) })
	if t.Failed() {
		return previousState
	}

	// ---- the browser ----
	if os.Getenv("GATEWAY_NO_BROWSER") == "1" { // development only: CI never sets it and requires the browser run's "N passed"
		t.Log("GATEWAY_NO_BROWSER=1: the browser run was left out")
		return previousState
	}
	stateOut := filepath.Join(stateDir, set.name+".json")
	stack, _ := json.Marshal(map[string]any{
		"set": set.name, "site": set.site,
		"id": g.idO, "api": g.apiO, "trip": g.tripO, "metro": g.metroO, "admin": g.adminO,
		"adminHost": set.host(set.admin), "mainPort": g.main, "shippedPort": g.shipped,
		"mailbox": mailSrv.URL, "password": E2EPassword, "accounts": accounts,
		"stateOut": stateOut, "stateIn": previousState,
		"landingHeaders": ozidLandingHeaders(g.apiO),
	})
	args := []string{"playwright", "test", "-c", "e2e/gateway/playwright.config.ts"}
	if only := os.Getenv("GATEWAY_BROWSER_GREP"); only != "" { // while developing one scenario
		args = append(args, "-g", only)
	}
	runPlaywrightIn(t, root, []string{"GATEWAY_STACK=" + string(stack)}, args)
	return stateOut
}

func mustPort(t *testing.T, rawURL string) int {
	t.Helper()
	var port int
	if _, err := fmt.Sscanf(rawURL[strings.LastIndex(rawURL, ":")+1:], "%d", &port); err != nil {
		t.Fatalf("no port in %q", rawURL)
	}
	return port
}

// runPlaywrightIn runs the gateway spec; with GATEWAY_BROWSER_ARTIFACTS=<dir> it keeps traces, screenshots,
// videos and the JSON report there for CI to upload (the same convention as the console's real-stack run).
func runPlaywrightIn(t *testing.T, root string, env []string, args []string) {
	t.Helper()
	cmd := exec.Command("npx", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	if dir := os.Getenv("GATEWAY_BROWSER_ARTIFACTS"); dir != "" {
		abs, err := filepath.Abs(filepath.Join(dir, t.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd.Args = append(cmd.Args, "--reporter=list,json", "--output="+filepath.Join(abs, "test-results"))
		cmd.Env = append(cmd.Env, "PLAYWRIGHT_JSON_OUTPUT_NAME="+filepath.Join(abs, "playwright-report.json"))
	}
	out, err := cmd.CombinedOutput()
	t.Logf("npx %s\n%s", strings.Join(args, " "), out)
	if err != nil {
		t.Fatalf("npx %v: %v", args, err)
	}
}

// ---- the two Nginx containers ----------------------------------------------------------------

// startOzidNginx runs deploy/ozid-web as the image would: the entrypoint check, then the template.
func startOzidNginx(t *testing.T, root string, bins gwBinaries, g *gwRun, port int) *nginxApp {
	t.Helper()
	env := map[string]string{"API_ORIGIN": g.apiO}
	check := exec.Command("sh", filepath.Join(root, "deploy", "ozid-web", "19-require-api-origin.sh"))
	check.Env = append(os.Environ(), "API_ORIGIN="+g.apiO)
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("ozid-web entrypoint check rejects the scenario's API_ORIGIN: %v\n%s", err, out)
	}
	conf := renderTemplate(t, root, "deploy/ozid-web/default.conf.template", env, dockerEnvFilter(t, root, "deploy/ozid-web/Dockerfile"))
	conf = mustReplace(t, "ozid-web template", conf, "listen 80;", fmt.Sprintf("listen 127.0.0.1:%d;", port))
	conf = mustReplace(t, "ozid-web template", conf, "/usr/share/nginx/html", g.dist["ozid"])
	if strings.Contains(conf, "/etc/nginx") || strings.Contains(conf, "${") {
		t.Fatalf("the ozid-web template still has image paths or unsubstituted variables:\n%s", conf)
	}
	return startNginx(t, bins, "ozid-"+g.set.name, port, func(string) string { return conf })
}

// startAdminNginx runs deploy/admin-web as the image would: the entrypoint script writes the access rules
// from the environment (and refuses an empty or overlapping list), then the templates are rendered.
func startAdminNginx(t *testing.T, root string, bins gwBinaries, g *gwRun, port int) *nginxApp {
	t.Helper()
	env := map[string]string{
		"API_ORIGIN": g.apiO, "ADMIN_HOST": g.set.host(g.set.admin),
		"ADMIN_ALLOWED_CIDRS": gwInsideClient + "/32", "ADMIN_TRUSTED_PROXIES": gwGatewayAddress + "/32", "ADMIN_REAL_IP_HEADER": "X-Forwarded-For",
	}
	filter := dockerEnvFilter(t, root, "deploy/admin-web/Dockerfile")
	return startNginx(t, bins, "admin-"+g.set.name, port, func(dir string) string {
		confDir := filepath.Join(dir, "conf")
		if err := os.MkdirAll(filepath.Join(confDir, "conf.d", "inc"), 0o755); err != nil {
			t.Fatal(err)
		}
		script := exec.Command("sh", filepath.Join(root, "deploy", "admin-web", "19-admin-access.sh"))
		script.Env = append(os.Environ(), "ADMIN_CONF_DIR="+confDir)
		for k, v := range env {
			script.Env = append(script.Env, k+"="+v)
		}
		if out, err := script.CombinedOutput(); err != nil {
			t.Fatalf("admin-web entrypoint rejects the scenario's settings: %v\n%s", err, out)
		}
		headers := renderTemplate(t, root, "deploy/admin-web/templates/inc/headers.inc.template", env, filter)
		if err := os.WriteFile(filepath.Join(confDir, "conf.d", "inc", "headers.inc"), []byte(headers), 0o600); err != nil {
			t.Fatal(err)
		}
		conf := renderTemplate(t, root, "deploy/admin-web/templates/default.conf.template", env, filter)
		conf = mustReplace(t, "admin-web template", conf, "listen 80 default_server;", fmt.Sprintf("listen 127.0.0.1:%d default_server;", port))
		conf = mustReplace(t, "admin-web template", conf, "listen 80;", fmt.Sprintf("listen 127.0.0.1:%d;", port))
		conf = mustReplace(t, "admin-web template", conf, "/usr/share/nginx/html", g.dist["admin"])
		conf = mustReplace(t, "admin-web template", conf, "/etc/nginx/admin-access.conf", filepath.Join(confDir, "admin-access.conf"))
		conf = mustReplace(t, "admin-web template", conf, "/etc/nginx/conf.d/inc/", filepath.Join(confDir, "conf.d", "inc")+"/")
		if strings.Contains(conf, "/etc/nginx") || strings.Contains(conf, "${") {
			t.Fatalf("the admin-web template still has image paths or unsubstituted variables:\n%s", conf)
		}
		return conf
	})
}

// ---- helpers shared by the probes ---------------------------------------------------------------

// gwDo sends one request through the gateway instance on port from a source address.
func (g *gwRun) do(port int, src, method, rawURL string, body string, headers ...string) (int, http.Header, string) {
	g.t.Helper()
	req, err := http.NewRequest(method, rawURL, strings.NewReader(body))
	if err != nil {
		g.t.Fatal(err)
	}
	host := ""
	for i := 0; i+1 < len(headers); i += 2 {
		if strings.EqualFold(headers[i], "Host") {
			host = headers[i+1]
			continue
		}
		req.Header.Set(headers[i], headers[i+1])
	}
	if host != "" {
		req.Host = host
	}
	client := plainClient(src) // port 0: a bare port, no gateway
	if port != 0 {
		client = gwClient(src, port, "")
	}
	res, err := client.Do(req)
	if err != nil {
		g.t.Fatalf("%s %s from %s: %v", method, rawURL, src, err)
	}
	defer res.Body.Close()
	data := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		data.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return res.StatusCode, res.Header, data.String()
}

const connectJSON = "application/json"

func connectHeaders(origin string, extra ...string) []string {
	h := []string{"Content-Type", connectJSON, "Connect-Protocol-Version", "1"}
	if origin != "" {
		h = append(h, "Origin", origin)
	}
	return append(h, extra...)
}
