package application_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/config"
	entuser "github.com/kongken/mono/internal/tripmap/ent/user"
	"github.com/kongken/mono/internal/tripmap/regions"
	"github.com/kongken/mono/internal/tripmap/testutil"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"github.com/kongken/mono/pkg/proto/tripmap/v1/tripmapv1connect"
)

type env struct {
	t         *testing.T
	store     *application.Store
	cfg       *config.AppConfig
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
	reportSvc *application.ReportService
}

type storeSource struct{ st *application.Store }

func (s storeSource) Store() *application.Store { return s.st }

// testRPCAuth maps the bearer token to a user ID for isolated application tests.
// Production tripmap no longer serves Connect; this harness exercises handlers only.
func testRPCAuth() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			call := rpcauth.Call{
				TripmapSchema:  req.Header().Get("X-Tripmap-Schema"),
				TripmapRegions: req.Header().Get("X-Tripmap-Regions"),
			}
			header := req.Header().Get("Authorization")
			token, ok := strings.CutPrefix(header, "Bearer ")
			if ok {
				if id := strings.TrimSpace(token); id != "" {
					call.UserID = id
				}
			}
			ctx = rpcauth.WithCall(ctx, call)
			return next(ctx, req)
		}
	})
}

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
	opts := connect.WithInterceptors(testRPCAuth())
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
	reportSvc := application.NewReportService(src, idx)
	mux := http.NewServeMux()
	mux.Handle(tripmapv1connect.NewNotificationServiceHandler(application.NewNotificationService(src, notifier), opts))
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
		t: t, store: src.st, cfg: cfg, reportSvc: reportSvc,
		notify:   tripmapv1connect.NewNotificationServiceClient(server.Client(), server.URL),
		notifier: notifier, mailer: mailer, pusher: pusher, clock: clock,
		public:    application.NewNotificationPublic(cfg, src, notifier),
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

func bearer[T any](userID string, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	if userID != "" {
		req.Header().Set("Authorization", "Bearer "+userID)
	}
	return req
}

func (e *env) register(username string) string {
	e.t.Helper()
	u, err := e.store.DB.User.Create().SetUsername(username).Save(context.Background())
	if err != nil {
		e.t.Fatalf("register %s: %v", username, err)
	}
	return u.ID
}

func (e *env) cleanupAccount(ctx context.Context, userID string) error {
	e.t.Helper()
	svc := application.NewAccountCleanupService(storeSource{e.store})
	_, err := svc.CleanupUserData(rpcauth.WithCall(ctx, rpcauth.Call{UserID: userID}), &tripmapv1.CleanupUserDataRequest{UserId: userID})
	return err
}

func (e *env) mustCleanupAccount(ctx context.Context, userID string) {
	e.t.Helper()
	if err := e.cleanupAccount(ctx, userID); err != nil {
		e.t.Fatalf("cleanup %s: %v", userID, err)
	}
}

func codeOf(err error) connect.Code { return connect.CodeOf(err) }

func wantCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %v, got success", code)
	}
	if connect.CodeOf(err) != code {
		t.Fatalf("want %v, got %v", code, err)
	}
}

// friendCode returns the invite code of the user registered under the test label
// username. Friends are found only by code, never by name.
func (e *env) friendCode(username string) string {
	e.t.Helper()
	u, err := e.store.DB.User.Query().Where(entuser.Username(username)).Only(context.Background())
	if err != nil {
		e.t.Fatalf("friendCode %s: %v", username, err)
	}
	ov, err := e.collab.GetCollaborationOverview(context.Background(), bearer(u.ID, &tripmapv1.GetCollaborationOverviewRequest{}))
	if err != nil {
		e.t.Fatalf("friendCode %s: %v", username, err)
	}
	return ov.Msg.MyFriendCode
}
