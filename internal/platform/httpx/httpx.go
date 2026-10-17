// Package httpx holds the HTTP edge behaviour shared by every service: the
// caller's address, CORS for browser clients, and keeping credential-bearing
// paths out of tracing.
package httpx

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ClientAddr returns the caller's address for rate limiting: the first entry of
// headerName when one is configured and present (a trusted reverse proxy sets
// it), otherwise the TCP peer address.
func ClientAddr(headerName string, h http.Header, remoteAddr string) string {
	if headerName != "" {
		if v := strings.TrimSpace(strings.Split(h.Get(headerName), ",")[0]); v != "" {
			return v
		}
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// connectHeaders are the request headers every Connect-Web client may send.
var connectHeaders = []string{"Authorization", "Content-Type", "Connect-Protocol-Version", "Connect-Timeout-Ms", "X-User-Agent", "X-CSRF-Token", "X-Tripmap-Schema", "X-Tripmap-Regions"}

// CORS answers cross-origin requests from the configured origins only. Requests
// without an Origin header, and same-origin deployments, are unaffected.
// origins is read on every request; headers are the extra request headers the
// service's own clients send.
func CORS(origins func() []string, headers ...string) gin.HandlerFunc {
	allowHeaders := strings.Join(append(slices.Clone(connectHeaders), headers...), ", ")
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			return
		}
		c.Writer.Header().Add("Vary", "Origin")
		if !slices.Contains(origins(), origin) {
			return
		}
		header := c.Writer.Header()
		header.Set("Access-Control-Allow-Origin", origin)
		header.Set("Access-Control-Expose-Headers", "Connect-Protocol-Version, Content-Type, Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin, Connect-Content-Encoding, Connect-Accept-Encoding")
		if c.Request.Method == http.MethodOptions {
			header.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			header.Set("Access-Control-Allow-Headers", allowHeaders)
			header.Set("Access-Control-Max-Age", "7200")
			c.AbortWithStatus(http.StatusNoContent)
		}
	}
}

// CheckOrigins rejects any entry that is not an exact browser origin such as
// https://example.com; field names the setting in the error.
func CheckOrigins(field string, origins []string) error {
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || strings.HasSuffix(origin, "/") {
			return fmt.Errorf("%s: %q must be an origin like https://example.com", field, origin)
		}
	}
	return nil
}

// RedactTrace keeps credentials in a path or query (share tokens, signed
// unsubscribe links) out of tracing: the otel Gin middleware records the raw
// request path as a span attribute before any handler runs, so these routes
// replace it with the route pattern. Butterfly discards Gin's request log.
func RedactTrace(pattern string) gin.HandlerFunc {
	return func(c *gin.Context) {
		RedactSpan(c.Request, pattern)
		c.Next()
	}
}

// RedactSpan is RedactTrace for handlers that decide per request.
func RedactSpan(r *http.Request, pattern string) {
	trace.SpanFromContext(r.Context()).SetAttributes(
		attribute.String("url.path", pattern), attribute.String("http.target", pattern),
		attribute.String("url.full", pattern), attribute.String("http.url", pattern),
		attribute.String("url.query", ""), attribute.String("http.route", pattern),
	)
}
