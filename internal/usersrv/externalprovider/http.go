package externalprovider

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	requestTimeout   = 10 * time.Second
	maxResponseBytes = 64 * 1024
	defaultCacheTTL  = 5 * time.Minute
	maxCacheTTL      = 6 * time.Hour
	refreshCooldown  = 30 * time.Second
)

func newHTTPClient(rt http.RoundTripper) *http.Client {
	if rt == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableKeepAlives = true // Never replay a token POST on a reused connection.
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		rt = t
	}
	return &http.Client{Transport: rt, Timeout: requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// readJSON has no upstream error text or URL in its errors. Even a successful
// response is size-limited and must be a single JSON document of the right type.
func readJSON(c *http.Client, req *http.Request, dst any) (http.Header, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 ||
		(resp.StatusCode == http.StatusForbidden && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "")) {
		return nil, ErrUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ErrRejected
	}
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return nil, ErrRejected
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	defer clear(body)
	if len(body) > maxResponseBytes || json.Unmarshal(body, dst) != nil {
		return nil, ErrRejected
	}
	return resp.Header, nil
}

// cacheTTL respects provider max-age/Age, but never retains keys beyond 6h.
// no-cache/no-store and malformed cache headers cause a fetch on the next use.
func cacheTTL(h http.Header) time.Duration {
	ttl := defaultCacheTTL
	foundMaxAge := false
	for _, part := range strings.Split(strings.Join(h.Values("Cache-Control"), ","), ",") {
		p := strings.TrimSpace(strings.ToLower(part))
		if p == "no-store" || p == "no-cache" || strings.HasPrefix(p, "no-cache=") {
			return 0
		}
		if v, ok := strings.CutPrefix(p, "max-age="); ok {
			n, err := strconv.ParseInt(strings.Trim(v, `"`), 10, 32)
			if err != nil || n < 0 {
				return 0
			}
			value := min(time.Duration(n)*time.Second, maxCacheTTL)
			if foundMaxAge {
				ttl = min(ttl, value)
			} else {
				ttl = value
			}
			foundMaxAge = true
		}
	}
	if age := h.Get("Age"); age != "" {
		n, err := strconv.ParseInt(age, 10, 32)
		if err != nil || n < 0 {
			return 0
		}
		ttl -= time.Duration(n) * time.Second
	}
	return max(ttl, 0)
}

// documentCache coalesces concurrent GETs, including forced key rotation, with
// cancellable waiting. No goroutines/timers/background refresh survive calls.
// Expired data is never used when refresh fails. Forced refreshes are limited
// to one per 30s to bound attacker-controlled unknown-kid/signature requests.
type documentCache[T any] struct {
	mu                  sync.Mutex
	gate                chan struct{}
	value               T
	valid               bool
	expires, lastForced time.Time
}

func newDocumentCache[T any]() *documentCache[T] {
	return &documentCache[T]{gate: make(chan struct{}, 1)}
}

func (c *documentCache[T]) load(ctx context.Context, clock func() time.Time, force bool, fetch func() (T, time.Duration, error)) (T, error) {
	if ctx.Err() != nil {
		var zero T
		return zero, ErrUnavailable
	}
	c.mu.Lock()
	if c.valid && clock().Before(c.expires) && !force {
		v := c.value
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		var zero T
		return zero, ErrUnavailable
	}
	c.mu.Lock()
	now := clock()
	if force && !c.lastForced.IsZero() && now.Sub(c.lastForced) < refreshCooldown {
		if c.valid && now.Before(c.expires) {
			v := c.value
			c.mu.Unlock()
			return v, nil
		}
		c.mu.Unlock()
		var zero T
		return zero, ErrRejected
	}
	if c.valid && now.Before(c.expires) && !force {
		v := c.value
		c.mu.Unlock()
		return v, nil
	}
	if force {
		c.lastForced = now
	}
	c.mu.Unlock()
	v, ttl, err := fetch()
	if err != nil {
		var zero T
		return zero, err
	}
	c.mu.Lock()
	c.value, c.expires, c.valid = v, clock().Add(ttl), true
	c.mu.Unlock()
	return v, nil
}
