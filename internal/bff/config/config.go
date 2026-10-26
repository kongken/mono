// Package config is the BFF configuration. It shares the YAML file with
// Butterfly's log settings.
package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/kongken/mono/internal/bff/adminguard"
	"github.com/kongken/mono/internal/bff/navigation"
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
	// Navigation is the registry of front-end apps the browser may be sent back to
	// after signing in at OzID. Omit it to run without OzID navigation.
	Navigation navigation.Config `yaml:"navigation"`
	// Admin is the perimeter of the unified admin console's API: which networks may
	// use it and which reverse proxies are trusted to report the client address.
	// Leaving it out closes the console: every management procedure is refused.
	Admin adminguard.Config `yaml:"admin"`
}

// MCPConfig controls the metro MCP endpoint.
type MCPConfig struct {
	// RateLimitPerMinute is the request limit per management token. Default 60.
	RateLimitPerMinute int `yaml:"rate_limit_per_minute"`
	// AllowedCIDRs are the networks MCP clients may call from. It is the machine allowlist, independent
	// of admin.allowed_cidrs (the browser console's): neither admits the other's callers. EMPTY REFUSES
	// EVERY MCP REQUEST. The rules for the entries are those of admin.allowed_cidrs.
	AllowedCIDRs []string `yaml:"allowed_cidrs"`
	// TrustedProxies are the networks of the reverse proxies that connect to the BFF directly and are
	// trusted to write ForwardedHeader for MCP calls. Empty means the TCP peer is the client. It must not
	// overlap AllowedCIDRs.
	TrustedProxies []string `yaml:"trusted_proxies"`
	// ForwardedHeader is the multi-hop header such a proxy appends to (default X-Forwarded-For).
	ForwardedHeader string `yaml:"forwarded_header"`
}

// Source is the machine perimeter's source-network configuration.
func (m MCPConfig) Source() adminguard.SourceConfig {
	return adminguard.SourceConfig{AllowedCIDRs: m.AllowedCIDRs, TrustedProxies: m.TrustedProxies, ForwardedHeader: m.ForwardedHeader}
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
	// their account, each named like its entry in the route table. Default: tripmap plus
	// every deployed user-data product (Config.CleanupProducts). A product not deployed in
	// this environment must not be listed, and a deployed one must be (Validate).
	AccountCleanup []string      `yaml:"account_cleanup"`
	CookieSecure   bool          `yaml:"cookie_secure"`
	CSRFTokenTTL   time.Duration `yaml:"csrf_token_ttl"`
	// LegacyMailTransition keeps the product pages that finish an account mail sent before the
	// central landing working for a bounded time: see LegacyMailTransition. Omit it once those mails
	// have expired; with it omitted the account procedures take the user center's origin only.
	LegacyMailTransition LegacyMailTransition `yaml:"legacy_mail_transition"`
}

// MaxLegacyMailTransition is the longest a configured old-mail transition may reach into the
// future. The tokens in those mails live for minutes (usersrv registration_ttl and reset_ttl, 30m
// and 15m by default), so a week covers them with a long queue of retries to spare; anything longer
// is a transition that was meant to be permanent, and the BFF refuses to start with it.
const MaxLegacyMailTransition = 7 * 24 * time.Hour

// LegacyMailTransition is the controlled, time-limited exception to "only the user center calls
// the account procedures": mails issued before the central OzID landing point to a product's own
// /account/verify-email or /account/reset-password page, and that page completes the token from the
// product's origin. Those origins may call exactly CompleteRegistration and ResetPassword until
// Until, and nothing else; after Until they are refused like any other origin.
type LegacyMailTransition struct {
	// Origins are exact product origins, each the registered navigation origin of trip-map or metro.
	Origins []string `yaml:"origins"`
	// Until is an RFC 3339 instant, required with Origins, at most MaxLegacyMailTransition ahead of
	// the moment the configuration is loaded. A past value is not a start error (a restart after the
	// end must not take the service down): the origins are simply not accepted, and the BFF logs it.
	Until string `yaml:"until"`
}

// UntilTime parses Until.
func (t LegacyMailTransition) UntilTime() (time.Time, error) {
	if t.Until == "" {
		return time.Time{}, fmt.Errorf("auth.legacy_mail_transition.until is required with origins (an RFC 3339 instant)")
	}
	at, err := time.Parse(time.RFC3339, t.Until)
	if err != nil {
		return time.Time{}, fmt.Errorf("auth.legacy_mail_transition.until: %q is not an RFC 3339 instant", t.Until)
	}
	return at, nil
}

