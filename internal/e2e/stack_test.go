// Package e2e runs the real BFF in front of the real tripmap gRPC server over
// TLS. It is test-only and the one place that imports both services, so neither
// service package depends on the other and each image still builds alone.
//
// Dependency probes use a real PostgreSQL schema and Redis key prefix (the same
// isolated, self-cleaning ones the tripmap application tests use) when
// TRIPMAP_TEST_POSTGRES_DSN and TRIPMAP_TEST_REDIS_ADDR are set (make
// test-integration); otherwise they are in-memory stand-ins and only the
// backing-store probe differs.
package e2e_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	bffapp "github.com/kongken/mono/internal/bff/app"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/platform/testkit"
	tripmapapp "github.com/kongken/mono/internal/tripmap/app"
	tripmapconfig "github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/testutil"
	bfftripmapv1connect "github.com/kongken/mono/pkg/proto/bff/tripmap/v1/bfftripmapv1connect"
	"google.golang.org/grpc"
)

const (
	issuer   = "tripmap-bff"
	audience = "tripmap"
)

// stack is a BFF → TLS gRPC → tripmap chain.
type stack struct {
	t  *testing.T
	ca *testkit.CA
	// transport is what tripmap's gRPC listener serves: "tls" or "h2c".
	transport string
	keys      testkit.Keys // the BFF signing key, kid "k1"
	tripmap   *tripmapapp.Handlers
	bff       *httptest.Server
	status    bfftripmapv1connect.StatusServiceClient
	// failing makes the named dependency probe report not ready.
	failing map[string]*atomic.Bool
}

type options struct {
	// trusted lists the kid → key files tripmap trusts; defaults to {k1: keys}.
	trusted []servicetoken.TrustedKey
	extra   []func(grpc.ServiceRegistrar)
	// signing is the key and kid the BFF signs with; defaults to the stack's k1.
	signing    *testkit.Keys
	signingKID string
	// transport is tripmap's gRPC transport ("tls" default); bffTransport is what
	// the BFF is configured to use for tripmap and defaults to transport. Setting
	// them differently builds a deliberately mismatched deployment.
	transport, bffTransport string
}

// withTransport makes tripmap's gRPC listener and the BFF's upstream both use tr.
func withTransport(tr string) func(*options, *stack) {
	return func(o *options, _ *stack) { o.transport = tr }
}

// withMismatchedTransports configures tripmap and the BFF with different transports.
func withMismatchedTransports(server, bff string) func(*options, *stack) {
	return func(o *options, _ *stack) { o.transport, o.bffTransport = server, bff }
}

// transports are the internal gRPC transports every trust property must hold on.
var transports = []string{"tls", "h2c"}

func eachTransport(t *testing.T, fn func(t *testing.T, transport string)) {
	for _, tr := range transports {
		t.Run(tr, func(t *testing.T) { fn(t, tr) })
	}
}

func newStack(t *testing.T, mutate ...func(*options, *stack)) *stack {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := &stack{t: t, ca: testkit.NewCA(t), keys: testkit.NewKeys(t), failing: map[string]*atomic.Bool{"postgres": {}, "redis": {}, "s3": {}}}
	opts := &options{}
	for _, m := range mutate {
		m(opts, s)
	}
	if opts.trusted == nil {
		opts.trusted = []servicetoken.TrustedKey{{KID: "k1", PublicKeyFile: s.keys.PublicFile}}
	}

	if opts.transport == "" {
		opts.transport = "tls"
	}
	if opts.bffTransport == "" {
		opts.bffTransport = opts.transport
	}
	s.transport = opts.transport
	// h2c needs no certificate at all; tls uses a CA-issued one.
	grpcCfg := grpcx.ServerConfig{Transport: opts.transport, Listen: "127.0.0.1:0"}
	if opts.transport == "tls" {
		tls := s.ca.Issue(t, "tripmap.internal")
		grpcCfg.CertFile, grpcCfg.KeyFile = tls.CertFile, tls.KeyFile
	}
	cfg := &tripmapconfig.AppConfig{
		GRPC:         grpcCfg,
		InternalAuth: servicetoken.VerifyConfig{Issuer: issuer, Audience: audience, TrustedKeys: opts.trusted},
	}
	s.tripmap = tripmapapp.New(cfg)
	s.tripmap.SetResources(&tripmapapp.Resources{Health: health.New(s.probes()...)})
	if err := s.tripmap.StartGRPC(opts.extra...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.tripmap.Shutdown() })
	httpRouter := gin.New()
	s.tripmap.Routes(httpRouter)
	tripmapHTTP := httptest.NewServer(httpRouter)
	t.Cleanup(tripmapHTTP.Close)

	signing, kid := s.keys, "k1"
	if opts.signing != nil {
		signing, kid = *opts.signing, opts.signingKID
	}
	bffCfg := &bffconfig.Config{
		Upstreams:    map[string]string{"tripmap": tripmapHTTP.URL},
		InternalAuth: bffconfig.InternalAuth{Issuer: issuer, KID: kid, PrivateKeyFile: signing.PrivateFile},
		GRPC: bffconfig.GRPCConfig{CAFile: s.ca.CAFile, Services: map[string]bffconfig.GRPCService{
			"tripmap": {Address: s.tripmap.GRPCAddr(), ServerName: "tripmap.internal"},
		}},
	}
	if opts.bffTransport == "h2c" {
		// No CA and no server name: an all-h2c BFF has no TLS material at all.
		bffCfg.GRPC = bffconfig.GRPCConfig{Services: map[string]bffconfig.GRPCService{
			"tripmap": {Address: s.tripmap.GRPCAddr(), Transport: "h2c"},
		}}
	}
	b := bffapp.New(bffCfg)
	if err := b.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Shutdown() })
	router := gin.New()
	b.Routes(router)
	s.bff = httptest.NewServer(router)
	t.Cleanup(s.bff.Close)
	s.status = bfftripmapv1connect.NewStatusServiceClient(s.bff.Client(), s.bff.URL+"/api")
	return s
}

// probes returns the dependency probes tripmap reports. With the TRIPMAP_TEST_*
// variables set they query the isolated PostgreSQL schema and Redis prefix.
func (s *stack) probes() []health.Probe {
	up := func(context.Context) error { return nil }
	postgres, redis := up, up
	if os.Getenv("TRIPMAP_TEST_POSTGRES_DSN") != "" && os.Getenv("TRIPMAP_TEST_REDIS_ADDR") != "" {
		db, rdb, prefix := testutil.Backing(s.t)
		postgres = func(ctx context.Context) error { _, err := db.User.Query().Exist(ctx); return err }
		redis = func(ctx context.Context) error { return rdb.Set(ctx, prefix+"probe", "1", time.Minute).Err() }
	}
	wrap := func(name string, check func(context.Context) error) health.Probe {
		return health.Probe{Name: name, Check: func(ctx context.Context) error {
			if s.failing[name].Load() {
				return errors.New(name + " is down")
			}
			return check(ctx)
		}}
	}
	return []health.Probe{wrap("postgres", postgres), wrap("redis", redis), wrap("s3", up)}
}
