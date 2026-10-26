// Package adminguard is the perimeter of the unified admin console's API: it decides,
// before any session is looked at, whether a request may reach a management
// procedure at all. A request passes only when its client address is inside the
// configured allowlist AND its Origin is exactly the console's registered origin.
//
// The client address is the TCP peer, unless the peer is a configured trusted proxy;
// only then is the forwarded header read, from its right-hand end, skipping trusted
// proxies, so entries a client wrote itself (always on the left) are never believed.
// With no allowlist everything is refused: an unconfigured console is closed, not open.
package adminguard

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultForwardedHeader is read when the peer is a trusted proxy and none is configured.
const DefaultForwardedHeader = "X-Forwarded-For"

// Minimum prefix lengths. A network wider than these is not an office, a VPN or a
// proxy tier; it is "everyone", written in a way that slips past a check for /0.
const (
	minPrefixV4 = 8
	minPrefixV6 = 16
)

// ErrDenied is the one refusal: it never says which condition failed.
var ErrDenied = errors.New("request rejected")

// Config is the `admin` section of the BFF configuration.
type Config struct {
	// AllowedCIDRs are the networks the console may be used from. Empty refuses all.
	AllowedCIDRs []string `yaml:"allowed_cidrs"`
	// TrustedProxies are the networks of the reverse proxies that connect to the BFF
	// directly and are trusted to write ForwardedHeader. Empty means no proxy is
	// trusted: the TCP peer is the client and no header is read.
	TrustedProxies []string `yaml:"trusted_proxies"`
	// ForwardedHeader is the multi-hop header such a proxy appends to (default
	// X-Forwarded-For). The proxy must append to it, not pass a client's value through.
	ForwardedHeader string `yaml:"forwarded_header"`
	// RateLimitPerMinute bounds the calls of one operator. Default 120.
	RateLimitPerMinute int `yaml:"rate_limit_per_minute"`
	// WriteRateLimitPerMinute bounds the account changes of one operator. Default 30.
	WriteRateLimitPerMinute int `yaml:"write_rate_limit_per_minute"`
}

// WriteLimit returns the effective per-operator limit of account changes.
func (c Config) WriteLimit() int {
	if c.WriteRateLimitPerMinute <= 0 {
		return 30
	}
	return c.WriteRateLimitPerMinute
}

// Limit returns the effective per-operator rate limit.
func (c Config) Limit() int {
	if c.RateLimitPerMinute <= 0 {
		return 120
	}
	return c.RateLimitPerMinute
}

// Validate checks every network and the header name. It does not depend on the
// deployment mode: a fully open network is never a valid setting.
func (c Config) Validate() error {
	if err := c.Source().Validate("admin"); err != nil {
		return err
	}
	if c.RateLimitPerMinute < 0 {
		return errors.New("admin.rate_limit_per_minute must not be negative")
	}
	if c.WriteRateLimitPerMinute < 0 {
		return errors.New("admin.write_rate_limit_per_minute must not be negative")
	}
	return nil
}

// Source is the part of the configuration that decides where a request came from.
func (c Config) Source() SourceConfig {
	return SourceConfig{AllowedCIDRs: c.AllowedCIDRs, TrustedProxies: c.TrustedProxies, ForwardedHeader: c.ForwardedHeader}
}

// ParseCIDRs parses networks; "field" names the setting in the error. A bare
// address is refused (write /32 or /128 to say so), as is a wide network.
func ParseCIDRs(field string, in []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(in))
	for _, raw := range in {
		p, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a CIDR network (a single host is x.x.x.x/32)", field, raw)
		}
		if p.Addr().Is4In6() {
			return nil, fmt.Errorf("%s: %q is an IPv4-mapped IPv6 network; write the IPv4 network", field, raw)
		}
		if p != p.Masked() {
			return nil, fmt.Errorf("%s: %q has host bits set; write %s", field, raw, p.Masked())
		}
		min := minPrefixV6
		if p.Addr().Is4() {
			min = minPrefixV4
		}
		if p.Bits() < min {
			return nil, fmt.Errorf("%s: %q is too wide (at least /%d for %s): that is not an allowlist", field, raw, min, family(p.Addr()))
		}
		out = append(out, p)
	}
	return out, nil
}

func family(a netip.Addr) string {
	if a.Is4() {
		return "IPv4"
	}
	return "IPv6"
}

// Guard is the immutable perimeter.
type Guard struct {
	src    *Source
	origin string
}

// New builds the guard for the console served from origin (its registered
// navigation origin; empty means the console is not registered and everything is
// refused).
func New(c Config, origin string) (*Guard, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	src, err := NewSource("admin", c.Source())
	if err != nil {
		return nil, err
	}
	return &Guard{src: src, origin: origin}, nil
}

// Open reports whether anything can pass at all: an allowlist and a registered console.
func (g *Guard) Open() bool { return g != nil && g.src.Open() && g.origin != "" }

// Check admits the request or returns ErrDenied. Every condition is checked in
// every case, so the caller cannot learn from the time which one failed.
func (g *Guard) Check(h http.Header, remoteAddr string) error {
	if g == nil {
		return ErrDenied
	}
	ipOK := g.AllowedIP(h, remoteAddr)
	originOK := g.origin != "" && len(h.Values("Origin")) == 1 && h.Get("Origin") == g.origin
	if !ipOK || !originOK {
		return ErrDenied
	}
	return nil
}

// AllowedIP reports whether the resolved client address is inside the allowlist.
func (g *Guard) AllowedIP(h http.Header, remoteAddr string) bool {
	if g == nil {
		return false
	}
	_, ok := g.src.Allowed(h, remoteAddr)
	return ok
}

// ClientIP resolves the client address. ok is false when it cannot be determined
// (an unparsable peer, or an unparsable entry in the forwarded chain), which is a
// refusal and never a fallback to something a client could have chosen.
func (g *Guard) ClientIP(h http.Header, remoteAddr string) (netip.Addr, bool) {
	return g.src.ClientIP(h, remoteAddr)
}

func parseHostPort(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if i := strings.IndexByte(host, '%'); i >= 0 { // IPv6 zone
		host = host[:i]
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}
