package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.orx.me/apps/trip-map/internal/tripmap/auth"
	"go.orx.me/apps/trip-map/internal/tripmap/blobstore"
	"go.orx.me/apps/trip-map/internal/tripmap/config"
	"go.orx.me/apps/trip-map/internal/tripmap/ent"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/mediaobject"
	entshare "go.orx.me/apps/trip-map/internal/tripmap/ent/share"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/user"
	"go.orx.me/apps/trip-map/internal/tripmap/share"
)

// Default per-client-address limits per minute. A burst of misses is the
// signature of someone guessing tokens, so it has its own, lower budget.
const (
	sharePageLimit  = 120
	shareImageLimit = 30
	shareMissLimit  = 30
	sharePhotoLimit = 300
	shareRateWindow = time.Minute
	// ogCacheTTL bounds how long a rendered preview is reused. The key already
	// carries the share ID and version, and is only read after the share was
	// looked up and found public, so it never keeps a revoked page reachable.
	ogCacheTTL       = 10 * time.Minute
	sharePhotoMax    = 12 << 20
	shareRenderLimit = 2
)

// SharePublic serves the public share routes. It owns no routing: the app mounts
// its methods. Every method answers a failed lookup, whatever the reason (bad
// token, unknown, revoked, private, disabled account, missing photo), with the
// same 404 body and headers.
type SharePublic struct {
	cfg      *config.AppConfig
	src      StoreSource
	index    *share.IndexSource
	renderer func() (*share.Renderer, error)
	render   chan struct{}
}

func NewSharePublic(cfg *config.AppConfig, src StoreSource) *SharePublic {
	return &SharePublic{cfg: cfg, src: src, index: share.NewIndexSource(cfg.Share.AppIndexURL), renderer: share.DefaultRenderer, render: make(chan struct{}, shareRenderLimit)}
}

// Enabled reports whether the public routes are active.
func (p *SharePublic) Enabled() bool { return p.cfg.Share.Enabled() }

func limitOr(configured, fallback int) int64 {
	if configured > 0 {
		return int64(configured)
	}
	return int64(fallback)
}

func secure(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
}

// NotFound is the one response of every failed public lookup.
func (p *SharePublic) NotFound(w http.ResponseWriter) {
	h := w.Header()
	secure(h)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(share.NotFoundPage)
}

func plain(w http.ResponseWriter, code int, retryAfter bool) {
	h := w.Header()
	secure(h)
	h.Set("Content-Type", "text/plain; charset=utf-8")
	if retryAfter {
		h.Set("Retry-After", strconv.Itoa(int(shareRateWindow.Seconds())))
	}
	w.WriteHeader(code)
	_, _ = io.WriteString(w, http.StatusText(code)+"\n")
}

// gate applies the per-address budget and the guessing penalty before any lookup.
// It returns false after writing the response. A Redis failure closes the route.
func (p *SharePublic) gate(w http.ResponseWriter, r *http.Request, st *Store, bucket string, limit int64) bool {
	ip := auth.ClientAddr(p.cfg.Auth.ClientIPHeader, r.Header, r.RemoteAddr)
	limiter := auth.NewLimiter(st.Redis, st.prefix())
	misses, err := limiter.Count(r.Context(), "share-miss:"+ip)
	if err != nil {
		slog.Error("share rate limiter unavailable", "error", err)
		plain(w, http.StatusServiceUnavailable, false)
		return false
	}
	if misses >= limitOr(p.cfg.Share.MissRatePerMinute, shareMissLimit) {
		plain(w, http.StatusTooManyRequests, true)
		return false
	}
	ok, err := limiter.Allow(r.Context(), bucket+":"+ip, limit, shareRateWindow)
	if err != nil {
		slog.Error("share rate limiter unavailable", "error", err)
		plain(w, http.StatusServiceUnavailable, false)
		return false
	}
	if !ok {
		plain(w, http.StatusTooManyRequests, true)
		return false
	}
	return true
}

