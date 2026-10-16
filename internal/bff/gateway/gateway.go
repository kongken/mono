// Package gateway forwards public requests to the business services: Connect
// procedures by proto package, other paths by the route table. The BFF owns
// CORS and the caller's address; request bodies stream through unbuffered and
// each service keeps its own limits.
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

// forwardedHeaders are what a front proxy reported; they pass through unchanged,
// as they did when it connected to a service directly.
var forwardedHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"}

type upstream struct {
	name  string
	base  *url.URL
	proxy *httputil.ReverseProxy
}

type route struct {
	Route
	up *upstream
}

type Gateway struct {
	upstreams []*upstream
	packages  map[string]*upstream
	// routes are ordered so the longest path wins.
	routes []route
	cors   gin.HandlerFunc
	client *http.Client
}

// New builds the gateway for services from a validated configuration.
func New(cfg *config.Config, services []Service) (*Gateway, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	g := &Gateway{packages: map[string]*upstream{}, client: &http.Client{Transport: transport}}
	var headers []string
	for _, s := range services {
		base, err := url.Parse(cfg.Upstreams[s.Name])
		if err != nil || base.Host == "" {
			return nil, fmt.Errorf("upstreams.%s: invalid base URL", s.Name)
		}
		base.Path = ""
		up := &upstream{name: s.Name, base: base, proxy: newProxy(s.Name, base, cfg.ClientIPHeader, transport)}
		g.upstreams = append(g.upstreams, up)
		for _, pkg := range s.Packages {
			if g.packages[pkg] != nil {
				return nil, fmt.Errorf("proto package %s is routed twice", pkg)
			}
			g.packages[pkg] = up
		}
		for _, r := range s.Routes {
			if !strings.HasPrefix(r.Path, "/") || slices.ContainsFunc(g.routes, func(o route) bool { return o.Path == r.Path }) {
				return nil, fmt.Errorf("route %q is invalid or routed twice", r.Path)
			}
			g.routes = append(g.routes, route{Route: r, up: up})
		}
		headers = append(headers, s.Headers...)
	}
	sort.SliceStable(g.routes, func(i, j int) bool { return len(g.routes[i].Path) > len(g.routes[j].Path) })
	origins := slices.Clone(cfg.CORSAllowedOrigins)
	g.cors = httpx.CORS(func() []string { return origins }, headers...)
	return g, nil
}

func newProxy(name string, base *url.URL, clientIPHeader string, transport http.RoundTripper) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(base)
			pr.Out.Host = pr.In.Host
			for _, h := range forwardedHeaders {
				if v := pr.In.Header.Values(h); len(v) > 0 {
					pr.Out.Header[http.CanonicalHeaderKey(h)] = slices.Clone(v)
				}
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
			// url.Error repeats the request URL, which can hold a share token.
			var urlErr *url.Error
			if errors.As(err, &urlErr) {
				err = urlErr.Err
			}
			if !errors.Is(err, context.Canceled) {
				slog.Warn("upstream request failed", "service", name, "error", err)
			}
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

// match finds the service owning a request path. Paths that are not clean, such
// as /api/share/../x, never match, so routing and the service agree on the path.
func (g *Gateway) match(p string) (*route, *upstream) {
	if clean := path.Clean(p); p != clean && p != clean+"/" {
		return nil, nil
	}
	for i := range g.routes {
		r := &g.routes[i]
		if p == r.Path || (strings.HasSuffix(r.Path, "/") && strings.HasPrefix(p, r.Path)) {
			return r, r.up
		}
	}
	// Connect procedures: /api/<package>.<Service>/<Method>.
	rest, ok := strings.CutPrefix(p, "/api/")
	if !ok {
		return nil, nil
	}
	service, method, ok := strings.Cut(rest, "/")
	dot := strings.LastIndex(service, ".")
	if !ok || method == "" || strings.Contains(method, "/") || dot <= 0 {
		return nil, nil
	}
	return nil, g.packages[service[:dot]]
}

// Handle forwards the request to the service that owns its path, or answers 404.
func (g *Gateway) Handle(c *gin.Context) {
	r, up := g.match(c.Request.URL.Path)
	if r != nil && r.Trace != "" {
		httpx.RedactSpan(c.Request, r.Trace)
	}
	if up == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/api/") {
		if g.cors(c); c.IsAborted() {
			return
		}
	}
	up.proxy.ServeHTTP(c.Writer, c.Request)
}

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
