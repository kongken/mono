// Package app wires the BFF: the public entry in front of every business
// service.
package app

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"butterfly.orx.me/core/store/redis"
	"butterfly.orx.me/core/store/sqldb"
	"connectrpc.com/connect"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/stdlib"
	bffauth "github.com/kongken/mono/internal/bff/auth"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/gateway"
	"github.com/kongken/mono/internal/bff/ingress"
	bffmetro "github.com/kongken/mono/internal/bff/metro"
	"github.com/kongken/mono/internal/bff/oauth"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/bff/session"
	bfftripmap "github.com/kongken/mono/internal/bff/tripmap"
	"github.com/kongken/mono/internal/bff/upstream"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	redisclient "github.com/redis/go-redis/v9"
)

const publicPrefix = "/api/bff."

type Resources struct {
	DB        *ent.Client
	Redis     *redisclient.Client
	KeyPrefix string
}

type Handlers struct {
	cfg       *config.Config
	services  []gateway.Service
	gateway   atomic.Pointer[gateway.Gateway]
	health    atomic.Pointer[health.Checker]
	public    atomic.Pointer[http.Handler]
	upstreams atomic.Pointer[upstream.Clients]
	resources atomic.Pointer[Resources]
	auth      atomic.Pointer[bffauth.Handler]
	oauthReg  atomic.Pointer[provider.Registry]
}

func New(cfg *config.Config) *Handlers {
	return &Handlers{cfg: cfg, services: gateway.Services}
}

func (h *Handlers) SetServices(services []gateway.Service) { h.services = services }
func (h *Handlers) SetResources(r *Resources)              { h.resources.Store(r) }

// SetOAuthRegistry overrides configured OAuth providers (tests).
func (h *Handlers) SetOAuthRegistry(reg *provider.Registry) { h.oauthReg.Store(reg) }

func (h *Handlers) Bootstrap() error {
	services := gateway.Active(h.services, h.cfg.Upstreams)
	if err := h.cfg.Validate(gateway.Names(services)); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if h.cfg.DBStore != "" {
		if err := h.bootstrapStores(ctx); err != nil {
			return err
		}
	}
	gw, err := gateway.New(h.cfg, services)
	if err != nil {
		return err
	}
	grpcServices := gateway.GRPCNames(services)
	if err := h.cfg.ValidateInternal(grpcServices); err != nil {
		return err
	}
	var clients *upstream.Clients
	if len(grpcServices) > 0 {
		clients, err = upstream.Dial(h.cfg, grpcServices)
		if err != nil {
			return err
		}
		h.upstreams.Store(clients)
	}
	if clients != nil {
		public := h.publicAPI(clients)
		h.public.Store(&public)
	}
	probes := gw.Probes()
	if r := h.resources.Load(); r != nil && r.DB != nil {
		probes = append(probes, health.Probe{Name: "bff_postgres", Check: func(ctx context.Context) error {
			_, err := r.DB.User.Query().Exist(ctx)
			return err
		}})
		if r.Redis != nil {
			probes = append(probes, health.Probe{Name: "bff_redis", Check: func(ctx context.Context) error {
				return r.Redis.Ping(ctx).Err()
			}})
		}
	}
	if clients != nil {
		probes = append(probes, upstream.GRPCProbes(clients, grpcServices)...)
	}
	h.gateway.Store(gw)
	h.health.Store(health.New(probes...))
	return nil
}

func (h *Handlers) bootstrapStores(ctx context.Context) error {
	if r := h.resources.Load(); r != nil {
		return nil
	}
	if h.cfg.DBStore == "" || h.cfg.RedisStore == "" {
		return fmt.Errorf("db_store and redis_store are required")
	}
	db := sqldb.GetDB(h.cfg.DBStore)
	if db == nil {
		return fmt.Errorf("db_store does not refer to a configured store.db client")
	}
	if _, ok := db.Driver().(*stdlib.Driver); !ok {
		return fmt.Errorf("db_store must use PostgreSQL")
	}
	cache := redis.GetClient(h.cfg.RedisStore)
	if cache == nil {
		return fmt.Errorf("redis_store does not refer to a configured store.redis client")
	}
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres startup check: %w", err)
	}
	if err := cache.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis startup check: %w", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	if h.cfg.AutoMigrate {
		if err := client.Schema.Create(ctx); err != nil {
			_ = client.Close()
			return fmt.Errorf("ent migration: %w", err)
		}
	}
	h.resources.Store(&Resources{DB: client, Redis: cache, KeyPrefix: h.cfg.KeyPrefix})
	return nil
}

// heavyProcedures are the procedures that get the long request deadline: account
// deletion plus each product's own.
func heavyProcedures() map[string]bool {
	heavy := map[string]bool{bffv1connect.AuthServiceDeleteAccountProcedure: true}
	for procedure := range bfftripmap.HeavyProcedures() {
		heavy[procedure] = true
	}
	for procedure := range bffmetro.HeavyProcedures() {
		heavy[procedure] = true
	}
	return heavy
}

