package externalprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type githubResponse struct {
	status int
	body   string
	header http.Header
	block  bool
}

// The transport redirects only official, pinned URLs to a local TLS server.
// The server independently validates OAuth client/callback/verifier, single-use
// grants and API bearer credentials. The real adapter parses every response.
type fakeGitHub struct {
	t        *testing.T
	server   *httptest.Server
	adapter  *GitHub
	done     chan struct{}
	mu       sync.Mutex
	grants   map[string]string
	replies  map[string]githubResponse
	requests []string
	posts    atomic.Int64
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{t: t, done: make(chan struct{}), grants: map[string]string{}, replies: map[string]githubResponse{
		"/user":        {body: `{"id":9007199254740993,"type":"User","login":"mutable-login","name":"Octocat","email":"untrusted-profile@example.com","avatar_url":"https://github.com/avatar.png","url":"https://evil.example.com/profile"}`},
		"/user/emails": {body: `[{"email":"ignored@example.com","primary":false,"verified":true},{"email":" Octocat+tag@Example.COM ","primary":true,"verified":true,"visibility":"private"}]`},
	}}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() { close(f.done); f.server.Close() })
	target, _ := url.Parse(f.server.URL)
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		valid := r.URL.String() == githubTokenURL || r.URL.String() == githubUserURL ||
			(r.URL.Scheme == "https" && r.URL.Host == "api.github.com" && r.URL.Path == "/user/emails" && r.URL.Query().Get("per_page") == "100" && r.URL.Query().Get("page") != "")
		if !valid || r.URL.User != nil || r.URL.Fragment != "" {
			t.Error("adapter attempted unpinned URL")
			return nil, errors.New("untrusted endpoint")
		}
		local := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		local.URL = &u
		local.Host = r.URL.Host
		return f.server.Client().Transport.RoundTrip(local)
	})
	var err error
	f.adapter, err = NewGitHub(GitHubConfig{Enabled: true, ClientID: "github-client", ClientSecret: "github-secret",
		RedirectURI: "https://api.example.com/api/identity/callback/github", ConfigVersion: 7}, rt)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeGitHub) issue() CodeExchangeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	verifier := strings.Repeat("v", 64)
	digest := sha256.Sum256([]byte(verifier))
	code := fmt.Sprintf("local-grant-%d", len(f.grants)+len(f.requests)+1)
	f.grants[code] = verifier
	return CodeExchangeRequest{Provider: GitHubName, Code: code, Verifier: verifier,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]), ConfigVersion: 7}
}

func (f *fakeGitHub) respond(path string, response githubResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[path] = response
}

