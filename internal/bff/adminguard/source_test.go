package adminguard

import (
	"net/http"
	"strings"
	"testing"
)

func source(t *testing.T, c SourceConfig) *Source {
	t.Helper()
	s, err := NewSource("mcp", c)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func admitted(s *Source, h http.Header, peer string) bool {
	_, ok := s.Allowed(h, peer)
	return ok
}

// The machine allowlist is the same machinery as the console's, so the same rules hold for it, and an
// error names the section it came from.
func TestSourceRefusesEverythingWithoutAnAllowlist(t *testing.T) {
	s := source(t, SourceConfig{})
	if s.Open() {
		t.Fatal("an empty allowlist reports open")
	}
	for _, peer := range []string{"127.0.0.1:1", "10.0.0.1:1", "[::1]:1", "203.0.113.9:1"} {
		if admitted(s, hdr(), peer) {
			t.Errorf("%s passed an empty allowlist", peer)
		}
	}
	var none *Source
	if none.Open() || admitted(none, hdr(), "10.0.0.1:1") {
		t.Fatal("a missing source check must refuse")
	}
}

func TestSourceValidationNamesItsSectionAndRefusesOpenNetworks(t *testing.T) {
	for _, bad := range []string{"0.0.0.0/0", "::/0", "0.0.0.0/1", "128.0.0.0/1", "10.0.0.0/7", "2000::/3", "10.0.0.1", "10.0.0.1/24", "not-a-cidr", "", "::ffff:10.0.0.0/104"} {
		err := SourceConfig{AllowedCIDRs: []string{bad}}.Validate("mcp")
		if err == nil || !strings.Contains(err.Error(), "mcp.allowed_cidrs") {
			t.Errorf("allowed_cidrs %q: %v", bad, err)
		}
		err = SourceConfig{AllowedCIDRs: []string{"10.0.0.0/8"}, TrustedProxies: []string{bad}}.Validate("mcp")
		if err == nil || !strings.Contains(err.Error(), "mcp.trusted_proxies") {
			t.Errorf("trusted_proxies %q: %v", bad, err)
		}
	}
	for _, good := range []string{"10.0.0.0/8", "192.0.2.0/24", "198.51.100.7/32", "2001:db8::/32", "2001:db8::1/128"} {
		if err := (SourceConfig{AllowedCIDRs: []string{good}}).Validate("mcp"); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
	if err := (SourceConfig{ForwardedHeader: "x-forwarded-for"}).Validate("mcp"); err == nil || !strings.Contains(err.Error(), "mcp.forwarded_header") {
		t.Errorf("a non-canonical header name: %v", err)
	}
	err := SourceConfig{AllowedCIDRs: []string{"10.0.0.0/8"}, TrustedProxies: []string{"10.2.0.0/16"}}.Validate("mcp")
	if err == nil || !strings.Contains(err.Error(), "mcp.trusted_proxies") || !strings.Contains(err.Error(), "mcp.allowed_cidrs") {
		t.Errorf("a proxy inside the allowlist: %v", err)
	}
	if _, err := NewSource("mcp", SourceConfig{AllowedCIDRs: []string{"0.0.0.0/0"}}); err == nil {
		t.Error("NewSource built a check from an open network")
	}
}

func TestSourceDirectPeerIsTheClientAndHeadersAreIgnored(t *testing.T) {
	s := source(t, SourceConfig{AllowedCIDRs: []string{"192.0.2.0/24", "2001:db8:1::/48"}})
	if ip, ok := s.Allowed(hdr(), "192.0.2.10:5000"); !ok || ip.String() != "192.0.2.10" {
		t.Fatalf("allowed peer: %v %v", ip, ok)
	}
	if !admitted(s, hdr(), "[2001:db8:1::7]:5000") {
		t.Error("an allowed IPv6 peer was refused")
	}
	for _, h := range []http.Header{
		hdr("X-Forwarded-For", "192.0.2.10"),
		hdr("X-Real-IP", "192.0.2.10", "Forwarded", "for=192.0.2.10", "X-Client-IP", "192.0.2.10", "True-Client-IP", "192.0.2.10"),
	} {
		if admitted(s, h, "203.0.113.9:5000") {
			t.Errorf("forged headers admitted an outside peer: %v", h)
		}
	}
	for _, peer := range []string{"[2001:db8:2::1]:5000", "garbage", "", "203.0.113.9:1"} {
		if admitted(s, hdr(), peer) {
			t.Errorf("%q passed", peer)
		}
	}
}

func TestSourceMatchesMappedAddressesAsIPv4(t *testing.T) {
	s := source(t, SourceConfig{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"10.0.0.0/8"}})
	if ip, ok := s.Allowed(hdr(), "[::ffff:192.0.2.5]:1"); !ok || ip.String() != "192.0.2.5" {
		t.Fatalf("mapped peer: %v %v", ip, ok)
	}
	if admitted(s, hdr(), "[::ffff:203.0.113.5]:1") {
		t.Fatal("mapped outsider passed")
	}
	if !admitted(s, hdr("X-Forwarded-For", "::ffff:192.0.2.7"), "[::ffff:10.0.0.2]:1") {
		t.Fatal("mapped proxy and mapped client were not matched as IPv4")
	}
	// A mapped address cannot be used to slip past a proxy list written for IPv4.
	if admitted(s, hdr("X-Forwarded-For", "192.0.2.7, ::ffff:203.0.113.9"), "10.0.0.2:1") {
		t.Fatal("a mapped outsider on the right of the chain was believed less than the left")
	}
}

func TestSourceTrustedProxyChain(t *testing.T) {
	s := source(t, SourceConfig{AllowedCIDRs: []string{"192.0.2.0/24", "2001:db8:1::/48"}, TrustedProxies: []string{"10.0.0.0/8", "fd00::/16"}})
	cases := []struct {
		name string
		peer string
		h    http.Header
		ok   bool
	}{
		{"client forwarded by a trusted proxy", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7"), true},
		{"IPv6 client forwarded by an IPv6 proxy", "[fd00::2]:1", hdr("X-Forwarded-For", "2001:db8:1::9"), true},
		{"two proxies", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7, 10.0.0.9"), true},
		{"split over two header lines", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7", "X-Forwarded-For", "10.0.0.9"), true},
		{"forged left entry is not believed", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7, 203.0.113.9"), false},
		{"forged left entry before the real client", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7, 203.0.113.9, 10.0.0.9"), false},
		{"forged left entry with the real client allowed", "10.0.0.2:1", hdr("X-Forwarded-For", "203.0.113.9, 192.0.2.7"), true},
		{"untrusted peer cannot use the header", "198.51.100.1:1", hdr("X-Forwarded-For", "192.0.2.7"), false},
		{"browser allowlist is not consulted: an outsider is an outsider", "10.0.0.2:1", hdr("X-Forwarded-For", "198.51.100.9"), false},
		{"unparsable hop", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7, nonsense"), false},
		{"unparsable hop between proxies", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7, nonsense, 10.0.0.9"), false},
		{"chain is a word", "10.0.0.2:1", hdr("X-Forwarded-For", "garbage"), false},
		{"word on a second header line", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7", "X-Forwarded-For", "garbage"), false},
		{"hop with a port", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7:80"), false},
		{"trailing empty hop", "10.0.0.2:1", hdr("X-Forwarded-For", "192.0.2.7,"), false},
		{"junk left of a valid last hop is never read", "10.0.0.2:1", hdr("X-Forwarded-For", "garbage, 192.0.2.7"), true},
		{"no header: the proxy itself is the client", "10.0.0.2:1", hdr(), false},
		{"only proxies in the chain", "10.0.0.2:1", hdr("X-Forwarded-For", "10.0.0.9"), false},
		{"other header names are not read", "10.0.0.2:1", hdr("X-Real-IP", "192.0.2.7", "Forwarded", "for=192.0.2.7"), false},
	}
	for _, c := range cases {
		if got := admitted(s, c.h, c.peer); got != c.ok {
			t.Errorf("%s: admitted=%v want %v", c.name, got, c.ok)
		}
	}
	custom := source(t, SourceConfig{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"10.0.0.0/8"}, ForwardedHeader: "X-Edge-Chain"})
	if !admitted(custom, hdr("X-Edge-Chain", "192.0.2.7"), "10.0.0.2:1") {
		t.Error("the configured header is not read")
	}
	if admitted(custom, hdr("X-Forwarded-For", "192.0.2.7"), "10.0.0.2:1") {
		t.Error("the default header is read although another is configured")
	}
}

// The console's guard and a machine check are built from different configuration and never share a list:
// the same address is admitted by one and refused by the other.
func TestTheTwoPerimetersAreIndependent(t *testing.T) {
	browser := guard(t, Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
	machine := source(t, SourceConfig{AllowedCIDRs: []string{"198.51.100.0/24"}})
	if browser.Check(hdr("Origin", origin), "198.51.100.5:1") == nil {
		t.Error("a machine address was admitted by the browser perimeter")
	}
	if admitted(machine, hdr(), "192.0.2.5:1") {
		t.Error("a browser address was admitted by the machine perimeter")
	}
	if browser.Check(hdr("Origin", origin), "192.0.2.5:1") != nil || !admitted(machine, hdr(), "198.51.100.5:1") {
		t.Error("each perimeter must admit its own list")
	}
}