// validate checks the transition against the registry it refers to. now is injectable for tests.
func (t LegacyMailTransition) validate(nav navigation.Config, now time.Time) error {
	if len(t.Origins) == 0 {
		if t.Until != "" {
			return fmt.Errorf("auth.legacy_mail_transition.until is set without origins: remove the section")
		}
		return nil
	}
	if nav.Apps[navigation.AppOzID].Origin == "" {
		return fmt.Errorf("auth.legacy_mail_transition needs navigation.apps.%s: the transition is an exception to the user center rule", navigation.AppOzID)
	}
	at, err := t.UntilTime()
	if err != nil {
		return err
	}
	if at.Sub(now) > MaxLegacyMailTransition {
		return fmt.Errorf("auth.legacy_mail_transition.until is more than %s away: account mail tokens live for minutes, so the transition is short and is not permanent", MaxLegacyMailTransition)
	}
	seen := map[string]bool{}
	for _, o := range t.Origins {
		if seen[o] {
			return fmt.Errorf("auth.legacy_mail_transition.origins lists %q twice", o)
		}
		seen[o] = true
		product := false
		for _, id := range []string{navigation.AppTrip, navigation.AppMetro} {
			if nav.Apps[id].Origin == o {
				product = true
			}
		}
		if !product {
			return fmt.Errorf("auth.legacy_mail_transition.origins: %q is not the registered origin of a product (%s or %s): only the products whose old mails exist belong here, never OzID, the console or an unregistered origin", o, navigation.AppTrip, navigation.AppMetro)
		}
	}
	return nil
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

// UserDataProducts are the services that keep data of an account. Closing an account must
// remove all of it, so a deployed one can never be left out of the cleanup list.
var UserDataProducts = []string{"tripmap", "metro"}

// CleanupProducts returns the products whose data an account deletion removes: the
// configured auth.account_cleanup, or by default tripmap plus every other user-data
// product that has an upstream here (a deployed product is never skipped by default).
func (c *Config) CleanupProducts() []string {
	if len(c.Auth.AccountCleanup) > 0 {
		return c.Auth.AccountCleanup
	}
	out := []string{"tripmap"}
	for _, p := range UserDataProducts[1:] {
		if c.Upstreams[p] != "" {
			out = append(out, p)
		}
	}
	return out
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

// AccountService is the name of the usersrv upstream: configuring it is what makes the BFF serve the
// account procedures (sign-in, registration, password, devices, closing). A test in internal/bff/app
// keeps it equal to usersrv.Service.
const AccountService = "usersrv"

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
	if _, err := navigation.New(c.Navigation, c.CORSAllowedOrigins, c.Auth.CookieSecure); err != nil {
		return err
	}
	if slices.Contains(services, AccountService) {
		// The account procedures are answered for the user center's registered origin only, so a BFF that
		// serves accounts without a registry would have nobody to name: it does not start. There is no
		// switch that lets it start unrestricted. A deployment without a BFF (a purely local front end) is
		// not affected, and neither is one that does not serve accounts.
		if c.Navigation.APIOrigin == "" || c.Navigation.Apps[navigation.AppOzID].Origin == "" {
			return fmt.Errorf("upstreams.%s serves the account procedures, which only the user center's origin may call: navigation.api_origin and navigation.apps.%s.origin are required (see config.bff.example.yaml)", AccountService, navigation.AppOzID)
		}
	}
	if err := c.Auth.LegacyMailTransition.validate(c.Navigation, time.Now()); err != nil {
		return err
	}
	if err := c.Admin.Validate(); err != nil {
		return err
	}
	if len(c.Admin.AllowedCIDRs) > 0 {
		if _, ok := c.Navigation.Apps[navigation.AppAdmin]; !ok {
			return fmt.Errorf("admin.allowed_cidrs is set but navigation.apps.%s is not: the console needs a registered origin", navigation.AppAdmin)
		}
	}
	if err := c.MCP.Source().Validate("mcp"); err != nil {
		return err
	}
	if c.MCP.RateLimitPerMinute < 0 {
		return fmt.Errorf("mcp.rate_limit_per_minute must not be negative")
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
	// A deployed product's data must not survive an account deletion because the list left it out.
	cleanup := c.CleanupProducts()
	for _, p := range UserDataProducts {
		if c.Upstreams[p] != "" && !slices.Contains(cleanup, p) {
			return fmt.Errorf("auth.account_cleanup must list %q: its upstream is configured, so its data of a deleted account would be left behind", p)
		}
	}
	if c.Auth.SessionTTL < 0 {
		return fmt.Errorf("auth.session_ttl must not be negative")
	}
	return nil
}