func (f *fakeGitHub) requestPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.RequestURI())
	response, override := f.replies[r.URL.RequestURI()]
	if !override {
		response, override = f.replies[r.URL.Path]
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/login/oauth/access_token" {
		f.posts.Add(1)
		if r.Method != http.MethodPost || r.Host != "github.com" || r.URL.RawQuery != "" || r.ParseForm() != nil ||
			r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" ||
			r.PostForm.Get("client_id") != "github-client" || r.PostForm.Get("client_secret") != "github-secret" ||
			r.PostForm.Get("redirect_uri") != f.adapter.cfg.RedirectURI || len(r.PostForm) != 5 || r.Header.Get("Authorization") != "" {
			f.t.Error("invalid OAuth client/callback/token request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		verifier, ok := f.grants[r.PostForm.Get("code")]
		valid := ok && verifier == r.PostForm.Get("code_verifier")
		if valid {
			delete(f.grants, r.PostForm.Get("code"))
		}
		f.mu.Unlock()
		if !valid {
			fmt.Fprint(w, `{"error":"bad_verification_code","error_description":"sensitive upstream explanation"}`)
			return
		}
		if !override {
			response.body = `{"access_token":"gho_local-secret-token","token_type":"bearer","scope":"user:email","refresh_token":"never-retained"}`
		}
	} else if r.Host != "api.github.com" || r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer gho_local-secret-token" ||
		r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != githubAPIVersion || r.Header.Get("User-Agent") != "kongken-usersrv" {
		f.t.Error("invalid authenticated GitHub API request")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if response.block {
		select {
		case <-r.Context().Done():
		case <-f.done:
		}
		return
	}
	for k, values := range response.header {
		w.Header()[k] = values
	}
	if response.status != 0 {
		w.WriteHeader(response.status)
	}
	fmt.Fprint(w, response.body)
}

func TestGitHubProviderContractAndStableNumericSubject(t *testing.T) {
	f := newFakeGitHub(t)
	var provider ExternalProvider = f.adapter
	if provider.Name() != GitHubName {
		t.Fatal("wrong provider name")
	}
	r := f.issue()
	auth, err := provider.AuthorizationURL(context.Background(), AuthorizationRequest{State: strings.Repeat("s", 43), CodeChallenge: r.CodeChallenge, Nonce: "not-an-oidc-flow"})
	u, _ := url.Parse(auth)
	if err != nil || u.Scheme+"://"+u.Host+u.Path != githubAuthorizationURL || len(u.Query()) != 7 || u.Query().Get("scope") != "user:email" ||
		u.Query().Get("client_id") != "github-client" || u.Query().Get("redirect_uri") != f.adapter.cfg.RedirectURI || u.Query().Get("state") != strings.Repeat("s", 43) ||
		u.Query().Get("code_challenge") != r.CodeChallenge || u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("response_type") != "code" || len(f.requestPaths()) != 0 {
		t.Fatal("authorization was not a local, minimal-scope state/PKCE URL")
	}
	before := time.Now().Add(-time.Second)
	i, err := provider.ExchangeCode(context.Background(), r)
	if err != nil || i.Subject != "9007199254740993" || i.Issuer != GitHubIssuer || i.Provider != GitHubName ||
		i.Email != "octocat+tag@example.com" || !i.EmailVerified || i.EmailTrust != ProviderVerified || i.DisplayName != "Octocat" ||
		i.AuthenticatedAt.Before(before) || i.AuthenticatedAt.After(time.Now()) {
		t.Fatalf("unexpected verified identity: %v", err)
	}
	if got := strings.Join(f.requestPaths(), " "); got != "/login/oauth/access_token /user /user/emails?per_page=100&page=1" {
		t.Fatalf("unexpected API footprint: %s", got)
	}
	// A changed login/profile/email never changes the immutable numeric key.
	f.respond("/user", githubResponse{body: `{"id":9007199254740993,"type":"User","login":"renamed","name":"Different","avatar_url":"http://invalid.example.com/avatar","email":"other@example.com"}`})
	f.respond("/user/emails", githubResponse{body: `[{"email":"changed@example.com","primary":true,"verified":true}]`})
	j, err := provider.ExchangeCode(context.Background(), f.issue())
	if err != nil || j.Subject != i.Subject || j.DisplayName != "Different" || j.Email != "changed@example.com" || j.AvatarURL != "" {
		t.Fatalf("mutable profile affected identity: %v", err)
	}
	if result, err := provider.ExchangeCode(context.Background(), r); !errors.Is(err, ErrRejected) || result != (ProviderIdentity{}) {
		t.Fatal("replayed code was accepted")
	}
}

func TestGitHubPrimaryVerifiedEmailPolicy(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"verified-private", `[{"email":"private@example.com","primary":true,"verified":true,"visibility":"private"}]`, "private@example.com"},
		{"gmail-still-requires-ozid", `[{"email":"somebody@gmail.com","primary":true,"verified":true}]`, "somebody@gmail.com"},
		{"missing", `[]`, ""}, {"null", `null`, ""},
		{"unverified-primary", `[{"email":"primary@example.com","primary":true,"verified":false},{"email":"other@example.com","verified":true}]`, ""},
		{"secondary-only", `[{"email":"other@example.com","primary":false,"verified":true}]`, ""},
		{"legacy-noreply", `[{"email":"login@users.noreply.github.com","primary":true,"verified":true}]`, ""},
		{"numeric-noreply", `[{"email":"123+login@users.noreply.github.com","primary":true,"verified":true},{"email":"fallback@example.com","verified":true}]`, ""},
		{"noreply-local", `[{"email":"no-reply@example.com","primary":true,"verified":true}]`, ""},
		{"invalid", `[{"email":"Name <valid@example.com>","primary":true,"verified":true}]`, ""},
		{"empty", `[{"email":"","primary":true,"verified":true}]`, ""},
		{"duplicate-primary", `[{"email":"a@example.com","primary":true,"verified":true},{"email":"b@example.com","primary":true,"verified":true}]`, ""},
		{"missing-verification", `[{"email":"a@example.com","primary":true}]`, ""},
		{"wrong-verification-type", `[{"email":"a@example.com","primary":true,"verified":"true"}]`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGitHub(t)
			f.respond("/user/emails", githubResponse{body: tt.body})
			i, err := f.adapter.ExchangeCode(context.Background(), f.issue())
			if tt.want == "" {
				if !errors.Is(err, ErrRejected) || i != (ProviderIdentity{}) {
					t.Fatal("unusable provider email must fail neutrally without partial identity")
				}
			} else if err != nil || i.Email != tt.want || i.EmailTrust != ProviderVerified || !i.EmailVerified {
				t.Fatalf("wrong email trust: %v", err)
			}
		})
	}
}

