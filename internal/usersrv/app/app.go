// Package app wires the usersrv service: its PostgreSQL and Redis stores, the
// account core, the background worker, the gRPC listener the BFF calls, and the
// health endpoints.
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
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/application"
	"github.com/kongken/mono/internal/usersrv/config"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/passwordhash"
	"github.com/kongken/mono/internal/usersrv/ratelimit"
	"github.com/kongken/mono/internal/usersrv/worker"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

// maintenanceInterval is how often expired tokens, tombstones and audit rows are purged.
const maintenanceInterval = time.Hour

// Resources are the connected backing services and what is built on them.
type Resources struct {
	DB       *ent.Client
	Accounts *account.Service
	Limiter  application.Limiter
	Limits   application.Limits
	Health   *health.Checker
	// Workers run in the background and stop on shutdown.
	Workers []*worker.Loop
}

// Handlers is the usersrv service.
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
// Tests use it to run the real gRPC server over isolated PostgreSQL and Redis.
func (h *Handlers) SetResources(r *Resources) { h.resources.Store(r) }

// Check reports dependency readiness for /readyz and the BFF's status probe.
func (h *Handlers) Check(ctx context.Context) []health.Status {
	if r := h.resources.Load(); r != nil && r.Health != nil {
		return r.Health.Check(ctx)
	}
	return []health.Status{{Name: "postgres"}, {Name: "redis"}, {Name: "worker"}}
}

// Bootstrap runs after Butterfly loads YAML and registers its named store clients.
func (h *Handlers) Bootstrap(ctx context.Context) error {
	if err := h.cfg.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	hashCfg, err := h.cfg.Auth.PasswordHash.Resolve()
	if err != nil {
		return err
	}
	hasher, err := passwordhash.New(hashCfg)
	if err != nil {
		return fmt.Errorf("password hashing: %w", err)
	}
	hmacKey, err := h.cfg.Auth.HMACKeyBytes()
	if err != nil {
		return err
	}
	rules, err := h.cfg.RateLimit.Limits()
	if err != nil {
		return err
	}
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
	cache := redisstore.GetClient(h.cfg.RedisStore)
	if cache == nil {
		return fmt.Errorf("redis_store does not refer to a configured store.redis client")
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
	// A database without the schema must fail at start, not at the first login.
	if _, err := client.User.Query().Limit(1).Count(ctx); err != nil {
		_ = client.Close()
		return fmt.Errorf("account schema (apply migrations/usersrv or set auto_migrate): %w", err)
	}
	res, err := NewResources(client, hasher, cache, h.cfg.KeyPrefix, hmacKey, limitsOf(rules), db.PingContext)
	if err != nil {
		_ = client.Close()
		return err
	}
	h.resources.Store(res)
	for _, w := range res.Workers {
		w.Start()
	}
	if err := h.StartGRPC(); err != nil {
		_ = h.Shutdown()
		return err
	}
	return nil
}

func limitsOf(l config.Limits) application.Limits {
	return application.Limits{
		PasswordPerAccount: l.PasswordPerAccount, PasswordPerIP: l.PasswordPerIP,
		ProfileWritePerUser: l.ProfileWritePerUser, DeletionPerUser: l.DeletionPerUser,
	}
}

// NewResources builds the account core, limiter, maintenance worker and health
// probes over connected stores. Bootstrap uses it; so do tests.
func NewResources(db *ent.Client, hasher *passwordhash.Hasher, rdb redis.UniversalClient, keyPrefix string, hmacKey []byte,
	limits application.Limits, pingDB func(context.Context) error) (*Resources, error) {
	limiter, err := ratelimit.New(rdb, keyPrefix, hmacKey)
	if err != nil {
		return nil, err
	}
	accounts := account.NewService(db, hasher)
	janitor := worker.New("maintenance", maintenanceInterval, func(ctx context.Context) error {
		_, err := accounts.PurgeExpired(ctx)
		return err
	})
	probes := []health.Probe{
		{Name: "postgres", Check: pingDB},
		{Name: "redis", Check: func(ctx context.Context) error { return rdb.Ping(ctx).Err() }},
		{Name: "worker", Check: janitor.Check},
	}
	return &Resources{DB: db, Accounts: accounts, Limiter: limiter, Limits: limits, Health: health.New(probes...), Workers: []*worker.Loop{janitor}}, nil
}

// Shutdown drains RPCs, stops the workers, then closes the stores they use.
func (h *Handlers) Shutdown() error {
	h.StopGRPC()
	r := h.resources.Swap(nil)
	if r == nil {
		return nil
	}
	for _, w := range r.Workers {
		w.Stop()
	}
	if r.DB == nil {
		return nil
	}
	return errors.Join(r.DB.Close())
}

// Routes serves the internal health endpoints; account RPCs are gRPC only.
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
