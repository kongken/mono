// Package app wires the BFF: the public entry in front of every business
// service.
package app

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"go.orx.me/apps/trip-map/internal/bff/config"
	"go.orx.me/apps/trip-map/internal/bff/gateway"
	"go.orx.me/apps/trip-map/internal/platform/health"
)

type Handlers struct {
	cfg      *config.Config
	services []gateway.Service
	gateway  atomic.Pointer[gateway.Gateway]
	health   atomic.Pointer[health.Checker]
}

func New(cfg *config.Config) *Handlers {
	return &Handlers{cfg: cfg, services: gateway.Services}
}

// SetServices replaces the route table; tests use it to route to fake services.
func (h *Handlers) SetServices(services []gateway.Service) { h.services = services }

// Bootstrap runs after Butterfly loads the YAML. Services are not required to be
// up: the BFF starts first and reports them in /readyz.
func (h *Handlers) Bootstrap() error {
	if err := h.cfg.Validate(gateway.Names(h.services)); err != nil {
		return err
	}
	gw, err := gateway.New(h.cfg, h.services)
	if err != nil {
		return err
	}
	h.gateway.Store(gw)
	h.health.Store(health.New(gw.Probes()...))
	return nil
}

func (h *Handlers) Check(ctx context.Context) []health.Status {
	if c := h.health.Load(); c != nil {
		return c.Check(ctx)
	}
	return nil
}

func (h *Handlers) Routes(r *gin.Engine) {
	// A reverse proxy can be explicitly configured later; don't trust arbitrary X-Forwarded-For.
	_ = r.SetTrustedProxies(nil)
	r.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"message": "pong"}) })
	r.GET("/readyz", func(c *gin.Context) {
		statuses := h.Check(c.Request.Context())
		code := http.StatusOK
		if !health.Ready(statuses) {
			code = http.StatusServiceUnavailable
		}
		c.JSON(code, gin.H{"ready": health.Ready(statuses), "dependencies": statuses})
	})
	r.NoRoute(func(c *gin.Context) {
		gw := h.gateway.Load()
		if gw == nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		gw.Handle(c)
	})
}
