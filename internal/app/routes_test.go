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
	response, err = client.GetStatus(context.Background(), connect.NewRequest(&tripmapv1.GetStatusRequest{}))
	if err != nil || !response.Msg.Ready {
		t.Fatalf("RPC after resource wiring: %v", err)
	}
}
