package provider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kongken/mono/internal/bff/config"
)

func testLogtoConfig() LogtoConfig {
	return LogtoConfig{
		IssuerURL:    "https://auth.example.com/oidc/",
		ClientID:     "app-id",
		ClientSecret: "app-secret",
		RedirectURL:  "https://trip.example.com/auth/callback/logto",
	}
}

func TestLogto_NilWithoutRequiredConfig(t *testing.T) {
	for name, mutate := range map[string]func(*LogtoConfig){
		"client id":    func(c *LogtoConfig) { c.ClientID = "" },
		"secret":       func(c *LogtoConfig) { c.ClientSecret = "" },
		"issuer":       func(c *LogtoConfig) { c.IssuerURL = "" },
		"redirect url": func(c *LogtoConfig) { c.RedirectURL = "" },
	} {
		cfg := testLogtoConfig()
		mutate(&cfg)
		if NewLogto(cfg) != nil {
			t.Errorf("provider built without %s", name)
		}
	}
}

func TestLogto_Identity(t *testing.T) {
	p := NewLogto(testLogtoConfig())
	if p.Name() != "logto" || p.DisplayName() != "Logto" {
		t.Fatalf("name %q display %q", p.Name(), p.DisplayName())
	}
	cfg := testLogtoConfig()
	cfg.DisplayName = "统一登录"
	if got := NewLogto(cfg).DisplayName(); got != "统一登录" {
		t.Fatalf("display name = %q", got)
	}
}

func TestLogto_AuthorizeURLCarriesPKCE(t *testing.T) {
	p := NewLogto(testLogtoConfig())
	raw := p.AuthorizePKCEURL("state-xyz", "challenge-abc")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != "https://auth.example.com/oidc/auth" {
		t.Fatalf("authorize endpoint = %s", got)
	}
	q := u.Query()
	for key, want := range map[string]string{
		"client_id":             "app-id",
		"redirect_uri":          "https://trip.example.com/auth/callback/logto",
		"response_type":         "code",
		"scope":                 "openid profile email",
		"state":                 "state-xyz",
		"code_challenge":        "challenge-abc",
		"code_challenge_method": "S256",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if strings.Contains(raw, "app-secret") {
		t.Fatal("authorize URL leaks the client secret")
	}
}

func TestLogto_CustomScopes(t *testing.T) {
	cfg := testLogtoConfig()
	cfg.Scopes = []string{"openid", "profile"}
	q, _ := url.Parse(NewLogto(cfg).AuthorizePKCEURL("s", "c"))
	if got := q.Query().Get("scope"); got != "openid profile" {
		t.Fatalf("scope = %q", got)
	}
}

func TestLogto_PlainFlowsAreRefused(t *testing.T) {
	p := NewLogto(testLogtoConfig())
	if strings.Contains(p.AuthorizeURL("s"), "code_challenge") {
		t.Fatal("plain AuthorizeURL must not pretend to carry PKCE")
	}
	if _, err := p.Exchange(context.Background(), "code"); err == nil {
		t.Fatal("exchange without a code_verifier must fail")
	}
}

func TestPKCEPair(t *testing.T) {
	verifier, challenge, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	// RFC 7636: the verifier is 43-128 unreserved characters.
	if len(verifier) < 43 || len(verifier) > 128 || strings.Trim(verifier, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~") != "" {
		t.Fatalf("invalid verifier %q", verifier)
	}
	sum := sha256.Sum256([]byte(verifier))
	if challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatalf("challenge %q is not S256(verifier)", challenge)
	}
	other, _, _ := NewPKCE()
	if other == verifier {
		t.Fatal("verifiers must be random")
	}
	// Worked example from RFC 7636 appendix B.
	if got := PKCEChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("RFC 7636 vector: %q", got)
	}
}

type logtoServer struct {
	token      func(w http.ResponseWriter, r *http.Request)
	userinfo   func(w http.ResponseWriter, r *http.Request)
	tokenForm  url.Values
	tokenAuth  string
	tokenCalls int
}

func (l *logtoServer) start(t *testing.T) *Logto {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oidc/token", func(w http.ResponseWriter, r *http.Request) {
		l.tokenCalls++
		_ = r.ParseForm()
		l.tokenForm = r.PostForm
		l.tokenAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("token request %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		l.token(w, r)
	})
	mux.HandleFunc("/oidc/me", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-tok" {
			t.Errorf("userinfo auth = %q", got)
		}
		l.userinfo(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := testLogtoConfig()
	cfg.IssuerURL = srv.URL + "/oidc"
	cfg.HTTPClient = srv.Client()
	return NewLogto(cfg)
}

