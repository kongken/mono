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
	GRPC        GRPCConfig `yaml:"grpc"`
	DBStore     string     `yaml:"db_store"`
	RedisStore  string     `yaml:"redis_store"`
	KeyPrefix   string     `yaml:"key_prefix"`
	AutoMigrate bool       `yaml:"auto_migrate"`
	Auth        AuthConfig `yaml:"auth"`
	// MCP configures the metro MCP endpoint (POST /mcp/metro).
	MCP MCPConfig `yaml:"mcp"`
}

// MCPConfig controls the metro MCP endpoint.
type MCPConfig struct {
	// RateLimitPerMinute is the request limit per management token. Default 60.
	RateLimitPerMinute int `yaml:"rate_limit_per_minute"`
}

// RateLimit returns the effective per-token limit.
func (m MCPConfig) RateLimit() int {
	if m.RateLimitPerMinute <= 0 {
		return 60
	}
	return m.RateLimitPerMinute
}

// AuthConfig controls BFF browser sessions. Accounts, passwords, registration
// policy, roles and account mail belong to usersrv (see config.usersrv.example.yaml);
// the BFF keeps the Cookie session, CSRF, the admin session age and the list of
// products whose data an account deletion removes.
type AuthConfig struct {
	SessionTTL time.Duration `yaml:"session_ttl"`
	// AdminSessionMaxAge bounds how old, counted from the last real password check
	// (sign-in or Reauthenticate), a session may be when it calls a procedure that
	// declares required_role. Default 12h; reauthenticating starts a new period.
	AdminSessionMaxAge time.Duration `yaml:"admin_session_max_age"`
	// AccountCleanup lists the products whose data is removed when a user deletes
	// their account, each named like its entry in the route table. Default: tripmap
	// only. A product not deployed in this environment must not be listed.
	AccountCleanup []string      `yaml:"account_cleanup"`
	CookieSecure   bool          `yaml:"cookie_secure"`
	CSRFTokenTTL   time.Duration `yaml:"csrf_token_ttl"`
}

// DefaultAdminSessionMaxAge is used when auth.admin_session_max_age is unset.
const DefaultAdminSessionMaxAge = 12 * time.Hour

// AdminSessionLimit returns the effective admin_session_max_age.
func (a *AuthConfig) AdminSessionLimit() time.Duration {
	if a.AdminSessionMaxAge <= 0 {
		return DefaultAdminSessionMaxAge
	}
	return a.AdminSessionMaxAge
}

// CleanupProducts returns the products enabled for account data cleanup.
func (a *AuthConfig) CleanupProducts() []string {
	if len(a.AccountCleanup) == 0 {
		return []string{"tripmap"}
	}
	return a.AccountCleanup
}

type InternalAuth struct {
	// Issuer is the iss claim; services must be configured with the same value.
	Issuer string `yaml:"issuer"`
	// KID names the signing key; services look the public key up by it. Rotating
	// means adding the new public key to every service first, then changing
	// kid and the private key source here.
	KID string `yaml:"kid"`
	// PrivateKey and PrivateKeyFile are mutually exclusive PKCS#8 PEM sources.
	PrivateKey     string `yaml:"private_key"`
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
	if c.InternalAuth.Issuer == "" || c.InternalAuth.KID == "" || (c.InternalAuth.PrivateKey == "") == (c.InternalAuth.PrivateKeyFile == "") {
		return fmt.Errorf("internal_auth requires issuer, kid and exactly one of private_key or private_key_file")
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
	if c.Auth.AdminSessionMaxAge < 0 {
		return fmt.Errorf("auth.admin_session_max_age must not be negative")
	}
	for i, name := range c.Auth.AccountCleanup {
		if !slices.Contains(services, name) {
			return fmt.Errorf("auth.account_cleanup names %q, which is not a service in the route table", name)
		}
		if slices.Contains(c.Auth.AccountCleanup[:i], name) {
			return fmt.Errorf("auth.account_cleanup lists %q twice", name)
		}
	}
	if c.Auth.SessionTTL < 0 {
		return fmt.Errorf("auth.session_ttl must not be negative")
	}
	return nil
}
