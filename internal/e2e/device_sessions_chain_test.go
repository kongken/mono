package e2e_test

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	usersrvratelimit "github.com/kongken/mono/internal/usersrv/ratelimit"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Device and session management through the real chain (OzID T3): the BFF, usersrv with
// PostgreSQL and Redis, and the metro and tripmap services where a test needs them.

// ListSessions lists the caller's live sessions.
func (b *Browser) ListSessions(ctx context.Context) ([]*bffv1.Session, error) {
	res, err := b.Auth.ListSessions(ctx, connect.NewRequest(&bffv1.ListSessionsRequest{}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetSessions(), nil
}

// RevokeSession revokes one session of the caller by ID.
func (b *Browser) RevokeSession(ctx context.Context, id string) error {
	req := connect.NewRequest(&bffv1.RevokeSessionRequest{Id: id})
	b.Write(ctx, req)
	_, err := b.Auth.RevokeSession(ctx, req)
	return err
}

// RevokeOtherSessions signs out every other device and returns how many sessions that ended.
func (b *Browser) RevokeOtherSessions(ctx context.Context) (int32, error) {
	req := connect.NewRequest(&bffv1.RevokeOtherSessionsRequest{})
	b.Write(ctx, req)
	res, err := b.Auth.RevokeOtherSessions(ctx, req)
	if err != nil {
		return 0, err
	}
	return res.Msg.GetRevoked(), nil
}

// fullMe is the owner's own account screen (email, roles, password check time).
func fullMe(ctx context.Context, b *Browser) (*bffv1.MeResponse, error) {
	res, err := b.Auth.Me(ctx, connect.NewRequest(&bffv1.MeRequest{}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// signedIn reports whether b's session still passes the BFF's check.
func signedIn(ctx context.Context, b *Browser) bool {
	_, err := b.Me(ctx)
	return err == nil
}

func (h *ChainHarness) authVersionOf(ctx context.Context, id string) int64 {
	h.T.Helper()
	st, err := h.Accounts.GetAuthState(ctx, userUUID(h.T, id))
	if err != nil {
		h.T.Fatal(err)
	}
	return st.AuthVersion
}

// Sign out other devices: ends every other session of the caller in one step, keeps the
// current one, leaves other accounts, the password, auth_version and management tokens
// alone, and does not touch a session opened afterwards.
func TestChainRevokeOtherSessionsKeepsTheCurrentOne(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	current, id := h.adminBrowser(ctx, "dev-owner")
	d2, d3 := h.FreshBrowser(), h.FreshBrowser()
	for _, b := range []*Browser{d2, d3} {
		if err := b.Login(ctx, "dev-owner", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	// Someone else's devices must never be touched.
	neighbour, neighbourPhone := h.FreshBrowser(), h.FreshBrowser()
	for _, b := range []*Browser{neighbour, neighbourPhone} {
		if err := b.LoginWithRolesOrLogin(ctx, "dev-neighbour"); err != nil {
			t.Fatal(err)
		}
	}
	token := h.mcp(h.newAdminToken(ctx, current, "kept", 30))
	listCities := func() bool {
		st, res := token.rpc("tools/call", map[string]any{"name": "metro_list_cities", "arguments": map[string]any{}})
		if st != http.StatusOK {
			return false
		}
		out := res["result"].(map[string]any)
		return out["isError"] != true
	}
	if !listCities() {
		t.Fatal("a fresh management token does not work")
	}
	version := h.authVersionOf(ctx, id)
	cookie := current.SessionCookie()

	n, err := current.RevokeOtherSessions(ctx)
	if err != nil || n != 2 {
		t.Fatalf("revoked %d, %v; want 2", n, err)
	}
	for name, b := range map[string]*Browser{"second device": d2, "third device": d3} {
		if _, err := b.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("%s kept its session: %v", name, err)
		}
	}
	if !signedIn(ctx, current) || current.SessionCookie() != cookie {
		t.Fatal("the current session was ended or its cookie changed")
	}
	for name, b := range map[string]*Browser{"neighbour": neighbour, "neighbour's phone": neighbourPhone} {
		if !signedIn(ctx, b) {
			t.Fatalf("%s lost a session to somebody else's call", name)
		}
	}
	if got := h.authVersionOf(ctx, id); got != version {
		t.Fatalf("auth_version moved from %d to %d", version, got)
	}
	if !listCities() {
		t.Fatal("signing out other devices revoked the management token")
	}
	// The password is untouched: signing in again works, and the new session stays.
	fresh := h.FreshBrowser()
	if err := fresh.Login(ctx, "dev-owner", "", ""); err != nil {
		t.Fatalf("sign in after signing out other devices: %v", err)
	}
	if !signedIn(ctx, fresh) {
		t.Fatal("a session opened after the call was ended by it")
	}
	if live, err := current.ListSessions(ctx); err != nil || len(live) != 2 {
		t.Fatalf("live sessions %d, %v; want the current one and the new one", len(live), err)
	}

	// Repeating it is harmless: nothing left to end except what has appeared since.
	if n, err = current.RevokeOtherSessions(ctx); err != nil || n != 1 {
		t.Fatalf("second call revoked %d, %v; want only the session opened since", n, err)
	}
	if n, err = current.RevokeOtherSessions(ctx); err != nil || n != 0 {
		t.Fatalf("third call revoked %d, %v; want 0", n, err)
	}
	if !signedIn(ctx, current) {
		t.Fatal("repeating the call ended the current session")
	}
}

// LoginWithRolesOrLogin registers the account of subject on first use and signs in afterwards.
func (b *Browser) LoginWithRolesOrLogin(ctx context.Context, subject string) error {
	if err := b.Login(ctx, subject, "", ""); err == nil {
		return nil
	}
	if err := b.register(ctx, b.h, subject, subject); err != nil {
		return err
	}
	return b.Login(ctx, subject, "", "")
}

// Concurrent calls: every session that exists is ended exactly once between them, however
// many calls run at the same moment, and the caller is never among them.
func TestChainRevokeOtherSessionsIsAtomicUnderConcurrency(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	current := h.FreshBrowser()
	if _, err := current.Register(ctx, "dev-race", "", "Race"); err != nil {
		t.Fatal(err)
	}
	const others = 6
	devices := make([]*Browser, others)
	for i := range devices {
		devices[i] = h.FreshBrowser()
		if err := devices[i].Login(ctx, "dev-race", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	// One CSRF token for all calls: they share a cookie jar, so each call asking for its
	// own token would race on the cookie.
	csrf := current.refreshCSRF(ctx)
	const calls = 8
	var wg sync.WaitGroup
	var revoked, failed atomic.Int32
	start := make(chan struct{})
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := connect.NewRequest(&bffv1.RevokeOtherSessionsRequest{})
			current.writeHeaders(req, csrf, current.Origin)
			<-start
			res, err := current.Auth.RevokeOtherSessions(ctx, req)
			if err != nil {
				failed.Add(1)
				return
			}
			revoked.Add(res.Msg.GetRevoked())
		}()
	}
	close(start)
	wg.Wait()
	if failed.Load() != 0 {
		t.Fatalf("%d concurrent calls failed", failed.Load())
	}
	if revoked.Load() != others {
		t.Fatalf("the calls ended %d sessions between them, want exactly %d", revoked.Load(), others)
	}
	for i, d := range devices {
		if signedIn(ctx, d) {
			t.Fatalf("device %d kept its session", i)
		}
	}
	if !signedIn(ctx, current) {
		t.Fatal("the current session did not survive")
	}
}

// The per-account limit (10 calls per 15 minutes) answers RESOURCE_EXHAUSTED and, when it
// refuses, ends nothing.
func TestChainRevokeOtherSessionsIsRateLimited(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	current := h.FreshBrowser()
	if _, err := current.Register(ctx, "dev-limit", "", "Limit"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := current.RevokeOtherSessions(ctx); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	late := h.FreshBrowser()
	if err := late.Login(ctx, "dev-limit", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := current.RevokeOtherSessions(ctx); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("the eleventh call: %v", err)
	}
	if !signedIn(ctx, late) {
		t.Fatal("a refused call still ended a session")
	}
}

// RevokeSession only ever reaches the caller's own sessions, answers the same for another
// account's session and an unknown one, and repeating it is harmless.
func TestChainRevokeSessionIsOwnedAndIdempotent(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	owner, phone := h.FreshBrowser(), h.FreshBrowser()
	if _, err := owner.Register(ctx, "dev-own", "", "Own"); err != nil {
		t.Fatal(err)
	}
	if err := phone.Login(ctx, "dev-own", "", ""); err != nil {
		t.Fatal(err)
	}
	stranger := h.FreshBrowser()
	if _, err := stranger.Register(ctx, "dev-stranger", "", "Stranger"); err != nil {
		t.Fatal(err)
	}
	sessions, err := owner.ListSessions(ctx)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("sessions %v, %v", sessions, err)
	}
	var phoneID, ownID string
	for _, s := range sessions {
		if s.GetCurrent() {
			ownID = s.GetId()
		} else {
			phoneID = s.GetId()
		}
	}
	strangerSessions, _ := stranger.ListSessions(ctx)
	if len(strangerSessions) != 1 {
		t.Fatalf("the stranger sees %d sessions", len(strangerSessions))
	}
	for _, s := range strangerSessions {
		if s.GetId() == ownID || s.GetId() == phoneID {
			t.Fatal("a session of another account appeared in the list")
		}
	}

	// Cross-account and unknown IDs: the same NOT_FOUND with the same text, nothing revoked.
	errOf := func(b *Browser, id string) error { return b.RevokeSession(ctx, id) }
	cross, unknown := errOf(stranger, phoneID), errOf(stranger, "00000000-0000-4000-8000-000000000000")
	if connect.CodeOf(cross) != connect.CodeNotFound || connect.CodeOf(unknown) != connect.CodeNotFound || cross.Error() != unknown.Error() {
		t.Fatalf("cross-account %v, unknown %v", cross, unknown)
	}
	if !signedIn(ctx, phone) {
		t.Fatal("a stranger ended somebody else's session")
	}

	// Concurrent duplicate revocations of the same device: every call succeeds.
	csrf := owner.refreshCSRF(ctx)
	var wg sync.WaitGroup
	var failed atomic.Int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := connect.NewRequest(&bffv1.RevokeSessionRequest{Id: phoneID})
			owner.writeHeaders(req, csrf, owner.Origin)
			if _, err := owner.Auth.RevokeSession(ctx, req); err != nil {
				failed.Add(1)
			}
		}()
	}
	wg.Wait()
	if failed.Load() != 0 || signedIn(ctx, phone) {
		t.Fatalf("%d duplicate revocations failed, or the device stayed signed in", failed.Load())
	}
	if err := owner.RevokeSession(ctx, phoneID); err != nil {
		t.Fatalf("a later repeat: %v", err)
	}
	if live, _ := owner.ListSessions(ctx); len(live) != 1 || !live[0].GetCurrent() {
		t.Fatalf("live sessions after the revocation: %v", live)
	}

	// Revoking the session of the request itself signs this browser out and clears its cookie.
	if err := owner.RevokeSession(ctx, ownID); err != nil {
		t.Fatal(err)
	}
	if owner.SessionCookie() != "" {
		t.Fatal("the cookie stayed after the session ended itself")
	}
	if _, err := owner.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("after ending its own session: %v", err)
	}
	if !signedIn(ctx, stranger) {
		t.Fatal("the stranger lost a session")
	}
}

// Device operations need a live session, the CSRF token and an allowed Origin, and a
// refused call changes nothing. With usersrv unreachable they are refused as a
// service failure (UNAVAILABLE) and nothing is revoked.
func TestChainDeviceOperationsRefuseBadCallersAndOutages(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b, other := h.FreshBrowser(), h.FreshBrowser()
	if _, err := b.Register(ctx, "dev-guard", "", "Guard"); err != nil {
		t.Fatal(err)
	}
	if err := other.Login(ctx, "dev-guard", "", ""); err != nil {
		t.Fatal(err)
	}
	csrf := b.refreshCSRF(ctx)

	for name, call := range map[string]func() error{
		"no csrf": func() error {
			req := connect.NewRequest(&bffv1.RevokeOtherSessionsRequest{})
			b.writeHeaders(req, "", b.Origin)
			_, err := b.Auth.RevokeOtherSessions(ctx, req)
			return err
		},
		"foreign origin": func() error {
			req := connect.NewRequest(&bffv1.RevokeOtherSessionsRequest{})
			b.writeHeaders(req, csrf, "https://evil.example")
			_, err := b.Auth.RevokeOtherSessions(ctx, req)
			return err
		},
	} {
		assertWriteRejectedNamed(t, name, call())
	}
	anonymous := h.FreshBrowser()
	if _, err := anonymous.RevokeOtherSessions(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("without a session: %v", err)
	}
	if !signedIn(ctx, other) {
		t.Fatal("a refused call ended a session")
	}

	h.FailUsersrv(func(string) error { return status.Error(codes.Unavailable, "usersrv is down") })
	if _, err := b.RevokeOtherSessions(ctx); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("with usersrv down: %v", err)
	}
	h.FailUsersrv(nil)
	if !signedIn(ctx, other) {
		t.Fatal("an outage ended a session")
	}
	if !signedIn(ctx, b) {
		t.Fatal("an outage ended the caller's own session")
	}
}

func assertWriteRejectedNamed(t *testing.T, name string, err error) {
	t.Helper()
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("%s: want permission denied, got %v", name, err)
	}
}

// Changing the password ends every browser session and management token of this account,
// the current device included, and no other account's; the old password stops working. A
// wrong current password changes nothing.
func TestChainChangePasswordEndsOnlyThisAccountsAccess(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	current, id := h.adminBrowser(ctx, "dev-pw")
	phone := h.FreshBrowser()
	if err := phone.Login(ctx, "dev-pw", "", ""); err != nil {
		t.Fatal(err)
	}
	token := h.mcp(h.newAdminToken(ctx, current, "old", 30))
	cities := func() bool {
		st, res := token.rpc("tools/call", map[string]any{"name": "metro_list_cities", "arguments": map[string]any{}})
		return st == http.StatusOK && res["result"].(map[string]any)["isError"] != true
	}
	neighbour, _ := h.adminBrowser(ctx, "dev-pw-neighbour")
	neighbourMCP := h.mcp(h.newAdminToken(ctx, neighbour, "neighbour", 30))
	version := h.authVersionOf(ctx, id)

	if err := current.ChangePassword(ctx, "not the current password", "a brand new password"); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong current password: %v", err)
	}
	if !signedIn(ctx, current) || !signedIn(ctx, phone) || !cities() || h.authVersionOf(ctx, id) != version {
		t.Fatal("a refused change altered the account")
	}
	if err := current.ChangePassword(ctx, E2EPassword, "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if got := h.authVersionOf(ctx, id); got != version+1 {
		t.Fatalf("auth_version %d, want %d", got, version+1)
	}
	for name, b := range map[string]*Browser{"this device": current, "the phone": phone} {
		if _, err := b.Me(ctx); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("%s kept its session: %v", name, err)
		}
	}
	if cities() {
		t.Fatal("the old management token survived the password change")
	}
	// The other account is not affected in any way.
	if !signedIn(ctx, neighbour) {
		t.Fatal("another account's session ended")
	}
	if st, res := neighbourMCP.rpc("tools/call", map[string]any{"name": "metro_list_cities", "arguments": map[string]any{}}); st != http.StatusOK || res["result"].(map[string]any)["isError"] == true {
		t.Fatal("another account's management token stopped working")
	}
	// Reauthenticating cannot bring an ended session back.
	if err := phone.Reauthenticate(ctx, "a brand new password"); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("reauthentication upgraded an ended session: %v", err)
	}
	fresh := h.FreshBrowser()
	if _, err := fresh.LoginWithEmail(ctx, emailFor("dev-pw"), E2EPassword); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("old password: %v", err)
	}
	if _, err := fresh.LoginWithEmail(ctx, emailFor("dev-pw"), "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if cities() {
		t.Fatal("signing in again brought the old token back")
	}
}

