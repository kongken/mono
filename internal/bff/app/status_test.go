package app_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/bff/app"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/platform/testkit"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	bfftripmapv1connect "github.com/kongken/mono/pkg/proto/bff/tripmap/v1/bfftripmapv1connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// seenCall is what the fake tripmap observed about one gRPC call.
type seenCall struct {
	call     rpcauth.Call
	deadline time.Duration
}

// fakeTripmap is a tripmap gRPC server behind the real credential interceptor, so
// the BFF's calls are verified exactly as a service would verify them.
type fakeTripmap struct {
	tripmapv1.UnimplementedSystemServiceServer
	srv      *grpcx.Server
	mu       sync.Mutex
	calls    []seenCall
	count    atomic.Int32
	canceled chan struct{}
	// reply decides the answer; it may block until ctx ends.
	reply func(context.Context) (*tripmapv1.GetStatusResponse, error)
}

func (f *fakeTripmap) GetStatus(ctx context.Context, _ *tripmapv1.GetStatusRequest) (*tripmapv1.GetStatusResponse, error) {
	f.count.Add(1)
	seen := seenCall{call: rpcauth.CallFrom(ctx)}
	if d, ok := ctx.Deadline(); ok {
		seen.deadline = time.Until(d)
	}
	f.mu.Lock()
	f.calls = append(f.calls, seen)
	f.mu.Unlock()
	res, err := f.reply(ctx)
	if ctx.Err() != nil {
		select {
		case f.canceled <- struct{}{}:
		default:
		}
	}
	return res, err
}

func (f *fakeTripmap) last() seenCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

type stack struct {
	bff    *httptest.Server
	tripm  *fakeTripmap
	trip   *fake // the HTTP side of tripmap, which must not see bff.v1 traffic
	client bfftripmapv1connect.StatusServiceClient
}

var readyReply = func(context.Context) (*tripmapv1.GetStatusResponse, error) {
	return &tripmapv1.GetStatusResponse{
		Ready: true, FootprintSchema: 2, ShareEnabled: true, RegionVersion: "region-digest",
		Dependencies: []*tripmapv1.DependencyStatus{{Name: "postgres", Ready: true}, {Name: "redis", Ready: false}},
	}, nil
}

func newStack(t *testing.T, mutate ...func(*config.Config)) *stack {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keys, ca := testkit.NewKeys(t), testkit.NewCA(t)
	server := ca.Issue(t)
	verifier := &servicetoken.Verifier{Issuer: "bff", Audience: "tripmap", Keys: map[string]ed25519.PublicKey{"k1": keys.Private.Public().(ed25519.PublicKey)}}
	f := &fakeTripmap{reply: readyReply, canceled: make(chan struct{}, 1)}
	srv, err := grpcx.Start(grpcx.ServerConfig{Listen: "127.0.0.1:0", CertFile: server.CertFile, KeyFile: server.KeyFile},
		func(r grpc.ServiceRegistrar) { tripmapv1.RegisterSystemServiceServer(r, f) },
		[]grpc.UnaryServerInterceptor{rpcauth.UnaryServerInterceptor(verifier)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.srv = srv
	t.Cleanup(func() { srv.Shutdown(time.Second) })
	trip := newFake(t, "tripmap")
	cfg := &config.Config{
		Upstreams:    map[string]string{"usersrv": "http://127.0.0.1:1", "tripmap": trip.URL},
		InternalAuth: config.InternalAuth{Issuer: "bff", KID: "k1", PrivateKeyFile: keys.PrivateFile},
		GRPC:         config.GRPCConfig{CAFile: ca.CAFile, Services: map[string]config.GRPCService{"usersrv": {Address: "127.0.0.1:1"}, "tripmap": {Address: srv.Addr()}}},
	}
	for _, m := range mutate {
		m(cfg)
	}
	withAccountRegistry(cfg)
	h := app.New(cfg)
	if err := h.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Shutdown() })
	r := gin.New()
	h.Routes(r)
	bff := httptest.NewServer(r)
	t.Cleanup(bff.Close)
	return &stack{bff: bff, tripm: f, trip: trip, client: bfftripmapv1connect.NewStatusServiceClient(bff.Client(), bff.URL+"/api")}
}

