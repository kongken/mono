package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/adminguard"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// The MCP endpoint has its own source allowlist (mcp.allowed_cidrs, mcp.trusted_proxies), independent of the
// browser console's (admin.*). These scenarios run the real chain: MCP client -> BFF -> metro, with the real
// token table, usersrv and Redis. A request from outside the list is refused before the token is looked at,
// and the counters below prove the business did not run.

// mcpSource sets the machine perimeter of a scenario.
func mcpSource(cfg bffconfig.MCPConfig) func(*bffconfig.Config) {
	return func(c *bffconfig.Config) {
		rate := c.MCP.RateLimitPerMinute
		c.MCP = cfg
		c.MCP.RateLimitPerMinute = rate
	}
}

// protocolStep is one request of the endpoint's protocol, in every shape it accepts or refuses.
type protocolStep struct {
	name   string
	method string
	body   string
	// ok is the status an admitted caller with a good write token gets.
	ok int
}

func protocolSteps(slug string) []protocolStep {
	catalog, _ := json.Marshal(map[string]any{"format": "metro-catalog/1", "cities": []map[string]string{{"slug": slug + "-imp", "name": "导入"}}})
	importCall, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": map[string]any{
		"name": "metro_import_catalog", "arguments": map[string]any{"catalogJson": string(catalog), "dryRun": false}}})
	return []protocolStep{
		{"initialize", http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, 200},
		{"initialized notification", http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, 202},
		{"ping", http.MethodPost, `{"jsonrpc":"2.0","id":2,"method":"ping"}`, 200},
		{"tools/list", http.MethodPost, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, 200},
		{"read tool", http.MethodPost, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"metro_list_cities","arguments":{}}}`, 200},
		{"write tool", http.MethodPost, fmt.Sprintf(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"metro_create_city","arguments":{"city":{"slug":%q,"name":"来源"}}}}`, slug), 200},
		{"import (applied)", http.MethodPost, string(importCall), 200},
		{"batch", http.MethodPost, `[{"jsonrpc":"2.0","id":7,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":8,"method":"tools/list"}]`, 200},
		{"unknown method", http.MethodPost, `{"jsonrpc":"2.0","id":9,"method":"resources/list"}`, 200},
		{"garbage body", http.MethodPost, `{nope`, 200},
		{"GET (SSE stream)", http.MethodGet, ``, 405},
		{"DELETE (end session)", http.MethodDelete, ``, 405},
		{"preflight", http.MethodOptions, ``, 405},
	}
}

