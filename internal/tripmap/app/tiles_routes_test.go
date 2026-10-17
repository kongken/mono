package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/tiles"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// The tests below drive tripmap's real HTTP routes. The only replaced dependency
// is the network to Mapbox: the handler's http.Client sends to a local server, and
// the transport records the URL the handler actually asked for.

const (
	testToken = "pk.eyJ1IjoidGVzdC11c2VyIiwiYSI6InRlc3QifQ.c2lnbmF0dXJlLXZhbHVl"
	clientIP  = "203.0.113.9"
)

func tilePNG(t testing.TB, size int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// redirectingTransport sends every request to the local mock while recording the
// URL the handler built, which must be Mapbox's official HTTPS endpoint.
type redirectingTransport struct {
	target string
	mu     sync.Mutex
	urls   []*url.URL
}

func (rt *redirectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	u := *req.URL
	rt.urls = append(rt.urls, &u)
	rt.mu.Unlock()
	if req.URL.Host != "api.mapbox.com" {
		// Anything else is a redirect target or a wrong address: send it where it says.
		return http.DefaultTransport.RoundTrip(req)
	}
	target, _ := url.Parse(rt.target)
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host = target.Scheme, target.Host
	return http.DefaultTransport.RoundTrip(out)
}

func (rt *redirectingTransport) requested() []*url.URL {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]*url.URL(nil), rt.urls...)
}

type fakeLimiter struct {
	mu    sync.Mutex
	allow bool
	err   error
	keys  []string
	limit int64
}

func (l *fakeLimiter) Allow(_ context.Context, key string, limit int64, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys, l.limit = append(l.keys, key), limit
	return l.allow, l.err
}

type tileEnv struct {
	router  *gin.Engine
	mapbox  *httptest.Server
	hits    func() []*http.Request
	rt      *redirectingTransport
	limiter *fakeLimiter
}

func okMapbox(t testing.TB) http.HandlerFunc {
	body := tilePNG(t, 256)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=43200, s-maxage=43200")
		w.Header().Set("Expires", "Sat, 17 Oct 2026 22:00:00 GMT")
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Last-Modified", "Sat, 17 Oct 2026 10:00:00 GMT")
		w.Header().Set("Set-Cookie", "mapbox=1")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Mapbox-Debug", testToken)
		_, _ = w.Write(body)
	}
}

func newTileEnv(t *testing.T, upstream http.HandlerFunc, mutate func(*tiles.Config)) *tileEnv {
	t.Helper()
	return newTileEnvWith(t, upstream, mutate)
}

// newTileEnvWith installs middleware (such as Butterfly's tracing) in front of the routes.
func newTileEnvWith(t *testing.T, upstream http.HandlerFunc, mutate func(*tiles.Config), middleware ...gin.HandlerFunc) *tileEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var mu sync.Mutex
	var hits []*http.Request
	mapbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.Clone(context.Background()))
		mu.Unlock()
		upstream(w, r)
	}))
	t.Cleanup(mapbox.Close)
	cfg := &config.AppConfig{Auth: config.AuthConfig{ClientIPHeader: "X-Real-IP"}}
	cfg.MapboxTiles = tiles.Config{Enabled: true, Token: testToken, Username: "test-user", StyleID: "journal-style", MaxZoom: 18, RatePerMinute: 100}
	if mutate != nil {
		mutate(&cfg.MapboxTiles)
	}
	rt := &redirectingTransport{target: mapbox.URL}
	limiter := &fakeLimiter{allow: true}
	handlers := New(cfg)
	handlers.SetTileClient(&http.Client{Transport: rt})
	handlers.SetTileLimiter(limiter)
	router := gin.New()
	router.Use(middleware...)
	handlers.Routes(router)
	return &tileEnv{router: router, mapbox: mapbox, rt: rt, limiter: limiter, hits: func() []*http.Request {
		mu.Lock()
		defer mu.Unlock()
		return append([]*http.Request(nil), hits...)
	}}
}

func (e *tileEnv) get(target string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	return e.do(http.MethodGet, target, mutate...)
}

