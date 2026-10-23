// Package app wires the metro service: its PostgreSQL store, the gRPC listener
// the BFF calls, and the health endpoints.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	redisstore "butterfly.orx.me/core/store/redis"
	"butterfly.orx.me/core/store/sqldb"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/metro/application"
	"github.com/kongken/mono/internal/metro/config"
	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/health"
	"google.golang.org/grpc"
)

// Resources are the connected backing services.
type Resources struct {
	DB     *ent.Client
	Health *health.Checker
}

// Handlers is the metro service.
type Handlers struct {
	cfg            *config.AppConfig
	resources      atomic.Pointer[Resources]
	grpc           atomic.Pointer[grpcx.Server]
	grpcUnaryExtra []grpc.UnaryServerInterceptor
}

// New returns the service for cfg; Bootstrap connects it.
func New(cfg *config.AppConfig) *Handlers { return &Handlers{cfg: cfg} }

// SetGRPCTestUnaryInterceptors adds interceptors after authentication; tests use it.
func (h *Handlers) SetGRPCTestUnaryInterceptors(in ...grpc.UnaryServerInterceptor) {
	h.grpcUnaryExtra = append(h.grpcUnaryExtra, in...)
}

// SetResources installs already connected services instead of bootstrapping them.
// Tests use it to run the real gRPC server over an isolated database.
func (h *Handlers) SetResources(r *Resources) { h.resources.Store(r) }

// Check reports dependency readiness for /readyz and the BFF's status probe.
func (h *Handlers) Check(ctx context.Context) []health.Status {
	if r := h.resources.Load(); r != nil && r.Health != nil {
		return r.Health.Check(ctx)
	}
	return []health.Status{{Name: "postgres"}}
}

// Bootstrap runs after Butterfly loads YAML and registers its named store clients.
func (h *Handlers) Bootstrap(ctx context.Context) error {
	if err := h.cfg.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	db := sqldb.GetDB(h.cfg.DBStore)
	if db == nil {
		return fmt.Errorf("db_store does not refer to a configured store.db client")
	}
	if _, ok := db.Driver().(*stdlib.Driver); !ok {
		return fmt.Errorf("db_store must use PostgreSQL")
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres startup check: %w", err)
	}
	probes := []health.Probe{{Name: "postgres", Check: db.PingContext}}
	if h.cfg.RedisStore != "" {
		cache := redisstore.GetClient(h.cfg.RedisStore)
		if cache == nil {
			return fmt.Errorf("redis_store does not refer to a configured store.redis client")
		}
		if err := cache.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("redis startup check: %w", err)
		}
		probes = append(probes, health.Probe{Name: "redis", Check: func(ctx context.Context) error { return cache.Ping(ctx).Err() }})
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	if h.cfg.AutoMigrate {
		if err := client.Schema.Create(ctx); err != nil {
			_ = client.Close()
			return fmt.Errorf("ent migration: %w", err)
		}
	}
	if err := application.EnsureCatalogState(ctx, client); err != nil {
		_ = client.Close()
		return fmt.Errorf("catalog state (apply migrations/metro or set auto_migrate): %w", err)
	}
	h.resources.Store(&Resources{DB: client, Health: health.New(probes...)})
	if err := h.StartGRPC(); err != nil {
		_ = h.Shutdown()
		return err
	}
	return nil
}

// Shutdown drains RPCs, then closes the stores they use.
func (h *Handlers) Shutdown() error {
	h.StopGRPC()
	r := h.resources.Swap(nil)
	if r == nil || r.DB == nil {
		return nil
	}
	return errors.Join(r.DB.Close())
}

// Routes serves the internal health endpoints; business RPCs are gRPC only.
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
}
