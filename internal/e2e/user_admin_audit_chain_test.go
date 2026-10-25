package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/adminguard"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	usersrvaccount "github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// auditPage reads one page of the account audit the way the console does: its own Origin, no CSRF token
// (a read), the session Cookie of b.
func auditPage(ctx context.Context, b *Browser, msg *bffv1.ListSecurityAuditRequest, headers ...string) (*bffv1.ListSecurityAuditResponse, error) {
	res, err := adminClient(b).ListSecurityAudit(ctx, withOrigin(msg, adminOrigin, headers...))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// auditAll follows the page tokens of one filter to the end and fails on a repeated row.
func auditAll(ctx context.Context, t *testing.T, b *Browser, f *bffv1.ListSecurityAuditRequest, size int32) []*bffv1.SecurityAuditEntry {
	t.Helper()
	var out []*bffv1.SecurityAuditEntry
	seen := map[string]bool{}
	token := ""
	for i := 0; i < 100; i++ {
		req := &bffv1.ListSecurityAuditRequest{
			UserId: f.UserId, OperatorUserId: f.OperatorUserId, Channel: f.Channel, Action: f.Action, Result: f.Result,
			MutationId: f.MutationId, From: f.From, To: f.To, PageSize: size, PageToken: token,
		}
		page, err := auditPage(ctx, b, req)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Entries {
			if seen[e.Id] {
				t.Fatalf("row %s appears twice", e.Id)
			}
			seen[e.Id] = true
			out = append(out, e)
		}
		if token = page.NextPageToken; token == "" {
			return out
		}
	}
	t.Fatal("paging does not end")
	return nil
}

// Who may read the account audit, through the real BFF and usersrv: the platform role and nothing else.
// metro-admin reads the metro audit and is refused here, and the two stay separate stores.
func TestChainUserAuditRoleMatrix(t *testing.T) {
	h := adminHarness(t, loopback, ChainMetro{})
	ctx := context.Background()
	plain, ozidOnly, metroOnly, both := h.FreshBrowser(), h.FreshBrowser(), h.FreshBrowser(), h.FreshBrowser()
	for _, u := range []struct {
		b       *Browser
		subject string
		roles   []string
	}{
		{plain, "am_plain", nil}, {ozidOnly, "am_ozid", []string{"ozid-admin"}},
		{metroOnly, "am_metro", []string{"metro-admin"}}, {both, "am_both", []string{"ozid-admin", "metro-admin"}},
	} {
		if err := u.b.LoginWithRoles(ctx, u.subject, u.roles...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := auditPage(ctx, h.FreshBrowser(), &bffv1.ListSecurityAuditRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous: %v", err)
	}
	for name, c := range map[string]struct {
		b       *Browser
		account bool
		metro   bool
	}{
		"plain user": {plain, false, false}, "only ozid-admin": {ozidOnly, true, false},
		"only metro-admin": {metroOnly, false, true}, "ozid + metro admin": {both, true, true},
	} {
		page, err := auditPage(ctx, c.b, &bffv1.ListSecurityAuditRequest{})
		switch {
		case c.account && err != nil:
			t.Errorf("%s: account audit: %v", name, err)
		case !c.account && (connect.CodeOf(err) != connect.CodePermissionDenied || page != nil):
			t.Errorf("%s: account audit: want PERMISSION_DENIED and no data, got %v %v", name, page, err)
		}
		_, err = c.b.MetroAdmin.ListAuditLogs(ctx, connect.NewRequest(&bffmetrov1.ListAuditLogsRequest{}))
		switch {
		case c.metro && err != nil:
			t.Errorf("%s: metro audit: %v", name, err)
		case !c.metro && connect.CodeOf(err) != connect.CodePermissionDenied:
			t.Errorf("%s: metro audit: want PERMISSION_DENIED, got %v", name, err)
		}
	}
	// The refusals of the account audit are recorded by usersrv (the BFF refused the others before usersrv).
	if n := h.UsersrvDB.SecurityAudit.Query().Where(securityaudit.Action("admin.audit.list"), securityaudit.Result("ok")).CountX(ctx); n != 2 {
		t.Fatalf("%d successful audit reads recorded, want 2", n)
	}
}

// Every layer of the perimeter applies to the audit read like to every other management method: source
// network, the console's exact Origin, a recent password, the session; a CSRF token is not needed (a read).
func TestChainUserAuditPerimeterAndSession(t *testing.T) {
	ctx := context.Background()
	login := func(h *ChainHarness) *Browser {
		b := h.FreshBrowser()
		if err := b.LoginWithRoles(ctx, "ap_admin", "ozid-admin"); err != nil {
			t.Fatal(err)
		}
		return b
	}
	denied := func(what string, err error) {
		t.Helper()
		if connect.CodeOf(err) != connect.CodePermissionDenied || !isPerimeterRefusal(err) {
			t.Errorf("%s: want the generic refusal, got %v", what, err)
		}
	}
	forged := []string{"X-Forwarded-For", "192.0.2.5", "X-Real-IP", "192.0.2.5", "Forwarded", "for=192.0.2.5"}

	t.Run("inside the allowlist, without a CSRF token, from the console", func(t *testing.T) {
		h := adminHarness(t, loopback)
		b := login(h)
		if _, err := auditPage(ctx, b, &bffv1.ListSecurityAuditRequest{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("only the console's Origin", func(t *testing.T) {
		h := adminHarness(t, loopback)
		b := login(h)
		for name, origin := range map[string]string{"none": "", "a product": tripOrigin, "the account center": ozidOrigin, "a sibling": siblingOrigin, "evil": "https://evil.example"} {
			res, err := adminClient(b).ListSecurityAudit(ctx, withOrigin(&bffv1.ListSecurityAuditRequest{}, origin))
			if err == nil || res != nil || !isPerimeterRefusal(err) {
				t.Errorf("Origin %s: %v", name, err)
			}
		}
	})
	t.Run("a source outside the allowlist, forged headers included", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
		b := login(h)
		_, err := auditPage(ctx, b, &bffv1.ListSecurityAuditRequest{})
		denied("outside peer", err)
		_, err = auditPage(ctx, b, &bffv1.ListSecurityAuditRequest{}, forged...)
		denied("outside peer with forged headers", err)
		_, err = auditPage(ctx, h.FreshBrowser(), &bffv1.ListSecurityAuditRequest{})
		denied("anonymous outside", err)
		res := preflight(t, h, "/api/bff.v1.UserAdminService/ListSecurityAudit", adminOrigin)
		if res.StatusCode != http.StatusForbidden || res.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("preflight outside the list: %d %v", res.StatusCode, res.Header)
		}
		if n := h.UsersrvDB.SecurityAudit.Query().Where(securityaudit.Action("admin.audit.list")).CountX(ctx); n != 0 {
			t.Errorf("%d audit reads reached usersrv from outside", n)
		}
	})
	t.Run("a trusted proxy reports the client", func(t *testing.T) {
		h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}, TrustedProxies: []string{"127.0.0.0/8"}})
		b := login(h)
		if _, err := auditPage(ctx, b, &bffv1.ListSecurityAuditRequest{}, "X-Forwarded-For", "192.0.2.5"); err != nil {
			t.Fatalf("client reported by the trusted proxy: %v", err)
		}
		_, err := auditPage(ctx, b, &bffv1.ListSecurityAuditRequest{}, "X-Forwarded-For", "203.0.113.7")
		denied("client outside the list", err)
		_, err = auditPage(ctx, b, &bffv1.ListSecurityAuditRequest{})
		denied("no forwarded header: the proxy itself", err)
	})
	t.Run("a password check older than the administrator maximum", func(t *testing.T) {
		h := adminHarness(t, loopback, func(cfg *bffconfig.Config) { cfg.Auth.AdminSessionMaxAge = 1 })
		b := login(h)
		_, err := auditPage(ctx, b, &bffv1.ListSecurityAuditRequest{})
		if connect.CodeOf(err) != connect.CodeUnauthenticated || !strings.Contains(err.Error(), "sign in again") {
			t.Fatalf("an old session: %v", err)
		}
		if n := h.UsersrvDB.SecurityAudit.Query().Where(securityaudit.Action("admin.audit.list")).CountX(ctx); n != 0 {
			t.Fatalf("an old session reached usersrv (%d reads)", n)
		}
	})
	t.Run("the read limit of the operator", func(t *testing.T) {
		cfg := loopback
		cfg.RateLimitPerMinute = 3
		h := adminHarness(t, cfg)
		b := login(h)
		for i := 0; i < 3; i++ {
			if _, err := auditPage(ctx, b, &bffv1.ListSecurityAuditRequest{}); err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		if _, err := auditPage(ctx, b, &bffv1.ListSecurityAuditRequest{}); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("fourth call: %v", err)
		}
	})
}

// After real management changes through the console, the audit shows them: who, which account, what, why, the
// state before and after, and how each attempt ended - refused and unchanged ones too - with the channel told
// apart, the filters and paging working through the whole stack and nothing secret in any answer.
func TestChainUserAuditShowsWhatReallyHappened(t *testing.T) {
	h := adminHarness(t, loopback, ChainMetro{})
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "au_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	adminID, _ := h.Accounts.ResolveEmail(ctx, emailFor("au_admin"))
	tb := h.FreshBrowser()
	target, err := tb.Register(ctx, "au_target", "", "Target")
	if err != nil {
		t.Fatal(err)
	}
	targetID, _ := h.Accounts.ResolveEmail(ctx, emailFor("au_target"))

	v := versionOfUser(ctx, t, admin, target)
	steps := []struct {
		method string
		msg    *bffv1.ChangeUserRequest
	}{
		{"DisableUser", change(target, v, "au-1")},
		{"DisableUser", change(target, v+1, "au-2")},         // unchanged
		{"EnableUser", change(target, v, "au-3")},            // stale version
		{"EnableUser", change(target, v+1, "au-4")},          // ok
		{"GrantMetroAdmin", change(target, v+2, "au-5")},     // ok
		{"RevokeMetroAdmin", change(target, v+3, "au-6")},    // ok
		{"DisableUser", change(adminID.String(), 2, "au-7")}, // protected
		{"DisableUser", change(target, v+4, "au-1")},         // key reused with another request
		{"DisableUser", change(target, v, "au-1")},           // the very request of au-1 again: replayed
	}
	for _, s := range steps {
		_, _ = consoleWrite(ctx, admin, s.method, s.msg)
	}
	// The operator CLI changes the account too.
	if _, err := h.Accounts.GrantRole(ctx, usersrvaccount.Operator{Name: "ops-e2e", Reason: "ticket 9", RequestID: "cli-e2e"}, targetID, usersrvaccount.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}

	rows := auditAll(ctx, t, admin, &bffv1.ListSecurityAuditRequest{UserId: target}, 4)
	count := func(action, result string) (n int) {
		for _, e := range rows {
			if e.Action == action && e.Result == result {
				n++
			}
		}
		return
	}
	for _, c := range []struct {
		action, result string
		want           int
	}{
		{"admin.account.disable", "ok", 1}, {"admin.account.disable", "unchanged", 1}, {"admin.account.enable", "refused_version_conflict", 1},
		{"admin.account.enable", "ok", 1}, {"admin.role.grant:metro-admin", "ok", 1}, {"admin.role.revoke:metro-admin", "ok", 1},
		{"admin.account.disable", "refused_mutation_reuse", 1}, {"admin.account.disable", "replayed", 1},
		{"role.grant:metro-admin", "ok", 1}, {"account.create", "ok", 1},
	} {
		if n := count(c.action, c.result); n != c.want {
			t.Errorf("%s / %s: %d rows, want %d", c.action, c.result, n, c.want)
		}
	}
	for _, e := range rows {
		switch {
		case e.Action == "admin.account.disable" && e.Result == "ok":
			if e.Channel != bffv1.AuditChannel_AUDIT_CHANNEL_WEB || e.OperatorUserId != adminID.String() || e.UserId != target || e.Reason != "support ticket 77" ||
				e.MutationId != "au-1" || e.BeforeStatus != "active" || e.AfterStatus != "disabled" || e.RequestId == "" || e.CreatedAt == nil {
				t.Errorf("web change: %+v", e)
			}
		case e.Action == "role.grant:metro-admin":
			if e.Channel != bffv1.AuditChannel_AUDIT_CHANNEL_CLI || e.OperatorUserId != "" || e.OperatorLabel != "ops-e2e" || e.Reason != "ticket 9" {
				t.Errorf("cli change: %+v", e)
			}
		case e.Action == "admin.role.grant:metro-admin" && e.Result == "ok":
			if strings.Join(e.BeforeRoles, ",") != "" || strings.Join(e.AfterRoles, ",") != "metro-admin" {
				t.Errorf("grant before/after roles: %+v", e)
			}
		case e.Result == "replayed":
			// The replay says it handed an answer back; the state is in the original it points at.
			if e.BeforeStatus != "" || e.AfterStatus != "" || e.OriginId == "" || e.Origin == nil || e.Origin.AfterStatus != "disabled" ||
				e.Origin.Result != "ok" || e.Origin.Action != "admin.account.disable" {
				t.Errorf("replay: %+v", e)
			}
		}
	}
	// The protected target's refusal is listed under that account and by its result.
	refused := auditAll(ctx, t, admin, &bffv1.ListSecurityAuditRequest{Result: "refused_target_protected"}, 10)
	if len(refused) != 1 || refused[0].UserId != adminID.String() || refused[0].OperatorUserId != adminID.String() {
		t.Fatalf("protected refusal: %+v", refused)
	}

	// Filters through the stack.
	web := auditAll(ctx, t, admin, &bffv1.ListSecurityAuditRequest{OperatorUserId: adminID.String(), Channel: bffv1.AuditChannel_AUDIT_CHANNEL_WEB}, 5)
	for _, e := range web {
		if e.Channel != bffv1.AuditChannel_AUDIT_CHANNEL_WEB || e.OperatorUserId != adminID.String() {
			t.Fatalf("not a web row of the operator: %+v", e)
		}
	}
	if len(web) < 9 {
		t.Fatalf("%d web rows of the operator", len(web))
	}
	for _, e := range auditAll(ctx, t, admin, &bffv1.ListSecurityAuditRequest{Channel: bffv1.AuditChannel_AUDIT_CHANNEL_CLI}, 5) {
		if e.Channel != bffv1.AuditChannel_AUDIT_CHANNEL_CLI || e.OperatorUserId != "" || e.OperatorLabel == "" {
			t.Fatalf("not a CLI row: %+v", e)
		}
	}
	keyed := auditAll(ctx, t, admin, &bffv1.ListSecurityAuditRequest{MutationId: "au-1"}, 2)
	// The key was used by the change, by a different request (refused) and by the original again (replayed).
	if len(keyed) != 3 {
		t.Fatalf("%d rows under au-1, want 3", len(keyed))
	}
	future := auditAll(ctx, t, admin, &bffv1.ListSecurityAuditRequest{From: timestamppb.New(time.Now().Add(time.Hour))}, 5)
	if len(future) != 0 {
		t.Fatalf("%d rows from the future", len(future))
	}
	// Paging by one row walks the same set as one big page.
	all := auditAll(ctx, t, admin, &bffv1.ListSecurityAuditRequest{}, 100)
	one := auditAll(ctx, t, admin, &bffv1.ListSecurityAuditRequest{}, 1)
	if len(one) < len(all) { // the one-by-one walk also sees the reads made meanwhile, which are newer
		t.Fatalf("paging by one found %d rows, one page %d", len(one), len(all))
	}

	// Bad input is refused without echoing it.
	for name, req := range map[string]*bffv1.ListSecurityAuditRequest{
		"user id": {UserId: "nope-SECRET"}, "action": {Action: "Not OK SECRET"}, "mutation": {MutationId: "bad key SECRET"},
		"channel": {Channel: bffv1.AuditChannel(42)}, "page token": {PageToken: "SECRET"}, "page size": {PageSize: -1},
		"time order": {From: timestamppb.New(time.Now()), To: timestamppb.New(time.Now().Add(-time.Hour))},
	} {
		_, err := auditPage(ctx, admin, req)
		if connect.CodeOf(err) != connect.CodeInvalidArgument || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Nothing secret in any row: no email address, password, token or hash.
	text := fmt.Sprint(all)
	for _, secret := range []string{"@example.com", E2EPassword, "argon2", "Bearer", "mcp_", "csrf"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(secret)) {
			t.Fatalf("the audit answer carries %q", secret)
		}
	}
}

// The BFF's log line of an audit read has the kinds of filter and the size of the answer: no filter value, no
// account ID, no key, no reason and nothing of a row.
func TestChainUserAuditLogsCarryNoFilterValuesOrRows(t *testing.T) {
	sink := captureLogs(t)
	h := adminHarness(t, loopback)
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "alog_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	tb := h.FreshBrowser()
	id, err := tb.Register(ctx, "alog_target", "", "Target")
	if err != nil {
		t.Fatal(err)
	}
	msg := change(id, versionOfUser(ctx, t, admin, id), "audit-log-key-77")
	msg.Reason = "synthetic-reason-9a2b unique text"
	if _, err = consoleWrite(ctx, admin, "DisableUser", msg); err != nil {
		t.Fatal(err)
	}
	if _, err = auditPage(ctx, admin, &bffv1.ListSecurityAuditRequest{UserId: id, MutationId: "audit-log-key-77", Action: "admin.account.disable"}); err != nil {
		t.Fatal(err)
	}
	logs := sink.String()
	if !strings.Contains(logs, "admin audit query") || !strings.Contains(logs, "user+action+mutation") {
		t.Fatalf("the audit read left no usable log line:\n%s", logs)
	}
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, "admin audit query") {
			continue
		}
		for _, secret := range []string{id, "audit-log-key-77", "synthetic-reason-9a2b", emailFor("alog_target"), emailFor("alog_admin"), E2EPassword} {
			if strings.Contains(line, secret) {
				t.Fatalf("the audit log line carries %q: %s", secret, line)
			}
		}
	}
	for _, secret := range []string{"synthetic-reason-9a2b", emailFor("alog_target"), emailFor("alog_admin"), E2EPassword} {
		if strings.Contains(logs, secret) {
			t.Fatalf("a log line carries %q", secret)
		}
	}
}
