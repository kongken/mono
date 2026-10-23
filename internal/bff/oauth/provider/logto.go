package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

const maxLogtoResponseBytes = 1 << 20

// LogtoConfig configures the Logto provider. IssuerURL is the OIDC issuer of
// the tenant, for example https://<tenant>.logto.app/oidc; the authorize,
// token and userinfo endpoints are derived from it. RedirectURL must exactly
// match a redirect URI registered on the Logto "Traditional web" application.
type LogtoConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	DisplayName  string
	HTTPClient   *http.Client
}

// NewLogto builds a Logto provider. It returns nil when the issuer, client
// credentials or redirect URL are missing so callers can skip registering it.
func NewLogto(cfg LogtoConfig) *Logto {
	cfg.IssuerURL = strings.TrimRight(strings.TrimSpace(cfg.IssuerURL), "/")
	if cfg.IssuerURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	// Business roles (such as metro-admin) reach the session through the roles
	// scope, so it is always requested.
	if !slices.Contains(cfg.Scopes, "roles") {
		cfg.Scopes = append(slices.Clone(cfg.Scopes), "roles")
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = "Logto"
	}
	return &Logto{cfg: cfg}
}

// Logto implements PKCEProvider for Logto (standard OIDC authorization code
// flow with PKCE). Accounts are identified by the userinfo "sub" claim.
type Logto struct {
	cfg LogtoConfig
}

var _ PKCEProvider = (*Logto)(nil)

func (p *Logto) Name() string        { return "logto" }
func (p *Logto) DisplayName() string { return p.cfg.DisplayName }

// AuthorizeURL builds the authorization URL without a code challenge. Logto
// rejects it, so the AuthService uses AuthorizePKCEURL for this provider.
func (p *Logto) AuthorizeURL(state string) string {
	return p.authorizeURL(state, "")
}

func (p *Logto) AuthorizePKCEURL(state, codeChallenge string) string {
	return p.authorizeURL(state, codeChallenge)
}

func (p *Logto) authorizeURL(state, codeChallenge string) string {
	q := url.Values{}
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", p.cfg.RedirectURL)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(p.cfg.Scopes, " "))
	q.Set("state", state)
	if codeChallenge != "" {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "S256")
	}
	return p.cfg.IssuerURL + "/auth?" + q.Encode()
}

// Exchange always fails: Logto codes can only be redeemed with a verifier.
func (p *Logto) Exchange(context.Context, string) (*Claims, error) {
	return nil, errors.New("logto: code_verifier is required")
}

func (p *Logto) ExchangePKCE(ctx context.Context, code, codeVerifier string) (*Claims, error) {
	if codeVerifier == "" {
		return nil, errors.New("logto: code_verifier is required")
	}
	token, err := p.exchangeCode(ctx, code, codeVerifier)
	if err != nil {
		return nil, err
	}
	return p.fetchUser(ctx, token)
}

func (p *Logto) exchangeCode(ctx context.Context, code, codeVerifier string) (string, error) {
	body := url.Values{}
	body.Set("grant_type", "authorization_code")
	body.Set("code", code)
	body.Set("redirect_uri", p.cfg.RedirectURL)
	body.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.IssuerURL+"/token", strings.NewReader(body.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// client_secret_basic: credentials are form-encoded before base64 (RFC 6749 2.3.1).
	cred := url.QueryEscape(p.cfg.ClientID) + ":" + url.QueryEscape(p.cfg.ClientSecret)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cred)))

	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("logto: token request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxLogtoResponseBytes))
	if err != nil {
		return "", fmt.Errorf("logto: read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("logto: token request status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("logto: decode token response: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("logto: %s: %s", out.Error, out.ErrorDescription)
	}
	if out.AccessToken == "" {
		return "", errors.New("logto: empty access token in response")
	}
	return out.AccessToken, nil
}

func (p *Logto) fetchUser(ctx context.Context, token string) (*Claims, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.IssuerURL+"/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("logto: userinfo request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxLogtoResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("logto: read userinfo response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("logto: userinfo status %d: %s", resp.StatusCode, raw)
	}
	var u struct {
		Sub      string   `json:"sub"`
		Name     string   `json:"name"`
		Username string   `json:"username"`
		Picture  string   `json:"picture"`
		Email    string   `json:"email"`
		Roles    []string `json:"roles"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, fmt.Errorf("logto: decode userinfo response: %w", err)
	}
	sub := strings.TrimSpace(u.Sub)
	if sub == "" {
		return nil, errors.New("logto: userinfo response missing sub")
	}
	login := u.Username
	if login == "" {
		if at := strings.IndexByte(u.Email, '@'); at > 0 {
			login = u.Email[:at]
		}
	}
	return &Claims{Provider: "logto", ExternalID: sub, Login: login, Name: u.Name, Email: u.Email, AvatarURL: u.Picture, Roles: cleanRoles(u.Roles)}, nil
}

// cleanRoles keeps distinct, non-empty role names of reasonable size, in order.
func cleanRoles(in []string) []string {
	var out []string
	for _, r := range in {
		r = strings.TrimSpace(r)
		if r == "" || len(r) > 64 || slices.Contains(out, r) {
			continue
		}
		out = append(out, r)
		if len(out) == 32 {
			break
		}
	}
	return out
}
