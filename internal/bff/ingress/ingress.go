// Package ingress is the BFF's public Connect edge: it accepts only Connect
// unary calls, applies the anonymous policy, bounds every call's lifetime, and
// describes the caller to the internal credential layer.
package ingress

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// ConnectOnly lets only Connect unary requests through. connect-go's handler also
// speaks gRPC and gRPC-Web on the same path; the public API is Connect alone, so
// those content types and streaming frames are refused before the handler sees
// them. The Connect-Protocol-Version requirement is enforced by the handler
// itself (connect.WithRequireConnectProtocolHeader).
func ConnectOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || (media != "application/json" && media != "application/proto") {
			w.Header().Set("Accept-Post", "application/json, application/proto")
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const (
	// DefaultTimeout bounds an ordinary call.
	DefaultTimeout = 10 * time.Second
	// HeavyTimeout bounds uploads and other procedures registered in Policy.Heavy.
	HeavyTimeout = 25 * time.Second
)

// Policy configures the interceptor.
type Policy struct {
	// AllowedOrigins lists browser Origins permitted on write RPCs (see session.CheckWrite).
	AllowedOrigins []string
	// Heavy lists the procedures (full names, such as /tripmap.v1.MediaService/X)
	// that may run for HeavyTimeout instead of DefaultTimeout.
	Heavy map[string]bool
	// ClientIPHeader names the header a trusted reverse proxy in front of the BFF
	// writes the client address into; empty uses the TCP peer. Never set it when
	// clients can reach the BFF directly.
	ClientIPHeader string
	// AdminSessionMaxAge is how old (since the password was last checked) a session may be when it calls
	// a procedure that declares required_role. Zero disables the check.
	AdminSessionMaxAge time.Duration
	// Perimeter runs first for every procedure, before the write protection and any
	// session lookup, with the request headers and the TCP peer address. A non-nil
	// error refuses the call with the same "request rejected" whatever failed. It is
	// how the admin console's source policy (network and Origin) reaches the procedures
	// that need it; nil admits every procedure.
	Perimeter func(procedure string, header http.Header, remoteAddr string) error
	// Now defaults to time.Now; tests set it.
	Now func() time.Time
	// Authenticate resolves the browser session cookie for protected procedures and
	// checks the account at usersrv (active, same auth_version, current roles). It
	// fails UNAUTHENTICATED for a session that is not valid, and UNAVAILABLE or
	// INTERNAL when the check itself could not be made; the latter are passed on to
	// the caller and nothing is forwarded.
	Authenticate func(ctx context.Context, header http.Header) (session.BrowserSession, error)
	// DeletionKnown reports whether the browser's cookies (the deletion recovery cookie, or the
	// session cookie of a deletion that began before that existed) opened an account deletion.
	// DeleteAccount is admitted for such a browser even though its session no longer passes the
	// account check (a deleting account has a new auth_version).
	DeletionKnown func(ctx context.Context, header http.Header) bool
	// Projection runs after the session is verified and before the call is
	// forwarded, so a product that keeps a display projection of the account can
	// ensure it exists. It decides by procedure whether it applies; a failure is
	// returned to the caller as is.
	Projection func(ctx context.Context, procedure string, browser session.BrowserSession) error
}

var (
	errUnauthenticated = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))
	errRoleMissing     = connect.NewError(connect.CodePermissionDenied, errors.New("required role missing"))
	errSessionTooOld   = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in again to use this feature"))
	errWriteRejected   = connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
)

// RejectionHeader names the response header that tells a browser client why a write was
// refused before it ran, when the reason is one it can repair and repeat. Today that is a
// single value, RejectionCSRF: the double-submit token did not match the Cookie, which
// happens when another tab or application replaced the shared token. It carries no value
// from the request, the session or the account. Origin, perimeter, role, session and
// business refusals never carry it, so a client that retries only on this marker repeats
// nothing else.
const (
	RejectionHeader = "X-Bff-Rejection"
	RejectionCSRF   = "csrf"
)

// csrfRejected is the "request rejected" answer for a CSRF mismatch, with the marker. It is
// built per call: the shared errWriteRejected must never carry the marker, because the
// request ID is set on whichever error value is returned.
func csrfRejected() error {
	err := connect.NewError(connect.CodePermissionDenied, errors.New("request rejected"))
	err.Meta().Set(RejectionHeader, RejectionCSRF)
	return err
}

