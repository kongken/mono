package config

import (
	"testing"
	"time"

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
	if got := a.CleanupProducts(); len(got) != 1 || got[0] != "tripmap" {
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
