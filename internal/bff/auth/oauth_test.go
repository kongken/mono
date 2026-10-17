package auth_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/auth"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/oauth"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/testutil"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

type fakeProvider struct {
	name   string
	claims *provider.Claims
}

func (f *fakeProvider) Name() string        { return f.name }
func (f *fakeProvider) DisplayName() string { return strings.ToUpper(f.name) }
func (f *fakeProvider) AuthorizeURL(state string) string {
	return "https://provider.example/authorize?state=" + state
}
func (f *fakeProvider) Exchange(_ context.Context, code string) (*provider.Claims, error) {
	if code != "good-code" {
		return nil, context.DeadlineExceeded
	}
	return f.claims, nil
}

func oauthSvc(t *testing.T, reg *provider.Registry, cfg *config.AuthConfig) (*auth.Service, *auth.Handler) {
	t.Helper()
	db, rdb, prefix := testutil.Backing(t)
	if cfg == nil {
		cfg = &config.AuthConfig{SessionTTL: time.Hour}
	}
	cookies := session.CookieConfig{Path: "/", SameSite: http.SameSiteLaxMode}
	state := oauth.NewStateStore(rdb, prefix)
	svc := auth.NewService(cfg, cookies, db, ratelimit.New(rdb, prefix), nil, auth.OAuthDeps{State: state, Registry: reg})
	return svc, auth.NewHandler(svc, nil, time.Hour)
}

func TestOAuthFlow(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider{name: "fake", claims: &provider.Claims{Provider: "fake", ExternalID: "42", Login: "octo", Name: "Octo Cat", AvatarURL: "https://img.example/a.png"}})
	svc, h := oauthSvc(t, reg, nil)
	ctx := context.Background()

	list, err := svc.ListOAuthProviders(ctx)
	if err != nil || len(list.Providers) != 1 {
		t.Fatalf("providers: %v %v", list, err)
	}
	begin := func() string {
		res, err := svc.BeginOAuthFlow(ctx, "127.0.0.1", &bffv1.BeginOAuthFlowRequest{Provider: "fake"})
		if err != nil || !strings.Contains(res.AuthorizeUrl, res.State) {
			t.Fatalf("begin: %v %v", res, err)
		}
		return res.State
	}
	complete := func(code, state string) (*bffv1.CompleteOAuthFlowResponse, string, error) {
		msg, _, tok, err := svc.CompleteOAuthFlow(ctx, "127.0.0.1", &bffv1.CompleteOAuthFlowRequest{Provider: "fake", Code: code, State: state})
		if err != nil {
			return nil, "", err
		}
		return msg, tok, nil
	}
	_, err = svc.BeginOAuthFlow(ctx, "127.0.0.1", &bffv1.BeginOAuthFlowRequest{Provider: "nope"})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unknown provider: %v", err)
	}
	state := begin()
	first, tok1, err := complete("good-code", state)
	if err != nil || first.User.DisplayName != "Octo Cat" || tok1 == "" {
		t.Fatalf("complete: %v", first)
	}
	_, _, err = complete("good-code", state)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("replay state: %v", err)
	}
	_, _, err = complete("good-code", "forged")
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("forged state: %v", err)
	}
	_, _, err = complete("bad-code", begin())
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("bad code: %v", err)
	}
	again, tok2, err := complete("good-code", begin())
	if err != nil || again.User.Id != first.User.Id || tok2 == tok1 {
		t.Fatalf("second sign-in: %v", again)
	}
	hdr := http.Header{}
	hdr.Set("Cookie", session.CookieName+"="+tok2)
	if _, err = h.Authenticate(ctx, hdr); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthClosedRegistration(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider{name: "fake", claims: &provider.Claims{ExternalID: "7"}})
	svc, _ := oauthSvc(t, reg, &config.AuthConfig{SessionTTL: time.Hour, RegistrationDisabled: true})
	ctx := context.Background()
	begin, err := svc.BeginOAuthFlow(ctx, "127.0.0.1", &bffv1.BeginOAuthFlowRequest{Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = svc.CompleteOAuthFlow(ctx, "127.0.0.1", &bffv1.CompleteOAuthFlowRequest{Provider: "fake", Code: "good-code", State: begin.State})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("closed registration: %v", err)
	}
}
