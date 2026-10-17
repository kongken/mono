package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	redisstore "butterfly.orx.me/core/store/redis"
	s3store "butterfly.orx.me/core/store/s3"
	"butterfly.orx.me/core/store/sqldb"
	"connectrpc.com/connect"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/stdlib"
	redisclient "github.com/redis/go-redis/v9"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	"github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/accountdeletionreceipt"
	"github.com/kongken/mono/internal/tripmap/regions"
	"github.com/kongken/mono/pkg/proto/tripmap/v1/tripmapv1connect"
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
}

func New(cfg *config.AppConfig) *Handlers {
	return &Handlers{cfg: cfg, notifier: application.NewNotifier(cfg.Notification)}
}

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
				cleanup, cancel := context.WithTimeout(maintenance, 10*time.Second)
				_, _ = client.AccountDeletionReceipt.Delete().Where(accountdeletionreceipt.ExpiresAtLTE(time.Now().UTC())).Exec(cleanup)
				cancel()
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
	index, err := regions.Default()
	if err != nil {
		panic(err) // the index is embedded, so this is a build defect
	}
	opts := connect.WithInterceptors(application.NewAuthInterceptor(h))
	api := r.Group("/api", httpx.CORS(func() []string { return h.cfg.Auth.CORSAllowedOrigins }, "X-Tripmap-Schema", "X-Tripmap-Regions"))
	mount := func(path string, handler http.Handler) {
		api.Any(path+"*path", gin.WrapH(http.StripPrefix("/api", handler)))
	}
	path, handler := tripmapv1connect.NewAuthServiceHandler(application.NewAuthService(h.cfg, h), opts)
	mount(path, handler)
	path, handler = tripmapv1connect.NewSnapshotServiceHandler(application.NewSnapshotService(h.cfg, h, index), opts)
	mount(path, handler)
	path, handler = tripmapv1connect.NewMediaServiceHandler(application.NewMediaService(h, index), opts, connect.WithReadMaxBytes(16*1024*1024))
	mount(path, handler)
	path, handler = tripmapv1connect.NewShareServiceHandler(application.NewShareService(h.cfg, h, index), opts)
	mount(path, handler)
	path, handler = tripmapv1connect.NewCollaborationServiceHandler(application.NewCollaborationService(h, index), opts)
	mount(path, handler)
	path, handler = tripmapv1connect.NewReportServiceHandler(application.NewReportService(h, index), opts)
	mount(path, handler)
	path, handler = tripmapv1connect.NewRegionServiceHandler(application.NewRegionService(h, index), opts, connect.WithReadMaxBytes(4096))
	mount(path, handler)
	path, handler = tripmapv1connect.NewSearchServiceHandler(application.NewSearchService(h, index), opts, connect.WithReadMaxBytes(4096))
	mount(path, handler)
	path, handler = tripmapv1connect.NewItineraryServiceHandler(application.NewItineraryService(h, index), opts, connect.WithReadMaxBytes(256*1024))
	mount(path, handler)
	mountShare(r, api, application.NewSharePublic(h.cfg, h))
}
