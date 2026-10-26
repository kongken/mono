package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/adminguard"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/session"
	usersrvaccount "github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
)

// The four writes of the console, by name; each is one RPC and nothing else can change an account.
var consoleWrites = []string{"DisableUser", "EnableUser", "GrantMetroAdmin", "RevokeMetroAdmin"}

// consoleWrite sends one write the way the console does: its own Origin and the session's CSRF token.
// tweak may remove or change what a test wants to test the absence of.
func consoleWrite(ctx context.Context, b *Browser, method string, msg *bffv1.ChangeUserRequest, tweak ...func(*connect.Request[bffv1.ChangeUserRequest])) (*bffv1.ChangeUserResponse, error) {
	req := connect.NewRequest(msg)
	req.Header().Set("Origin", adminOrigin)
	req.Header().Set(session.CSRFHeader, b.refreshCSRF(ctx))
	for _, t := range tweak {
		t(req)
	}
	c := adminClient(b)
	var res *connect.Response[bffv1.ChangeUserResponse]
	var err error
	switch method {
	case "DisableUser":
		res, err = c.DisableUser(ctx, req)
	case "EnableUser":
		res, err = c.EnableUser(ctx, req)
	case "GrantMetroAdmin":
		res, err = c.GrantMetroAdmin(ctx, req)
	case "RevokeMetroAdmin":
		res, err = c.RevokeMetroAdmin(ctx, req)
	default:
		panic("unknown method " + method)
	}
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func change(target string, version int64, key string) *bffv1.ChangeUserRequest {
	return &bffv1.ChangeUserRequest{UserId: target, ExpectedAuthVersion: version, MutationId: key, Reason: "support ticket 77"}
}

// versionOfUser is what the console would read: the version a write must name.
func versionOfUser(ctx context.Context, t *testing.T, b *Browser, id string) int64 {
	t.Helper()
	res, err := adminClient(b).GetUser(ctx, withOrigin(&bffv1.GetUserRequest{UserId: id}, adminOrigin))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetUser().GetAuthVersion()
}

func wantCode(t *testing.T, what string, err error, code connect.Code, text string) {
	t.Helper()
	if connect.CodeOf(err) != code || (text != "" && !strings.Contains(err.Error(), text)) {
		t.Errorf("%s: want %s %q, got %v", what, code, text, err)
	}
}

// The whole path with real everything: perimeter, session, CSRF, recent password, BFF, usersrv, PostgreSQL.
// The effects that matter to the account's owner are checked from the owner's side: sessions and management
// tokens die at once and are not brought back by enabling or granting again.
func TestChainUserAdminChangesEndToEnd(t *testing.T) {
	h := adminHarness(t, loopback, ChainMetro{})
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "w_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	adminID, _ := h.Accounts.ResolveEmail(ctx, emailFor("w_admin"))

	owner := h.FreshBrowser()
	if err := owner.LoginWithRoles(ctx, "w_owner", "metro-admin"); err != nil {
		t.Fatal(err)
	}
	me, err := owner.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := me.GetId()
	token := h.newAdminToken(ctx, owner, "console-test", 30)
	mcp := h.mcp(token)
	toolWorks := func() bool {
		t.Helper()
		st, res := mcp.rpc("tools/call", map[string]any{"name": "metro_list_cities", "arguments": map[string]any{}})
		if st != http.StatusOK {
			return false
		}
		isErr, _ := res["result"].(map[string]any)["isError"].(bool)
		return !isErr
	}
	if !toolWorks() {
		t.Fatal("a fresh management token does not work")
	}

	// Disable: the owner's session and token stop at their next check.
	v := versionOfUser(ctx, t, admin, id)
	res, err := consoleWrite(ctx, admin, "DisableUser", change(id, v, "w-1"))
	if err != nil || !res.Changed || res.Replayed || res.AuthVersion != v+1 || res.Status != bffv1.UserStatus_USER_STATUS_DISABLED {
		t.Fatalf("disable: %+v %v", res, err)
	}
	if _, err = owner.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the disabled account's session: %v", err)
	}
	if toolWorks() {
		t.Fatal("the disabled account's management token kept working")
	}

	// Enable: nothing that died comes back.
	if res, err = consoleWrite(ctx, admin, "EnableUser", change(id, v+1, "w-2")); err != nil || !res.Changed || res.AuthVersion != v+2 {
		t.Fatalf("enable: %+v %v", res, err)
	}
	if _, err = owner.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("enabling revived an old session: %v", err)
	}
	if toolWorks() {
		t.Fatal("enabling revived an old management token")
	}
	again := h.FreshBrowser()
	if err = again.Login(ctx, "w_owner", "", ""); err != nil {
		t.Fatalf("the enabled account cannot sign in: %v", err)
	}
	if mine, err := again.MeFull(ctx); err != nil || len(mine.GetRoles()) != 1 {
		t.Fatalf("roles after enabling: %v %v", mine, err)
	}

	// Revoke and grant metro-admin: each moves the version, the token of before stays dead, and a role
	// that is already there (or already gone) is a classified "unchanged".
	fresh := h.mcp(h.newAdminToken(ctx, again, "second", 30))
	freshWorks := func() bool {
		st, res := fresh.rpc("tools/call", map[string]any{"name": "metro_list_cities", "arguments": map[string]any{}})
		if st != http.StatusOK {
			return false
		}
		isErr, _ := res["result"].(map[string]any)["isError"].(bool)
		return !isErr
	}
	if !freshWorks() {
		t.Fatal("the second token does not work")
	}
	v = versionOfUser(ctx, t, admin, id)
	if res, err = consoleWrite(ctx, admin, "RevokeMetroAdmin", change(id, v, "w-3")); err != nil || !res.Changed || len(res.Roles) != 0 || res.AuthVersion != v+1 {
		t.Fatalf("revoke: %+v %v", res, err)
	}
	if res, err = consoleWrite(ctx, admin, "RevokeMetroAdmin", change(id, v+1, "w-4")); err != nil || res.Changed || res.AuthVersion != v+1 {
		t.Fatalf("revoke again: %+v %v", res, err)
	}
	if res, err = consoleWrite(ctx, admin, "GrantMetroAdmin", change(id, v+1, "w-5")); err != nil || !res.Changed || len(res.Roles) != 1 || res.Roles[0] != "metro-admin" || res.AuthVersion != v+2 {
		t.Fatalf("grant: %+v %v", res, err)
	}
	if freshWorks() {
		t.Fatal("granting the role again revived an older token")
	}
	if res, err = consoleWrite(ctx, admin, "GrantMetroAdmin", change(id, v+2, "w-6")); err != nil || res.Changed || res.AuthVersion != v+2 {
		t.Fatalf("grant again: %+v %v", res, err)
	}

	// The audit tells who did what to whom, with before and after, and nothing secret.
	rows := h.UsersrvDB.SecurityAudit.Query().Where(securityaudit.ChannelEQ("web"), securityaudit.ActionHasPrefix("admin.account."),
		securityaudit.Result("ok")).AllX(ctx)
	if len(rows) != 2 {
		t.Fatalf("%d account-change audit rows", len(rows))
	}
	for _, row := range rows {
		if row.ActorUserID == nil || *row.ActorUserID != adminID || row.UserID == nil || row.UserID.String() != id || row.Reason != "support ticket 77" ||
			row.MutationID == "" || row.RequestID == "" || row.BeforeStatus == row.AfterStatus {
			t.Fatalf("audit row: %+v", row)
		}
	}
}

