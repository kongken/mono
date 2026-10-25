package e2e_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The pieces of the controlled gateway scenario (TestGatewayDomainSets): real processes of the two
// gateways the repository ships configuration for, started from that configuration.
//
//   - Caddy, from deploy/reverse-proxy/Caddyfile.example: TLS, the source allowlist of the console host,
//     the header rules of the mail landing pages, the product hosts' static files, the 404 for the
//     retired login paths, the MCP route.
//   - Nginx, from the container templates of deploy/ozid-web and deploy/admin-web, rendered with the
//     image's own rules (the Dockerfile's NGINX_ENVSUBST_FILTER, the 19-*.sh entrypoint scripts).
//
// Both are adapted by text substitution only: host names, the upstream addresses, the file roots, the
// listen ports (a test cannot use 443/80 or a Docker network), and the one thing a single machine
// cannot have, a gateway address apart from the client's (Caddy dials its upstreams from 127.0.0.2, the
// client is 127.0.0.1). Every substitution fails the test when its target is not in the shipped file,
// so a change of the example cannot silently stop being what is tested. What a single host cannot show
// (a real public address, DNS, a publicly trusted certificate, Docker networking, the real CDN) is
// listed in docs/ozid/acceptance.md.

// gwSet is one set of host names: the five front ends of a deployment on one reserved site.
type gwSet struct {
	name                        string
	site                        string
	id, api, trip, metro, admin string // labels
}

func (s gwSet) host(label string) string   { return label + "." + s.site }
func (s gwSet) origin(label string) string { return "https://" + s.host(label) }

// The two sets share nothing: a different site, and different labels, so that no part of the code,
// the examples or the configuration can depend on "id", "api" or the first set's names.
var gatewaySets = []gwSet{
	{"set-a", "alpha-e2e.test", "id", "api", "trip", "metro", "admin"},
	{"set-b", "bravo-e2e.test", "accounts", "edge", "footprints", "rails", "ops"},
}

// The addresses of one machine standing in for the networks of a deployment (all loopback, all reserved).
const (
	gwInsideClient   = "127.0.0.1" // a browser on the operator's allowed network
	gwGatewayAddress = "127.0.0.2" // the gateway as its upstreams see it (a trusted proxy, outside every allowlist)
	gwMachineClient  = "127.0.0.4" // a machine client on the MCP allowlist
	gwOutsideClient  = "127.0.0.9" // anyone else
)

type gwBinaries struct{ nginx, caddy, mime string }

// gatewayBinaries finds the two gateways. With GATEWAY_E2E=1 a missing one is a failure, never a skip.
func gatewayBinaries(t *testing.T) gwBinaries {
	t.Helper()
	find := func(env, name string) string {
		if p := os.Getenv(env); p != "" {
			return p
		}
		p, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("%s not found: set %s or put %s on PATH (the gateway scenario runs the real gateway and is never skipped)", name, env, name)
		}
		return p
	}
	b := gwBinaries{nginx: find("GATEWAY_NGINX", "nginx"), caddy: find("GATEWAY_CADDY", "caddy")}
	candidates := []string{os.Getenv("GATEWAY_NGINX_MIME"), filepath.Join(filepath.Dir(b.nginx), "..", "conf", "mime.types"), "/etc/nginx/mime.types"}
	for _, c := range candidates {
		if c != "" {
			if _, err := os.Stat(c); err == nil {
				b.mime = c
				break
			}
		}
	}
	if b.mime == "" {
		t.Fatal("nginx's mime.types not found: set GATEWAY_NGINX_MIME")
	}
	return b
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// waitPort waits for a TCP listener, failing with the process log when it does not come up.
func waitPort(t *testing.T, port int, what string, logFile string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	logs, _ := os.ReadFile(logFile)
	t.Fatalf("%s did not listen on %d:\n%s", what, port, logs)
}

// startProcess starts a child that is stopped (only that child) when the test ends.
func startProcess(t *testing.T, name string, dir string, env []string, bin string, args ...string) string {
	t.Helper()
	logPath := filepath.Join(dir, name+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
		if t.Failed() {
			if logs, err := os.ReadFile(logPath); err == nil {
				t.Logf("---- %s ----\n%s", name, tail(string(logs), 4000))
			}
		}
	})
	return logPath
}

