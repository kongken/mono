package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.orx.me/apps/trip-map/internal/platform/httpx"
)

// AppConfig shares the YAML file with Butterfly's store.db/redis/s3 definitions.
type AppConfig struct {
	DBStore     string        `yaml:"db_store"`
	RedisStore  string        `yaml:"redis_store"`
	AutoMigrate bool          `yaml:"auto_migrate"`
	Storage     StorageConfig `yaml:"storage"`
	Auth        AuthConfig    `yaml:"auth"`
	Share       ShareConfig   `yaml:"share"`
	// Notification configures F12 reminders; every channel is off until configured.
	Notification NotificationConfig `yaml:"notification"`
}

// ShareConfig controls the public share page (F6). Sharing is off while
// PublicBaseURL is empty: no owner method works and every public route answers 404.
type ShareConfig struct {
	// PublicBaseURL is the site address visitors use, such as https://trip.example.com.
	// Share links are PublicBaseURL + "/s/<token>", so the address must route /s/ and
	// /api/share/ to this backend. It is used instead of the request's Host header.
	PublicBaseURL string `yaml:"public_base_url"`
	// AppIndexURL is the front end's index.html (for example https://trip.example.com/
	// or http://127.0.0.1:5173/ in development). The backend fetches it to serve /s/<token>
	// with Open Graph tags. It must be trusted configuration, must not lead back to /s/ or
	// /api/, and is optional: without it the page is a minimal fallback.
	AppIndexURL string `yaml:"app_index_url"`
	// HTMLCSP overrides the Content-Security-Policy of the share HTML. Empty uses a
	// strict same-origin policy; Vite's development server needs a looser one.
	HTMLCSP string `yaml:"html_csp"`
	// Per-client-address request limits per minute; zero keeps the defaults.
	PageRatePerMinute  int `yaml:"page_rate_per_minute"`
	ImageRatePerMinute int `yaml:"image_rate_per_minute"`
	MissRatePerMinute  int `yaml:"miss_rate_per_minute"`
	PhotoRatePerMinute int `yaml:"photo_rate_per_minute"`
}

// Enabled reports whether public sharing is configured.
func (s ShareConfig) Enabled() bool { return s.PublicBaseURL != "" }

// BaseURL is PublicBaseURL without a trailing slash.
func (s ShareConfig) BaseURL() string { return strings.TrimRight(s.PublicBaseURL, "/") }

func shareURL(name, raw string, allowPath bool) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "#") || strings.Contains(raw, "?") {
		return fmt.Errorf("%s: %q must be an http(s) URL without credentials, query or fragment", name, raw)
	}
	if !allowPath && strings.Trim(u.Path, "/") != "" {
		return fmt.Errorf("%s: %q must not contain a path", name, raw)
	}
	return nil
}

func (s ShareConfig) validate() error {
	for name, v := range map[string]int{"page_rate_per_minute": s.PageRatePerMinute, "image_rate_per_minute": s.ImageRatePerMinute, "miss_rate_per_minute": s.MissRatePerMinute, "photo_rate_per_minute": s.PhotoRatePerMinute} {
		if v < 0 {
			return fmt.Errorf("share.%s must not be negative", name)
		}
	}
	if s.PublicBaseURL == "" {
		if s.AppIndexURL != "" {
			return fmt.Errorf("share.app_index_url requires share.public_base_url")
		}
		return nil
	}
	if err := shareURL("share.public_base_url", s.PublicBaseURL, true); err != nil {
		return err
	}
	if s.AppIndexURL == "" {
		return nil
	}
	if err := shareURL("share.app_index_url", s.AppIndexURL, true); err != nil {
		return err
	}
	// The fetch must never come back to the routes it feeds.
	u, _ := url.Parse(s.AppIndexURL)
	path := "/" + strings.TrimLeft(u.Path, "/")
	for _, forbidden := range []string{"/s/", "/api/"} {
		if strings.Contains(path+"/", forbidden) {
			return fmt.Errorf("share.app_index_url must not point into %s", forbidden)
		}
	}
	return nil
}

