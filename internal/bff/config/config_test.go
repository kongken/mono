package config

import (
	"strings"
	"testing"
	"time"

	"github.com/kongken/mono/internal/bff/adminguard"
	"github.com/kongken/mono/internal/bff/navigation"
)

func TestValidate(t *testing.T) {
	services := []string{"tripmap"}
	ok := func() Config {
		return Config{Upstreams: map[string]string{"tripmap": "http://tripmap:18081"}, CORSAllowedOrigins: []string{"https://trip.example.com"}}
	}
	if c := ok(); c.Validate(services) != nil {
		t.Fatal(c.Validate(services))
	}
	for name, mutate := range map[string]func(*Config){
		"missing upstream":    func(c *Config) { delete(c.Upstreams, "tripmap") },
		"unknown service":     func(c *Config) { c.Upstreams["tirpmap"] = "http://tripmap:18081" },
		"relative URL":        func(c *Config) { c.Upstreams["tripmap"] = "tripmap:18081" },
		"URL with path":       func(c *Config) { c.Upstreams["tripmap"] = "http://tripmap:18081/api" },
		"URL with query":      func(c *Config) { c.Upstreams["tripmap"] = "http://tripmap:18081?x=1" },
		"URL with password":   func(c *Config) { c.Upstreams["tripmap"] = "http://u:p@tripmap:18081" },
		"origin with a slash": func(c *Config) { c.CORSAllowedOrigins = []string{"https://trip.example.com/"} },
	} {
		c := ok()
		mutate(&c)
		if c.Validate(services) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateNavigation(t *testing.T) {
	services := []string{"tripmap"}
	ok := func() Config {
		return Config{
			Upstreams:          map[string]string{"tripmap": "http://tripmap:18081"},
			CORSAllowedOrigins: []string{"https://id.example.com", "https://trip.example.com"},
			Auth:               AuthConfig{CookieSecure: true},
			Navigation: navigation.Config{
				APIOrigin: "https://api.example.com",
				Apps: map[string]navigation.App{
					"ozid":     {Origin: "https://id.example.com"},
					"trip-map": {Origin: "https://trip.example.com"},
				},
			},
		}
	}
	if c := ok(); c.Validate(services) != nil {
		t.Fatal(c.Validate(services))
	}
	if c := (Config{Upstreams: ok().Upstreams}); c.Validate(services) != nil {
		t.Fatal("navigation is optional")
	}
	for name, mutate := range map[string]func(*Config){
		"app origin not in CORS": func(c *Config) { c.CORSAllowedOrigins = c.CORSAllowedOrigins[:1] },
		"no ozid":                func(c *Config) { delete(c.Navigation.Apps, "ozid") },
		"no api origin":          func(c *Config) { c.Navigation.APIOrigin = "" },
		"cross-site app": func(c *Config) {
			c.Navigation.Apps["trip-map"] = navigation.App{Origin: "https://trip.other.test"}
			c.CORSAllowedOrigins = append(c.CORSAllowedOrigins, "https://trip.other.test")
		},
		"http with secure cookies": func(c *Config) { c.Navigation.APIOrigin = "http://api.example.com" },
	} {
		c := ok()
		mutate(&c)
		if c.Validate(services) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateInternal(t *testing.T) {
	ok := func() Config {
		return Config{
			InternalAuth: InternalAuth{Issuer: "bff", KID: "k1", PrivateKeyFile: "bff.key"},
			GRPC:         GRPCConfig{CAFile: "ca.pem", Services: map[string]GRPCService{"tripmap": {Address: "tripmap:18083"}}},
		}
	}
	if c := ok(); c.ValidateInternal([]string{"tripmap"}) != nil {
		t.Fatal(c.ValidateInternal([]string{"tripmap"}))
	}
	inline := ok()
	inline.InternalAuth.PrivateKeyFile = ""
	inline.InternalAuth.PrivateKey = "PEM content is checked at dial"
	if err := inline.ValidateInternal([]string{"tripmap"}); err != nil {
		t.Fatal(err)
	}
	if c := (Config{}); c.ValidateInternal(nil) != nil {
		t.Fatal("a route table without gRPC services needs no internal settings")
	}
	for name, mutate := range map[string]func(*Config){
		"no issuer":        func(c *Config) { c.InternalAuth.Issuer = "" },
		"no kid":           func(c *Config) { c.InternalAuth.KID = "" },
		"no private key":   func(c *Config) { c.InternalAuth.PrivateKeyFile = "" },
		"both key sources": func(c *Config) { c.InternalAuth.PrivateKey = "inline" },
		"no CA":            func(c *Config) { c.GRPC.CAFile = "" },
		"no address":       func(c *Config) { c.GRPC.Services["tripmap"] = GRPCService{} },
		"unknown service":  func(c *Config) { c.GRPC.Services["tirpmap"] = GRPCService{Address: "x:1"} },
		"negative limit":   func(c *Config) { c.GRPC.MaxMessageBytes = -1 },
	} {
		c := ok()
		mutate(&c)
		if c.ValidateInternal([]string{"tripmap"}) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateInternalTransports(t *testing.T) {
	cfg := func(ca string, services map[string]GRPCService) Config {
		return Config{
			InternalAuth: InternalAuth{Issuer: "bff", KID: "k1", PrivateKeyFile: "bff.key"},
			GRPC:         GRPCConfig{CAFile: ca, Services: services},
		}
	}
	two := []string{"tripmap", "billing"}
	for name, c := range map[string]Config{
		"explicit tls":         cfg("ca.pem", map[string]GRPCService{"tripmap": {Address: "a:1", Transport: "tls"}, "billing": {Address: "b:1"}}),
		"all h2c needs no CA":  cfg("", map[string]GRPCService{"tripmap": {Address: "a:1", Transport: "h2c"}, "billing": {Address: "b:1", Transport: "h2c"}}),
		"all h2c ignores a CA": cfg("ca.pem", map[string]GRPCService{"tripmap": {Address: "a:1", Transport: "h2c"}, "billing": {Address: "b:1", Transport: "h2c"}}),
		"mixed with a CA":      cfg("ca.pem", map[string]GRPCService{"tripmap": {Address: "a:1", Transport: "h2c"}, "billing": {Address: "b:1", ServerName: "billing.internal"}}),
	} {
		if err := c.ValidateInternal(two); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, c := range map[string]Config{
		"unknown transport":       cfg("ca.pem", map[string]GRPCService{"tripmap": {Address: "a:1", Transport: "insecure"}, "billing": {Address: "b:1"}}),
		"transport is exact":      cfg("ca.pem", map[string]GRPCService{"tripmap": {Address: "a:1", Transport: "H2C"}, "billing": {Address: "b:1"}}),
		"mixed needs the CA":      cfg("", map[string]GRPCService{"tripmap": {Address: "a:1", Transport: "h2c"}, "billing": {Address: "b:1"}}),
		"unset transport is tls":  cfg("", map[string]GRPCService{"tripmap": {Address: "a:1"}, "billing": {Address: "b:1", Transport: "h2c"}}),
		"h2c with a server name":  cfg("ca.pem", map[string]GRPCService{"tripmap": {Address: "a:1", Transport: "h2c", ServerName: "x"}, "billing": {Address: "b:1"}}),
		"h2c without an address":  cfg("", map[string]GRPCService{"tripmap": {Transport: "h2c"}, "billing": {Address: "b:1", Transport: "h2c"}}),
		"h2c typo in service key": cfg("", map[string]GRPCService{"tripmap": {Address: "a:1", Transport: "h2c"}, "billing": {Address: "b:1", Transport: "h2c"}, "tirpmap": {Address: "c:1", Transport: "h2c"}}),
	} {
		if c.ValidateInternal(two) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAuthAdminSessionAndAccountCleanup(t *testing.T) {
	var a AuthConfig
	if a.AdminSessionLimit() != DefaultAdminSessionMaxAge || a.AdminSessionLimit().Hours() != 12 {
		t.Fatalf("default admin session limit: %v", a.AdminSessionLimit())
	}
	a.AdminSessionMaxAge = time.Hour
	if a.AdminSessionLimit() != time.Hour {
		t.Fatalf("configured admin session limit: %v", a.AdminSessionLimit())
	}
	if got := (&Config{}).CleanupProducts(); len(got) != 1 || got[0] != "tripmap" {
		t.Fatalf("default cleanup products: %v", got)
	}

	services := []string{"tripmap", "metro"}
	base := func() Config {
		return Config{Upstreams: map[string]string{"tripmap": "http://tripmap:1", "metro": "http://metro:1"}}
	}
	ok := base()
	ok.Auth.AccountCleanup = []string{"tripmap", "metro"}
	if err := ok.Validate(services); err != nil {
		t.Fatal(err)
	}
	// By default every deployed user-data product is cleaned; the list may not leave one out.
	deployed := base()
	if got := deployed.CleanupProducts(); len(got) != 2 || got[0] != "tripmap" || got[1] != "metro" {
		t.Fatalf("default cleanup products with metro deployed: %v", got)
	}
	if err := deployed.Validate(services); err != nil {
		t.Fatal(err)
	}
	leftOut := base()
	leftOut.Auth.AccountCleanup = []string{"tripmap"}
	if err := leftOut.Validate(services); err == nil || !strings.Contains(err.Error(), "metro") {
		t.Fatalf("a deployed metro left out of auth.account_cleanup was accepted: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"unknown product":    func(c *Config) { c.Auth.AccountCleanup = []string{"tripmap", "railmap"} },
		"duplicate product":  func(c *Config) { c.Auth.AccountCleanup = []string{"metro", "metro"} },
		"negative admin age": func(c *Config) { c.Auth.AdminSessionMaxAge = -time.Hour },
		"not in route table": func(c *Config) { c.Auth.AccountCleanup = []string{"metro"} },
	} {
		c := base()
		mutate(&c)
		if name == "not in route table" {
			if err := c.Validate([]string{"tripmap"}); err == nil {
				t.Errorf("%s: accepted", name)
			}
			continue
		}
		if c.Validate(services) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateAdminPerimeter(t *testing.T) {
	services := []string{"tripmap"}
	ok := func() Config {
		return Config{
			Upstreams:          map[string]string{"tripmap": "http://tripmap:18081"},
			CORSAllowedOrigins: []string{"https://id.example.com", "https://admin.example.com"},
			Auth:               AuthConfig{CookieSecure: true},
			Navigation: navigation.Config{
				APIOrigin: "https://api.example.com",
				Apps: map[string]navigation.App{
					"ozid":  {Origin: "https://id.example.com"},
					"admin": {Origin: "https://admin.example.com"},
				},
			},
			Admin: adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"10.0.0.0/8"}},
		}
	}
	if c := ok(); c.Validate(services) != nil {
		t.Fatal(c.Validate(services))
	}
	// Leaving the section out is valid and closes the console.
	if c := ok(); true {
		c.Admin = adminguard.Config{}
		if c.Validate(services) != nil {
			t.Fatal("an empty admin section must be valid")
		}
	}
	for name, mutate := range map[string]func(*Config){
		"open to everyone":                       func(c *Config) { c.Admin.AllowedCIDRs = []string{"0.0.0.0/0"} },
		"open to everyone (v6)":                  func(c *Config) { c.Admin.AllowedCIDRs = []string{"::/0"} },
		"open in two halves":                     func(c *Config) { c.Admin.AllowedCIDRs = []string{"0.0.0.0/1", "128.0.0.0/1"} },
		"every proxy trusted":                    func(c *Config) { c.Admin.TrustedProxies = []string{"0.0.0.0/0"} },
		"not a network":                          func(c *Config) { c.Admin.AllowedCIDRs = []string{"office"} },
		"allowlist without a registered console": func(c *Config) { delete(c.Navigation.Apps, "admin") },
	} {
		c := ok()
		mutate(&c)
		if c.Validate(services) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The machine perimeter (mcp.*) is validated by the same rules and is its own section.
	if c := ok(); true {
		c.MCP = MCPConfig{AllowedCIDRs: []string{"198.51.100.0/24"}, TrustedProxies: []string{"10.0.0.0/8"}}
		if err := c.Validate(services); err != nil {
			t.Fatal(err)
		}
		// Leaving it out is valid: the endpoint is then closed, not open.
		c.MCP = MCPConfig{}
		if err := c.Validate(services); err != nil {
			t.Fatal(err)
		}
	}
	for name, mutate := range map[string]func(*Config){
		"mcp open to everyone":      func(c *Config) { c.MCP.AllowedCIDRs = []string{"0.0.0.0/0"} },
		"mcp open to everyone (v6)": func(c *Config) { c.MCP.AllowedCIDRs = []string{"::/0"} },
		"mcp open in two halves":    func(c *Config) { c.MCP.AllowedCIDRs = []string{"0.0.0.0/1", "128.0.0.0/1"} },
		"mcp mapped network":        func(c *Config) { c.MCP.AllowedCIDRs = []string{"::ffff:198.51.100.0/120"} },
		"mcp host bits":             func(c *Config) { c.MCP.AllowedCIDRs = []string{"198.51.100.7/24"} },
		"mcp every proxy trusted": func(c *Config) {
			c.MCP.AllowedCIDRs = []string{"198.51.100.0/24"}
			c.MCP.TrustedProxies = []string{"0.0.0.0/0"}
		},
		"mcp not a network": func(c *Config) { c.MCP.AllowedCIDRs = []string{"ci-runner"} },
		"mcp proxy inside the list": func(c *Config) {
			c.MCP.AllowedCIDRs = []string{"10.0.0.0/8"}
			c.MCP.TrustedProxies = []string{"10.1.0.0/16"}
		},
		"mcp lower-case header": func(c *Config) { c.MCP.ForwardedHeader = "x-forwarded-for" },
		"mcp negative rate":     func(c *Config) { c.MCP.RateLimitPerMinute = -1 },
	} {
		c := ok()
		mutate(&c)
		if err := c.Validate(services); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.Contains(err.Error(), "mcp.") {
			t.Errorf("%s: the error does not name the mcp section: %v", name, err)
		}
	}
}

// The mcp section is validated whether or not metro is configured: without upstreams.metro the endpoint is
// never mounted, but a malformed list is still a startup failure rather than a silently ignored setting.
func TestValidateMCPSourceIgnoresWhetherMetroIsConfigured(t *testing.T) {
	services := []string{"tripmap"}
	c := Config{
		Upstreams:          map[string]string{"tripmap": "http://tripmap:18081"},
		CORSAllowedOrigins: []string{"https://trip.example.com"},
	}
	if _, ok := c.Upstreams["metro"]; ok {
		t.Fatal("the premise of this test is a configuration without a metro upstream")
	}
	if err := c.Validate(services); err != nil {
		t.Fatalf("no metro and no mcp section must be valid: %v", err)
	}
	c.MCP.AllowedCIDRs = []string{"0.0.0.0/0"}
	err := c.Validate(services)
	if err == nil || !strings.Contains(err.Error(), "mcp.allowed_cidrs") {
		t.Fatalf("an open mcp allowlist without metro must still fail, naming the section: %v", err)
	}
}
