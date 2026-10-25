package adminguard

import (
	"net/http"
	"strings"
	"testing"
)

const origin = "https://admin.example.com"

func guard(t *testing.T, c Config) *Guard {
	t.Helper()
	g, err := New(c, origin)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestEmptyAllowlistRefusesEverything(t *testing.T) {
	g := guard(t, Config{})
	if g.Open() {
		t.Fatal("an empty allowlist reports open")
	}
	for _, addr := range []string{"127.0.0.1:1", "10.0.0.1:1", "[::1]:1", "203.0.113.9:1"} {
		if g.Check(hdr("Origin", origin), addr) == nil {
			t.Errorf("%s passed an empty allowlist", addr)
		}
	}
	var nilGuard *Guard
	if nilGuard.Check(hdr("Origin", origin), "10.0.0.1:1") == nil {
		t.Fatal("a missing guard must refuse")
	}
}

func TestInvalidOrOpenNetworksAreRejected(t *testing.T) {
	for _, bad := range []string{"0.0.0.0/0", "::/0", "0.0.0.0/1", "128.0.0.0/1", "10.0.0.0/7", "2000::/3", "10.0.0.1", "10.0.0.1/24", "not-a-cidr", "", "::ffff:10.0.0.0/104"} {
		if err := (Config{AllowedCIDRs: []string{bad}}).Validate(); err == nil {
			t.Errorf("allowed_cidrs %q accepted", bad)
		}
		if err := (Config{AllowedCIDRs: []string{"10.0.0.0/8"}, TrustedProxies: []string{bad}}).Validate(); err == nil {
			t.Errorf("trusted_proxies %q accepted", bad)
		}
	}
	for _, good := range []string{"10.0.0.0/8", "192.0.2.0/24", "198.51.100.7/32", "2001:db8::/32", "2001:db8::1/128"} {
		if err := (Config{AllowedCIDRs: []string{good}}).Validate(); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
	if err := (Config{ForwardedHeader: "x-forwarded-for"}).Validate(); err == nil {
		t.Error("a non-canonical header name was accepted")
	}
	if err := (Config{RateLimitPerMinute: -1}).Validate(); err == nil {
		t.Error("a negative rate limit was accepted")
	}
}

func TestDirectPeerIsTheClientAndHeadersAreIgnored(t *testing.T) {
	g := guard(t, Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
	if err := g.Check(hdr("Origin", origin), "192.0.2.10:5000"); err != nil {
		t.Fatalf("allowed peer: %v", err)
	}
	// No trusted proxy: a client cannot talk its way in with any header.
	for _, h := range []http.Header{
		hdr("Origin", origin, "X-Forwarded-For", "192.0.2.10"),
		hdr("Origin", origin, "X-Real-IP", "192.0.2.10", "Forwarded", "for=192.0.2.10", "X-Client-IP", "192.0.2.10"),
	} {
		if g.Check(h, "203.0.113.9:5000") == nil {
			t.Errorf("forged headers admitted an outside peer: %v", h)
		}
	}
	if g.Check(hdr("Origin", origin), "[2001:db8::1]:5000") == nil {
		t.Error("an IPv6 peer outside the list passed")
	}
	if g.Check(hdr("Origin", origin), "garbage") == nil {
		t.Error("an unparsable peer passed")
	}
}

func TestIPv4MappedPeerMatchesTheIPv4Network(t *testing.T) {
	g := guard(t, Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
	if err := g.Check(hdr("Origin", origin), "[::ffff:192.0.2.5]:1"); err != nil {
		t.Fatalf("mapped peer: %v", err)
	}
	if g.Check(hdr("Origin", origin), "[::ffff:203.0.113.5]:1") == nil {
		t.Fatal("mapped outsider passed")
	}
}

func TestTrustedProxyChain(t *testing.T) {
	g := guard(t, Config{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"10.0.0.0/8"}})
	cases := []struct {
		name string
		peer string
		h    http.Header
		ok   bool
	}{
		{"client forwarded by a trusted proxy", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7"), true},
		{"two proxies", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7, 10.0.0.9"), true},
		{"split over two header lines", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7", "X-Forwarded-For", "10.0.0.9"), true},
		{"forged left entry is not believed", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7, 203.0.113.9"), false},
		{"forged left entry before the real client", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7, 203.0.113.9, 10.0.0.9"), false},
		{"forged left entry with the real client allowed", "10.0.0.2:1", hdr("X-Forwarded-For", "203.0.113.9, 192.0.2.7"), true},
		{"untrusted peer cannot use the header", "198.51.100.1:1", hdr("X-Forwarded-For", "192.0.2.7"), false},
		{"unparsable hop", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7, nonsense"), false},
		{"unparsable hop between proxies", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7, nonsense, 10.0.0.9"), false},
		{"no header: the proxy itself is the client", "10.0.0.2:1", hdr(), false},
		{"only proxies in the chain", "10.0.0.2:1", hdr("X-Forwarded-For", "10.0.0.9"), false},
		{"mapped IPv6 hop", "10.0.0.2:1", hdr("X-Forwarded-For", "::ffff:192.0.2.7"), true},
		{"other header names are not read", "10.0.0.2:1", hdr("X-Real-IP", "192.0.2.7"), false},
	}
	for _, c := range cases {
		h := c.h.Clone()
		h.Set("Origin", origin)
		if got := g.Check(h, c.peer) == nil; got != c.ok {
			t.Errorf("%s: admitted=%v want %v", c.name, got, c.ok)
		}
	}
	custom := guard(t, Config{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"10.0.0.0/8"}, ForwardedHeader: "X-Edge-Chain"})
	if custom.Check(hdr("Origin", origin, "X-Edge-Chain", "192.0.2.7"), "10.0.0.2:1") != nil {
		t.Error("the configured header is not read")
	}
	if custom.Check(hdr("Origin", origin, "X-Forwarded-For", "192.0.2.7"), "10.0.0.2:1") == nil {
		t.Error("the default header is read although another is configured")
	}
}

func TestOriginMustBeExactlyTheConsole(t *testing.T) {
	g := guard(t, Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
	peer := "192.0.2.1:1"
	for name, h := range map[string]http.Header{
		"missing":          hdr(),
		"another origin":   hdr("Origin", "https://app.example.com"),
		"null":             hdr("Origin", "null"),
		"other port":       hdr("Origin", "https://admin.example.com:8443"),
		"other scheme":     hdr("Origin", "http://admin.example.com"),
		"suffix":           hdr("Origin", origin+".evil.example"),
		"prefix":           hdr("Origin", "https://evil-admin.example.com"),
		"trailing slash":   hdr("Origin", origin+"/"),
		"upper case":       hdr("Origin", strings.ToUpper(origin)),
		"two values":       hdr("Origin", origin, "Origin", origin),
		"referer only":     hdr("Referer", origin+"/"),
		"host header only": hdr("Host", "admin.example.com"),
	} {
		if g.Check(h, peer) == nil {
			t.Errorf("%s: admitted", name)
		}
	}
	if err := g.Check(hdr("Origin", origin), peer); err != nil {
		t.Fatalf("the console's own origin: %v", err)
	}
	unregistered, _ := New(Config{AllowedCIDRs: []string{"192.0.2.0/24"}}, "")
	if unregistered.Open() || unregistered.Check(hdr("Origin", ""), peer) == nil || unregistered.Check(hdr(), peer) == nil {
		t.Fatal("a guard without a registered console must refuse")
	}
}