func (p Policy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Interceptor applies, to every unary procedure and independently of the service
// behind it: the anonymous policy declared in proto (default protected), the
// call deadline, a validated request ID, and the caller description the internal
// credential layer sends on. The deadline is the smaller of the client's
// remaining time and the procedure's cap, set once here and passed down.
func Interceptor(p Policy) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			procedure := req.Spec().Procedure
			requestID := rpcauth.NormalizeRequestID(req.Header().Get(RequestIDHeader))
			withID := func(err error) error {
				var ce *connect.Error
				if errors.As(err, &ce) {
					ce.Meta().Set(RequestIDHeader, requestID)
				}
				return err
			}
			if p.Perimeter != nil {
				if err := p.Perimeter(procedure, req.Header(), req.Peer().Addr); err != nil {
					return nil, withID(errWriteRejected)
				}
			}
			if RequiresWriteProtection(procedure) {
				if err := session.CheckWrite(req.Header(), p.AllowedOrigins, req.Header().Get("Host")); err != nil {
					if errors.Is(err, session.ErrCSRF) {
						return nil, withID(csrfRejected())
					}
					return nil, withID(errWriteRejected)
				}
			}
			call := rpcauth.Call{
				Channel:     rpcauth.ChannelWeb,
				RequestID:   requestID,
				ClientIP:    httpx.ClientAddr(p.ClientIPHeader, req.Header(), req.Peer().Addr),
				TraceParent: rpcauth.NormalizeTraceparent(req.Header().Get(rpcauth.MetadataTraceparent)),
			}
			var browser session.BrowserSession
			if !rpcauth.Anonymous(procedure) {
				if p.Authenticate == nil {
					return nil, withID(errUnauthenticated)
				}
				var err error
				browser, err = p.Authenticate(ctx, req.Header())
				if err == nil && browser.UserID == "" {
					err = errUnauthenticated
				}
				if err != nil {
					// Only a refused session is "sign in"; a check that could not be made
					// (usersrv or a store down) is reported as such.
					if ce := new(connect.Error); errors.As(err, &ce) && ce.Code() != connect.CodeUnauthenticated {
						return nil, withID(err)
					}
					if procedure != bffv1connect.AuthServiceDeleteAccountProcedure ||
						p.DeletionKnown == nil || !p.DeletionKnown(ctx, req.Header()) {
						return nil, withID(errUnauthenticated)
					}
				}
				// This header only narrows the authenticated Cookie identity; it
				// never authenticates or selects an account on its own.
				if expected := req.Header().Get("X-Kongken-User"); browser.UserID != "" && expected != "" && expected != browser.UserID {
					return nil, withID(errUnauthenticated)
				}
				call.UserID = browser.UserID
				// A role requirement is checked after the session is known, so an
				// anonymous caller sees UNAUTHENTICATED and a signed-in caller
				// without the role sees PERMISSION_DENIED. The business service
				// checks the same requirement again from the credential.
				if role := rpcauth.RequiredRole(procedure); role != "" && browser.UserID != "" {
					if !browser.HasRole(role) {
						return nil, withID(errRoleMissing)
					}
					if p.AdminSessionMaxAge > 0 && p.now().Sub(browser.AuthenticatedAt) > p.AdminSessionMaxAge {
						return nil, withID(errSessionTooOld)
					}
					call.Roles = browser.Roles
				}
			}
			ctx = rpcauth.WithCall(ctx, call)
			if browser.UserID != "" {
				ctx = session.WithBrowser(ctx, browser)
			}
			limit := DefaultTimeout
			if p.Heavy[procedure] {
				limit = HeavyTimeout
			}
			// WithTimeout keeps an earlier client deadline, so the smaller wins.
			ctx, cancel := context.WithTimeout(ctx, limit)
			defer cancel()
			if p.Projection != nil && browser.UserID != "" {
				if err := p.Projection(ctx, procedure, browser); err != nil {
					return nil, withID(err)
				}
			}
			res, err := next(ctx, req)
			if err != nil {
				return nil, withID(err)
			}
			res.Header().Set(RequestIDHeader, requestID)
			return res, nil
		}
	})
}

// RequestIDHeader carries the request ID on the public edge, in both directions.
const RequestIDHeader = "X-Request-Id"
