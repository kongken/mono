package app

import (
	s3store "butterfly.orx.me/core/store/s3"
	"butterfly.orx.me/core/store/sqldb"
	"context"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/timeline/accountcleanup"
	"github.com/kongken/mono/internal/timeline/application"
	"github.com/kongken/mono/internal/timeline/config"
	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/media"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc"
	"net/http"
	"sync/atomic"
	"time"
)

type Resources struct {
	DB     *ent.Client
	Blobs  blobstore.Store
	Health *health.Checker
}
type Handlers struct {
	cfg       *config.AppConfig
	resources atomic.Pointer[Resources]
	grpc      atomic.Pointer[grpcx.Server]
}

func New(cfg *config.AppConfig) *Handlers { return &Handlers{cfg: cfg} }
func (h *Handlers) Bootstrap(ctx context.Context) error {
	if e := h.cfg.Validate(); e != nil {
		return e
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
	if e := db.PingContext(ctx); e != nil {
		return e
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	s3c, bucket := s3store.GetClient(h.cfg.Storage.S3Store), s3store.GetBucket(h.cfg.Storage.S3Store)
	if s3c == nil || bucket == "" {
		return fmt.Errorf("storage.s3_store must refer to a configured S3 client with a bucket")
	}
	blobs := blobstore.NewS3(s3c, bucket, h.cfg.Storage.KeyPrefix)
	if e := blobs.Check(ctx); e != nil {
		return fmt.Errorf("S3 bucket unavailable: %w", e)
	}
	if h.cfg.AutoMigrate {
		if e := client.Schema.Create(ctx); e != nil {
			return e
		}
	}
	h.resources.Store(&Resources{DB: client, Blobs: blobs, Health: health.New(health.Probe{Name: "postgres", Check: db.PingContext}, health.Probe{Name: "s3", Check: blobs.Check})})
	return h.StartGRPC()
}
func (h *Handlers) Check(ctx context.Context) []health.Status {
	if r := h.resources.Load(); r != nil && r.Health != nil {
		return r.Health.Check(ctx)
	}
	return []health.Status{{Name: "postgres"}, {Name: "s3"}}
}
func (h *Handlers) StartGRPC() error {
	v, e := h.cfg.InternalAuth.NewVerifier()
	if e != nil {
		return e
	}
	srv, e := grpcx.Start(h.cfg.GRPC, func(r grpc.ServiceRegistrar) {
		if res := h.resources.Load(); res != nil {
			timelinev1.RegisterTimelineServiceServer(r, application.NewTimelineService(res.DB))
			timelinev1.RegisterMediaServiceServer(r, application.NewMediaService(&media.Store{DB: res.DB, Blobs: res.Blobs}))
			timelinev1.RegisterAccountCleanupServiceServer(r, application.NewAccountCleanupService(&accountcleanup.Store{DB: res.DB}))
		}
	}, []grpc.UnaryServerInterceptor{rpcauth.UnaryServerInterceptor(v)}, []grpc.StreamServerInterceptor{rpcauth.StreamServerInterceptor(v)})
	if e != nil {
		return e
	}
	h.grpc.Store(srv)
	return nil
}
func (h *Handlers) Shutdown() error {
	if s := h.grpc.Swap(nil); s != nil {
		s.Shutdown(10 * time.Second)
	}
	if r := h.resources.Swap(nil); r != nil && r.DB != nil {
		return r.DB.Close()
	}
	return nil
}
func (h *Handlers) Routes(r *gin.Engine) {
	r.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"message": "pong"}) })
	r.GET("/readyz", func(c *gin.Context) {
		s := h.Check(c)
		code := http.StatusOK
		if !health.Ready(s) {
			code = http.StatusServiceUnavailable
		}
		c.JSON(code, gin.H{"ready": health.Ready(s), "dependencies": s})
	})
}
