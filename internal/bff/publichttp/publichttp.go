// Package publichttp registers credential-bearing HTTP routes on the BFF outside
// Connect and the transparent gateway (share pages, email unsubscribe, and similar).
// Each route can declare edge rate limits and a trace path template so signed tokens
// never appear in spans or access logs.
package publichttp

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/platform/httpx"
)

// Limit configures a fixed-window rate limit keyed by client IP.
type Limit struct {
	KeyPrefix string
	Max       int64
	Window    time.Duration
}

// Route is one explicitly registered public HTTP handler.
type Route struct {
	Methods      []string
	Path         string
	TracePattern string
	Limit        *Limit
	Handler      http.Handler
}

// Registry mounts public routes on a Gin engine.
type Registry struct {
	limiter        *ratelimit.Limiter
	clientIPHeader string
}

// New builds a registry. limiter may be nil to skip rate limiting.
func New(limiter *ratelimit.Limiter, clientIPHeader string) *Registry {
	return &Registry{limiter: limiter, clientIPHeader: clientIPHeader}
}

// Mount registers routes before the catch-all gateway. TracePattern must not contain
// credentials; use placeholders such as /api/notifications/unsubscribe.
func (r *Registry) Mount(engine *gin.Engine, routes ...Route) {
	for _, route := range routes {
		rt := route
		h := gin.WrapH(rt.Handler)
		if rt.TracePattern != "" {
			h = func(c *gin.Context) {
				httpx.RedactSpan(c.Request, rt.TracePattern)
				rt.Handler.ServeHTTP(c.Writer, c.Request)
			}
		}
		if rt.Limit != nil && r.limiter != nil {
			prev := h
			lim := *rt.Limit
			h = func(c *gin.Context) {
				ip := httpx.ClientAddr(r.clientIPHeader, c.Request.Header, c.Request.RemoteAddr)
				ok, err := r.limiter.Allow(c.Request.Context(), lim.KeyPrefix+ip, lim.Max, lim.Window)
				if err != nil {
					slog.Error("public route rate limiter unavailable", "path", rt.Path, "error", err)
					c.Status(http.StatusServiceUnavailable)
					return
				}
				if !ok {
					c.Header("Retry-After", "60")
					c.Status(http.StatusTooManyRequests)
					return
				}
				prev(c)
			}
		}
		engine.Handle(rt.Methods[0], rt.Path, h)
		for _, m := range rt.Methods[1:] {
			engine.Handle(m, rt.Path, h)
		}
	}
}

// MountParam registers a Gin path with a wildcard parameter (for example /s/*rest).
// paramName is the Gin param without the colon, usually "rest".
func (r *Registry) MountParam(engine *gin.Engine, route Route, ginPath, paramName string, fn func(http.ResponseWriter, *http.Request, string)) {
	rt := route
	serve := func(c *gin.Context) {
		if rt.TracePattern != "" {
			httpx.RedactSpan(c.Request, rt.TracePattern)
		}
		fn(c.Writer, c.Request, c.Param(paramName))
	}
	h := gin.HandlerFunc(serve)
	if rt.Limit != nil && r.limiter != nil {
		prev := h
		lim := *rt.Limit
		h = func(c *gin.Context) {
			ip := httpx.ClientAddr(r.clientIPHeader, c.Request.Header, c.Request.RemoteAddr)
			ok, err := r.limiter.Allow(c.Request.Context(), lim.KeyPrefix+ip, lim.Max, lim.Window)
			if err != nil {
				slog.Error("public route rate limiter unavailable", "path", ginPath, "error", err)
				c.Status(http.StatusServiceUnavailable)
				return
			}
			if !ok {
				c.Header("Retry-After", "60")
				c.Status(http.StatusTooManyRequests)
				return
			}
			prev(c)
		}
	}
	engine.Handle(rt.Methods[0], ginPath, h)
	for _, m := range rt.Methods[1:] {
		engine.Handle(m, ginPath, h)
	}
}
