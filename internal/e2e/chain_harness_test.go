// Package e2e runs the real BFF in front of the real usersrv and tripmap (and,
// on request, metro) gRPC servers over TLS or h2c.
//
// ChainHarness starts the services with isolated PostgreSQL schemas and Redis
// key prefixes (requires TRIPMAP_TEST_POSTGRES_DSN, TRIPMAP_TEST_REDIS_ADDR,
// USERSRV_TEST_POSTGRES_DSN and USERSRV_TEST_REDIS_ADDR, same as make
// test-integration). The only thing replaced is the far end of account mail: a
// local Inbox that records what a vendor would have been handed. Use
// FreshBrowser for a cookie jar + CSRF-aware Connect client against the BFF
// public URL:
//
//	h := NewChainHarness(t)
//	a := h.FreshBrowser()
//	id, err := a.Register(t.Context(), "alice", "", "Alice")
//
// Register runs the real flow: request mail, read the token out of the inbox,
// complete the registration, sign in.
//
// Later tickets add scenarios by reusing ChainHarness and Browser helpers only.
package e2e_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	bffapp "github.com/kongken/mono/internal/bff/app"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	bffent "github.com/kongken/mono/internal/bff/ent"
	bffsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/session"
	bfftestutil "github.com/kongken/mono/internal/bff/testutil"
	metroapp "github.com/kongken/mono/internal/metro/app"
	metroconfig "github.com/kongken/mono/internal/metro/config"
	metroent "github.com/kongken/mono/internal/metro/ent"
	metrotestutil "github.com/kongken/mono/internal/metro/testutil"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/platform/testkit"
	tripmapapp "github.com/kongken/mono/internal/tripmap/app"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	tripmapconfig "github.com/kongken/mono/internal/tripmap/config"
	tripmapent "github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/regions"
	"github.com/kongken/mono/internal/tripmap/testutil"
	usersrvaccount "github.com/kongken/mono/internal/usersrv/account"
	usersrvapp "github.com/kongken/mono/internal/usersrv/app"
	usersrvapplication "github.com/kongken/mono/internal/usersrv/application"
	usersrvconfig "github.com/kongken/mono/internal/usersrv/config"
	usersrvent "github.com/kongken/mono/internal/usersrv/ent"
	usersrvmailer "github.com/kongken/mono/internal/usersrv/mailer"
	usersrvoutbox "github.com/kongken/mono/internal/usersrv/outbox"
	usersrvtestutil "github.com/kongken/mono/internal/usersrv/testutil"
	bffmetrov1connect "github.com/kongken/mono/pkg/proto/bff/metro/v1/bffmetrov1connect"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	bfftripmapv1connect "github.com/kongken/mono/pkg/proto/bff/tripmap/v1/bfftripmapv1connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// ChainHarness is a live BFF → internal gRPC (TLS or h2c) → tripmap stack with real auth and business DBs.
type ChainHarness struct {
	T *testing.T

	BFFURL  string
	BFF     *bffapp.Handlers
	Tripmap *tripmapapp.Handlers

	BFFDB     *bffent.Client
	TripmapDB *tripmapent.Client

	// Usersrv is the real account service; Accounts is its account core, for the
	// operator actions that have no browser path (granting a role).
	Usersrv   *usersrvapp.Handlers
	UsersrvDB *usersrvent.Client
	// UsersrvSQL is the connection under UsersrvDB, for row states no code path produces.
	UsersrvSQL   *sql.DB
	UsersrvRes   *usersrvapp.Resources
	Accounts     *usersrvaccount.Service
	Inbox        *Inbox
	usersrvFault *usersrvFault

	// Metro and MetroDB are set when the harness was built with ChainMetro.
	Metro   *metroapp.Handlers
	MetroDB *metroent.Client

	regionVersion string
	// transport is the internal gRPC transport in use: "tls" or "h2c".
	transport  string
	caFile     string
	serverName string
	signer     *servicetoken.Signer
}

