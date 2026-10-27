package e2e_test

import (
	"io/fs"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These scans are the T12 "no old entry is left" and "no real deployment data in public files" checks.
// They read the repository's own tracked files (no network, no service), so they run in `go test` of
// every CI run and fail the build when an old account form, a proxy door or a real-looking domain,
// address or credential comes back. A scan that finds nothing is only meaningful if it can find
// something: each one has a negative case on a sample that must be caught.

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// trackedFiles lists the repository's files without generated and third-party trees. It prefers
// `git ls-files` (so an untracked scratch file is not judged) and falls back to walking the tree.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		for _, f := range strings.Split(string(out), "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
	} else {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "dist") {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				rel, _ := filepath.Rel(root, p)
				files = append(files, rel)
			}
			return nil
		})
	}
	var out []string
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			continue // listed by git but deleted in the working tree
		}
		switch {
		case strings.HasPrefix(f, "node_modules/"), strings.Contains(f, "/node_modules/"), strings.HasPrefix(f, "pkg/proto/"),
			strings.HasPrefix(f, "packages/api-client/"), f == "package-lock.json", strings.HasSuffix(f, ".sum"):
			continue
		}
		out = append(out, f)
	}
	return out
}

func readText(t *testing.T, root, rel string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range raw[:min(len(raw), 4096)] {
		if b == 0 {
			return "", false // binary
		}
	}
	return string(raw), true
}

func isTestFile(f string) bool {
	return strings.Contains(f, ".test.") || strings.Contains(f, ".spec.") || strings.Contains(f, "/tests/") || strings.HasSuffix(f, "_test.go")
}

// ---- old account forms, old management UI, leftover proxy login entries ----------------------

var (
	passwordInput = regexp.MustCompile(`type\s*=\s*["{'][^"}']*password|autoComplete\s*=\s*["'](current|new)-password`)
	// The account procedures as the browser clients name them. The products and the console may only use
	// the ones that are open to every registered application (me, issueCSRFToken, resolveNavigation, logout).
	accountStepCall = regexp.MustCompile(`\b(?:auth|account|accountApi|authClient|users)\s*\.\s*(loginWithEmail|requestRegistration|completeRegistration|requestPasswordReset|resetPassword|reauthenticate|changePassword|revokeSession|revokeOtherSessions|deleteAccount|retryAccountDeletion|updateMyProfile|listSessions|getAccountDeletion)\b`)
	retiredLogin    = regexp.MustCompile(`(?i)logto|/auth/login|/auth/callback|oauth_apps|oauth_providers`)
)

func findPasswordInput(src string) bool { return passwordInput.MatchString(src) }
func findAccountCall(src string) bool   { return accountStepCall.MatchString(src) }

