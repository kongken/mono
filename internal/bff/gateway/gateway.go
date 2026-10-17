// Package gateway holds upstream health probes and shared CORS for the BFF public API.
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/platform/httpx"
)

type upstream struct {
	name string
	base *url.URL
}

// Gateway tracks configured business services for readiness and CORS.
type Gateway struct {
	upstreams []*upstream
	cors      gin.HandlerFunc
	client    *http.Client
}

// New builds the gateway for services from a validated configuration.
func New(cfg *config.Config, services []Service) (*Gateway, error) {
	g := &Gateway{client: &http.Client{Timeout: 5 * time.Second}}
	var headers []string
	for _, s := range services {
		base, err := url.Parse(cfg.Upstreams[s.Name])
		if err != nil || base.Host == "" {
			return nil, fmt.Errorf("upstreams.%s: invalid base URL", s.Name)
		}
		base.Path = ""
		g.upstreams = append(g.upstreams, &upstream{name: s.Name, base: base})
		headers = append(headers, s.Headers...)
	}
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
