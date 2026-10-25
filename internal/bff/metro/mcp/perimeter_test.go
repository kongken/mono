package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kongken/mono/internal/bff/accesstoken"
	"github.com/kongken/mono/internal/bff/adminguard"
	"github.com/kongken/mono/internal/bff/usersrv"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
)

// Everything behind the perimeter is counted. A request from outside the allowlist must leave every count at
// zero: no token looked up, no account asked, no counter touched, no metro method called. The metro client is
// a nil interface, so a call to it would panic and fail the test loudly.
type counted struct {
	tokens, accounts, limits atomic.Int64
	principal                accesstoken.Principal
}

func (c *counted) Authenticate(_ context.Context, secret, _ string) (accesstoken.Principal, error) {
	c.tokens.Add(1)
	if secret != "mcp_good" {
		return accesstoken.Principal{}, accesstoken.ErrInvalidToken
	}
	return c.principal, nil
}

func (c *counted) AuthState(context.Context, string) (usersrv.AuthState, error) {
	c.accounts.Add(1)
	return usersrv.AuthState{Status: usersrv.StatusActive, AuthVersion: 1, Roles: []string{AdminRole}}, nil
}

func (c *counted) Allow(context.Context, string, int64, time.Duration) (bool, error) {
	c.limits.Add(1)
	return true, nil
}

func (c *counted) touched() int64 { return c.tokens.Load() + c.accounts.Load() + c.limits.Load() }

func newCounted(t *testing.T, cfg adminguard.SourceConfig) (*Server, *counted) {
	t.Helper()
	src, err := adminguard.NewSource("mcp", cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := &counted{principal: accesstoken.Principal{TokenID: "t1", UserID: "u1", AuthVersion: 1, Roles: []string{AdminRole}, Scopes: []string{accesstoken.ScopeWrite}}}
	return New(Config{Admin: metrov1.AdminServiceClient(nil), Tokens: c, Accounts: c, Limiter: c, Perimeter: src}), c
}

type call struct {
	name, method, body string
}

// Every kind of request the endpoint knows, and the ones it does not.
var everyRequest = []call{
	{"initialize", http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`},
	{"initialized notification", http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`},
	{"ping", http.MethodPost, `{"jsonrpc":"2.0","id":2,"method":"ping"}`},
	{"tools/list", http.MethodPost, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`},
	{"read tool", http.MethodPost, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"metro_list_cities","arguments":{}}}`},
	{"write tool", http.MethodPost, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"metro_create_city","arguments":{"slug":"x","name":"x"}}}`},
	{"import", http.MethodPost, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"metro_import_catalog","arguments":{"catalogJson":"{}","dryRun":false}}}`},
	{"batch", http.MethodPost, `[{"jsonrpc":"2.0","id":7,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"}]`},
	{"empty batch", http.MethodPost, `[]`},
	{"unknown method", http.MethodPost, `{"jsonrpc":"2.0","id":8,"method":"resources/list"}`},
	{"garbage", http.MethodPost, `{nope`},
	{"empty body", http.MethodPost, ``},
	{"GET (SSE stream)", http.MethodGet, ``},
	{"DELETE (end session)", http.MethodDelete, ``},
	{"preflight", http.MethodOptions, ``},
	{"HEAD", http.MethodHead, ``},
}