func TestGitHubRejectsNonCanonicalSubjectsAndNonUsers(t *testing.T) {
	for _, id := range []string{`0`, `-1`, `1.5`, `1e3`, `"123"`, `null`, `18446744073709551616`, `true`, `[]`, `{}`} {
		t.Run(id, func(t *testing.T) {
			f := newFakeGitHub(t)
			f.respond("/user", githubResponse{body: `{"type":"User","id":` + id + `}`})
			if i, err := f.adapter.ExchangeCode(context.Background(), f.issue()); !errors.Is(err, ErrRejected) || i != (ProviderIdentity{}) || len(f.requestPaths()) != 2 {
				t.Fatal("invalid numeric subject was accepted or caused further API requests")
			}
		})
	}
	for _, typ := range []string{"Bot", "Organization", ""} {
		f := newFakeGitHub(t)
		f.respond("/user", githubResponse{body: `{"id":1,"type":"` + typ + `"}`})
		if _, err := f.adapter.ExchangeCode(context.Background(), f.issue()); !errors.Is(err, ErrRejected) {
			t.Fatal("non-user principal was accepted")
		}
	}
}

func TestGitHubFlowInputsFailBeforeNetwork(t *testing.T) {
	mutations := []func(*CodeExchangeRequest){
		func(r *CodeExchangeRequest) { r.Provider = GoogleName },
		func(r *CodeExchangeRequest) { r.ConfigVersion-- },
		func(r *CodeExchangeRequest) { r.Code = "" }, // cancelled callback supplies no code
		func(r *CodeExchangeRequest) { r.Code = "bad\ncode" },
		func(r *CodeExchangeRequest) { r.Code = strings.Repeat("c", 4097) },
		func(r *CodeExchangeRequest) { r.Verifier = "short" },
		func(r *CodeExchangeRequest) { r.Verifier = strings.Repeat("x", 64) },
		func(r *CodeExchangeRequest) { r.CodeChallenge = "bad" },
	}
	for n, mutate := range mutations {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			f := newFakeGitHub(t)
			r := f.issue()
			mutate(&r)
			if i, err := f.adapter.ExchangeCode(context.Background(), r); !errors.Is(err, ErrRejected) || i != (ProviderIdentity{}) || len(f.requestPaths()) != 0 {
				t.Fatal("unbound flow reached the provider")
			}
		})
	}
	for _, code := range []string{"wrong-code", "google-code"} {
		f := newFakeGitHub(t)
		r := f.issue()
		r.Code = code
		if _, err := f.adapter.ExchangeCode(context.Background(), r); !errors.Is(err, ErrRejected) || f.posts.Load() != 1 || len(f.requestPaths()) != 1 {
			t.Fatal("foreign/invalid grant accepted or retried")
		}
	}
	// Even replacing both local verifier and challenge cannot replace GitHub's
	// binding to the original authorization grant.
	f := newFakeGitHub(t)
	r := f.issue()
	r.Verifier = strings.Repeat("x", 64)
	digest := sha256.Sum256([]byte(r.Verifier))
	r.CodeChallenge = base64.RawURLEncoding.EncodeToString(digest[:])
	if _, err := f.adapter.ExchangeCode(context.Background(), r); !errors.Is(err, ErrRejected) || f.posts.Load() != 1 {
		t.Fatal("provider-side PKCE binding bypassed")
	}
}

