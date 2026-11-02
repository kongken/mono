package externalprovider

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kongken/mono/internal/usersrv/identity"
)

const (
	GoogleIssuer           = "https://accounts.google.com"
	googleDiscoveryURL     = GoogleIssuer + "/.well-known/openid-configuration"
	googleAuthorizationURL = GoogleIssuer + "/o/oauth2/v2/auth"
	googleTokenURL         = "https://oauth2.googleapis.com/token"
	googleJWKSURL          = "https://www.googleapis.com/oauth2/v3/certs"
)

// GoogleConfig is an immutable snapshot of the enabled global/provider gate and
// validated client settings. Rebuild the adapter on configuration replacement;
// the challenge layer must recheck the current gate/generation at final commit.
type GoogleConfig struct {
	Enabled           bool
	ClientID          string
	ClientSecret      string `json:"-"`
	RedirectURI       string
	ConfigVersion     int64
	AllowLoopbackHTTP bool
}

func (GoogleConfig) String() string         { return "[google config redacted]" }
func (c GoogleConfig) GoString() string     { return c.String() }
func (c GoogleConfig) LogValue() slog.Value { return slog.StringValue(c.String()) }

// Google implements the provider seam without a database, session store or
// browser callback. The transport seam permits local fake-provider contracts;
// production passes nil. Endpoints cannot be replaced by config or requests.
type Google struct {
	cfg       GoogleConfig
	client    *http.Client
	now       func() time.Time
	discovery *documentCache[struct{}]
	keys      *documentCache[map[string]*rsa.PublicKey]
}

var _ ExternalProvider = (*Google)(nil)

func NewGoogle(cfg GoogleConfig, transport http.RoundTripper) (*Google, error) {
	g := &Google{cfg: cfg, client: newHTTPClient(transport), now: time.Now,
		discovery: newDocumentCache[struct{}](), keys: newDocumentCache[map[string]*rsa.PublicKey]()}
	if err := g.ValidateConfiguration(); err != nil {
		return nil, err
	}
	return g, nil
}

func (*Google) Name() ProviderName     { return GoogleName }
func (*Google) String() string         { return "[google adapter redacted]" }
func (g *Google) GoString() string     { return g.String() }
func (g *Google) LogValue() slog.Value { return slog.StringValue(g.String()) }

func (g *Google) ValidateConfiguration() error {
	if !g.cfg.Enabled {
		return nil
	}
	c := g.cfg
	if strings.TrimSpace(c.ClientID) != c.ClientID || c.ClientID == "" || len(c.ClientID) > 1024 || strings.ContainsAny(c.ClientID, "\r\n\x00") ||
		strings.TrimSpace(c.ClientSecret) != c.ClientSecret || c.ClientSecret == "" || len(c.ClientSecret) > 8192 || strings.ContainsAny(c.ClientSecret, "\r\n\x00") || c.ConfigVersion < 1 {
		return errors.New("google provider requires a client ID, secret and positive config version")
	}
	u, err := url.Parse(c.RedirectURI)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.RawPath != "" || u.Path != "/api/identity/callback/google" || u.String() != c.RedirectURI {
		return errors.New("google provider requires an exact callback URL")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && c.AllowLoopbackHTTP && loopback) {
		return errors.New("google callback requires HTTPS (loopback HTTP needs development opt-in)")
	}
	return nil
}