// Every protection is checked on its own and none is skipped for a request that satisfies the rest.
func TestChainUserAdminWritesNeedEveryProtection(t *testing.T) {
	h := adminHarness(t, loopback, ChainMetro{})
	ctx := context.Background()
	admin, plain, metro := h.FreshBrowser(), h.FreshBrowser(), h.FreshBrowser()
	for b, c := range map[*Browser]struct {
		sub   string
		roles []string
	}{admin: {"p_admin", []string{"ozid-admin"}}, plain: {"p_plain", nil}, metro: {"p_metro", []string{"metro-admin"}}} {
		if err := b.LoginWithRoles(ctx, c.sub, c.roles...); err != nil {
			t.Fatal(err)
		}
	}
	targetBrowser := h.FreshBrowser()
	if _, err := targetBrowser.Register(ctx, "p_target", "", "Target"); err != nil {
		t.Fatal(err)
	}
	tid, _ := h.Accounts.ResolveEmail(ctx, emailFor("p_target"))
	target := tid.String()
	unchanged := func(what string) {
		t.Helper()
		u := h.UsersrvDB.User.GetX(ctx, tid)
		if u.AuthVersion != 1 || u.Status != "active" || h.UsersrvDB.UserRole.Query().CountX(ctx) != 2 { // the two roles of the test accounts
			t.Fatalf("%s changed the target or a role: %+v", what, u)
		}
	}
	if n := h.UsersrvDB.SecurityAudit.Query().Where(securityaudit.ChannelEQ("web")).CountX(ctx); n != 0 {
		t.Fatalf("%d web audit rows before any write", n)
	}

	for i, method := range consoleWrites {
		key := fmt.Sprintf("prot-%d", i)
		msg := func() *bffv1.ChangeUserRequest { return change(target, 1, key) }

		// No CSRF token / a wrong one / no Origin / a product's Origin: the same bare refusal.
		_, err := consoleWrite(ctx, admin, method, msg(), func(r *connect.Request[bffv1.ChangeUserRequest]) { r.Header().Del(session.CSRFHeader) })
		wantCode(t, method+" without CSRF", err, connect.CodePermissionDenied, "request rejected")
		_, err = consoleWrite(ctx, admin, method, msg(), func(r *connect.Request[bffv1.ChangeUserRequest]) { r.Header().Set(session.CSRFHeader, "forged") })
		wantCode(t, method+" with a wrong CSRF token", err, connect.CodePermissionDenied, "request rejected")
		_, err = consoleWrite(ctx, admin, method, msg(), func(r *connect.Request[bffv1.ChangeUserRequest]) { r.Header().Del("Origin") })
		if err == nil || !isPerimeterRefusal(err) {
			t.Errorf("%s without Origin: %v", method, err)
		}
		_, err = consoleWrite(ctx, admin, method, msg(), func(r *connect.Request[bffv1.ChangeUserRequest]) { r.Header().Set("Origin", tripOrigin) })
		if err == nil || !isPerimeterRefusal(err) {
			t.Errorf("%s from a product origin: %v", method, err)
		}

		// Not signed in; signed in without the platform role (an ordinary user, and a metro-admin).
		_, err = consoleWrite(ctx, h.FreshBrowser(), method, msg())
		wantCode(t, method+" anonymously", err, connect.CodeUnauthenticated, "")
		for who, b := range map[string]*Browser{"an ordinary user": plain, "a metro-admin": metro} {
			_, err = consoleWrite(ctx, b, method, msg())
			wantCode(t, method+" by "+who, err, connect.CodePermissionDenied, "required role missing")
		}

		// The session says who the operator is: a header naming somebody else is no authority.
		other, _ := plain.Me(ctx)
		_, err = consoleWrite(ctx, admin, method, msg(), func(r *connect.Request[bffv1.ChangeUserRequest]) { r.Header().Set("X-Kongken-User", other.GetId()) })
		wantCode(t, method+" claiming another operator", err, connect.CodeUnauthenticated, "")
	}
	unchanged("a refused write")

	// A password check older than 15 minutes is refused before anything is forwarded; the write is not
	// replayed by the BFF after the user confirms the password - the user sends it again on purpose.
	h.ageSession(admin, 16*time.Minute)
	for i, method := range consoleWrites {
		_, err := consoleWrite(ctx, admin, method, change(target, 1, fmt.Sprintf("old-%d", i)))
		wantCode(t, method+" with an old password check", err, connect.CodeFailedPrecondition, "enter your password again")
	}
	unchanged("a write behind an old password check")
	if _, err := adminClient(admin).ListUsers(ctx, withOrigin(&bffv1.ListUsersRequest{}, adminOrigin)); err != nil {
		t.Fatalf("a read does not need the recent password: %v", err)
	}
	if err := admin.Reauthenticate(ctx, E2EPassword); err != nil {
		t.Fatal(err)
	}
	unchanged("re-authenticating")
	if res, err := consoleWrite(ctx, admin, "DisableUser", change(target, 1, "old-0")); err != nil || !res.Changed {
		t.Fatalf("the same key after re-authentication: %+v %v", res, err)
	}
	// 14 minutes still counts as recent; a session that is only 14 minutes old is fine.
	h.ageSession(admin, 14*time.Minute)
	if _, err := consoleWrite(ctx, admin, "EnableUser", change(target, 2, "old-fresh")); err != nil {
		t.Fatalf("a 14-minute-old password check: %v", err)
	}
}