func (h *Handlers) publicAPI(clients *upstream.Clients) http.Handler {
	conn := clients.Conn(bfftripmap.Service)
	policy := ingress.Policy{
		ClientIPHeader:     h.cfg.ClientIPHeader,
		AllowedOrigins:     h.cfg.CORSAllowedOrigins,
		Heavy:              heavyProcedures(),
		AdminSessionMaxAge: h.cfg.Auth.AdminSessionLimit(),
	}
	if r := h.resources.Load(); r != nil && conn != nil {
		cookies := session.CookieConfig{Path: "/", SameSite: http.SameSiteLaxMode, Secure: h.cfg.Auth.CookieSecure}
		oauthState := oauth.NewStateStore(r.Redis, r.KeyPrefix)
		var reg *provider.Registry
		if custom := h.oauthReg.Load(); custom != nil {
			reg = custom
		}
		oauthDeps := bffauth.OAuthDeps{State: oauthState, Registry: reg}
		svc := bffauth.NewService(&h.cfg.Auth, cookies, r.DB, ratelimit.New(r.Redis, r.KeyPrefix),
			tripmapv1.NewProfileServiceClient(conn), h.cleaners(clients), oauthDeps)
		authHandler := bffauth.NewHandler(svc, h.cfg.CORSAllowedOrigins, h.cfg.Auth.CSRFTokenTTL)
		h.auth.Store(authHandler)
		policy.Authenticate = authHandler.Authenticate
		policy.DeletionReceiptValid = svc.CompletionReceiptValid
	}
	opts := []connect.HandlerOption{
		connect.WithInterceptors(ingress.Interceptor(policy)),
		connect.WithRequireConnectProtocolHeader(),
		connect.WithReadMaxBytes(grpcx.LegitimatePayloadBytes),
	}
	mux := http.NewServeMux()
	if conn != nil {
		bfftripmap.MountStatus(mux, conn, opts)
		if auth := h.auth.Load(); auth != nil {
			route, handler := bffv1connect.NewAuthServiceHandler(auth, opts...)
			mux.Handle(route, ingress.ConnectOnly(handler))
			bfftripmap.MountRPC(mux, conn, opts)
			if metroConn := clients.Conn(bffmetro.Service); metroConn != nil {
				bffmetro.MountRPC(mux, metroConn, opts)
			}
		}
	}
	return http.StripPrefix("/api", mux)
}

// cleaners builds the account-deletion cleaners of the products enabled by
// auth.account_cleanup.
func (h *Handlers) cleaners(clients *upstream.Clients) []bffauth.UserDataCleaner {
	var out []bffauth.UserDataCleaner
	for _, name := range h.cfg.Auth.CleanupProducts() {
		conn := clients.Conn(name)
		if conn == nil {
			continue
		}
		switch name {
		case bfftripmap.Service:
			out = append(out, bfftripmap.NewCleaner(conn))
		}
	}
	return out
}

func (h *Handlers) mountPublicHTTP(r *gin.Engine) {
	clients := h.upstreams.Load()
	if clients == nil {
		return
	}
	conn := clients.Conn(bfftripmap.Service)
	if conn == nil {
		return
	}
	var limiter *ratelimit.Limiter
	if res := h.resources.Load(); res != nil {
		limiter = ratelimit.New(res.Redis, res.KeyPrefix)
	}
	bfftripmap.MountPublicHTTP(r, conn, limiter, h.cfg.ClientIPHeader)
}

func (h *Handlers) Shutdown() error {
	var errs []error
	if c := h.upstreams.Swap(nil); c != nil {
		errs = append(errs, c.Close())
	}
	if r := h.resources.Swap(nil); r != nil && r.DB != nil {
		errs = append(errs, r.DB.Close())
	}
	if len(errs) == 0 {
		return nil
	}
	return errs[0]
}

func (h *Handlers) Check(ctx context.Context) []health.Status {
	if c := h.health.Load(); c != nil {
		return c.Check(ctx)
	}
	return nil
}

func (h *Handlers) Routes(r *gin.Engine) {
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
	for _, route := range gateway.BFFOwnedRoutes {
		r.GET(route.Path, httpx.RedactTrace(route.Trace), func(c *gin.Context) {
			if auth := h.auth.Load(); auth != nil {
				auth.ServeOAuthHTTP(c.Writer, c.Request, h.cfg.ClientIPHeader)
			} else {
				c.AbortWithStatus(http.StatusServiceUnavailable)
			}
		})
	}
	h.mountPublicHTTP(r)
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
		// Registered plain HTTP routes (gateway.Service.Routes), such as the tile proxy.
		if gw.Forward(c) {
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
	})
}

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
	c.Status(http.StatusOK)
	(*public).ServeHTTP(c.Writer, c.Request)
}

// AuthHandler exposes the auth handler for tests.
func (h *Handlers) AuthHandler() *bffauth.Handler {
	if a := h.auth.Load(); a != nil {
		return a
	}
	return nil
}

// CORSHeaders returns extra allowed headers for bff browser clients.
func CORSHeaders() []string {
	return gateway.BrowserHeaders()
}
