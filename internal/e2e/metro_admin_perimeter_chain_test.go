package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/adminguard"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	"github.com/kongken/mono/pkg/proto/bff/metro/v1/bffmetrov1connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
	"google.golang.org/grpc"
)

// The browser management perimeter covers the metro administration and the management tokens, not only
// the user administration: reads, writes, preflight and the other path spellings, all before the session,
// the role and the upstream. MCP keeps its own protocol and is not touched here (its machine allowlist is
// a separate ticket).

// countMetroAdmin counts the calls that reach metro's admin service, whoever made them.
func countMetroAdmin(n *atomic.Int64) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if strings.HasPrefix(info.FullMethod, "/metro.v1.AdminService/") {
			n.Add(1)
		}
		return next(ctx, req)
	}
}

// perimeterCall is one browser-reachable procedure of the metro administration or the token service.
type perimeterCall struct {
	name  string
	write bool
	run   func(ctx context.Context, b *Browser, origin string, headers ...string) error
}

func setOrigin(h http.Header, origin string, headers []string) {
	if origin == "" {
		h.Del("Origin")
	} else {
		h.Set("Origin", origin)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		h.Set(headers[i], headers[i+1])
	}
}

// metroPerimeterCalls lists every kind of call (reads, writes, import and export, audit, tokens). The
// clients are plain ones, so each call states its own Origin and headers.
func metroPerimeterCalls() []perimeterCall {
	admin := func(b *Browser) bffmetrov1connect.AdminServiceClient {
		return bffmetrov1connect.NewAdminServiceClient(b.HTTP, b.APIURL)
	}
	tokens := func(b *Browser) bffv1connect.AccessTokenServiceClient {
		return bffv1connect.NewAccessTokenServiceClient(b.HTTP, b.APIURL)
	}
	const someID = "00000000-0000-0000-0000-000000000001"
	read := func(name string, f func(ctx context.Context, b *Browser, origin string, headers []string) error) perimeterCall {
		return perimeterCall{name: name, run: func(ctx context.Context, b *Browser, origin string, headers ...string) error {
			return f(ctx, b, origin, headers)
		}}
	}
	write := func(name string, f func(ctx context.Context, b *Browser, origin string, headers []string) error) perimeterCall {
		return perimeterCall{name: name, write: true, run: func(ctx context.Context, b *Browser, origin string, headers ...string) error {
			return f(ctx, b, origin, headers)
		}}
	}
	return []perimeterCall{
		read("ListAdminCities", func(ctx context.Context, b *Browser, o string, h []string) error {
			_, err := admin(b).ListAdminCities(ctx, withOrigin(&bffmetrov1.ListAdminCitiesRequest{}, o, h...))
			return err
		}),
		read("ListAdminLines", func(ctx context.Context, b *Browser, o string, h []string) error {
			_, err := admin(b).ListAdminLines(ctx, withOrigin(&bffmetrov1.ListAdminLinesRequest{}, o, h...))
			return err
		}),
		read("ListAdminRollingStocks", func(ctx context.Context, b *Browser, o string, h []string) error {
			_, err := admin(b).ListAdminRollingStocks(ctx, withOrigin(&bffmetrov1.ListAdminRollingStocksRequest{}, o, h...))
			return err
		}),
		read("ListAdminRollingStockTopics", func(ctx context.Context, b *Browser, o string, h []string) error {
			_, err := admin(b).ListAdminRollingStockTopics(ctx, withOrigin(&bffmetrov1.ListAdminRollingStockTopicsRequest{}, o, h...))
			return err
		}),
		read("ExportCatalog", func(ctx context.Context, b *Browser, o string, h []string) error {
			_, err := admin(b).ExportCatalog(ctx, withOrigin(&bffmetrov1.ExportCatalogRequest{}, o, h...))
			return err
		}),
		read("ListAuditLogs", func(ctx context.Context, b *Browser, o string, h []string) error {
			_, err := admin(b).ListAuditLogs(ctx, withOrigin(&bffmetrov1.ListAuditLogsRequest{}, o, h...))
			return err
		}),
		read("ListAccessTokens", func(ctx context.Context, b *Browser, o string, h []string) error {
			_, err := tokens(b).ListAccessTokens(ctx, withOrigin(&bffv1.ListAccessTokensRequest{}, o, h...))
			return err
		}),
		write("CreateCity", func(ctx context.Context, b *Browser, o string, h []string) error {
			req := connect.NewRequest(&bffmetrov1.CreateCityRequest{City: &bffmetrov1.CityInput{Slug: "perimeter", Name: "边界"}})
			b.Write(ctx, req)
			setOrigin(req.Header(), o, h)
			_, err := admin(b).CreateCity(ctx, req)
			return err
		}),
		write("DeleteCity", func(ctx context.Context, b *Browser, o string, h []string) error {
			req := connect.NewRequest(&bffmetrov1.DeleteCityRequest{Id: someID, ExpectedRevision: 1})
			b.Write(ctx, req)
			setOrigin(req.Header(), o, h)
			_, err := admin(b).DeleteCity(ctx, req)
			return err
		}),
		write("ImportCatalog", func(ctx context.Context, b *Browser, o string, h []string) error {
			// A dry run is a read of the document, and is inside the perimeter like the real import.
			req := connect.NewRequest(&bffmetrov1.ImportCatalogRequest{CatalogJson: `{"format":"metro-catalog/1"}`, DryRun: true})
			b.Write(ctx, req)
			setOrigin(req.Header(), o, h)
			_, err := admin(b).ImportCatalog(ctx, req)
			return err
		}),
		write("CreateAccessToken", func(ctx context.Context, b *Browser, o string, h []string) error {
			req := connect.NewRequest(&bffv1.CreateAccessTokenRequest{Name: "perimeter", ExpiresInDays: 1})
			b.Write(ctx, req)
			setOrigin(req.Header(), o, h)
			_, err := tokens(b).CreateAccessToken(ctx, req)
			return err
		}),
		write("RevokeAccessToken", func(ctx context.Context, b *Browser, o string, h []string) error {
			req := connect.NewRequest(&bffv1.RevokeAccessTokenRequest{Id: someID})
			b.Write(ctx, req)
			setOrigin(req.Header(), o, h)
			_, err := tokens(b).RevokeAccessToken(ctx, req)
			return err
		}),
	}
}