func tail(s string, n int) string {
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// ---- Nginx (the container templates) ----------------------------------------------------------

// dockerEnvFilter reads NGINX_ENVSUBST_FILTER from a Dockerfile: the image's own template step
// substitutes the variables whose names match it (a regular expression), and only those.
func dockerEnvFilter(t *testing.T, root, dockerfile string) *regexp.Regexp {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, dockerfile))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`NGINX_ENVSUBST_FILTER=(\S+)`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("%s sets no NGINX_ENVSUBST_FILTER", dockerfile)
	}
	return regexp.MustCompile(m[1])
}

// renderTemplate does what the image's 20-envsubst-on-templates.sh does: envsubst over the variables
// that match the filter, with the real envsubst.
func renderTemplate(t *testing.T, root, rel string, env map[string]string, filter *regexp.Regexp) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var environ []string
	for k, v := range env {
		environ = append(environ, k+"="+v)
		if filter.MatchString(k) {
			names = append(names, "${"+k+"}")
		}
	}
	cmd := exec.Command("envsubst", strings.Join(names, " "))
	cmd.Env = append(os.Environ(), environ...)
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("envsubst %s: %v", rel, err)
	}
	return string(out)
}

// mustReplace replaces every occurrence of old and fails when there is none: the shipped file no
// longer has what the scenario adapts.
func mustReplace(t *testing.T, what, s, old, new string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("%s: %q is not in the shipped file any more; update the scenario with the change", what, old)
	}
	return strings.ReplaceAll(s, old, new)
}

type nginxApp struct {
	port int
	dir  string
}

// startNginx runs one real Nginx with the server blocks that servers returns for its private directory
// (an entrypoint script may write its own files there first), listening on a private port.
func startNginx(t *testing.T, bins gwBinaries, name string, port int, servers func(dir string) string) *nginxApp {
	t.Helper()
	dir := t.TempDir()
	blocks := servers(dir)
	conf := fmt.Sprintf(`daemon off;
worker_processes 1;
pid %[1]s/nginx.pid;
error_log %[1]s/error.log info;
events { worker_connections 128; }
http {
    include %[2]s;
    default_type application/octet-stream;
    access_log off;
    client_body_temp_path %[1]s/body;
    proxy_temp_path %[1]s/proxy;
    fastcgi_temp_path %[1]s/fcgi;
    uwsgi_temp_path %[1]s/uwsgi;
    scgi_temp_path %[1]s/scgi;
%[3]s
}
`, dir, bins.mime, blocks)
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil { // nginx opens its default log before it reads the file
		t.Fatal(err)
	}
	confPath := filepath.Join(dir, "nginx.conf")
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := startProcess(t, "nginx-"+name, dir, nil, bins.nginx, "-p", dir, "-c", confPath)
	waitPort(t, port, "nginx "+name, filepath.Join(dir, "error.log"))
	_ = logPath
	return &nginxApp{port: port, dir: dir}
}

// ---- Caddy (the shipped example) --------------------------------------------------------------

type caddyMode int

const (
	// caddyAllowlist: the console host admits the test's inside client only (the shipped rule with the
	// scenario's network in place of the documentation range).
	caddyAllowlist caddyMode = iota
	// caddyAsShipped: the example's own documentation range: nothing real is allowed, so a console
	// that nobody configured is closed.
	caddyAsShipped
	// caddyNoCheck: the gateway's own console rule removed, as if its operator forgot it. The console
	// container and the BFF must still refuse everyone outside their lists.
	caddyNoCheck
)

// gwUpstreams are where the gateway's upstream names go in the scenario.
type gwUpstreams struct {
	bff, ozid, admin int
	trip, metro      string // dist directories
}

