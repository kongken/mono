package externalprovider

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kongken/mono/internal/usersrv/identity"
)

const (
	GitHubIssuer           = "https://github.com"
	githubAuthorizationURL = GitHubIssuer + "/login/oauth/authorize"
	githubTokenURL         = GitHubIssuer + "/login/oauth/access_token"
	githubUserURL          = "https://api.github.com/user"
	githubEmailsURL        = githubUserURL + "/emails"
	githubAPIVersion       = "2026-03-10"
	githubEmailPageSize    = 100
	githubMaxEmailPages    = 5
)

// GitHubConfig has the same offline validation and immutable gate/generation
// semantics as GoogleConfig. No caller can supply a provider endpoint.
type GitHubConfig struct {
	Enabled           bool
	ClientID          string
	ClientSecret      string `json:"-"`
	RedirectURI       string
	ConfigVersion     int64
	AllowLoopbackHTTP bool
}

func (GitHubConfig) String() string         { return "[github config redacted]" }
func (c GitHubConfig) GoString() string     { return c.String() }
func (c GitHubConfig) LogValue() slog.Value { return slog.StringValue(c.String()) }

type GitHub struct {
	cfg    GitHubConfig
	client *http.Client
	now    func() time.Time
}

var _ ExternalProvider = (*GitHub)(nil)

func NewGitHub(cfg GitHubConfig, transport http.RoundTripper) (*GitHub, error) {
	g := &GitHub{cfg: cfg, client: newHTTPClient(transport), now: time.Now}
	if err := g.ValidateConfiguration(); err != nil {
		return nil, err
	}
	return g, nil
}

func (*GitHub) Name() ProviderName     { return GitHubName }
func (*GitHub) String() string         { return "[github adapter redacted]" }
func (g *GitHub) GoString() string     { return g.String() }
func (g *GitHub) LogValue() slog.Value { return slog.StringValue(g.String()) }

func (g *GitHub) ValidateConfiguration() error {
	if !g.cfg.Enabled {
		return nil
	}
	c := g.cfg
	return validateClientConfiguration(GitHubName, c.ClientID, c.ClientSecret, c.RedirectURI, c.ConfigVersion, c.AllowLoopbackHTTP)
}

func (g *GitHub) AuthorizationURL(ctx context.Context, req AuthorizationRequest) (string, error) {
	if !g.cfg.Enabled {
		return "", ErrDisabled
	}
	if !flowValue.MatchString(req.State) || !s256Challenge.MatchString(req.CodeChallenge) {
		return "", ErrRejected
	}
	if ctx.Err() != nil {
		return "", ErrUnavailable
	}
	u, _ := url.Parse(githubAuthorizationURL)
	u.RawQuery = url.Values{
		"client_id": {g.cfg.ClientID}, "redirect_uri": {g.cfg.RedirectURI}, "response_type": {"code"},
		"scope": {"user:email"}, "state": {req.State},
		"code_challenge": {req.CodeChallenge}, "code_challenge_method": {"S256"},
	}.Encode()
	return u.String(), nil
}