func TestChainMetroAdminPerimeterSourcePolicy(t *testing.T) {
	ctx := context.Background()
	calls := metroPerimeterCalls()
	forged := []string{"X-Forwarded-For", "192.0.2.5", "X-Real-IP", "192.0.2.5", "Forwarded", "for=192.0.2.5", "X-Client-IP", "192.0.2.5", "CF-Connecting-IP", "192.0.2.5", "True-Client-IP", "192.0.2.5"}
	denied := func(t *testing.T, what string, err error) {
		t.Helper()
		if connect.CodeOf(err) != connect.CodePermissionDenied || !isPerimeterRefusal(err) {
			t.Errorf("%s: want the generic refusal, got %v", what, err)
		}
	}
	setup := func(t *testing.T, perimeter adminguard.Config) (*ChainHarness, *Browser, *atomic.Int64) {
		t.Helper()
		var reached atomic.Int64
		h := adminHarness(t, perimeter, ChainMetro{}, ChainMetroUnary{countMetroAdmin(&reached)})
		b := h.FreshBrowser()
		if err := b.LoginWithRoles(ctx, "perimeter_admin", "metro-admin"); err != nil {
			t.Fatal(err)
		}
		return h, b, &reached
	}
	// nothingHappened is the proof that a refused request never reached the business: metro's admin
	// service was not called and no token was created.
	nothingHappened := func(t *testing.T, h *ChainHarness, reached *atomic.Int64) {
		t.Helper()
		if n := reached.Load(); n != 0 {
			t.Errorf("metro's admin service was reached %d times", n)
		}
		if n, err := h.BFFDB.AccessToken.Query().Count(ctx); err != nil || n != 0 {
			t.Errorf("tokens created: %d %v", n, err)
		}
		if n, err := h.MetroDB.City.Query().Count(ctx); err != nil || n != 0 {
			t.Errorf("cities created: %d %v", n, err)
		}
	}

	t.Run("an empty allowlist denies every metro and token procedure, forged headers too", func(t *testing.T) {
		h, b, reached := setup(t, adminguard.Config{})
		for _, c := range calls {
			denied(t, c.name, c.run(ctx, b, adminOrigin))
			denied(t, c.name+" with forged headers", c.run(ctx, b, adminOrigin, forged...))
		}
		nothingHappened(t, h, reached)
	})
	t.Run("a peer outside the list is denied and forged headers do not help; ordinary features are untouched", func(t *testing.T) {
		h, b, reached := setup(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
		for _, c := range calls {
			denied(t, c.name, c.run(ctx, b, adminOrigin))
			denied(t, c.name+" with forged headers", c.run(ctx, b, adminOrigin, forged...))
		}
		nothingHappened(t, h, reached)
		if _, err := b.Me(ctx); err != nil {
			t.Fatalf("an ordinary call of the same session: %v", err)
		}
		// The public catalogue is a product feature, outside the perimeter.
		if _, err := h.FreshBrowser().Catalog.ListCities(ctx, connect.NewRequest(&bffmetrov1.ListCitiesRequest{})); err != nil {
			t.Fatalf("the public catalogue: %v", err)
		}
		if err := b.Reauthenticate(ctx, E2EPassword); err != nil {
			t.Fatalf("reauthenticate: %v", err)
		}
	})
	t.Run("the perimeter is checked before the session and before the role", func(t *testing.T) {
		var reached atomic.Int64
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}}, ChainMetro{}, ChainMetroUnary{countMetroAdmin(&reached)})
		plain := h.FreshBrowser()
		if err := plain.LoginWithRoles(ctx, "perimeter_plain"); err != nil {
			t.Fatal(err)
		}
		for _, c := range calls {
			// An anonymous caller and a signed-in user without the role learn nothing about either:
			// the same refusal as every other one.
			denied(t, c.name+" anonymous", c.run(ctx, h.FreshBrowser(), adminOrigin))
			denied(t, c.name+" without the role", c.run(ctx, plain, adminOrigin))
		}
		nothingHappened(t, h, &reached)
	})
	t.Run("a trusted proxy reports the client; the left of the chain is not believed", func(t *testing.T) {
		h, b, reached := setup(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"127.0.0.0/8"}})
		for _, c := range calls {
			if c.write {
				continue // writes are shown to pass the perimeter below, where their effect can be checked
			}
			if err := c.run(ctx, b, adminOrigin, "X-Forwarded-For", "192.0.2.5"); err != nil {
				t.Fatalf("%s with the client reported by the trusted proxy: %v", c.name, err)
			}
			if err := c.run(ctx, b, adminOrigin, "X-Forwarded-For", "203.0.113.7, 192.0.2.5"); err != nil {
				t.Fatalf("%s: a forged left entry must not matter: %v", c.name, err)
			}
			denied(t, c.name+" forged right-most client", c.run(ctx, b, adminOrigin, "X-Forwarded-For", "192.0.2.5, 203.0.113.7"))
			denied(t, c.name+" client outside the list", c.run(ctx, b, adminOrigin, "X-Forwarded-For", "203.0.113.7"))
			denied(t, c.name+" no forwarded header: the proxy itself", c.run(ctx, b, adminOrigin))
			denied(t, c.name+" garbage in the chain", c.run(ctx, b, adminOrigin, "X-Forwarded-For", "192.0.2.5, not-an-ip"))
			denied(t, c.name+" another header name", c.run(ctx, b, adminOrigin, "X-Real-IP", "192.0.2.5"))
		}
		// A refused write leaves nothing behind; the same write from an allowed client does its work.
		for _, c := range calls {
			if c.name == "CreateCity" {
				denied(t, "CreateCity outside", c.run(ctx, b, adminOrigin, "X-Forwarded-For", "203.0.113.7"))
				if n, _ := h.MetroDB.City.Query().Count(ctx); n != 0 {
					t.Fatalf("a refused write created %d cities", n)
				}
				if err := c.run(ctx, b, adminOrigin, "X-Forwarded-For", "192.0.2.5"); err != nil {
					t.Fatalf("CreateCity inside: %v", err)
				}
				if n, _ := h.MetroDB.City.Query().Count(ctx); n != 1 {
					t.Fatalf("an allowed write created %d cities", n)
				}
			}
		}
		if reached.Load() == 0 {
			t.Fatal("the allowed calls never reached metro: the scenario proves nothing")
		}
	})
	t.Run("a peer that is not a trusted proxy cannot use the header", func(t *testing.T) {
		h, b, reached := setup(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"10.0.0.0/8"}})
		for _, c := range calls {
			denied(t, c.name, c.run(ctx, b, adminOrigin, "X-Forwarded-For", "192.0.2.5"))
		}
		nothingHappened(t, h, reached)
	})
}

