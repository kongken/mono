package e2e_test

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/protobuf/encoding/protojson"
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
	h := NewChainHarness(t, reg)
	ctx := context.Background()
	b := h.FreshBrowser()
	csrf := b.refreshCSRF(ctx)

	list, err := b.Auth.ListOAuthProviders(ctx, connect.NewRequest(&bffv1.ListOAuthProvidersRequest{}))
	if err != nil || len(list.Msg.Providers) != 1 {
		t.Fatalf("providers: %v %v", list, err)
	}

	beginReq := connect.NewRequest(&bffv1.BeginOAuthFlowRequest{Provider: "fake"})
	b.writeHeaders(beginReq, csrf, b.Origin)
	begin, err := b.Auth.BeginOAuthFlow(ctx, beginReq)
	if err != nil {
		t.Fatal(err)
	}

	completeReq := connect.NewRequest(&bffv1.CompleteOAuthFlowRequest{
		Provider: "fake", Code: "good-code", State: begin.Msg.State,
	})
	b.writeHeaders(completeReq, csrf, b.Origin)
	complete, err := b.Auth.CompleteOAuthFlow(ctx, completeReq)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := protojson.Marshal(complete.Msg)
	if strings.Contains(strings.ToLower(string(raw)), "token") || strings.Contains(string(raw), "bf_") {
		t.Fatalf("response leaked session token: %s", raw)
	}
	if b.SessionCookie() == "" {
		t.Fatal("expected session cookie after oauth")
	}
	me, err := b.Me(ctx)
	if err != nil || me.GetId() != complete.Msg.GetUser().GetId() {
		t.Fatalf("me: %v err=%v", me, err)
	}
	if _, err = b.GetFootprints(ctx); err != nil {
		t.Fatal(err)
	}

	replayReq := connect.NewRequest(&bffv1.CompleteOAuthFlowRequest{
		Provider: "fake", Code: "good-code", State: begin.Msg.State,
	})
	b.writeHeaders(replayReq, b.refreshCSRF(ctx), b.Origin)
	if _, err = b.Auth.CompleteOAuthFlow(ctx, replayReq); err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("state replay: %v", err)
	}
}
