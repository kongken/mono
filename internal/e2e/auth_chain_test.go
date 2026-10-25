package e2e_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	bffsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/session"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestChainCookieAuthAndFootprints(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()

	a := h.FreshBrowser()
	aID, err := a.Register(ctx, "alice", "", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if a.SessionCookie() == "" || !strings.HasPrefix(a.SessionCookie(), "bf_") {
		t.Fatal("expected session cookie after login")
	}
	me, err := a.MeFull(ctx)
	if err != nil || me.GetUser().GetId() != aID || me.GetUser().GetDisplayName() != "Alice" || me.GetEmail() != "alice@example.com" {
		t.Fatalf("me after register: %v err=%v", me, err)
	}
	if len(me.GetRoles()) != 0 {
		t.Fatalf("a normal registration has roles: %v", me.GetRoles())
	}
	// Same jar simulates refresh.
	if me2, err := a.Me(ctx); err != nil || me2.GetId() != aID {
		t.Fatal(err)
	}
	if err = a.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("me after logout: %v", err)
	}
	// Logging out again, without a session, is not an error.
	if err = a.Logout(ctx); err != nil {
		t.Fatalf("second logout: %v", err)
	}

	b := h.FreshBrowser()
	bID, err := b.Register(ctx, "bob", "", "Bob")
	if err != nil {
		t.Fatal(err)
	}
	h.TripmapDB.CityVisit.Create().SetUserID(bID).
		SetRegionCode("330100").SetRegionName("杭州市").SetProvinceCode("330000").SetColor("#4b937d").SaveX(ctx)
	bFoot, err := b.GetFootprints(ctx)
	if err != nil || len(bFoot.GetVisits()) == 0 {
		t.Fatalf("bob footprints: %v err=%v", bFoot, err)
	}
	a2 := h.FreshBrowser()
	if _, err = a2.Register(ctx, "alice2", "", "A2"); err != nil {
		t.Fatal(err)
	}
	aFoot, err := a2.GetFootprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range aFoot.GetVisits() {
		if v.GetCode() == "330100" {
			t.Fatal("user A must not read user B city visit")
		}
	}
}

func TestChainCSRFAndOriginRejected(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	login := func() *connect.Request[bffv1.LoginWithEmailRequest] {
		return connect.NewRequest(&bffv1.LoginWithEmailRequest{Email: "x@example.com", Password: E2EPassword})
	}

	req := login()
	b.writeHeaders(req, "", "")
	if _, err := b.Auth.LoginWithEmail(ctx, req); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("missing csrf: %v", err)
	}
	req = login()
	b.writeHeaders(req, b.refreshCSRF(ctx), "https://evil.example.com")
	if _, err := b.Auth.LoginWithEmail(ctx, req); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("bad origin: %v", err)
	}
	req = login()
	b.writeHeaders(req, "not-the-cookie", b.Origin)
	if _, err := b.Auth.LoginWithEmail(ctx, req); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("bad csrf: %v", err)
	}
	// Registration and reset requests are writes too.
	for name, call := range map[string]func(*Browser) error{
		"request registration": func(b *Browser) error {
			req := connect.NewRequest(&bffv1.RequestRegistrationRequest{Email: "x@example.com", AppId: "trip-map"})
			b.writeHeaders(req, "", "")
			_, err := b.Auth.RequestRegistration(ctx, req)
			return err
		},
		"request reset": func(b *Browser) error {
			req := connect.NewRequest(&bffv1.RequestPasswordResetRequest{Email: "x@example.com", AppId: "trip-map"})
			b.writeHeaders(req, "", "")
			_, err := b.Auth.RequestPasswordReset(ctx, req)
			return err
		},
	} {
		if err := call(b); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("%s without csrf: %v", name, err)
		}
	}
	if h.Inbox.Total() != 0 {
		t.Fatal("a rejected request sent mail")
	}
}

