// Package e2e runs the real BFF in front of the real tripmap gRPC server over TLS.
//
// ChainHarness starts both services with isolated PostgreSQL schemas and Redis
// key prefixes (requires TRIPMAP_TEST_POSTGRES_DSN and TRIPMAP_TEST_REDIS_ADDR,
// same as make test-integration). Use FreshBrowser for a cookie jar + CSRF-aware
// Connect client against the BFF public URL:
//
//	h := NewChainHarness(t)
//	a := h.FreshBrowser()
//	id, err := a.Register(t.Context(), "alice", "password1", "Alice")
//	b := h.FreshBrowser()
//	_, err = b.Register(t.Context(), "bob", "password1", "Bob")
//
// Later tickets add scenarios by reusing ChainHarness and Browser helpers only.
package e2e_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	bffapp "github.com/kongken/mono/internal/bff/app"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	bffent "github.com/kongken/mono/internal/bff/ent"
	bfftestutil "github.com/kongken/mono/internal/bff/testutil"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/health"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/platform/testkit"
	tripmapapp "github.com/kongken/mono/internal/tripmap/app"
	tripmapconfig "github.com/kongken/mono/internal/tripmap/config"
	tripmapent "github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	"github.com/kongken/mono/internal/tripmap/regions"
	"github.com/kongken/mono/internal/tripmap/testutil"
	"github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// ChainHarness is a live BFF → TLS gRPC → tripmap stack with real auth and business DBs.
type ChainHarness struct {
	T *testing.T

	BFFURL string
	BFF    *bffapp.Handlers
	Tripmap *tripmapapp.Handlers

	BFFDB     *bffent.Client
	TripmapDB *tripmapent.Client

	regionVersion string
}

// Browser is a cookie-aware Connect client for bff.v1 (CSRF + schema headers).
type Browser struct {
	t      *testing.T
	HTTP   *http.Client
	Auth   bffv1connect.AuthServiceClient
	Foot    bffv1connect.FootprintServiceClient
	Friends bffv1connect.FriendServiceClient
	Itin    bffv1connect.ItineraryServiceClient
	media   bffv1connect.MediaServiceClient
	APIURL  string
	Origin string
	Region string
}

// NewChainHarness builds the full stack or skips when storage env is unset.
// Optional extras: *provider.Registry, func(*tripmapconfig.AppConfig), blobstore.Store.
func NewChainHarness(t *testing.T, extras ...any) *ChainHarness {
	t.Helper()
	var oauthReg *provider.Registry
	var mutate []func(*tripmapconfig.AppConfig)
	blob := blobstore.Store(noopBlobs{})
	for _, e := range extras {
		switch v := e.(type) {
		case *provider.Registry:
			oauthReg = v
		case func(*tripmapconfig.AppConfig):
			mutate = append(mutate, v)
		case blobstore.Store:
			if v != nil {
				blob = v
			}
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
	tls := ca.Issue(t, "tripmap.internal")

	tripDB, tripRedis, tripPrefix := testutil.Backing(t)
	bffDB, bffRedis, bffPrefix := bfftestutil.Backing(t)
	h.TripmapDB = tripDB
	h.BFFDB = bffDB

	tripCfg := &tripmapconfig.AppConfig{
		GRPC:         grpcx.ServerConfig{Listen: "127.0.0.1:0", CertFile: tls.CertFile, KeyFile: tls.KeyFile},
		InternalAuth: servicetoken.VerifyConfig{Issuer: issuer, Audience: audience, TrustedKeys: []servicetoken.TrustedKey{{KID: "k1", PublicKeyFile: keys.PublicFile}}},
	}
	for _, m := range mutate {
		m(tripCfg)
	}
	h.Tripmap = tripmapapp.New(tripCfg)
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

	bffCfg := &bffconfig.Config{
		Upstreams: map[string]string{"tripmap": tripSrv.URL},
		InternalAuth: bffconfig.InternalAuth{Issuer: issuer, KID: "k1", PrivateKeyFile: keys.PrivateFile},
		GRPC: bffconfig.GRPCConfig{CAFile: ca.CAFile, Services: map[string]bffconfig.GRPCService{
			"tripmap": {Address: h.Tripmap.GRPCAddr(), ServerName: "tripmap.internal"},
		}},
		Auth: bffconfig.AuthConfig{CookieSecure: false},
	}
	h.BFF = bffapp.New(bffCfg)
	if oauthReg != nil {
		h.BFF.SetOAuthRegistry(oauthReg)
	}
	h.BFF.SetResources(&bffapp.Resources{DB: bffDB, Redis: bffRedis, KeyPrefix: bffPrefix})
	if err := h.BFF.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.BFF.Shutdown() })

	bffRouter := gin.New()
	h.BFF.Routes(bffRouter)
	bffSrv := httptest.NewServer(bffRouter)
	t.Cleanup(bffSrv.Close)
	h.BFFURL = bffSrv.URL
	return h
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
		t: h.T, HTTP: hc, APIURL: api, Origin: h.BFFURL, Region: h.regionVersion,
		Auth: bffv1connect.NewAuthServiceClient(hc, api),
		Foot:    bffv1connect.NewFootprintServiceClient(hc, api),
		Friends: bffv1connect.NewFriendServiceClient(hc, api),
		Itin:    bffv1connect.NewItineraryServiceClient(hc, api),
		media:   bffv1connect.NewMediaServiceClient(hc, api),
	}
}

