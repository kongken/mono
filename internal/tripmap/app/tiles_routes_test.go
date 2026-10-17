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
		"Last-Modified": "Sat, 17 Oct 2026 10:00:00 GMT",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if etag := rec.Header().Get("ETag"); !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `-abc123"`) {
		t.Errorf("ETag = %q, want the upstream validator scoped to the style", etag)
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

// validatedMapbox is a Mapbox that honours conditional requests the way the real
// one does: the current ETag and Last-Modified identify the tile's content.
type validatedMapbox struct {
	mu            sync.Mutex
	etag          string
	lastModified  string
	cacheControl  string
	omitETagOn304 bool
}

func (m *validatedMapbox) set(etag, lastModified string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.etag, m.lastModified = etag, lastModified
}

func (m *validatedMapbox) handler(t testing.TB) http.HandlerFunc {
	body := tilePNG(t, 256)
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		etag, modified, cc, omit := m.etag, m.lastModified, m.cacheControl, m.omitETagOn304
		m.mu.Unlock()
		h := w.Header()
		h.Set("Cache-Control", cc)
		h.Set("Expires", "Sat, 17 Oct 2026 22:00:00 GMT")
		h.Set("Last-Modified", modified)
		h.Set("Set-Cookie", "mapbox=1")
		matched := false
		if inm := r.Header.Get("If-None-Match"); inm != "" {
			for _, candidate := range strings.Split(inm, ",") {
				if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == etag {
					matched = true
				}
			}
		} else if ims := r.Header.Get("If-Modified-Since"); ims != "" {
			if since, err := http.ParseTime(ims); err == nil {
				if last, err := http.ParseTime(modified); err == nil && !last.After(since) {
					matched = true
				}
			}
		}
		if matched {
			if !omit {
				h.Set("ETag", etag)
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		h.Set("ETag", etag)
		h.Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}
}

func newValidatedMapbox() *validatedMapbox {
	return &validatedMapbox{etag: `"v1"`, lastModified: "Sat, 17 Oct 2026 10:00:00 GMT", cacheControl: "max-age=43200, s-maxage=43200"}
}

func upstreamHeader(t *testing.T, env *tileEnv, name string) []string {
	t.Helper()
	hits := env.hits()
	if len(hits) == 0 {
		t.Fatal("Mapbox was not contacted")
	}
	return hits[len(hits)-1].Header.Values(name)
}

func TestTileValidatorsAreForwardedAndNotModifiedIsRelayed(t *testing.T) {
	mock := newValidatedMapbox()
	env := newTileEnv(t, mock.handler(t), nil)
	const target = "/api/tiles/3/5/2.png"

	first := env.get(target)
	if first.Code != http.StatusOK {
		t.Fatalf("first request: %d", first.Code)
	}
	etag := first.Header().Get("ETag")
	if etag == "" || etag == `"v1"` {
		t.Fatalf("ETag %q must be set and scoped to the style", etag)
	}
	if got := upstreamHeader(t, env, "If-None-Match"); len(got) != 0 {
		t.Errorf("an unconditional request sent If-None-Match %v upstream", got)
	}

	// The browser revalidates with the ETag it was given: Mapbox sees its own validator.
	hit := env.get(target, func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if hit.Code != http.StatusNotModified {
		t.Fatalf("conditional request: %d, want 304", hit.Code)
	}
	if got := upstreamHeader(t, env, "If-None-Match"); len(got) != 1 || got[0] != `"v1"` {
		t.Errorf("Mapbox saw If-None-Match %v, want its own validator", got)
	}
	if hit.Body.Len() != 0 || hit.Header().Get("Content-Type") != "" || hit.Header().Get("Content-Length") != "" {
		t.Errorf("304 carries a body or entity headers: %q %v", hit.Body.String(), hit.Header())
	}
	for _, k := range []string{"Cache-Control", "Expires", "ETag", "Last-Modified"} {
		if hit.Header().Get(k) != first.Header().Get(k) {
			t.Errorf("%s: 304 has %q, 200 has %q", k, hit.Header().Get(k), first.Header().Get(k))
		}
	}
	if v := hit.Header().Get("Set-Cookie"); v != "" {
		t.Errorf("upstream Set-Cookie leaked on 304: %q", v)
	}
	assertNoSecrets(t, hit)

	// A weak comparison and a list of validators both match.
	for _, inm := range []string{"W/" + etag, `"unrelated", ` + etag} {
		if rec := env.get(target, func(r *http.Request) { r.Header.Set("If-None-Match", inm) }); rec.Code != http.StatusNotModified {
			t.Errorf("If-None-Match %q: %d, want 304", inm, rec.Code)
		}
	}

	// The tile changes: the old validator no longer matches and the full tile returns.
	mock.set(`"v2"`, "Sat, 17 Oct 2026 12:00:00 GMT")
	changed := env.get(target, func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if changed.Code != http.StatusOK || changed.Body.Len() == 0 {
		t.Fatalf("after a content change: %d with %d bytes, want a full 200", changed.Code, changed.Body.Len())
	}
	if changed.Header().Get("ETag") == etag {
		t.Error("the new tile kept the old ETag")
	}
}

func TestTileIfModifiedSinceIsForwardedWhenThereIsNoETag(t *testing.T) {
	mock := newValidatedMapbox()
	env := newTileEnv(t, mock.handler(t), nil)
	const target = "/api/tiles/3/5/2.png"
	const lastModified = "Sat, 17 Oct 2026 10:00:00 GMT"

	rec := env.get(target, func(r *http.Request) { r.Header.Set("If-Modified-Since", lastModified) })
	if rec.Code != http.StatusNotModified {
		t.Fatalf("If-Modified-Since: %d, want 304", rec.Code)
	}
	if got := upstreamHeader(t, env, "If-Modified-Since"); len(got) != 1 || got[0] != lastModified {
		t.Errorf("Mapbox saw If-Modified-Since %v", got)
	}
	if rec.Header().Get("Last-Modified") != lastModified || rec.Header().Get("Cache-Control") == "" {
		t.Errorf("304 headers %v", rec.Header())
	}

	// An older date than the tile's own gets the full tile.
	if rec := env.get(target, func(r *http.Request) { r.Header.Set("If-Modified-Since", "Fri, 16 Oct 2026 10:00:00 GMT") }); rec.Code != http.StatusOK {
		t.Errorf("older If-Modified-Since: %d, want 200", rec.Code)
	}

	// An unparseable date is ignored rather than forwarded.
	before := len(env.hits())
	if rec := env.get(target, func(r *http.Request) { r.Header.Set("If-Modified-Since", "yesterday-ish\r") }); rec.Code != http.StatusOK {
		t.Errorf("garbage If-Modified-Since: %d, want 200", rec.Code)
	}
	if got := env.hits()[before].Header.Values("If-Modified-Since"); len(got) != 0 {
		t.Errorf("garbage date forwarded: %v", got)
	}

	// If-None-Match takes precedence: with an ETag in play the date is not sent.
	etag := env.get(target).Header().Get("ETag")
	env.get(target, func(r *http.Request) {
		r.Header.Set("If-None-Match", etag)
		r.Header.Set("If-Modified-Since", lastModified)
	})
	if got := upstreamHeader(t, env, "If-Modified-Since"); len(got) != 0 {
		t.Errorf("If-Modified-Since sent alongside If-None-Match: %v", got)
	}
}

func TestTileNotModifiedWithoutUpstreamETagKeepsTheClientsValidator(t *testing.T) {
	mock := newValidatedMapbox()
	mock.omitETagOn304 = true
	env := newTileEnv(t, mock.handler(t), nil)
	etag := env.get("/api/tiles/3/5/2.png").Header().Get("ETag")
	rec := env.get("/api/tiles/3/5/2.png", func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if rec.Code != http.StatusNotModified || rec.Header().Get("ETag") != etag {
		t.Fatalf("got %d with ETag %q, want 304 with %q", rec.Code, rec.Header().Get("ETag"), etag)
	}
}

func TestTileValidatorsDoNotCrossStyles(t *testing.T) {
	mock := newValidatedMapbox()
	a := newTileEnv(t, mock.handler(t), nil)
	b := newTileEnv(t, mock.handler(t), func(c *tiles.Config) { c.StyleID = "another-style" })
	c := newTileEnv(t, mock.handler(t), func(c *tiles.Config) { c.Username = "another-user" })
	d := newTileEnv(t, mock.handler(t), func(c *tiles.Config) { c.StyleVersion = "2" })

	const target = "/api/tiles/3/5/2.png"
	etags := map[string]string{}
	for name, env := range map[string]*tileEnv{"a": a, "b": b, "c": c, "d": d} {
		target := target
		if name == "d" {
			target += "?v=2"
		}
		etags[name] = env.get(target).Header().Get("ETag")
	}
	seen := map[string]string{}
	for name, etag := range etags {
		if other, dup := seen[etag]; dup {
			t.Errorf("styles %s and %s share ETag %q", name, other, etag)
		}
		seen[etag] = name
	}

	// A validator from style A never reaches Mapbox when asked of the others, even
	// though this Mapbox would have answered 304 for the same underlying content.
	for name, env := range map[string]*tileEnv{"b": b, "c": c, "d": d} {
		target := target
		if name == "d" {
			target += "?v=2"
		}
		before := len(env.hits())
		rec := env.get(target, func(r *http.Request) { r.Header.Set("If-None-Match", etags["a"]) })
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("style %s: a foreign validator got %d, want a full 200", name, rec.Code)
		}
		if got := env.hits()[before].Header.Values("If-None-Match"); len(got) != 0 {
			t.Errorf("style %s forwarded a foreign validator: %v", name, got)
		}
	}
	// Its own validator still works.
	if rec := b.get(target, func(r *http.Request) { r.Header.Set("If-None-Match", etags["b"]) }); rec.Code != http.StatusNotModified {
		t.Errorf("own validator: %d, want 304", rec.Code)
	}
}

func TestTileStyleVersionInTheURL(t *testing.T) {
	mock := newValidatedMapbox()
	env := newTileEnv(t, mock.handler(t), func(c *tiles.Config) { c.StyleVersion = "2026-10" })

	rec := env.get("/api/tiles/3/5/2.png?v=2026-10")
	if rec.Code != http.StatusOK {
		t.Fatalf("versioned URL: %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "max-age=43200, s-maxage=43200" {
		t.Errorf("versioned Cache-Control = %q: the freshness Mapbox allows must be kept, not extended", got)
	}
	if u := env.rt.requested()[0]; u.Query().Get("v") != "" || len(u.Query()) != 1 {
		t.Errorf("the version reached Mapbox: %s", u.RawQuery)
	}

	// The legacy URL (an older front end build) still works but must always revalidate,
	// so a style switch is never hidden behind a fresh cached copy.
	legacy := env.get("/api/tiles/3/5/2.png")
	if legacy.Code != http.StatusOK {
		t.Fatalf("unversioned URL: %d", legacy.Code)
	}
	if got := legacy.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("unversioned Cache-Control = %q, want no-cache", got)
	}
	if legacy.Header().Get("Expires") != "" {
		t.Errorf("unversioned response keeps Expires %q", legacy.Header().Get("Expires"))
	}
	if rec := env.get("/api/tiles/3/5/2.png", func(r *http.Request) { r.Header.Set("If-None-Match", legacy.Header().Get("ETag")) }); rec.Code != http.StatusNotModified || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("unversioned revalidation: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}

	before := len(env.hits())
	for _, target := range []string{
		"/api/tiles/3/5/2.png?v=2026-11",
		"/api/tiles/3/5/2.png?v=",
		"/api/tiles/3/5/2.png?v",
		"/api/tiles/3/5/2.png?v=2026-10&v=2026-10",
		"/api/tiles/3/5/2.png?v=2026-10&access_token=x",
		"/api/tiles/3/5/2.png?access_token=x&v=2026-10",
		"/api/tiles/3/5/2.png?V=2026-10",
		"/api/tiles/3/5/2.png?v=2026%2D10",
		"/api/tiles/3/5/2.png?style=other",
		"/api/tiles/3/5/2.png?",
	} {
		rec := env.get(target, func(r *http.Request) { r.Header.Set("If-None-Match", legacy.Header().Get("ETag")) })
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", target, rec.Code)
		}
		assertNoStore(t, rec)
	}
	if n := len(env.hits()); n != before {
		t.Errorf("%d rejected requests reached Mapbox", n-before)
	}
}

func TestTileVersionIsRefusedWhenNoneIsConfigured(t *testing.T) {
	env := newTileEnv(t, okMapbox(t), nil)
	for _, target := range []string{"/api/tiles/3/5/2.png?v=1", "/api/tiles/3/5/2.png?v="} {
		rec := env.get(target)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", target, rec.Code)
		}
		assertNoStore(t, rec)
	}
	if n := len(env.hits()); n != 0 {
		t.Errorf("%d requests reached Mapbox", n)
	}
	// Without a configured version nothing is added to the lifetime Mapbox allows.
	rec := env.get("/api/tiles/3/5/2.png")
	if got := rec.Header().Get("Cache-Control"); got != "max-age=43200, s-maxage=43200" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestTileConditionalRequestsKeepEveryGuard(t *testing.T) {
	mock := newValidatedMapbox()
	env := newTileEnv(t, mock.handler(t), nil)
	etag := env.get("/api/tiles/3/5/2.png").Header().Get("ETag")
	conditional := func(r *http.Request) {
		r.Header.Set("If-None-Match", etag)
		r.Header.Set("If-Modified-Since", "Sat, 17 Oct 2026 10:00:00 GMT")
	}
	before := len(env.hits())

	for _, tc := range []struct {
		method, target string
		status         int
	}{
		{"GET", "/api/tiles/3/8/2.png", http.StatusBadRequest},
		{"GET", "/api/tiles/3/5/2.png?access_token=x", http.StatusBadRequest},
		{"GET", "/api/tiles/3/5/2.jpg", http.StatusNotFound},
		{"POST", "/api/tiles/3/5/2.png", http.StatusMethodNotAllowed},
	} {
		rec := env.do(tc.method, tc.target, conditional)
		if rec.Code != tc.status {
			t.Errorf("%s %s: %d, want %d", tc.method, tc.target, rec.Code, tc.status)
		}
		assertNoStore(t, rec)
	}

	env.limiter.allow = false
	rec := env.get("/api/tiles/3/5/2.png", conditional)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limited conditional request: %d", rec.Code)
	}
	assertNoStore(t, rec)
	env.limiter.allow = true
	if n := len(env.hits()); n != before {
		t.Fatalf("%d guarded conditional requests reached Mapbox", n-before)
	}

	disabled := newTileEnv(t, mock.handler(t), func(c *tiles.Config) { *c = tiles.Config{} })
	if rec := disabled.get("/api/tiles/3/5/2.png", conditional); rec.Code != http.StatusNotFound {
		t.Errorf("disabled proxy: %d, want 404", rec.Code)
	} else {
		assertNoStore(t, rec)
	}

	// Conditional requests still reach Mapbox with nothing but the validators added.
	env.get("/api/tiles/3/5/2.png", conditional)
	sent := env.hits()[before]
	allowed := map[string]bool{"Accept": true, "User-Agent": true, "Accept-Encoding": true, "If-None-Match": true}
	for k := range sent.Header {
		if !allowed[k] {
			t.Errorf("header %s was sent to Mapbox with a conditional request", k)
		}
	}
}

func TestTileUnexpectedNotModifiedIsAnError(t *testing.T) {
	logs := captureLogs(t)
	env := newTileEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=43200")
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(http.StatusNotModified)
	}, nil)
	// No validator was sent, so there is no cached copy a 304 could refer to.
	rec := env.get("/api/tiles/3/5/2.png")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unconditional request answered 304 upstream: %d, want 502", rec.Code)
	}
	assertNoStore(t, rec)
	assertNoSecrets(t, rec)
	// A foreign validator is not forwarded, so a 304 for it is just as unexpected.
	rec = env.get("/api/tiles/3/5/2.png", func(r *http.Request) { r.Header.Set("If-None-Match", `"someone-elses"`) })
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("foreign validator: %d, want 502", rec.Code)
	}
	if out := logs.String(); strings.Contains(out, testToken) || strings.Contains(out, "access_token") {
		t.Errorf("log leaks the credential: %s", out)
	}
}

func TestTileErrorsAreNeverCachedOnConditionalRequests(t *testing.T) {
	var status atomicStatus
	env := newTileEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=43200")
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(status.get())
		_, _ = io.WriteString(w, "Invalid token "+testToken)
	}, nil)
	for _, code := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusPreconditionFailed} {
		status.set(code)
		rec := env.get("/api/tiles/3/5/2.png", func(r *http.Request) { r.Header.Set("If-None-Match", `"v1"`) })
		if rec.Code != http.StatusBadGateway {
			t.Errorf("upstream %d: %d, want 502", code, rec.Code)
		}
		assertNoStore(t, rec)
		assertNoSecrets(t, rec)
		if rec.Header().Get("ETag") != "" || rec.Header().Get("Last-Modified") != "" {
			t.Errorf("upstream %d: error carries validators %v", code, rec.Header())
		}
	}
}

type atomicStatus struct {
	mu sync.Mutex
	v  int
}

func (s *atomicStatus) set(v int) { s.mu.Lock(); s.v = v; s.mu.Unlock() }
func (s *atomicStatus) get() int  { s.mu.Lock(); defer s.mu.Unlock(); return s.v }