// The console's own Origin is part of the perimeter for every one of these procedures: another origin,
// none at all, even a registered product origin, are refused with the same answer, and nothing happens.
func TestChainMetroAdminPerimeterDemandsTheConsoleOrigin(t *testing.T) {
	ctx := context.Background()
	var reached atomic.Int64
	h := adminHarness(t, loopback, ChainMetro{}, ChainMetroUnary{countMetroAdmin(&reached)})
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "origin_metro_admin", "metro-admin"); err != nil {
		t.Fatal(err)
	}
	for _, c := range metroPerimeterCalls() {
		// The write calls may fail on their own rules (an unknown record); what matters is that they are
		// not the perimeter's refusal when the Origin is the console's.
		if err := c.run(ctx, admin, adminOrigin); err != nil && isPerimeterRefusal(err) {
			t.Errorf("%s with the console's origin: %v", c.name, err)
		}
	}
	before, tokensBefore := reached.Load(), count(t)(h.BFFDB.AccessToken.Query().Count(ctx))
	cities := count(t)(h.MetroDB.City.Query().Count(ctx))
	for _, origin := range []string{"", tripOrigin, ozidOrigin, metroOrigin, siblingOrigin, "null", adminOrigin + "/", "http://localhost:5176.evil.example", "https://localhost:5176"} {
		for _, c := range metroPerimeterCalls() {
			err := c.run(ctx, admin, origin)
			if connect.CodeOf(err) != connect.CodePermissionDenied || !isPerimeterRefusal(err) {
				t.Errorf("%s with origin %q: %v", c.name, origin, err)
			}
		}
	}
	if n := reached.Load(); n != before {
		t.Errorf("a refused origin reached metro: %d calls", n-before)
	}
	if n := count(t)(h.BFFDB.AccessToken.Query().Count(ctx)); n != tokensBefore {
		t.Errorf("a refused origin changed the tokens: %d -> %d", tokensBefore, n)
	}
	if n := count(t)(h.MetroDB.City.Query().Count(ctx)); n != cities {
		t.Errorf("a refused origin changed the cities: %d -> %d", cities, n)
	}
}