// Browser is a cookie-aware Connect client for bff.v1 (CSRF + schema headers).
type Browser struct {
	t          *testing.T
	h          *ChainHarness
	HTTP       *http.Client
	Auth       bffv1connect.AuthServiceClient
	Users      bffv1connect.UserServiceClient
	Tokens     bffv1connect.AccessTokenServiceClient
	Catalog    bffmetrov1connect.CatalogServiceClient
	MetroAdmin bffmetrov1connect.AdminServiceClient
	MetroRider bffmetrov1connect.RiderServiceClient
	Foot       bfftripmapv1connect.FootprintServiceClient
	Snap       bfftripmapv1connect.SnapshotServiceClient
	Friends    bfftripmapv1connect.FriendServiceClient
	SharedTrip bfftripmapv1connect.SharedTripServiceClient
	Itin       bfftripmapv1connect.ItineraryServiceClient
	media      bfftripmapv1connect.MediaServiceClient
	Report     bfftripmapv1connect.ReportServiceClient
	Search     bfftripmapv1connect.SearchServiceClient
	RegionSvc  bfftripmapv1connect.RegionServiceClient
	APIURL     string
	Origin     string
	Region     string
}

// ChainHTTPOptions configures browser tests before Bootstrap, using the actual
// listener URL so the provider's fixed redirect URI matches the HTTP callback.
type ChainHTTPOptions struct {
	Observe   func(*http.Request, http.Header, int)
	TLS       bool
	Configure func(*bffconfig.Config, string)
	// MailOzidOrigin is the central OzID origin that account mail links to (usersrv's
	// auth.apps.ozid); the default is a reserved example name nothing resolves.
	MailOzidOrigin string
}

// ChainMetro adds the metro service (with an isolated database of its own) to the
// harness and registers it with the BFF as an optional product.
type ChainMetro struct{}

// ChainMetroUnary adds server interceptors to metro (after authentication), for
// injecting faults.
type ChainMetroUnary []grpc.UnaryServerInterceptor

// ChainTransport selects the internal gRPC transport of a harness ("tls" or
// "h2c"). Without it the harness uses TRIPMAP_TEST_GRPC_TRANSPORT, then tls, so
// the whole suite can be rerun over h2c.
type ChainTransport string

