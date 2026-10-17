// Package app wires the BFF: the public entry in front of every business
// service.
package app

import (
	"context"
	"net/http"
	"path"
	"strings"
	"sync/atomic"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/gateway"
	"github.com/kongken/mono/internal/bff/ingress"
	"github.com/kongken/mono/internal/bff/status"
	"github.com/kongken/mono/internal/bff/upstream"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

// publicPrefix is where the BFF's own Connect procedures (package bff.v1) live;
// everything under it is answered by the BFF and never forwarded.
const publicPrefix = "/api/bff.v1."

type Handlers struct {
	cfg      *config.Config
	services []gateway.Service
	gateway  atomic.Pointer[gateway.Gateway]
	health   atomic.Pointer[health.Checker]
	// public serves the bff.v1 procedures; upstreams are the gRPC connections it uses.
	public    atomic.Pointer[http.Handler]
	upstreams atomic.Pointer[upstream.Clients]
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
	grpcServices := gateway.GRPCNames(h.services)
	if err := h.cfg.ValidateInternal(grpcServices); err != nil {
		return err
	}
	if len(grpcServices) > 0 {
		clients, err := upstream.Dial(h.cfg, grpcServices)
		if err != nil {
			return err
		}
		h.upstreams.Store(clients)
		public := h.publicAPI(clients)
		h.public.Store(&public)
	}
	h.gateway.Store(gw)
	h.health.Store(health.New(gw.Probes()...))
	return nil
}

// publicAPI serves the BFF's own Connect procedures. Handlers are built with the
// Connect-only gate and the ingress interceptor, and share one authentication
// policy; sessions are not implemented yet, so protected procedures refuse
// every caller.
func (h *Handlers) publicAPI(clients *upstream.Clients) http.Handler {
	opts := []connect.HandlerOption{
		connect.WithInterceptors(ingress.Interceptor(ingress.Policy{ClientIPHeader: h.cfg.ClientIPHeader})),
		connect.WithRequireConnectProtocolHeader(),
		connect.WithReadMaxBytes(4096),
	}
	mux := http.NewServeMux()
	if conn := clients.Conn("tripmap"); conn != nil {
		route, handler := bffv1connect.NewStatusServiceHandler(status.New(tripmapv1.NewSystemServiceClient(conn)), opts...)
		mux.Handle(route, ingress.ConnectOnly(handler))
	}
	return http.StripPrefix("/api", mux)
}

// Shutdown closes the connections to the business services.
func (h *Handlers) Shutdown() error {
	if c := h.upstreams.Swap(nil); c != nil {
		return c.Close()
	}
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
		if strings.HasPrefix(c.Request.URL.Path, publicPrefix) {
			h.servePublic(gw, c)
			return
		}
		gw.Handle(c)
	})
}

// servePublic answers a bff.v1 procedure. Paths that are not canonical, and
// procedures that do not exist, are 404 and are not forwarded anywhere.
func (h *Handlers) servePublic(gw *gateway.Gateway, c *gin.Context) {
	p := c.Request.URL.Path
	public := h.public.Load()
	if public == nil || path.Clean(p) != p {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if gw.CORS()(c); c.IsAborted() {
		return
	}
	// NoRoute starts the response as 404; Connect writes 200 implicitly.
	c.Status(http.StatusOK)
	(*public).ServeHTTP(c.Writer, c.Request)
}