type StorageConfig struct {
	S3Store   string `yaml:"s3_store"`
	KeyPrefix string `yaml:"key_prefix"`
}

// AuthConfig controls accounts, sessions, rate limits and cross-origin access.
type AuthConfig struct {
	// SessionTTL is the bearer session lifetime. Defaults to 7 days.
	SessionTTL time.Duration `yaml:"session_ttl"`
	// RegistrationDisabled closes sign-up for both password and OAuth accounts.
	// Registration is open by default.
	RegistrationDisabled bool `yaml:"registration_disabled"`
	// ClientIPHeader names a header set by a trusted reverse proxy (for example
	// X-Forwarded-For or CF-Connecting-IP) holding the client address. Leave
	// empty to use the TCP peer address; never set it when clients can reach the
	// service directly, because they could spoof it and dodge rate limits.
	ClientIPHeader string `yaml:"client_ip_header"`
	// CORSAllowedOrigins lists exact browser origins allowed to call the API
	// from another site, for example https://trip.example.com. Same-origin
	// deployments behind the front-end proxy need none.
	CORSAllowedOrigins []string                 `yaml:"cors_allowed_origins"`
	OAuthProviders     map[string]OAuthProvider `yaml:"oauth_providers"`
}

// OAuthProvider configures one sign-in provider. It is offered only when both
// ClientID and ClientSecret are set. RedirectURL must be the frontend callback
// page registered with the OAuth app.
type OAuthProvider struct {
	// IssuerURL is the OIDC issuer (for Logto, https://<domain>/oidc). Required
	// by the "logto" provider, whose endpoints are derived from it.
	IssuerURL    string   `yaml:"issuer_url"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	RedirectURL  string   `yaml:"redirect_url"`
	Scopes       []string `yaml:"scopes"`
	DisplayName  string   `yaml:"display_name"`
}

func (p OAuthProvider) Enabled() bool { return p.ClientID != "" && p.ClientSecret != "" }

const DefaultSessionTTL = 7 * 24 * time.Hour

func (c *AppConfig) Print() {}
func (c *AppConfig) Validate() error {
	if c.DBStore == "" || c.RedisStore == "" || c.Storage.S3Store == "" {
		return fmt.Errorf("db_store, redis_store and storage.s3_store are required")
	}
	if c.Auth.SessionTTL < 0 {
		return fmt.Errorf("auth.session_ttl must not be negative")
	}
	if err := httpx.CheckOrigins("auth.cors_allowed_origins", c.Auth.CORSAllowedOrigins); err != nil {
		return err
	}
	if err := c.Share.validate(); err != nil {
		return err
	}
	if err := c.Notification.validate(); err != nil {
		return err
	}
	for name, p := range c.Auth.OAuthProviders {
		if p.Enabled() && p.RedirectURL == "" {
			return fmt.Errorf("auth.oauth_providers.%s.redirect_url is required", name)
		}
		if name == "logto" && p.Enabled() {
			if p.IssuerURL == "" {
				return fmt.Errorf("auth.oauth_providers.logto.issuer_url is required")
			}
			if err := shareURL("auth.oauth_providers.logto.issuer_url", p.IssuerURL, true); err != nil {
				return err
			}
			if u, err := url.Parse(p.RedirectURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Fragment != "" || strings.Contains(p.RedirectURL, "#") {
				return fmt.Errorf("auth.oauth_providers.logto.redirect_url: %q must be an http(s) URL without fragment", p.RedirectURL)
			}
		}
	}
	return nil
}

// SessionLifetime returns the configured or default session lifetime.
func (c *AppConfig) SessionLifetime() time.Duration {
	if c.Auth.SessionTTL > 0 {
		return c.Auth.SessionTTL
	}
	return DefaultSessionTTL
}
