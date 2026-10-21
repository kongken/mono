package auth_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kongken/mono/internal/bff/auth"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/oauth"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/testutil"
	"github.com/redis/go-redis/v9"
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
	svc, h, _, _ := oauthSvcBacking(t, reg, cfg)
	return svc, h
}

func oauthSvcBacking(t *testing.T, reg *provider.Registry, cfg *config.AuthConfig) (*auth.Service, *auth.Handler, *redis.Client, string) {
	t.Helper()
	db, rdb, prefix := testutil.Backing(t)
	if cfg == nil {
		cfg = &config.AuthConfig{SessionTTL: time.Hour}
	}
	cookies := session.CookieConfig{Path: "/", SameSite: http.SameSiteLaxMode, Secure: cfg.CookieSecure}
	state := oauth.NewStateStore(rdb, prefix)
	svc := auth.NewService(cfg, cookies, db, ratelimit.New(rdb, prefix), nil, nil, auth.OAuthDeps{State: state, Registry: reg})
	return svc, auth.NewHandler(svc, nil, time.Hour), rdb, prefix
}
