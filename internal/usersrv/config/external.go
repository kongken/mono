package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/kongken/mono/internal/usersrv/outbox"
)

// ExternalConfig is independent of RegistrationEnabled: disabling new accounts
// must never disable an existing account's external login or explicit Link.
// No adapter is contacted during validation. Runtime flows arrive in later stages.
type ExternalConfig struct {
	Enabled              bool                              `yaml:"enabled"`
	Providers            map[string]ExternalProviderConfig `yaml:"providers"`
	RequireVerifiedEmail *bool                             `yaml:"require_verified_email"`
	// ProofKeyID names a configured mail.encryption.keys entry. The later
	// challenge implementation must use its own purpose-bound associated data.
	ProofKeyID string `yaml:"proof_key_id"`
}

// ExternalProviderConfig contains no configurable endpoints or issuer: each
// provider is fixed to its official endpoints by the adapter, never by requests.
type ExternalProviderConfig struct {
	Enabled          bool   `yaml:"enabled"`
	ClientID         string `yaml:"client_id"`
	ClientSecret     string `yaml:"client_secret" json:"-"`
	ClientSecretFile string `yaml:"client_secret_file" json:"-"`
	RedirectURI      string `yaml:"redirect_uri"`
	ConfigVersion    int64  `yaml:"config_version"`
}

// Config formatting is deliberately opaque, including nested fmt/slog output.
func (ExternalConfig) String() string                 { return "[external auth config redacted]" }
func (c ExternalConfig) GoString() string             { return c.String() }
func (c ExternalConfig) LogValue() slog.Value         { return slog.StringValue(c.String()) }
func (ExternalProviderConfig) String() string         { return "[external provider config redacted]" }
func (c ExternalProviderConfig) GoString() string     { return c.String() }
func (c ExternalProviderConfig) LogValue() slog.Value { return slog.StringValue(c.String()) }

// ProviderEnabled is the usersrv gate only. The BFF independently gates its
// browser entry; registration status is intentionally not an input here.
func (c ExternalConfig) ProviderEnabled(provider string) bool {
	if provider != "google" && provider != "github" {
		return false
	}
	return c.Enabled && c.Providers[provider].Enabled
}

// ClientSecretValue reads one inline or mounted Secret. Errors never echo its
// value, contents, or file name; disabled providers need no mounted file.
func (p ExternalProviderConfig) ClientSecretValue() (string, error) {
	if (p.ClientSecret == "") == (p.ClientSecretFile == "") {
		return "", errors.New("set exactly one of client_secret or client_secret_file")
	}
	raw := p.ClientSecret
	if p.ClientSecretFile != "" {
		b, err := os.ReadFile(p.ClientSecretFile)
		if err != nil {
			return "", errors.New("client_secret_file cannot be read")
		}
		raw = string(b)
	}
	secret := strings.TrimSpace(raw)
	if secret == "" || len(secret) > 8192 || strings.ContainsAny(secret, "\r\n\x00") {
		return "", errors.New("client secret must be nonempty, single-line and at most 8192 bytes")
	}
	return secret, nil
}

func (c *ExternalConfig) validate(allowLoopbackHTTP bool, encryption outbox.EncryptionConfig) error {
	if c.RequireVerifiedEmail == nil {
		on := true
		c.RequireVerifiedEmail = &on
	}
	if !*c.RequireVerifiedEmail {
		return errors.New("auth.external.require_verified_email cannot be disabled")
	}
	for name, p := range c.Providers {
		if name != "google" && name != "github" {
			return errors.New("auth.external.providers supports only google and github")
		}
		if !p.Enabled {
			continue
		}
		prefix := "auth.external.providers." + name + ": "
		if strings.TrimSpace(p.ClientID) != p.ClientID || p.ClientID == "" || len(p.ClientID) > 1024 || strings.ContainsAny(p.ClientID, "\r\n\x00") {
			return errors.New(prefix + "client_id is required and must be single-line")
		}
		if _, err := p.ClientSecretValue(); err != nil {
			return fmt.Errorf("%s%w", prefix, err)
		}
		u, err := url.Parse(p.RedirectURI)
		if err != nil || u == nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
			u.RawPath != "" || u.Path != "/api/identity/callback/"+name || u.String() != p.RedirectURI {
			return errors.New(prefix + "redirect_uri must be an exact callback URL without credentials, query or fragment")
		}
		if err := validateOrigin(u.Scheme+"://"+u.Host, allowLoopbackHTTP); err != nil {
			return errors.New(prefix + "redirect_uri must use HTTPS (loopback HTTP requires the development opt-in)")
		}
		if p.ConfigVersion < 1 {
			return errors.New(prefix + "config_version must be positive")
		}
	}
	if c.Enabled {
		keys, err := encryption.Keyring()
		if err != nil || c.ProofKeyID == "" || !keys.Has(c.ProofKeyID) {
			return errors.New("auth.external.proof_key_id must name a configured encryption key")
		}
	}
	return nil
}