// count unwraps a Count() result: count(t)(client.Query().Count(ctx)).
func count(t *testing.T) func(int, error) int {
	return func(n int, err error) int {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
}

// Preflight and path spellings: outside the allowlist no grant is given for any of the services, the
// console's origin is answered inside, and nothing slips past with another spelling of the path.
func TestChainMetroAdminPerimeterPreflightAndPaths(t *testing.T) {
	paths := []string{
		"/api/bff.metro.v1.AdminService/ListAdminCities",
		"/api/bff.metro.v1.AdminService/ImportCatalog",
		"/api/bff.metro.v1.AdminService/ExportCatalog",
		"/api/bff.metro.v1.AdminService/ListAuditLogs",
		"/api/bff.v1.AccessTokenService/ListAccessTokens",
		"/api/bff.v1.AccessTokenService/CreateAccessToken",
		"/api/bff.v1.AccessTokenService/RevokeAccessToken",
	}
	t.Run("preflight outside the allowlist gets no grant", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}}, ChainMetro{})
		for _, p := range paths {
			res := preflight(t, h, p, adminOrigin)
			if res.StatusCode != http.StatusForbidden || res.Header.Get("Access-Control-Allow-Origin") != "" || res.Header.Get("Access-Control-Allow-Credentials") != "" {
				t.Errorf("%s preflight outside the list: %d %v", p, res.StatusCode, res.Header)
			}
		}
		// The public catalogue's preflight is unchanged.
		if res := preflight(t, h, "/api/bff.metro.v1.CatalogService/ListCities", metroOrigin); res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != metroOrigin {
			t.Errorf("catalogue preflight: %d %v", res.StatusCode, res.Header)
		}
	})
	t.Run("preflight inside the allowlist is answered for the console only", func(t *testing.T) {
		h := adminHarness(t, loopback, ChainMetro{})
		for _, p := range paths {
			if res := preflight(t, h, p, adminOrigin); res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != adminOrigin {
				t.Errorf("%s console preflight: %d %v", p, res.StatusCode, res.Header)
			}
			for _, origin := range []string{tripOrigin, ozidOrigin, metroOrigin, siblingOrigin} {
				if res := preflight(t, h, p, origin); res.StatusCode != http.StatusForbidden || res.Header.Get("Access-Control-Allow-Origin") != "" {
					t.Errorf("%s preflight from %s: %d %v", p, origin, res.StatusCode, res.Header)
				}
			}
		}
	})
	t.Run("another procedure path does not slip past", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}}, ChainMetro{})
		variants := []string{
			"/api/bff.metro.v1.AdminService/ListAdminCities/", "/api//bff.metro.v1.AdminService/ListAdminCities",
			"/api/bff.metro.v1.AdminService/../AdminService/ListAdminCities", "/api/bff.metro.v1.AdminService/%4cistAdminCities",
			"/api/bff.metro.v1.AdminService/ExportCatalog/", "/api/bff.v1.AccessTokenService/ListAccessTokens/",
			"/api//bff.v1.AccessTokenService/ListAccessTokens", "/api/bff.v1.AccessTokenService/%4cistAccessTokens",
			"/api/bff.v1.AccessTokenService/../AccessTokenService/CreateAccessToken",
		}
		for _, p := range variants {
			req, _ := http.NewRequest(http.MethodPost, h.BFFURL+p, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Connect-Protocol-Version", "1")
			req.Header.Set("Origin", adminOrigin)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				t.Errorf("%s answered 200 outside the allowlist", p)
			}
		}
	})
}

