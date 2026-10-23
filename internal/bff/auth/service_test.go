package auth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/auth"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

func logtoConfig() *config.AuthConfig {
	return &config.AuthConfig{SessionTTL: time.Hour, OAuthProviders: map[string]config.OAuthProvider{
		"logto": {IssuerURL: "https://identity.example/oidc", RedirectURL: "https://api.example/auth/callback/logto"},
	}, OAuthApps: map[string]config.OAuthApp{"test": {Origin: "https://app.example", DefaultPath: "/", AllowedPaths: []string{"/"}}}}
}

func signIn(t *testing.T, h *auth.Handler) string {
	t.Helper()
	begin := httptest.NewRecorder()
	h.ServeOAuthHTTP(begin, httptest.NewRequest("GET", "/auth/login?app=test", nil), "")
	if begin.Code != http.StatusFound {
		t.Fatalf("begin: %d %s", begin.Code, begin.Body)
	}
	u, _ := url.Parse(begin.Header().Get("Location"))
	r := httptest.NewRequest("GET", "/auth/callback/logto?code=good-code&state="+u.Query().Get("state"), nil)
	for _, c := range begin.Result().Cookies() {
		r.AddCookie(c)
	}
	end := httptest.NewRecorder()
	h.ServeOAuthHTTP(end, r, "")
	if end.Code != http.StatusSeeOther {
		t.Fatalf("callback: %d %s", end.Code, end.Body)
	}
	for _, c := range end.Result().Cookies() {
		if c.Name == session.CookieName {
			return c.Value
		}
	}
	t.Fatal("missing session cookie")
	return ""
}

func TestLogtoSessionMeLogoutAndProfileRefresh(t *testing.T) {
	p := &fakeProvider{name: "logto", claims: &provider.Claims{ExternalID: "alice", Login: "alice", Name: "Alice"}}
	reg := provider.NewRegistry()
	reg.Register(p)
	svc, h := oauthSvc(t, reg, logtoConfig())
	token := signIn(t, h)
	bs, err := svc.ResolveSession(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	me, err := svc.Me(t.Context(), bs.UserID)
	if err != nil || me.User.DisplayName != "Alice" {
		t.Fatalf("me: %v %v", me, err)
	}
	p.claims.Name = "New name"
	p.claims.Login = "renamed"
	signIn(t, h)
	me, err = svc.Me(t.Context(), bs.UserID)
	if err != nil || me.User.DisplayName != "New name" || me.User.Username != "renamed" {
		t.Fatalf("refresh: %v %v", me, err)
	}
	if err := svc.Logout(t.Context(), bs.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveSession(t.Context(), token); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("revoked: %v", err)
	}
}

func TestListAndRevokeLogtoSessions(t *testing.T) {
	p := &fakeProvider{name: "logto", claims: &provider.Claims{ExternalID: "erin"}}
	reg := provider.NewRegistry()
	reg.Register(p)
	svc, h := oauthSvc(t, reg, logtoConfig())
	tok1, tok2 := signIn(t, h), signIn(t, h)
	bs1, err := svc.ResolveSession(t.Context(), tok1)
	if err != nil {
		t.Fatal(err)
	}
	bs2, err := svc.ResolveSession(t.Context(), tok2)
	if err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListSessions(t.Context(), bs1.UserID, bs1.SessionID)
	if err != nil || len(list.Sessions) != 2 {
		t.Fatalf("sessions: %v %v", list, err)
	}
	p.claims.ExternalID = "mallory"
	stranger, err := svc.ResolveSession(t.Context(), signIn(t, h))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeSession(t.Context(), stranger.UserID, bs2.SessionID); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross user revoke: %v", err)
	}
	if err := svc.RevokeSession(t.Context(), bs1.UserID, bs2.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveSession(t.Context(), tok2); err == nil {
		t.Fatal("revoked session accepted")
	}
	if _, err := svc.ResolveSession(t.Context(), tok1); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordEndpointsAreRetiredWithoutStorage(t *testing.T) {
	h := auth.NewHandler(nil, nil, time.Hour)
	if _, err := h.Register(t.Context(), connect.NewRequest(&bffv1.RegisterRequest{Username: "alice", Password: "password1"})); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("register: %v", err)
	}
	if _, err := h.Login(t.Context(), connect.NewRequest(&bffv1.LoginRequest{Username: "alice", Password: "password1"})); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("login: %v", err)
	}
}

func TestLogtoIssuerAndSubjectAreTheOnlyIdentityKey(t *testing.T) {
	p := &fakeProvider{name: "logto", claims: &provider.Claims{ExternalID: "one", Login: "same", Email: "same@example.com"}}
	reg := provider.NewRegistry()
	reg.Register(p)
	cfg := logtoConfig()
	svc, h := oauthSvc(t, reg, cfg)
	first, err := svc.ResolveSession(t.Context(), signIn(t, h))
	if err != nil {
		t.Fatal(err)
	}
	p.claims.ExternalID = "two"
	second, err := svc.ResolveSession(t.Context(), signIn(t, h))
	if err != nil {
		t.Fatal(err)
	}
	if first.UserID == second.UserID {
		t.Fatal("identities linked by username or email")
	}
	pc := cfg.OAuthProviders["logto"]
	pc.IssuerURL = "https://other.example/oidc"
	cfg.OAuthProviders["logto"] = pc
	third, err := svc.ResolveSession(t.Context(), signIn(t, h))
	if err != nil {
		t.Fatal(err)
	}
	if third.UserID == second.UserID {
		t.Fatal("identities linked across issuers")
	}
}
