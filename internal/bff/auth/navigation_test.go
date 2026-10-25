package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/auth"
	"github.com/kongken/mono/internal/bff/ingress"
	"github.com/kongken/mono/internal/bff/navigation"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

func registry(t *testing.T) *navigation.Registry {
	t.Helper()
	r, err := navigation.New(navigation.Config{
		APIOrigin: "https://api.example.com",
		Apps: map[string]navigation.App{
			"ozid":  {Origin: "https://id.example.com"},
			"metro": {Origin: "https://metro.example.com", Paths: []string{"/me", "/city/*"}},
		},
	}, []string{"https://id.example.com", "https://metro.example.com"}, true)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// navClient serves ResolveNavigation behind the real ingress interceptor, which has no
// session lookup at all: an anonymous method must not need one.
func navClient(t *testing.T, reg *navigation.Registry) bffv1connect.AuthServiceClient {
	t.Helper()
	h := auth.NewHandler(nil, 0).WithNavigation(reg)
	policy := ingress.Policy{AllowedOrigins: []string{"https://id.example.com"}}
	path, handler := bffv1connect.NewAuthServiceHandler(h,
		connect.WithInterceptors(ingress.Interceptor(policy)), connect.WithRequireConnectProtocolHeader())
	mux := http.NewServeMux()
	mux.Handle(path, ingress.ConnectOnly(handler))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return bffv1connect.NewAuthServiceClient(srv.Client(), srv.URL)
}

func resolve(c bffv1connect.AuthServiceClient, app, path string) (*bffv1.ResolveNavigationResponse, error) {
	res, err := c.ResolveNavigation(context.Background(), connect.NewRequest(&bffv1.ResolveNavigationRequest{AppId: app, ReturnPath: path}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func TestResolveNavigationIsAnonymousAndReadOnly(t *testing.T) {
	c := navClient(t, registry(t))
	got, err := resolve(c, "metro", "/city/shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if got.GetAppId() != "metro" || got.GetReturnPath() != "/city/shanghai" ||
		got.GetReturnUrl() != "https://metro.example.com/city/shanghai" || got.GetOzidOrigin() != "https://id.example.com" {
		t.Fatalf("got %v", got)
	}
	// No path means the app's default.
	if got, err := resolve(c, "metro", ""); err != nil || got.GetReturnUrl() != "https://metro.example.com/" {
		t.Fatalf("%v %v", got, err)
	}
	// The call establishes nothing: no cookie of any kind comes back.
	res, err := c.ResolveNavigation(context.Background(), connect.NewRequest(&bffv1.ResolveNavigationRequest{AppId: "ozid"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Header().Get("Set-Cookie") != "" {
		t.Fatalf("resolving a target set a cookie: %v", res.Header())
	}
}

func TestResolveNavigationRefusesWithoutSayingWhy(t *testing.T) {
	c := navClient(t, registry(t))
	for _, tc := range []struct{ app, path string }{
		{"unknown", "/"},
		{"metro", "https://evil.example/"},
		{"metro", "//evil.example"},
		{"metro", "/city/%2e%2e/x"},
		{"metro", "/me?next=https://evil.example"},
		{"metro", "/me#x"},
		{"metro", "/admin"},
		{"https://metro.example.com", "/me"},
	} {
		_, err := resolve(c, tc.app, tc.path)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%q %q: %v", tc.app, tc.path, err)
			continue
		}
		if msg := connect.NewError(connect.CodeInvalidArgument, err).Error(); len(msg) > 120 {
			t.Errorf("verbose error: %s", msg)
		}
	}
}

func TestResolveNavigationWithoutRegistryIsRefused(t *testing.T) {
	_, err := resolve(navClient(t, nil), "ozid", "")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("got %v", err)
	}
}