func (b *Browser) Media() bffv1connect.MediaServiceClient { return b.media }

func (b *Browser) Itinerary() bffv1connect.ItineraryServiceClient { return b.Itin }

func (b *Browser) GetCollaborationOverview(ctx context.Context) (*bffv1.GetCollaborationOverviewResponse, error) {
	req := connect.NewRequest(&bffv1.GetCollaborationOverviewRequest{})
	b.withTripmapHeaders(req)
	res, err := b.Friends.GetCollaborationOverview(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) GetFriendFootprints(ctx context.Context, friendUserID string) (*bffv1.GetFriendFootprintsResponse, error) {
	req := connect.NewRequest(&bffv1.GetFriendFootprintsRequest{FriendUserId: friendUserID})
	b.withTripmapHeaders(req)
	res, err := b.Friends.GetFriendFootprints(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) ListItineraries(ctx context.Context) (*bffv1.ListItinerariesResponse, error) {
	req := connect.NewRequest(&bffv1.ListItinerariesRequest{})
	b.withTripmapHeaders(req)
	res, err := b.Itin.ListItineraries(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) GetItinerary(ctx context.Context, id string) (*bffv1.GetItineraryResponse, error) {
	req := connect.NewRequest(&bffv1.GetItineraryRequest{Id: id})
	b.withTripmapHeaders(req)
	res, err := b.Itin.GetItinerary(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) SaveItinerary(ctx context.Context, msg *bffv1.SaveItineraryRequest) (*bffv1.SaveItineraryResponse, error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(msg)
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.Itin.SaveItinerary(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) DeleteItinerary(ctx context.Context, msg *bffv1.DeleteItineraryRequest) (*bffv1.DeleteItineraryResponse, error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(msg)
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.Itin.DeleteItinerary(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
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

// Register creates a password account and leaves the session cookie in the jar.
func (b *Browser) Register(ctx context.Context, username, password, display string) (userID string, err error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(&bffv1.RegisterRequest{Username: username, Password: password, DisplayName: display})
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.Auth.Register(ctx, req)
	if err != nil {
		return "", err
	}
	return res.Msg.GetUser().GetId(), nil
}

func (b *Browser) Login(ctx context.Context, username, password string, origin string) error {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(&bffv1.LoginRequest{Username: username, Password: password})
	b.writeHeaders(req, csrf, origin)
	_, err := b.Auth.Login(ctx, req)
	return err
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

func (b *Browser) GetFootprints(ctx context.Context) (*bffv1.GetFootprintsResponse, error) {
	req := connect.NewRequest(&bffv1.GetFootprintsRequest{})
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
