package config

import "testing"

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
	if c := (Config{}); c.ValidateInternal(nil) != nil {
		t.Fatal("a route table without gRPC services needs no internal settings")
	}
	for name, mutate := range map[string]func(*Config){
		"no issuer":       func(c *Config) { c.InternalAuth.Issuer = "" },
		"no kid":          func(c *Config) { c.InternalAuth.KID = "" },
		"no private key":  func(c *Config) { c.InternalAuth.PrivateKeyFile = "" },
		"no CA":           func(c *Config) { c.GRPC.CAFile = "" },
		"no address":      func(c *Config) { c.GRPC.Services["tripmap"] = GRPCService{} },
		"unknown service": func(c *Config) { c.GRPC.Services["tirpmap"] = GRPCService{Address: "x:1"} },
		"negative limit":  func(c *Config) { c.GRPC.MaxMessageBytes = -1 },
	} {
		c := ok()
		mutate(&c)
		if c.ValidateInternal([]string{"tripmap"}) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