// do sends one request to the MCP endpoint from a client the proxy reports as xff ("" = no header).
func (h *ChainHarness) mcpDo(step protocolStep, token, xff string, extra ...string) (int, http.Header, string) {
	h.T.Helper()
	req, err := http.NewRequest(step.method, h.BFFURL+"/mcp/metro", bytes.NewReader([]byte(step.body)))
	if err != nil {
		h.T.Fatal(err)
	}
	if step.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		req.Header.Set(extra[i], extra[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.T.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(body)
}

// newTokenVia creates a management token as the console would: with its Origin and from an address the
// console's own allowlist admits.
func (h *ChainHarness) newTokenVia(ctx context.Context, b *Browser, name, xff string, scopes ...string) string {
	h.T.Helper()
	req := connect.NewRequest(&bffv1.CreateAccessTokenRequest{Name: name, Scopes: scopes, ExpiresInDays: 30})
	b.Write(ctx, req)
	req.Header().Set("Origin", adminOrigin)
	if xff != "" {
		req.Header().Set("X-Forwarded-For", xff)
	}
	res, err := bffv1connect.NewAccessTokenServiceClient(b.HTTP, b.APIURL).CreateAccessToken(ctx, req)
	if err != nil {
		h.T.Fatalf("create token: %v", err)
	}
	return res.Msg.GetSecret()
}

type mcpWorld struct {
	h       *ChainHarness
	reached atomic.Int64
	admin   *Browser
	write   string
	read    string
}

func newMCPWorld(t *testing.T, admin adminguard.Config, mcp bffconfig.MCPConfig, tokenVia string) *mcpWorld {
	t.Helper()
	w := &mcpWorld{}
	w.h = adminHarness(t, admin, ChainMetro{}, ChainMetroUnary{countMetroAdmin(&w.reached)}, mcpSource(mcp))
	ctx := context.Background()
	w.admin = w.h.FreshBrowser()
	if err := w.admin.LoginWithRoles(ctx, "mcp_perimeter", "metro-admin"); err != nil {
		t.Fatal(err)
	}
	w.write = w.h.newTokenVia(ctx, w.admin, "writer", tokenVia, "metro:write")
	w.read = w.h.newTokenVia(ctx, w.admin, "reader", tokenVia, "metro:read")
	return w
}

// nothingRan is the proof that refused requests did not execute anything: metro's admin service was not
// called, no city was created and no token was used.
func (w *mcpWorld) snapshot(ctx context.Context, t *testing.T) (calls int64, cities int, used int) {
	t.Helper()
	cities, err := w.h.MetroDB.City.Query().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	list, err := w.admin.Tokens.ListAccessTokens(ctx, connect.NewRequest(&bffv1.ListAccessTokensRequest{}))
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	for _, tk := range list.Msg.Tokens {
		if tk.LastUsedAt != nil {
			used++
		}
	}
	return w.reached.Load(), cities, used
}

func TestChainMCPSourceAllowlist(t *testing.T) {
	ctx := context.Background()
	machine := bffconfig.MCPConfig{
		AllowedCIDRs:   []string{"192.0.2.0/24", "2001:db8:1::/48"},
		TrustedProxies: []string{"127.0.0.0/8"},
	}
	w := newMCPWorld(t, loopback, machine, "")
	h := w.h

	t.Run("an allowed client reaches every part of the protocol", func(t *testing.T) {
		for i, xff := range []string{
			"192.0.2.7",                  // IPv4 behind the trusted proxy
			"2001:db8:1::9",              // IPv6
			"::ffff:192.0.2.9",           // an IPv4-mapped address is matched as IPv4
			"203.0.113.5, 192.0.2.7",     // a forged left entry does not matter
			"192.0.2.7, 127.0.0.1",       // a further trusted hop on the right
			"203.0.113.9, 2001:db8:1::5", // mixed families
		} {
			for _, step := range protocolSteps(fmt.Sprintf("allowed-%d", i)) {
				if status, _, body := h.mcpDo(step, w.write, xff); status != step.ok {
					t.Errorf("%s as %q: HTTP %d, want %d (%s)", step.name, xff, status, step.ok, body)
				}
			}
		}
		if n, _ := h.MetroDB.City.Query().Count(ctx); n != 12 {
			t.Fatalf("the allowed write tool and import should have created 12 cities, found %d", n)
		}
	})

	t.Run("a client outside the list is refused on every request kind and nothing runs", func(t *testing.T) {
		calls, cities, used := w.snapshot(ctx, t)
		for _, xff := range []string{
			"198.51.100.9",           // outside
			"2001:db8:2::1",          // IPv6 outside
			"::ffff:203.0.113.5",     // mapped outsider
			"192.0.2.7, 203.0.113.9", // the allowed address is a forged left entry
			"192.0.2.7, 203.0.113.9, 127.0.0.1",
			"192.0.2.7, nonsense", // an unparsable hop is a refusal, not a fallback
			"garbage",
			"",          // no header: the proxy itself is the client
			"127.0.0.1", // only proxies in the chain
		} {
			for _, step := range protocolSteps("denied") {
				status, header, body := h.mcpDo(step, w.write, xff, "Origin", adminOrigin, "Cookie", "kongken_sid=x")
				if status != http.StatusForbidden {
					t.Errorf("%s as %q: HTTP %d %s", step.name, xff, status, body)
				}
				if header.Get("Access-Control-Allow-Origin") != "" || header.Get("WWW-Authenticate") != "" || header.Get("Allow") != "" {
					t.Errorf("%s as %q: the refusal says more than no: %v", step.name, xff, header)
				}
				if strings.TrimSpace(body) != "request rejected" {
					t.Errorf("%s as %q: body %q", step.name, xff, body)
				}
			}
		}
		// Other header names never stand in for the forwarded chain, and an untrusted peer cannot use it at all.
		for _, headers := range [][]string{
			{"X-Real-IP", "192.0.2.7"}, {"Forwarded", "for=192.0.2.7"}, {"X-Client-IP", "192.0.2.7"}, {"True-Client-IP", "192.0.2.7"}, {"CF-Connecting-IP", "192.0.2.7"},
		} {
			if status, _, _ := h.mcpDo(protocolSteps("x")[2], w.write, "", headers...); status != http.StatusForbidden {
				t.Errorf("%v admitted: HTTP %d", headers, status)
			}
		}
		// A refusal is not an authentication failure: with a wrong or no token the answer is the same 403.
		for _, token := range []string{"", "mcp_" + strings.Repeat("0", 64), w.read} {
			if status, _, _ := h.mcpDo(protocolSteps("x")[2], token, "198.51.100.9"); status != http.StatusForbidden {
				t.Errorf("token %q from outside: HTTP %d", token, status)
			}
		}
		// The other spellings of the path do not get around it either.
		for _, path := range []string{"/mcp/metro/", "/mcp/metro?x=1", "//mcp/metro", "/mcp/metro/."} {
			req, _ := http.NewRequest(http.MethodPost, h.BFFURL+path, strings.NewReader(protocolSteps("x")[2].body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+w.write)
			req.Header.Set("X-Forwarded-For", "198.51.100.9")
			res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			// Either the perimeter's refusal or no such route: never a redirect that says the route exists, never a service.
			if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusNotFound {
				t.Errorf("%s from outside: HTTP %d (Location %q)", path, res.StatusCode, res.Header.Get("Location"))
			}
		}

		calls2, cities2, used2 := w.snapshot(ctx, t)
		if calls2 != calls || cities2 != cities || used2 != used {
			t.Fatalf("refused requests ran something: metro calls %d->%d, cities %d->%d, tokens used %d->%d", calls, calls2, cities, cities2, used, used2)
		}
	})

	t.Run("refused requests do not use up the token's rate limit or the failed-attempt throttle of the allowed ones", func(t *testing.T) {
		fresh := h.newTokenVia(ctx, w.admin, "flooded", "", "metro:read")
		// More than the per-token limit (60 a minute), all from outside, then one from inside.
		for i := 0; i < 100; i++ {
			h.mcpDo(protocolSteps("x")[2], fresh, "198.51.100.9")
			h.mcpDo(protocolSteps("x")[2], "mcp_"+strings.Repeat("1", 64), "198.51.100.9")
		}
		if status, _, _ := h.mcpDo(protocolSteps("x")[2], fresh, "192.0.2.7"); status != http.StatusOK {
			t.Fatalf("an allowed client after a flood from outside: HTTP %d", status)
		}
	})
}

func TestChainMCPEmptyAllowlistRefusesEveryoneAndNobodyElse(t *testing.T) {
	ctx := context.Background()
	// No mcp.allowed_cidrs at all, but a trusted proxy and a fully working console and public catalogue.
	w := newMCPWorld(t, loopback, bffconfig.MCPConfig{TrustedProxies: []string{"127.0.0.0/8"}}, "")
	h := w.h
	for _, xff := range []string{"", "192.0.2.7", "127.0.0.1", "2001:db8:1::9", "10.0.0.1"} {
		for _, step := range protocolSteps("closed") {
			if status, _, _ := h.mcpDo(step, w.write, xff); status != http.StatusForbidden {
				t.Errorf("%s as %q on a closed endpoint: HTTP %d", step.name, xff, status)
			}
		}
	}
	if calls, cities, used := w.snapshot(ctx, t); calls != 0 || cities != 0 || used != 0 {
		t.Fatalf("a closed endpoint ran something: %d %d %d", calls, cities, used)
	}
	// The rest of the product is untouched: the browser administration (its own list), the public catalogue
	// and an ordinary account call.
	if _, err := h.FreshBrowser().Catalog.ListCities(ctx, connect.NewRequest(&bffmetrov1.ListCitiesRequest{})); err != nil {
		t.Fatalf("the public catalogue: %v", err)
	}
	if _, err := w.admin.Me(ctx); err != nil {
		t.Fatalf("an ordinary call: %v", err)
	}
	for _, c := range metroPerimeterCalls() {
		if c.write {
			continue
		}
		if err := c.run(ctx, w.admin, adminOrigin); err != nil {
			t.Errorf("the browser administration %s with the MCP list empty: %v", c.name, err)
		}
	}
}

// The two lists are separate configuration with separate callers: an address in one is not in the other, in
// either direction, and a browser session is never a substitute for a Bearer token.
func TestChainMCPAndBrowserAllowlistsAreIndependent(t *testing.T) {
	ctx := context.Background()
	const browserIP, machineIP = "192.0.2.7", "198.51.100.7"
	browser := adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"127.0.0.0/8"}}
	machine := bffconfig.MCPConfig{AllowedCIDRs: []string{"198.51.100.0/24"}, TrustedProxies: []string{"127.0.0.0/8"}}
	w := newMCPWorld(t, browser, machine, browserIP)
	h := w.h
	ping := protocolSteps("x")[2]

	// The browser list does not open MCP, and the machine list does not open the browser administration.
	if status, _, _ := h.mcpDo(ping, w.write, browserIP); status != http.StatusForbidden {
		t.Fatalf("a browser-list address reached MCP: HTTP %d", status)
	}
	if status, _, _ := h.mcpDo(ping, w.write, machineIP); status != http.StatusOK {
		t.Fatalf("a machine-list address was refused by MCP: HTTP %d", status)
	}
	cities, _ := h.MetroDB.City.Query().Count(ctx)
	calls := w.reached.Load()
	for _, c := range metroPerimeterCalls() {
		err := c.run(ctx, w.admin, adminOrigin, "X-Forwarded-For", machineIP)
		if connect.CodeOf(err) != connect.CodePermissionDenied || !isPerimeterRefusal(err) {
			t.Errorf("%s from a machine-list address: want the generic refusal, got %v", c.name, err)
		}
	}
	if _, err := bffv1connect.NewUserAdminServiceClient(w.admin.HTTP, w.admin.APIURL).ListUsers(ctx, withOrigin(&bffv1.ListUsersRequest{}, adminOrigin, "X-Forwarded-For", machineIP)); !isPerimeterRefusal(err) {
		t.Errorf("user administration from a machine-list address: %v", err)
	}
	if n, _ := h.MetroDB.City.Query().Count(ctx); n != cities || w.reached.Load() != calls {
		t.Fatalf("a refused browser call ran something: cities %d -> %d, metro calls %d -> %d", cities, n, calls, w.reached.Load())
	}
	// From the browser list the same reads go through (the browser list is not narrowed by the machine list).
	for _, c := range metroPerimeterCalls() {
		if c.write {
			continue
		}
		if err := c.run(ctx, w.admin, adminOrigin, "X-Forwarded-For", browserIP); err != nil {
			t.Errorf("%s from a browser-list address: %v", c.name, err)
		}
	}

	// A session Cookie of an administrator, from the allowed machine address, is not a credential here.
	cookies := w.admin.HTTP.Jar.Cookies(mustURL(t, h.BFFURL))
	if len(cookies) == 0 {
		t.Fatal("no session cookies")
	}
	var cookie []string
	for _, c := range cookies {
		cookie = append(cookie, c.Name+"="+c.Value)
	}
	status, header, _ := h.mcpDo(ping, "", machineIP, "Cookie", strings.Join(cookie, "; "), "Origin", adminOrigin)
	if status != http.StatusUnauthorized || header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("a session cookie from the machine list: HTTP %d %v", status, header)
	}

	// No browser CORS is ever added: not for an allowed caller, not for a refused one.
	for _, xff := range []string{machineIP, browserIP} {
		for _, step := range []protocolStep{ping, {"preflight", http.MethodOptions, ``, 405}} {
			_, header, _ := h.mcpDo(step, w.write, xff, "Origin", adminOrigin, "Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "authorization,content-type")
			for k := range header {
				if strings.HasPrefix(strings.ToLower(k), "access-control-") {
					t.Errorf("%s from %s: CORS header %s", step.name, xff, k)
				}
			}
		}
	}
}

// Passing the network is only the first gate: the Bearer digest, scope, expiry and revocation, and the owner's
// current account state are still checked, MCP does not borrow the browser's password re-check, and the tools
// still run the same metro methods.
func TestChainMCPAllowedAddressStillNeedsAValidTokenAndScope(t *testing.T) {
	ctx := context.Background()
	machine := bffconfig.MCPConfig{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"127.0.0.0/8"}}
	w := newMCPWorld(t, loopback, machine, "")
	h := w.h
	const ip = "192.0.2.7"
	ping := protocolSteps("x")[2]

	for name, token := range map[string]string{"no token": "", "a made-up token": "mcp_" + strings.Repeat("a", 64), "a token of the wrong shape": "nope"} {
		if status, header, _ := h.mcpDo(ping, token, ip); status != http.StatusUnauthorized || header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s from an allowed address: HTTP %d", name, status)
		}
	}

	// A read-only token cannot use the write tools, even from an allowed address, and metro is not reached.
	calls, cities, _ := w.snapshot(ctx, t)
	write := protocolSteps("scope")[5]
	status, _, body := h.mcpDo(write, w.read, ip)
	if status != http.StatusOK || !strings.Contains(body, "isError") || !strings.Contains(body, "metro:write") {
		t.Fatalf("a read-only token called a write tool: HTTP %d %s", status, body)
	}
	imp := protocolSteps("scope")[6]
	if status, _, body = h.mcpDo(imp, w.read, ip); status != http.StatusOK || !strings.Contains(body, "isError") {
		t.Fatalf("a read-only token called import: HTTP %d %s", status, body)
	}
	if calls2, cities2, _ := w.snapshot(ctx, t); calls2 != calls || cities2 != cities {
		t.Fatalf("a scope refusal ran something: metro calls %d->%d, cities %d->%d", calls, calls2, cities, cities2)
	}
	// With the write token the same call runs.
	if status, _, body = h.mcpDo(write, w.write, ip); status != http.StatusOK || strings.Contains(body, `"isError":true`) {
		t.Fatalf("the write token: HTTP %d %s", status, body)
	}

	// Revoking the token stops it at once, from the allowed address.
	list, err := w.admin.Tokens.ListAccessTokens(ctx, connect.NewRequest(&bffv1.ListAccessTokensRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range list.Msg.Tokens {
		if tk.Name == "writer" {
			rev := connect.NewRequest(&bffv1.RevokeAccessTokenRequest{Id: tk.Id})
			w.admin.Write(ctx, rev)
			if _, err := w.admin.Tokens.RevokeAccessToken(ctx, rev); err != nil {
				t.Fatal(err)
			}
		}
	}
	if status, _, _ := h.mcpDo(ping, w.write, ip); status != http.StatusUnauthorized {
		t.Fatalf("a revoked token from an allowed address: HTTP %d", status)
	}

	// The browser's password re-check is not applied to MCP: a token keeps working when the owner's session
	// is old (the owner's password has not changed, so the account's auth version has not either).
	fresh := h.newTokenVia(ctx, w.admin, "later", "", "metro:write")
	h.ageSession(w.admin, 16*time.Minute)
	if status, _, body := h.mcpDo(protocolSteps("aged")[5], fresh, ip); status != http.StatusOK || strings.Contains(body, `"isError":true`) {
		t.Fatalf("an old browser session affected a token: HTTP %d %s", status, body)
	}
}
