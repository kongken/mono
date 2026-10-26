package config

import (
	"strings"
	"testing"

	"github.com/kongken/mono/internal/bff/navigation"
)

func sharedDomainConfig() *Config {
	return &Config{
		Auth: AuthConfig{CookieSecure: true, CookieDomain: "example.com"},
		Navigation: navigation.Config{APIOrigin: "https://api.example.com", Apps: map[string]navigation.App{
			"ozid": {Origin: "https://id.example.com"}, "trip-map": {Origin: "https://trip.example.com"},
		}},
		CORSAllowedOrigins: []string{"https://id.example.com", "https://trip.example.com"},
	}
}

func TestCookieDomainValidation(t *testing.T) {
	if err := sharedDomainConfig().validateCookieDomain(); err != nil {
		t.Fatal(err)
	}
	if err := new(Config).validateCookieDomain(); err != nil {
		t.Fatalf("host-only default must remain valid: %v", err)
	}
	for _, domain := range []string{"*.example.com", ".example.com", "Example.com", " example.com", "example.com ", "https://example.com", "example.com:443", "127.0.0.1", "::1", "com", "co.uk", "github.io", "localhost", "example..com", "-bad.example.com", "bad-.example.com", "bad_example.com", "example.com/", strings.Repeat("a", 64) + ".com"} {
		t.Run(domain, func(t *testing.T) {
			c := sharedDomainConfig()
			c.Auth.CookieDomain = domain
			if err := c.validateCookieDomain(); err == nil {
				t.Fatal("invalid cookie domain accepted")
			}
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*Config)
	}{
		{"insecure", func(c *Config) { c.Auth.CookieSecure = false }},
		{"missing registry", func(c *Config) { c.Navigation = navigation.Config{} }},
		{"wrong API suffix", func(c *Config) { c.Navigation.APIOrigin = "https://notexample.com" }},
		{"domain too narrow", func(c *Config) { c.Auth.CookieDomain = "api.example.com" }},
		{"uncovered app", func(c *Config) { c.Navigation.Apps["ozid"] = navigation.App{Origin: "https://id.example.org"} }},
		{"uncovered CORS", func(c *Config) { c.CORSAllowedOrigins = []string{"https://other.example.org"} }},
		{"plain HTTP app", func(c *Config) { c.Navigation.Apps["ozid"] = navigation.App{Origin: "http://id.example.com"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := sharedDomainConfig()
			tc.edit(c)
			if err := c.validateCookieDomain(); err == nil {
				t.Fatal("unsafe cookie domain configuration accepted")
			}
		})
	}
}

func TestConfigValidateChecksCookieDomain(t *testing.T) {
	c := sharedDomainConfig()
	c.Auth.CookieDomain = "*.example.com"
	if err := c.Validate(nil); err == nil || !strings.Contains(err.Error(), "auth.cookie_domain") {
		t.Fatalf("Validate did not reject cookie_domain: %v", err)
	}
}