// The password check time on a session is the server's, never the client's: nothing in
// ReauthenticateRequest or the other requests carries one, a wrong password leaves it
// where it was, and only a real check moves it.
func TestChainReauthenticationTimeIsTheServers(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "dev-reauth", "", "Reauth"); err != nil {
		t.Fatal(err)
	}
	h.ageSession(b, 10*time.Minute)
	before, err := fullMe(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Reauthenticate(ctx, "not the password!!"); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong password: %v", err)
	}
	same, _ := fullMe(ctx, b)
	if !same.GetAuthenticatedAt().AsTime().Equal(before.GetAuthenticatedAt().AsTime()) {
		t.Fatal("a wrong password moved the check time")
	}
	// Hand-written client headers cannot set it either.
	req := connect.NewRequest(&bffv1.ReauthenticateRequest{Password: E2EPassword})
	b.Write(ctx, req)
	req.Header().Set("X-Authenticated-At", "2999-01-01T00:00:00Z")
	res, err := b.Auth.Reauthenticate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if at := res.Msg.GetAuthenticatedAt().AsTime(); at.After(now.Add(time.Minute)) || at.Before(now.Add(-time.Minute)) {
		t.Fatalf("the recorded time %v is not the server's now (%v)", at, now)
	}
	after, _ := fullMe(ctx, b)
	if !after.GetAuthenticatedAt().AsTime().After(before.GetAuthenticatedAt().AsTime()) {
		t.Fatal("a real check did not move the time")
	}
}

