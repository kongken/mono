package ingress_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ingress"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// A browser may repeat a write once only when the BFF says the refusal was the CSRF double
// submit: that is the one refusal another tab can cause (it replaced the shared token) and the
// one a fresh token repairs. Every other refusal, with the very same status and message, must
// not carry the marker, so a client that retries on it repeats nothing else.
func TestOnlyACsrfMismatchCarriesTheRepeatMarker(t *testing.T) {
	const origin = "https://app.example.com"
	var authenticated int
	var authErr error
	refusePerimeter := false
	policy := ingress.Policy{
		AllowedOrigins: []string{origin},
		Perimeter: func(string, http.Header, string) error {
			if refusePerimeter {
				return errors.New("outside the perimeter")
			}
			return nil
		},
		Authenticate: func(context.Context, http.Header) (session.BrowserSession, error) {
			authenticated++
			return session.BrowserSession{UserID: "u-1"}, authErr
		},
	}
	probe := &authProbe{}
	path, handler := bffv1connect.NewAuthServiceHandler(probe,
		connect.WithInterceptors(ingress.Interceptor(policy)), connect.WithRequireConnectProtocolHeader())
	mux := http.NewServeMux()
	mux.Handle(path, ingress.ConnectOnly(handler))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	send := func(t *testing.T, csrfHeader, csrfCookie, from string) (code connect.Code, message, marker string, err error) {
		t.Helper()
		req := connect.NewRequest(&bffv1.DeleteAccountRequest{})
		if csrfHeader != "" {
			req.Header().Set(session.CSRFHeader, csrfHeader)
		}
		if csrfCookie != "" {
			req.Header().Set("Cookie", session.CSRFCookieName+"="+csrfCookie)
		}
		if from != "" {
			req.Header().Set("Origin", from)
		}
		_, err = bffv1connect.NewAuthServiceClient(srv.Client(), srv.URL).DeleteAccount(context.Background(), req)
		var ce *connect.Error
		if !errors.As(err, &ce) {
			return 0, "", "", err
		}
		return ce.Code(), ce.Message(), ce.Meta().Get(ingress.RejectionHeader), err
	}

	t.Run("a token that does not match the Cookie is marked", func(t *testing.T) {
		code, msg, marker, _ := send(t, "old", "new", origin)
		if code != connect.CodePermissionDenied || msg != "request rejected" || marker != ingress.RejectionCSRF {
			t.Fatalf("code=%v message=%q marker=%q", code, msg, marker)
		}
	})
	t.Run("a missing token is marked too", func(t *testing.T) {
		if _, _, marker, _ := send(t, "", "", origin); marker != ingress.RejectionCSRF {
			t.Fatalf("marker=%q", marker)
		}
	})
	t.Run("a bad Origin with a valid token is the same refusal without the marker", func(t *testing.T) {
		for _, from := range []string{"https://evil.example.net", "null"} {
			code, msg, marker, _ := send(t, "t", "t", from)
			if code != connect.CodePermissionDenied || msg != "request rejected" || marker != "" {
				t.Fatalf("origin %q: code=%v message=%q marker=%q", from, code, msg, marker)
			}
		}
	})
	t.Run("the perimeter refuses without the marker, before anything else is looked at", func(t *testing.T) {
		refusePerimeter = true
		defer func() { refusePerimeter = false }()
		before := authenticated
		code, msg, marker, _ := send(t, "old", "new", origin) // a stale token as well: the perimeter still comes first
		if code != connect.CodePermissionDenied || msg != "request rejected" || marker != "" {
			t.Fatalf("code=%v message=%q marker=%q", code, msg, marker)
		}
		if authenticated != before {
			t.Fatal("a refused perimeter looked up a session")
		}
	})
	t.Run("a refused session is unauthenticated and unmarked", func(t *testing.T) {
		authErr = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))
		defer func() { authErr = nil }()
		code, _, marker, _ := send(t, "t", "t", origin)
		if code != connect.CodeUnauthenticated || marker != "" {
			t.Fatalf("code=%v marker=%q", code, marker)
		}
	})
	t.Run("the stale-tab guard is unauthenticated and unmarked", func(t *testing.T) {
		req := connect.NewRequest(&bffv1.DeleteAccountRequest{})
		req.Header().Set(session.CSRFHeader, "t")
		req.Header().Set("Cookie", session.CSRFCookieName+"=t")
		req.Header().Set("Origin", origin)
		req.Header().Set("X-Kongken-User", "u-other")
		_, err := bffv1connect.NewAuthServiceClient(srv.Client(), srv.URL).DeleteAccount(context.Background(), req)
		var ce *connect.Error
		if !errors.As(err, &ce) || ce.Code() != connect.CodeUnauthenticated || ce.Meta().Get(ingress.RejectionHeader) != "" {
			t.Fatalf("%v", err)
		}
	})
	t.Run("a valid write goes through and carries no marker", func(t *testing.T) {
		before := probe.reached.Load()
		if _, _, _, err := send(t, "t", "t", origin); err != nil {
			t.Fatal(err)
		}
		if probe.reached.Load() != before+1 {
			t.Fatal("the handler was not reached")
		}
	})
	t.Run("the marker of one refusal never leaks into the next", func(t *testing.T) {
		// The interceptor sets the request ID on the error it returns. A shared error value would
		// keep the marker after a CSRF refusal and show it on an Origin refusal that follows.
		_, _, first, _ := send(t, "old", "new", origin)
		_, _, second, _ := send(t, "t", "t", "https://evil.example.net")
		if first != ingress.RejectionCSRF || second != "" {
			t.Fatalf("first=%q second=%q", first, second)
		}
	})
}

// A role the caller lacks is a permission refusal like the others, and is never a reason to repeat.
func TestARoleRefusalCarriesNoRepeatMarker(t *testing.T) {
	roles := []string{"metro-admin"}
	policy := ingress.Policy{
		AdminSessionMaxAge: time.Hour,
		Authenticate: func(context.Context, http.Header) (session.BrowserSession, error) {
			return session.BrowserSession{UserID: "u-1", Roles: roles, AuthenticatedAt: time.Now()}, nil
		},
	}
	path, handler := bffv1connect.NewUserAdminServiceHandler(&directoryProbe{}, connect.WithInterceptors(ingress.Interceptor(policy)), connect.WithRequireConnectProtocolHeader())
	mux := http.NewServeMux()
	mux.Handle(path, ingress.ConnectOnly(handler))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_, err := bffv1connect.NewUserAdminServiceClient(srv.Client(), srv.URL).ListUsers(context.Background(), connect.NewRequest(&bffv1.ListUsersRequest{}))
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodePermissionDenied || !strings.Contains(ce.Message(), "role") {
		t.Fatalf("%v", err)
	}
	if got := ce.Meta().Get(ingress.RejectionHeader); got != "" {
		t.Fatalf("a role refusal carries the marker %q", got)
	}
}