func send(s *Server, c call, peer string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(c.method, "/mcp/metro", strings.NewReader(c.body))
	r.RemoteAddr = peer
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer mcp_good")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestEveryRequestFromOutsideTheAllowlistIsRefusedBeforeAnythingRuns(t *testing.T) {
	s, c := newCounted(t, adminguard.SourceConfig{AllowedCIDRs: []string{"192.0.2.0/24"}})
	for _, req := range everyRequest {
		for _, peer := range []string{"203.0.113.9:4000", "[2001:db8::9]:4000", "127.0.0.1:4000", "garbage"} {
			// A valid token, a session Cookie, a browser Origin and a header claiming an allowed address
			// change nothing: only the network decides.
			w := send(s, req, peer, map[string]string{
				"Cookie": "kongken_sid=whatever", "Origin": "https://admin.example.com",
				"X-Forwarded-For": "192.0.2.7", "X-Real-IP": "192.0.2.7",
			})
			if w.Code != http.StatusForbidden {
				t.Errorf("%s from %s: HTTP %d", req.name, peer, w.Code)
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("WWW-Authenticate") != "" || w.Header().Get("Allow") != "" {
				t.Errorf("%s from %s: the refusal says more than no: %v", req.name, peer, w.Header())
			}
			if body := strings.TrimSpace(w.Body.String()); body != "request rejected" {
				t.Errorf("%s from %s: body %q", req.name, peer, body)
			}
		}
	}
	if n := c.touched(); n != 0 {
		t.Fatalf("a refused request reached the token table, the account service or a counter %d times", n)
	}
}

func TestAnEmptyOrMissingAllowlistRefusesEveryone(t *testing.T) {
	s, c := newCounted(t, adminguard.SourceConfig{})
	for _, req := range everyRequest {
		for _, peer := range []string{"127.0.0.1:1", "192.0.2.5:1", "[::1]:1", "10.0.0.1:1"} {
			if w := send(s, req, peer, nil); w.Code != http.StatusForbidden {
				t.Errorf("%s from %s with no allowlist: HTTP %d", req.name, peer, w.Code)
			}
		}
	}
	// No perimeter at all is the same as an empty one.
	bare := New(Config{Admin: metrov1.AdminServiceClient(nil), Tokens: c, Accounts: c, Limiter: c})
	if w := send(bare, everyRequest[0], "192.0.2.5:1", nil); w.Code != http.StatusForbidden {
		t.Errorf("a server built without a perimeter answered %d", w.Code)
	}
	if n := c.touched(); n != 0 {
		t.Fatalf("a closed endpoint reached %d dependencies", n)
	}
}

func TestFromInsideTheAllowlistTheTokenChecksStillApply(t *testing.T) {
	s, c := newCounted(t, adminguard.SourceConfig{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"10.0.0.0/8"}})
	// Reached through a trusted proxy that reports an allowed client.
	via := map[string]string{"X-Forwarded-For": "192.0.2.7"}
	if w := send(s, call{"ping", http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"ping"}`}, "10.0.0.2:1", via); w.Code != http.StatusOK {
		t.Fatalf("an allowed client with a good token: HTTP %d %s", w.Code, w.Body)
	}
	if w := send(s, call{"notification", http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`}, "192.0.2.7:1", nil); w.Code != http.StatusAccepted {
		t.Fatalf("notification: HTTP %d", w.Code)
	}
	// Allowed address, bad token: the bearer decides, so 401 (not 403) and the token table was asked.
	before := c.tokens.Load()
	w := send(s, everyRequest[0], "192.0.2.7:1", map[string]string{"Authorization": "Bearer mcp_wrong"})
	if w.Code != http.StatusUnauthorized || c.tokens.Load() != before+1 {
		t.Fatalf("allowed address, wrong token: HTTP %d", w.Code)
	}
	// A Cookie never stands in for the bearer.
	w = send(s, everyRequest[0], "192.0.2.7:1", map[string]string{"Authorization": "", "Cookie": "kongken_sid=whatever"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("allowed address, a Cookie and no bearer: HTTP %d", w.Code)
	}
	// An allowed address does not lift the method rule, and a browser Origin gets no CORS.
	w = send(s, call{"GET", http.MethodGet, ""}, "192.0.2.7:1", map[string]string{"Origin": "https://admin.example.com"})
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("GET from an allowed address: HTTP %d %v", w.Code, w.Header())
	}
	w = send(s, call{"preflight", http.MethodOptions, ""}, "192.0.2.7:1", map[string]string{"Origin": "https://admin.example.com", "Access-Control-Request-Method": "POST"})
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("preflight from an allowed address: HTTP %d %v", w.Code, w.Header())
	}
	// Forged forwarding from an untrusted peer does not make it allowed.
	if w := send(s, everyRequest[0], "203.0.113.9:1", via); w.Code != http.StatusForbidden {
		t.Fatalf("forged header from an untrusted peer: HTTP %d", w.Code)
	}
	// The throttle of failed attempts and the audit trail use the resolved address, not a header's word.
	if got := resolvedKey(t, s, "10.0.0.2:1", map[string]string{"X-Forwarded-For": "203.0.113.50, 192.0.2.7"}); got != "192.0.2.7" {
		t.Fatalf("resolved address: %q", got)
	}
}

// resolvedKey reports which address the perimeter handed to the rest of the request.
func resolvedKey(t *testing.T, s *Server, peer string, h map[string]string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp/metro", nil)
	r.RemoteAddr = peer
	for k, v := range h {
		r.Header.Set(k, v)
	}
	ip, ok := s.cfg.Perimeter.Allowed(r.Header, r.RemoteAddr)
	if !ok {
		t.Fatalf("not admitted: %s %v", peer, h)
	}
	return ip.String()
}