// Editing the shared profile changes the nickname and avatar only: not the login email, the
// roles, auth_version or the sessions, and not the rider profile or the catalog that
// metro keeps for itself.
func TestChainSharedProfileEditLeavesEverythingElseAlone(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	b, id := h.adminBrowser(ctx, "dev-profile")
	phone := h.FreshBrowser()
	if err := phone.Login(ctx, "dev-profile", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.MetroRider.UpdateMyProfile(ctx, riderWrite(b, &bffmetrov1.UpdateMyProfileRequest{Profile: &bffmetrov1.ProfileInput{
		Handle: "dev_profile", DisplayName: "Rider Name", Bio: "rides at night",
	}})); err != nil {
		t.Fatal(err)
	}
	riderBefore, err := b.MetroRider.GetMyProfile(ctx, connect.NewRequest(&bffmetrov1.GetMyProfileRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	meBefore, _ := fullMe(ctx, b)
	version := h.authVersionOf(ctx, id)
	cities := h.MetroDB.City.Query().CountX(ctx)

	user, err := b.UpdateMyProfile(ctx, "  Account Name ", "https://img.example.com/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if user.GetDisplayName() != "Account Name" || user.GetAvatarUrl() != "https://img.example.com/a.png" {
		t.Fatalf("saved profile %+v", user)
	}
	meAfter, err := fullMe(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if meAfter.GetEmail() != meBefore.GetEmail() || meAfter.GetEmail() != emailFor("dev-profile") {
		t.Fatalf("the login email changed: %q -> %q", meBefore.GetEmail(), meAfter.GetEmail())
	}
	if meAfter.GetUser().GetDisplayName() != "Account Name" {
		t.Fatalf("Me shows %q", meAfter.GetUser().GetDisplayName())
	}
	if len(meAfter.GetRoles()) != len(meBefore.GetRoles()) {
		t.Fatalf("roles %v -> %v", meBefore.GetRoles(), meAfter.GetRoles())
	}
	if h.authVersionOf(ctx, id) != version || !signedIn(ctx, phone) {
		t.Fatal("a profile edit moved auth_version or ended a session")
	}
	riderAfter, err := b.MetroRider.GetMyProfile(ctx, connect.NewRequest(&bffmetrov1.GetMyProfileRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if riderAfter.Msg.GetProfile().GetDisplayName() != riderBefore.Msg.GetProfile().GetDisplayName() ||
		riderAfter.Msg.GetProfile().GetBio() != riderBefore.Msg.GetProfile().GetBio() ||
		riderAfter.Msg.GetProfile().GetHandle() != riderBefore.Msg.GetProfile().GetHandle() {
		t.Fatalf("the rider profile changed: %+v -> %+v", riderBefore.Msg.GetProfile(), riderAfter.Msg.GetProfile())
	}
	if n := h.MetroDB.City.Query().CountX(ctx); n != cities {
		t.Fatalf("catalog rows %d -> %d", cities, n)
	}
	// Fields that are not part of the profile are not accepted: an email cannot be sent.
	if _, err := b.UpdateMyProfile(ctx, "", ""); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("an empty nickname: %v", err)
	}
}

// Guessing the current password through a stolen session is limited per account (usersrv,
// shared with the password confirmation): once the limit is hit even the right password is
// refused for a while, and nothing changes.
func TestChainChangePasswordAttemptsAreLimited(t *testing.T) {
	// The production default is 10 attempts per 15 minutes per account; the same rule, smaller.
	limits := usersrvLimits()
	limits.PasswordPerAccount = usersrvratelimit.Rule{Name: "password_per_account", Limit: 5, Window: 15 * time.Minute}
	h := NewChainHarness(t, ChainUsersrvLimits(limits))
	ctx := context.Background()
	b := h.FreshBrowser()
	id, err := b.Register(ctx, "dev-guess", "", "Guess")
	if err != nil {
		t.Fatal(err)
	}
	version := h.authVersionOf(ctx, id)
	limited := false
	for i := 0; i < 8 && !limited; i++ {
		switch connect.CodeOf(b.ChangePassword(ctx, "a wrong guess number one", "a brand new password")) {
		case connect.CodeResourceExhausted:
			limited = true
		case connect.CodeUnauthenticated:
		default:
			t.Fatalf("attempt %d: unexpected answer", i+1)
		}
	}
	if !limited {
		t.Fatal("eight wrong guesses were never limited under a limit of five")
	}
	if err := b.ChangePassword(ctx, E2EPassword, "a brand new password"); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("the right password while limited: %v", err)
	}
	if h.authVersionOf(ctx, id) != version || !signedIn(ctx, b) {
		t.Fatal("a limited attempt changed the account")
	}
}
