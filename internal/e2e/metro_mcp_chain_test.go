package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"connectrpc.com/connect"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

// mcpClient speaks MCP Streamable HTTP to the BFF.
type mcpClient struct {
	t      *testing.T
	url    string
	token  string
	extra  http.Header
	client *http.Client
}

func (h *ChainHarness) mcp(token string) *mcpClient {
	return &mcpClient{t: h.T, url: h.BFFURL + "/mcp/metro", token: token, client: &http.Client{}, extra: http.Header{}}
}

func (c *mcpClient) post(body any) (int, http.Header, []byte) {
	c.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		c.t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range c.extra {
		req.Header[k] = v
	}
	res, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, out
}

var mcpID = 0

// rpc sends one JSON-RPC request and returns its result object.
func (c *mcpClient) rpc(method string, params any) (int, map[string]any) {
	c.t.Helper()
	mcpID++
	status, _, body := c.post(map[string]any{"jsonrpc": "2.0", "id": mcpID, "method": method, "params": params})
	if status != http.StatusOK {
		return status, nil
	}
	var res map[string]any
	if err := json.Unmarshal(body, &res); err != nil {
		c.t.Fatalf("response is not JSON: %s", body)
	}
	return status, res
}

// call runs a tool and returns (isError, parsed JSON text).
func (c *mcpClient) call(tool string, args any) (bool, map[string]any) {
	c.t.Helper()
	status, res := c.rpc("tools/call", map[string]any{"name": tool, "arguments": args})
	if status != http.StatusOK {
		c.t.Fatalf("tools/call %s: HTTP %d", tool, status)
	}
	result, ok := res["result"].(map[string]any)
	if !ok {
		c.t.Fatalf("tools/call %s: %v", tool, res)
	}
	content := result["content"].([]any)[0].(map[string]any)["text"].(string)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		c.t.Fatalf("tool text is not JSON: %s", content)
	}
	isErr, _ := result["isError"].(bool)
	return isErr, parsed
}

func (h *ChainHarness) newAdminToken(ctx context.Context, b *Browser, name string, days int32, scopes ...string) string {
	h.T.Helper()
	req := connect.NewRequest(&bffv1.CreateAccessTokenRequest{Name: name, Scopes: scopes, ExpiresInDays: days})
	b.Write(ctx, req)
	res, err := b.Tokens.CreateAccessToken(ctx, req)
	if err != nil {
		h.T.Fatalf("create token: %v", err)
	}
	return res.Msg.GetSecret()
}

func (h *ChainHarness) adminBrowser(ctx context.Context, sub string) (*Browser, string) {
	h.T.Helper()
	b := h.FreshBrowser()
	if err := b.LoginWithRoles(ctx, sub, "metro-admin"); err != nil {
		h.T.Fatal(err)
	}
	me, err := b.Me(ctx)
	if err != nil {
		h.T.Fatal(err)
	}
	return b, me.GetId()
}