func okToken(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"access_token":"access-tok","token_type":"Bearer","expires_in":3600,"id_token":"x.y.z"}`))
}

func TestLogto_ExchangePKCE(t *testing.T) {
	srv := &logtoServer{
		token: okToken,
		userinfo: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sub":"u8x2k","name":"小山","username":"shanhai","picture":"https://cdn.example.com/a.png","email":"a@example.com","email_verified":true}`))
		},
	}
	p := srv.start(t)
	claims, err := p.ExchangePKCE(context.Background(), "the-code", "the-verifier")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "the-code",
		"code_verifier": "the-verifier",
		"redirect_uri":  "https://trip.example.com/auth/callback/logto",
	} {
		if got := srv.tokenForm.Get(key); got != want {
			t.Errorf("token form %s = %q, want %q", key, got, want)
		}
	}
	// Client authentication uses exactly one mechanism: HTTP Basic.
	if srv.tokenForm.Get("client_secret") != "" {
		t.Error("client secret must not be sent in the form body")
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("app-id:app-secret"))
	if srv.tokenAuth != wantAuth {
		t.Errorf("token authorization = %q", srv.tokenAuth)
	}
	want := Claims{Provider: "logto", ExternalID: "u8x2k", Login: "shanhai", Name: "小山", Email: "a@example.com", AvatarURL: "https://cdn.example.com/a.png"}
	if *claims != want {
		t.Fatalf("claims = %+v, want %+v", *claims, want)
	}
}

func TestLogto_BasicCredentialsAreFormEncoded(t *testing.T) {
	srv := &logtoServer{token: okToken, userinfo: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"sub":"u"}`)) }}
	p := srv.start(t)
	p.cfg.ClientID, p.cfg.ClientSecret = "app id", "s:e+c/ret"
	if _, err := p.ExchangePKCE(context.Background(), "c", "v"); err != nil {
		t.Fatal(err)
	}
	// RFC 6749 2.3.1: id and secret are form-encoded before Base64, so ':' in a secret is unambiguous.
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("app+id:s%3Ae%2Bc%2Fret"))
	if srv.tokenAuth != want {
		t.Fatalf("authorization = %q, want %q", srv.tokenAuth, want)
	}
}

func TestLogto_ExchangePKCEFailures(t *testing.T) {
	json := func(status int, body string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
	}
	userinfoOK := json(200, `{"sub":"u1"}`)
	cases := map[string]struct {
		token, userinfo func(http.ResponseWriter, *http.Request)
		verifier        string
	}{
		"token status":         {token: json(400, `{"error":"invalid_grant","error_description":"bad verifier"}`), userinfo: userinfoOK, verifier: "v"},
		"token error in 200":   {token: json(200, `{"error":"invalid_grant"}`), userinfo: userinfoOK, verifier: "v"},
		"token malformed":      {token: json(200, `not json`), userinfo: userinfoOK, verifier: "v"},
		"empty access token":   {token: json(200, `{"token_type":"Bearer"}`), userinfo: userinfoOK, verifier: "v"},
		"userinfo status":      {token: okToken, userinfo: json(401, `{"error":"invalid_token"}`), verifier: "v"},
		"userinfo malformed":   {token: okToken, userinfo: json(200, `<html>`), verifier: "v"},
		"userinfo missing sub": {token: okToken, userinfo: json(200, `{"name":"x"}`), verifier: "v"},
		"userinfo empty sub":   {token: okToken, userinfo: json(200, `{"sub":"  "}`), verifier: "v"},
		"missing verifier":     {token: okToken, userinfo: userinfoOK, verifier: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := &logtoServer{token: tc.token, userinfo: tc.userinfo}
			claims, err := srv.start(t).ExchangePKCE(context.Background(), "code", tc.verifier)
			if err == nil || claims != nil {
				t.Fatalf("got %+v, %v; want an error", claims, err)
			}
			if tc.verifier == "" && srv.tokenCalls != 0 {
				t.Fatal("token endpoint called without a verifier")
			}
		})
	}
}

func TestLogto_UsernameFallsBackToEmailLocalPart(t *testing.T) {
	srv := &logtoServer{
		token: okToken,
		userinfo: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"sub":"u2","email":"bob@example.com"}`))
		},
	}
	claims, err := srv.start(t).ExchangePKCE(context.Background(), "c", "v")
	if err != nil || claims.Login != "bob" || claims.Name != "" {
		t.Fatalf("claims = %+v, err = %v", claims, err)
	}
}

func TestBuildRegistryRegistersLogto(t *testing.T) {
	reg := BuildRegistry(config.AuthConfig{OAuthProviders: map[string]config.OAuthProvider{
		"logto": {IssuerURL: "https://auth.example.com/oidc", ClientID: "id", ClientSecret: "s", RedirectURL: "https://trip.example.com/auth/callback/logto", DisplayName: "统一登录"},
	}})
	p, err := reg.Get("logto")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(PKCEProvider); !ok {
		t.Fatal("logto must be a PKCEProvider")
	}
	if p.DisplayName() != "统一登录" {
		t.Fatalf("display name = %q", p.DisplayName())
	}
	// Without an issuer there is nothing to talk to, so it is not offered.
	reg = BuildRegistry(config.AuthConfig{OAuthProviders: map[string]config.OAuthProvider{
		"logto": {ClientID: "id", ClientSecret: "s", RedirectURL: "https://x"},
	}})
	if _, err := reg.Get("logto"); err == nil {
		t.Fatal("logto registered without issuer_url")
	}
}