// The metro administration needs the metro role in the BFF and again inside metro; a platform-only
// administrator is refused by the role, with nothing reaching metro.
func TestChainMetroAdminRolesAndReauthentication(t *testing.T) {
	ctx := context.Background()
	var reached atomic.Int64
	h := adminHarness(t, loopback, ChainMetro{}, ChainMetroUnary{countMetroAdmin(&reached)})
	platform, metroAdmin := h.FreshBrowser(), h.FreshBrowser()
	if err := platform.LoginWithRoles(ctx, "platform_only", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	if err := metroAdmin.LoginWithRoles(ctx, "metro_only", "metro-admin"); err != nil {
		t.Fatal(err)
	}
	for _, c := range metroPerimeterCalls() {
		if err := c.run(ctx, platform, adminOrigin); connect.CodeOf(err) != connect.CodePermissionDenied || isPerimeterRefusal(err) {
			t.Errorf("%s as a platform-only administrator: want the role's refusal, got %v", c.name, err)
		}
	}
	if n := reached.Load(); n != 0 {
		t.Errorf("a platform-only administrator reached metro %d times", n)
	}
	if n := count(t)(h.BFFDB.AccessToken.Query().Count(ctx)); n != 0 {
		t.Errorf("a platform-only administrator created %d tokens", n)
	}
	// And the other way round: the metro role does not open the user administration.
	if _, err := adminClient(metroAdmin).ListUsers(ctx, withOrigin(&bffv1.ListUsersRequest{}, adminOrigin)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("metro-admin on the user administration: %v", err)
	}

	// An ordinary catalogue edit asks for no password check, however old it is; the token does.
	h.ageSession(metroAdmin, 16*time.Minute)
	cl := bffmetrov1connect.NewAdminServiceClient(metroAdmin.HTTP, metroAdmin.APIURL)
	req := connect.NewRequest(&bffmetrov1.CreateCityRequest{City: &bffmetrov1.CityInput{Slug: "no-password", Name: "无需密码"}})
	metroAdmin.Write(ctx, req)
	req.Header().Set("Origin", adminOrigin)
	if _, err := cl.CreateCity(ctx, req); err != nil {
		t.Fatalf("a catalogue edit with a 16-minute-old password check: %v", err)
	}
	tr := connect.NewRequest(&bffv1.CreateAccessTokenRequest{Name: "late", ExpiresInDays: 1})
	metroAdmin.Write(ctx, tr)
	tr.Header().Set("Origin", adminOrigin)
	if _, err := bffv1connect.NewAccessTokenServiceClient(metroAdmin.HTTP, metroAdmin.APIURL).CreateAccessToken(ctx, tr); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a token with a stale password check: %v", err)
	}
	if n := count(t)(h.BFFDB.AccessToken.Query().Count(ctx)); n != 0 {
		t.Errorf("a refused token creation left %d tokens", n)
	}
}

