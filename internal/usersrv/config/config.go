// Package config is the usersrv service configuration. It shares the YAML file
// with Butterfly's store.db / store.redis definitions.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/usersrv/mailer"
	"github.com/kongken/mono/internal/usersrv/outbox"
	"github.com/kongken/mono/internal/usersrv/passwordhash"
	"github.com/kongken/mono/internal/usersrv/ratelimit"
)

// AppConfig is the usersrv configuration.
type AppConfig struct {
	// DBStore names the store.db client of the usersrv PostgreSQL database. It is
	// a database of its own, not the BFF, tripmap or metro one.
	DBStore string `yaml:"db_store"`
	// RedisStore names the store.redis client for rate limits. It is required:
	// without it password checks and account mail would have no limits.
	RedisStore string `yaml:"redis_store"`
	// KeyPrefix starts every Redis key of the service, so it can share a Redis
	// server with the other services and tests can use isolated prefixes.
	KeyPrefix string `yaml:"key_prefix"`
	// AutoMigrate lets Ent create and extend tables at startup. Production
	// deployments apply migrations/usersrv/ instead and leave it off.
	AutoMigrate bool `yaml:"auto_migrate"`
	// GRPC is the listener the BFF calls; every RPC is served only here.
	GRPC grpcx.ServerConfig `yaml:"grpc"`
	// InternalAuth lists the BFF public keys that may call this service.
	InternalAuth servicetoken.VerifyConfig `yaml:"internal_auth"`
	Auth         AuthConfig                `yaml:"auth"`
	// Mail selects the one mail vendor and holds the outbox encryption keys.
	Mail      MailConfig      `yaml:"mail"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
}

// MailConfig is the mail section: the vendor settings of package mailer plus the
// outbox encryption keys. The encryption keys are independent of the BFF
// signing key and of auth.hmac_key, and live in configuration or a mounted
// Secret, never in the database that holds the encrypted jobs.
type MailConfig struct {
	mailer.Config `yaml:",inline"`
	Encryption    outbox.EncryptionConfig `yaml:"encryption"`
}

// AuthConfig groups the account rules.
type AuthConfig struct {
	// RegistrationEnabled defaults to true; set it to false to refuse new sign-ups
	// (stage P3 enforces it). It never affects login or password reset.
	RegistrationEnabled *bool `yaml:"registration_enabled"`
	// Apps maps an app ID to its link origin. Mail links (stage P2/P3) are built
	// only from these origins and fixed paths. Both trip-map and metro are required
	// and must match the BFF's allowed origins.
	Apps map[string]AppOrigin `yaml:"apps"`
	// AllowLoopbackHTTP permits http origins on loopback hosts for local development.
	AllowLoopbackHTTP bool `yaml:"allow_loopback_http"`
	// RegistrationTTL and ResetTTL are the token lifetimes (defaults 30m and 15m).
	RegistrationTTL string             `yaml:"registration_ttl"`
	ResetTTL        string             `yaml:"reset_ttl"`
	PasswordHash    PasswordHashConfig `yaml:"password_hash"`
	// HMACKey or HMACKeyFile (exactly one) is the standard-base64 key, at least 32
	// bytes, that keys the digests of rate-limit subjects in Redis. It is
	// independent of the BFF signing key and of the mail encryption key.
	HMACKey     string `yaml:"hmac_key"`
	HMACKeyFile string `yaml:"hmac_key_file"`
}

// AppOrigin is one product's exact origin.
type AppOrigin struct {
	Origin string `yaml:"origin"`
}

// PasswordHashConfig is the Argon2id resource budget (spec 4.2). Zero values
// take the defaults of 64 MiB, 3 iterations, 1 lane, 2 concurrent hashes. The
// validated floor is the OWASP minimum; do not lower a deployed value without
// recording why.
type PasswordHashConfig struct {
	MemoryKiB     uint32 `yaml:"memory_kib"`
	Iterations    uint32 `yaml:"iterations"`
	Parallelism   uint8  `yaml:"parallelism"`
	MaxConcurrent int    `yaml:"max_concurrent"`
	// WaitTimeout bounds the wait for a free hashing slot, such as 2s.
	WaitTimeout string `yaml:"wait_timeout"`
}

// LimitConfig is "limit attempts per window".
type LimitConfig struct {
	Limit  int    `yaml:"limit"`
	Window string `yaml:"window"`
}

// RateLimitConfig lists the limits usersrv enforces. Counters live in Redis and
// fail closed. The token-submission limits arrive with stage P3.
type RateLimitConfig struct {
	// PasswordPerAccount and PasswordPerIP bound password verification (re-auth,
	// change password): success counts too. Defaults 10 and 40 per 15 minutes.
	PasswordPerAccount LimitConfig `yaml:"password_per_account"`
	PasswordPerIP      LimitConfig `yaml:"password_per_ip"`
	// ProfileWritePerUser defaults to 30 per minute.
	ProfileWritePerUser LimitConfig `yaml:"profile_write_per_user"`
	// DeletionPerUser bounds BeginDeletion/FinishDeletion; default 10 per 15 minutes.
	DeletionPerUser LimitConfig `yaml:"deletion_per_user"`

	// Account mail requests (spec section 10). Per client address 20/hour for
	// each of registration and reset; per mailbox a cooldown (1 per 60s) and 5 per
	// hour for each purpose; 8 per hour for all account mail to one mailbox;
	// 200 per hour for the whole service (adjust to the vendor's quota).
	RegistrationMailPerIP    LimitConfig `yaml:"registration_mail_per_ip"`
	ResetMailPerIP           LimitConfig `yaml:"reset_mail_per_ip"`
	RegistrationMailCooldown LimitConfig `yaml:"registration_mail_cooldown"`
	RegistrationMailPerEmail LimitConfig `yaml:"registration_mail_per_email"`
	ResetMailCooldown        LimitConfig `yaml:"reset_mail_cooldown"`
	ResetMailPerEmail        LimitConfig `yaml:"reset_mail_per_email"`
	MailPerEmail             LimitConfig `yaml:"mail_per_email"`
	MailGlobal               LimitConfig `yaml:"mail_global"`
}

func (c *AppConfig) Print() {}

var (
	keyPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,64}$`)
	appIDPattern     = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
)

