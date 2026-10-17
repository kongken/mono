package ingress_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ingress"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/platform/rpcauth"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
	_ "github.com/kongken/mono/pkg/proto/tripmap/v1" // registers the protected methods used below
)

type okStatus struct{}

func (okStatus) GetStatus(context.Context, *connect.Request[bffv1.GetStatusRequest]) (*connect.Response[bffv1.GetStatusResponse], error) {
	return connect.NewResponse(&bffv1.GetStatusResponse{Ready: true}), nil
}

// publicServer serves the real generated handler behind the public protocol gate.
func publicServer(t *testing.T) *httptest.Server {
	t.Helper()
	path, handler := bffv1connect.NewStatusServiceHandler(okStatus{}, connect.WithRequireConnectProtocolHeader())
	mux := http.NewServeMux()
	mux.Handle(path, ingress.ConnectOnly(handler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func post(t *testing.T, url, contentType string, headers ...string) *http.Response {
	t.Helper()
	body := "{}"
	if contentType == "application/proto" {
		body = "" // the empty request message
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func TestOnlyConnectUnaryIsAccepted(t *testing.T) {
	url := publicServer(t).URL + bffv1connect.StatusServiceGetStatusProcedure
	for name, tc := range map[string]struct {
		contentType string
		headers     []string
		want        int
	}{
		"connect json":            {"application/json", []string{"Connect-Protocol-Version", "1"}, 200},
		"connect json charset":    {"application/json; charset=utf-8", []string{"Connect-Protocol-Version", "1"}, 200},
		"connect proto":           {"application/proto", []string{"Connect-Protocol-Version", "1"}, 200},
		"missing version":         {"application/json", nil, 400},
		"unknown version":         {"application/json", []string{"Connect-Protocol-Version", "2"}, 400},
		"grpc":                    {"application/grpc", nil, 415},
		"grpc proto":              {"application/grpc+proto", []string{"Connect-Protocol-Version", "1"}, 415},
		"grpc-web":                {"application/grpc-web+proto", nil, 415},
		"grpc-web text":           {"application/grpc-web-text", nil, 415},
		"connect streaming frame": {"application/connect+json", []string{"Connect-Protocol-Version", "1"}, 415},
		"other media type":        {"text/plain", []string{"Connect-Protocol-Version", "1"}, 415},
	} {
		if got := post(t, url, tc.contentType, tc.headers...); got.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", name, got.StatusCode, tc.want)
		}
	}
	if got := post(t, url, "text/plain"); got.Header.Get("Accept-Post") != "application/json, application/proto" {
		t.Errorf("415 does not list the accepted types: %v", got.Header)
	}
	res, err := http.Get(url + "?connect=v1&encoding=json&message=%7B%7D")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d, want 405", res.StatusCode)
	}
}

const (
	anonymousProcedure = bffv1connect.StatusServiceGetStatusProcedure
	// A real protected method: it has no anonymous option, so it needs a user.
	protectedProcedure = "/tripmap.v1.FootprintService/GetFootprints"
	heavyProcedure     = bffv1connect.MediaServiceCompleteUploadProcedure
)

// probe is what a handler behind the interceptor observes.
type probe struct {
	called   bool
	call     rpcauth.Call
	deadline time.Duration
}

func writeSafeReq() *connect.Request[bffv1.GetStatusRequest] {
	req := connect.NewRequest(&bffv1.GetStatusRequest{})
	tok := "test-csrf"
	req.Header().Set(session.CSRFHeader, tok)
	req.Header().Set("Cookie", session.CSRFCookieName+"="+tok)
	req.Header().Set("Sec-Fetch-Site", "same-origin")
	return req
}

func intercepted(t *testing.T, policy ingress.Policy, procedure string) (*probe, *connect.Client[bffv1.GetStatusRequest, bffv1.GetStatusResponse]) {
	t.Helper()
	seen := &probe{}
	handler := connect.NewUnaryHandler(procedure, func(ctx context.Context, _ *connect.Request[bffv1.GetStatusRequest]) (*connect.Response[bffv1.GetStatusResponse], error) {
		seen.called = true
		seen.call = rpcauth.CallFrom(ctx)
		if d, ok := ctx.Deadline(); ok {
			seen.deadline = time.Until(d)
		}
		return connect.NewResponse(&bffv1.GetStatusResponse{}), nil
	}, connect.WithInterceptors(ingress.Interceptor(policy)), connect.WithRequireConnectProtocolHeader())
	mux := http.NewServeMux()
	mux.Handle(procedure, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return seen, connect.NewClient[bffv1.GetStatusRequest, bffv1.GetStatusResponse](server.Client(), server.URL+procedure)
}

func TestProceduresAreProtectedUnlessDeclaredAnonymous(t *testing.T) {
	seen, client := intercepted(t, ingress.Policy{}, protectedProcedure)
	_, err := client.CallUnary(context.Background(), connect.NewRequest(&bffv1.GetStatusRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated || seen.called {
		t.Fatalf("protected procedure without a session: err=%v called=%v", err, seen.called)
	}

	// An unknown procedure is protected too.
	seen, client = intercepted(t, ingress.Policy{}, "/bff.v1.UnknownService/Do")
	if _, err = client.CallUnary(context.Background(), writeSafeReq()); connect.CodeOf(err) != connect.CodeUnauthenticated || seen.called {
		t.Fatalf("unknown procedure: err=%v called=%v", err, seen.called)
	}

	// An anonymous procedure runs without one, and carries no user.
	seen, client = intercepted(t, ingress.Policy{}, anonymousProcedure)
	if _, err = client.CallUnary(context.Background(), connect.NewRequest(&bffv1.GetStatusRequest{})); err != nil || !seen.called || seen.call.UserID != "" {
		t.Fatalf("anonymous procedure: err=%v called=%v user=%q", err, seen.called, seen.call.UserID)
	}

	// A session resolved by the BFF becomes the user of a protected call; an
	// anonymous procedure ignores it.
	policy := ingress.Policy{Authenticate: func(context.Context, http.Header) (session.BrowserSession, error) {
		return session.BrowserSession{UserID: "user-1", SessionID: "s1"}, nil
	}}
	seen, client = intercepted(t, policy, protectedProcedure)
	if _, err = client.CallUnary(context.Background(), writeSafeReq()); err != nil || seen.call.UserID != "user-1" {
		t.Fatalf("signed-in call: err=%v user=%q", err, seen.call.UserID)
	}
	seen, client = intercepted(t, policy, anonymousProcedure)
	if _, err = client.CallUnary(context.Background(), connect.NewRequest(&bffv1.GetStatusRequest{})); err != nil || seen.call.UserID != "" {
		t.Fatalf("anonymous procedure with a session: err=%v user=%q", err, seen.call.UserID)
	}
}

func TestCallLifetimeIsBoundedAndKeepsTheShorterDeadline(t *testing.T) {
	near := func(got, want time.Duration) bool { return got > want-2*time.Second && got <= want }
	call := func(policy ingress.Policy, procedure string, clientTimeout time.Duration) time.Duration {
		seen, client := intercepted(t, policy, procedure)
		ctx := context.Background()
		req := writeSafeReq()
		if clientTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, clientTimeout)
			defer cancel()
		}
		if _, err := client.CallUnary(ctx, req); err != nil {
			t.Fatal(err)
		}
		return seen.deadline
	}
	policy := ingress.Policy{Authenticate: func(context.Context, http.Header) (session.BrowserSession, error) {
		return session.BrowserSession{UserID: "u", SessionID: "s1"}, nil
	}, Heavy: map[string]bool{heavyProcedure: true}}
	if got := call(policy, protectedProcedure, 0); !near(got, 10*time.Second) {
		t.Errorf("ordinary call without a client deadline: %v, want about 10s", got)
	}
	if got := call(policy, protectedProcedure, 3*time.Second); !near(got, 3*time.Second) {
		t.Errorf("shorter client deadline: %v, want about 3s", got)
	}
	if got := call(policy, protectedProcedure, time.Minute); !near(got, 10*time.Second) {
		t.Errorf("longer client deadline: %v, want the 10s cap", got)
	}
	if got := call(policy, heavyProcedure, 0); !near(got, 25*time.Second) {
		t.Errorf("registered heavy call: %v, want about 25s", got)
	}
	if got := call(policy, heavyProcedure, time.Minute); !near(got, 25*time.Second) {
		t.Errorf("registered heavy call with a long client deadline: %v, want the 25s cap", got)
	}
}

func TestRequestIDAndClientAddress(t *testing.T) {
	do := func(policy ingress.Policy, headers ...string) (rpcauth.Call, http.Header) {
		seen, client := intercepted(t, policy, anonymousProcedure)
		req := connect.NewRequest(&bffv1.GetStatusRequest{})
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header().Set(headers[i], headers[i+1])
		}
		res, err := client.CallUnary(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return seen.call, res.Header()
	}
	if call, res := do(ingress.Policy{}, "X-Request-Id", "req-123.A_b"); call.RequestID != "req-123.A_b" || res.Get("X-Request-Id") != "req-123.A_b" {
		t.Errorf("a well-formed request ID was not kept: %q / %q", call.RequestID, res.Get("X-Request-Id"))
	}
	for _, bad := range []string{"has spaces", "semi;colon", strings.Repeat("a", 65)} {
		call, res := do(ingress.Policy{}, "X-Request-Id", bad)
		if call.RequestID == bad || len(call.RequestID) != 32 || res.Get("X-Request-Id") != call.RequestID {
			t.Errorf("malformed request ID %q: handler saw %q, response %q", bad, call.RequestID, res.Get("X-Request-Id"))
		}
	}
	if call, _ := do(ingress.Policy{}); len(call.RequestID) != 32 {
		t.Errorf("no request ID was generated: %q", call.RequestID)
	}

	// Without a trusted proxy header the TCP peer is the client, whatever the headers say.
	if call, _ := do(ingress.Policy{}, "X-Real-IP", "203.0.113.7", rpcauth.MetadataClientIP, "198.51.100.9"); call.ClientIP != "127.0.0.1" {
		t.Errorf("untrusted headers chose the client address: %q", call.ClientIP)
	}
	// Behind a configured trusted proxy its header is the address.
	if call, _ := do(ingress.Policy{ClientIPHeader: "X-Real-IP"}, "X-Real-IP", "203.0.113.7", rpcauth.MetadataClientIP, "198.51.100.9"); call.ClientIP != "203.0.113.7" {
		t.Errorf("trusted proxy address ignored: %q", call.ClientIP)
	}
}
