package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// validateCookieDomain never derives trust from a request Host or proxy header.
func (c *Config) validateCookieDomain() error {
	domain := c.Auth.CookieDomain
	if domain == "" {
		return nil
	}
	invalid := func() error {
		return fmt.Errorf("auth.cookie_domain must be a lowercase registrable DNS domain, without a leading dot, wildcard, port or URL")
	}
	if len(domain) > 253 || domain != strings.ToLower(domain) || net.ParseIP(domain) != nil {
		return invalid()
	}
	for label := range strings.SplitSeq(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return invalid()
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return invalid()
			}
		}
	}
	suffix, _ := publicsuffix.PublicSuffix(domain)
	if domain == suffix {
		return invalid()
	}
	if !c.Auth.CookieSecure {
		return fmt.Errorf("auth.cookie_domain requires auth.cookie_secure=true")
	}
	if c.Navigation.APIOrigin == "" || len(c.Navigation.Apps) == 0 {
		return fmt.Errorf("auth.cookie_domain requires navigation.api_origin and registered applications")
	}
	check := func(field, origin string) error {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" || (u.Hostname() != domain && !strings.HasSuffix(u.Hostname(), "."+domain)) {
			return fmt.Errorf("auth.cookie_domain must cover the HTTPS host of %s", field)
		}
		return nil
	}
	if err := check("navigation.api_origin", c.Navigation.APIOrigin); err != nil {
		return err
	}
	for id, app := range c.Navigation.Apps {
		if err := check("navigation.apps."+id+".origin", app.Origin); err != nil {
			return err
		}
	}
	for _, origin := range c.CORSAllowedOrigins {
		if err := check("cors_allowed_origins", origin); err != nil {
			return err
		}
	}
	return nil
}