// RequiredApps are the app IDs that must have an origin.
var RequiredApps = []string{"trip-map", "metro"}

// Validate checks the settings that must be right before the service starts and
// fills defaults. It does not contact PostgreSQL, Redis or any mail vendor.
func (c *AppConfig) Validate() error {
	if c.DBStore == "" {
		return errors.New("db_store is required")
	}
	if c.RedisStore == "" {
		return errors.New("redis_store is required (rate limits need Redis)")
	}
	if !keyPrefixPattern.MatchString(c.KeyPrefix) {
		return errors.New("key_prefix is required: 1-64 characters of letters, digits, colon, underscore, hyphen")
	}
	if err := c.GRPC.Validate(); err != nil {
		return err
	}
	if c.InternalAuth.Issuer == "" || c.InternalAuth.Audience == "" || len(c.InternalAuth.TrustedKeys) == 0 {
		return errors.New("internal_auth requires issuer, audience and at least one trusted_keys entry")
	}
	if c.InternalAuth.Audience != "usersrv" {
		return errors.New(`internal_auth.audience must be "usersrv"`)
	}
	if err := c.Auth.validate(); err != nil {
		return err
	}
	if err := c.Mail.validate(&c.Auth); err != nil {
		return err
	}
	return c.RateLimit.validate()
}

// validate checks the vendor settings and the encryption keys. Both are required:
// without a usable mail setup usersrv would look able to register users while
// no mail can leave, so it refuses to start.
func (m *MailConfig) validate(auth *AuthConfig) error {
	if err := m.Config.Validate(); err != nil {
		return err
	}
	keys, err := m.Encryption.Keyring()
	if err != nil {
		return err
	}
	if hmacKey, err := auth.HMACKeyBytes(); err == nil && keys.Overlaps(hmacKey) {
		return errors.New("mail.encryption keys must differ from auth.hmac_key")
	}
	return nil
}

// Keyring returns the loaded outbox encryption keys; call it after Validate.
func (m MailConfig) Keyring() (*outbox.Keyring, error) { return m.Encryption.Keyring() }

