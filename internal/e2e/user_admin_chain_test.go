package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/adminguard"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/navigation"
	usersrvaccount "github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// The admin console's origin in these scenarios: a registered sibling of the other apps.
const adminOrigin = "http://localhost:5176"

// adminHarness starts the stack with the admin console registered and the given perimeter.
func adminHarness(t *testing.T, perimeter adminguard.Config, extras ...any) *ChainHarness {
	t.Helper()
	var mutate []func(*bffconfig.Config)
	var rest []any
	for _, e := range extras {
		if m, ok := e.(func(*bffconfig.Config)); ok {
			mutate = append(mutate, m)
		} else {
			rest = append(rest, e)
		}
	}
	opts := append([]any{ChainHTTPOptions{Configure: func(cfg *bffconfig.Config, baseURL string) {
		cfg.CORSAllowedOrigins = []string{ozidOrigin, tripOrigin, metroOrigin, adminOrigin}
		cfg.Navigation = navigation.Config{
			APIOrigin: baseURL,
			Apps: map[string]navigation.App{
				"ozid":     {Origin: ozidOrigin, Paths: []string{"/security"}},
				"admin":    {Origin: adminOrigin, Paths: []string{"/users", "/users/*"}},
				"trip-map": {Origin: tripOrigin},
				"metro":    {Origin: metroOrigin, Paths: []string{"/me", "/city/*"}},
			},
		}
		cfg.Admin = perimeter
		for _, m := range mutate {
			m(cfg)
		}
	}}}, rest...)
	return NewChainHarness(t, opts...)
}

var loopback = adminguard.Config{AllowedCIDRs: []string{"127.0.0.0/8"}}

// adminClient is the user-management client of the browser b, with the Origin the console would send.
func adminClient(b *Browser) bffv1connect.UserAdminServiceClient {
	return bffv1connect.NewUserAdminServiceClient(b.HTTP, b.APIURL)
}

// isPerimeterRefusal reports whether err is the perimeter's answer: the HTTP edge's bare 403 (which
// the Connect client reports as PERMISSION_DENIED) or the interceptor's generic "request rejected".
// Neither says which condition failed.
func isPerimeterRefusal(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "403 Forbidden") || strings.Contains(msg, "request rejected")
}

func withOrigin[T any](msg *T, origin string, extra ...string) *connect.Request[T] {
	req := connect.NewRequest(msg)
	if origin != "" {
		req.Header().Set("Origin", origin)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		req.Header().Set(extra[i], extra[i+1])
	}
	return req
}

func listAll(c bffv1connect.UserAdminServiceClient, msg *bffv1.ListUsersRequest) (*bffv1.ListUsersResponse, error) {
	res, err := c.ListUsers(context.Background(), withOrigin(msg, adminOrigin))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// The four roles of the spec's matrix, through the real BFF and the real usersrv.
func TestChainUserAdminRoleMatrix(t *testing.T) {
	h := adminHarness(t, loopback, ChainMetro{})
	ctx := context.Background()
	plain, ozidOnly, metroOnly, both := h.FreshBrowser(), h.FreshBrowser(), h.FreshBrowser(), h.FreshBrowser()
	for _, u := range []struct {
		b       *Browser
		subject string
		roles   []string
	}{
		{plain, "matrix_plain", nil},
		{ozidOnly, "matrix_ozid", []string{"ozid-admin"}},
		{metroOnly, "matrix_metro", []string{"metro-admin"}},
		{both, "matrix_both", []string{"ozid-admin", "metro-admin"}},
	} {
		if err := u.b.LoginWithRoles(ctx, u.subject, u.roles...); err != nil {
			t.Fatal(err)
		}
	}
	listUsers := func(b *Browser) error {
		_, err := adminClient(b).ListUsers(ctx, withOrigin(&bffv1.ListUsersRequest{}, adminOrigin))
		return err
	}
	listCities := func(b *Browser) error {
		_, err := b.MetroAdmin.ListAdminCities(ctx, connect.NewRequest(&bffmetrov1.ListAdminCitiesRequest{}))
		return err
	}

	if err := listUsers(h.FreshBrowser()); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous: %v", err)
	}
	for name, c := range map[string]struct {
		b            *Browser
		users, metro bool
	}{
		"plain user":         {plain, false, false},
		"only ozid-admin":    {ozidOnly, true, false},
		"only metro-admin":   {metroOnly, false, true},
		"ozid + metro admin": {both, true, true},
	} {
		check := func(what string, err error, allowed bool) {
			t.Helper()
			switch {
			case allowed && err != nil:
				t.Errorf("%s / %s: %v", name, what, err)
			case !allowed && connect.CodeOf(err) != connect.CodePermissionDenied:
				t.Errorf("%s / %s: want PERMISSION_DENIED, got %v", name, what, err)
			}
		}
		check("user management", listUsers(c.b), c.users)
		_, getErr := adminClient(c.b).GetUser(ctx, withOrigin(&bffv1.GetUserRequest{UserId: "00000000-0000-0000-0000-000000000001"}, adminOrigin))
		if c.users {
			if connect.CodeOf(getErr) != connect.CodeNotFound {
				t.Errorf("%s / get unknown: %v", name, getErr)
			}
		} else {
			check("user detail", getErr, false)
		}
		check("metro admin", listCities(c.b), c.metro)
	}
	// What the session says is what the section shows: Me lists the roles, nothing is inherited.
	for b, want := range map[*Browser]string{ozidOnly: "ozid-admin", metroOnly: "metro-admin"} {
		me, err := b.MeFull(ctx)
		if err != nil || len(me.GetRoles()) != 1 || me.GetRoles()[0] != want {
			t.Fatalf("roles of the session: %v %v (want only %s)", me, err, want)
		}
	}
}

