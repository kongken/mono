package share

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testMeta() Meta {
	return Meta{Title: `T "<x>"`, Description: `a & b <script>alert(1)</script>`, PageURL: "https://x.test/s/t?a=1&b=2", ImageURL: "https://x.test/s/t/og.png", Indexable: false}
}

func TestInjectEscapesAndReplacesExistingTags(t *testing.T) {
	index := []byte(`<!doctype html><html><head><meta charset="utf-8"><title>old</title>
<meta name="description" content="old"><META PROPERTY='og:title' content="old">
<meta name="robots" content="index"><link rel="icon" href="/favicon.svg"><script type="module" src="/assets/index.js"></script></head><body><div id="root"></div></body></html>`)
	out := string(Inject(index, testMeta()))
	for _, bad := range []string{"<title>old", `content="old"`, `content="index"`, "<script>alert(1)", `<x>`} {
		if strings.Contains(out, bad) {
			t.Fatalf("%q survived:\n%s", bad, out)
		}
	}
	for _, want := range []string{`name="robots" content="noindex, nofollow"`, `/assets/index.js`, `href="/favicon.svg"`, `og:image" content="https://x.test/s/t/og.png"`,
		`content="https://x.test/s/t?a=1&amp;b=2"`, `name="referrer" content="no-referrer"`, "&lt;script&gt;"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "<title>") != 1 || strings.Count(out, `name="robots"`) != 1 {
		t.Fatalf("duplicate tags:\n%s", out)
	}
	m := testMeta()
	m.Indexable = true
	if !strings.Contains(string(Inject(index, m)), `content="index, follow"`) {
		t.Fatal("public shares must be indexable")
	}
}

func TestFallbackAndNotFoundPagesAreSelfContained(t *testing.T) {
	fb := string(Fallback(testMeta()))
	if !strings.Contains(fb, "og:image") || !strings.Contains(fb, "&lt;script&gt;") || strings.Contains(fb, "<script>") {
		t.Fatalf("fallback page: %s", fb)
	}
	nf := string(NotFoundPage)
	if !strings.Contains(nf, "noindex") || strings.Contains(nf, "og:") || strings.Contains(nf, "/assets/") {
		t.Fatalf("404 page leaks metadata or assets: %s", nf)
	}
}

func TestIndexSourceLimitsRedirectsAndFailures(t *testing.T) {
	if NewIndexSource("") != nil || (*IndexSource)(nil).Get(context.Background()) != nil {
		t.Fatal("an unset index source must yield nothing")
	}
	serve := func(h http.HandlerFunc) *IndexSource {
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		return NewIndexSource(srv.URL)
	}
	html := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
	if got := serve(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(IndexFetchHeader) == "" {
			t.Error("the marker header is missing")
		}
		html(w, "<html></html>")
	}).Get(context.Background()); string(got) != "<html></html>" {
		t.Fatalf("plain index: %q", got)
	}
	if got := serve(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/s/abc", http.StatusFound) }).Get(context.Background()); got != nil {
		t.Fatalf("a redirect was followed: %q", got)
	}
	if got := serve(func(w http.ResponseWriter, r *http.Request) { html(w, strings.Repeat("a", indexMaxBytes+1)) }).Get(context.Background()); got != nil {
		t.Fatal("an oversized index was accepted")
	}
	if got := serve(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }).Get(context.Background()); got != nil {
		t.Fatal("an error page was used as the index")
	}
	if got := serve(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}).Get(context.Background()); got != nil {
		t.Fatal("a non-HTML index was accepted")
	}
	start := time.Now()
	if got := serve(func(w http.ResponseWriter, r *http.Request) { time.Sleep(3 * time.Second) }).Get(context.Background()); got != nil || time.Since(start) > 2800*time.Millisecond {
		t.Fatalf("slow index was not cut off (%v)", time.Since(start))
	}
}

func TestIndexSourceKeepsAStaleCopyDuringOutages(t *testing.T) {
	healthy := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>ok</html>"))
	}))
	defer srv.Close()
	src := NewIndexSource(srv.URL)
	if src.Get(context.Background()) == nil {
		t.Fatal("first fetch failed")
	}
	healthy = false
	src.fetched = time.Now().Add(-2 * indexTTL)
	if got := src.Get(context.Background()); string(got) != "<html>ok</html>" {
		t.Fatalf("stale copy not served: %q", got)
	}
	src.fetched = time.Now().Add(-2 * indexStale)
	if src.Get(context.Background()) != nil {
		t.Fatal("a copy older than the stale limit was served")
	}
}
