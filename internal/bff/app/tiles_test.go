package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kongken/mono/internal/bff/config"
)

// tileUpstream stands in for tripmap's HTTP port and records what the BFF sent.
type tileUpstream struct {
	*httptest.Server
	mu   sync.Mutex
	seen []*http.Request
	// reply writes the response; the default is a small image with cache headers.
	reply http.HandlerFunc
}

func newTileUpstream(t *testing.T) *tileUpstream {
	u := &tileUpstream{}
	u.reply = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=43200")
		w.Header().Set("ETag", `"tile-1"`)
		w.Header().Set("Last-Modified", "Sat, 17 Oct 2026 10:00:00 GMT")
		// A service still configured for CORS; the BFF must drop this.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = io.WriteString(w, "PNGBYTES")
	}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			return
		}
		u.mu.Lock()
		u.seen = append(u.seen, r.Clone(r.Context()))
		u.mu.Unlock()
		u.reply(w, r)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *tileUpstream) requests() []*http.Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]*http.Request(nil), u.seen...)
}

func TestTilePathIsForwardedToTripmapWithoutCredentials(t *testing.T) {
	up := newTileUpstream(t)
	r := newBFF(t, &config.Config{Upstreams: map[string]string{"tripmap": up.URL}, CORSAllowedOrigins: []string{"https://trip.example.com"}, ClientIPHeader: "X-Forwarded-Client"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/tiles/3/5/2.png", nil)
	req.RemoteAddr = "192.0.2.50:4000"
	req.Header.Set("X-Forwarded-Client", "198.51.100.7")
	req.Header.Set("X-Real-IP", "6.6.6.6")
	req.Header.Set("Authorization", "Bearer browser-secret")
	req.Header.Set("Cookie", "shanhai_sid=session-secret; shanhai_csrf=csrf-secret")
	req.Header.Set("X-CSRF-Token", "csrf-secret")
	req.Header.Set("Origin", "https://trip.example.com")
	req.Header.Set("Accept", "image/png")
	rec := serve(r, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "PNGBYTES" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	for k, want := range map[string]string{"Content-Type": "image/png", "Cache-Control": "max-age=43200", "ETag": `"tile-1"`, "Last-Modified": "Sat, 17 Oct 2026 10:00:00 GMT"} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if got := rec.Header().Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "https://trip.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %v, want only the BFF's", got)
	}

	seen := up.requests()
	if len(seen) != 1 {
		t.Fatalf("%d requests reached tripmap", len(seen))
	}
	got := seen[0]
	if got.Method != http.MethodGet || got.URL.Path != "/api/tiles/3/5/2.png" || got.URL.RawQuery != "" {
		t.Errorf("tripmap saw %s %s", got.Method, got.URL)
	}
	if ip := got.Header.Get("X-Real-IP"); ip != "198.51.100.7" {
		t.Errorf("X-Real-IP = %q, want the BFF-resolved client address", ip)
	}
	for _, name := range []string{"Authorization", "Cookie", "X-Csrf-Token", "Origin", "X-Forwarded-Client"} {
		if v := got.Header.Get(name); v != "" {
			t.Errorf("%s reached tripmap: %q", name, v)
		}
	}
}

func TestTileStatusesAndErrorsPassThroughUncached(t *testing.T) {
	up := newTileUpstream(t)
	r := newBFF(t, &config.Config{Upstreams: map[string]string{"tripmap": up.URL}}, nil)
	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusGatewayTimeout} {
		up.reply = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, http.StatusText(status), status)
		}
		rec := serve(r, httptest.NewRequest(http.MethodGet, "/api/tiles/3/5/2.png", nil))
		if rec.Code != status || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("status %d came back as %d with Cache-Control %q", status, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
}

func TestTileRouteRejectsUncleanPathsAndUnreachableUpstream(t *testing.T) {
	up := newTileUpstream(t)
	r := newBFF(t, &config.Config{Upstreams: map[string]string{"tripmap": up.URL}}, nil)
	for _, target := range []string{"/api/tiles/../bff.v1.X/y", "/api/tiles//3/5/2.png", "/api/tiles/3/5/../2.png", "/api/tilesx/3/5/2.png", "/api/tile/3/5/2.png"} {
		if rec := serve(r, httptest.NewRequest(http.MethodGet, target, nil)); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", target, rec.Code)
		}
	}
	if n := len(up.requests()); n != 0 {
		t.Errorf("%d unclean requests were forwarded", n)
	}

	down := newTileUpstream(t)
	r = newBFF(t, &config.Config{Upstreams: map[string]string{"tripmap": down.URL}}, nil)
	down.Close()
	rec := serve(r, httptest.NewRequest(http.MethodGet, "/api/tiles/3/5/2.png", nil))
	if rec.Code != http.StatusBadGateway || rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), "127.0.0.1") {
		t.Errorf("unreachable tripmap: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
}