func TestChainSessionStatesRejected(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	unauth := func(b *Browser, what string) {
		t.Helper()
		if _, err := b.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("%s: %v", what, err)
		}
	}

	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "carol", "", "Carol"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.BFFDB.Session.Update().Where(bffsession.TokenHash(session.HashToken(b.SessionCookie()))).
		SetExpiresAt(time.Now().UTC().Add(-time.Hour)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	unauth(b, "expired session")

	b = h.FreshBrowser()
	if _, err := b.Register(ctx, "dave", "", "Dave"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.BFFDB.Session.Update().Where(bffsession.TokenHash(session.HashToken(b.SessionCookie()))).SetRevoked(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	unauth(b, "revoked session")

	// A disabled account loses every session at its next request, and does not get
	// a new one.
	b = h.FreshBrowser()
	id, err := b.Register(ctx, "eve", "", "Eve")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Accounts.SetDisabled(ctx, operator, userUUID(t, id), true); err != nil {
		t.Fatal(err)
	}
	unauth(b, "disabled user")
	if err = b.Login(ctx, "eve", "", ""); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a disabled account signed in: %v", err)
	}
	if _, err = h.Accounts.SetDisabled(ctx, operator, userUUID(t, id), false); err != nil {
		t.Fatal(err)
	}
	unauth(b, "re-enabled account keeps the old session")
	if err = b.Login(ctx, "eve", "", ""); err != nil {
		t.Fatal(err)
	}

	// A session row from before usersrv (no auth_version, a legacy user ID) is
	// never accepted, whatever its expiry.
	b = h.FreshBrowser()
	token, hash, err := session.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	h.BFFDB.Session.Create().SetUserID("legacy-subject").SetTokenHash(hash).SetExpiresAt(time.Now().Add(time.Hour)).SaveX(ctx)
	b.SetSessionCookie(token)
	unauth(b, "legacy session")
}

func TestChainMeResponseHasNoSessionToken(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "gina", "", "Gina"); err != nil {
		t.Fatal(err)
	}
	res, err := b.Auth.Me(ctx, connect.NewRequest(&bffv1.MeRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := protojson.Marshal(res.Msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "token") || strings.Contains(string(raw), "bf_") {
		t.Fatalf("response leaked a session secret: %s", raw)
	}
}

func TestChainLoginAfterLogout(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "helen", "", "Helen"); err != nil {
		t.Fatal(err)
	}
	old := b.SessionCookie()
	if err := b.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	b2 := h.FreshBrowser()
	if err := b2.Login(ctx, "helen", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b2.Me(ctx); err != nil {
		t.Fatal(err)
	}
	// Every sign-in is a new token; the old one stays revoked.
	if b2.SessionCookie() == old {
		t.Fatal("a session token was reused across sign-ins")
	}
	b.SetSessionCookie(old)
	if _, err := b.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a logged-out token still works: %v", err)
	}
}

// Spec §14 scenario 5: an unknown address, a wrong password and a disabled account
// look the same, set no Cookie, and expose no secret.
func TestChainLoginFailuresAreUniform(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	id, err := b.Register(ctx, "uniform", "", "U")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Accounts.SetDisabled(ctx, operator, userUUID(t, id), true); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Register(ctx, "uniform-2", "", "U2"); err != nil {
		t.Fatal(err)
	}
	fresh := func(email, password string) (connect.Code, string, bool) {
		br := h.FreshBrowser()
		_, err := br.LoginWithEmail(ctx, email, password)
		var ce *connect.Error
		if err == nil {
			t.Fatalf("login %s succeeded", email)
		}
		ce, _ = err.(*connect.Error)
		return ce.Code(), ce.Message(), br.SessionCookie() != ""
	}
	type outcome struct {
		code   connect.Code
		msg    string
		cookie bool
	}
	var outcomes []outcome
	for _, c := range []struct{ email, password string }{
		{"nobody@example.com", E2EPassword},
		{"uniform-2@example.com", "the wrong password!"},
		{"uniform@example.com", E2EPassword}, // disabled
		{"uniform@example.com", "the wrong password!"},
	} {
		code, msg, cookie := fresh(c.email, c.password)
		outcomes = append(outcomes, outcome{code, msg, cookie})
	}
	for _, o := range outcomes {
		if o != outcomes[0] || o.code != connect.CodeUnauthenticated || o.cookie {
			t.Fatalf("login failures differ: %+v", outcomes)
		}
	}
	if strings.Contains(outcomes[0].msg, "example.com") {
		t.Fatalf("the failure names the address: %q", outcomes[0].msg)
	}
	// No successful sign-in left a session of the failed accounts behind.
	if n := h.BFFDB.Session.Query().Where(bffsession.UserID(id)).CountX(ctx); n != 1 {
		t.Fatalf("%d sessions for the disabled account, want only the one from registration", n)
	}
}

// Spec §14 scenarios 2-4 on the BFF edge: asking for registration neither signs
// anyone in nor reveals whether the address is known, and completing it sets no
// Cookie.
func TestChainRegistrationAnswersAreNeutral(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	known := h.FreshBrowser()
	if _, err := known.Register(ctx, "known", "", "Known"); err != nil {
		t.Fatal(err)
	}
	b := h.FreshBrowser()
	fresh, err := b.RequestRegistration(ctx, "newcomer@example.com", "trip-map")
	if err != nil {
		t.Fatal(err)
	}
	existing, err := b.RequestRegistration(ctx, "known@example.com", "trip-map")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.GetMessage() == "" || fresh.GetMessage() != existing.GetMessage() {
		t.Fatalf("answers differ: %q vs %q", fresh.GetMessage(), existing.GetMessage())
	}
	reset, err := b.RequestPasswordReset(ctx, "nobody-at-all@example.com", "trip-map")
	if err != nil || reset.GetMessage() == "" {
		t.Fatalf("reset answer: %v %v", reset, err)
	}
	h.DeliverMail()
	if len(h.Inbox.To("newcomer@example.com")) != 1 || len(h.Inbox.To("nobody-at-all@example.com")) != 0 {
		t.Fatal("mail went to the wrong addresses")
	}
	// The request signed nobody in.
	if b.SessionCookie() != "" {
		t.Fatal("requesting registration set a session cookie")
	}
	if err = b.CompleteRegistration(ctx, h.Inbox.TokenIn(t, "newcomer@example.com"), E2EPassword, "New"); err != nil {
		t.Fatal(err)
	}
	if b.SessionCookie() != "" {
		t.Fatal("verifying an address signed the user in")
	}
	if _, err = b.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("me before login: %v", err)
	}
	// A used token cannot be used again.
	if err = b.CompleteRegistration(ctx, h.Inbox.TokenIn(t, "newcomer@example.com"), E2EPassword, "New"); err == nil {
		t.Fatal("a verification token was used twice")
	}
}