// NewChainHarness builds the full stack or skips when storage env is unset.
// Optional extras: ChainUsersrvLimits, func(*tripmapconfig.AppConfig), blobstore.Store,
// ChainHTTPOptions, ChainTransport, []grpc.UnaryServerInterceptor.
func NewChainHarness(t *testing.T, extras ...any) *ChainHarness {
	t.Helper()
	usersrvLimit := usersrvLimits()
	var httpOptions ChainHTTPOptions
	var mutate []func(*tripmapconfig.AppConfig)
	var tripUnary []grpc.UnaryServerInterceptor
	withMetro := false
	var metroUnary []grpc.UnaryServerInterceptor
	transport := os.Getenv("TRIPMAP_TEST_GRPC_TRANSPORT")
	blob := blobstore.Store(noopBlobs{})
	for _, e := range extras {
		switch v := e.(type) {
		case ChainHTTPOptions:
			httpOptions = v
		case ChainTransport:
			transport = string(v)
		case ChainMetro:
			withMetro = true
		case ChainMetroUnary:
			metroUnary = v
		case ChainUsersrvLimits:
			usersrvLimit = usersrvapplication.Limits(v)
		case func(*tripmapconfig.AppConfig):
			mutate = append(mutate, v)
		case blobstore.Store:
			if v != nil {
				blob = v
			}
		case []grpc.UnaryServerInterceptor:
			tripUnary = v
		default:
			t.Fatalf("NewChainHarness: unsupported option %T", e)
		}
	}
	gin.SetMode(gin.TestMode)
	idx, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}
	h := &ChainHarness{T: t, regionVersion: idx.Version()}

	ca := testkit.NewCA(t)
	keys := testkit.NewKeys(t)
	if transport == "" {
		transport = "tls"
	}
	h.transport = transport
	grpcCfg := grpcx.ServerConfig{Transport: transport, Listen: "127.0.0.1:0"}
	if transport == "tls" { // h2c needs no certificate
		tls := ca.Issue(t, "tripmap.internal")
		grpcCfg.CertFile, grpcCfg.KeyFile = tls.CertFile, tls.KeyFile
	}
	h.caFile = ca.CAFile
	h.serverName = "tripmap.internal"
	h.signer = &servicetoken.Signer{Issuer: issuer, KID: "k1", Key: keys.Private}

	tripDB, tripRedis, tripPrefix := testutil.Backing(t)
	bffDB, bffRedis, bffPrefix := bfftestutil.Backing(t)
	h.TripmapDB = tripDB
	h.BFFDB = bffDB

	tripCfg := &tripmapconfig.AppConfig{
		GRPC:         grpcCfg,
		InternalAuth: servicetoken.VerifyConfig{Issuer: issuer, Audience: audience, TrustedKeys: []servicetoken.TrustedKey{{KID: "k1", PublicKeyFile: keys.PublicFile}}},
	}
	for _, m := range mutate {
		m(tripCfg)
	}
	h.Tripmap = tripmapapp.New(tripCfg)
	if len(tripUnary) > 0 {
		h.Tripmap.SetGRPCTestUnaryInterceptors(tripUnary...)
	}
	h.Tripmap.SetResources(&tripmapapp.Resources{
		DB: tripDB, Redis: tripRedis, Blobs: blob, KeyPrefix: tripPrefix,
		Health: health.New(
			health.Probe{Name: "postgres", Check: func(ctx context.Context) error { _, err := tripDB.User.Query().Exist(ctx); return err }},
			health.Probe{Name: "redis", Check: func(ctx context.Context) error { return tripRedis.Ping(ctx).Err() }},
			health.Probe{Name: "s3", Check: func(context.Context) error { return nil }},
		),
	})
	if err := h.Tripmap.StartGRPC(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Tripmap.Shutdown() })

	tripHTTP := gin.New()
	h.Tripmap.Routes(tripHTTP)
	tripSrv := httptest.NewServer(tripHTTP)
	t.Cleanup(tripSrv.Close)

	// usersrv: the real account service over its own PostgreSQL schema and Redis
	// prefix. Mail is the only thing replaced; the outbox runs for real and the
	// harness delivers it on demand (DeliverMail).
	h.Inbox = &Inbox{}
	h.usersrvFault = &usersrvFault{}
	usersrvDB, usersrvSQL := usersrvtestutil.DBWithSQL(t)
	usersrvRedis, usersrvPrefix := usersrvtestutil.Redis(t)
	usersrvGRPC := grpcx.ServerConfig{Transport: transport, Listen: "127.0.0.1:0"}
	if transport == "tls" {
		tls := ca.Issue(t, "usersrv.internal")
		usersrvGRPC.CertFile, usersrvGRPC.KeyFile = tls.CertFile, tls.KeyFile
	}
	h.Usersrv = usersrvapp.New(&usersrvconfig.AppConfig{
		GRPC:         usersrvGRPC,
		InternalAuth: servicetoken.VerifyConfig{Issuer: issuer, Audience: "usersrv", TrustedKeys: []servicetoken.TrustedKey{{KID: "k1", PublicKeyFile: keys.PublicFile}}},
	})
	h.Usersrv.SetGRPCTestUnaryInterceptors(h.usersrvFault.interceptor())
	mailSender, err := usersrvmailer.NewWithSender(usersrvmailer.Config{Provider: "sendgrid", FromEmail: "no-reply@example.com", SendGrid: usersrvmailer.SendGridConfig{APIKey: "SG.key"}}, h.Inbox)
	if err != nil {
		t.Fatal(err)
	}
	mailOzid := "https://id.example.com"
	if httpOptions.MailOzidOrigin != "" {
		mailOzid = httpOptions.MailOzidOrigin
	}
	links, err := usersrvmailer.NewLinks(map[string]string{"ozid": mailOzid, "trip-map": "https://map.example.com", "metro": "https://metro.example.com"}, false)
	if err != nil {
		t.Fatal(err)
	}
	outboxSvc, err := usersrvoutbox.New(usersrvDB, mailKeyring(t), links, mailSender, usersrvoutbox.Settings{RegistrationTTL: 30 * time.Minute, ResetTTL: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	usersrvRes, err := usersrvapp.NewResources(usersrvDB, usersrvtestutil.Hasher(t), usersrvRedis, usersrvPrefix, []byte(strings.Repeat("h", 32)), usersrvLimit,
		func(ctx context.Context) error { _, err := usersrvDB.User.Query().Exist(ctx); return err }, usersrvapp.WithMail(outboxSvc))
	if err != nil {
		t.Fatal(err)
	}
	h.Usersrv.SetResources(usersrvRes)
	h.UsersrvDB, h.UsersrvSQL, h.UsersrvRes, h.Accounts = usersrvDB, usersrvSQL, usersrvRes, usersrvRes.Accounts
	// The workers run as in production (readiness reports them); DeliverMail
	// additionally runs the outbox by hand so a scenario does not wait for a tick.
	for _, w := range usersrvRes.Workers {
		w.Start()
	}
	if err = h.Usersrv.StartGRPC(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Usersrv.Shutdown() })
	usersrvHTTP := gin.New()
	h.Usersrv.Routes(usersrvHTTP)
	usersrvSrv := httptest.NewServer(usersrvHTTP)
	t.Cleanup(usersrvSrv.Close)

	bffCfg := &bffconfig.Config{
		Upstreams:    map[string]string{"usersrv": usersrvSrv.URL, "tripmap": tripSrv.URL},
		InternalAuth: bffconfig.InternalAuth{Issuer: issuer, KID: "k1", PrivateKeyFile: keys.PrivateFile},
		GRPC: bffconfig.GRPCConfig{CAFile: ca.CAFile, Services: map[string]bffconfig.GRPCService{
			"usersrv": {Address: h.Usersrv.GRPCAddr(), ServerName: "usersrv.internal"},
			"tripmap": {Address: h.Tripmap.GRPCAddr(), ServerName: "tripmap.internal"},
		}},
		Auth: bffconfig.AuthConfig{CookieSecure: false},
	}
	if transport == "h2c" {
		bffCfg.GRPC = bffconfig.GRPCConfig{Services: map[string]bffconfig.GRPCService{
			"usersrv": {Address: h.Usersrv.GRPCAddr(), Transport: "h2c"},
			"tripmap": {Address: h.Tripmap.GRPCAddr(), Transport: "h2c"},
		}}
	}
	if withMetro {
		h.MetroDB = metrotestutil.DB(t)
		metroGRPC := grpcx.ServerConfig{Transport: transport, Listen: "127.0.0.1:0"}
		if transport == "tls" {
			tls := ca.Issue(t, "metro.internal")
			metroGRPC.CertFile, metroGRPC.KeyFile = tls.CertFile, tls.KeyFile
		}
		h.Metro = metroapp.New(&metroconfig.AppConfig{
			GRPC:         metroGRPC,
			InternalAuth: servicetoken.VerifyConfig{Issuer: issuer, Audience: "metro", TrustedKeys: []servicetoken.TrustedKey{{KID: "k1", PublicKeyFile: keys.PublicFile}}},
		})
		h.Metro.SetResources(&metroapp.Resources{
			DB: h.MetroDB,
			Health: health.New(health.Probe{Name: "postgres", Check: func(ctx context.Context) error {
				_, err := h.MetroDB.CatalogState.Query().Exist(ctx)
				return err
			}}),
		})
		h.Metro.SetGRPCTestUnaryInterceptors(metroUnary...)
		bffCfg.Auth.AccountCleanup = []string{"tripmap", "metro"}
		if err := h.Metro.StartGRPC(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Metro.Shutdown() })
		metroHTTP := gin.New()
		h.Metro.Routes(metroHTTP)
		metroSrv := httptest.NewServer(metroHTTP)
		t.Cleanup(metroSrv.Close)
		bffCfg.Upstreams["metro"] = metroSrv.URL
		svc := bffconfig.GRPCService{Address: h.Metro.GRPCAddr(), ServerName: "metro.internal"}
		if transport == "h2c" {
			svc = bffconfig.GRPCService{Address: h.Metro.GRPCAddr(), Transport: "h2c"}
		}
		bffCfg.GRPC.Services["metro"] = svc
	}
	bffRouter := gin.New()
	bffSrv := httptest.NewUnstartedServer(bffRouter)
	scheme := "http"
	if httpOptions.TLS {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://localhost:%d", scheme, bffSrv.Listener.Addr().(*net.TCPAddr).Port)
	if httpOptions.Configure != nil {
		httpOptions.Configure(bffCfg, baseURL)
	}
	h.BFF = bffapp.New(bffCfg)
	h.BFF.SetResources(&bffapp.Resources{DB: bffDB, Redis: bffRedis, KeyPrefix: bffPrefix})
	if err := h.BFF.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.BFF.Shutdown() })

	if httpOptions.Observe != nil {
		bffRouter.Use(func(c *gin.Context) { c.Next(); httpOptions.Observe(c.Request, c.Writer.Header(), c.Writer.Status()) })
	}
	h.BFF.Routes(bffRouter)
	if httpOptions.TLS {
		bffSrv.StartTLS()
	} else {
		bffSrv.Start()
	}
	t.Cleanup(bffSrv.Close)
	h.BFFURL = baseURL
	return h
}

