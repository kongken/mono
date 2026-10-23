package app_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/bff/app"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/gateway"
	"github.com/kongken/mono/internal/platform/testkit"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gopkg.in/yaml.v3"
)

// fake is a business service that records what reached it.
type fake struct {
	*httptest.Server
	mu    sync.Mutex
	last  *http.Request
	body  string
	ready int
}

func newFake(t *testing.T, name string) *fake {
	f := &fake{ready: http.StatusOK}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/readyz" {
			w.WriteHeader(f.ready)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		f.last, f.body = r, string(raw)
		// A service still configured for CORS; the BFF must drop this.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = io.WriteString(w, name)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fake) seen() (*http.Request, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last, f.body
}

func (f *fake) setReady(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ready = code
}

var testServices = []gateway.Service{
	{Name: "tripmap", GRPC: true},
	{Name: "other", Packages: []string{"other.v1"}, Routes: []gateway.Route{{Path: "/api/share/special/"}}},
}

var proxyOnlyServices = []gateway.Service{
	{Name: "other", Packages: []string{"other.v1"}, Routes: []gateway.Route{{Path: "/api/share/special/"}}},
}

// withInternalTransport points the configuration at an unused gRPC endpoint with
// real keys and a CA: the tests below exercise HTTP routing and never call it.
func withInternalTransport(t *testing.T, cfg *config.Config) {
	t.Helper()
	keys, ca := testkit.NewKeys(t), testkit.NewCA(t)
	cfg.InternalAuth = config.InternalAuth{Issuer: "bff", KID: "k1", PrivateKeyFile: keys.PrivateFile}
	cfg.GRPC = config.GRPCConfig{CAFile: ca.CAFile, Services: map[string]config.GRPCService{"tripmap": {Address: "127.0.0.1:1"}}}
}

func newBFF(t *testing.T, cfg *config.Config, services []gateway.Service) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if services == nil {
		services = gateway.Services
	}
	if len(gateway.GRPCNames(services)) > 0 {
		withInternalTransport(t, cfg)
	}
	h := app.New(cfg)
	if services != nil {
		h.SetServices(services)
	}
	if err := h.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	h.Routes(r)
	return r
}

// serve gives the request a cancellable context, as http.Server always does;
// without one ReverseProxy falls back to CloseNotifier, which the recorder lacks.
func serve(r http.Handler, req *http.Request) *httptest.ResponseRecorder {
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestTheBFFOwnsCORS(t *testing.T) {
	trip := newFake(t, "tripmap")
	r := newBFF(t, &config.Config{Upstreams: map[string]string{"tripmap": trip.URL, "other": "http://127.0.0.1:1"}, CORSAllowedOrigins: []string{"https://trip.example.com"}}, testServices)
	preflight := httptest.NewRequest(http.MethodOptions, "/api/bff.tripmap.v1.FootprintService/GetFootprints", nil)
	preflight.Header.Set("Origin", "https://trip.example.com")
	got := serve(r, preflight)
	if got.Code != http.StatusNoContent || got.Header().Get("Access-Control-Allow-Origin") != "https://trip.example.com" ||
		!strings.Contains(got.Header().Get("Access-Control-Allow-Headers"), "X-Tripmap-Schema") {
		t.Fatalf("preflight: %d %v", got.Code, got.Header())
	}
	if seen, _ := trip.seen(); seen != nil {
		t.Fatal("preflight reached the service")
	}
	post := httptest.NewRequest(http.MethodPost, "/api/bff.tripmap.v1.FootprintService/GetFootprints", nil)
	post.Header.Set("Origin", "https://trip.example.com")
	if got := serve(r, post).Header().Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "https://trip.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %v, want only the BFF's", got)
	}
	post = httptest.NewRequest(http.MethodPost, "/api/bff.tripmap.v1.FootprintService/GetFootprints", nil)
	post.Header.Set("Origin", "https://evil.example.com")
	if got := serve(r, post).Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unlisted origin got %q", got)
	}
}