// Spec §14 scenario 6: a reset changes only that password, ends every old session
// at its next request, and does not sign anyone in.
func TestChainPasswordResetEndsEverySession(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	phone, laptop := h.FreshBrowser(), h.FreshBrowser()
	if _, err := phone.Register(ctx, "reset-me", "", "Reset"); err != nil {
		t.Fatal(err)
	}
	if err := laptop.Login(ctx, "reset-me", "", ""); err != nil {
		t.Fatal(err)
	}
	bystander := h.FreshBrowser()
	if _, err := bystander.Register(ctx, "bystander", "", "B"); err != nil {
		t.Fatal(err)
	}

	anon := h.FreshBrowser()
	if _, err := anon.RequestPasswordReset(ctx, "reset-me@example.com", "metro"); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	token := h.Inbox.TokenIn(t, "reset-me@example.com")
	const next = "another long password"
	if err := anon.ResetPassword(ctx, token, next); err != nil {
		t.Fatal(err)
	}
	if anon.SessionCookie() != "" {
		t.Fatal("a reset signed the user in")
	}
	for name, br := range map[string]*Browser{"phone": phone, "laptop": laptop} {
		if _, err := br.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("%s kept its session after the reset: %v", name, err)
		}
	}
	if _, err := bystander.Me(ctx); err != nil {
		t.Fatalf("the reset ended another user's session: %v", err)
	}
	if _, err := anon.LoginWithEmail(ctx, "reset-me@example.com", E2EPassword); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("the old password still works: %v", err)
	}
	if _, err := anon.LoginWithEmail(ctx, "reset-me@example.com", next); err != nil {
		t.Fatal(err)
	}
	if _, err := anon.Me(ctx); err != nil {
		t.Fatal(err)
	}
	// The token is one-use.
	if err := anon.ResetPassword(ctx, token, "yet another long password"); err == nil {
		t.Fatal("a reset token was used twice")
	}
}

