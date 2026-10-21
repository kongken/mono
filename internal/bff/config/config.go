// Package config is the BFF configuration. It shares the YAML file with
// Butterfly's log settings.
package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/httpx"
)

type Config struct {
	// Upstreams maps every business service in the route table to its internal
	// base URL, for example tripmap: http://tripmap:18081.
	Upstreams map[string]string `yaml:"upstreams"`
	// ClientIPHeader names a header set by a trusted reverse proxy in front of
	// the BFF (X-Real-IP from the front image's Nginx, or CF-Connecting-IP)
	// holding the client address. Leave empty when clients reach the BFF
	// directly, or they could choose their own address and dodge rate limits.
	ClientIPHeader string `yaml:"client_ip_header"`
	// CORSAllowedOrigins lists exact browser origins allowed to call the API
	// from another site. Same-origin deployments behind the front-end proxy
	// need none.
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
	// InternalAuth is how the BFF signs the credential every internal gRPC call
	// carries. The private key never leaves the BFF.
	InternalAuth InternalAuth `yaml:"internal_auth"`
	// GRPC says where each business service's gRPC listener is and which
	// certificate authority vouches for it.
	GRPC GRPCConfig `yaml:"grpc"`
	DBStore        string `yaml:"db_store"`
	RedisStore     string `yaml:"redis_store"`
	KeyPrefix      string `yaml:"key_prefix"`
	AutoMigrate    bool   `yaml:"auto_migrate"`
	Auth           AuthConfig `yaml:"auth"`
}

// AuthConfig controls browser sessions and registration.
type AuthConfig struct {
	SessionTTL           time.Duration `yaml:"session_ttl"`
	RegistrationDisabled bool          `yaml:"registration_disabled"`
	CookieSecure         bool          `yaml:"cookie_secure"`
	CSRFTokenTTL         time.Duration `yaml:"csrf_token_ttl"`
	OAuthProviders       map[string]OAuthProvider `yaml:"oauth_providers"`
}

// OAuthProvider configures one sign-in provider. It is offered only when both
// ClientID and ClientSecret are set. RedirectURL must be the frontend callback
// page registered with the OAuth app.
type OAuthProvider struct {
	IssuerURL    string   `yaml:"issuer_url"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	RedirectURL  string   `yaml:"redirect_url"`
	Scopes       []string `yaml:"scopes"`
	DisplayName  string   `yaml:"display_name"`
}

func (p OAuthProvider) Enabled() bool { return p.ClientID != "" && p.ClientSecret != "" }

func (a *AuthConfig) validateOAuth() error {
	for name, p := range a.OAuthProviders {
		if p.Enabled() && p.RedirectURL == "" {
			return fmt.Errorf("auth.oauth_providers.%s.redirect_url is required", name)
		}
		if name == "logto" && p.Enabled() {
			if p.IssuerURL == "" {
				return fmt.Errorf("auth.oauth_providers.logto.issuer_url is required")
			}
			u, err := url.Parse(p.IssuerURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
				return fmt.Errorf("auth.oauth_providers.logto.issuer_url: %q must be an http(s) URL without credentials or fragment", p.IssuerURL)
			}
			if u, err := url.Parse(p.RedirectURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Fragment != "" {
				return fmt.Errorf("auth.oauth_providers.logto.redirect_url: %q must be an http(s) URL without fragment", p.RedirectURL)
			}
		}
	}
	return nil
}

type InternalAuth struct {
	// Issuer is the iss claim; services must be configured with the same value.
	Issuer string `yaml:"issuer"`
	// KID names the signing key; services look the public key up by it. Rotating
	// means adding the new public key to every service first, then changing
	// kid and private_key_file here.
	KID string `yaml:"kid"`
	// PrivateKeyFile is an Ed25519 key in PKCS#8 PEM form.
	PrivateKeyFile string `yaml:"private_key_file"`
}

type GRPCConfig struct {
	// CAFile holds the PEM certificate authorities trusted for the certificates
	// of tls services. It is required when any service uses tls and is not read
	// when every service uses h2c. There is no insecure fallback.
	CAFile string `yaml:"ca_file"`
	// Services maps a business service name to its gRPC endpoint.
	Services map[string]GRPCService `yaml:"services"`
	// MaxMessageBytes caps one internal request or response; default 20 MiB.
	MaxMessageBytes int `yaml:"max_message_bytes"`
}

type GRPCService struct {
	// Address is host:port of the gRPC listener.
	Address string `yaml:"address"`
	// Transport is "tls" (default) or "h2c" and must match the service's own
	// grpc.transport. h2c is unencrypted HTTP/2 for an explicitly approved
	// trusted internal network; it is never chosen automatically.
	Transport string `yaml:"transport"`
	// ServerName overrides the name checked against the certificate of a tls
	// service; by default it is the host of Address. Not allowed with h2c.
	ServerName string `yaml:"server_name"`
}

// ValidateInternal checks the internal transport settings of the services that
// are called over gRPC: each needs an endpoint, and signing and trust must be
// configured.
func (c *Config) ValidateInternal(grpcServices []string) error {
	if len(grpcServices) == 0 {
		return nil
	}
	if c.InternalAuth.Issuer == "" || c.InternalAuth.KID == "" || c.InternalAuth.PrivateKeyFile == "" {
		return fmt.Errorf("internal_auth requires issuer, kid and private_key_file")
	}
	if c.GRPC.MaxMessageBytes < 0 {
		return fmt.Errorf("grpc.max_message_bytes must not be negative")
	}
	needsCA := false
	for _, name := range grpcServices {
		svc := c.GRPC.Services[name]
		if svc.Address == "" {
			return fmt.Errorf("grpc.services.%s.address is required", name)
		}
		transport, err := grpcx.NormalizeTransport(svc.Transport)
		if err != nil {
			return fmt.Errorf("grpc.services.%s.transport: %w", name, err)
		}
		if transport == grpcx.TransportH2C && svc.ServerName != "" {
			return fmt.Errorf("grpc.services.%s.server_name does not apply to h2c", name)
		}
		needsCA = needsCA || transport == grpcx.TransportTLS
	}
	if needsCA && c.GRPC.CAFile == "" {
		return fmt.Errorf("grpc.ca_file is required for tls services")
	}
	for name := range c.GRPC.Services {
		if !slices.Contains(grpcServices, name) {
			return fmt.Errorf("grpc.services.%s does not name a gRPC service in the route table", name)
		}
	}
	return nil
}

func (c *Config) Print() {}

// Validate checks the settings against the services in the route table: each
// needs an upstream, and an upstream for an unknown service is a typo.
func (c *Config) Validate(services []string) error {
	for _, name := range services {
		if c.Upstreams[name] == "" {
			return fmt.Errorf("upstreams.%s is required", name)
		}
	}
	for name, raw := range c.Upstreams {
		if !slices.Contains(services, name) {
			return fmt.Errorf("upstreams.%s does not name a service in the route table", name)
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("upstreams.%s: %q must be an http(s) base URL without path, query or credentials", name, raw)
		}
	}
	if err := httpx.CheckOrigins("cors_allowed_origins", c.CORSAllowedOrigins); err != nil {
		return err
	}
	return c.Auth.validateOAuth()
}
