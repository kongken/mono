package tiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image/png"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kongken/mono/internal/platform/httpx"
)

// PathPrefix is where the route lives; tiles are PathPrefix + "{z}/{x}/{y}.png".
const PathPrefix = "/api/tiles/"

const rateWindow = time.Minute

// Limiter counts one event for key and reports whether it is within limit for the
// current window. application.Limiter (Redis) satisfies it.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error)
}

// Deps are the collaborators the handler cannot own.
type Deps struct {
	// ClientIPHeader names the header a trusted proxy writes the caller's address to
	// (the BFF overwrites X-Real-IP); empty uses the TCP peer.
	ClientIPHeader string
	// Limiter is resolved on every request because the stores connect after the
	// routes are mounted; nil means the limiter is not available yet.
	Limiter func() Limiter
	// Client sends the requests to Mapbox; nil uses a dedicated transport. Tests
	// replace it to keep the network out.
	Client *http.Client
}

// Handler serves GET /api/tiles/{z}/{x}/{y}.png.
type Handler struct {
	cfg     Config
	enabled bool
	deps    Deps
	client  *http.Client
	// fingerprint identifies the effective style (account, style and version, never the
	// token). It scopes the ETags browsers see, so a validator earned under one style is
	// never answered for another.
	fingerprint string
}

// New builds the handler. An invalid or disabled configuration yields a handler
// that answers 404; startup validation (Config.Validate) is what reports the error.
func New(cfg Config, deps Deps) *Handler {
	h := &Handler{cfg: cfg, deps: deps}
	if cfg.Enabled {
		if err := cfg.Validate(); err != nil {
			// Startup validation already refuses this; the handler stays closed rather
			// than fetching with a configuration it cannot trust. The error names a
			// field only, never a value.
			slog.Error("mapbox tiles disabled by invalid configuration", "error", err)
		} else {
			h.enabled = true
			sum := sha256.Sum256([]byte(cfg.Username + "\x00" + cfg.StyleID + "\x00" + cfg.StyleVersion))
			h.fingerprint = hex.EncodeToString(sum[:5])
		}
	}
	h.SetClient(deps.Client)
	return h
}

// SetClient replaces the HTTP client used for Mapbox (tests). Redirects are never
// followed whatever the client says: Mapbox does not redirect tiles, and a redirect
// could carry the token somewhere else.
func (h *Handler) SetClient(c *http.Client) {
	if c == nil {
		// A dedicated plain transport: no tracing wrapper that would record the request
		// URL (it carries the token), and no type assertion on the shared default.
		c = &http.Client{Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        20,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second,
		}}
	}
	copied := *c
	copied.Timeout = 0 // the per-request context owns the deadline, body included
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	h.client = &copied
}

// SetLimiter replaces the limiter (tests).
func (h *Handler) SetLimiter(l Limiter) { h.deps.Limiter = func() Limiter { return l } }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.enabled {
		fail(w, http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, http.StatusMethodNotAllowed)
		return
	}
	z, x, y, shape := h.parsePath(r.URL.Path)
	if !shape {
		fail(w, http.StatusNotFound)
		return
	}
	versioned, ok := h.checkQuery(r.URL)
	if !ok || !h.validCoords(z, x, y) {
		fail(w, http.StatusBadRequest)
		return
	}

	limiter := h.limiter()
	if limiter == nil {
		fail(w, http.StatusBadGateway)
		return
	}
	addr := httpx.ClientAddr(h.deps.ClientIPHeader, r.Header, r.RemoteAddr)
	allowed, err := limiter.Allow(r.Context(), "tiles:"+addr, h.cfg.ratePerMinute(), rateWindow)
	switch {
	case err != nil:
		if r.Context().Err() == nil {
			slog.Warn("mapbox tiles: rate limiter unavailable", "error", err)
		}
		fail(w, http.StatusBadGateway)
		return
	case !allowed:
		w.Header().Set("Retry-After", strconv.Itoa(int(rateWindow.Seconds())))
		fail(w, http.StatusTooManyRequests)
		return
	}

	h.fetch(w, r, z, x, y, versioned)
}

// checkQuery accepts no query, or exactly "v=<style_version>" when a version is
// configured. Anything else is unknown: the token, style and upstream are never the
// client's to pick, and one tile must have one URL per style. versioned reports
// whether the URL carried the configured version.
func (h *Handler) checkQuery(u *url.URL) (versioned, ok bool) {
	if u.ForceQuery {
		return false, false
	}
	if u.RawQuery == "" {
		return false, true
	}
	value, found := strings.CutPrefix(u.RawQuery, "v=")
	if !found || h.cfg.StyleVersion == "" || value != h.cfg.StyleVersion {
		return false, false
	}
	return true, true
}

func (h *Handler) limiter() Limiter {
	if h.deps.Limiter == nil {
		return nil
	}
	return h.deps.Limiter()
}

// parsePath splits PathPrefix + "z/x/y.png". shape is false when the path is not
// exactly that form; the numbers are checked separately by validCoords.
func (h *Handler) parsePath(p string) (z, x, y string, shape bool) {
	rest, ok := strings.CutPrefix(p, PathPrefix)
	if !ok {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return "", "", "", false
	}
	last, ok := strings.CutSuffix(parts[2], ".png")
	if !ok {
		return "", "", "", false
	}
	// Empty and dot-only segments ("", ".", "..") are path tricks, not coordinates.
	for _, part := range []string{parts[0], parts[1], last} {
		if strings.Trim(part, ".") == "" {
			return "", "", "", false
		}
	}
	return parts[0], parts[1], last, true
}