func TestGitHubHTTPFailuresAreBoundedNeutralAndNeverRetried(t *testing.T) {
	for _, path := range []string{"/login/oauth/access_token", "/user", "/user/emails"} {
		for _, tc := range []struct {
			name string
			r    githubResponse
			want error
		}{
			{"cancelled", githubResponse{body: `{"error":"access_denied","error_description":"secret provider detail"}`}, ErrRejected},
			{"redirect", githubResponse{status: 302, header: http.Header{"Location": {"https://evil.example.com/leak"}}}, ErrRejected},
			{"unauthorized", githubResponse{status: 401}, ErrRejected},
			{"forbidden", githubResponse{status: 403}, ErrRejected},
			{"primary-limit", githubResponse{status: 403, header: http.Header{"X-Ratelimit-Remaining": {"0"}}}, ErrUnavailable},
			{"secondary-limit", githubResponse{status: 403, header: http.Header{"Retry-After": {"60"}}}, ErrUnavailable},
			{"rate-limit", githubResponse{status: 429}, ErrUnavailable},
			{"outage", githubResponse{status: 503}, ErrUnavailable},
			{"oversize", githubResponse{body: strings.Repeat("x", maxResponseBytes+1)}, ErrRejected},
			{"malformed", githubResponse{body: `{`}, ErrRejected},
			{"trailing-json", githubResponse{body: `{} {}`}, ErrRejected},
			{"wrong-type", githubResponse{body: `42`}, ErrRejected},
			{"html", githubResponse{body: `<html>secret</html>`, header: http.Header{"Content-Type": {"text/html"}}}, ErrRejected},
		} {
			t.Run(path+tc.name, func(t *testing.T) {
				f := newFakeGitHub(t)
				f.respond(path, tc.r)
				i, err := f.adapter.ExchangeCode(context.Background(), f.issue())
				if !errors.Is(err, tc.want) || i != (ProviderIdentity{}) || f.posts.Load() != 1 || strings.Contains(fmt.Sprint(err), "secret") {
					t.Fatalf("unsafe error/retry: %v", err)
				}
			})
		}
	}
}

func TestGitHubTokenResponseValidation(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"access_token":"gho_local-secret-token","token_type":"bearer","scope":"user"}`,
		`{"access_token":"gho_local-secret-token","token_type":"bearer","scope":"repo,user:email"}`,
		`{"access_token":"gho_local-secret-token","token_type":"mac","scope":"user:email"}`,
		`{"access_token":"bad\r\ntoken","token_type":"bearer","scope":"user:email"}`,
		`{"access_token":"gho_local-secret-token","token_type":"bearer","scope":"user:email","error":"denied"}`,
	} {
		f := newFakeGitHub(t)
		f.respond("/login/oauth/access_token", githubResponse{body: body})
		if i, err := f.adapter.ExchangeCode(context.Background(), f.issue()); !errors.Is(err, ErrRejected) || i != (ProviderIdentity{}) || len(f.requestPaths()) != 1 {
			t.Fatal("unusable/overprivileged token reached API")
		}
	}
}

func TestGitHubCancellationCoversEveryStage(t *testing.T) {
	for _, path := range []string{"/login/oauth/access_token", "/user", "/user/emails"} {
		t.Run(path, func(t *testing.T) {
			f := newFakeGitHub(t)
			f.respond(path, githubResponse{block: true})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			if i, err := f.adapter.ExchangeCode(ctx, f.issue()); !errors.Is(err, ErrUnavailable) || i != (ProviderIdentity{}) || time.Since(start) > 2*time.Second || f.posts.Load() != 1 {
				t.Fatal("cancelled flow did not terminate without retry/partial identity")
			}
		})
	}
}

func TestGitHubSharesOneDeadlineAndHidesTransportErrors(t *testing.T) {
	f := newFakeGitHub(t)
	original := f.adapter.client.Transport
	var first time.Time
	f.adapter.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Error("provider request has no bounded deadline")
		}
		if first.IsZero() {
			first = deadline
		} else if !deadline.Equal(first) {
			t.Error("each API request extended the exchange deadline")
		}
		return original.RoundTrip(r)
	})
	if _, err := f.adapter.ExchangeCode(context.Background(), f.issue()); err != nil {
		t.Fatal(err)
	}
	var calls int
	f.adapter.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("network failure github-secret gho_local-secret-token")
	})
	if i, err := f.adapter.ExchangeCode(context.Background(), f.issue()); !errors.Is(err, ErrUnavailable) || i != (ProviderIdentity{}) || calls != 1 || strings.Contains(fmt.Sprint(err), "secret") {
		t.Fatal("transport error leaked or code POST retried")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.adapter.AuthorizationURL(ctx, AuthorizationRequest{State: strings.Repeat("s", 43), CodeChallenge: strings.Repeat("c", 43)}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("cancelled authorization succeeded")
	}
}

func TestGitHubEmailPaginationIsBoundedAndNeverFollowsLink(t *testing.T) {
	var full []map[string]any
	for n := 0; n < githubEmailPageSize; n++ {
		full = append(full, map[string]any{"email": fmt.Sprintf("mail%d@example.com", n), "primary": false, "verified": true})
	}
	body, _ := json.Marshal(full)
	for _, name := range []string{"second-page-primary", "incomplete", "too-many-entries", "duplicate-primary"} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGitHub(t)
			f.respond("/user/emails", githubResponse{body: string(body), header: http.Header{"Link": {`<https://evil.example.com/steal>; rel="next"`}}})
			switch name {
			case "second-page-primary":
				f.respond("/user/emails?per_page=100&page=2", githubResponse{body: `[{"email":"primary@example.com","primary":true,"verified":true}]`})
			case "duplicate-primary":
				first := append([]map[string]any(nil), full...)
				first[0] = map[string]any{"email": "primary@example.com", "primary": true, "verified": true}
				b, _ := json.Marshal(first)
				f.respond("/user/emails?per_page=100&page=1", githubResponse{body: string(b)})
				f.respond("/user/emails?per_page=100&page=2", githubResponse{body: `[{"email":"other@example.com","primary":true,"verified":true}]`})
			case "too-many-entries":
				b, _ := json.Marshal(append(full, full[0]))
				f.respond("/user/emails", githubResponse{body: string(b)})
			}
			i, err := f.adapter.ExchangeCode(context.Background(), f.issue())
			if name == "second-page-primary" {
				if err != nil || i.Email != "primary@example.com" || len(f.requestPaths()) != 4 {
					t.Fatalf("primary on page 2 lost: %v", err)
				}
			} else if !errors.Is(err, ErrRejected) || i != (ProviderIdentity{}) || len(f.requestPaths()) > 2+githubMaxEmailPages {
				t.Fatal("ambiguous/unbounded mail list accepted")
			}
		})
	}
}

