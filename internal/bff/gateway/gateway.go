// Package gateway holds upstream health probes, shared CORS and the registered
// plain HTTP routes the BFF forwards to a business service.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/platform/httpx"
)

// ClientIPHeader carries the caller's address to the services, which set it as
// their auth.client_ip_header. The BFF always overwrites it, so a client cannot
// choose its own address.
const ClientIPHeader = "X-Real-IP"

// anonymousRequestHeaders are the only client headers an Anonymous route forwards.
// The cache validators let browsers revalidate without credentials; the service
// checks them against what it issued.
var anonymousRequestHeaders = []string{"Accept", "User-Agent", "If-None-Match", "If-Modified-Since"}

type upstream struct {
	name string
	base *url.URL
}

type route struct {
	Route
	up    *upstream
	proxy *httputil.ReverseProxy
}

// Gateway tracks configured business services for readiness, CORS and the
// registered HTTP routes.
type Gateway struct {
	upstreams []*upstream
	// routes are ordered so the longest path wins.
	routes []route
	cors   gin.HandlerFunc
	client *http.Client
}

// New builds the gateway for services from a validated configuration.
func New(cfg *config.Config, services []Service) (*Gateway, error) {
	g := &Gateway{client: &http.Client{Timeout: 5 * time.Second}}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	var headers []string
	for _, s := range services {
		base, err := url.Parse(cfg.Upstreams[s.Name])
		if err != nil || base.Host == "" {
			return nil, fmt.Errorf("upstreams.%s: invalid base URL", s.Name)
		}
		base.Path = ""
		up := &upstream{name: s.Name, base: base}
		g.upstreams = append(g.upstreams, up)
		for _, r := range s.Routes {
			if !strings.HasPrefix(r.Path, "/") || slices.ContainsFunc(g.routes, func(o route) bool { return o.Path == r.Path }) {
				return nil, fmt.Errorf("route %q is invalid or routed twice", r.Path)
			}
			g.routes = append(g.routes, route{Route: r, up: up, proxy: newProxy(s.Name, base, cfg.ClientIPHeader, transport, r.Anonymous)})
		}
		headers = append(headers, s.Headers...)
	}
	sort.SliceStable(g.routes, func(i, j int) bool { return len(g.routes[i].Path) > len(g.routes[j].Path) })
	origins := slices.Clone(cfg.CORSAllowedOrigins)
	g.cors = httpx.CORS(func() []string { return origins }, headers...)
	return g, nil
}

// CORS is the cross-origin handler for /api/bff.v1.* Connect paths.
func (g *Gateway) CORS() gin.HandlerFunc { return g.cors }

// Probes checks each service's /readyz, which every service provides.
func (g *Gateway) Probes() []health.Probe {
	probes := make([]health.Probe, len(g.upstreams))
	for i, up := range g.upstreams {
		target := up.base.JoinPath("/readyz").String()
		probes[i] = health.Probe{Name: up.name, Check: func(ctx context.Context) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if err != nil {
				return err
			}
			res, err := g.client.Do(req)
			if err != nil {
				return err
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusOK {
				return fmt.Errorf("%s readyz: %s", up.name, res.Status)
			}
			return nil
		}}
	}
	return probes
}

func newProxy(name string, base *url.URL, clientIPHeader string, transport http.RoundTripper, anonymous bool) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(base)
			if anonymous {
				kept := http.Header{}
				for _, h := range anonymousRequestHeaders {
					if v := pr.In.Header.Values(h); len(v) > 0 {
						kept[http.CanonicalHeaderKey(h)] = slices.Clone(v)
					}
				}
				pr.Out.Header = kept
			}
			pr.Out.Header.Set(ClientIPHeader, httpx.ClientAddr(clientIPHeader, pr.In.Header, pr.In.RemoteAddr))
		},
		ModifyResponse: func(res *http.Response) error {
			// A service still configured for CORS must not add a second set.
			for k := range res.Header {
				if strings.HasPrefix(k, "Access-Control-") {
					res.Header.Del(k)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// url.Error repeats the request URL, which names the internal service address.
			var urlErr *url.Error
			if errors.As(err, &urlErr) {
				err = urlErr.Err
			}
			if !errors.Is(err, context.Canceled) {
				slog.Warn("upstream request failed", "service", name, "error", err)
			}
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

// matches reports whether a request path falls under a route path: exactly, or as a
// prefix when the route path ends in "/".
func matches(routePath, p string) bool {
	return p == routePath || (strings.HasSuffix(routePath, "/") && strings.HasPrefix(p, routePath))
}

// match finds the registered route owning a request path. Paths that are not
// clean, such as /api/tiles/../x, never match, so routing and the service agree on
// the path.
func (g *Gateway) match(p string) *route {
	if clean := path.Clean(p); p != clean && p != clean+"/" {
		return nil
	}
	for i := range g.routes {
		if matches(g.routes[i].Path, p) {
			return &g.routes[i]
		}
	}
	return nil
}

// Forward sends the request to the service owning its path and reports true, or
// reports false when no registered route matches. Paths under /api/ get the BFF's
// CORS handling, and the service's own Access-Control-* headers are dropped.
func (g *Gateway) Forward(c *gin.Context) bool {
	r := g.match(c.Request.URL.Path)
	if r == nil {
		return false
	}
	if r.Trace != "" {
		httpx.RedactSpan(c.Request, r.Trace)
	}
	if strings.HasPrefix(c.Request.URL.Path, "/api/") {
		if g.cors(c); c.IsAborted() {
			return true
		}
	}
	r.proxy.ServeHTTP(c.Writer, c.Request)
	return true
}