func TestNoOldAccountFormsOrManagementUIInProducts(t *testing.T) {
	root := repoRoot(t)
	// The scans can see what they are for.
	if !findPasswordInput(`<input type="password" />`) || !findPasswordInput(`autoComplete="new-password"`) {
		t.Fatal("the password-input scan does not match a password field")
	}
	if !findAccountCall(`await auth.changePassword({})`) || findAccountCall(`await auth.me({})`) {
		t.Fatal("the account-call scan is wrong")
	}

	// The only code that may render a password field or call an account step is the shared account package
	// and OzID itself. The products' old mail landing pages only hand the link over (MailHandOff): they never
	// mount the page that completes it.
	for _, f := range trackedFiles(t, root) {
		if !strings.HasPrefix(f, "apps/trip-map/") && !strings.HasPrefix(f, "apps/metro/") && !strings.HasPrefix(f, "apps/admin/") {
			continue
		}
		if isTestFile(f) || !strings.Contains(f, "/src/") {
			continue
		}
		src, ok := readText(t, root, f)
		if !ok {
			continue
		}
		if findPasswordInput(src) {
			t.Errorf("%s renders a password field: sign-in, registration, password change are OzID's only", f)
		}
		if (strings.Contains(src, "AccountLanding") || strings.Contains(src, "account/landing")) && strings.HasPrefix(f, "apps/") && !strings.HasPrefix(f, "apps/ozid/") {
			t.Errorf("%s mounts the landing that completes a mail link: a product only hands the link over to the user center", f)
		}
		if findAccountCall(src) {
			t.Errorf("%s calls an account step; a product or the console may only read Me, get a CSRF token, resolve navigation and sign out", f)
		}
	}

	// The metro catalogue administration lives in the console: no admin pages, routes or generated
	// administration client in the product's source.
	for _, dir := range []string{"apps/metro/src/pages/admin", "apps/metro/src/components/admin"} {
		if _, err := os.Stat(filepath.Join(root, dir)); err == nil {
			t.Errorf("%s still exists: the catalogue administration moved to apps/admin", dir)
		}
	}
	for _, f := range trackedFiles(t, root) {
		if !strings.HasPrefix(f, "apps/metro/src/") || isTestFile(f) {
			continue
		}
		src, ok := readText(t, root, f)
		if !ok {
			continue
		}
		if strings.Contains(src, "AdminService") && !strings.HasSuffix(f, "lib/errors.ts") {
			// lib/errors.ts only imports the shared ValidationDetail message type.
			t.Errorf("%s uses the metro administration service: that belongs to the console", f)
		}
	}
}

func TestNoRetiredLoginEntryRemains(t *testing.T) {
	root := repoRoot(t)
	if !retiredLogin.MatchString("location /auth/login {") {
		t.Fatal("the retired-entry scan does not match")
	}
	// Files that may name the retired entries: the 404 stubs, the tests that prove they are gone, the docs
	// and the one-off SQL that retires old identities.
	allowed := func(f string) bool {
		return strings.HasPrefix(f, "docs/") || isTestFile(f) || strings.HasPrefix(f, "deploy/sql/") ||
			strings.HasPrefix(f, "migrations/") || strings.HasPrefix(f, "scripts/") || f == "deploy/reverse-proxy/Caddyfile.example" ||
			f == "AGENTS.md" || f == "GLOSSARY.md" || f == "README.md" || strings.HasPrefix(f, ".github/") ||
			strings.HasPrefix(f, "internal/") || strings.HasPrefix(f, "cmd/") || f == "config.bff.example.yaml"
	}
	for _, f := range trackedFiles(t, root) {
		if allowed(f) || !(strings.HasPrefix(f, "apps/") || strings.HasPrefix(f, "packages/") || strings.HasPrefix(f, "deploy/") || f == "compose.yaml") {
			continue
		}
		src, ok := readText(t, root, f)
		if !ok {
			continue
		}
		for n, line := range strings.Split(src, "\n") {
			if !retiredLogin.MatchString(line) {
				continue
			}
			// The nginx stubs answer the retired paths with 404 so the single-page fallback cannot; a stub is fine.
			if strings.Contains(line, "return 404") || strings.Contains(line, "respond 404") {
				continue
			}
			// A cleanup of a browser-storage key a previous version wrote.
			if strings.Contains(line, "removeItem") {
				continue
			}
			t.Errorf("%s:%d names a retired login entry: %s", f, n+1, strings.TrimSpace(line))
		}
	}
}

