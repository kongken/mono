package ingress_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/adminguard"
	"github.com/kongken/mono/internal/bff/ingress"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

type directoryProbe struct {
	bffv1connect.UnimplementedUserAdminServiceHandler
	reached atomic.Int32
}

func (p *directoryProbe) ListUsers(context.Context, *connect.Request[bffv1.ListUsersRequest]) (*connect.Response[bffv1.ListUsersResponse], error) {
	p.reached.Add(1)
	return connect.NewResponse(&bffv1.ListUsersResponse{}), nil
}

// The perimeter is the interceptor's own check, independent of the HTTP edge in front of it: a call
// that reaches the handler without having passed the edge is still refused before the session
// lookup, the role check and the handler.
func TestPerimeterRunsBeforeSessionAndRoleAndHandler(t *testing.T) {
	const origin = "https://admin.example.com"
	guard, err := adminguard.New(adminguard.Config{AllowedCIDRs: []string{"127.0.0.0/8"}}, origin)
	if err != nil {
		t.Fatal(err)
	}
	var lookups atomic.Int32
	roles := []string{"ozid-admin"}
	policy := ingress.Policy{
		Perimeter: func(procedure string, h http.Header, remote string) error {
			if strings.HasPrefix(procedure, "/"+bffv1connect.UserAdminServiceName+"/") {
				return guard.Check(h, remote)
			}
			return nil
		},
		AdminSessionMaxAge: time.Hour,
		Authenticate: func(context.Context, http.Header) (session.BrowserSession, error) {
			lookups.Add(1)
			return session.BrowserSession{UserID: "u-1", Roles: roles, AuthenticatedAt: time.Now()}, nil
		},
	}
	probe := &directoryProbe{}
	path, handler := bffv1connect.NewUserAdminServiceHandler(probe, connect.WithInterceptors(ingress.Interceptor(policy)), connect.WithRequireConnectProtocolHeader())
	mux := http.NewServeMux()
	mux.Handle(path, ingress.ConnectOnly(handler))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := bffv1connect.NewUserAdminServiceClient(srv.Client(), srv.URL)
	call := func(origin string) error {
		req := connect.NewRequest(&bffv1.ListUsersRequest{})
		if origin != "" {
			req.Header().Set("Origin", origin)
		}
		_, err := c.ListUsers(context.Background(), req)
		return err
	}

	for _, bad := range []string{"", "https://app.example.com", origin + "/", "null"} {
		err := call(bad)
		if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "request rejected") {
			t.Errorf("origin %q: %v", bad, err)
		}
	}
	if lookups.Load() != 0 || probe.reached.Load() != 0 {
		t.Fatalf("a refused call looked up a session %d times and reached the handler %d times", lookups.Load(), probe.reached.Load())
	}
	if err := call(origin); err != nil {
		t.Fatalf("admitted call: %v", err)
	}
	// Past the perimeter the role is still required, by the same interceptor.
	roles = []string{"metro-admin"}
	if err := call(origin); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("metro-admin: %v", err)
	}
	if probe.reached.Load() != 1 {
		t.Fatalf("handler reached %d times", probe.reached.Load())
	}
	// The same guard does not touch an ordinary procedure.
	probeAuth := &authProbe{}
	authPolicy := policy
	authPolicy.Authenticate = func(context.Context, http.Header) (session.BrowserSession, error) {
		return session.BrowserSession{UserID: "u-1"}, nil
	}
	if _, err := authClient(t, authPolicy, probeAuth).Me(context.Background(), connect.NewRequest(&bffv1.MeRequest{})); err != nil {
		t.Fatalf("an ordinary procedure behind the same policy: %v", err)
	}
}

func TestUserAdminReadsNeedNoCSRFButAreNotAnonymous(t *testing.T) {
	for _, p := range []string{bffv1connect.UserAdminServiceListUsersProcedure, bffv1connect.UserAdminServiceGetUserProcedure} {
		if ingress.RequiresWriteProtection(p) {
			t.Errorf("%s is a read and must not demand a CSRF token", p)
		}
	}
}
