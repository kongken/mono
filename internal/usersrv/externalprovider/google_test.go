package externalprovider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var testSigningKeys = sync.OnceValues(func() (*rsa.PrivateKey, *rsa.PrivateKey) {
	a, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	b, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return a, b
})

type fakeGrant struct {
	request  CodeExchangeRequest
	claims   jwt.MapClaims
	key      *rsa.PrivateKey
	kid      string
	method   jwt.SigningMethod
	header   map[string]any
	consumed bool
}

// fakeGoogle is a complete local HTTP discovery/JWKS/token issuer. The adapter
// still sees the official URLs and exercises its real HTTP and JWT parsing.
// Grants enforce client, redirect URI, S256 and single-use like a provider.
type fakeGoogle struct {
	t                        *testing.T
	server                   *httptest.Server
	adapter                  *Google
	mu                       sync.Mutex
	grants                   map[string]*fakeGrant
	key                      *rsa.PrivateKey
	kid                      string
	clock                    atomic.Int64
	posts, discoveries, jwks atomic.Int64
	discoveryChange          func(map[string]any)
	keysChange               func(map[string]any)
	keyCache                 string
	keyStatus, tokenStatus   int
	tokenBody                string
	tokenChange              func(map[string]any)
	blockToken               bool
	done                     chan struct{}
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	k, _ := testSigningKeys()
	f := &fakeGoogle{t: t, grants: map[string]*fakeGrant{}, key: k, kid: "key-1", keyCache: "max-age=300", done: make(chan struct{})}
	f.clock.Store(time.Now().Unix())
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() { close(f.done); f.server.Close() })
	target, _ := url.Parse(f.server.URL)
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case googleDiscoveryURL, googleJWKSURL, googleTokenURL:
		default:
			return nil, errors.New("fake transport refused a non-official endpoint")
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		clone.URL, clone.Host = &u, r.URL.Host
		return f.server.Client().Transport.RoundTrip(clone)
	})
	var err error
	f.adapter, err = NewGoogle(GoogleConfig{Enabled: true, ClientID: "test-client", ClientSecret: "client-secret",
		RedirectURI: "https://api.example.com/api/identity/callback/google", ConfigVersion: 3}, rt)
	if err != nil {
		t.Fatal(err)
	}
	f.adapter.now = func() time.Time { return time.Unix(f.clock.Load(), 0) }
	return f
}

func (f *fakeGoogle) grant() *fakeGrant {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.clock.Load()
	verifier := strings.Repeat("v", 43)
	h := sha256.Sum256([]byte(verifier))
	g := &fakeGrant{
		request: CodeExchangeRequest{Provider: GoogleName, Code: fmt.Sprintf("code-%d", len(f.grants)), Verifier: verifier,
			Nonce: strings.Repeat("n", 43), CodeChallenge: base64.RawURLEncoding.EncodeToString(h[:]), ConfigVersion: 3},
		claims: jwt.MapClaims{"iss": GoogleIssuer, "sub": "stable-subject", "aud": "test-client", "iat": now, "exp": now + 3600,
			"nonce": strings.Repeat("n", 43), "email": "person@gmail.com", "email_verified": true, "name": "Test person", "picture": "https://images.example.com/avatar.png"},
		key: f.key, kid: f.kid, method: jwt.SigningMethodRS256, header: map[string]any{},
	}
	f.grants[g.request.Code] = g
	return g
}

