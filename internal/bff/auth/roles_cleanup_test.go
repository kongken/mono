package auth_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/auth"
	"github.com/kongken/mono/internal/bff/oauth"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/testutil"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

func TestSessionRecordsRolesAndSignInTime(t *testing.T) {
	p := &fakeProvider{name: "logto", claims: &provider.Claims{ExternalID: "root", Roles: []string{"metro-admin"}}}
	reg := provider.NewRegistry()
	reg.Register(p)
	svc, h := oauthSvc(t, reg, logtoConfig())
	before := time.Now().Add(-time.Second)
	bs, err := svc.ResolveSession(t.Context(), signIn(t, h))
	if err != nil {
		t.Fatal(err)
	}
	if !bs.HasRole("metro-admin") || bs.HasRole("other") || bs.AuthenticatedAt.Before(before) {
		t.Fatalf("session facts: %+v", bs)
	}
	me, err := svc.Me(t.Context(), bs.UserID, bs.Roles)
	if err != nil || !slices.Equal(me.Roles, []string{"metro-admin"}) {
		t.Fatalf("me roles: %v %v", me, err)
	}

	// Roles are re-read at every sign-in: a role removed in Logto disappears from the next session.
	p.claims.Roles = nil
	bs, err = svc.ResolveSession(t.Context(), signIn(t, h))
	if err != nil || len(bs.Roles) != 0 {
		t.Fatalf("second sign-in: %+v %v", bs, err)
	}
}

type fakeCleaner struct {
	name  string
	calls int
	err   error
}

func (f *fakeCleaner) Name() string { return f.name }
func (f *fakeCleaner) CleanupUserData(context.Context, string) error {
	f.calls++
	return f.err
}

func TestAccountDeletionRunsEveryProductCleanerAndRetries(t *testing.T) {
	db, rdb, prefix := testutil.Backing(t)
	one, two := &fakeCleaner{name: "tripmap"}, &fakeCleaner{name: "metro", err: errors.New("metro down")}
	cfg := logtoConfig()
	cookies := session.CookieConfig{Path: "/", SameSite: http.SameSiteLaxMode}
	p := &fakeProvider{name: "logto", claims: &provider.Claims{ExternalID: "leaver"}}
	reg := provider.NewRegistry()
	reg.Register(p)
	svc := auth.NewService(cfg, cookies, db, ratelimit.New(rdb, prefix), nil, []auth.UserDataCleaner{one, two},
		auth.OAuthDeps{State: oauth.NewStateStore(rdb, prefix), Registry: reg})
	h := auth.NewHandler(svc, nil, time.Hour)
	token := signIn(t, h)
	bs, err := svc.ResolveSession(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	req := &bffv1.DeleteAccountRequest{Confirmation: "DELETE"}

	// One product fails: the other still ran, and the account stays pending deletion.
	if err = svc.DeleteAccount(t.Context(), bs, req); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("first attempt: %v", err)
	}
	if one.calls != 1 || two.calls != 1 {
		t.Fatalf("calls after first attempt: %d %d", one.calls, two.calls)
	}
	if _, err = svc.ResolveSession(t.Context(), token); err != nil {
		// The pending-deletion session is still valid, which is what lets the retry happen.
		t.Fatalf("pending session: %v", err)
	}

	// The retry calls the product that already succeeded again; it completes once all succeed.
	two.err = nil
	if err = svc.DeleteAccount(t.Context(), bs, req); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if one.calls != 2 || two.calls != 2 {
		t.Fatalf("calls after retry: %d %d", one.calls, two.calls)
	}
	if !svc.CompletionReceiptValid(t.Context(), token) {
		t.Fatal("no completion receipt after all products were cleaned")
	}
}
