package e2e_test

import (
	"context"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	"net/http"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

type chainFakeProvider struct {
	name   string
	claims *provider.Claims
}

func (f *chainFakeProvider) Name() string        { return f.name }
func (f *chainFakeProvider) DisplayName() string { return "Fake OAuth" }
func (f *chainFakeProvider) AuthorizeURL(state string) string {
	return "https://provider.example/authorize?state=" + state
}
func (f *chainFakeProvider) Exchange(_ context.Context, code string) (*provider.Claims, error) {
	if code != "good-code" {
		return nil, context.DeadlineExceeded
	}
	return f.claims, nil
}

func TestChainOAuthCookieAndFootprints(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&chainFakeProvider{name: "fake", claims: &provider.Claims{ExternalID: "oauth-99", Name: "OAuth User"}})
	h := NewChainHarness(t, reg, ChainHTTPOptions{Configure: func(cfg *bffconfig.Config, base string) {
		cfg.Auth.OAuthDevelopment = true
		cfg.Auth.OAuthApps = map[string]bffconfig.OAuthApp{"a": {Origin: "http://localhost:5173", DefaultPath: "/", AllowedPaths: []string{"/"}}}
		cfg.Auth.OAuthProviders = map[string]bffconfig.OAuthProvider{"fake": {RedirectURL: base + "/auth/callback/fake"}}
	}})
	ctx := context.Background()
	b := h.FreshBrowser()
	csrf := b.refreshCSRF(ctx)

	list, err := b.Auth.ListOAuthProviders(ctx, connect.NewRequest(&bffv1.ListOAuthProvidersRequest{}))
	if err != nil || len(list.Msg.Providers) != 1 {
		t.Fatalf("providers: %v %v", list, err)
	}

	b.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	begin, err := b.HTTP.Get(h.BFFURL + "/auth/login?app=a&provider=fake")
	if err != nil {
		t.Fatal(err)
	}
	begin.Body.Close()
	authorize, _ := url.Parse(begin.Header.Get("Location"))
	state := authorize.Query().Get("state")
	callback := h.BFFURL + "/auth/callback/fake?code=good-code&state=" + state
	complete, err := b.HTTP.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	complete.Body.Close()
	if complete.StatusCode != 303 || complete.Header.Get("Location") != "http://localhost:5173/" {
		t.Fatalf("callback: %v", complete)
	}
	if b.SessionCookie() == "" {
		t.Fatal("expected session cookie after oauth")
	}
	me, err := b.Me(ctx)
	if err != nil || me.GetId() == "" {
		t.Fatalf("me: %v err=%v", me, err)
	}
	if _, err = b.GetFootprints(ctx); err != nil {
		t.Fatal(err)
	}

	replay, err := b.HTTP.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	replay.Body.Close()
	if replay.StatusCode != 400 {
		t.Fatal("replay accepted")
	}
	req := connect.NewRequest(&bffv1.BeginOAuthFlowRequest{Provider: "fake"})
	b.writeHeaders(req, csrf, b.Origin)
	if _, err = b.Auth.BeginOAuthFlow(ctx, req); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("legacy RPC: %v", err)
	}
}

func TestSharedCookieCannotWritePreviousAccountEdits(t *testing.T) {
	h := NewChainHarness(t)
	a, b := h.FreshBrowser(), h.FreshBrowser()
	ctx := context.Background()
	aID, err := a.Register(ctx, "expected_a", "password1", "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Register(ctx, "expected_b", "password1", "B"); err != nil {
		t.Fatal(err)
	}
	// Me previously returned A. Another app now changed the shared Cookie to B.
	// Send A's queued edit with B's actual Cookie and valid CSRF; the request
	// itself must bind the intended account, independent of any preceding Me.
	req := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{MutationId: "stale-account-edit", Mutations: []*bfftripmapv1.FootprintMutation{{Operation: &bfftripmapv1.FootprintMutation_UpsertCity{UpsertCity: &bfftripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"}}}}})
	b.writeHeaders(req, b.refreshCSRF(ctx), b.Origin)
	req.Header().Set("X-Kongken-User", aID)
	if _, err = b.Foot.SyncChanges(ctx, req); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("stale account write accepted: %v", err)
	}
	fp, err := b.GetFootprints(ctx)
	if err != nil || len(fp.Visits) != 0 {
		t.Fatalf("new account was modified: %v %v", fp, err)
	}
}
