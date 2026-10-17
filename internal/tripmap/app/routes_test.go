package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/config"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
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
	// The status query is served over gRPC only; the old Connect procedure is gone.
	legacy := httptest.NewRecorder()
	router.ServeHTTP(legacy, httptest.NewRequest(http.MethodPost, "/api/tripmap.v1.SystemService/GetStatus", strings.NewReader("{}")))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy Connect status procedure: %d", legacy.Code)
	}
	status := application.NewSystemService(handlers, func() bool { return false })
	response, err := status.GetStatus(context.Background(), &tripmapv1.GetStatusRequest{})
	if err != nil || response.Ready || len(response.Dependencies) != 3 || response.FootprintSchema < 2 {
		t.Fatalf("readiness before wiring: response=%v err=%v", response, err)
	}
	handlers.resources.Store(&Resources{Health: health.New(health.Probe{Name: "test", Check: func(context.Context) error { return nil }})})
	response, err = status.GetStatus(context.Background(), &tripmapv1.GetStatusRequest{})
	if err != nil || !response.Ready {
		t.Fatalf("readiness after resource wiring: %v", err)
	}
}

func TestSharePublicRoutesAreNotOnTripmapHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(&config.AppConfig{}).Routes(router)
	token := strings.Repeat("a", 43)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/"+token, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("share page should be served by the BFF only: %d", rec.Code)
	}
}

func TestShareRoutesAreNotRegisteredOnTripmapHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(&config.AppConfig{}).Routes(router)
	token := strings.Repeat("a", 43)
	for _, path := range []string{"/s", "/s/" + token, "/api/share/" + token + "/data"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), token) {
			t.Fatalf("%s echoed the token", path)
		}
	}
}

func TestUnsubscribeIsNotOnTripmapHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(&config.AppConfig{}).Routes(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notifications/unsubscribe?t=abc.def", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unsubscribe should be served by the BFF only: %d", rec.Code)
	}
}
