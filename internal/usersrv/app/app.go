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
	"github.com/kongken/mono/internal/usersrv/mailer"
	"github.com/kongken/mono/internal/usersrv/outbox"
	"github.com/kongken/mono/internal/usersrv/passwordhash"
	"github.com/kongken/mono/internal/usersrv/ratelimit"
	"github.com/kongken/mono/internal/usersrv/worker"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

// maintenanceInterval is how often expired tokens, tombstones, audit rows and
// finished mail job records are purged.
const maintenanceInterval = time.Hour

// outboxInterval is how often the in-process mail worker looks for due jobs.
const outboxInterval = 3 * time.Second

// Resources are the connected backing services and what is built on them.
type Resources struct {
	DB       *ent.Client
	Accounts *account.Service
	Limiter  application.Limiter
	Limits   application.Limits
	// Mail is the outbox that issues challenges and delivers their mail; MailGuard
	// applies the account mail limits. Both are nil only in tests that do not use mail.
	Mail      *outbox.Service
	MailGuard *outbox.Guard
	Health    *health.Checker
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
	return []health.Status{{Name: "postgres"}, {Name: "redis"}, {Name: "worker"}, {Name: "mail_outbox"}}
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
	mail, err := h.buildMail(client)
	if err != nil {
		_ = client.Close()
		return err
	}
	// A queued job that needs a key we no longer have must stop the start, not be dropped.
	if err := mail.Preflight(ctx); err != nil {
		_ = client.Close()
		return err
	}
	res, err := NewResources(client, hasher, cache, h.cfg.KeyPrefix, hmacKey, limitsOf(rules), db.PingContext,
		WithMail(mail), WithRegistrationOpen(h.cfg.Auth.RegistrationOpen()))
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
		TokenSubmitPerIP: l.TokenSubmitPerIP, TokenSubmitPerToken: l.TokenSubmitPerToken,
		Mail: l.Mail,
	}
}

// buildMail assembles the mail outbox from the configuration: the one active
// vendor adapter, the fixed link builder and the encryption keys. Nothing here
// contacts the vendor.
func (h *Handlers) buildMail(db *ent.Client) (*outbox.Service, error) {
	keys, err := h.cfg.Mail.Keyring()
	if err != nil {
		return nil, err
	}
	m, err := mailer.New(h.cfg.Mail.Config)
	if err != nil {
		return nil, err
	}
	origins := make(map[string]string, len(h.cfg.Auth.Apps))
	for id, a := range h.cfg.Auth.Apps {
		origins[id] = a.Origin
	}
	links, err := mailer.NewLinks(origins, h.cfg.Auth.AllowLoopbackHTTP)
	if err != nil {
		return nil, err
	}
	regTTL, err := time.ParseDuration(h.cfg.Auth.RegistrationTTL)
	if err != nil {
		return nil, err
	}
	resetTTL, err := time.ParseDuration(h.cfg.Auth.ResetTTL)
	if err != nil {
		return nil, err
	}
	return outbox.New(db, keys, links, m, outbox.Settings{RegistrationTTL: regTTL, ResetTTL: resetTTL})
}

// ResourceOption customises NewResources.
type ResourceOption func(*resourceOptions)

type resourceOptions struct {
	mail               *outbox.Service
	registrationClosed bool
}

// WithRegistrationOpen sets auth.registration_enabled (default open).
func WithRegistrationOpen(open bool) ResourceOption {
	return func(o *resourceOptions) { o.registrationClosed = !open }
}

// WithMail adds the mail outbox, its worker and its readiness probe.
func WithMail(m *outbox.Service) ResourceOption { return func(o *resourceOptions) { o.mail = m } }

// NewResources builds the account core, limiter, maintenance worker and health
// probes over connected stores. Bootstrap uses it; so do tests.
func NewResources(db *ent.Client, hasher *passwordhash.Hasher, rdb redis.UniversalClient, keyPrefix string, hmacKey []byte,
	limits application.Limits, pingDB func(context.Context) error, opts ...ResourceOption) (*Resources, error) {
	var o resourceOptions
	for _, opt := range opts {
		opt(&o)
	}
	limiter, err := ratelimit.New(rdb, keyPrefix, hmacKey)
	if err != nil {
		return nil, err
	}
	accounts := account.NewService(db, hasher)
	accounts.SetRegistrationOpen(!o.registrationClosed)
	janitor := worker.New("maintenance", maintenanceInterval, func(ctx context.Context) error {
		_, err := accounts.PurgeExpired(ctx)
		if o.mail != nil {
			if _, perr := o.mail.Purge(ctx); perr != nil {
				err = errors.Join(err, perr)
			}
		}
		return err
	})
	probes := []health.Probe{
		{Name: "postgres", Check: pingDB},
		{Name: "redis", Check: func(ctx context.Context) error { return rdb.Ping(ctx).Err() }},
		{Name: "worker", Check: janitor.Check},
	}
	res := &Resources{DB: db, Accounts: accounts, Limiter: limiter, Limits: limits, Workers: []*worker.Loop{janitor}}
	if o.mail != nil {
		guard, err := outbox.NewGuard(limiter, limits.Mail)
		if err != nil {
			return nil, err
		}
		sender := worker.New("mail-outbox", outboxInterval, func(ctx context.Context) error {
			_, err := o.mail.RunOnce(ctx)
			return err
		})
		probes = append(probes, health.Probe{Name: "mail_outbox", Check: sender.Check})
		res.Mail, res.MailGuard = o.mail, guard
		accounts.SetMail(AccountMail(o.mail, guard))
		res.Workers = append(res.Workers, sender)
	}
	res.Health = health.New(probes...)
	return res, nil
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