func (e *tileEnv) do(method, target string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(method, target, nil).WithContext(ctx)
	req.Header.Set("X-Real-IP", clientIP)
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func assertNoStore(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func assertNoSecrets(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	dump := rec.Body.String()
	for k, v := range rec.Header() {
		dump += k + ": " + strings.Join(v, ",") + "\n"
	}
	if strings.Contains(dump, testToken) || strings.Contains(dump, "access_token") || strings.Contains(dump, "mapbox.com") {
		t.Errorf("response leaks upstream details: %s", dump)
	}
}

func TestTilesAreOffUnlessEnabled(t *testing.T) {
	env := newTileEnv(t, okMapbox(t), func(c *tiles.Config) { *c = tiles.Config{} })
	for _, target := range []string{"/api/tiles/3/5/2.png", "/api/tiles/bad/path", "/api/tiles/3/5/2.png?x=1"} {
		rec := env.get(target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", target, rec.Code)
		}
		assertNoStore(t, rec)
	}
	if n := len(env.hits()); n != 0 || len(env.limiter.keys) != 0 {
		t.Fatalf("disabled proxy reached Mapbox (%d) or the limiter (%d)", n, len(env.limiter.keys))
	}
}

func TestAnonymousTileIsFetchedWithServerSideToken(t *testing.T) {
	env := newTileEnv(t, okMapbox(t), nil)
	rec := env.get("/api/tiles/3/5/2.png", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer client-secret")
		r.Header.Set("Cookie", "shanhai_sid=session-secret")
		r.Header.Set("X-Tripmap-Schema", "3")
		r.Header.Set("Origin", "https://trip.example.com")
		r.Header.Set("Referer", "https://trip.example.com/map")
		r.Header.Set("Authorization-Internal", "claims")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if want := tilePNG(t, 256); !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatal("body is not the upstream tile")
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q", got)
	}
	for k, want := range map[string]string{
		"Cache-Control": "max-age=43200, s-maxage=43200", "Expires": "Sat, 17 Oct 2026 22:00:00 GMT",
		"ETag": `"abc123"`, "Last-Modified": "Sat, 17 Oct 2026 10:00:00 GMT",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	for _, k := range []string{"Set-Cookie", "Access-Control-Allow-Origin", "X-Mapbox-Debug"} {
		if v := rec.Header().Get(k); v != "" {
			t.Errorf("upstream header %s leaked: %q", k, v)
		}
	}
	if strings.Contains(rec.Header().Get("Content-Type"), testToken) || strings.Contains(rec.Body.String(), testToken) {
		t.Error("token reached the browser")
	}

	urls := env.rt.requested()
	if len(urls) != 1 {
		t.Fatalf("%d upstream requests, want 1", len(urls))
	}
	u := urls[0]
	if u.Scheme != "https" || u.Host != "api.mapbox.com" || u.Path != "/styles/v1/test-user/journal-style/tiles/256/3/5/2.png" {
		t.Errorf("upstream URL %s://%s%s", u.Scheme, u.Host, u.Path)
	}
	if q := u.Query(); len(q) != 1 || q.Get("access_token") != testToken {
		t.Errorf("upstream query %v", q)
	}
	sent := env.hits()[0]
	allowed := map[string]bool{"Accept": true, "User-Agent": true, "Accept-Encoding": true}
	for k, v := range sent.Header {
		if !allowed[k] {
			t.Errorf("header %s=%v was sent to Mapbox", k, v)
		}
	}
}

func TestInvalidTileRequestsNeverReachMapbox(t *testing.T) {
	env := newTileEnv(t, okMapbox(t), nil)
	for _, tc := range []struct {
		method, target string
		status         int
	}{
		{"POST", "/api/tiles/3/5/2.png", http.StatusMethodNotAllowed},
		{"PUT", "/api/tiles/3/5/2.png", http.StatusMethodNotAllowed},
		{"DELETE", "/api/tiles/3/5/2.png", http.StatusMethodNotAllowed},
		{"HEAD", "/api/tiles/3/5/2.png", http.StatusMethodNotAllowed},
		{"GET", "/api/tiles/3/5/2.jpg", http.StatusNotFound},
		{"GET", "/api/tiles/3/5/2.PNG", http.StatusNotFound},
		{"GET", "/api/tiles/3/5/2", http.StatusNotFound},
		{"GET", "/api/tiles/3/5/.png", http.StatusNotFound},
		{"GET", "/api/tiles/3/5/2.png/", http.StatusNotFound},
		{"GET", "/api/tiles/3/5/2/1.png", http.StatusNotFound},
		{"GET", "/api/tiles/3/5.png", http.StatusNotFound},
		{"GET", "/api/tiles/", http.StatusNotFound},
		{"GET", "/api/tiles", http.StatusNotFound},
		{"GET", "/api/tiles//3/5/2.png", http.StatusNotFound},
		{"GET", "/api/tiles/3//2.png", http.StatusNotFound},
		{"GET", "/api/tiles/3/../2.png", http.StatusNotFound},
		{"GET", "/api/tiles/3/5/%2e%2e.png", http.StatusNotFound},
		{"GET", "/api/tiles/3/5/2%2f1.png", http.StatusNotFound},
		{"GET", "/api/tiles/03/5/2.png", http.StatusBadRequest},
		{"GET", "/api/tiles/3/05/2.png", http.StatusBadRequest},
		{"GET", "/api/tiles/-3/5/2.png", http.StatusBadRequest},
		{"GET", "/api/tiles/3/-5/2.png", http.StatusBadRequest},
		{"GET", "/api/tiles/+3/5/2.png", http.StatusBadRequest},
		{"GET", "/api/tiles/3.0/5/2.png", http.StatusBadRequest},
		{"GET", "/api/tiles/a/5/2.png", http.StatusBadRequest},
		{"GET", "/api/tiles/3/0x5/2.png", http.StatusBadRequest},
		{"GET", "/api/tiles/3/8/2.png", http.StatusBadRequest},
		{"GET", "/api/tiles/3/5/8.png", http.StatusBadRequest},
		{"GET", "/api/tiles/0/1/0.png", http.StatusBadRequest},
		{"GET", "/api/tiles/19/0/0.png", http.StatusBadRequest},
		{"GET", "/api/tiles/99999999999999999999/0/0.png", http.StatusBadRequest},
		{"GET", "/api/tiles/3/5/2.png?access_token=client", http.StatusBadRequest},
		{"GET", "/api/tiles/3/5/2.png?style=other", http.StatusBadRequest},
		{"GET", "/api/tiles/3/5/2.png?", http.StatusBadRequest},
		{"GET", "/api/tiles/3/5/2.png?username=x&upstream=http://evil", http.StatusBadRequest},
	} {
		rec := env.do(tc.method, tc.target)
		if rec.Code != tc.status {
			t.Errorf("%s %s: %d, want %d", tc.method, tc.target, rec.Code, tc.status)
		}
		assertNoStore(t, rec)
		if tc.status == http.StatusMethodNotAllowed && rec.Header().Get("Allow") != "GET" {
			t.Errorf("%s: Allow = %q", tc.method, rec.Header().Get("Allow"))
		}
	}
	if n := len(env.hits()); n != 0 {
		t.Fatalf("%d invalid requests reached Mapbox", n)
	}
	if rec := env.get("/api/tiles/18/262143/0.png"); rec.Code != http.StatusOK {
		t.Errorf("highest allowed zoom with the last column: %d", rec.Code)
	}
	if rec := env.get("/api/tiles/0/0/0.png"); rec.Code != http.StatusOK {
		t.Errorf("world tile: %d", rec.Code)
	}
}

func TestTileRequestsAreRateLimitedByClientAddress(t *testing.T) {
	env := newTileEnv(t, okMapbox(t), nil)
	if rec := env.get("/api/tiles/3/5/2.png"); rec.Code != http.StatusOK {
		t.Fatalf("allowed request: %d", rec.Code)
	}
	if len(env.limiter.keys) != 1 || !strings.Contains(env.limiter.keys[0], clientIP) || env.limiter.limit != 100 {
		t.Fatalf("limiter saw keys=%v limit=%d", env.limiter.keys, env.limiter.limit)
	}
	// A client-supplied forwarding header must not choose the counted address.
	env.get("/api/tiles/3/5/2.png", func(r *http.Request) { r.Header.Set("X-Forwarded-For", "198.51.100.1") })
	if last := env.limiter.keys[len(env.limiter.keys)-1]; strings.Contains(last, "198.51.100.1") {
		t.Errorf("X-Forwarded-For chose the rate-limit key: %s", last)
	}

	before := len(env.hits())
	env.limiter.allow = false
	rec := env.get("/api/tiles/3/5/2.png")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limited request: %d", rec.Code)
	}
	assertNoStore(t, rec)
	assertNoSecrets(t, rec)
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
	if len(env.hits()) != before {
		t.Error("a limited request reached Mapbox")
	}

	env.limiter.allow, env.limiter.err = true, errors.New("redis down")
	rec = env.get("/api/tiles/3/5/2.png")
	if rec.Code != http.StatusBadGateway || len(env.hits()) != before {
		t.Fatalf("limiter outage: %d, upstream hits %d", rec.Code, len(env.hits())-before)
	}
	assertNoStore(t, rec)
}

type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logCapture) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	logs := &logCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func TestUpstreamFailuresAreSanitized(t *testing.T) {
	const leak = "Invalid token " + testToken + " for https://api.mapbox.com/styles/v1/test-user/journal-style"
	over := bytes.Repeat([]byte("x"), 2<<20)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect target was contacted: %s", r.URL)
	}))
	t.Cleanup(other.Close)
	stream := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Transfer-Encoding", "chunked")
		_, _ = w.Write(tilePNG(t, 256))
		w.(http.Flusher).Flush()
		_, _ = w.Write(over)
	}
	for name, tc := range map[string]struct {
		handler http.HandlerFunc
		mutate  func(*tiles.Config)
	}{
		"unauthorized":  {handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, leak, http.StatusUnauthorized) }},
		"forbidden":     {handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, leak, http.StatusForbidden) }},
		"missing style": {handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, leak, http.StatusNotFound) }},
		"upstream 429":  {handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, leak, http.StatusTooManyRequests) }},
		"upstream 500":  {handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, leak, http.StatusInternalServerError) }},
		"redirect": {handler: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+"/steal?access_token="+testToken, http.StatusFound)
		}},
		"html with 200": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "<html>"+leak+"</html>")
		}},
		"jpeg declared as png": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\xff\xd8\xff\xe0\x00\x10JFIF"))
		}},
		"empty 200": {handler: func(w http.ResponseWriter, r *http.Request) {}},
		"wrong tile size": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(tilePNG(t, 512))
		}},
		"truncated png": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(tilePNG(t, 256)[:20])
		}},
		"declared too large": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", strconv.Itoa(len(over)))
			_, _ = w.Write(over)
		}},
		"streamed too large":     {handler: stream},
		"no upstream connection": {handler: okMapbox(t), mutate: nil},
	} {
		t.Run(name, func(t *testing.T) {
			logs := captureLogs(t)
			env := newTileEnv(t, tc.handler, tc.mutate)
			if name == "no upstream connection" {
				env.rt.target = "http://127.0.0.1:1"
			}
			rec := env.get("/api/tiles/3/5/2.png")
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status %d, want 502: %s", rec.Code, rec.Body.String())
			}
			assertNoStore(t, rec)
			assertNoSecrets(t, rec)
			if strings.Contains(rec.Body.String(), "Invalid token") {
				t.Errorf("upstream body passed through: %s", rec.Body.String())
			}
			if out := logs.String(); strings.Contains(out, testToken) || strings.Contains(out, "access_token") {
				t.Errorf("log leaks the credential: %s", out)
			}
		})
	}
}