func (a *AuthConfig) validate() error {
	if a.RegistrationEnabled == nil {
		on := true
		a.RegistrationEnabled = &on
	}
	for _, id := range RequiredApps {
		if _, ok := a.Apps[id]; !ok {
			return fmt.Errorf("auth.apps.%s.origin is required", id)
		}
	}
	for id, app := range a.Apps {
		if !appIDPattern.MatchString(id) {
			return fmt.Errorf("auth.apps: invalid app id %q", id)
		}
		if err := validateOrigin(app.Origin, a.AllowLoopbackHTTP); err != nil {
			return fmt.Errorf("auth.apps.%s.origin: %w", id, err)
		}
	}
	for name, ttl := range map[string]*string{"registration_ttl": &a.RegistrationTTL, "reset_ttl": &a.ResetTTL} {
		def := map[string]string{"registration_ttl": "30m", "reset_ttl": "15m"}[name]
		if *ttl == "" {
			*ttl = def
		}
		d, err := time.ParseDuration(*ttl)
		if err != nil || d < time.Minute || d > 24*time.Hour {
			return fmt.Errorf("auth.%s %q must be a duration between 1m and 24h", name, *ttl)
		}
	}
	if _, err := a.PasswordHash.Resolve(); err != nil {
		return err
	}
	if _, err := a.HMACKeyBytes(); err != nil {
		return err
	}
	return nil
}

// RegistrationOpen reports whether new sign-ups are allowed.
func (a AuthConfig) RegistrationOpen() bool {
	return a.RegistrationEnabled == nil || *a.RegistrationEnabled
}

// HMACKeyBytes returns the decoded rate-limit subject key.
func (a AuthConfig) HMACKeyBytes() ([]byte, error) {
	if (a.HMACKey == "") == (a.HMACKeyFile == "") {
		return nil, errors.New("configure exactly one of auth.hmac_key or auth.hmac_key_file")
	}
	raw := a.HMACKey
	if a.HMACKeyFile != "" {
		b, err := os.ReadFile(a.HMACKeyFile)
		if err != nil {
			return nil, fmt.Errorf("auth.hmac_key_file: %w", err)
		}
		raw = string(b)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("auth.hmac_key must be standard base64")
	}
	if len(key) < 32 {
		return nil, errors.New("auth.hmac_key must decode to at least 32 bytes")
	}
	return key, nil
}

func validateOrigin(origin string, allowLoopbackHTTP bool) error {
	return mailer.ValidateOrigin(origin, allowLoopbackHTTP)
}

// Resolve applies defaults and checks the bounds, returning the hasher config.
func (p PasswordHashConfig) Resolve() (passwordhash.Config, error) {
	cfg := passwordhash.DefaultConfig()
	if p.MemoryKiB != 0 {
		cfg.MemoryKiB = p.MemoryKiB
	}
	if p.Iterations != 0 {
		cfg.Iterations = p.Iterations
	}
	if p.Parallelism != 0 {
		cfg.Parallelism = p.Parallelism
	}
	if p.MaxConcurrent != 0 {
		cfg.MaxConcurrent = p.MaxConcurrent
	}
	if p.WaitTimeout != "" {
		d, err := time.ParseDuration(p.WaitTimeout)
		if err != nil || d <= 0 || d > time.Minute {
			return cfg, fmt.Errorf("auth.password_hash.wait_timeout %q must be a duration up to 1m", p.WaitTimeout)
		}
		cfg.WaitTimeout = d
	}
	switch {
	case cfg.MemoryKiB < 19456 || cfg.MemoryKiB > 1<<20:
		return cfg, errors.New("auth.password_hash.memory_kib must be between 19456 (19 MiB) and 1048576")
	case cfg.Iterations < 2 || cfg.Iterations > 10:
		return cfg, errors.New("auth.password_hash.iterations must be between 2 and 10")
	case cfg.Parallelism < 1 || cfg.Parallelism > 8:
		return cfg, errors.New("auth.password_hash.parallelism must be between 1 and 8")
	case cfg.MaxConcurrent < 1 || cfg.MaxConcurrent > 16:
		return cfg, errors.New("auth.password_hash.max_concurrent must be between 1 and 16")
	}
	return cfg, nil
}

