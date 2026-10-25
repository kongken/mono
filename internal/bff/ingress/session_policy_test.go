package ingress_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ingress"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// authProbe is an AuthService that records what reached it.
type authProbe struct {
	bffv1connect.UnimplementedAuthServiceHandler
	reached atomic.Int32
	seen    atomic.Pointer[session.BrowserSession]
}

func (p *authProbe) Me(ctx context.Context, _ *connect.Request[bffv1.MeRequest]) (*connect.Response[bffv1.MeResponse], error) {
	p.reached.Add(1)
	if s, ok := session.FromBrowser(ctx); ok {
		p.seen.Store(&s)
	}
	return connect.NewResponse(&bffv1.MeResponse{}), nil
}

func (p *authProbe) DeleteAccount(ctx context.Context, _ *connect.Request[bffv1.DeleteAccountRequest]) (*connect.Response[bffv1.DeleteAccountResponse], error) {
	p.reached.Add(1)
	if s, ok := session.FromBrowser(ctx); ok {
		p.seen.Store(&s)
	} else {
		p.seen.Store(nil)
	}
	return connect.NewResponse(&bffv1.DeleteAccountResponse{}), nil
}

func authClient(t *testing.T, policy ingress.Policy, probe *authProbe) bffv1connect.AuthServiceClient {
	t.Helper()
	path, handler := bffv1connect.NewAuthServiceHandler(probe,
		connect.WithInterceptors(ingress.Interceptor(policy)), connect.WithRequireConnectProtocolHeader())
	mux := http.NewServeMux()
	mux.Handle(path, ingress.ConnectOnly(handler))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return bffv1connect.NewAuthServiceClient(srv.Client(), srv.URL)
}

func TestSessionCheckFailuresKeepTheirMeaning(t *testing.T) {
	probe := &authProbe{}
	var result atomic.Pointer[error]
	set := func(err error) { result.Store(&err) }
	policy := ingress.Policy{Authenticate: func(context.Context, http.Header) (session.BrowserSession, error) {
		if e := result.Load(); e != nil && *e != nil {
			return session.BrowserSession{}, *e
		}
		return session.BrowserSession{UserID: "u-1", AuthVersion: 3, Roles: []string{"r"}}, nil
	}}
	c := authClient(t, policy, probe)

	for name, tc := range map[string]struct {
		err  error
		want connect.Code
	}{
		"session refused":     {connect.NewError(connect.CodeUnauthenticated, errors.New("no")), connect.CodeUnauthenticated},
		"usersrv unavailable": {connect.NewError(connect.CodeUnavailable, errors.New("down")), connect.CodeUnavailable},
		"internal":            {connect.NewError(connect.CodeInternal, errors.New("boom")), connect.CodeInternal},
		"a plain error":       {errors.New("who knows"), connect.CodeUnauthenticated},
	} {
		set(tc.err)
		_, err := c.Me(context.Background(), connect.NewRequest(&bffv1.MeRequest{}))
		if connect.CodeOf(err) != tc.want {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	if probe.reached.Load() != 0 {
		t.Fatalf("a failed session check forwarded the call %d times", probe.reached.Load())
	}
	set(nil)
	if _, err := c.Me(context.Background(), connect.NewRequest(&bffv1.MeRequest{})); err != nil {
		t.Fatal(err)
	}
	if s := probe.seen.Load(); s == nil || s.UserID != "u-1" || s.AuthVersion != 3 {
		t.Fatalf("handler session: %+v", s)
	}
}

// DeleteAccount is the one protected call a Cookie may make after its session is
// gone, and only when that Cookie started a deletion.
func TestDeleteAccountPassesForACookieThatStartedADeletion(t *testing.T) {
	probe := &authProbe{}
	var known atomic.Bool
	refused := connect.NewError(connect.CodeUnauthenticated, errors.New("no"))
	policy := ingress.Policy{
		Authenticate: func(context.Context, http.Header) (session.BrowserSession, error) {
			return session.BrowserSession{}, refused
		},
		DeletionKnown: func(context.Context, string) bool { return known.Load() },
	}
	c := authClient(t, policy, probe)
	del := func() error {
		req := connect.NewRequest(&bffv1.DeleteAccountRequest{})
		// A write as a browser makes it: CSRF cookie and header, same origin.
		req.Header().Set("Cookie", session.CSRFCookieName+"=c; "+session.CookieName+"=s")
		req.Header().Set(session.CSRFHeader, "c")
		req.Header().Set("Sec-Fetch-Site", "same-origin")
		_, err := c.DeleteAccount(context.Background(), req)
		return err
	}
	if connect.CodeOf(del()) != connect.CodeUnauthenticated || probe.reached.Load() != 0 {
		t.Fatalf("an unknown cookie reached DeleteAccount: %v reached=%d", del(), probe.reached.Load())
	}
	known.Store(true)
	if err := del(); err != nil || probe.reached.Load() != 1 || probe.seen.Load() != nil {
		t.Fatalf("a cookie with a deletion: %v reached=%d", err, probe.reached.Load())
	}
	// No other protected call gets that exemption.
	if _, err := c.Me(context.Background(), connect.NewRequest(&bffv1.MeRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("Me for a deleting cookie: %v", err)
	}
}

func TestProjectionRunsAfterTheSessionCheckWithTheCaller(t *testing.T) {
	probe := &authProbe{}
	var projected atomic.Value
	policy := ingress.Policy{
		Authenticate: func(context.Context, http.Header) (session.BrowserSession, error) {
			return session.BrowserSession{UserID: "u-9", AuthenticatedAt: time.Now()}, nil
		},
		Projection: func(_ context.Context, procedure string, bs session.BrowserSession) error {
			projected.Store(procedure + " " + bs.UserID)
			return connect.NewError(connect.CodeUnavailable, errors.New("projection failed"))
		},
	}
	c := authClient(t, policy, probe)
	_, err := c.Me(context.Background(), connect.NewRequest(&bffv1.MeRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable || probe.reached.Load() != 0 {
		t.Fatalf("a failed projection must stop the call: %v reached=%d", err, probe.reached.Load())
	}
	if projected.Load() != bffv1connect.AuthServiceMeProcedure+" u-9" {
		t.Fatalf("projection saw %v", projected.Load())
	}
}