func toolNames(t *testing.T, c *mcpClient) map[string]bool {
	t.Helper()
	status, res := c.rpc("tools/list", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("tools/list: HTTP %d", status)
	}
	names := map[string]bool{}
	for _, tl := range res["result"].(map[string]any)["tools"].([]any) {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	return names
}

// Acceptance 14 and 15: scopes decide the tool list, writes need metro:write, and a
// token stops working the moment it is revoked, expired, or missing.
func TestChainMCPTokensAndScopes(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	admin, _ := h.adminBrowser(ctx, "root")

	// Only administrators can create tokens.
	user := h.FreshBrowser()
	if err := user.LoginWithRoles(ctx, "reader"); err != nil {
		t.Fatal(err)
	}
	req := connect.NewRequest(&bffv1.CreateAccessTokenRequest{Name: "x", ExpiresInDays: 1})
	user.Write(ctx, req)
	if _, err := user.Tokens.CreateAccessToken(ctx, req); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-admin token creation: %v", err)
	}
	// A write needs CSRF like any other browser write.
	if _, err := admin.Tokens.CreateAccessToken(ctx, connect.NewRequest(&bffv1.CreateAccessTokenRequest{Name: "x", ExpiresInDays: 1})); err == nil {
		t.Fatal("token creation without CSRF succeeded")
	}
	// Validation: lifetime cap and unknown scope.
	for _, bad := range []*bffv1.CreateAccessTokenRequest{
		{Name: "x", ExpiresInDays: 91}, {Name: "x", ExpiresInDays: 0}, {Name: "", ExpiresInDays: 1},
		{Name: "x", ExpiresInDays: 1, Scopes: []string{"metro:root"}}, {Name: "x", ExpiresInDays: 1, Audience: "tripmap"},
	} {
		r := connect.NewRequest(bad)
		admin.Write(ctx, r)
		if _, err := admin.Tokens.CreateAccessToken(ctx, r); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("%+v: %v", bad, err)
		}
	}

	readSecret := h.newAdminToken(ctx, admin, "reader", 30)
	writeSecret := h.newAdminToken(ctx, admin, "writer", 30, "metro:write")
	if !strings.HasPrefix(readSecret, "mcp_") || len(readSecret) != 68 {
		t.Fatalf("secret shape: %q", readSecret)
	}

	// The list never contains the secret.
	list, err := admin.Tokens.ListAccessTokens(ctx, connect.NewRequest(&bffv1.ListAccessTokensRequest{}))
	if err != nil || len(list.Msg.Tokens) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
	if b, _ := json.Marshal(list.Msg); strings.Contains(string(b), readSecret) || strings.Contains(string(b), writeSecret) {
		t.Fatal("the list exposes a secret")
	}

	// Read-only token: the write tools are neither listed nor callable.
	ro := h.mcp(readSecret)
	names := toolNames(t, ro)
	if !names["metro_list_cities"] || !names["metro_export_catalog"] || names["metro_create_city"] || names["metro_import_catalog"] {
		t.Fatalf("read-only tool list: %v", names)
	}
	if isErr, out := ro.call("metro_create_city", map[string]any{"city": map[string]any{"slug": "x", "name": "x"}}); !isErr || !strings.Contains(out["error"].(map[string]any)["message"].(string), "metro:write") {
		t.Fatalf("read-only token wrote: %v", out)
	}
	if isErr, out := ro.call("metro_list_cities", map[string]any{}); isErr {
		t.Fatalf("read tool: %v", out)
	}
	rw := h.mcp(writeSecret)
	if names := toolNames(t, rw); !names["metro_create_city"] || !names["metro_list_cities"] || len(names) != 20 {
		t.Fatalf("write tool list (%d): %v", len(names), names)
	}

	// No token, a wrong token, and a valid Cookie without a Bearer all fail.
	if status, _ := h.mcp("").rpc("ping", nil); status != http.StatusUnauthorized {
		t.Fatalf("no token: %d", status)
	}
	if status, _ := h.mcp("mcp_"+strings.Repeat("0", 64)).rpc("ping", nil); status != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", status)
	}
	cookies := admin.HTTP.Jar.Cookies(mustURL(t, h.BFFURL))
	if len(cookies) == 0 {
		t.Fatal("the admin browser has no cookies")
	}
	withCookie := h.mcp("")
	for _, c := range cookies {
		withCookie.extra.Add("Cookie", c.Name+"="+c.Value)
	}
	if status, _ := withCookie.rpc("ping", nil); status != http.StatusUnauthorized {
		t.Fatalf("a session cookie authenticated the MCP endpoint: %d", status)
	}
	// Wrong method and content type.
	res, err := http.Get(h.BFFURL + "/mcp/metro")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("GET: %d %v", res.StatusCode, res.Header)
	}

	// Revocation is immediate (expiry is covered by the accesstoken package tests). The list is newest first, so the last
	// entry is the read-only token.
	oldest := list.Msg.Tokens[len(list.Msg.Tokens)-1].Id
	rev := connect.NewRequest(&bffv1.RevokeAccessTokenRequest{Id: oldest})
	admin.Write(ctx, rev)
	if _, err = admin.Tokens.RevokeAccessToken(ctx, rev); err != nil {
		t.Fatal(err)
	}
	if status, _ := ro.rpc("ping", nil); status != http.StatusUnauthorized {
		t.Fatalf("revoked token still works: %d", status)
	}
	if status, _ := rw.rpc("ping", nil); status != http.StatusOK {
		t.Fatalf("the other token stopped: %d", status)
	}
	// Revoking again succeeds; an unknown id does not.
	rev = connect.NewRequest(&bffv1.RevokeAccessTokenRequest{Id: oldest})
	admin.Write(ctx, rev)
	if _, err = admin.Tokens.RevokeAccessToken(ctx, rev); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	rev = connect.NewRequest(&bffv1.RevokeAccessTokenRequest{Id: "nope"})
	admin.Write(ctx, rev)
	if _, err = admin.Tokens.RevokeAccessToken(ctx, rev); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestChainMCPTokenCap(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	admin, _ := h.adminBrowser(ctx, "root")
	for i := 0; i < 10; i++ {
		h.newAdminToken(ctx, admin, "t", 1)
	}
	req := connect.NewRequest(&bffv1.CreateAccessTokenRequest{Name: "eleventh", ExpiresInDays: 1})
	admin.Write(ctx, req)
	if _, err := admin.Tokens.CreateAccessToken(ctx, req); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("eleventh token: %v", err)
	}
}