func TestGitHubConfigurationAndRedaction(t *testing.T) {
	f := newFakeGitHub(t)
	for _, change := range []func(*GitHubConfig){
		func(c *GitHubConfig) { c.ClientID = "" }, func(c *GitHubConfig) { c.ClientSecret = "" },
		func(c *GitHubConfig) { c.ClientSecret = "secret\n" }, func(c *GitHubConfig) { c.ConfigVersion = 0 },
		func(c *GitHubConfig) { c.RedirectURI = "http://api.example.com/api/identity/callback/github" },
		func(c *GitHubConfig) { c.RedirectURI += "?override=true" },
		func(c *GitHubConfig) { c.RedirectURI += "#fragment" },
		func(c *GitHubConfig) { c.RedirectURI = "https://api.example.com/api/identity/callback/google" },
	} {
		c := f.adapter.cfg
		change(&c)
		if _, err := NewGitHub(c, nil); err == nil || strings.Contains(err.Error(), "github-secret") {
			t.Fatal("unsafe config accepted or error disclosed secret")
		}
	}
	off, err := NewGitHub(GitHubConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := off.AuthorizationURL(context.Background(), AuthorizationRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal("default enabled")
	}
	if _, err := off.ExchangeCode(context.Background(), CodeExchangeRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled exchange accepted")
	}
	if _, err := f.adapter.AuthorizationURL(context.Background(), AuthorizationRequest{State: "short", CodeChallenge: strings.Repeat("c", 43)}); !errors.Is(err, ErrRejected) {
		t.Fatal("bad state accepted")
	}
	if _, err := f.adapter.AuthorizationURL(context.Background(), AuthorizationRequest{State: strings.Repeat("s", 43), CodeChallenge: "bad"}); !errors.Is(err, ErrRejected) {
		t.Fatal("bad challenge accepted")
	}
	i, err := f.adapter.ExchangeCode(context.Background(), f.issue())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{f.adapter.cfg, f.adapter, i, f.issue(), AuthorizationRequest{State: "secret-state"}} {
		var b bytes.Buffer
		fmt.Fprintf(&b, "%v %+v %#v", v, v, v)
		slog.New(slog.NewJSONHandler(&b, nil)).Info("provider", "value", v)
		for _, secret := range []string{"github-secret", "gho_local-secret-token", "never-retained", "9007199254740993", "octocat+tag@example.com", "secret-state", strings.Repeat("v", 64)} {
			if strings.Contains(b.String(), secret) {
				t.Fatal("sensitive provider facts leaked through logs")
			}
		}
	}
	encoded, _ := json.Marshal(f.adapter.cfg)
	if strings.Contains(string(encoded), "github-secret") || len(f.requestPaths()) != 3 {
		t.Fatal("secret serialized or constructor performed network")
	}
}
