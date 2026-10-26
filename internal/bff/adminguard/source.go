package adminguard

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// SourceConfig is the source-network part of a perimeter: which networks are admitted and which reverse
// proxies are trusted to report the client address. The browser console (`admin`) and the MCP machine
// endpoint (`mcp`) each have their own, built from their own configuration section; they share the rules
// (validation, proxy chain, address families) and nothing else, so one list never admits the other's
// callers.
type SourceConfig struct {
	// AllowedCIDRs are the admitted networks. Empty refuses all.
	AllowedCIDRs []string
	// TrustedProxies are the networks of the reverse proxies that connect to the BFF directly and are
	// trusted to write ForwardedHeader. Empty means no proxy is trusted: the TCP peer is the client and no
	// header is read.
	TrustedProxies []string
	// ForwardedHeader is the multi-hop header such a proxy appends to (default X-Forwarded-For).
	ForwardedHeader string
}

// Validate checks every network and the header name; section names the configuration section in the
// errors ("admin", "mcp"). A fully open network is never valid, and a trusted proxy must not sit inside
// the allowlist: a request without a usable forwarded chain (no header, or only proxies in it) is judged
// by the address of the proxy itself, so a proxy inside the allowlist would let everyone who can reach it
// through that proxy in.
func (c SourceConfig) Validate(section string) error {
	allowed, err := ParseCIDRs(section+".allowed_cidrs", c.AllowedCIDRs)
	if err != nil {
		return err
	}
	trusted, err := ParseCIDRs(section+".trusted_proxies", c.TrustedProxies)
	if err != nil {
		return err
	}
	for _, p := range trusted {
		for _, a := range allowed {
			if p.Overlaps(a) {
				return fmt.Errorf("%[1]s.trusted_proxies %[2]q overlaps %[1]s.allowed_cidrs %[3]q: a proxy inside the allowlist admits everyone who can reach it through that proxy; keep the two lists apart", section, p, a)
			}
		}
	}
	if h := c.ForwardedHeader; h != "" && http.CanonicalHeaderKey(h) != h {
		return fmt.Errorf("%s.forwarded_header %q must be a canonical header name such as %s", section, h, DefaultForwardedHeader)
	}
	return nil
}

// Source is an immutable source-network check: it resolves the client address of a request and says
// whether it is inside the allowlist. It knows nothing about origins, sessions or tokens.
//
// The client address is the TCP peer, unless the peer is a configured trusted proxy; only then is the
// forwarded header read, from its right-hand end, skipping trusted proxies, so entries a client wrote
// itself (always on the left) are never believed. IPv4-mapped addresses are matched as IPv4.
type Source struct {
	allowed []netip.Prefix
	trusted []netip.Prefix
	header  string
}

// NewSource validates c and builds the check for the configuration section named section.
func NewSource(section string, c SourceConfig) (*Source, error) {
	if err := c.Validate(section); err != nil {
		return nil, err
	}
	allowed, _ := ParseCIDRs(section+".allowed_cidrs", c.AllowedCIDRs)
	trusted, _ := ParseCIDRs(section+".trusted_proxies", c.TrustedProxies)
	header := c.ForwardedHeader
	if header == "" {
		header = DefaultForwardedHeader
	}
	return &Source{allowed: allowed, trusted: trusted, header: header}, nil
}

// Open reports whether anything can pass at all: there is an allowlist.
func (s *Source) Open() bool { return s != nil && len(s.allowed) > 0 }

// Allowed resolves the client address and reports whether it is inside the allowlist; the address is
// returned only when it is. A nil or empty Source admits nobody.
func (s *Source) Allowed(h http.Header, remoteAddr string) (netip.Addr, bool) {
	if !s.Open() {
		return netip.Addr{}, false
	}
	ip, ok := s.ClientIP(h, remoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	for _, p := range s.allowed {
		if p.Contains(ip) {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

// ClientIP resolves the client address. ok is false when it cannot be determined (an unparsable peer, or
// an unparsable entry in the forwarded chain), which is a refusal and never a fallback to something a
// client could have chosen.
func (s *Source) ClientIP(h http.Header, remoteAddr string) (netip.Addr, bool) {
	peer, ok := parseHostPort(remoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	if !s.isTrusted(peer) {
		return peer, true
	}
	// Every instance of the header counts, in order, as one chain.
	var hops []string
	for _, v := range h.Values(s.header) {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return netip.Addr{}, false
		}
		hop = hop.Unmap()
		if s.isTrusted(hop) {
			continue
		}
		return hop, true
	}
	// No header, or a chain made only of trusted proxies: the nearest peer is what we know.
	return peer, true
}

func (s *Source) isTrusted(a netip.Addr) bool {
	for _, p := range s.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