// Spec §14 scenario 7: a refresh, a new Cookie or time does not stand in for the
// password; a sensitive action after the window needs the password again.
func TestChainSensitiveActionsNeedARecentPassword(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "sensitive", "", "S"); err != nil {
		t.Fatal(err)
	}
	h.ageSession(b, 16*time.Minute)

	del := &bffv1.DeleteAccountRequest{Confirmation: "DELETE", MutationId: "m-sensitive"}
	if err := b.DeleteAccount(ctx, del); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a stale password check started a deletion: %v", err)
	}
	if n := h.BFFDB.AccountDeletion.Query().CountX(ctx); n != 0 {
		t.Fatal("the rejected deletion left a workflow")
	}
	// Reading Me does not renew it.
	if _, err := b.Me(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteAccount(ctx, del); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("Me renewed the password check: %v", err)
	}
	// A wrong password does not renew it either.
	if err := b.Reauthenticate(ctx, "not the password!!"); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong password on reauthenticate: %v", err)
	}
	if err := b.DeleteAccount(ctx, del); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a failed reauthentication renewed the check: %v", err)
	}
	// Confirmation and mutation ID are checked before anything starts.
	if err := b.Reauthenticate(ctx, E2EPassword); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]*bffv1.DeleteAccountRequest{
		"no confirmation": {MutationId: "m-1"},
		"wrong word":      {Confirmation: "delete", MutationId: "m-1"},
		"no mutation id":  {Confirmation: "DELETE"},
		"bad mutation id": {Confirmation: "DELETE", MutationId: "bad id!"},
	} {
		if err := b.DeleteAccount(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if n := h.BFFDB.AccountDeletion.Query().CountX(ctx); n != 0 {
		t.Fatal("an invalid request left a workflow")
	}
}

func TestChainChangePassword(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	a, other := h.FreshBrowser(), h.FreshBrowser()
	if _, err := a.Register(ctx, "changer", "", "C"); err != nil {
		t.Fatal(err)
	}
	if err := other.Login(ctx, "changer", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.ChangePassword(ctx, "the wrong current one", "a brand new password"); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong current password: %v", err)
	}
	if _, err := a.Me(ctx); err != nil {
		t.Fatalf("a failed change ended the session: %v", err)
	}
	if err := a.ChangePassword(ctx, E2EPassword, "short"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("weak new password: %v", err)
	}
	if err := a.ChangePassword(ctx, E2EPassword, "a brand new password"); err != nil {
		t.Fatal(err)
	}
	for name, br := range map[string]*Browser{"this device": a, "other device": other} {
		if _, err := br.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("%s kept its session: %v", name, err)
		}
	}
	if a.SessionCookie() != "" {
		t.Fatal("the cookie was not cleared after a password change")
	}
	if _, err := a.LoginWithEmail(ctx, "changer@example.com", E2EPassword); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("old password: %v", err)
	}
	if _, err := a.LoginWithEmail(ctx, "changer@example.com", "a brand new password"); err != nil {
		t.Fatal(err)
	}
}