// The product images (and the example gateway) may serve only the product's own anonymous public paths
// from their own origin: nothing of the BFF's session, account or management API. A generic /api proxy
// would be a second, unguarded door.
func TestProductGatewaysDoNotProxyTheBrowserAPI(t *testing.T) {
	root := repoRoot(t)
	allowedLocations := map[string]bool{
		"^~ /api/share/": true, "^~ /api/notifications/": true, "^~ /api/tiles/": true, "^~ /api/": true,
	}
	location := regexp.MustCompile(`^\s*location\s+(.+?)\s*\{`)
	for _, f := range []string{"deploy/trip-map-web/default.conf.template", "deploy/metro-web/default.conf.template", "deploy/ozid-web/default.conf.template"} {
		src, _ := readText(t, root, f)
		if src == "" {
			t.Fatalf("%s is empty or missing", f)
		}
		for n, line := range strings.Split(src, "\n") {
			m := location.FindStringSubmatch(line)
			if m == nil || !strings.Contains(m[1], "api") {
				continue
			}
			if !allowedLocations[m[1]] {
				t.Errorf("%s:%d proxies or matches %q: only the public share, unsubscribe and tile paths (and a 404 for the rest of /api/) may mention /api here", f, n+1, m[1])
			}
		}
		if strings.Contains(f, "ozid-web") && strings.Contains(src, "proxy_pass") {
			t.Errorf("%s proxies: OzID is static and calls the API origin directly", f)
		}
		// The catch-all /api/ must answer 404, not forward.
		if i := strings.Index(src, "location ^~ /api/ {"); i >= 0 {
			if !strings.Contains(src[i:min(len(src), i+40)], "return 404") {
				t.Errorf("%s: `location ^~ /api/` must return 404", f)
			}
		}
	}
	// The same rule in the Caddy example: under the product hosts `handle /api/*` answers 404.
	caddy, _ := readText(t, root, "deploy/reverse-proxy/Caddyfile.example")
	for _, block := range strings.Split(caddy, "\n}\n") {
		isProduct := strings.Contains(block, "root * /srv/www") || strings.Contains(block, "root * /srv/metro")
		if !isProduct {
			continue
		}
		i := strings.Index(block, "handle /api/* {")
		if i < 0 {
			continue
		}
		if !strings.Contains(block[i:min(len(block), i+60)], "respond 404") {
			t.Errorf("a product host of the Caddy example forwards /api/*: it must answer 404")
		}
	}
}

// ---- public files carry reserved names only --------------------------------------------------