// renderCaddyfile adapts deploy/reverse-proxy/Caddyfile.example for the scenario.
func renderCaddyfile(t *testing.T, root string, set gwSet, up gwUpstreams, mode caddyMode, httpsPort, httpPort int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "reverse-proxy", "Caddyfile.example"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	// The five site blocks of the example, by their addresses.
	sites := map[string]string{
		"example.com":       set.host(set.trip),
		"metro.example.com": set.host(set.metro),
		"api.example.com":   set.host(set.api),
		"id.example.com":    set.host(set.id),
		"admin.example.com": set.host(set.admin),
	}
	site := regexp.MustCompile(`(?m)^(\S+) \{$`)
	found := map[string]bool{}
	src = site.ReplaceAllStringFunc(src, func(line string) string {
		addr := strings.TrimSuffix(line, " {")
		if to, ok := sites[addr]; ok {
			found[addr] = true
			return to + " {"
		}
		return line
	})
	for addr := range sites {
		if !found[addr] {
			t.Fatalf("Caddyfile.example has no site block for %s any more; update the scenario", addr)
		}
	}

	// The upstreams: the BFF, and the two static containers. Each gets its own dial address, so the
	// upstream (BFF, Nginx) sees the gateway at gwGatewayAddress and not at the client's address.
	upstream := regexp.MustCompile(`(?m)^(\s*)reverse_proxy (bff:18081|ozid-web:80|admin-web:80)( \{)?$`)
	n := 0
	src = upstream.ReplaceAllStringFunc(src, func(line string) string {
		m := upstream.FindStringSubmatch(line)
		indent, target, open := m[1], m[2], m[3] != ""
		port := map[string]int{"bff:18081": up.bff, "ozid-web:80": up.ozid, "admin-web:80": up.admin}[target]
		n++
		out := fmt.Sprintf("%sreverse_proxy 127.0.0.1:%d {\n%s\ttransport http {\n%s\t\tlocal_address %s\n%s\t}", indent, port, indent, indent, gwGatewayAddress, indent)
		if !open {
			out += "\n" + indent + "}"
		}
		return out
	})
	if n < 6 {
		t.Fatalf("Caddyfile.example has %d upstream routes, expected at least 6; update the scenario", n)
	}
	src = mustReplace(t, "Caddyfile.example", src, "root * /srv/www", "root * "+up.trip)
	src = mustReplace(t, "Caddyfile.example", src, "root * /srv/metro", "root * "+up.metro)

	// The console's own source rule.
	const rule = "@outside not remote_ip 203.0.113.0/24"
	switch mode {
	case caddyAllowlist:
		src = mustReplace(t, "Caddyfile.example", src, rule, "@outside not remote_ip "+gwInsideClient+"/32")
	case caddyAsShipped:
		if !strings.Contains(src, rule) {
			t.Fatal("Caddyfile.example has no console source rule any more")
		}
	case caddyNoCheck:
		src = mustReplace(t, "Caddyfile.example", src, rule+"\n\trespond @outside 403\n", "")
	}

	global := fmt.Sprintf(`{
	admin off
	http_port %d
	https_port %d
	auto_https disable_redirects
	local_certs
	skip_install_trust
}

`, httpPort, httpsPort)
	return global + src
}

// startCaddy runs one real Caddy with the rendered example, on its own HTTPS port.
func startCaddy(t *testing.T, bins gwBinaries, caddyfile string, httpsPort int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(path, []byte(caddyfile), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + dir, "XDG_DATA_HOME=" + filepath.Join(dir, "data"), "XDG_CONFIG_HOME=" + filepath.Join(dir, "cfg")}
	if out, err := exec.Command(bins.caddy, "validate", "--config", path, "--adapter", "caddyfile").CombinedOutput(); err != nil {
		t.Fatalf("the adapted Caddyfile is not valid: %v\n%s\n%s", err, out, caddyfile)
	}
	logPath := startProcess(t, "caddy", dir, env, bins.caddy, "run", "--config", path, "--adapter", "caddyfile")
	waitPort(t, httpsPort, "caddy", logPath)
}

// ---- clients ----------------------------------------------------------------------------------

// gwClient is a client of one gateway instance as seen from one source address: every host name goes
// to the instance's port (what the browser's resolver rule does), TLS is verified against nothing
// (Caddy's internal CA), and the TLS server name is the URL's host unless sni says otherwise.
func gwClient(src string, port int, sni string) *http.Client {
	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(src)}, Timeout: 5 * time.Second}
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", port))
			},
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, ServerName: sni}, //nolint:gosec // the scenario's own internal CA
			DisableKeepAlives: true,
		},
	}
}

// plainClient dials a plain-HTTP listener directly (a bare port) from a source address.
func plainClient(src string) *http.Client {
	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(src)}, Timeout: 5 * time.Second}
	return &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true},
	}
}