// ExchangeCode accepts no client-supplied token or profile. The authenticated
// code exchange binds the token to our OAuth client, callback and original
// PKCE proof. Every exchange re-fetches /user; no token/profile cache is used.
// State comparison/single consumption belong to the later BFF challenge layer;
// GitHub OAuth has no OIDC nonce or ID token, so Nonce is intentionally unused.
func (g *GitHub) ExchangeCode(ctx context.Context, req CodeExchangeRequest) (ProviderIdentity, error) {
	var zero ProviderIdentity
	if !g.cfg.Enabled {
		return zero, ErrDisabled
	}
	digest := sha256.Sum256([]byte(req.Verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	if req.Provider != GitHubName || req.ConfigVersion != g.cfg.ConfigVersion || req.Code == "" || len(req.Code) > 4096 || strings.ContainsAny(req.Code, "\r\n\x00") ||
		!pkceVerifier.MatchString(req.Verifier) || !s256Challenge.MatchString(req.CodeChallenge) ||
		subtle.ConstantTimeCompare([]byte(challenge), []byte(req.CodeChallenge)) != 1 {
		return zero, ErrRejected
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	form := url.Values{"client_id": {g.cfg.ClientID}, "client_secret": {g.cfg.ClientSecret}, "code": {req.Code},
		"code_verifier": {req.Verifier}, "redirect_uri": {g.cfg.RedirectURI}}.Encode()
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, githubTokenURL, strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
	}
	// Exactly one POST, including after timeouts/unknown outcomes. Refresh tokens
	// and all other response fields are ignored and never retained.
	if _, err := readJSON(g.client, r, &token); err != nil {
		return zero, err
	}
	if token.Error != "" || !strings.EqualFold(token.TokenType, "bearer") || !validBearerToken(token.AccessToken) || token.Scope != "user:email" {
		return zero, ErrRejected
	}
	var user struct {
		ID        json.RawMessage `json:"id"`
		Type      string          `json:"type"`
		Name      string          `json:"name"`
		AvatarURL string          `json:"avatar_url"`
	}
	if err := g.apiJSON(ctx, githubUserURL, token.AccessToken, &user); err != nil {
		return zero, err
	}
	// No float64 conversion: even IDs above 2^53 keep every digit. Quoted IDs,
	// exponent/fraction/signed forms and zero are not canonical numeric subjects.
	subject := string(user.ID)
	if !githubSubject.MatchString(subject) || user.Type != "User" {
		return zero, ErrRejected
	}
	if _, err := strconv.ParseUint(subject, 10, 64); err != nil {
		return zero, ErrRejected
	}
	email, err := g.verifiedEmail(ctx, token.AccessToken)
	if err != nil {
		return zero, err
	}
	name, err := identity.NormalizeDisplayName(user.Name, true)
	if err != nil {
		name = identity.DefaultDisplayName
	}
	avatar, err := identity.NormalizeAvatarURL(user.AvatarURL)
	if err != nil {
		avatar = ""
	}
	if ctx.Err() != nil {
		return zero, ErrUnavailable
	}
	return ProviderIdentity{Provider: GitHubName, Issuer: GitHubIssuer, Subject: subject, Email: email, EmailVerified: true,
		EmailTrust: ProviderVerified, DisplayName: name, AvatarURL: avatar, AuthenticatedAt: g.now().UTC()}, nil
}

var githubSubject = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func validBearerToken(token string) bool {
	if token == "" || len(token) > 8192 {
		return false
	}
	for _, c := range token {
		if c <= ' ' || c >= 127 {
			return false
		}
	}
	return true
}

func (g *GitHub) apiJSON(ctx context.Context, endpoint, token string, dst any) error {
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	r.Header.Set("User-Agent", "kongken-usersrv")
	_, err := readJSON(g.client, r, dst)
	return err
}

func (g *GitHub) verifiedEmail(ctx context.Context, token string) (string, error) {
	email := ""
	for page := 1; page <= githubMaxEmailPages; page++ {
		var addresses []struct {
			Email             string `json:"email"`
			Primary, Verified bool
		}
		// Construct bounded pagination ourselves. Never follow provider Link or
		// profile URLs with a bearer credential, even if returned by GitHub.
		endpoint := githubEmailsURL + "?per_page=100&page=" + strconv.Itoa(page)
		if err := g.apiJSON(ctx, endpoint, token, &addresses); err != nil {
			return "", err
		}
		if len(addresses) > githubEmailPageSize {
			return "", ErrRejected
		}
		for _, a := range addresses {
			if !a.Primary {
				continue
			}
			candidate, err := identity.NormalizeEmail(a.Email)
			if err != nil || !a.Verified || email != "" {
				return "", ErrRejected
			}
			parts := strings.SplitN(candidate, "@", 2)
			if strings.Contains(parts[1], "noreply") || parts[0] == "noreply" || parts[0] == "no-reply" {
				return "", ErrRejected
			}
			email = candidate
		}
		if len(addresses) < githubEmailPageSize {
			if email == "" {
				return "", ErrRejected
			}
			return email, nil
		}
	}
	// An incomplete/oversized email list is not evidence of a unique primary.
	return "", ErrRejected
}
