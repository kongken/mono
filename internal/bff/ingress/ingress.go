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
	// Heavy lists the procedures (full names, such as /tripmap.v1.MediaService/X)
	// that may run for HeavyTimeout instead of DefaultTimeout.
	Heavy map[string]bool
	// ClientIPHeader names the header a trusted reverse proxy in front of the BFF
	// writes the client address into; empty uses the TCP peer. Never set it when
	// clients can reach the BFF directly.
	ClientIPHeader string
	// Authenticate resolves the browser session cookie for protected procedures.
	Authenticate func(ctx context.Context, header http.Header) (session.BrowserSession, error)
}

var errUnauthenticated = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))

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
			call := rpcauth.Call{RequestID: requestID, ClientIP: httpx.ClientAddr(p.ClientIPHeader, req.Header(), req.Peer().Addr)}
			var browser session.BrowserSession
			if !rpcauth.Anonymous(procedure) {
				if p.Authenticate == nil {
					return nil, withID(errUnauthenticated)
				}
				var err error
				browser, err = p.Authenticate(ctx, req.Header())
				if err != nil || browser.UserID == "" {
					return nil, withID(errUnauthenticated)
				}
				call.UserID = browser.UserID
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