func TestAnonymousStatusReachesTripmapOverVerifiedGRPC(t *testing.T) {
	s := newStack(t, func(c *config.Config) { c.ClientIPHeader = "X-Real-IP" })
	req := connect.NewRequest(&bfftripmapv1.GetStatusRequest{})
	req.Header().Set("X-Request-Id", "browser-req-1")
	req.Header().Set("X-Real-IP", "203.0.113.7")
	req.Header().Set(rpcauth.MetadataClientIP, "198.51.100.9") // not a thing the BFF reads
	req.Header().Set("Authorization", "Bearer browser-supplied")
	res, err := s.client.GetStatus(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := res.Msg
	if !got.Ready || got.FootprintSchema != 2 || !got.ShareEnabled || got.RegionVersion != "region-digest" ||
		len(got.Dependencies) != 2 || got.Dependencies[0].Name != "postgres" || !got.Dependencies[0].Ready || got.Dependencies[1].Name != "redis" || got.Dependencies[1].Ready {
		t.Fatalf("response not carried over: %v", got)
	}
	if res.Header().Get("X-Request-Id") != "browser-req-1" {
		t.Errorf("response request ID = %q", res.Header().Get("X-Request-Id"))
	}
	seen := s.tripm.last()
	if seen.call.UserID != "" || seen.call.RequestID != "browser-req-1" || seen.call.ClientIP != "203.0.113.7" || len(seen.call.TraceParent) != 55 {
		t.Fatalf("what tripmap saw: %+v", seen.call)
	}
	if seen, _ := s.trip.seen(); seen != nil {
		t.Fatal("the status query also reached tripmap's HTTP port")
	}
}

func TestStatusCallLifetime(t *testing.T) {
	s := newStack(t)
	if _, err := s.client.GetStatus(context.Background(), connect.NewRequest(&bfftripmapv1.GetStatusRequest{})); err != nil {
		t.Fatal(err)
	}
	if d := s.tripm.last().deadline; d < 8*time.Second || d > 10*time.Second {
		t.Errorf("deadline passed to tripmap = %v, want about 10s", d)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := s.client.GetStatus(ctx, connect.NewRequest(&bfftripmapv1.GetStatusRequest{})); err != nil {
		t.Fatal(err)
	}
	if d := s.tripm.last().deadline; d < time.Second || d > 3*time.Second {
		t.Errorf("a shorter client deadline became %v at tripmap, want at most 3s", d)
	}
}

func TestClientCancellationStopsTheServiceWork(t *testing.T) {
	s := newStack(t)
	started := make(chan struct{})
	s.tripm.reply = func(ctx context.Context) (*tripmapv1.GetStatusResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.client.GetStatus(ctx, connect.NewRequest(&bfftripmapv1.GetStatusRequest{}))
		done <- err
	}()
	<-started
	cancel()
	select {
	case <-s.tripm.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("tripmap kept working after the client gave up")
	}
	if err := <-done; connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("client error = %v", err)
	}
}

func TestFailuresAreNotRetriedAndDoNotLeakInternals(t *testing.T) {
	s := newStack(t)
	for name, tc := range map[string]struct {
		err  error
		want connect.Code
	}{
		"unavailable": {status.Error(codes.Unavailable, "postgres at 10.0.0.5 refused"), connect.CodeUnavailable},
		"internal":    {status.Error(codes.Internal, "pq: password authentication failed for user tripmap"), connect.CodeInternal},
		// The BFF's own credential being refused is a server fault, not the user's sign-in.
		"credential refused": {status.Error(codes.Unauthenticated, "invalid or missing service credential"), connect.CodeInternal},
	} {
		s.tripm.reply = func(context.Context) (*tripmapv1.GetStatusResponse, error) { return nil, tc.err }
		before := s.tripm.count.Load()
		_, err := s.client.GetStatus(context.Background(), connect.NewRequest(&bfftripmapv1.GetStatusRequest{}))
		if connect.CodeOf(err) != tc.want {
			t.Errorf("%s: code %v, want %v (%v)", name, connect.CodeOf(err), tc.want, err)
		}
		for _, leak := range []string{"10.0.0.5", "pq:", "password", "credential"} {
			if err != nil && strings.Contains(err.Error(), leak) {
				t.Errorf("%s: error %q leaks %q", name, err, leak)
			}
		}
		if calls := s.tripm.count.Load() - before; calls != 1 {
			t.Errorf("%s: tripmap was called %d times, want 1 (no automatic retry)", name, calls)
		}
	}

	// With the service gone the browser still gets a clean error.
	s.tripm.srv.Shutdown(time.Second)
	_, err := s.client.GetStatus(context.Background(), connect.NewRequest(&bfftripmapv1.GetStatusRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("service down: %v", err)
	}
}

func TestPublicEntryIsConnectOnlyAndUnknownPathsAreNotForwarded(t *testing.T) {
	s := newStack(t, func(c *config.Config) { c.CORSAllowedOrigins = []string{"https://trip.example.com"} })
	call := func(method, path, contentType string, headers ...string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, s.bff.URL+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		res, err := s.bff.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res
	}
	const status = "/api/bff.tripmap.v1.StatusService/GetStatus"
	if got := call(http.MethodPost, status, "application/json", "Connect-Protocol-Version", "1"); got.StatusCode != 200 {
		t.Fatalf("valid Connect call: %d", got.StatusCode)
	}
	for name, tc := range map[string]struct {
		path, contentType string
		headers           []string
		want              int
	}{
		"missing version":       {status, "application/json", nil, 400},
		"grpc":                  {status, "application/grpc", []string{"Connect-Protocol-Version", "1"}, 415},
		"grpc-web":              {status, "application/grpc-web+proto", nil, 415},
		"unknown method":        {"/api/bff.tripmap.v1.StatusService/Other", "application/json", []string{"Connect-Protocol-Version", "1"}, 404},
		"extra segment":         {status + "/x", "application/json", []string{"Connect-Protocol-Version", "1"}, 404},
		"unknown service":       {"/api/bff.v1.Other/GetStatus", "application/json", []string{"Connect-Protocol-Version", "1"}, 404},
		"dot segments":          {"/api/bff.tripmap.v1.StatusService/../tripmap.v1.AuthService/Login", "application/json", []string{"Connect-Protocol-Version", "1"}, 404},
		"double slash":          {"/api//bff.tripmap.v1.StatusService/GetStatus", "application/json", []string{"Connect-Protocol-Version", "1"}, 404},
		"trailing double slash": {"/api/bff.tripmap.v1.StatusService//GetStatus", "application/json", []string{"Connect-Protocol-Version", "1"}, 404},
	} {
		if got := call(http.MethodPost, tc.path, tc.contentType, tc.headers...); got.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", name, got.StatusCode, tc.want)
		}
	}
	if seen, _ := s.trip.seen(); seen != nil {
		t.Fatalf("a bff.v1 request was forwarded to tripmap: %s", seen.URL)
	}
	// The BFF owns CORS for its own procedures too.
	preflight := call(http.MethodOptions, status, "", "Origin", "https://trip.example.com", "Access-Control-Request-Method", "POST")
	if preflight.StatusCode != http.StatusNoContent || preflight.Header.Get("Access-Control-Allow-Origin") != "https://trip.example.com" {
		t.Fatalf("preflight: %d %v", preflight.StatusCode, preflight.Header)
	}
	if got := call(http.MethodPost, status, "application/json", "Origin", "https://evil.example.com", "Connect-Protocol-Version", "1"); got.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unlisted origin was allowed")
	}
}

func TestUnreachableTripmapIsAnErrorNotAFakeStatus(t *testing.T) {
	s := newStack(t)
	s.tripm.srv.Shutdown(time.Second)
	_, err := s.client.GetStatus(context.Background(), connect.NewRequest(&bfftripmapv1.GetStatusRequest{}))
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeUnavailable {
		t.Fatalf("got %v", err)
	}
}