func (h *ChainHarness) DirectTripmapGRPC() *grpc.ClientConn {
	h.T.Helper()
	if h.transport == "h2c" {
		conn, err := grpc.NewClient(h.Tripmap.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			h.T.Fatal(err)
		}
		h.T.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	pem, err := os.ReadFile(h.caFile)
	if err != nil {
		h.T.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	conn, err := grpc.NewClient(h.Tripmap.GRPCAddr(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs: pool, ServerName: h.serverName, MinVersion: tls.VersionTLS13,
	})))
	if err != nil {
		h.T.Fatal(err)
	}
	h.T.Cleanup(func() { _ = conn.Close() })
	return conn
}

// DirectMetroGRPC dials metro's gRPC port, bypassing the BFF, with the BFF's
// credential interceptor so a test decides exactly what the credential carries.
func (h *ChainHarness) DirectMetroGRPC() *grpc.ClientConn {
	h.T.Helper()
	creds := insecure.NewCredentials()
	if h.transport != "h2c" {
		pem, err := os.ReadFile(h.caFile)
		if err != nil {
			h.T.Fatal(err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(pem)
		creds = credentials.NewTLS(&tls.Config{RootCAs: pool, ServerName: "metro.internal", MinVersion: tls.VersionTLS13})
	}
	conn, err := grpc.NewClient(h.Metro.GRPCAddr(), grpc.WithTransportCredentials(creds),
		grpc.WithUnaryInterceptor(rpcauth.UnaryClientInterceptor(h.signer, "metro")))
	if err != nil {
		h.T.Fatal(err)
	}
	h.T.Cleanup(func() { _ = conn.Close() })
	return conn
}

// FreshBrowser returns a new isolated cookie jar for another user/session.
func (h *ChainHarness) FreshBrowser() *Browser {
	h.T.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.T.Fatal(err)
	}
	hc := &http.Client{Jar: jar}
	api := h.BFFURL + "/api"
	return &Browser{
		t: h.T, h: h, HTTP: hc, APIURL: api, Origin: h.BFFURL, Region: h.regionVersion,
		Auth:       bffv1connect.NewAuthServiceClient(hc, api),
		Users:      bffv1connect.NewUserServiceClient(hc, api),
		Tokens:     bffv1connect.NewAccessTokenServiceClient(hc, api),
		Catalog:    bffmetrov1connect.NewCatalogServiceClient(hc, api),
		MetroAdmin: bffmetrov1connect.NewAdminServiceClient(hc, api),
		MetroRider: bffmetrov1connect.NewRiderServiceClient(hc, api),
		Foot:       bfftripmapv1connect.NewFootprintServiceClient(hc, api),
		Snap:       bfftripmapv1connect.NewSnapshotServiceClient(hc, api),
		Friends:    bfftripmapv1connect.NewFriendServiceClient(hc, api),
		SharedTrip: bfftripmapv1connect.NewSharedTripServiceClient(hc, api),
		Itin:       bfftripmapv1connect.NewItineraryServiceClient(hc, api),
		media:      bfftripmapv1connect.NewMediaServiceClient(hc, api),
		Report:     bfftripmapv1connect.NewReportServiceClient(hc, api),
		Search:     bfftripmapv1connect.NewSearchServiceClient(hc, api),
		RegionSvc:  bfftripmapv1connect.NewRegionServiceClient(hc, api),
	}
}

func (b *Browser) Media() bfftripmapv1connect.MediaServiceClient { return b.media }

func (b *Browser) GetRegionIndex(ctx context.Context) (*bfftripmapv1.GetRegionIndexResponse, error) {
	req := connect.NewRequest(&bfftripmapv1.GetRegionIndexRequest{})
	b.withTripmapHeaders(req)
	res, err := b.RegionSvc.GetRegionIndex(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) PreviewRegionUpgrade(ctx context.Context) (*bfftripmapv1.PreviewRegionUpgradeResponse, error) {
	req := connect.NewRequest(&bfftripmapv1.PreviewRegionUpgradeRequest{})
	b.withTripmapHeaders(req)
	res, err := b.RegionSvc.PreviewRegionUpgrade(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) ApplyRegionUpgrade(ctx context.Context, version string, baseRevision int64, mutationID string) (*bfftripmapv1.ApplyRegionUpgradeResponse, error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(&bfftripmapv1.ApplyRegionUpgradeRequest{
		Version: version, BaseRevision: baseRevision, MutationId: mutationID,
	})
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.RegionSvc.ApplyRegionUpgrade(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) Itinerary() bfftripmapv1connect.ItineraryServiceClient { return b.Itin }

func (b *Browser) GetCollaborationOverview(ctx context.Context) (*bfftripmapv1.GetCollaborationOverviewResponse, error) {
	req := connect.NewRequest(&bfftripmapv1.GetCollaborationOverviewRequest{})
	b.withTripmapHeaders(req)
	res, err := b.Friends.GetCollaborationOverview(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) GetFriendFootprints(ctx context.Context, friendUserID string) (*bfftripmapv1.GetFriendFootprintsResponse, error) {
	req := connect.NewRequest(&bfftripmapv1.GetFriendFootprintsRequest{FriendUserId: friendUserID})
	b.withTripmapHeaders(req)
	res, err := b.Friends.GetFriendFootprints(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) ListItineraries(ctx context.Context) (*bfftripmapv1.ListItinerariesResponse, error) {
	req := connect.NewRequest(&bfftripmapv1.ListItinerariesRequest{})
	b.withTripmapHeaders(req)
	res, err := b.Itin.ListItineraries(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) GetItinerary(ctx context.Context, id string) (*bfftripmapv1.GetItineraryResponse, error) {
	req := connect.NewRequest(&bfftripmapv1.GetItineraryRequest{Id: id})
	b.withTripmapHeaders(req)
	res, err := b.Itin.GetItinerary(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) SaveItinerary(ctx context.Context, msg *bfftripmapv1.SaveItineraryRequest) (*bfftripmapv1.SaveItineraryResponse, error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(msg)
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.Itin.SaveItinerary(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) DeleteItinerary(ctx context.Context, msg *bfftripmapv1.DeleteItineraryRequest) (*bfftripmapv1.DeleteItineraryResponse, error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(msg)
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.Itin.DeleteItinerary(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// Write adds the CSRF token and Origin a state-changing browser call carries.
func (b *Browser) Write(ctx context.Context, req connect.AnyRequest) {
	b.writeHeaders(req, b.refreshCSRF(ctx), b.Origin)
}

func (b *Browser) withTripmapHeaders(req connect.AnyRequest) {
	req.Header().Set("X-Tripmap-Schema", "2")
	req.Header().Set("X-Tripmap-Regions", b.Region)
}

func (b *Browser) refreshCSRF(ctx context.Context) string {
	res, err := b.Auth.IssueCSRFToken(ctx, connect.NewRequest(&bffv1.IssueCSRFTokenRequest{}))
	if err != nil {
		b.t.Fatal(err)
	}
	return res.Msg.GetToken()
}

func (b *Browser) writeHeaders(req connect.AnyRequest, csrf string, origin string) {
	b.withTripmapHeaders(req)
	if csrf != "" {
		req.Header().Set(session.CSRFHeader, csrf)
	}
	if origin != "" {
		req.Header().Set("Origin", origin)
	}
}

// RequestRegistration asks for the verification mail of email.
func (b *Browser) RequestRegistration(ctx context.Context, email, appID string) (*bffv1.RequestRegistrationResponse, error) {
	req := connect.NewRequest(&bffv1.RequestRegistrationRequest{Email: email, AppId: appID})
	b.Write(ctx, req)
	res, err := b.Auth.RequestRegistration(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// CompleteRegistration submits the token from the verification mail.
func (b *Browser) CompleteRegistration(ctx context.Context, token, password, display string) error {
	req := connect.NewRequest(&bffv1.CompleteRegistrationRequest{Token: token, Password: password, DisplayName: display})
	b.Write(ctx, req)
	_, err := b.Auth.CompleteRegistration(ctx, req)
	return err
}

// LoginWithEmail signs in with an address and password.
func (b *Browser) LoginWithEmail(ctx context.Context, email, password string) (*bffv1.LoginWithEmailResponse, error) {
	req := connect.NewRequest(&bffv1.LoginWithEmailRequest{Email: email, Password: password})
	b.Write(ctx, req)
	res, err := b.Auth.LoginWithEmail(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// register runs the real registration of subject and returns its user ID after
// the account exists; it does not sign in.
func (b *Browser) register(ctx context.Context, h *ChainHarness, subject, display string) error {
	email := emailFor(subject)
	if _, err := b.RequestRegistration(ctx, email, ""); err != nil {
		return err
	}
	h.DeliverMail()
	if len(h.Inbox.To(email)) == 0 {
		return fmt.Errorf("no verification mail for %s", email)
	}
	return b.CompleteRegistration(ctx, h.Inbox.TokenIn(b.t, email), E2EPassword, display)
}

// Register registers the account of subject (a label, or an address) through the
// real flow (request, mail, verify) and signs in. The unused legacy password
// argument is kept for existing business fixtures; every account's password is
// E2EPassword.
func (b *Browser) Register(ctx context.Context, subject, _, display string) (string, error) {
	if err := b.register(ctx, b.h, subject, display); err != nil {
		return "", err
	}
	if _, err := b.LoginWithEmail(ctx, emailFor(subject), E2EPassword); err != nil {
		return "", err
	}
	b.touchTripmap(ctx)
	me, err := b.Me(ctx)
	if err != nil {
		return "", err
	}
	return me.Id, nil
}

// Login signs in as the account of subject.
func (b *Browser) Login(ctx context.Context, subject, _ string, _ string) error {
	if _, err := b.LoginWithEmail(ctx, emailFor(subject), E2EPassword); err != nil {
		return err
	}
	b.touchTripmap(ctx)
	return nil
}

// touchTripmap makes the first signed-in tripmap call, as a product page does when
// it loads. tripmap's display projection of the account is made on demand by that
// call, so business fixtures that seed tripmap rows for a user need it to have
// happened. A failure (tripmap paused, usersrv down) is the test's business, not
// the sign-in's.
func (b *Browser) touchTripmap(ctx context.Context) {
	req := connect.NewRequest(&bfftripmapv1.GetFootprintsRequest{})
	b.withTripmapHeaders(req)
	_, _ = b.Foot.GetFootprints(ctx, req)
}

// LoginWithRoles registers the account of subject, has the operator grant it the
// given roles (only roles usersrv knows are granted) and signs in, so the session
// starts at the version that includes them.
func (b *Browser) LoginWithRoles(ctx context.Context, subject string, roles ...string) error {
	if err := b.register(ctx, b.h, subject, subject); err != nil {
		return err
	}
	id, err := b.h.Accounts.ResolveEmail(ctx, emailFor(subject))
	if err != nil {
		return err
	}
	for _, role := range roles {
		if role != "metro-admin" && role != "ozid-admin" {
			continue
		}
		if _, err := b.h.Accounts.GrantRole(ctx, usersrvaccount.Operator{Name: "e2e", Reason: "test"}, id, role); err != nil {
			return err
		}
	}
	return b.Login(ctx, subject, "", "")
}

// Reauthenticate re-checks the password on the current session.
func (b *Browser) Reauthenticate(ctx context.Context, password string) error {
	req := connect.NewRequest(&bffv1.ReauthenticateRequest{Password: password})
	b.Write(ctx, req)
	_, err := b.Auth.Reauthenticate(ctx, req)
	return err
}

// ChangePassword changes the password of the signed-in account.
func (b *Browser) ChangePassword(ctx context.Context, current, next string) error {
	req := connect.NewRequest(&bffv1.ChangePasswordRequest{CurrentPassword: current, NewPassword: next})
	b.Write(ctx, req)
	_, err := b.Auth.ChangePassword(ctx, req)
	return err
}

// UpdateMyProfile replaces the nickname and avatar.
func (b *Browser) UpdateMyProfile(ctx context.Context, display, avatar string) (*bffv1.User, error) {
	req := connect.NewRequest(&bffv1.UpdateMyProfileRequest{DisplayName: display, AvatarUrl: avatar})
	b.Write(ctx, req)
	res, err := b.Users.UpdateMyProfile(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg.GetUser(), nil
}

// RequestPasswordReset asks for the reset mail of email.
func (b *Browser) RequestPasswordReset(ctx context.Context, email, appID string) (*bffv1.RequestPasswordResetResponse, error) {
	req := connect.NewRequest(&bffv1.RequestPasswordResetRequest{Email: email, AppId: appID})
	b.Write(ctx, req)
	res, err := b.Auth.RequestPasswordReset(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// ResetPassword submits the token of the reset mail with a new password.
func (b *Browser) ResetPassword(ctx context.Context, token, next string) error {
	req := connect.NewRequest(&bffv1.ResetPasswordRequest{Token: token, NewPassword: next})
	b.Write(ctx, req)
	_, err := b.Auth.ResetPassword(ctx, req)
	return err
}

// MeFull returns the whole Me response.
func (b *Browser) MeFull(ctx context.Context) (*bffv1.MeResponse, error) {
	req := connect.NewRequest(&bffv1.MeRequest{})
	b.withTripmapHeaders(req)
	res, err := b.Auth.Me(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// GetAccountDeletion reads the deletion the session cookie started.
func (b *Browser) GetAccountDeletion(ctx context.Context) (*bffv1.AccountDeletion, error) {
	req := connect.NewRequest(&bffv1.GetAccountDeletionRequest{})
	res, err := b.Auth.GetAccountDeletion(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg.GetDeletion(), nil
}

// RetryAccountDeletion continues the deletion the session cookie started.
func (b *Browser) RetryAccountDeletion(ctx context.Context) (*bffv1.AccountDeletion, error) {
	req := connect.NewRequest(&bffv1.RetryAccountDeletionRequest{})
	b.Write(ctx, req)
	res, err := b.Auth.RetryAccountDeletion(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg.GetDeletion(), nil
}

// SetSessionCookie puts a session token into the jar, as a browser that kept an
// old Cookie would send it.
func (b *Browser) SetSessionCookie(token string) {
	u, _ := url.Parse(b.Origin)
	b.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: session.CookieName, Value: token, Path: "/"}})
}

func (b *Browser) Logout(ctx context.Context) error {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(&bffv1.LogoutRequest{})
	b.writeHeaders(req, csrf, b.Origin)
	_, err := b.Auth.Logout(ctx, req)
	return err
}

func (b *Browser) Me(ctx context.Context) (*bffv1.User, error) {
	req := connect.NewRequest(&bffv1.MeRequest{})
	b.withTripmapHeaders(req)
	res, err := b.Auth.Me(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg.GetUser(), nil
}

func (b *Browser) GetFootprints(ctx context.Context) (*bfftripmapv1.GetFootprintsResponse, error) {
	req := connect.NewRequest(&bfftripmapv1.GetFootprintsRequest{})
	b.withTripmapHeaders(req)
	res, err := b.Foot.GetFootprints(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) SessionCookie() string {
	u, err := url.Parse(b.Origin)
	if err != nil {
		return ""
	}
	for _, c := range b.HTTP.Jar.Cookies(u) {
		if c.Name == session.CookieName {
			return c.Value
		}
	}
	return ""
}

// operator is the identity the harness acts under for the controlled account
// operations that have no browser path.
var operator = usersrvaccount.Operator{Name: "e2e", Reason: "test"}

// ageSession makes the password check of b's current session d old, as if the
// user had signed in that long ago.
func (h *ChainHarness) ageSession(b *Browser, d time.Duration) {
	h.T.Helper()
	n, err := h.BFFDB.Session.Update().Where(bffsession.TokenHash(session.HashToken(b.SessionCookie()))).
		SetAuthenticatedAt(time.Now().UTC().Add(-d)).Save(context.Background())
	if err != nil || n != 1 {
		h.T.Fatalf("age session: %d %v", n, err)
	}
}

// Status reads the anonymous tripmap status through the BFF.
func (b *Browser) Status(ctx context.Context) (*bfftripmapv1.GetStatusResponse, error) {
	res, err := bfftripmapv1connect.NewStatusServiceClient(b.HTTP, b.APIURL).GetStatus(ctx, connect.NewRequest(&bfftripmapv1.GetStatusRequest{}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}