func (r *RateLimitConfig) validate() error {
	for _, l := range []struct {
		name  string
		cfg   *LimitConfig
		limit int
		win   string
	}{
		{"password_per_account", &r.PasswordPerAccount, 10, "15m"},
		{"password_per_ip", &r.PasswordPerIP, 40, "15m"},
		{"profile_write_per_user", &r.ProfileWritePerUser, 30, "1m"},
		{"deletion_per_user", &r.DeletionPerUser, 10, "15m"},
		{"registration_mail_per_ip", &r.RegistrationMailPerIP, 20, "1h"},
		{"reset_mail_per_ip", &r.ResetMailPerIP, 20, "1h"},
		{"registration_mail_cooldown", &r.RegistrationMailCooldown, 1, "60s"},
		{"registration_mail_per_email", &r.RegistrationMailPerEmail, 5, "1h"},
		{"reset_mail_cooldown", &r.ResetMailCooldown, 1, "60s"},
		{"reset_mail_per_email", &r.ResetMailPerEmail, 5, "1h"},
		{"mail_per_email", &r.MailPerEmail, 8, "1h"},
		{"mail_global", &r.MailGlobal, 200, "1h"},
	} {
		if l.cfg.Limit == 0 {
			l.cfg.Limit = l.limit
		}
		if l.cfg.Window == "" {
			l.cfg.Window = l.win
		}
		if _, err := l.cfg.Rule(l.name); err != nil {
			return err
		}
	}
	return nil
}

// Rule converts one limit to a rate-limit rule.
func (l LimitConfig) Rule(name string) (ratelimit.Rule, error) {
	d, err := time.ParseDuration(l.Window)
	if err != nil || d < time.Second || d > 24*time.Hour {
		return ratelimit.Rule{}, fmt.Errorf("rate_limit.%s.window %q must be a duration between 1s and 24h", name, l.Window)
	}
	if l.Limit < 1 || l.Limit > 1_000_000 {
		return ratelimit.Rule{}, fmt.Errorf("rate_limit.%s.limit must be between 1 and 1000000", name)
	}
	return ratelimit.Rule{Name: name, Limit: l.Limit, Window: d}, nil
}

// Limits returns the resolved rules; call it after Validate.
func (r RateLimitConfig) Limits() (Limits, error) {
	var out Limits
	var err error
	for _, p := range []struct {
		dst  *ratelimit.Rule
		name string
		cfg  LimitConfig
	}{
		{&out.PasswordPerAccount, "password_per_account", r.PasswordPerAccount},
		{&out.PasswordPerIP, "password_per_ip", r.PasswordPerIP},
		{&out.ProfileWritePerUser, "profile_write_per_user", r.ProfileWritePerUser},
		{&out.DeletionPerUser, "deletion_per_user", r.DeletionPerUser},
		{&out.Mail.RegistrationPerIP, "registration_mail_per_ip", r.RegistrationMailPerIP},
		{&out.Mail.ResetPerIP, "reset_mail_per_ip", r.ResetMailPerIP},
		{&out.Mail.RegistrationEmailCooldown, "registration_mail_cooldown", r.RegistrationMailCooldown},
		{&out.Mail.RegistrationPerEmail, "registration_mail_per_email", r.RegistrationMailPerEmail},
		{&out.Mail.ResetEmailCooldown, "reset_mail_cooldown", r.ResetMailCooldown},
		{&out.Mail.ResetPerEmail, "reset_mail_per_email", r.ResetMailPerEmail},
		{&out.Mail.MailPerEmail, "mail_per_email", r.MailPerEmail},
		{&out.Mail.MailGlobal, "mail_global", r.MailGlobal},
	} {
		if *p.dst, err = p.cfg.Rule(p.name); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Limits are the resolved rules.
type Limits struct {
	PasswordPerAccount, PasswordPerIP, ProfileWritePerUser, DeletionPerUser ratelimit.Rule
	// Mail are the limits of account mail requests.
	Mail ratelimit.MailRules
}