// The console's own Origin is part of the perimeter: another origin, none at all, and
// even a registered product origin are refused, with the same answer as every other refusal.
func TestChainUserAdminDemandsTheConsoleOrigin(t *testing.T) {
	h := adminHarness(t, loopback)
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "origin_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	c := adminClient(admin)
	if _, err := c.ListUsers(ctx, withOrigin(&bffv1.ListUsersRequest{}, adminOrigin)); err != nil {
		t.Fatalf("the console's origin: %v", err)
	}
	for _, origin := range []string{"", tripOrigin, ozidOrigin, metroOrigin, siblingOrigin, "null", adminOrigin + "/", "http://localhost:5176.evil.example", "https://localhost:5176"} {
		_, err := c.ListUsers(ctx, withOrigin(&bffv1.ListUsersRequest{}, origin))
		if connect.CodeOf(err) != connect.CodePermissionDenied || !isPerimeterRefusal(err) {
			t.Errorf("origin %q: %v", origin, err)
		}
		_, err = c.GetUser(ctx, withOrigin(&bffv1.GetUserRequest{UserId: "00000000-0000-0000-0000-000000000001"}, origin))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("get with origin %q: %v", origin, err)
		}
	}
}

// Source policy: outside the allowlist the procedure answers nothing, for an administrator
// with a valid session too, and no header the client can write changes that.
func TestChainUserAdminSourcePolicy(t *testing.T) {
	ctx := context.Background()
	login := func(h *ChainHarness) *Browser {
		b := h.FreshBrowser()
		if err := b.LoginWithRoles(ctx, "ip_admin", "ozid-admin"); err != nil {
			t.Fatal(err)
		}
		return b
	}
	call := func(b *Browser, headers ...string) error {
		_, err := adminClient(b).ListUsers(ctx, withOrigin(&bffv1.ListUsersRequest{}, adminOrigin, headers...))
		return err
	}
	denied := func(what string, err error) {
		t.Helper()
		if connect.CodeOf(err) != connect.CodePermissionDenied || !isPerimeterRefusal(err) {
			t.Errorf("%s: want the generic refusal, got %v", what, err)
		}
	}
	forged := []string{"X-Forwarded-For", "192.0.2.5", "X-Real-IP", "192.0.2.5", "Forwarded", "for=192.0.2.5", "X-Client-IP", "192.0.2.5", "CF-Connecting-IP", "192.0.2.5", "True-Client-IP", "192.0.2.5"}

	t.Run("empty allowlist denies everything", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{})
		b := login(h)
		denied("empty allowlist", call(b))
		denied("empty allowlist with forged headers", call(b, forged...))
	})
	t.Run("a peer outside the list is denied and forged headers do not help", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
		b := login(h)
		denied("outside peer", call(b))
		denied("outside peer with forged forwarded headers", call(b, forged...))
		// Ordinary account features of the same user are untouched by the perimeter.
		if _, err := b.Me(ctx); err != nil {
			t.Fatalf("an ordinary call of the same session: %v", err)
		}
		if err := b.Reauthenticate(ctx, E2EPassword); err != nil {
			t.Fatalf("reauthenticate: %v", err)
		}
		if _, err := b.UpdateMyProfile(ctx, "Still Works", ""); err != nil {
			t.Fatalf("profile: %v", err)
		}
	})
	t.Run("the perimeter is checked before the session", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
		// An anonymous caller outside the list learns nothing about sessions: the same refusal.
		denied("anonymous outside", call(h.FreshBrowser()))
	})
	t.Run("a trusted proxy reports the client; the left of the chain is not believed", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"127.0.0.0/8"}})
		b := login(h)
		if err := call(b, "X-Forwarded-For", "192.0.2.5"); err != nil {
			t.Fatalf("client reported by the trusted proxy: %v", err)
		}
		if err := call(b, "X-Forwarded-For", "203.0.113.7, 192.0.2.5"); err != nil {
			t.Fatalf("a forged left entry must not matter when the proxy's own entry is allowed: %v", err)
		}
		denied("forged right-most client", call(b, "X-Forwarded-For", "192.0.2.5, 203.0.113.7"))
		denied("client outside the list", call(b, "X-Forwarded-For", "203.0.113.7"))
		denied("no forwarded header: the proxy itself", call(b))
		denied("garbage in the chain", call(b, "X-Forwarded-For", "192.0.2.5, not-an-ip"))
		denied("another header name", call(b, "X-Real-IP", "192.0.2.5"))
	})
	t.Run("a peer that is not a trusted proxy cannot use the header", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"10.0.0.0/8"}})
		denied("untrusted peer with a forwarded header", call(login(h), "X-Forwarded-For", "192.0.2.5"))
	})
	t.Run("preflight outside the allowlist gets no grant", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
		for _, procedure := range []string{"ListUsers", "GetUser"} {
			res := preflight(t, h, "/api/bff.v1.UserAdminService/"+procedure, adminOrigin)
			if res.StatusCode != http.StatusForbidden || res.Header.Get("Access-Control-Allow-Origin") != "" || res.Header.Get("Access-Control-Allow-Credentials") != "" {
				t.Errorf("%s preflight outside the list: %d %v", procedure, res.StatusCode, res.Header)
			}
		}
		// An ordinary procedure's preflight is unchanged.
		if res := preflight(t, h, "/api/bff.v1.AuthService/Me", ozidOrigin); res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != ozidOrigin {
			t.Errorf("auth preflight: %d %v", res.StatusCode, res.Header)
		}
	})
	t.Run("preflight inside the allowlist is answered for the console only", func(t *testing.T) {
		h := adminHarness(t, loopback)
		res := preflight(t, h, "/api/bff.v1.UserAdminService/ListUsers", adminOrigin)
		if res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != adminOrigin {
			t.Errorf("console preflight: %d %v", res.StatusCode, res.Header)
		}
		for _, origin := range []string{tripOrigin, ozidOrigin, siblingOrigin} {
			res = preflight(t, h, "/api/bff.v1.UserAdminService/ListUsers", origin)
			if res.StatusCode != http.StatusForbidden || res.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("preflight from %s: %d %v", origin, res.StatusCode, res.Header)
			}
		}
	})
	t.Run("another procedure path does not slip past", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
		for _, p := range []string{"/api/bff.v1.UserAdminService/ListUsers/", "/api//bff.v1.UserAdminService/ListUsers", "/api/bff.v1.UserAdminService/../UserAdminService/ListUsers", "/api/bff.v1.UserAdminService/%4cistUsers"} {
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

func preflight(t *testing.T, h *ChainHarness, path, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodOptions, h.BFFURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type,connect-protocol-version")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

// The real query chain: BFF → usersrv → PostgreSQL, with every filter and paging.
func TestChainUserAdminQueries(t *testing.T) {
	h := adminHarness(t, loopback)
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "query_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	c := adminClient(admin)
	ids := map[string]string{}
	for i := 0; i < 5; i++ {
		subject := fmt.Sprintf("member_%d", i)
		b := h.FreshBrowser()
		id, err := b.Register(ctx, subject, "", fmt.Sprintf("Member_%d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids[subject] = id
	}
	disabled, err := h.Accounts.ResolveEmail(ctx, emailFor("member_1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Accounts.SetDisabled(ctx, operator, disabled, true); err != nil {
		t.Fatal(err)
	}
	metro, _ := h.Accounts.ResolveEmail(ctx, emailFor("member_2"))
	if _, err = h.Accounts.GrantRole(ctx, operator, metro, "metro-admin"); err != nil {
		t.Fatal(err)
	}

	// Exact email, normalised; never a prefix.
	res, err := listAll(c, &bffv1.ListUsersRequest{Email: "  " + strings.ToUpper(emailFor("member_3")) + " "})
	if err != nil || len(res.Users) != 1 || res.Users[0].UserId != ids["member_3"] || res.Users[0].Email != emailFor("member_3") ||
		res.Users[0].DisplayName != "Member_3" || res.Users[0].Status != bffv1.UserStatus_USER_STATUS_ACTIVE || res.Users[0].CreatedAt == nil {
		t.Fatalf("email lookup: %v %v", res, err)
	}
	if res, err = listAll(c, &bffv1.ListUsersRequest{Email: "member_"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a partial email is not a lookup: %v %v", res, err)
	}
	if res, err = listAll(c, &bffv1.ListUsersRequest{Email: "nobody@example.com"}); err != nil || len(res.Users) != 0 || res.NextPageToken != "" {
		t.Fatalf("no match is a normal answer: %v %v", res, err)
	}
	// UUID lookup and detail.
	if res, err = listAll(c, &bffv1.ListUsersRequest{UserId: ids["member_0"]}); err != nil || len(res.Users) != 1 || res.Users[0].UserId != ids["member_0"] {
		t.Fatalf("uuid lookup: %v %v", res, err)
	}
	if _, err = listAll(c, &bffv1.ListUsersRequest{UserId: "member_0"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a malformed UUID: %v", err)
	}
	got, err := c.GetUser(ctx, withOrigin(&bffv1.GetUserRequest{UserId: ids["member_2"]}, adminOrigin))
	if err != nil || got.Msg.User.UserId != ids["member_2"] || len(got.Msg.User.Roles) != 1 || got.Msg.User.Roles[0] != "metro-admin" {
		t.Fatalf("detail: %v %v", got, err)
	}
	// Nickname: literal, case-insensitive substring; % and _ are ordinary characters.
	if res, err = listAll(c, &bffv1.ListUsersRequest{Nickname: "member_"}); err != nil || len(res.Users) != 5 {
		t.Fatalf("nickname: %v %v", res, err)
	}
	if res, err = listAll(c, &bffv1.ListUsersRequest{Nickname: "MEMBER_3"}); err != nil || len(res.Users) != 1 {
		t.Fatalf("nickname case: %v %v", res, err)
	}
	if res, err = listAll(c, &bffv1.ListUsersRequest{Nickname: "%"}); err != nil || len(res.Users) != 0 {
		t.Fatalf("a bare %% must not match everything: %v %v", res, err)
	}
	if res, err = listAll(c, &bffv1.ListUsersRequest{Nickname: "m_mber"}); err != nil || len(res.Users) != 0 {
		t.Fatalf("_ must not be a wildcard: %v %v", res, err)
	}
	// Status and role filters.
	if res, err = listAll(c, &bffv1.ListUsersRequest{Status: bffv1.UserStatus_USER_STATUS_DISABLED}); err != nil || len(res.Users) != 1 || res.Users[0].UserId != ids["member_1"] {
		t.Fatalf("status: %v %v", res, err)
	}
	if res, err = listAll(c, &bffv1.ListUsersRequest{Role: "ozid-admin"}); err != nil || len(res.Users) != 1 || res.Users[0].Roles[0] != "ozid-admin" {
		t.Fatalf("role: %v %v", res, err)
	}
	if _, err = listAll(c, &bffv1.ListUsersRequest{Role: "root"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("an unknown role: %v", err)
	}
	if _, err = listAll(c, &bffv1.ListUsersRequest{Email: emailFor("member_1"), Nickname: "x"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("two lookups: %v", err)
	}
	// Stable pagination: 6 accounts in pages of 2, each once, in a fixed order.
	seen := map[string]int{}
	var order []string
	token := ""
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("paging does not end")
		}
		res, err = listAll(c, &bffv1.ListUsersRequest{PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range res.Users {
			seen[u.UserId]++
			order = append(order, u.UserId)
		}
		if token = res.NextPageToken; token == "" {
			break
		}
	}
	if len(seen) != 6 || len(order) != 6 {
		t.Fatalf("paging: %v", order)
	}
	if res, err = listAll(c, &bffv1.ListUsersRequest{PageSize: 100000}); err != nil || len(res.Users) != 6 {
		t.Fatalf("an oversized page is clamped: %v %v", res, err)
	}
	if _, err = listAll(c, &bffv1.ListUsersRequest{PageToken: "garbage"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a forged page token: %v", err)
	}
	if _, err = listAll(c, &bffv1.ListUsersRequest{PageSize: -1}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a negative page size: %v", err)
	}
	if _, err = c.GetUser(ctx, withOrigin(&bffv1.GetUserRequest{UserId: "00000000-0000-0000-0000-0000000000aa"}, adminOrigin)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unknown account: %v", err)
	}

	// The audit trail names the operator and the request and holds no filter value or email.
	rows := h.UsersrvDB.SecurityAudit.Query().Where(securityaudit.ActionHasPrefix("admin.users.")).AllX(ctx)
	if len(rows) < 10 {
		t.Fatalf("audit rows: %d", len(rows))
	}
	operator, _ := h.Accounts.ResolveEmail(ctx, emailFor("query_admin"))
	for _, row := range rows {
		if row.Actor != "user:"+operator.String() || row.RequestID == "" || strings.Contains(row.Reason, "@") || strings.Contains(row.Reason, "member") || strings.Contains(row.Actor, "@") {
			t.Fatalf("audit row: %+v", row)
		}
	}
}

// Calls are limited per operator; a limit hit is RESOURCE_EXHAUSTED and later calls recover only
// with the window.
func TestChainUserAdminRateLimit(t *testing.T) {
	cfg := loopback
	cfg.RateLimitPerMinute = 3
	h := adminHarness(t, cfg)
	ctx := context.Background()
	admin, other := h.FreshBrowser(), h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "limit_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	if err := other.LoginWithRoles(ctx, "limit_other", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := listAll(adminClient(admin), &bffv1.ListUsersRequest{}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if _, err := listAll(adminClient(admin), &bffv1.ListUsersRequest{}); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("fourth call: %v", err)
	}
	// One operator's limit is not another's.
	if _, err := listAll(adminClient(other), &bffv1.ListUsersRequest{}); err != nil {
		t.Fatalf("another operator: %v", err)
	}
}

// A role change takes effect on the next call, and the platform role protects its account from the
// web deletion flow from the moment it exists.
func TestChainUserAdminRoleLifecycle(t *testing.T) {
	h := adminHarness(t, loopback)
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "life_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	me, err := admin.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = listAll(adminClient(admin), &bffv1.ListUsersRequest{}); err != nil {
		t.Fatal(err)
	}

	// Self-service deletion is refused and changes nothing, and does not leave a workflow behind.
	err = admin.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "life-1"}, me.Id)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "platform administrator") {
		t.Fatalf("deleting a platform administrator: %v", err)
	}
	if n := h.BFFDB.AccountDeletion.Query().CountX(ctx); n != 0 {
		t.Fatalf("a refused deletion left %d workflow rows", n)
	}
	if _, err = admin.Me(ctx); err != nil {
		t.Fatalf("the refused deletion closed the session: %v", err)
	}
	if _, err = listAll(adminClient(admin), &bffv1.ListUsersRequest{}); err != nil {
		t.Fatalf("the refused deletion changed the role: %v", err)
	}
	if err = admin.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "life-2"}, me.Id); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a second attempt: %v", err)
	}

	// Revoking the role is an operator action; it ends the old session's access at once.
	id, _ := h.Accounts.ResolveEmail(ctx, emailFor("life_admin"))
	if _, err = h.Accounts.RevokeRole(ctx, operator, id, "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	// The refusal was final: revoking the role does not close the account by itself. No workflow row
	// is left for a runner to finish, and the account is still active until a new request says otherwise.
	if n := h.BFFDB.AccountDeletion.Query().CountX(ctx); n != 0 {
		t.Fatalf("revoking the role left %d deletion workflow rows", n)
	}
	if st, serr := h.Accounts.GetAuthState(ctx, id); serr != nil || st.Status != usersrvaccount.StatusActive {
		t.Fatalf("revoking the role changed the account: %+v %v", st, serr)
	}
	if _, err = listAll(adminClient(admin), &bffv1.ListUsersRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a session issued before the revocation: %v", err)
	}
	again := h.FreshBrowser()
	if err = again.Login(ctx, "life_admin", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = listAll(adminClient(again), &bffv1.ListUsersRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("after revocation: %v", err)
	}
	// Without the role the account is an ordinary one again.
	if err = again.DeleteAccount(ctx, &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "life-3"}, id.String()); err != nil {
		t.Fatalf("deleting after the revocation: %v", err)
	}
}

// An administrator whose session is older than the admin session limit has to sign in again.
func TestChainUserAdminNeedsARecentPassword(t *testing.T) {
	h := adminHarness(t, loopback, func(cfg *bffconfig.Config) { cfg.Auth.AdminSessionMaxAge = 1 })
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "stale_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	_, err := listAll(adminClient(admin), &bffv1.ListUsersRequest{})
	if connect.CodeOf(err) != connect.CodeUnauthenticated || !strings.Contains(err.Error(), "sign in again") {
		t.Fatalf("an old session: %v", err)
	}
	if err = admin.Reauthenticate(ctx, E2EPassword); err != nil {
		t.Fatal(err)
	}
}