// Acceptance 14 (import preview), 16: MCP writes use the same rules as the web,
// audit as channel mcp with the token id, and conflict with web edits.
func TestChainMCPWritesShareTheAdminRules(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	admin, adminID := h.adminBrowser(ctx, "root")
	secret := h.newAdminToken(ctx, admin, "agent", 30, "metro:write")
	c := h.mcp(secret)

	// Create through MCP; field errors come back as readable details.
	if isErr, out := c.call("metro_create_city", map[string]any{"city": map[string]any{"slug": "Bad Slug", "name": "x"}}); !isErr ||
		!strings.Contains(string(mustJSON(t, out)), "slug") {
		t.Fatalf("invalid city: %v", out)
	}
	isErr, out := c.call("metro_create_city", map[string]any{"city": map[string]any{"slug": "shanghai", "name": "上海"}})
	if isErr {
		t.Fatalf("create city: %v", out)
	}
	cityID := out["city"].(map[string]any)["id"].(string)
	if isErr, out = c.call("metro_create_line", map[string]any{"line": map[string]any{
		"code": "sh-1", "citySlug": "shanghai", "name": "1号线", "number": 1, "color": "E4002B", "lengthKm": 36.9, "cars": 6,
	}}); !isErr || !strings.Contains(string(mustJSON(t, out)), "color") {
		t.Fatalf("color without # must fail with the field path: %v", out)
	}
	if isErr, out = c.call("metro_create_line", map[string]any{"line": map[string]any{
		"code": "sh-1", "citySlug": "shanghai", "name": "1号线", "number": 1, "color": "#E4002B", "lengthKm": 36.9, "cars": 6, "status": "LINE_STATUS_OPERATING",
	}}); isErr {
		t.Fatalf("create line: %v", out)
	}

	// get_entity returns the id and revision to use next.
	isErr, out = c.call("metro_get_entity", map[string]any{"entityType": "city", "key": "shanghai"})
	if isErr || out["id"] != cityID || out["revision"] != "1" {
		t.Fatalf("get_entity: %v", out)
	}
	if isErr, _ = c.call("metro_get_entity", map[string]any{"entityType": "city", "key": "nowhere"}); !isErr {
		t.Fatal("get_entity of a missing city succeeded")
	}

	// update and delete must carry expectedRevision.
	if isErr, out = c.call("metro_update_city", map[string]any{"id": cityID, "city": map[string]any{"slug": "shanghai", "name": "上海市"}}); !isErr ||
		!strings.Contains(string(mustJSON(t, out)), "expectedRevision") {
		t.Fatalf("update without a revision: %v", out)
	}
	if isErr, _ = c.call("metro_delete_city", map[string]any{"id": cityID}); !isErr {
		t.Fatal("delete without a revision succeeded")
	}
	// Referenced city: FAILED_PRECONDITION with counts.
	if isErr, out = c.call("metro_delete_city", map[string]any{"id": cityID, "expectedRevision": 1}); !isErr ||
		out["error"].(map[string]any)["code"] != "FAILED_PRECONDITION" || !strings.Contains(string(mustJSON(t, out)), "ReferencedDetail") {
		t.Fatalf("delete referenced city: %v", out)
	}

	// A web edit and an MCP edit of the same revision conflict.
	webReq := connect.NewRequest(&bffmetrov1.UpdateCityRequest{Id: cityID, ExpectedRevision: 1, City: &bffmetrov1.CityInput{Slug: "shanghai", Name: "上海（网页）"}})
	admin.Write(ctx, webReq)
	if _, err := admin.MetroAdmin.UpdateCity(ctx, webReq); err != nil {
		t.Fatal(err)
	}
	isErr, out = c.call("metro_update_city", map[string]any{"id": cityID, "expectedRevision": 1, "city": map[string]any{"slug": "shanghai", "name": "上海（MCP）"}})
	if !isErr || out["error"].(map[string]any)["code"] != "FAILED_PRECONDITION" || !strings.Contains(string(mustJSON(t, out)), "RevisionConflictDetail") {
		t.Fatalf("stale MCP update: %v", out)
	}
	if isErr, out = c.call("metro_update_city", map[string]any{"id": cityID, "expectedRevision": 2, "city": map[string]any{"slug": "shanghai", "name": "上海（MCP）"}}); isErr {
		t.Fatalf("MCP update at the current revision: %v", out)
	}

	// Import: dryRun defaults to true and writes nothing.
	doc := `{"format":"metro-catalog/1","cities":[{"slug":"beijing","name":"北京"}]}`
	isErr, out = c.call("metro_import_catalog", map[string]any{"catalogJson": doc})
	if isErr || out["dryRun"] != true {
		t.Fatalf("default import must be a dry run: %v", out)
	}
	if isErr, out = c.call("metro_get_entity", map[string]any{"entityType": "city", "key": "beijing"}); !isErr {
		t.Fatalf("a dry run created a city: %v", out)
	}
	if isErr, out = c.call("metro_import_catalog", map[string]any{"catalogJson": doc, "dryRun": false}); isErr || out["dryRun"] == true {
		t.Fatalf("explicit import: %v", out)
	}
	if isErr, out = c.call("metro_get_entity", map[string]any{"entityType": "city", "key": "beijing"}); isErr {
		t.Fatalf("import did not write: %v", out)
	}
	// Export contains both cities.
	if isErr, out = c.call("metro_export_catalog", map[string]any{}); isErr || !strings.Contains(out["catalogJson"].(string), "beijing") {
		t.Fatalf("export: %v", out)
	}

	// Unknown tool arguments are rejected instead of ignored.
	if isErr, out = c.call("metro_list_cities", map[string]any{"qurey": "x"}); !isErr {
		t.Fatalf("a misspelt argument was ignored: %v", out)
	}

	// The audit trail marks the channel and the token.
	logs, err := admin.MetroAdmin.ListAuditLogs(ctx, connect.NewRequest(&bffmetrov1.ListAuditLogsRequest{Channel: "mcp"}))
	if err != nil {
		t.Fatal(err)
	}
	row, err := h.BFFDB.AccessToken.Query().Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sawUpdate bool
	for _, l := range logs.Msg.Logs {
		if l.Channel != "mcp" || l.TokenId != row.ID || l.ActorUserId != adminID {
			t.Fatalf("audit row: %+v (token %s, actor %s)", l, row.ID, adminID)
		}
		sawUpdate = sawUpdate || l.Action == "city.update"
	}
	if !sawUpdate || len(logs.Msg.Logs) < 4 {
		t.Fatalf("audit rows of the MCP calls: %v", logs.Msg.Logs)
	}
	web, err := admin.MetroAdmin.ListAuditLogs(ctx, connect.NewRequest(&bffmetrov1.ListAuditLogsRequest{Channel: "web"}))
	if err != nil || len(web.Msg.Logs) != 1 || web.Msg.Logs[0].TokenId != "" {
		t.Fatalf("the web edit's audit row: %v %v", web, err)
	}
}