func publicJWK(key *rsa.PrivateKey, kid string) map[string]any {
	return map[string]any{"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}
}

func (f *fakeGoogle) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		f.discoveries.Add(1)
		w.Header().Set("Cache-Control", "max-age=300")
		d := map[string]any{"issuer": GoogleIssuer, "authorization_endpoint": googleAuthorizationURL, "token_endpoint": googleTokenURL,
			"jwks_uri": googleJWKSURL, "id_token_signing_alg_values_supported": []string{"RS256"}}
		if f.discoveryChange != nil {
			f.discoveryChange(d)
		}
		_ = json.NewEncoder(w).Encode(d)
	case "/oauth2/v3/certs":
		f.jwks.Add(1)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.keyStatus != 0 {
			w.WriteHeader(f.keyStatus)
			return
		}
		w.Header().Set("Cache-Control", f.keyCache)
		d := map[string]any{"keys": []any{publicJWK(f.key, f.kid)}}
		if f.keysChange != nil {
			f.keysChange(d)
		}
		_ = json.NewEncoder(w).Encode(d)
	case "/token":
		f.posts.Add(1)
		if f.blockToken {
			select {
			case <-r.Context().Done():
			case <-f.done:
			}
			return
		}
		if r.Method != http.MethodPost || r.Host != "oauth2.googleapis.com" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			f.t.Error("incorrect token request method/host/content-type")
			w.WriteHeader(400)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		g := f.grants[r.Form.Get("code")]
		if g == nil || g.consumed || r.Form.Get("client_id") != "test-client" || r.Form.Get("client_secret") != "client-secret" ||
			r.Form.Get("redirect_uri") != "https://api.example.com/api/identity/callback/google" || r.Form.Get("grant_type") != "authorization_code" ||
			r.Form.Get("code_verifier") != g.request.Verifier {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"private-code"}`))
			return
		}
		g.consumed = true
		if len(r.Form) != 6 {
			f.t.Error("unexpected token parameters")
		}
		if f.tokenStatus != 0 {
			if f.tokenStatus == http.StatusFound {
				w.Header().Set("Location", f.server.URL+"/leaked")
			}
			w.WriteHeader(f.tokenStatus)
			_, _ = w.Write([]byte("private-provider-response"))
			return
		}
		if f.tokenBody != "" {
			_, _ = w.Write([]byte(f.tokenBody))
			return
		}
		token := jwt.NewWithClaims(g.method, g.claims)
		token.Header["kid"] = g.kid
		for k, v := range g.header {
			token.Header[k] = v
		}
		var signingKey any = g.key
		if g.method == jwt.SigningMethodHS256 {
			signingKey = []byte("wrong-key")
		}
		if g.method == jwt.SigningMethodNone {
			signingKey = jwt.UnsafeAllowNoneSignatureType
		}
		signed, err := token.SignedString(signingKey)
		if err != nil {
			f.t.Error(err)
			w.WriteHeader(500)
			return
		}
		d := map[string]any{"id_token": signed, "access_token": "temporary-access-token", "refresh_token": "must-be-discarded", "token_type": "Bearer"}
		if f.tokenChange != nil {
			f.tokenChange(d)
		}
		_ = json.NewEncoder(w).Encode(d)
	default:
		f.t.Errorf("unexpected provider request: %s", r.URL.Path)
		w.WriteHeader(404)
	}
}

func TestGoogleAuthorizationAndExchangeThroughProviderInterface(t *testing.T) {
	f := newFakeGoogle(t)
	var adapter ExternalProvider = f.adapter
	if f.discoveries.Load() != 0 || f.jwks.Load() != 0 || adapter.Name() != GoogleName {
		t.Fatal("constructor contacted provider")
	}
	g := f.grant()
	authorization, err := adapter.AuthorizationURL(context.Background(), AuthorizationRequest{State: strings.Repeat("s", 43), Nonce: g.request.Nonce, CodeChallenge: g.request.CodeChallenge})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authorization)
	q := u.Query()
	if u.Scheme+"://"+u.Host+u.Path != googleAuthorizationURL || len(q) != 8 || q.Get("scope") != "openid email profile" ||
		q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != g.request.CodeChallenge ||
		q.Get("redirect_uri") != f.adapter.cfg.RedirectURI || q.Get("nonce") != g.request.Nonce || q.Get("state") != strings.Repeat("s", 43) || q.Get("client_secret") != "" {
		t.Fatal("incorrect authorization parameters")
	}
	i, err := adapter.ExchangeCode(context.Background(), g.request)
	if err != nil || i.Provider != GoogleName || i.Issuer != GoogleIssuer || i.Subject != "stable-subject" || i.EmailTrust != GoogleAuthoritative || !i.EmailVerified {
		t.Fatalf("exchange failed: %v", err)
	}
	if f.discoveries.Load() != 1 || f.jwks.Load() != 1 || f.posts.Load() != 1 {
		t.Fatal("unexpected HTTP calls")
	}
	// A consumed provider grant is not re-exchanged automatically or accepted.
	if _, err := adapter.ExchangeCode(context.Background(), g.request); !errors.Is(err, ErrRejected) || f.posts.Load() != 2 {
		t.Fatal("provider code replay accepted")
	}
	g2 := f.grant()
	g2.claims["email"], g2.claims["name"], g2.claims["picture"] = "renamed@example.com", "New name", "https://images.example.com/new.png"
	i2, err := adapter.ExchangeCode(context.Background(), g2.request)
	if err != nil || i2.Subject != i.Subject || i2.Issuer != i.Issuer || i2.EmailTrust != ProviderVerified || f.jwks.Load() != 1 {
		t.Fatal("profile changes affected identity or JWKS cache")
	}
}

func TestGoogleRejectsInvalidSignedClaimsAndJOSE(t *testing.T) {
	_, otherKey := testSigningKeys()
	cases := map[string]func(*fakeGrant){
		"wrong signature":            func(g *fakeGrant) { g.key = otherKey },
		"wrong issuer":               func(g *fakeGrant) { g.claims["iss"] = "https://attacker.example.com" },
		"missing issuer":             func(g *fakeGrant) { delete(g.claims, "iss") },
		"wrong audience":             func(g *fakeGrant) { g.claims["aud"] = "other-client" },
		"missing audience":           func(g *fakeGrant) { delete(g.claims, "aud") },
		"unapproved azp":             func(g *fakeGrant) { g.claims["azp"] = "other-client" },
		"multi audience without azp": func(g *fakeGrant) { g.claims["aud"] = []string{"test-client", "other"} },
		"missing sub":                func(g *fakeGrant) { delete(g.claims, "sub") },
		"expired":                    func(g *fakeGrant) { g.claims["exp"] = int64(1) },
		"missing exp":                func(g *fakeGrant) { delete(g.claims, "exp") },
		"missing iat":                func(g *fakeGrant) { delete(g.claims, "iat") },
		"invalid iat":                func(g *fakeGrant) { g.claims["iat"] = -1 },
		"untrusted additional audience": func(g *fakeGrant) {
			g.claims["aud"] = []string{"test-client", "other"}
			g.claims["azp"] = "test-client"
		},
		"future iat":     func(g *fakeGrant) { g.claims["iat"] = time.Now().Add(time.Hour).Unix() },
		"future nbf":     func(g *fakeGrant) { g.claims["nbf"] = time.Now().Add(time.Hour).Unix() },
		"wrong nonce":    func(g *fakeGrant) { g.claims["nonce"] = "private-wrong-nonce" },
		"missing nonce":  func(g *fakeGrant) { delete(g.claims, "nonce") },
		"unknown key":    func(g *fakeGrant) { g.kid = "unknown" },
		"empty key id":   func(g *fakeGrant) { g.kid = "" },
		"HS256":          func(g *fakeGrant) { g.method = jwt.SigningMethodHS256 },
		"none":           func(g *fakeGrant) { g.method = jwt.SigningMethodNone },
		"remote key URL": func(g *fakeGrant) { g.header["jku"] = "https://attacker.example.com/keys" },
		"critical JOSE":  func(g *fakeGrant) { g.header["crit"] = []string{"unknown"} },
		"wrong at hash":  func(g *fakeGrant) { g.claims["at_hash"] = "wrong" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeGoogle(t)
			g := f.grant()
			change(g)
			if got, err := f.adapter.ExchangeCode(context.Background(), g.request); !errors.Is(err, ErrRejected) || got != (ProviderIdentity{}) {
				t.Fatalf("invalid proof accepted: %v", err)
			}
			if f.posts.Load() != 1 {
				t.Fatal("code exchange retried")
			}
			if (name == "HS256" || name == "none" || name == "remote key URL" || name == "critical JOSE") && f.jwks.Load() != 0 {
				t.Fatal("unsupported JOSE triggered a key fetch")
			}
		})
	}
	// Both documented Google issuer spellings canonicalize to the same key.
	f := newFakeGoogle(t)
	g := f.grant()
	g.claims["iss"] = "accounts.google.com"
	g.claims["aud"] = "test-client"
	g.claims["azp"] = "test-client"
	h := sha256.Sum256([]byte("temporary-access-token"))
	g.claims["at_hash"] = base64.RawURLEncoding.EncodeToString(h[:len(h)/2])
	if i, err := f.adapter.ExchangeCode(context.Background(), g.request); err != nil || i.Issuer != GoogleIssuer {
		t.Fatalf("valid OIDC claims rejected: %v", err)
	}
}

func TestGoogleEmailTrustUsesVerifiedSignedClaims(t *testing.T) {
	for _, tc := range []struct {
		name, email string
		verified    bool
		hd          string
		trust       EmailTrust
		rejected    bool
	}{
		{"gmail", "person@gmail.com", true, "", GoogleAuthoritative, false},
		{"workspace", "person@example.com", true, "example.com", GoogleAuthoritative, false},
		{"external google email", "person@example.com", true, "", ProviderVerified, false},
		{"mismatched hd", "person@example.com", true, "other.example.com", ProviderVerified, false},
		{"suffix trap", "person@gmail.com.example.com", true, "", ProviderVerified, false},
		{"unverified gmail", "person@gmail.com", false, "", Unverified, true},
		{"unverified workspace", "person@example.com", false, "example.com", Unverified, true},
		{"missing", "", true, "", Unverified, true},
		{"invalid", "not-an-email", true, "", Unverified, true},
		{"noreply", "123@users.noreply.github.com", true, "", Unverified, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGoogle(t)
			g := f.grant()
			g.claims["email"], g.claims["email_verified"], g.claims["hd"] = tc.email, tc.verified, tc.hd
			i, err := f.adapter.ExchangeCode(context.Background(), g.request)
			if tc.rejected {
				if !errors.Is(err, ErrRejected) || i != (ProviderIdentity{}) {
					t.Fatal("unusable email accepted")
				}
				return
			}
			if err != nil || i.EmailTrust != tc.trust {
				t.Fatalf("trust=%s err=%v", i.EmailTrust, err)
			}
		})
	}
}

func TestGoogleFlowValidationRejectsBeforeTokenExchange(t *testing.T) {
	for name, change := range map[string]func(*CodeExchangeRequest){
		"wrong provider":    func(r *CodeExchangeRequest) { r.Provider = GitHubName },
		"old configuration": func(r *CodeExchangeRequest) { r.ConfigVersion = 2 },
		"empty code":        func(r *CodeExchangeRequest) { r.Code = "" },
		"short verifier":    func(r *CodeExchangeRequest) { r.Verifier = "short" },
		"long verifier":     func(r *CodeExchangeRequest) { r.Verifier = strings.Repeat("x", 129) },
		"wrong verifier":    func(r *CodeExchangeRequest) { r.Verifier = strings.Repeat("x", 43) },
		"wrong challenge":   func(r *CodeExchangeRequest) { r.CodeChallenge = strings.Repeat("x", 43) },
		"short nonce":       func(r *CodeExchangeRequest) { r.Nonce = "short" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGoogle(t)
			r := f.grant().request
			change(&r)
			if _, err := f.adapter.ExchangeCode(context.Background(), r); !errors.Is(err, ErrRejected) || f.posts.Load() != 0 || f.discoveries.Load() != 0 {
				t.Fatal("invalid flow reached provider")
			}
		})
	}
	// Changing both local verifier and challenge still cannot defeat the
	// provider's binding of the authorization code to its original S256 value.
	f := newFakeGoogle(t)
	r := f.grant().request
	r.Verifier = strings.Repeat("x", 43)
	h := sha256.Sum256([]byte(r.Verifier))
	r.CodeChallenge = base64.RawURLEncoding.EncodeToString(h[:])
	if _, err := f.adapter.ExchangeCode(context.Background(), r); !errors.Is(err, ErrRejected) || f.posts.Load() != 1 {
		t.Fatal("provider PKCE binding bypassed")
	}
	for _, req := range []AuthorizationRequest{{}, {State: strings.Repeat("s", 43), Nonce: "", CodeChallenge: strings.Repeat("c", 43)}} {
		if _, err := f.adapter.AuthorizationURL(context.Background(), req); !errors.Is(err, ErrRejected) {
			t.Fatal("invalid authorization input")
		}
	}
}

func TestGoogleConfigurationAndDisabledProviderNeverContactNetwork(t *testing.T) {
	var calls atomic.Int64
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("network must not be used")
	})
	g, err := NewGoogle(GoogleConfig{}, rt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AuthorizationURL(context.Background(), AuthorizationRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled authorization")
	}
	if _, err := g.ExchangeCode(context.Background(), CodeExchangeRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled exchange")
	}
	base := GoogleConfig{Enabled: true, ClientID: "test-client", ClientSecret: "private-secret", RedirectURI: "https://api.example.com/api/identity/callback/google", ConfigVersion: 1}
	for name, change := range map[string]func(*GoogleConfig){
		"no client":               func(c *GoogleConfig) { c.ClientID = "" },
		"no secret":               func(c *GoogleConfig) { c.ClientSecret = "" },
		"no version":              func(c *GoogleConfig) { c.ConfigVersion = 0 },
		"wrong provider callback": func(c *GoogleConfig) { c.RedirectURI = "https://api.example.com/api/identity/callback/github" },
		"HTTP":                    func(c *GoogleConfig) { c.RedirectURI = "http://api.example.com/api/identity/callback/google" },
		"userinfo": func(c *GoogleConfig) {
			c.RedirectURI = "https://user:private-secret@api.example.com/api/identity/callback/google"
		},
		"query":    func(c *GoogleConfig) { c.RedirectURI += "?code=private-code" },
		"fragment": func(c *GoogleConfig) { c.RedirectURI += "#private-code" },
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			change(&c)
			if _, err := NewGoogle(c, rt); err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatal("unsafe config accepted or exposed")
			}
		})
	}
	if _, err := NewGoogle(base, rt); err != nil || calls.Load() != 0 {
		t.Fatal("valid configuration contacted provider")
	}
}

func TestGoogleDiscoveryPinsIssuerAndEveryEndpoint(t *testing.T) {
	for _, field := range []string{"issuer", "authorization_endpoint", "token_endpoint", "jwks_uri", "id_token_signing_alg_values_supported"} {
		t.Run(field, func(t *testing.T) {
			f := newFakeGoogle(t)
			f.discoveryChange = func(d map[string]any) {
				if field == "id_token_signing_alg_values_supported" {
					d[field] = []string{"HS256"}
				} else {
					d[field] = "https://attacker.example.com/private-code"
				}
			}
			if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); !errors.Is(err, ErrRejected) || f.posts.Load() != 0 || f.jwks.Load() != 0 {
				t.Fatal("untrusted discovery used")
			}
		})
	}
}

func TestGoogleJWKSCacheRotationAndConcurrentFetches(t *testing.T) {
	t.Run("rotation on unknown kid and cooldown", func(t *testing.T) {
		f := newFakeGoogle(t)
		if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); err != nil {
			t.Fatal(err)
		}
		_, newKey := testSigningKeys()
		f.key, f.kid = newKey, "key-2"
		if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); err != nil || f.jwks.Load() != 2 {
			t.Fatalf("key rotation failed: %v", err)
		}
		for range 4 {
			g := f.grant()
			g.kid = "unknown"
			if _, err := f.adapter.ExchangeCode(context.Background(), g.request); !errors.Is(err, ErrRejected) {
				t.Fatal("unknown key accepted")
			}
		}
		if f.jwks.Load() != 2 {
			t.Fatal("unknown-kid requests caused unbounded refreshes")
		}
		f.clock.Add(31)
		g := f.grant()
		g.kid = "unknown"
		if _, err := f.adapter.ExchangeCode(context.Background(), g.request); !errors.Is(err, ErrRejected) || f.jwks.Load() != 3 {
			t.Fatal("refresh cooldown never expires")
		}
	})
	t.Run("rotation reusing kid", func(t *testing.T) {
		f := newFakeGoogle(t)
		if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); err != nil {
			t.Fatal(err)
		}
		_, f.key = testSigningKeys()
		if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); err != nil || f.jwks.Load() != 2 {
			t.Fatalf("signature rotation failed: %v", err)
		}
	})
	t.Run("expired cache fails closed", func(t *testing.T) {
		f := newFakeGoogle(t)
		if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); err != nil {
			t.Fatal(err)
		}
		f.clock.Add(301)
		f.keyStatus = 503
		if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); !errors.Is(err, ErrUnavailable) {
			t.Fatal("expired key cache used through outage")
		}
	})
	t.Run("no-store does not bypass forced-refresh cooldown", func(t *testing.T) {
		f := newFakeGoogle(t)
		f.keyCache = "no-store"
		for range 3 {
			g := f.grant()
			g.kid = "unknown"
			if _, err := f.adapter.ExchangeCode(context.Background(), g.request); !errors.Is(err, ErrRejected) {
				t.Fatal("unknown key accepted")
			}
		}
		// Each call must fetch normally under no-store, with only one extra
		// forced refresh across all three attacker-controlled unknown kids.
		if f.jwks.Load() != 4 {
			t.Fatalf("unbounded forced refresh: %d", f.jwks.Load())
		}
	})
	t.Run("coalesced concurrent cold cache", func(t *testing.T) {
		f := newFakeGoogle(t)
		requests := make([]CodeExchangeRequest, 12)
		for i := range requests {
			requests[i] = f.grant().request
		}
		start := make(chan struct{})
		results := make(chan error, len(requests))
		for _, r := range requests {
			go func() { <-start; _, err := f.adapter.ExchangeCode(context.Background(), r); results <- err }()
		}
		close(start)
		for range requests {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		if f.discoveries.Load() != 1 || f.jwks.Load() != 1 || f.posts.Load() != 12 {
			t.Fatal("metadata/JWKS stampede")
		}
	})
}

func TestGoogleMalformedJWKSCannotSupplyVerificationKeys(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"empty":                     func(d map[string]any) { d["keys"] = []any{} },
		"bad modulus":               func(d map[string]any) { d["keys"].([]any)[0].(map[string]any)["n"] = "bad!" },
		"even exponent":             func(d map[string]any) { d["keys"].([]any)[0].(map[string]any)["e"] = "Ag" },
		"weak modulus":              func(d map[string]any) { d["keys"].([]any)[0].(map[string]any)["n"] = "AQ" },
		"enc use":                   func(d map[string]any) { d["keys"].([]any)[0].(map[string]any)["use"] = "enc" },
		"encryption key operations": func(d map[string]any) { d["keys"].([]any)[0].(map[string]any)["key_ops"] = []string{"encrypt"} },
		"wrong algorithm":           func(d map[string]any) { d["keys"].([]any)[0].(map[string]any)["alg"] = "HS256" },
		"duplicate kid":             func(d map[string]any) { key := d["keys"].([]any)[0]; d["keys"] = []any{key, key} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGoogle(t)
			f.keysChange = change
			if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); !errors.Is(err, ErrRejected) {
				t.Fatal("malformed key set accepted")
			}
		})
	}
}

func TestGoogleHTTPFailuresAreBoundedNeutralAndNeverRetryCode(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{{400, ErrRejected}, {302, ErrRejected}, {429, ErrUnavailable}, {503, ErrUnavailable}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			f := newFakeGoogle(t)
			f.tokenStatus = tc.status
			if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); !errors.Is(err, tc.want) || strings.Contains(err.Error(), "private") || f.posts.Load() != 1 {
				t.Fatal("HTTP error was retried or exposed")
			}
		})
	}
	for _, body := range []string{`null`, `{`, `{"token_type":"Bearer"}`, strings.Repeat("x", maxResponseBytes+1)} {
		f := newFakeGoogle(t)
		f.tokenBody = body
		if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); !errors.Is(err, ErrRejected) || f.posts.Load() != 1 {
			t.Fatal("invalid token response accepted/retried")
		}
	}
	f := newFakeGoogle(t)
	f.blockToken = true
	// Warm discovery so the deadline exercises cancellation of the token POST.
	r := f.grant().request
	if _, err := f.adapter.AuthorizationURL(context.Background(), AuthorizationRequest{State: strings.Repeat("s", 43), Nonce: r.Nonce, CodeChallenge: r.CodeChallenge}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := f.adapter.ExchangeCode(ctx, r); !errors.Is(err, ErrUnavailable) || f.posts.Load() != 1 {
		t.Fatal("timeout retried or ignored")
	}
}

func TestGoogleTokenSchemaAndInvalidProfileDefaults(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"no id token":      func(d map[string]any) { delete(d, "id_token") },
		"wrong token type": func(d map[string]any) { d["token_type"] = "MAC" },
		"non string token": func(d map[string]any) { d["id_token"] = 123 },
		"oauth error":      func(d map[string]any) { d["error"] = "private-provider-error" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGoogle(t)
			f.tokenChange = change
			if _, err := f.adapter.ExchangeCode(context.Background(), f.grant().request); !errors.Is(err, ErrRejected) {
				t.Fatal("wrong token schema accepted")
			}
		})
	}
	f := newFakeGoogle(t)
	g := f.grant()
	g.claims["email_verified"] = "true"
	if _, err := f.adapter.ExchangeCode(context.Background(), g.request); !errors.Is(err, ErrRejected) {
		t.Fatal("string email_verified accepted")
	}
	g = f.grant()
	g.claims["name"], g.claims["picture"] = "\x00bad", "javascript:alert(1)"
	i, err := f.adapter.ExchangeCode(context.Background(), g.request)
	if err != nil || i.DisplayName != "旅行者" || i.AvatarURL != "" {
		t.Fatal("invalid profile not replaced by local defaults")
	}
}

func TestGoogleSecretsAndIdentityAreRedactedWhenLogged(t *testing.T) {
	f := newFakeGoogle(t)
	g := f.grant()
	i, err := f.adapter.ExchangeCode(context.Background(), g.request)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []any{f.adapter, f.adapter.cfg, g.request, AuthorizationRequest{State: "private-state"}, i, googleClaims{Email: "person@gmail.com"}} {
		formatted := fmt.Sprintf("%v %+v %#v", object, object, object)
		var text, structured bytes.Buffer
		slog.New(slog.NewTextHandler(&text, nil)).Info("object", "value", object)
		slog.New(slog.NewJSONHandler(&structured, nil)).Info("object", "value", object)
		for _, output := range []string{formatted, text.String(), structured.String()} {
			for _, secret := range []string{"client-secret", g.request.Code, g.request.Verifier, g.request.Nonce, "person@gmail.com", "stable-subject", "private-state", "temporary-access-token", "must-be-discarded"} {
				if strings.Contains(output, secret) {
					t.Fatal("sensitive provider material leaked in logs")
				}
			}
		}
	}
}

func TestProviderCacheHeadersBoundRetention(t *testing.T) {
	for _, tc := range []struct {
		control, age string
		want         time.Duration
	}{
		{"", "", 5 * time.Minute}, {"max-age=60", "10", 50 * time.Second}, {"max-age=999999", "", 6 * time.Hour},
		{"no-store", "", 0}, {"no-cache", "", 0}, {"max-age=0", "", 0}, {"max-age=bad", "", 0}, {"max-age=60", "120", 0},
		{`no-cache="keys"`, "", 0}, {"max-age=0, max-age=60", "", 0},
	} {
		h := http.Header{}
		h.Set("Cache-Control", tc.control)
		h.Set("Age", tc.age)
		if got := cacheTTL(h); got != tc.want {
			t.Fatalf("cache %q/%q = %v", tc.control, tc.age, got)
		}
	}
}