// Spec §14 scenario 8: with usersrv unavailable, protected calls are refused and
// nothing is forwarded; anonymous reads and the other products' health are not
// turned into protected calls.
func TestChainUsersrvOutageRefusesProtectedCalls(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	id, err := b.Register(ctx, "outage", "", "O")
	if err != nil {
		t.Fatal(err)
	}
	profiles := h.TripmapDB.User.Query().CountX(ctx)
	h.FailUsersrv(func(string) error { return status.Error(codes.Unavailable, "usersrv is down") })

	for name, call := range map[string]func() error{
		"me":         func() error { _, err := b.Me(ctx); return err },
		"footprints": func() error { _, err := b.GetFootprints(ctx); return err },
		"sessions": func() error {
			_, err := b.Auth.ListSessions(ctx, connect.NewRequest(&bffv1.ListSessionsRequest{}))
			return err
		},
		"login": func() error {
			_, err := h.FreshBrowser().LoginWithEmail(ctx, "outage@example.com", E2EPassword)
			return err
		},
	} {
		if got := connect.CodeOf(call()); got != connect.CodeUnavailable {
			t.Fatalf("%s during the outage: %v", name, got)
		}
	}
	// The session is not revoked by an outage.
	if n := h.BFFDB.Session.Query().Where(bffsession.UserID(id), bffsession.Revoked(false)).CountX(ctx); n != 1 {
		t.Fatalf("%d live sessions after the outage, want 1", n)
	}
	// Nothing reached tripmap on behalf of the user.
	if n := h.TripmapDB.User.Query().CountX(ctx); n != profiles {
		t.Fatalf("%d tripmap profiles after the outage, was %d", n, profiles)
	}
	// Anonymous public status is a different path and keeps working.
	st, err := h.FreshBrowser().Status(ctx)
	if err != nil || !st.GetReady() {
		t.Fatalf("anonymous status during the outage: %v %v", st, err)
	}
	h.FailUsersrv(nil)
	if _, err = b.Me(ctx); err != nil {
		t.Fatalf("after recovery: %v", err)
	}
	if _, err = b.GetFootprints(ctx); err != nil {
		t.Fatalf("footprints after recovery: %v", err)
	}
}

// A product that is paused does not block sign-in or another product.
func TestChainPausedTripmapDoesNotBlockSignInOrMetro(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "paused", "", "P"); err != nil {
		t.Fatal(err)
	}
	h.Tripmap.StopGRPC()
	if err := h.FreshBrowser().Login(ctx, "paused", "", ""); err != nil {
		t.Fatalf("sign-in with tripmap stopped: %v", err)
	}
	if _, err := b.Me(ctx); err != nil {
		t.Fatalf("me with tripmap stopped: %v", err)
	}
	if _, err := b.GetFootprints(ctx); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("tripmap call with tripmap stopped: %v", err)
	}
	if _, err := b.MetroRider.ListMyLines(ctx, connect.NewRequest(&bffmetrov1.ListMyLinesRequest{})); err != nil {
		t.Fatalf("metro with tripmap stopped: %v", err)
	}
}

