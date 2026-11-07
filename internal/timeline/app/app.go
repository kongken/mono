package app

import (
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
	"github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc"
	"net/http"
	"sync/atomic"
	"time"
)

type Resources struct {
	DB     *ent.Client
	Health *health.Checker
}
type Handlers struct {
	cfg       *config.AppConfig
	resources atomic.Pointer[Resources]
	grpc      atomic.Pointer[grpcx.Server]
}

func New(cfg *config.AppConfig) *Handlers { return &Handlers{cfg: cfg} }
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
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	if h.cfg.AutoMigrate {
		if err := client.Schema.Create(ctx); err != nil {
			return err
		}
	}
	h.resources.Store(&Resources{DB: client, Health: health.New(health.Probe{Name: "postgres", Check: db.PingContext})})
	return h.StartGRPC()
}
func (h *Handlers) Check(ctx context.Context) []health.Status {
	if r := h.resources.Load(); r != nil && r.Health != nil {
		return r.Health.Check(ctx)
	}
	return []health.Status{{Name: "postgres"}}
}
func (h *Handlers) StartGRPC() error {
	v, err := h.cfg.InternalAuth.NewVerifier()
	if err != nil {
		return err
	}
	srv, err := grpcx.Start(h.cfg.GRPC, func(r grpc.ServiceRegistrar) {
		if res := h.resources.Load(); res != nil {
			timelinev1.RegisterAccountCleanupServiceServer(r, application.NewAccountCleanupService(&accountcleanup.Store{DB: res.DB}))
		}
	}, []grpc.UnaryServerInterceptor{rpcauth.UnaryServerInterceptor(v)}, []grpc.StreamServerInterceptor{rpcauth.StreamServerInterceptor(v)})
	if err != nil {
		return err
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