var (
	urlHost = regexp.MustCompile(`(?i)\b(?:https?|wss?)://(?:[^/\s@"'<>]+@)?([a-z0-9][a-z0-9.-]*[a-z0-9]|\[[0-9a-f:]+\])`)
	ipv4    = regexp.MustCompile(`\b(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\b`)
	// A private key is a header followed by a real body; a `<your base64-encoded key>` placeholder is not one.
	privateKey = regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----[\s#]*[A-Za-z0-9+/]{40,}`)
	credential = regexp.MustCompile(`AKIA[0-9A-Z]{16}|sk_live_[0-9A-Za-z]{10,}|xox[abprs]-[0-9A-Za-z-]{10,}|gh[pousr]_[A-Za-z0-9]{30,}|SG\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{30,}`)
)

// reservedHost accepts the names and addresses documentation may use: RFC 2606 / 6761 reserved names,
// loopback, internal service names with no dot, and the third-party documentation and data sources the
// repository really depends on (an addition here is a reviewed decision, not a convenience).
func reservedHost(h string) bool {
	h = strings.ToLower(strings.Trim(h, "[]"))
	switch {
	case h == "localhost", strings.HasSuffix(h, ".localhost"), !strings.Contains(h, "."):
		return true
	case h == "example.com" || h == "example.net" || h == "example.org", strings.HasSuffix(h, ".example.com"),
		strings.HasSuffix(h, ".example.net"), strings.HasSuffix(h, ".example.org"), strings.HasSuffix(h, ".example"),
		strings.HasSuffix(h, ".test"), strings.HasSuffix(h, ".invalid"), strings.HasSuffix(h, ".internal"):
		return true
	}
	if a, err := netip.ParseAddr(h); err == nil {
		return reservedAddr(a)
	}
	return thirdParty[h]
}

func reservedAddr(a netip.Addr) bool {
	for _, p := range []string{
		"127.0.0.0/8", "0.0.0.0/32", "::1/128", "::/128",
		"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32",
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "fe80::/10",
	} {
		if netip.MustParsePrefix(p).Contains(a) {
			return true
		}
	}
	return false
}

// thirdParty are real, public services named in documentation or build files: code hosts, standards,
// vendor documentation, map data and push services. None is a deployment of this project.
var thirdParty = map[string]bool{
	"github.com": true, "raw.githubusercontent.com": true, "ghcr.io": true, "registry.npmjs.org": true,
	"docs.github.com": true,
	"api.mapbox.com": true, "www.mapbox.com": true, "apps.mapbox.com": true, "docs.mapbox.com": true,
	"www.openstreetmap.org": true, "tile.openstreetmap.org": true, "operations.osmfoundation.org": true,
	"geo.datav.aliyun.com": true, "datav.aliyun.com": true, "www.naturalearthdata.com": true,
	"www.rfc-editor.org": true, "www.w3.org": true, "vite.dev": true, "scripts.sil.org": true,
	"www.twilio.com": true, "documentation.mailgun.com": true, "developers.smtp2go.com": true,
	"docs.rustfs.com": true, "docs.aws.amazon.com": true, "www.adobe.com": true,
	"www.instagram.com": true, "weibo.com": true, "www.facebook.com": true, "x.com": true,
	"fcm.googleapis.com": true, "updates.push.services.mozilla.com": true,
	"caddyserver.com": true, "nginx.org": true, "playwright.dev": true,
}

// publicFile is the set the T12 rule covers: documentation, examples, deployment files, scripts and workflows.
func publicFile(f string) bool {
	switch {
	case strings.HasPrefix(f, "docs/"), strings.HasPrefix(f, "deploy/"), strings.HasPrefix(f, "scripts/"), strings.HasPrefix(f, ".github/"):
		return true
	}
	return f == "README.md" || f == "GLOSSARY.md" || f == "AGENTS.md" || f == "compose.yaml" || f == "Makefile" ||
		f == ".env.example" || (strings.HasPrefix(f, "config.") && strings.HasSuffix(f, ".example.yaml"))
}

func TestPublicFilesUseOnlyReservedNamesAndNoCredentials(t *testing.T) {
	root := repoRoot(t)
	// The scan can see what it is for.
	if reservedHost("shop.real-company.com") || reservedAddr(netip.MustParseAddr("8.8.8.8")) ||
		!reservedHost("id.example.com") || !reservedHost("203.0.113.9") || !reservedHost("ozid-e2e.test") {
		t.Fatal("the reserved-name check is wrong")
	}
	if !privateKey.MatchString("-----BEGIN PRIVATE KEY-----\n"+strings.Repeat("A", 64)) || privateKey.MatchString("-----BEGIN PRIVATE KEY-----\n#   <your base64-encoded key>") ||
		!credential.MatchString("AKIAABCDEFGHIJKLMNOP") {
		t.Fatal("the credential scan does not match")
	}

	for _, f := range trackedFiles(t, root) {
		if !publicFile(f) || f == "internal/e2e/legacy_entries_test.go" {
			continue
		}
		src, ok := readText(t, root, f)
		if !ok {
			continue
		}
		if privateKey.MatchString(src) {
			t.Errorf("%s contains a private key", f)
		}
		for n, line := range strings.Split(src, "\n") {
			for _, m := range urlHost.FindAllStringSubmatch(line, -1) {
				if !reservedHost(m[1]) {
					t.Errorf("%s:%d names %q: use a reserved example name (example.com, *.test, 203.0.113.0/24, ...) or a configuration placeholder", f, n+1, m[1])
				}
			}
			for _, m := range ipv4.FindAllStringSubmatch(line, -1) {
				a, err := netip.ParseAddr(m[0])
				if err != nil {
					continue // not an address (a version number with a leading zero, an OID)
				}
				if !reservedAddr(a) {
					t.Errorf("%s:%d names the address %s: use a documentation range (192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24)", f, n+1, m[0])
				}
			}
			if credential.MatchString(line) {
				t.Errorf("%s:%d looks like a credential", f, n+1)
			}
		}
	}
}
