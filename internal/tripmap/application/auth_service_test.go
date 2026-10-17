package application_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/auth"
	"github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/ent/session"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

func TestRegisterMeLogout(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	res, err := e.auth.Register(ctx, connect.NewRequest(&tripmapv1.RegisterRequest{Username: " Traveler_1 ", Password: "correct horse battery", DisplayName: "小山"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.User.Username != "traveler_1" || res.Msg.User.DisplayName != "小山" || !strings.HasPrefix(res.Msg.Token, "tm_") {
		t.Fatalf("unexpected register response: %v", res.Msg)
	}
	me, err := e.auth.Me(ctx, bearer(res.Msg.Token, &tripmapv1.MeRequest{}))
	if err != nil || me.Msg.User.Id != res.Msg.User.Id {
		t.Fatalf("Me: %v %v", me, err)
	}
	if _, err = e.auth.Logout(ctx, bearer(res.Msg.Token, &tripmapv1.LogoutRequest{})); err != nil {
		t.Fatal(err)
	}
	_, err = e.auth.Me(ctx, bearer(res.Msg.Token, &tripmapv1.MeRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
}

func TestTokensAreStoredHashed(t *testing.T) {
	e := newEnv(t)
	token := e.register("hashcheck")
	rows, err := e.store.DB.Session.Query().All(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatalf("sessions: %v %v", rows, err)
	}
	if rows[0].TokenHash == token || rows[0].TokenHash != auth.HashToken(token) {
		t.Fatal("session must store only the token hash")
	}
	u, _ := e.store.DB.User.Query().Only(context.Background())
	if strings.Contains(u.PasswordHash, "correct horse") || !strings.HasPrefix(u.PasswordHash, "$2") {
		t.Fatal("password must be stored as a bcrypt hash")
	}
}

func TestRegisterValidation(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	for name, req := range map[string]*tripmapv1.RegisterRequest{
		"short username":   {Username: "ab", Password: "correct horse battery"},
		"bad characters":   {Username: "no spaces", Password: "correct horse battery"},
		"upper unicode":    {Username: "旅行者", Password: "correct horse battery"},
		"short password":   {Username: "validname", Password: "short"},
		"long password":    {Username: "validname", Password: strings.Repeat("a", 73)},
		"long displayname": {Username: "validname", Password: "correct horse battery", DisplayName: strings.Repeat("长", 41)},
	} {
		_, err := e.auth.Register(ctx, connect.NewRequest(req))
		if codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: code %v (%v)", name, codeOf(err), err)
		}
	}
	e.register("taken")
	_, err := e.auth.Register(ctx, connect.NewRequest(&tripmapv1.RegisterRequest{Username: "TAKEN", Password: "correct horse battery"}))
	wantCode(t, err, connect.CodeAlreadyExists)
}

func TestRegistrationCanBeClosed(t *testing.T) {
	e := newEnv(t, func(c *config.AppConfig) { c.Auth.RegistrationDisabled = true })
	_, err := e.auth.Register(context.Background(), connect.NewRequest(&tripmapv1.RegisterRequest{Username: "someone", Password: "correct horse battery"}))
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestLogin(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	e.register("alice")
	login := func(username, password string) error {
		_, err := e.auth.Login(ctx, connect.NewRequest(&tripmapv1.LoginRequest{Username: username, Password: password}))
		return err
	}
	if err := login("ALICE", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	wrong, unknown := login("alice", "wrong password"), login("nobody", "wrong password")
	wantCode(t, wrong, connect.CodeUnauthenticated)
	wantCode(t, unknown, connect.CodeUnauthenticated)
	if wrong.Error() != unknown.Error() {
		t.Fatalf("login must not reveal which usernames exist: %q vs %q", wrong, unknown)
	}
	wantCode(t, login("", "x"), connect.CodeInvalidArgument)
	if _, err := e.store.DB.User.Update().Where(user.UsernameEQ("alice")).SetDisabled(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	wantCode(t, login("alice", "correct horse battery"), connect.CodePermissionDenied)
}

func TestLoginIsRateLimitedPerAccount(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	e.register("victim")
	var last error
	for i := 0; i < 11; i++ {
		_, last = e.auth.Login(ctx, connect.NewRequest(&tripmapv1.LoginRequest{Username: "victim", Password: "guess number " + string(rune('a'+i))}))
	}
	wantCode(t, last, connect.CodeResourceExhausted)
	// Even the right password is refused while the window is exhausted.
	_, err := e.auth.Login(ctx, connect.NewRequest(&tripmapv1.LoginRequest{Username: "victim", Password: "correct horse battery"}))
	wantCode(t, err, connect.CodeResourceExhausted)
}

func TestProtectedProceduresRequireAValidSession(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	token := e.register("carol")
	_, err := e.foot.GetFootprints(ctx, bearer("", &tripmapv1.GetFootprintsRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = e.foot.GetFootprints(ctx, bearer("tm_not_a_real_token", &tripmapv1.GetFootprintsRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	if _, err = e.foot.GetFootprints(ctx, bearer(token, &tripmapv1.GetFootprintsRequest{})); err != nil {
		t.Fatal(err)
	}
	// Expired.
	if _, err = e.store.DB.Session.Update().SetExpiresAt(time.Now().Add(-time.Minute)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = e.foot.GetFootprints(ctx, bearer(token, &tripmapv1.GetFootprintsRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	// Disabled users lose access immediately even with a live session.
	token = e.register("dave")
	if _, err = e.store.DB.User.Update().Where(user.UsernameEQ("dave")).SetDisabled(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = e.foot.GetFootprints(ctx, bearer(token, &tripmapv1.GetFootprintsRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
}

func TestSessionListingAndRevocation(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	first := e.register("erin")
	login, err := e.auth.Login(ctx, connect.NewRequest(&tripmapv1.LoginRequest{Username: "erin", Password: "correct horse battery"}))
	if err != nil {
		t.Fatal(err)
	}
	second := login.Msg.Token
	list, err := e.auth.ListSessions(ctx, bearer(second, &tripmapv1.ListSessionsRequest{}))
	if err != nil || len(list.Msg.Sessions) != 2 {
		t.Fatalf("sessions: %v %v", list, err)
	}
	var current, other *tripmapv1.Session
	for _, s := range list.Msg.Sessions {
		if s.Current {
			current = s
		} else {
			other = s
		}
	}
	if current == nil || other == nil {
		t.Fatalf("expected one current and one other session: %v", list.Msg.Sessions)
	}
	// Another user cannot see or revoke it.
	stranger := e.register("mallory")
	_, err = e.auth.RevokeSession(ctx, bearer(stranger, &tripmapv1.RevokeSessionRequest{Id: other.Id}))
	wantCode(t, err, connect.CodeNotFound)
	if _, err = e.auth.Me(ctx, bearer(first, &tripmapv1.MeRequest{})); err != nil {
		t.Fatalf("stranger revoked someone else's session: %v", err)
	}
	// The owner can.
	if _, err = e.auth.RevokeSession(ctx, bearer(second, &tripmapv1.RevokeSessionRequest{Id: other.Id})); err != nil {
		t.Fatal(err)
	}
	_, err = e.auth.Me(ctx, bearer(first, &tripmapv1.MeRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	if n, _ := e.store.DB.Session.Query().Where(session.Revoked(false)).Count(ctx); n != 2 { // second + mallory
		t.Fatalf("active sessions = %d", n)
	}
}

func TestTripmapOAuthMovedToBFF(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	_, err := e.auth.ListOAuthProviders(ctx, connect.NewRequest(&tripmapv1.ListOAuthProvidersRequest{}))
	// OAuth RPCs are no longer public on tripmap Connect; callers must use bff.v1.
	wantCode(t, err, connect.CodeUnauthenticated)
}