// canonicalInt parses a non-negative decimal without sign, leading zeros or
// separators, so one tile has exactly one URL.
func canonicalInt(s string) (uint64, bool) {
	if s == "" || len(s) > 10 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

func (h *Handler) validCoords(zs, xs, ys string) bool {
	z, ok := canonicalInt(zs)
	if !ok || z > uint64(h.cfg.maxZoom()) {
		return false
	}
	limit := uint64(1) << z
	x, ok := canonicalInt(xs)
	if !ok || x >= limit {
		return false
	}
	y, ok := canonicalInt(ys)
	return ok && y < limit
}

func (h *Handler) upstreamURL(z, x, y string) string {
	u := url.URL{
		Scheme:   "https",
		Host:     upstreamHost,
		Path:     "/styles/v1/" + h.cfg.Username + "/" + h.cfg.StyleID + "/tiles/" + strconv.Itoa(tileSize) + "/" + z + "/" + x + "/" + y + ".png",
		RawQuery: url.Values{"access_token": {h.cfg.Token}}.Encode(),
	}
	return u.String()
}

// fetch asks Mapbox for one tile and relays it, or answers with a generic error.
func (h *Handler) fetch(w http.ResponseWriter, r *http.Request, z, x, y string, versioned bool) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.upstreamTimeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.upstreamURL(z, x, y), nil)
	if err != nil {
		h.upstreamFailed(w, r, ctx, "request", err)
		return
	}
	// Only what Mapbox needs. Nothing the browser sent is forwarded: no Authorization,
	// Cookie, Referer, Origin, or internal identity claims. The validators are the one
	// exception, rebuilt from what this style issued rather than copied.
	req.Header.Set("Accept", "image/png")
	req.Header.Set("User-Agent", "shanhai-footprint-tiles/1")
	req.Header.Set("Accept-Encoding", "identity")
	cond := h.conditional(r.Header)
	cond.apply(req.Header)

	res, err := h.client.Do(req)
	if err != nil {
		h.upstreamFailed(w, r, ctx, "transport", err)
		return
	}
	defer res.Body.Close()
	max := h.cfg.maxResponseBytes()
	switch {
	case res.StatusCode == http.StatusNotModified && cond.sent():
		h.notModified(w, res, cond, versioned)
		return
	case res.StatusCode != http.StatusOK:
		h.upstreamFailed(w, r, ctx, "status "+strconv.Itoa(res.StatusCode), nil)
		return
	case res.ContentLength > max:
		h.upstreamFailed(w, r, ctx, "response too large", nil)
		return
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		h.upstreamFailed(w, r, ctx, "body", err)
		return
	}
	if int64(len(body)) > max {
		h.upstreamFailed(w, r, ctx, "response too large", nil)
		return
	}
	// The body must be a PNG tile of the size we asked for before it reaches a browser.
	cfg, err := png.DecodeConfig(bytes.NewReader(body))
	if err != nil || cfg.Width != tileSize || cfg.Height != tileSize {
		h.upstreamFailed(w, r, ctx, "invalid tile image", nil)
		return
	}

	header := w.Header()
	h.relayCacheHeaders(header, res.Header, versioned, nil)
	header.Set("Content-Type", "image/png")
	header.Set("Content-Length", strconv.Itoa(len(body)))
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// notModified answers a conditional request Mapbox confirmed: no body, and the same
// cache headers and validator the full response would have carried.
func (h *Handler) notModified(w http.ResponseWriter, res *http.Response, cond conditionalHeaders, versioned bool) {
	header := w.Header()
	// A 304 should repeat the validator; when Mapbox leaves it out and the browser
	// offered exactly one, that is the one which matched.
	var fallback *entityTag
	if len(cond.tags) == 1 {
		fallback = &cond.tags[0]
	}
	h.relayCacheHeaders(header, res.Header, versioned, fallback)
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotModified)
}

// relayCacheHeaders copies the cache headers Mapbox's terms ask us to honour, and
// nothing else. Freshness is never extended. The ETag is scoped to the style.
// Without a configured version, a URL that carries none is the legacy form: it may be
// held by browsers across a style switch, so it must always revalidate.
func (h *Handler) relayCacheHeaders(dst, src http.Header, versioned bool, fallback *entityTag) {
	for _, name := range []string{"Cache-Control", "Expires", "Last-Modified"} {
		if v := src.Get(name); v != "" {
			dst.Set(name, v)
		}
	}
	if tag, ok := parseEntityTag(src.Get("ETag")); ok {
		dst.Set("ETag", h.scopeETag(tag))
	} else if fallback != nil {
		dst.Set("ETag", h.scopeETag(*fallback))
	}
	if h.cfg.StyleVersion != "" && !versioned {
		dst.Set("Cache-Control", "no-cache")
		dst.Del("Expires")
	}
	if dst.Get("Cache-Control") == "" && dst.Get("Expires") == "" {
		dst.Set("Cache-Control", "no-cache")
	}
}

// upstreamFailed picks the status for a failed Mapbox call and logs only a short
// reason: the request URL, and the errors that repeat it, carry the token.
func (h *Handler) upstreamFailed(w http.ResponseWriter, r *http.Request, ctx context.Context, reason string, err error) {
	switch {
	case r.Context().Err() != nil:
		// The browser left; there is nobody to answer.
		return
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		slog.Warn("mapbox tiles: upstream timed out")
		fail(w, http.StatusGatewayTimeout)
		return
	}
	attrs := []any{"reason", reason}
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		attrs = append(attrs, "error", strings.ReplaceAll(err.Error(), h.cfg.Token, "[redacted]"))
	}
	slog.Warn("mapbox tiles: upstream unavailable", attrs...)
	fail(w, http.StatusBadGateway)
}

// fail answers with a fixed body and no caching, so an error is never stored and
// never explains anything about the upstream.
func fail(w http.ResponseWriter, status int) {
	header := w.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, http.StatusText(status)+"\n")
}
