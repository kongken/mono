package app_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/bff/app"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/gateway"
	"github.com/kongken/mono/internal/bff/metro/mcp"
	"github.com/kongken/mono/internal/bff/navigation"
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
	// The default route table also has the account service.
	if _, ok := cfg.Upstreams["usersrv"]; ok {
		cfg.GRPC.Services["usersrv"] = config.GRPCService{Address: "127.0.0.1:1"}
	}
	withAccountRegistry(cfg)
}

// withAccountRegistry gives a configuration that serves the account procedures (a usersrv upstream) the
// registry it must have to start: the user center's origin. The tests below are about routing, tiles and
// status, not about who may call the account.
func withAccountRegistry(cfg *config.Config) {
	if _, ok := cfg.Upstreams[config.AccountService]; !ok || cfg.Navigation.Enabled() {
		return
	}
	const id = "https://id.example.com"
	cfg.Navigation = navigation.Config{APIOrigin: "https://api.example.com", Apps: map[string]navigation.App{"ozid": {Origin: id}}}
	if !slices.Contains(cfg.CORSAllowedOrigins, id) {
		cfg.CORSAllowedOrigins = append(cfg.CORSAllowedOrigins, id)
	}
}

func newBFF(t *testing.T, cfg *config.Config, services []gateway.Service) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	withAccountRegistry(cfg)
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
	// The example enables every product, optional ones included.
	active := gateway.Active(gateway.Services, cfg.Upstreams)
	if len(active) != len(gateway.Services) {
		t.Fatalf("the example configures %d of %d services", len(active), len(gateway.Services))
	}
	if err := cfg.Validate(gateway.Names(active)); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateInternal(gateway.GRPCNames(active)); err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCP.AllowedCIDRs) == 0 {
		t.Fatal("the example must admit the local machine to the MCP endpoint, which refuses everyone otherwise")
	}
	if _, ok := cfg.Navigation.Apps["ozid"]; !ok {
		t.Fatal("the example must register OzID so that the sign-in return flow works locally")
	}
	if cfg.GRPC.CAFile != "" || cfg.GRPC.Services["usersrv"].Transport != "h2c" || cfg.GRPC.Services["tripmap"].Transport != "h2c" || cfg.GRPC.Services["metro"].Transport != "h2c" {
		t.Fatal("local example must use explicit h2c without a CA")
	}
}

func TestOptionalServicesNeedAnUpstream(t *testing.T) {
	names := func(s []gateway.Service) []string { return gateway.Names(s) }
	without := names(gateway.Active(gateway.Services, map[string]string{"usersrv": "http://u", "tripmap": "http://t"}))
	with := names(gateway.Active(gateway.Services, map[string]string{"usersrv": "http://u", "tripmap": "http://t", "metro": "http://m"}))
	if len(without) != 2 || without[0] != "usersrv" || without[1] != "tripmap" || len(with) != 3 {
		t.Fatalf("active services without metro %v, with metro %v", without, with)
	}
	// A required service stays in the list even when its upstream is missing, so
	// validation can report it.
	if got := names(gateway.Active(gateway.Services, nil)); len(got) != 2 || got[0] != "usersrv" || got[1] != "tripmap" {
		t.Fatalf("no upstreams at all: %v", got)
	}
}

func TestCredentialPathsStayOutOfTraces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	trip := newFake(t, "tripmap")
	cfg := &config.Config{Upstreams: map[string]string{"usersrv": "http://127.0.0.1:1", "tripmap": trip.URL}}
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

// The MCP endpoint is a BFF-owned route; the table and the handler's path agree.
func TestMCPRouteIsRegisteredAsBFFOwned(t *testing.T) {
	for _, r := range gateway.BFFOwnedRoutes {
		if r.Path == mcp.Path && r.Method == http.MethodPost {
			return
		}
	}
	t.Fatalf("%s is not in gateway.BFFOwnedRoutes as a POST route", mcp.Path)
}

// Only exact origins get CORS. A same-site sibling of an allowed origin (it may send the
// host-only Cookie) gets none, and neither does any variation of an allowed origin.
func TestCORSIsExactForSameSiteSiblings(t *testing.T) {
	trip := newFake(t, "tripmap")
	r := newBFF(t, &config.Config{Upstreams: map[string]string{"tripmap": trip.URL, "other": "http://127.0.0.1:1"}, CORSAllowedOrigins: []string{"https://id.example.com"}}, testServices)
	for _, origin := range []string{"https://evil.example.com", "https://id.example.com.evil.test", "http://id.example.com", "https://id.example.com:8443", "null"} {
		for _, method := range []string{http.MethodOptions, http.MethodPost} {
			req := httptest.NewRequest(method, "/api/bff.tripmap.v1.FootprintService/GetFootprints", nil)
			req.Header.Set("Origin", origin)
			got := serve(r, req)
			if got.Header().Get("Access-Control-Allow-Origin") != "" || got.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Errorf("%s from %q was given CORS: %v", method, origin, got.Header())
			}
		}
	}
	req := httptest.NewRequest(http.MethodOptions, "/api/bff.tripmap.v1.FootprintService/GetFootprints", nil)
	req.Header.Set("Origin", "https://id.example.com")
	got := serve(r, req)
	if got.Header().Get("Access-Control-Allow-Origin") != "https://id.example.com" || got.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("the registered origin was refused: %v", got.Header())
	}
	if h := got.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(h, "X-CSRF-Token") {
		t.Fatalf("preflight does not allow the CSRF header: %q", h)
	}
}