// The perimeter applies to the writes exactly as to the reads.
func TestChainUserAdminWritesAreRefusedFromOutsideTheAllowlist(t *testing.T) {
	h := adminHarness(t, adminguard.Config{AllowedCIDRs: []string{"192.0.2.0/24"}})
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "o_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	tid, _ := h.Accounts.ResolveEmail(ctx, emailFor("o_admin"))
	for _, method := range consoleWrites {
		_, err := consoleWrite(ctx, admin, method, change(tid.String(), 2, "out-"+method), func(r *connect.Request[bffv1.ChangeUserRequest]) {
			r.Header().Set("X-Forwarded-For", "192.0.2.5")
		})
		if err == nil || !isPerimeterRefusal(err) {
			t.Errorf("%s from outside: %v", method, err)
		}
	}
}

// What the console must never be able to do, as the real stack answers it.
func TestChainUserAdminForbiddenOperations(t *testing.T) {
	h := adminHarness(t, loopback)
	ctx := context.Background()
	admin, second := h.FreshBrowser(), h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "f_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	if err := second.LoginWithRoles(ctx, "f_admin2", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	adminID, _ := h.Accounts.ResolveEmail(ctx, emailFor("f_admin"))
	secondID, _ := h.Accounts.ResolveEmail(ctx, emailFor("f_admin2"))

	// The service has exactly these seven methods (three reads, the audit among them, and four narrow writes):
	// nothing to set a role by name, a field list, a password, to impersonate, to delete or to export.
	methods := bffv1.File_bff_v1_user_admin_proto.Services().ByName("UserAdminService").Methods()
	var names []string
	for i := 0; i < methods.Len(); i++ {
		names = append(names, string(methods.Get(i).Name()))
	}
	if got := strings.Join(names, ","); got != "ListUsers,GetUser,ListSecurityAudit,DisableUser,EnableUser,GrantMetroAdmin,RevokeMetroAdmin" {
		t.Fatalf("the console's user service has the methods %s", got)
	}
	fields := bffv1.File_bff_v1_user_admin_proto.Messages().ByName("ChangeUserRequest").Fields()
	var got []string
	for i := 0; i < fields.Len(); i++ {
		got = append(got, string(fields.Get(i).Name()))
	}
	if strings.Join(got, ",") != "user_id,expected_auth_version,mutation_id,reason" {
		t.Fatalf("a change request carries %v", got)
	}

	// An account that holds the platform role, another one's or the operator's own, is never changed.
	for _, target := range []string{secondID.String(), adminID.String()} {
		for i, method := range consoleWrites {
			v := versionOfUser(ctx, t, admin, target)
			_, err := consoleWrite(ctx, admin, method, change(target, v, fmt.Sprintf("f-%s-%d", target[:8], i)))
			wantCode(t, method+" on a platform administrator", err, connect.CodeFailedPrecondition, "platform administrators cannot be changed here")
			if after := versionOfUser(ctx, t, admin, target); after != v {
				t.Fatalf("%s moved a platform administrator's version", method)
			}
		}
	}
	if u := h.UsersrvDB.User.GetX(ctx, secondID); u.Status != "active" || h.UsersrvDB.UserRole.Query().CountX(ctx) != 2 {
		t.Fatalf("a platform administrator was changed: %+v", u)
	}

	// A role granted by the operator CLI a moment ago protects at once - a stale view cannot slip through.
	late := h.FreshBrowser()
	if _, err := late.Register(ctx, "f_late", "", "Late"); err != nil {
		t.Fatal(err)
	}
	lateID, _ := h.Accounts.ResolveEmail(ctx, emailFor("f_late"))
	v := versionOfUser(ctx, t, admin, lateID.String())
	if _, err := h.Accounts.GrantRole(ctx, operator, lateID, usersrvaccount.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	_, err := consoleWrite(ctx, admin, "DisableUser", change(lateID.String(), v+1, "f-late"))
	wantCode(t, "disable behind a CLI grant", err, connect.CodeFailedPrecondition, "platform administrators cannot be changed here")

	// A deleting account can be neither enabled (that would cancel the deletion) nor changed.
	closing := h.FreshBrowser()
	if _, err = closing.Register(ctx, "f_closing", "", "Closing"); err != nil {
		t.Fatal(err)
	}
	cid, _ := h.Accounts.ResolveEmail(ctx, emailFor("f_closing"))
	if _, err = h.Accounts.BeginDeletion(ctx, cid, "f-del", "req-del"); err != nil {
		t.Fatal(err)
	}
	v = versionOfUser(ctx, t, admin, cid.String())
	for i, method := range consoleWrites {
		_, err = consoleWrite(ctx, admin, method, change(cid.String(), v, fmt.Sprintf("f-del-%d", i)))
		wantCode(t, method+" on a deleting account", err, connect.CodeFailedPrecondition, "account is not active")
	}
	if u := h.UsersrvDB.User.GetX(ctx, cid); u.Status != "deleting" || u.AuthVersion != v || u.DeletionMutationID == nil {
		t.Fatalf("the deletion was touched: %+v", u)
	}

	// An unknown account, a malformed one, and bad requests change nothing.
	_, err = consoleWrite(ctx, admin, "DisableUser", change("00000000-0000-0000-0000-0000000000aa", 1, "f-ghost"))
	wantCode(t, "unknown account", err, connect.CodeNotFound, "")
	_, err = consoleWrite(ctx, admin, "DisableUser", change("nope", 1, "f-bad"))
	wantCode(t, "malformed id", err, connect.CodeInvalidArgument, "user_id")
	for name, msg := range map[string]*bffv1.ChangeUserRequest{
		"no reason":    {UserId: cid.String(), ExpectedAuthVersion: 1, MutationId: "f-r1"},
		"email reason": {UserId: cid.String(), ExpectedAuthVersion: 1, MutationId: "f-r2", Reason: "asked by someone@example.com"},
		"no key":       {UserId: cid.String(), ExpectedAuthVersion: 1, Reason: "x"},
		"bad key":      {UserId: cid.String(), ExpectedAuthVersion: 1, MutationId: "a b", Reason: "x"},
		"no version":   {UserId: cid.String(), MutationId: "f-r3", Reason: "x"},
	} {
		_, err = consoleWrite(ctx, admin, "DisableUser", msg)
		wantCode(t, name, err, connect.CodeInvalidArgument, "")
	}
}

// A stale view, a lost response, a reused key and another operator.
func TestChainUserAdminWritesAreVersionedAndIdempotent(t *testing.T) {
	h := adminHarness(t, loopback)
	ctx := context.Background()
	a, b := h.FreshBrowser(), h.FreshBrowser()
	if err := a.LoginWithRoles(ctx, "i_admin_a", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	if err := b.LoginWithRoles(ctx, "i_admin_b", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	tb := h.FreshBrowser()
	id, err := tb.Register(ctx, "i_target", "", "Target")
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := h.Accounts.ResolveEmail(ctx, emailFor("i_target"))
	v := versionOfUser(ctx, t, a, id)

	// The administrators both look at version v. The CLI changes the account behind their backs.
	if _, err = h.Accounts.GrantRole(ctx, operator, tid, usersrvaccount.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}
	_, err = consoleWrite(ctx, a, "DisableUser", change(id, v, "i-1"))
	wantCode(t, "a stale view", err, connect.CodeAborted, "account changed since it was read")
	if h.UsersrvDB.User.GetX(ctx, tid).Status != "active" {
		t.Fatal("a stale write changed the account")
	}
	v = versionOfUser(ctx, t, a, id) // read again, as the console says

	first, err := consoleWrite(ctx, a, "DisableUser", change(id, v, "i-2"))
	if err != nil || !first.Changed || first.Replayed {
		t.Fatalf("first: %+v %v", first, err)
	}
	// The response was lost: the console sends the same request again (same key, same content). The
	// account has since moved on (enabled by the CLI), and the retry still gets the first answer.
	if _, err = h.Accounts.SetDisabled(ctx, operator, tid, false); err != nil {
		t.Fatal(err)
	}
	moved := h.UsersrvDB.User.GetX(ctx, tid).AuthVersion
	replay, err := consoleWrite(ctx, a, "DisableUser", change(id, v, "i-2"))
	if err != nil || !replay.Replayed || !replay.Changed || replay.AuthVersion != first.AuthVersion || replay.Status != first.Status {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if u := h.UsersrvDB.User.GetX(ctx, tid); u.AuthVersion != moved || u.Status != "active" {
		t.Fatalf("a replay changed the account again: %+v", u)
	}

	// The key with other content is refused and does nothing.
	_, err = consoleWrite(ctx, a, "EnableUser", change(id, v, "i-2"))
	wantCode(t, "the key for another action", err, connect.CodeAlreadyExists, "mutation id used for a different request")
	other := change(id, v, "i-2")
	other.Reason = "another reason"
	_, err = consoleWrite(ctx, a, "DisableUser", other)
	wantCode(t, "the key with another reason", err, connect.CodeAlreadyExists, "")

	// Another operator's receipt neither answers nor blocks: b's own request with the same key is judged on its
	// own (it names the old version and is stale).
	_, err = consoleWrite(ctx, b, "DisableUser", change(id, v, "i-2"))
	wantCode(t, "another operator, same key, stale view", err, connect.CodeAborted, "")
	v = versionOfUser(ctx, t, b, id)
	res, err := consoleWrite(ctx, b, "DisableUser", change(id, v, "i-2"))
	if err != nil || res.Replayed || !res.Changed {
		t.Fatalf("another operator's own request under the same key: %+v %v", res, err)
	}

	// A new key for a state the account is already in is a classified no-op: the version stays.
	res, err = consoleWrite(ctx, a, "DisableUser", change(id, res.AuthVersion, "i-3"))
	if err != nil || res.Changed || res.AuthVersion != v+1 {
		t.Fatalf("already disabled: %+v %v", res, err)
	}
	rows := h.UsersrvDB.SecurityAudit.Query().Where(securityaudit.ChannelEQ("web")).AllX(ctx)
	results := map[string]int{}
	for _, r := range rows {
		results[r.Result]++
	}
	for _, want := range []string{"ok", "unchanged", "replayed", "refused_version_conflict", "refused_mutation_reuse"} {
		if results[want] == 0 {
			t.Errorf("no audit row classified %q: %v", want, results)
		}
	}
}

// Requests that race are serialised by the account: one change, the rest see it.
func TestChainUserAdminConcurrentWrites(t *testing.T) {
	h := adminHarness(t, loopback, func(cfg *bffconfig.Config) { cfg.Admin.WriteRateLimitPerMinute = 1000 })
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "c_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	const n = 8
	var browsers [n]*Browser
	for i := range browsers {
		// The same operator on several devices: each has its own session and CSRF token.
		browsers[i] = h.FreshBrowser()
		if err := browsers[i].Login(ctx, "c_admin", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	run := func(f func(i int) (*bffv1.ChangeUserResponse, error)) ([]*bffv1.ChangeUserResponse, []error) {
		var wg sync.WaitGroup
		out := make([]*bffv1.ChangeUserResponse, n)
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); out[i], errs[i] = f(i) }()
		}
		wg.Wait()
		return out, errs
	}

	t.Run("one request sent many times", func(t *testing.T) {
		tb := h.FreshBrowser()
		id, err := tb.Register(ctx, "c_dup", "", "Dup")
		if err != nil {
			t.Fatal(err)
		}
		v := versionOfUser(ctx, t, admin, id)
		out, errs := run(func(i int) (*bffv1.ChangeUserResponse, error) {
			return consoleWrite(ctx, browsers[i], "DisableUser", change(id, v, "c-dup"))
		})
		var changed, replayed int
		for i := range out {
			if errs[i] != nil {
				t.Fatalf("request %d: %v", i, errs[i])
			}
			if out[i].AuthVersion != v+1 || !out[i].Changed {
				t.Fatalf("request %d answered %+v", i, out[i])
			}
			if out[i].Replayed {
				replayed++
			} else {
				changed++
			}
		}
		if changed != 1 || replayed != n-1 {
			t.Fatalf("%d first answers and %d replays", changed, replayed)
		}
		tid, _ := h.Accounts.ResolveEmail(ctx, emailFor("c_dup"))
		if u := h.UsersrvDB.User.GetX(ctx, tid); u.AuthVersion != v+1 {
			t.Fatalf("the version advanced %d times", u.AuthVersion-v)
		}
	})
	t.Run("different requests for the same version", func(t *testing.T) {
		tb := h.FreshBrowser()
		id, err := tb.Register(ctx, "c_race", "", "Race")
		if err != nil {
			t.Fatal(err)
		}
		v := versionOfUser(ctx, t, admin, id)
		_, errs := run(func(i int) (*bffv1.ChangeUserResponse, error) {
			return consoleWrite(ctx, browsers[i], "DisableUser", change(id, v, fmt.Sprintf("c-race-%d", i)))
		})
		var won, conflicts int
		for i, err := range errs {
			switch {
			case err == nil:
				won++
			case connect.CodeOf(err) == connect.CodeAborted:
				conflicts++
			default:
				t.Fatalf("request %d: %v", i, err)
			}
		}
		if won != 1 || conflicts != n-1 {
			t.Fatalf("%d won, %d conflicts", won, conflicts)
		}
	})
}

// Writes are limited per operator, separately and lower than reads.
func TestChainUserAdminWriteRateLimit(t *testing.T) {
	cfg := loopback
	cfg.WriteRateLimitPerMinute = 2
	h := adminHarness(t, cfg)
	ctx := context.Background()
	admin, other := h.FreshBrowser(), h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "r_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	if err := other.LoginWithRoles(ctx, "r_other", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	tb := h.FreshBrowser()
	id, err := tb.Register(ctx, "r_target", "", "Target")
	if err != nil {
		t.Fatal(err)
	}
	v := versionOfUser(ctx, t, admin, id)
	for i := 0; i < 2; i++ {
		if _, err = consoleWrite(ctx, admin, "RevokeMetroAdmin", change(id, v, fmt.Sprintf("rate-%d", i))); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	_, err = consoleWrite(ctx, admin, "RevokeMetroAdmin", change(id, v, "rate-3"))
	wantCode(t, "the third write", err, connect.CodeResourceExhausted, "")
	// Reads have their own, higher limit and another operator has his own budget.
	if _, err = listAll(adminClient(admin), &bffv1.ListUsersRequest{}); err != nil {
		t.Fatalf("a read after the write limit: %v", err)
	}
	if _, err = consoleWrite(ctx, other, "RevokeMetroAdmin", change(id, v, "rate-other")); err != nil {
		t.Fatalf("another operator: %v", err)
	}
}

// The BFF's log line of a change names who, what and which key, never the reason, an email or a secret.
func TestChainUserAdminWriteLogsCarryNoReasonOrEmail(t *testing.T) {
	sink := captureLogs(t)
	h := adminHarness(t, loopback)
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "l_admin", "ozid-admin"); err != nil {
		t.Fatal(err)
	}
	tb := h.FreshBrowser()
	id, err := tb.Register(ctx, "l_target", "", "Target")
	if err != nil {
		t.Fatal(err)
	}
	msg := change(id, versionOfUser(ctx, t, admin, id), "log-1")
	msg.Reason = "synthetic-reason-5c1d unique text"
	if _, err = consoleWrite(ctx, admin, "DisableUser", msg); err != nil {
		t.Fatal(err)
	}
	logs := sink.String()
	if !strings.Contains(logs, "admin user change") || !strings.Contains(logs, "log-1") {
		t.Fatalf("the change left no log line:\n%s", logs)
	}
	for _, secret := range []string{"synthetic-reason-5c1d", emailFor("l_target"), emailFor("l_admin"), E2EPassword} {
		if strings.Contains(logs, secret) {
			t.Fatalf("a log line carries %q", secret)
		}
	}
}