func TestReadyzReportsEveryService(t *testing.T) {
	trip, other := newFake(t, "tripmap"), newFake(t, "other")
	services := []gateway.Service{
		{Name: "tripmap"},
		{Name: "other", Packages: []string{"other.v1"}, Routes: []gateway.Route{{Path: "/api/share/special/"}}},
	}
	r := newBFF(t, &config.Config{Upstreams: map[string]string{"tripmap": trip.URL, "other": other.URL}}, services)
	other.setReady(http.StatusServiceUnavailable)
	readyz := func() (int, map[string]bool) {
		rec := serve(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		var body struct {
			Dependencies []struct {
				Name  string
				Ready bool
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		deps := map[string]bool{}
		for _, d := range body.Dependencies {
			deps[d.Name] = d.Ready
		}
		return rec.Code, deps
	}
	if code, deps := readyz(); code != http.StatusServiceUnavailable || !deps["tripmap"] || deps["other"] {
		t.Fatalf("one service down: %d %v", code, deps)
	}
	other.setReady(http.StatusOK)
	if code, deps := readyz(); code != http.StatusOK || !deps["tripmap"] || !deps["other"] {
		t.Fatalf("all up: %d %v", code, deps)
	}
}

func TestLegacyTripmapConnectPathsAreNotProxied(t *testing.T) {
	trip := newFake(t, "tripmap")
	cfg := &config.Config{Upstreams: map[string]string{"tripmap": trip.URL}}
	withInternalTransport(t, cfg)
	r := newBFF(t, cfg, testServices[:1])
	if got := serve(r, httptest.NewRequest(http.MethodPost, "/api/tripmap.v1.AuthService/Login", nil)); got.Code != http.StatusNotFound {
		t.Errorf("legacy login proxy: got %d, want 404", got.Code)
	}
}

func TestRoutesBeforeBootstrap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	app.New(&config.Config{}).Routes(r)
	if got := serve(r, httptest.NewRequest(http.MethodPost, "/api/tripmap.v1.SystemService/GetStatus", nil)); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("before bootstrap legacy tripmap path %d, want 503", got.Code)
	}
	if got := serve(r, httptest.NewRequest(http.MethodGet, "/readyz", nil)); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz %d, want 503", got.Code)
	}
}

func TestExampleConfigCoversTheRouteTable(t *testing.T) {
	raw, err := os.ReadFile("../../../config.bff.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(gateway.Names(gateway.Services)); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateInternal(gateway.GRPCNames(gateway.Services)); err != nil {
		t.Fatal(err)
	}
	if cfg.GRPC.CAFile != "" || cfg.GRPC.Services["tripmap"].Transport != "h2c" {
		t.Fatal("local example must use explicit h2c without a CA")
	}
}

func TestCredentialPathsStayOutOfTraces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	trip := newFake(t, "tripmap")
	cfg := &config.Config{Upstreams: map[string]string{"tripmap": trip.URL}}
	withInternalTransport(t, cfg)
	h := app.New(cfg)
	if err := h.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	spans := tracetest.NewSpanRecorder()
	r := gin.New()
	// Butterfly installs the same middleware in front of every route.
	r.Use(otelgin.Middleware("bff", otelgin.WithTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)))))
	h.Routes(r)
	const secret = "s3cr3t-token-value"
	for _, target := range []string{"/s/" + secret, "/s/" + secret + "/og.png", "/api/share/" + secret + "/data", "/api/notifications/unsubscribe?t=" + secret} {
		serve(r, httptest.NewRequest(http.MethodGet, target, nil))
	}
	ended := spans.Ended()
	if len(ended) != 4 {
		t.Fatalf("%d spans, want 4", len(ended))
	}
	for _, span := range ended {
		for _, attr := range span.Attributes() {
			if strings.Contains(attr.Value.Emit(), secret) {
				t.Errorf("span %q records %s=%s", span.Name(), attr.Key, attr.Value.Emit())
			}
		}
	}
}
