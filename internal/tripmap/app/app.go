package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	"github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/regions"
	"github.com/kongken/mono/internal/tripmap/tiles"
	"google.golang.org/grpc"
)

type Resources struct {
	DB     *ent.Client
	Redis  *redisclient.Client
	Blobs  blobstore.Store
	Health *health.Checker
	// KeyPrefix namespaces Redis keys; empty means the default. Only tests set it.
	KeyPrefix string
	stop      context.CancelFunc
}
type Handlers struct {
	cfg       *config.AppConfig
	resources atomic.Pointer[Resources]
	grpc      atomic.Pointer[grpcx.Server]
	notifier  *application.Notifier
	tiles     *tiles.Handler
	grpcUnaryExtra []grpc.UnaryServerInterceptor
}

func (h *Handlers) SetGRPCTestUnaryInterceptors(in ...grpc.UnaryServerInterceptor) {
	h.grpcUnaryExtra = append(h.grpcUnaryExtra, in...)
}

func New(cfg *config.AppConfig) *Handlers {
	h := &Handlers{cfg: cfg, notifier: application.NewNotifier(cfg.Notification)}
	h.tiles = tiles.New(cfg.MapboxTiles, tiles.Deps{ClientIPHeader: cfg.Auth.ClientIPHeader, Limiter: h.tileLimiter})
	return h
}

// tileLimiter is the Redis limiter once the stores are connected; before that the
// tile route answers 502 instead of fetching without a limit.
func (h *Handlers) tileLimiter() tiles.Limiter {
	if st := h.Store(); st != nil {
		return st.Limiter()
	}
	return nil
}

// SetTileClient replaces the HTTP client used to reach Mapbox; tests use it to keep
// the network out.
func (h *Handlers) SetTileClient(c *http.Client) { h.tiles.SetClient(c) }

// SetTileLimiter replaces the tile rate limiter; tests use it instead of Redis.
func (h *Handlers) SetTileLimiter(l tiles.Limiter) { h.tiles.SetLimiter(l) }

// SetNotifier replaces the notification channels; tests use it to avoid real SMTP
// and push services.
func (h *Handlers) SetNotifier(n *application.Notifier) { h.notifier = n }
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
	index, err := regions.Default()
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("region index: %w", err)
	}
	maintenance, stop := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-maintenance.Done():
				return
			case <-ticker.C:
				runSnapshotMaintenance(maintenance, client, cache)
				runReportMaintenance(maintenance, &application.Store{DB: client, Redis: cache, Blobs: blobs}, index)
				runNotificationMaintenance(maintenance, &application.Store{DB: client, Redis: cache, Blobs: blobs}, index, h.notifier)
				mediaCtx, mediaCancel := context.WithTimeout(maintenance, 5*time.Minute)
				if err := application.RunMediaMaintenance(mediaCtx, &application.Store{DB: client, Redis: cache, Blobs: blobs}, time.Now().UTC()); err != nil {
					slog.Error("media maintenance", "error", err)
				}
				mediaCancel()
			}
		}
	}()
	h.resources.Store(&Resources{DB: client, Redis: cache, Blobs: blobs, Health: probes, stop: stop})
	if err := h.StartGRPC(); err != nil {
		_ = h.Shutdown()
		return err
	}
	return nil
}

// snapshotLockKey makes one instance at a time run the snapshot job; the job is
// also safe without it, the lock only avoids repeated work across replicas.
const (
	snapshotLockKey = "tripmap:snapshot-maintenance"
	reportLockKey   = "tripmap:report-maintenance"
	// Replicas share one run per tick; notification_deliveries makes a repeated
	// run harmless even if the lock is lost.
	notificationLockKey = "tripmap:notification-maintenance"
)

func runSnapshotMaintenance(ctx context.Context, db *ent.Client, cache *redisclient.Client) {
	if ok, err := cache.SetNX(ctx, snapshotLockKey, "1", 50*time.Minute).Result(); err != nil || !ok {
		return
	}
	run, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := application.RunSnapshotMaintenance(run, db, time.Now().UTC()); err != nil {
		slog.Error("snapshot maintenance", "error", err)
	}
}

func runReportMaintenance(ctx context.Context, st *application.Store, index *regions.Index) {
	if ok, err := st.Redis.SetNX(ctx, reportLockKey, "1", 50*time.Minute).Result(); err != nil || !ok {
		return
	}
	run, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := application.RunReportMaintenance(run, st, index, time.Now().UTC()); err != nil {
		slog.Error("report maintenance", "error", err)
	}
}
func runNotificationMaintenance(ctx context.Context, st *application.Store, index *regions.Index, n *application.Notifier) {
	if !n.EmailAvailable() && !n.PushAvailable() {
		return
	}
	if ok, err := st.Redis.SetNX(ctx, notificationLockKey, "1", 50*time.Minute).Result(); err != nil || !ok {
		return
	}
	run, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := application.RunNotificationMaintenance(run, st, index, n); err != nil {
		slog.Error("notification maintenance", "error", err)
	}
}

func (h *Handlers) Shutdown() error {
	// Drain RPCs before the stores they use go away.
	h.StopGRPC()
	r := h.resources.Swap(nil)
	if r == nil {
		return nil
	}
	if r.stop != nil {
		r.stop()
	}
	var errs []error
	if r.DB != nil {
		errs = append(errs, r.DB.Close())
	}
	if r.Redis != nil {
		errs = append(errs, r.Redis.Close())
	}
	return errors.Join(errs...)
}

// SetResources installs already connected services instead of bootstrapping them.
// Tests use it to run the real routes over isolated databases.
func (h *Handlers) SetResources(r *Resources) { h.resources.Store(r) }

// Store exposes the connected backing services to RPC services, or nil before
// bootstrap has finished.
func (h *Handlers) Store() *application.Store {
	r := h.resources.Load()
	if r == nil || r.DB == nil || r.Redis == nil {
		return nil
	}
	return &application.Store{DB: r.DB, Redis: r.Redis, Blobs: r.Blobs, KeyPrefix: r.KeyPrefix}
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
	// Anonymous Mapbox basemap tiles. The BFF forwards this one path; the handler
	// answers 404 unless mapbox_tiles.enabled is set.
	tileRoute := gin.WrapH(h.tiles)
	r.Any(strings.TrimSuffix(tiles.PathPrefix, "/"), tileRoute)
	r.Any(tiles.PathPrefix+"*rest", tileRoute)
}

// MountShare registers public share HTTP routes (tests only; production serves them on the BFF).
func MountShare(r *gin.Engine, api *gin.RouterGroup, public *application.SharePublic) {
	mountShare(r, api, public)
}
