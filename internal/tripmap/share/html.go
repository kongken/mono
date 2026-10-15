package share

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Meta is everything the share HTML announces about a page. It only holds
// aggregate numbers and fixed text, never city names, notes or photos.
type Meta struct {
	Title       string
	Description string
	// PageURL and ImageURL are absolute.
	PageURL  string
	ImageURL string
	// Indexable is true for "public" shares; "link" shares are noindex.
	Indexable bool
}

// MetaFor derives the metadata of a published share.
func MetaFor(c *Content, pageURL, imageURL string, indexable bool) Meta {
	return Meta{
		Title: TextTitle + " · 山海足迹",
		Description: fmt.Sprintf("已探索 %d / %d 个省份，打卡 %d 座城市，共 %d 次旅行。", c.Stats.ExploredProvinces, c.Stats.TotalProvinces,
			c.Stats.Cities, c.Stats.Trips),
		PageURL: pageURL, ImageURL: imageURL, Indexable: indexable,
	}
}

// Robots is the robots directive matching the visibility.
func (m Meta) Robots() string {
	if m.Indexable {
		return "index, follow"
	}
	return "noindex, nofollow"
}

func (m Meta) tags() string {
	e := html.EscapeString
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, "    "+format+"\n", args...) }
	line(`<title>%s</title>`, e(m.Title))
	line(`<meta name="description" content="%s" />`, e(m.Description))
	line(`<meta name="robots" content="%s" />`, e(m.Robots()))
	line(`<meta name="referrer" content="no-referrer" />`)
	line(`<meta property="og:site_name" content="山海足迹" />`)
	line(`<meta property="og:type" content="website" />`)
	line(`<meta property="og:title" content="%s" />`, e(m.Title))
	line(`<meta property="og:description" content="%s" />`, e(m.Description))
	line(`<meta property="og:url" content="%s" />`, e(m.PageURL))
	line(`<meta property="og:image" content="%s" />`, e(m.ImageURL))
	line(`<meta property="og:image:type" content="image/png" />`)
	line(`<meta property="og:image:width" content="%d" />`, ImageWidth)
	line(`<meta property="og:image:height" content="%d" />`, ImageHeight)
	line(`<meta property="og:image:alt" content="%s" />`, e(m.Description))
	line(`<meta name="twitter:card" content="summary_large_image" />`)
	line(`<meta name="twitter:title" content="%s" />`, e(m.Title))
	line(`<meta name="twitter:description" content="%s" />`, e(m.Description))
	line(`<meta name="twitter:image" content="%s" />`, e(m.ImageURL))
	return b.String()
}

var (
	titleTag   = regexp.MustCompile(`(?is)<title\b[^>]*>.*?</title>`)
	metaTag    = regexp.MustCompile(`(?is)<meta\b[^>]*\b(?:name|property)\s*=\s*["'](?:description|robots|referrer|og:[^"']*|twitter:[^"']*)["'][^>]*>`)
	headClose  = regexp.MustCompile(`(?i)</head\s*>`)
	headOpen   = regexp.MustCompile(`(?i)<head\b[^>]*>`)
	defaultIdx = []byte(`<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8" /><meta name="viewport" content="width=device-width, initial-scale=1.0" /></head><body><div id="root"></div></body></html>`)
)

// Inject writes the metadata into an application index.html, replacing any
// title, description, robots and Open Graph tags it already carries.
func Inject(index []byte, m Meta) []byte {
	page := titleTag.ReplaceAll(index, nil)
	page = metaTag.ReplaceAll(page, nil)
	tags := []byte(m.tags())
	if loc := headClose.FindIndex(page); loc != nil {
		return bytes.Join([][]byte{page[:loc[0]], tags, page[loc[0]:]}, nil)
	}
	if loc := headOpen.FindIndex(page); loc != nil {
		return bytes.Join([][]byte{page[:loc[1]], []byte("\n"), tags, page[loc[1]:]}, nil)
	}
	return Fallback(m)
}

// Fallback is the minimal page served when the application index is
// unavailable: crawlers still get the metadata and a person gets the numbers.
func Fallback(m Meta) []byte {
	out := Inject(defaultIdx, m)
	body := fmt.Sprintf(`<main style="font-family:sans-serif;max-width:32rem;margin:3rem auto;padding:0 1rem"><h1>%s</h1><p>%s</p></main>`,
		html.EscapeString(m.Title), html.EscapeString(m.Description))
	return bytes.Replace(out, []byte(`<div id="root"></div>`), []byte(`<div id="root">`+body+`</div>`), 1)
}

// NotFoundPage is the one 404 body every failed lookup returns. It carries no
// metadata and no hint why the link failed.
var NotFoundPage = []byte(`<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8" /><meta name="viewport" content="width=device-width, initial-scale=1.0" /><meta name="robots" content="noindex, nofollow" /><meta name="referrer" content="no-referrer" /><title>链接不存在</title></head><body style="font-family:sans-serif;max-width:32rem;margin:3rem auto;padding:0 1rem"><h1>链接不存在或已撤销</h1><p>请向分享者确认链接是否有效。</p></body></html>`)

// IndexSource fetches the application's index.html from a trusted configured
// URL and keeps it briefly. It never follows redirects, bounds time and size,
// and marks its request so a misconfigured URL that routes back to /s/ is
// refused instead of recursing.
type IndexSource struct {
	url    string
	client *http.Client

	mu      sync.Mutex
	cached  []byte
	fetched time.Time
}

const (
	// IndexFetchHeader marks the backend's own index request.
	IndexFetchHeader = "X-Tripmap-Index-Fetch"
	indexTTL         = 60 * time.Second
	// A stale copy is better than none for a short outage, but not forever.
	indexStale    = 10 * time.Minute
	indexMaxBytes = 1 << 20
)

// NewIndexSource returns nil for an empty URL.
func NewIndexSource(url string) *IndexSource {
	if url == "" {
		return nil
	}
	return &IndexSource{url: url, client: &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Get returns the index page, or nil when none is available.
func (s *IndexSource) Get(ctx context.Context) []byte {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && time.Since(s.fetched) < indexTTL {
		return s.cached
	}
	page, err := s.fetch(ctx)
	if err == nil {
		s.cached, s.fetched = page, time.Now()
		return page
	}
	if s.cached != nil && time.Since(s.fetched) < indexStale {
		return s.cached
	}
	return nil
}

func (s *IndexSource) fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(IndexFetchHeader, "1")
	req.Header.Set("Accept", "text/html")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return nil, errors.New("unexpected index response")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, indexMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > indexMaxBytes || len(body) == 0 {
		return nil, errors.New("index page has an unusable size")
	}
	return body, nil
}