func TestUpstreamTimeoutIsAGatewayTimeout(t *testing.T) {
	logs := captureLogs(t)
	released := make(chan struct{})
	env := newTileEnv(t, func(w http.ResponseWriter, r *http.Request) {
		defer close(released)
		<-r.Context().Done()
	}, func(c *tiles.Config) { c.UpstreamTimeout = 50 * time.Millisecond })
	start := time.Now()
	rec := env.get("/api/tiles/3/5/2.png")
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status %d, want 504", rec.Code)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("the timeout was not enforced")
	}
	assertNoStore(t, rec)
	assertNoSecrets(t, rec)
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("the upstream request was not cancelled after the timeout")
	}
	if out := logs.String(); strings.Contains(out, testToken) || strings.Contains(out, "access_token") {
		t.Errorf("log leaks the credential: %s", out)
	}
}

func TestSlowUpstreamBodyIsCutOffByTheTimeout(t *testing.T) {
	env := newTileEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tilePNG(t, 256)[:40])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}, func(c *tiles.Config) { c.UpstreamTimeout = 50 * time.Millisecond })
	if rec := env.get("/api/tiles/3/5/2.png"); rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status %d, want 504", rec.Code)
	}
}

func TestClientCancellationStopsTheUpstreamRequest(t *testing.T) {
	started, released := make(chan struct{}), make(chan struct{})
	env := newTileEnv(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(released)
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/tiles/3/5/2.png", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		env.router.ServeHTTP(rec, req)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the request never reached Mapbox")
	}
	cancel()
	for name, ch := range map[string]chan struct{}{"upstream request": released, "handler": done} {
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s kept running after the client left", name)
		}
	}
	if rec.Body.Len() != 0 {
		t.Errorf("wrote a body to a client that left: %q", rec.Body.String())
	}
}