// Spec §14 scenario 9: roles come only from the operator; registration yields
// none; every change makes the older sessions fail.
func TestChainRolesComeOnlyFromTheOperatorAndExpireSessions(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	b := h.FreshBrowser()
	id, err := b.Register(ctx, "rolecheck", "", "R")
	if err != nil {
		t.Fatal(err)
	}
	// A normal user, first or not, is no administrator.
	if _, err = b.MetroAdmin.ListAdminCities(ctx, connect.NewRequest(&bffmetrov1.ListAdminCitiesRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("a plain user reached the admin API: %v", err)
	}
	if _, err = h.Accounts.GrantRole(ctx, operator, userUUID(t, id), "metro-admin"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a role grant left the old session valid: %v", err)
	}
	if err = b.Login(ctx, "rolecheck", "", ""); err != nil {
		t.Fatal(err)
	}
	me, err := b.MeFull(ctx)
	if err != nil || len(me.GetRoles()) != 1 || me.GetRoles()[0] != "metro-admin" {
		t.Fatalf("roles after grant: %v %v", me, err)
	}
	if _, err = b.MetroAdmin.ListAdminCities(ctx, connect.NewRequest(&bffmetrov1.ListAdminCitiesRequest{})); err != nil {
		t.Fatalf("an administrator could not list: %v", err)
	}
	if _, err = h.Accounts.RevokeRole(ctx, operator, userUUID(t, id), "metro-admin"); err != nil {
		t.Fatal(err)
	}
	// The revoke is felt at the very next request, not at the session's expiry.
	if _, err = b.MetroAdmin.ListAdminCities(ctx, connect.NewRequest(&bffmetrov1.ListAdminCitiesRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a revoked administrator kept access: %v", err)
	}
}

// Spec §14 scenario 12: a profile change reaches tripmap's display projection,
// without the email, and tripmap data is not moved by it; friends are found by
// invite code only.
func TestChainProfileProjectionAndFriendCodes(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	a, b := h.FreshBrowser(), h.FreshBrowser()
	aID, err := a.Register(ctx, "proj-a", "", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	bID, err := b.Register(ctx, "proj-b", "", "Bobby")
	if err != nil {
		t.Fatal(err)
	}
	// The projection is made on the first tripmap call and carries only the nickname.
	before, err := a.GetFootprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	row := h.TripmapDB.User.GetX(ctx, aID)
	if row.DisplayName != "Alice" {
		t.Fatalf("projection: %+v", row)
	}
	if strings.Contains(row.DisplayName+row.AvatarURL, "@") {
		t.Fatalf("the projection holds an address: %+v", row)
	}
	// A rider profile in metro is its own data.
	if _, err = a.MetroRider.UpdateMyProfile(ctx, riderWrite(a, &bffmetrov1.UpdateMyProfileRequest{Profile: &bffmetrov1.ProfileInput{Handle: "proj_a", DisplayName: "Rider A"}})); err != nil {
		t.Fatal(err)
	}

	user, err := a.UpdateMyProfile(ctx, "Alice Renamed", "https://img.example.com/a.png")
	if err != nil || user.GetDisplayName() != "Alice Renamed" {
		t.Fatalf("update profile: %v %v", user, err)
	}
	after, err := a.GetFootprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	row = h.TripmapDB.User.GetX(ctx, aID)
	if row.DisplayName != "Alice Renamed" || row.AvatarURL != "https://img.example.com/a.png" {
		t.Fatalf("projection after the change: %+v", row)
	}
	if after.GetRevision() != before.GetRevision() {
		t.Fatalf("a profile change moved the footprint revision: %d -> %d", before.GetRevision(), after.GetRevision())
	}
	mine, err := a.MetroRider.GetMyProfile(ctx, connect.NewRequest(&bffmetrov1.GetMyProfileRequest{}))
	if err != nil || mine.Msg.GetProfile().GetDisplayName() != "Rider A" {
		t.Fatalf("the rider profile was overwritten: %v %v", mine, err)
	}
	// A user cannot edit someone else's profile: the API has no user ID to name.
	if got := h.UsersrvDB.User.Query().CountX(ctx); got < 2 {
		t.Fatalf("users: %d", got)
	}
	_ = bID

	// Friends: the invite code works; a name, an address or an ID do not.
	ovB, err := b.GetCollaborationOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"Bobby", "proj-b@example.com", bID} {
		req := connect.NewRequest(&bfftripmapv1.SendFriendRequestRequest{Target: target})
		a.Write(ctx, req)
		if _, err := a.Friends.SendFriendRequest(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("friend lookup by %q: %v", target, err)
		}
	}
	req := connect.NewRequest(&bfftripmapv1.SendFriendRequestRequest{Target: ovB.GetMyFriendCode()})
	a.Write(ctx, req)
	res, err := a.Friends.SendFriendRequest(ctx, req)
	if err != nil || res.Msg.GetFriendship().GetFriend().GetId() != bID {
		t.Fatalf("friend by code: %v %v", res, err)
	}
	raw, _ := protojson.Marshal(res.Msg)
	if strings.Contains(string(raw), "@") || strings.Contains(string(raw), "username") {
		t.Fatalf("the friend payload leaks identity: %s", raw)
	}
}

func TestChainWritesStillNeedACSRFTokenAfterLogin(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "csrf-after", "", "C"); err != nil {
		t.Fatal(err)
	}
	req := connect.NewRequest(&bffv1.UpdateMyProfileRequest{DisplayName: "x"})
	b.writeHeaders(req, "", b.Origin)
	if _, err := b.Users.UpdateMyProfile(ctx, req); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("profile write without csrf: %v", err)
	}
}