var (
	flowValue     = regexp.MustCompile(`^[A-Za-z0-9_-]{43,128}$`)
	pkceVerifier  = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
	s256Challenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

func (g *Google) AuthorizationURL(ctx context.Context, req AuthorizationRequest) (string, error) {
	if !g.cfg.Enabled {
		return "", ErrDisabled
	}
	if !flowValue.MatchString(req.State) || !flowValue.MatchString(req.Nonce) || !s256Challenge.MatchString(req.CodeChallenge) {
		return "", ErrRejected
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := g.discover(ctx); err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", ErrUnavailable
	}
	u, _ := url.Parse(googleAuthorizationURL)
	u.RawQuery = url.Values{
		"client_id": {g.cfg.ClientID}, "redirect_uri": {g.cfg.RedirectURI}, "response_type": {"code"},
		"scope": {"openid email profile"}, "state": {req.State}, "nonce": {req.Nonce},
		"code_challenge": {req.CodeChallenge}, "code_challenge_method": {"S256"},
	}.Encode()
	return u.String(), nil
}

// ExchangeCode makes exactly one POST. Cancellation, an unknown result, or an
// unusable code requires a fresh flow; this method never retries code exchange.
// Provider single-use grants and nonce matching complement the persistent
// usersrv/BFF challenge consumption implemented in later stages.
func (g *Google) ExchangeCode(ctx context.Context, req CodeExchangeRequest) (ProviderIdentity, error) {
	var zero ProviderIdentity
	if !g.cfg.Enabled {
		return zero, ErrDisabled
	}
	digest := sha256.Sum256([]byte(req.Verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	if req.Provider != GoogleName || req.ConfigVersion != g.cfg.ConfigVersion || req.Code == "" || len(req.Code) > 4096 || strings.ContainsAny(req.Code, "\r\n\x00") ||
		!pkceVerifier.MatchString(req.Verifier) || !flowValue.MatchString(req.Nonce) || !s256Challenge.MatchString(req.CodeChallenge) ||
		subtle.ConstantTimeCompare([]byte(challenge), []byte(req.CodeChallenge)) != 1 {
		return zero, ErrRejected
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := g.discover(ctx); err != nil {
		return zero, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {g.cfg.ClientID}, "client_secret": {g.cfg.ClientSecret},
		"code": {req.Code}, "code_verifier": {req.Verifier}, "redirect_uri": {g.cfg.RedirectURI}}.Encode()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form))
	if err != nil {
		return zero, ErrRejected
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	var response struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
	}
	if _, err := readJSON(g.client, r, &response); err != nil {
		return zero, err
	}
	if response.Error != "" || response.IDToken == "" || len(response.IDToken) > 32*1024 || !strings.EqualFold(response.TokenType, "Bearer") {
		return zero, ErrRejected
	}
	// Only temporary local variables ever hold tokens; the result has no token
	// fields. No userinfo/tokeninfo request or refresh credential is retained.
	return g.verifyIDToken(ctx, response.IDToken, response.AccessToken, req.Nonce)
}

func (g *Google) discover(ctx context.Context) error {
	_, err := g.discovery.load(ctx, g.now, false, func() (struct{}, time.Duration, error) {
		r, _ := http.NewRequestWithContext(ctx, http.MethodGet, googleDiscoveryURL, nil)
		var doc struct {
			Issuer                string   `json:"issuer"`
			AuthorizationEndpoint string   `json:"authorization_endpoint"`
			TokenEndpoint         string   `json:"token_endpoint"`
			JWKSURI               string   `json:"jwks_uri"`
			Algorithms            []string `json:"id_token_signing_alg_values_supported"`
		}
		h, err := readJSON(g.client, r, &doc)
		if err != nil {
			return struct{}{}, 0, err
		}
		if doc.Issuer != GoogleIssuer || doc.AuthorizationEndpoint != googleAuthorizationURL || doc.TokenEndpoint != googleTokenURL || doc.JWKSURI != googleJWKSURL || !slices.Contains(doc.Algorithms, "RS256") {
			return struct{}{}, 0, ErrRejected
		}
		return struct{}{}, cacheTTL(h), nil
	})
	return err
}

func (g *Google) loadKeys(ctx context.Context, force bool) (map[string]*rsa.PublicKey, error) {
	return g.keys.load(ctx, g.now, force, func() (map[string]*rsa.PublicKey, time.Duration, error) {
		r, _ := http.NewRequestWithContext(ctx, http.MethodGet, googleJWKSURL, nil)
		var doc struct {
			Keys []struct {
				KTY, Kid, Use, Alg, N, E string
				KeyOps                   []string `json:"key_ops"`
			} `json:"keys"`
		}
		h, err := readJSON(g.client, r, &doc)
		if err != nil {
			return nil, 0, err
		}
		if len(doc.Keys) == 0 || len(doc.Keys) > 32 {
			return nil, 0, ErrRejected
		}
		keys := make(map[string]*rsa.PublicKey)
		for _, key := range doc.Keys {
			if key.KTY != "RSA" || (key.Use != "" && key.Use != "sig") || (key.Alg != "" && key.Alg != "RS256") || (key.KeyOps != nil && !slices.Contains(key.KeyOps, "verify")) {
				continue
			}
			n, ne := base64.RawURLEncoding.Strict().DecodeString(key.N)
			e, ee := base64.RawURLEncoding.Strict().DecodeString(key.E)
			if key.Kid == "" || len(key.Kid) > 256 || keys[key.Kid] != nil || ne != nil || ee != nil || len(e) == 0 || len(e) > 4 || len(n) == 0 || n[0] == 0 {
				return nil, 0, ErrRejected
			}
			modulus := new(big.Int).SetBytes(n)
			exponent := new(big.Int).SetBytes(e).Int64()
			if modulus.BitLen() < 2048 || modulus.BitLen() > 8192 || exponent < 3 || exponent > 2147483647 || exponent%2 == 0 {
				return nil, 0, ErrRejected
			}
			keys[key.Kid] = &rsa.PublicKey{N: modulus, E: int(exponent)}
		}
		if len(keys) == 0 {
			return nil, 0, ErrRejected
		}
		return keys, cacheTTL(h), nil
	})
}

type googleClaims struct {
	jwt.RegisteredClaims
	Nonce           string `json:"nonce"`
	AuthorizedParty string `json:"azp"`
	Email           string `json:"email"`
	EmailVerified   bool   `json:"email_verified"`
	HostedDomain    string `json:"hd"`
	Name            string `json:"name"`
	Picture         string `json:"picture"`
	AccessTokenHash string `json:"at_hash"`
}

func (g *Google) verifyIDToken(ctx context.Context, raw, accessToken, nonce string) (ProviderIdentity, error) {
	var zero ProviderIdentity
	if ctx.Err() != nil {
		return zero, ErrUnavailable
	}
	signatureAttempted := false
	parse := func() (*googleClaims, error) {
		signatureAttempted = false
		claims := &googleClaims{}
		_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
			kid, ok := t.Header["kid"].(string)
			if !ok || kid == "" || len(kid) > 256 {
				return nil, ErrRejected
			}
			for _, field := range []string{"jku", "jwk", "x5u", "crit"} {
				if _, present := t.Header[field]; present {
					return nil, ErrRejected
				}
			}
			keys, err := g.loadKeys(ctx, false)
			if err != nil {
				return nil, err
			}
			if keys[kid] == nil {
				keys, err = g.loadKeys(ctx, true)
			}
			if err != nil {
				return nil, err
			}
			if keys[kid] == nil {
				return nil, ErrRejected
			}
			signatureAttempted = true
			return keys[kid], nil
		}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(g.cfg.ClientID), jwt.WithExpirationRequired(), jwt.WithTimeFunc(g.now), jwt.WithStrictDecoding())
		return claims, err
	}
	c, err := parse()
	if signatureAttempted && errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		if _, refreshErr := g.loadKeys(ctx, true); refreshErr != nil {
			return zero, refreshErr
		}
		c, err = parse()
	}
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return zero, ErrUnavailable
		}
		return zero, ErrRejected
	}
	if (c.Issuer != GoogleIssuer && c.Issuer != "accounts.google.com") || c.Subject == "" || len(c.Subject) > 255 || strings.TrimSpace(c.Subject) != c.Subject || strings.ContainsAny(c.Subject, "\r\n\x00") ||
		c.IssuedAt == nil || c.IssuedAt.Unix() <= 0 || c.IssuedAt.Time.After(g.now().Add(30*time.Second)) || !c.IssuedAt.Time.Before(c.ExpiresAt.Time) ||
		len(c.Audience) != 1 || c.Audience[0] != g.cfg.ClientID || (c.AuthorizedParty != "" && c.AuthorizedParty != g.cfg.ClientID) ||
		subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1 {
		return zero, ErrRejected
	}
	if c.AccessTokenHash != "" {
		h := sha256.Sum256([]byte(accessToken))
		want := base64.RawURLEncoding.EncodeToString(h[:len(h)/2])
		if accessToken == "" || subtle.ConstantTimeCompare([]byte(want), []byte(c.AccessTokenHash)) != 1 {
			return zero, ErrRejected
		}
	}
	email, err := identity.NormalizeEmail(c.Email)
	if err != nil || !c.EmailVerified || strings.Contains(strings.SplitN(email, "@", 2)[1], "noreply") {
		return zero, ErrRejected
	}
	trust := ProviderVerified
	domain := strings.SplitN(email, "@", 2)[1]
	if domain == "gmail.com" {
		trust = GoogleAuthoritative
	} else if c.HostedDomain != "" {
		// Only signed hd is considered. Matching a syntactically valid email
		// domain is a conservative additional check; suffix/client hints never count.
		hd := strings.ToLower(c.HostedDomain)
		if hd == domain {
			trust = GoogleAuthoritative
		}
	}
	name, nameErr := identity.NormalizeDisplayName(c.Name, true)
	if nameErr != nil {
		name = identity.DefaultDisplayName
	}
	avatar, avatarErr := identity.NormalizeAvatarURL(c.Picture)
	if avatarErr != nil {
		avatar = ""
	}
	if ctx.Err() != nil {
		return zero, ErrUnavailable
	}
	return ProviderIdentity{Provider: GoogleName, Issuer: GoogleIssuer, Subject: c.Subject, Email: email, EmailVerified: true,
		EmailTrust: trust, DisplayName: name, AvatarURL: avatar, AuthenticatedAt: c.IssuedAt.Time.UTC()}, nil
}

// Keep claims out of accidental structured logs as well as fmt formatting.
func (googleClaims) String() string         { return "[google claims redacted]" }
func (c googleClaims) GoString() string     { return c.String() }
func (c googleClaims) LogValue() slog.Value { return slog.StringValue(c.String()) }