func TestTileTracesNeverRecordTheToken(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	// Anything instrumented that talks to Mapbox would report to the global provider.
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	fail := false
	env := newTileEnvWith(t, func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "Invalid token "+testToken, http.StatusUnauthorized)
			return
		}
		okMapbox(t)(w, r)
	}, nil, otelgin.Middleware("tripmap", otelgin.WithTracerProvider(provider)))
	if rec := env.get("/api/tiles/3/5/2.png"); rec.Code != http.StatusOK {
		t.Fatalf("success: %d", rec.Code)
	}
	fail = true
	if rec := env.get("/api/tiles/3/5/2.png"); rec.Code != http.StatusBadGateway {
		t.Fatalf("failure: %d", rec.Code)
	}
	ended := spans.Ended()
	if len(ended) == 0 {
		t.Fatal("no spans were recorded; the check proves nothing")
	}
	for _, span := range ended {
		dump := span.Name()
		for _, attr := range span.Attributes() {
			dump += " " + string(attr.Key) + "=" + attr.Value.Emit()
		}
		for _, event := range span.Events() {
			dump += " " + event.Name
			for _, attr := range event.Attributes {
				dump += " " + attr.Value.Emit()
			}
		}
		if strings.Contains(dump, testToken) || strings.Contains(dump, "access_token") || strings.Contains(dump, "api.mapbox.com") {
			t.Errorf("span records the credential or upstream URL: %s", dump)
		}
	}
}