// miss records a failed lookup against the client address and answers 404.
func (p *SharePublic) miss(w http.ResponseWriter, r *http.Request, st *Store) {
	ip := auth.ClientAddr(p.cfg.Auth.ClientIPHeader, r.Header, r.RemoteAddr)
	if _, err := auth.NewLimiter(st.Redis, st.prefix()).Allow(r.Context(), "share-miss:"+ip, 1<<30, shareRateWindow); err != nil {
		slog.Error("share rate limiter unavailable", "error", err)
		plain(w, http.StatusServiceUnavailable, false)
		return
	}
	p.NotFound(w)
}

// lookup finds the live share of a token. Public access needs a well-formed
// token, a link or public visibility, a stored snapshot and an enabled
// account, all read from the database on every request.
func (p *SharePublic) lookup(ctx context.Context, st *Store, token string) (*ent.Share, *share.Payload, bool, error) {
	if !share.ValidToken(token) {
		return nil, nil, false, nil
	}
	row, err := st.DB.Share.Query().Where(entshare.Token(token), entshare.VisibilityIn(entshare.VisibilityLink, entshare.VisibilityPublic),
		entshare.HasUserWith(user.Disabled(false))).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	if len(row.Payload) == 0 {
		return nil, nil, false, nil
	}
	payload, err := share.Decode(row.Payload)
	if err != nil {
		return nil, nil, false, err
	}
	return row, payload, true, nil
}

// resolve runs the shared front half of every route.
func (p *SharePublic) resolve(w http.ResponseWriter, r *http.Request, token, bucket string, limit int64) (*Store, *ent.Share, *share.Payload, bool) {
	if !p.Enabled() || r.Header.Get(share.IndexFetchHeader) != "" {
		p.NotFound(w)
		return nil, nil, nil, false
	}
	st, err := storeFrom(p.src)
	if err != nil {
		plain(w, http.StatusServiceUnavailable, false)
		return nil, nil, nil, false
	}
	if !p.gate(w, r, st, bucket, limit) {
		return nil, nil, nil, false
	}
	row, payload, ok, err := p.lookup(r.Context(), st, token)
	if err != nil {
		slog.Error("share lookup failed", "error", err)
		plain(w, http.StatusServiceUnavailable, false)
		return nil, nil, nil, false
	}
	if !ok {
		p.miss(w, r, st)
		return nil, nil, nil, false
	}
	return st, row, payload, true
}

func robots(row *ent.Share) string {
	if row.Visibility == entshare.VisibilityPublic {
		return "index, follow"
	}
	return "noindex, nofollow"
}

func (p *SharePublic) csp() string {
	if p.cfg.Share.HTMLCSP != "" {
		return p.cfg.Share.HTMLCSP
	}
	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; base-uri 'none'; object-src 'none'; form-action 'none'; frame-ancestors 'none'"
}

// Page serves /s/<token>: the application's index with Open Graph tags.
func (p *SharePublic) Page(w http.ResponseWriter, r *http.Request, token string) {
	_, row, payload, ok := p.resolve(w, r, token, "share-page", limitOr(p.cfg.Share.PageRatePerMinute, sharePageLimit))
	if !ok {
		return
	}
	base := p.cfg.Share.BaseURL()
	meta := share.MetaFor(&payload.Content, base+"/s/"+token, base+"/s/"+token+"/og.png", row.Visibility == entshare.VisibilityPublic)
	var page []byte
	if index := p.index.Get(r.Context()); index != nil {
		page = share.Inject(index, meta)
	} else {
		page = share.Fallback(meta)
	}
	h := w.Header()
	secure(h)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Robots-Tag", robots(row))
	h.Set("Content-Security-Policy", p.csp())
	h.Set("X-Frame-Options", "DENY")
	_, _ = w.Write(page)
}

// Data serves /api/share/<token>/data: the public content document.
func (p *SharePublic) Data(w http.ResponseWriter, r *http.Request, token string) {
	_, row, payload, ok := p.resolve(w, r, token, "share-page", limitOr(p.cfg.Share.PageRatePerMinute, sharePageLimit))
	if !ok {
		return
	}
	body, err := payload.Content.PublicJSON()
	if err != nil {
		plain(w, http.StatusInternalServerError, false)
		return
	}
	h := w.Header()
	secure(h)
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Robots-Tag", robots(row))
	h.Set("Content-Security-Policy", "default-src 'none'")
	_, _ = w.Write(body)
}