// Without a metro upstream the BFF offers no metro administration at all (the console shows no section);
// the user administration and the token service are unaffected.
func TestChainMetroAdminAbsentWithoutAnUpstream(t *testing.T) {
	ctx := context.Background()
	h := adminHarness(t, loopback)
	b := h.FreshBrowser()
	if err := b.LoginWithRoles(ctx, "no_metro_admin", "metro-admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	_, err := bffmetrov1connect.NewAdminServiceClient(b.HTTP, b.APIURL).ListAdminCities(ctx, withOrigin(&bffmetrov1.ListAdminCitiesRequest{}, adminOrigin))
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("no metro behind the BFF: %v", err)
	}
	if _, err := adminClient(b).ListUsers(ctx, withOrigin(&bffv1.ListUsersRequest{}, adminOrigin)); err != nil {
		t.Fatalf("the user administration: %v", err)
	}
}

// MCP is a machine protocol with its own bearer token; this ticket's browser perimeter does not touch it.
func TestChainMetroAdminPerimeterLeavesMCPAlone(t *testing.T) {
	ctx := context.Background()
	h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"127.0.0.0/8"}}, ChainMetro{})
	b := h.FreshBrowser()
	if err := b.LoginWithRoles(ctx, "mcp_alone", "metro-admin"); err != nil {
		t.Fatal(err)
	}
	// The token is made from an address inside the list (as the console would).
	req := connect.NewRequest(&bffv1.CreateAccessTokenRequest{Name: "mcp", Scopes: []string{"metro:read"}, ExpiresInDays: 1})
	b.Write(ctx, req)
	req.Header().Set("Origin", adminOrigin)
	req.Header().Set("X-Forwarded-For", "192.0.2.5")
	res, err := bffv1connect.NewAccessTokenServiceClient(b.HTTP, b.APIURL).CreateAccessToken(ctx, req)
	if err != nil {
		t.Fatalf("create token inside the perimeter: %v", err)
	}
	// The MCP caller is outside the browser list (the peer is the loopback proxy itself) and still served.
	c := h.mcp(res.Msg.GetSecret())
	if status, _ := c.rpc("ping", nil); status != http.StatusOK {
		t.Fatalf("an MCP call from outside the browser allowlist: HTTP %d", status)
	}
	if !toolNames(t, c)["metro_list_cities"] {
		t.Fatal("MCP tools are not served")
	}
}
