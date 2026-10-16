package application_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/tripmap/application"
	"go.orx.me/apps/trip-map/internal/tripmap/auth/provider"
	"go.orx.me/apps/trip-map/internal/tripmap/config"
	"go.orx.me/apps/trip-map/internal/tripmap/regions"
	"go.orx.me/apps/trip-map/internal/tripmap/testutil"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
	"go.orx.me/apps/trip-map/pkg/proto/tripmap/v1/tripmapv1connect"
)

type env struct {
	t         *testing.T
	store     *application.Store
	cfg       *config.AppConfig
	auth      tripmapv1connect.AuthServiceClient
	foot      tripmapv1connect.FootprintServiceClient
	snap      tripmapv1connect.SnapshotServiceClient
	media     tripmapv1connect.MediaServiceClient
	collab    tripmapv1connect.CollaborationServiceClient
	report    tripmapv1connect.ReportServiceClient
	search    tripmapv1connect.SearchServiceClient
	region    tripmapv1connect.RegionServiceClient
	itinerary tripmapv1connect.ItineraryServiceClient
	notify    tripmapv1connect.NotificationServiceClient
	notifier  *application.Notifier
	mailer    *fakeMailer
	pusher    *fakePusher
	public    *application.NotificationPublic
	clock     *fakeClock
	svc       *application.AuthService
	reportSvc *application.ReportService
}

type storeSource struct{ st *application.Store }

func (s storeSource) Store() *application.Store { return s.st }

// newEnv serves the real handlers and interceptor over HTTP, backed by an
// isolated PostgreSQL schema and Redis prefix.
func newEnv(t *testing.T, mutate ...func(*config.AppConfig)) *env {
	t.Helper()
	db, rdb, prefix := testutil.Backing(t)
	cfg := &config.AppConfig{}
	for _, m := range mutate {
		m(cfg)
	}
	src := storeSource{&application.Store{DB: db, Redis: rdb, KeyPrefix: prefix}}
	idx, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}
	authSvc := application.NewAuthService(cfg, src)
	reportSvc := application.NewReportService(src, idx)
	opts := connect.WithInterceptors(application.NewAuthInterceptor(src))
	mailer, pusher, clock := &fakeMailer{}, &fakePusher{}, &fakeClock{}
	clock.set(time.Date(2026, 10, 15, 2, 0, 0, 0, time.UTC))
	notifier := &application.Notifier{
		Config: config.NotificationConfig{
			PublicBaseURL: "https://trip.example.com",
			Secret:        strings.Repeat("s", 32),
			SMTP:          config.SMTPConfig{Host: "smtp.test", From: "山海足迹 <noreply@example.com>"},
			WebPush:       config.WebPushConfig{VAPIDPublicKey: "vapid-public", VAPIDPrivateKey: "vapid-private", Subject: "mailto:ops@example.com"},
		},
		Mail: mailer, Push: pusher, Now: clock.now,
	}
	mux := http.NewServeMux()
	mux.Handle(tripmapv1connect.NewNotificationServiceHandler(application.NewNotificationService(src, notifier), opts))
	mux.Handle(tripmapv1connect.NewAuthServiceHandler(authSvc, opts))
	mux.Handle(tripmapv1connect.NewFootprintServiceHandler(application.NewFootprintService(cfg, src, idx), opts))
	mux.Handle(tripmapv1connect.NewSnapshotServiceHandler(application.NewSnapshotService(cfg, src, idx), opts))
	mux.Handle(tripmapv1connect.NewMediaServiceHandler(application.NewMediaService(src, idx), opts))
	mux.Handle(tripmapv1connect.NewCollaborationServiceHandler(application.NewCollaborationService(src, idx), opts))
	mux.Handle(tripmapv1connect.NewReportServiceHandler(reportSvc, opts))
	mux.Handle(tripmapv1connect.NewSearchServiceHandler(application.NewSearchService(src, idx), opts))
	mux.Handle(tripmapv1connect.NewRegionServiceHandler(application.NewRegionService(src, idx), opts))
	mux.Handle(tripmapv1connect.NewItineraryServiceHandler(application.NewItineraryService(src, idx), opts))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &env{
		t: t, store: src.st, cfg: cfg, svc: authSvc, reportSvc: reportSvc,
		notify:   tripmapv1connect.NewNotificationServiceClient(server.Client(), server.URL),
		notifier: notifier, mailer: mailer, pusher: pusher, clock: clock,
		public: application.NewNotificationPublic(cfg, src, notifier),
		auth:      tripmapv1connect.NewAuthServiceClient(server.Client(), server.URL),
		foot:      tripmapv1connect.NewFootprintServiceClient(server.Client(), server.URL),
		snap:      tripmapv1connect.NewSnapshotServiceClient(server.Client(), server.URL),
		media:     tripmapv1connect.NewMediaServiceClient(server.Client(), server.URL),
		collab:    tripmapv1connect.NewCollaborationServiceClient(server.Client(), server.URL),
		report:    tripmapv1connect.NewReportServiceClient(server.Client(), server.URL),
		search:    tripmapv1connect.NewSearchServiceClient(server.Client(), server.URL),
		region:    tripmapv1connect.NewRegionServiceClient(server.Client(), server.URL),
		itinerary: tripmapv1connect.NewItineraryServiceClient(server.Client(), server.URL),
	}
}

// bearer attaches a session token to a request.
func bearer[T any](token string, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	if token != "" {
		req.Header().Set("Authorization", "Bearer "+token)
	}
	return req
}

func (e *env) register(username string) string {
	e.t.Helper()
	res, err := e.auth.Register(context.Background(), connect.NewRequest(&tripmapv1.RegisterRequest{Username: username, Password: "correct horse battery"}))
	if err != nil {
		e.t.Fatalf("register %s: %v", username, err)
	}
	return res.Msg.Token
}

func codeOf(err error) connect.Code {
	if err == nil {
		return 0
	}
	return connect.CodeOf(err)
}

func wantCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if got := codeOf(err); got != want {
		t.Fatalf("code = %v (%v), want %v", got, err, want)
	}
}

// fakeProvider is an OAuth provider whose Exchange returns canned claims.
type fakeProvider struct {
	name   string
	claims *provider.Claims
}

func (f *fakeProvider) Name() string        { return f.name }
func (f *fakeProvider) DisplayName() string { return strings.ToUpper(f.name) }
func (f *fakeProvider) AuthorizeURL(state string) string {
	return "https://provider.example/authorize?state=" + state
}
func (f *fakeProvider) Exchange(_ context.Context, code string) (*provider.Claims, error) {
	if code != "good-code" {
		return nil, context.DeadlineExceeded
	}
	return f.claims, nil
}
