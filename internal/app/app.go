package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	redisstore "butterfly.orx.me/core/store/redis"
	s3store "butterfly.orx.me/core/store/s3"
	"butterfly.orx.me/core/store/sqldb"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/stdlib"
	redisclient "github.com/redis/go-redis/v9"
	"go.orx.me/apps/trip-map/internal/application"
	"go.orx.me/apps/trip-map/internal/blobstore"
	"go.orx.me/apps/trip-map/internal/config"
	"go.orx.me/apps/trip-map/internal/ent"
	"go.orx.me/apps/trip-map/internal/health"
	"go.orx.me/apps/trip-map/pkg/proto/tripmap/v1/tripmapv1connect"
)

type Resources struct {
	DB     *ent.Client
	Redis  *redisclient.Client
	Blobs  *blobstore.S3
	Health *health.Checker
}
type Handlers struct {
	cfg       *config.AppConfig
	resources atomic.Pointer[Resources]
}

func New(cfg *config.AppConfig) *Handlers { return &Handlers{cfg: cfg} }
func (h *Handlers) Check(ctx context.Context) []health.Status {
	if r := h.resources.Load(); r != nil {
		return r.Health.Check(ctx)
	}
	return []health.Status{{Name: "postgres"}, {Name: "redis"}, {Name: "s3"}}
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
	cache := redisstore.GetClient(h.cfg.RedisStore)
	if cache == nil {
		return fmt.Errorf("redis_store does not refer to a configured store.redis client")
	}
	s3Client, bucket := s3store.GetClient(h.cfg.Storage.S3Store), s3store.GetBucket(h.cfg.Storage.S3Store)
	if s3Client == nil || bucket == "" {
		return fmt.Errorf("storage.s3_store must refer to a configured S3 client with a bucket")
	}
	blobs := blobstore.NewS3(s3Client, bucket, h.cfg.Storage.KeyPrefix)
	if err := cache.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis startup check: %w", err)
	}
	if err := blobs.Check(ctx); err != nil {
		return fmt.Errorf("S3 bucket unavailable (create it before startup): %w", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	if h.cfg.AutoMigrate {
		if err := client.Schema.Create(ctx); err != nil {
			_ = client.Close()
			return fmt.Errorf("ent migration: %w", err)
		}
	}
	probes := health.New(
		health.Probe{Name: "postgres", Check: db.PingContext},
		health.Probe{Name: "redis", Check: func(ctx context.Context) error { return cache.Ping(ctx).Err() }},
		health.Probe{Name: "s3", Check: blobs.Check},
	)
	h.resources.Store(&Resources{DB: client, Redis: cache, Blobs: blobs, Health: probes})
	return nil
}
func (h *Handlers) Shutdown() error {
	r := h.resources.Swap(nil)
	if r == nil {
		return nil
	}
	return errors.Join(r.DB.Close(), r.Redis.Close())
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
	path, handler := tripmapv1connect.NewSystemServiceHandler(application.NewSystemService(h))
	r.Any("/api"+path+"*path", gin.WrapH(http.StripPrefix("/api", handler)))
}
