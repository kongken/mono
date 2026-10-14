package app

import (
	"connectrpc.com/connect"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.orx.me/apps/trip-map/internal/config"
	"go.orx.me/apps/trip-map/internal/health"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
	"go.orx.me/apps/trip-map/pkg/proto/tripmap/v1/tripmapv1connect"
)

func TestRoutesBeforeBootstrap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handlers := New(&config.AppConfig{})
	router := gin.New()
	handlers.Routes(router)
	for _, tc := range []struct {
		path   string
		status int
		text   string
	}{{"/ping", 200, "pong"}, {"/readyz", 503, `"ready":false`}} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.text) {
			t.Fatalf("%s: %d %s", tc.path, response.Code, response.Body.String())
		}
	}
	server := httptest.NewServer(router)
	defer server.Close()
	client := tripmapv1connect.NewSystemServiceClient(server.Client(), server.URL+"/api")
	response, err := client.GetStatus(context.Background(), connect.NewRequest(&tripmapv1.GetStatusRequest{}))
	if err != nil || response.Msg.Ready || len(response.Msg.Dependencies) != 3 {
		t.Fatalf("RPC readiness: response=%v err=%v", response, err)
	}
	handlers.resources.Store(&Resources{Health: health.New(health.Probe{Name: "test", Check: func(context.Context) error { return nil }})})
	if response.Msg.FootprintSchema < 2 {
		t.Fatalf("status does not advertise the multi-trip schema: %v", response.Msg)
	}
	response, err = client.GetStatus(context.Background(), connect.NewRequest(&tripmapv1.GetStatusRequest{}))
	if err != nil || !response.Msg.Ready {
		t.Fatalf("RPC after resource wiring: %v", err)
	}
}

func TestCORSAllowsOnlyConfiguredOrigins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.AppConfig{Auth: config.AuthConfig{CORSAllowedOrigins: []string{"https://trip.example.com"}}}
	router := gin.New()
	New(cfg).Routes(router)
	do := func(method, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/tripmap.v1.AuthService/Login", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	preflight := do(http.MethodOptions, "https://trip.example.com")
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != "https://trip.example.com" ||
		!strings.Contains(preflight.Header().Get("Access-Control-Allow-Headers"), "Authorization") ||
		!strings.Contains(preflight.Header().Get("Access-Control-Allow-Headers"), "X-Tripmap-Schema") {
		t.Fatalf("allowed preflight: %d %v", preflight.Code, preflight.Header())
	}
	if got := do(http.MethodOptions, "https://evil.example.com"); got.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unlisted origin was allowed: %v", got.Header())
	}
	if got := do(http.MethodPost, "https://trip.example.com"); got.Header().Get("Access-Control-Allow-Origin") != "https://trip.example.com" {
		t.Fatalf("actual request lacks CORS header: %v", got.Header())
	}
	if got := do(http.MethodPost, ""); got.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("same-origin request got CORS headers")
	}
}