func TestChainMCPRateLimit(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{}, ChainHTTPOptions{Configure: func(cfg *bffconfig.Config, _ string) {
		cfg.MCP.RateLimitPerMinute = 5
	}})
	ctx := context.Background()
	admin, _ := h.adminBrowser(ctx, "root")
	c := h.mcp(h.newAdminToken(ctx, admin, "t", 1))
	for i := 0; i < 5; i++ {
		if status, _ := c.rpc("ping", nil); status != http.StatusOK {
			t.Fatalf("request %d: %d", i, status)
		}
	}
	status, hdr, _ := c.post(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "ping"})
	if status != http.StatusTooManyRequests || hdr.Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d %v", status, hdr)
	}
	// Another token has its own budget.
	other := h.mcp(h.newAdminToken(ctx, admin, "t2", 1))
	if status, _ := other.rpc("ping", nil); status != http.StatusOK {
		t.Fatalf("other token: %d", status)
	}
}

func TestChainMCPProtocol(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	admin, _ := h.adminBrowser(ctx, "root")
	c := h.mcp(h.newAdminToken(ctx, admin, "t", 1))

	status, res := c.rpc("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}})
	if status != http.StatusOK || res["result"].(map[string]any)["protocolVersion"] != "2025-03-26" {
		t.Fatalf("initialize: %v", res)
	}
	// Notifications get 202 and no body.
	code, _, body := c.post(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if code != http.StatusAccepted || len(body) != 0 {
		t.Fatalf("notification: %d %q", code, body)
	}
	if _, res = c.rpc("resources/list", nil); res["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("unknown method: %v", res)
	}
	if _, res = c.rpc("tools/call", map[string]any{"name": "metro_nope"}); res["error"].(map[string]any)["code"] != float64(-32602) {
		t.Fatalf("unknown tool: %v", res)
	}
	// Every tool publishes a JSON Schema derived from its request.
	_, list := c.rpc("tools/list", nil)
	for _, tl := range list["result"].(map[string]any)["tools"].([]any) {
		m := tl.(map[string]any)
		schema := m["inputSchema"].(map[string]any)
		if schema["type"] != "object" || m["description"] == "" {
			t.Fatalf("tool %v: %v", m["name"], m)
		}
	}
	// A batch answers each request and skips notifications.
	code, _, body = c.post([]any{
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"},
		map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
		map[string]any{"jsonrpc": "2.0", "id": 2, "method": "ping"},
	})
	var batch []map[string]any
	if code != http.StatusOK || json.Unmarshal(body, &batch) != nil || len(batch) != 2 {
		t.Fatalf("batch: %d %s", code, body)
	}
	// Garbage is a parse error, not a crash.
	req, _ := http.NewRequest(http.MethodPost, h.BFFURL+"/mcp/metro", strings.NewReader("{nope"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(b), "-32700") {
		t.Fatalf("garbage body: %d %s", r.StatusCode, b)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
