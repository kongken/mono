// Package externalprovider verifies external credentials without owning accounts,
// browser sessions or challenge consumption. Only the later account transaction
// layer may turn an adapter result into an account/login/binding (ADR 0001).
package externalprovider

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type ProviderName string

const (
	GoogleName ProviderName = "google"
	GitHubName ProviderName = "github"
)

var (
	ErrDisabled    = errors.New("external provider is disabled")
	ErrRejected    = errors.New("external authentication failed")
	ErrUnavailable = errors.New("external provider is unavailable")
)

type EmailTrust string

const (
	GoogleAuthoritative EmailTrust = "google_authoritative"
	ProviderVerified    EmailTrust = "provider_verified"
	Unverified          EmailTrust = "unverified"
)

// AuthorizationRequest comes from BFF-owned, browser-bound state. All three
// values must be retained with the original flow; none is a caller identity.
// Nonce is specific to Google OIDC; GitHub OAuth uses state and PKCE only.
type AuthorizationRequest struct {
	State, Nonce, CodeChallenge string `json:"-"`
}

// CodeExchangeRequest must come from a single claimed challenge, never directly
// from an untrusted browser. CodeChallenge is the original S256 challenge and
// ConfigVersion is the provider configuration generation bound to that flow.
type CodeExchangeRequest struct {
	Provider                             ProviderName
	Code, Verifier, Nonce, CodeChallenge string `json:"-"`
	ConfigVersion                        int64
}

func (AuthorizationRequest) String() string         { return "[external authorization request redacted]" }
func (r AuthorizationRequest) GoString() string     { return r.String() }
func (r AuthorizationRequest) LogValue() slog.Value { return slog.StringValue(r.String()) }
func (CodeExchangeRequest) String() string          { return "[external code exchange redacted]" }
func (r CodeExchangeRequest) GoString() string      { return r.String() }
func (r CodeExchangeRequest) LogValue() slog.Value  { return slog.StringValue(r.String()) }

// ProviderIdentity contains verified, minimal identity facts. Tokens, codes and
// complete claims are deliberately absent. Subject/email must not enter logs,
// audits or browser responses. This is not a user UUID or a session credential.
type ProviderIdentity struct {
	Provider               ProviderName
	Issuer, Subject, Email string
	EmailVerified          bool
	EmailTrust             EmailTrust
	DisplayName, AvatarURL string
	// Google token issuance time or GitHub proof verification time;
	// never an OzID password authentication time.
	AuthenticatedAt time.Time
}

func (ProviderIdentity) String() string         { return "[verified external identity redacted]" }
func (i ProviderIdentity) GoString() string     { return i.String() }
func (i ProviderIdentity) LogValue() slog.Value { return slog.StringValue(i.String()) }

type ExternalProvider interface {
	Name() ProviderName
	AuthorizationURL(context.Context, AuthorizationRequest) (string, error)
	ExchangeCode(context.Context, CodeExchangeRequest) (ProviderIdentity, error)
	ValidateConfiguration() error
}
