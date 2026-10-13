package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// AppConfig shares the YAML file with Butterfly's store.db/redis/s3 definitions.
type AppConfig struct {
	DBStore     string        `yaml:"db_store"`
	RedisStore  string        `yaml:"redis_store"`
	AutoMigrate bool          `yaml:"auto_migrate"`
	Storage     StorageConfig `yaml:"storage"`
	Auth        AuthConfig    `yaml:"auth"`
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
	for _, origin := range c.Auth.CORSAllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || strings.HasSuffix(origin, "/") {
			return fmt.Errorf("auth.cors_allowed_origins: %q must be an origin like https://example.com", origin)
		}
	}
	for name, p := range c.Auth.OAuthProviders {
		if p.Enabled() && p.RedirectURL == "" {
			return fmt.Errorf("auth.oauth_providers.%s.redirect_url is required", name)
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