// Image serves /s/<token>/og.png.
func (p *SharePublic) Image(w http.ResponseWriter, r *http.Request, token string) {
	st, row, payload, ok := p.resolve(w, r, token, "share-image", limitOr(p.cfg.Share.ImageRatePerMinute, shareImageLimit))
	if !ok {
		return
	}
	// The lookup above already proved the share is public right now; the cache
	// only saves rendering, and its key changes with every published version.
	key := st.prefix() + "share-og:" + row.ID + ":" + strconv.FormatInt(row.Version, 10)
	img, err := st.Redis.Get(r.Context(), key).Bytes()
	if err != nil {
		select {
		case p.render <- struct{}{}:
			defer func() { <-p.render }()
		case <-r.Context().Done():
			plain(w, http.StatusServiceUnavailable, false)
			return
		}
		renderer, rerr := p.renderer()
		if rerr == nil {
			img, rerr = renderer.Render(&payload.Content)
		}
		if rerr != nil {
			slog.Error("share preview render failed", "error", rerr)
			plain(w, http.StatusInternalServerError, false)
			return
		}
		if serr := st.Redis.Set(r.Context(), key, img, ogCacheTTL).Err(); serr != nil {
			slog.Warn("share preview cache write failed", "error", serr)
		}
	}
	h := w.Header()
	secure(h)
	h.Set("Content-Type", "image/png")
	h.Set("Content-Length", strconv.Itoa(len(img)))
	h.Set("X-Robots-Tag", robots(row))
	h.Set("Content-Security-Policy", "default-src 'none'")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	_, _ = w.Write(img)
}

// Photo serves /api/share/<token>/photos/<handle>?size=thumb|full. Each request
// re-checks the media row, so a deleted photo is gone at once.
func (p *SharePublic) Photo(w http.ResponseWriter, r *http.Request, token, handle string) {
	st, row, payload, ok := p.resolve(w, r, token, "share-photo", limitOr(p.cfg.Share.PhotoRatePerMinute, sharePhotoLimit))
	if !ok {
		return
	}
	size := r.URL.Query().Get("size")
	if size == "" {
		size = "thumb"
	}
	mediaID, listed := payload.Photos[handle]
	if !listed || (size != "thumb" && size != "full") || !payload.Content.Fields.Photos {
		p.miss(w, r, st)
		return
	}
	media, err := st.DB.MediaObject.Query().Where(mediaobject.ID(mediaID), mediaobject.UserID(row.UserID), mediaobject.StatusEQ(mediaobject.StatusReady),
		mediaobject.ContentType("image/jpeg"), mediaobject.WidthGT(0)).Only(r.Context())
	if ent.IsNotFound(err) {
		p.miss(w, r, st)
		return
	}
	if err != nil || st.Blobs == nil {
		plain(w, http.StatusServiceUnavailable, false)
		return
	}
	key := media.ObjectKey
	if size == "thumb" {
		key = media.ThumbnailKey
	}
	body, err := st.Blobs.Get(r.Context(), key)
	if err != nil {
		if errors.Is(err, blobstore.ErrNotFound) {
			p.miss(w, r, st)
			return
		}
		plain(w, http.StatusServiceUnavailable, false)
		return
	}
	defer body.Close()
	h := w.Header()
	secure(h)
	h.Set("Content-Type", "image/jpeg")
	h.Set("Content-Disposition", "inline")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Content-Security-Policy", "default-src 'none'")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	_, _ = io.Copy(w, io.LimitReader(body, sharePhotoMax))
}

// SplitSharePath splits the part of a public path after its prefix into a token
// and the remaining segments: "/<token>/photos/<handle>" -> token, [photos, handle].
func SplitSharePath(rest string) (token string, tail []string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", nil
	}
	return parts[0], parts[1:]
}
