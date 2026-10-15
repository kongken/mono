package application_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/auth"
	"go.orx.me/apps/trip-map/internal/auth/provider"
	"go.orx.me/apps/trip-map/internal/config"
	"go.orx.me/apps/trip-map/internal/ent/session"
	"go.orx.me/apps/trip-map/internal/ent/user"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
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

func TestOAuthFlow(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider{name: "fake", claims: &provider.Claims{Provider: "fake", ExternalID: "42", Login: "octo", Name: "Octo Cat", Email: "octo@example.com", AvatarURL: "https://img.example/a.png"}})
	e.svc.SetProviders(reg)

	list, err := e.auth.ListOAuthProviders(ctx, connect.NewRequest(&tripmapv1.ListOAuthProvidersRequest{}))
	if err != nil || len(list.Msg.Providers) != 1 || list.Msg.Providers[0].Name != "fake" {
		t.Fatalf("providers: %v %v", list, err)
	}
	begin := func() string {
		res, err := e.auth.BeginOAuthFlow(ctx, connect.NewRequest(&tripmapv1.BeginOAuthFlowRequest{Provider: "fake"}))
		if err != nil || !strings.Contains(res.Msg.AuthorizeUrl, res.Msg.State) {
			t.Fatalf("begin: %v %v", res, err)
		}
		return res.Msg.State
	}
	complete := func(provider, code, state string) (*tripmapv1.CompleteOAuthFlowResponse, error) {
		res, err := e.auth.CompleteOAuthFlow(ctx, connect.NewRequest(&tripmapv1.CompleteOAuthFlowRequest{Provider: provider, Code: code, State: state}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	_, err = e.auth.BeginOAuthFlow(ctx, connect.NewRequest(&tripmapv1.BeginOAuthFlowRequest{Provider: "nope"}))
	wantCode(t, err, connect.CodeNotFound)

	state := begin()
	first, err := complete("fake", "good-code", state)
	if err != nil {
		t.Fatal(err)
	}
	if first.User.DisplayName != "Octo Cat" || first.User.Username != "" || first.User.AvatarUrl == "" {
		t.Fatalf("unexpected user: %v", first.User)
	}
	// The state is single-use.
	_, err = complete("fake", "good-code", state)
	wantCode(t, err, connect.CodePermissionDenied)
	// A state minted for another provider or never minted is rejected.
	_, err = complete("fake", "good-code", "forged-state")
	wantCode(t, err, connect.CodePermissionDenied)
	// A bad code is an authentication failure.
	_, err = complete("fake", "bad-code", begin())
	wantCode(t, err, connect.CodeUnauthenticated)
	// Signing in again reaches the same account, with a distinct session.
	again, err := complete("fake", "good-code", begin())
	if err != nil || again.User.Id != first.User.Id || again.Token == first.Token {
		t.Fatalf("second sign-in: %v %v", again, err)
	}
	// Provider email is not stored.
	u, _ := e.store.DB.User.Get(ctx, first.User.Id)
	if u.Email != nil {
		t.Fatal("provider email must not be stored")
	}
	// The OAuth session works against protected procedures.
	if _, err = e.foot.GetFootprints(ctx, bearer(first.Token, &tripmapv1.GetFootprintsRequest{})); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthRespectsClosedRegistration(t *testing.T) {
	e, ctx := newEnv(t, func(c *config.AppConfig) { c.Auth.RegistrationDisabled = true }), context.Background()
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider{name: "fake", claims: &provider.Claims{ExternalID: "7", Login: "new"}})
	e.svc.SetProviders(reg)
	begin, err := e.auth.BeginOAuthFlow(ctx, connect.NewRequest(&tripmapv1.BeginOAuthFlowRequest{Provider: "fake"}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.auth.CompleteOAuthFlow(ctx, connect.NewRequest(&tripmapv1.CompleteOAuthFlowRequest{Provider: "fake", Code: "good-code", State: begin.Msg.State}))
	wantCode(t, err, connect.CodePermissionDenied)
	if n, _ := e.store.DB.User.Query().Count(ctx); n != 0 {
		t.Fatalf("closed registration created %d users", n)
	}
}

// fakeLogto is an OIDC server that enforces PKCE the way Logto does: a code
// is only redeemable with the verifier whose S256 challenge was presented on
// the authorization request.
type fakeLogto struct {
	mu         sync.Mutex
	challenges map[string]string // code -> challenge registered by the test
	sub        string
	tokenCalls int
}

func newFakeLogto(t *testing.T, sub string) (*fakeLogto, string) {
	t.Helper()
	f := &fakeLogto{challenges: map[string]string{}, sub: sub}
	mux := http.NewServeMux()
	mux.HandleFunc("/oidc/token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.tokenCalls++
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		challenge, ok := f.challenges[r.PostForm.Get("code")]
		if !ok || provider.PKCEChallenge(r.PostForm.Get("code_verifier")) != challenge {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"PKCE verification failed"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"logto-access","token_type":"Bearer"}`))
	})
	mux.HandleFunc("/oidc/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer logto-access" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"` + f.sub + `","name":"Logto 旅行者","username":"logto_user","picture":"https://cdn.example/l.png"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv.URL + "/oidc"
}

// authorize registers the challenge from authorizeURL under code, as Logto
// would when it issues a code for that authorization request.
func (f *fakeLogto) authorize(t *testing.T, authorizeURL, code string) {
	t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.challenges[code] = u.Query().Get("code_challenge")
}

func TestLogtoOAuthFlowUsesPKCE(t *testing.T) {
	logto, issuer := newFakeLogto(t, "logto-sub-1")
	e, ctx := newEnv(t, func(c *config.AppConfig) {
		c.Auth.OAuthProviders = map[string]config.OAuthProvider{"logto": {
			IssuerURL: issuer, ClientID: "app", ClientSecret: "secret",
			RedirectURL: "http://localhost:5173/auth/callback/logto", DisplayName: "统一登录",
		}}
	}), context.Background()

	list, err := e.auth.ListOAuthProviders(ctx, connect.NewRequest(&tripmapv1.ListOAuthProvidersRequest{}))
	if err != nil || len(list.Msg.Providers) != 1 || list.Msg.Providers[0].Name != "logto" || list.Msg.Providers[0].DisplayName != "统一登录" {
		t.Fatalf("providers: %v %v", list, err)
	}
	begin := func() *tripmapv1.BeginOAuthFlowResponse {
		res, err := e.auth.BeginOAuthFlow(ctx, connect.NewRequest(&tripmapv1.BeginOAuthFlowRequest{Provider: "logto"}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	complete := func(code, state string) (*tripmapv1.CompleteOAuthFlowResponse, error) {
		res, err := e.auth.CompleteOAuthFlow(ctx, connect.NewRequest(&tripmapv1.CompleteOAuthFlowRequest{Provider: "logto", Code: code, State: state}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}

	first := begin()
	u, err := url.Parse(first.AuthorizeUrl)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Path != "/oidc/auth" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || q.Get("state") != first.State {
		t.Fatalf("authorize URL lacks PKCE parameters: %s", first.AuthorizeUrl)
	}
	if strings.Contains(first.AuthorizeUrl, "verifier") || strings.Contains(first.AuthorizeUrl, "secret") {
		t.Fatalf("authorize URL leaks a secret: %s", first.AuthorizeUrl)
	}
	// Every authorization attempt gets its own challenge.
	if other, _ := url.Parse(begin().AuthorizeUrl); other.Query().Get("code_challenge") == q.Get("code_challenge") {
		t.Fatal("PKCE challenge reused across attempts")
	}

	// A code issued for a different authorization request fails PKCE at Logto.
	logto.authorize(t, "https://logto.example/oidc/auth?code_challenge=someone-elses-challenge", "stolen-code")
	_, err = complete("stolen-code", first.State)
	wantCode(t, err, connect.CodeUnauthenticated)
	if n, _ := e.store.DB.User.Query().Count(ctx); n != 0 {
		t.Fatalf("failed sign-in created %d users", n)
	}

	// The state is consumed even when the exchange fails, so retry from the start.
	state := begin()
	logto.authorize(t, state.AuthorizeUrl, "good-code")
	signedIn, err := complete("good-code", state.State)
	if err != nil {
		t.Fatal(err)
	}
	if signedIn.User.DisplayName != "Logto 旅行者" || signedIn.User.Username != "" {
		t.Fatalf("unexpected user: %v", signedIn.User)
	}
	created, err := e.store.DB.User.Query().Where(user.Provider("logto"), user.ExternalID("logto-sub-1")).Only(ctx)
	if err != nil || created.ID != signedIn.User.Id {
		t.Fatalf("account for (logto, sub): %v %v", created, err)
	}
	// Single use: replaying the state never reaches Logto again.
	calls := logto.tokenCalls
	_, err = complete("good-code", state.State)
	wantCode(t, err, connect.CodePermissionDenied)
	if logto.tokenCalls != calls {
		t.Fatal("replayed state reached the token endpoint")
	}

	// A second sign-in reaches the same account with a distinct session.
	again := begin()
	logto.authorize(t, again.AuthorizeUrl, "second-code")
	second, err := complete("second-code", again.State)
	if err != nil || second.User.Id != signedIn.User.Id || second.Token == signedIn.Token {
		t.Fatalf("second sign-in: %v %v", second, err)
	}

	// The session works for synced data and account management.
	if _, err = e.foot.GetFootprints(ctx, bearer(signedIn.Token, &tripmapv1.GetFootprintsRequest{})); err != nil {
		t.Fatal(err)
	}
	sessions, err := e.auth.ListSessions(ctx, bearer(signedIn.Token, &tripmapv1.ListSessionsRequest{}))
	if err != nil || len(sessions.Msg.Sessions) != 2 {
		t.Fatalf("sessions: %v %v", sessions, err)
	}
	// A recent Logto session satisfies the re-authentication check for deletion.
	if _, err = e.auth.DeleteAccount(ctx, bearer(second.Token, &tripmapv1.DeleteAccountRequest{Confirmation: "DELETE"})); err != nil {
		t.Fatal(err)
	}
}

func TestLogtoStateCannotBeUsedForAnotherProvider(t *testing.T) {
	_, issuer := newFakeLogto(t, "sub")
	e, ctx := newEnv(t, func(c *config.AppConfig) {
		c.Auth.OAuthProviders = map[string]config.OAuthProvider{"logto": {IssuerURL: issuer, ClientID: "app", ClientSecret: "secret", RedirectURL: "http://localhost:5173/auth/callback/logto"}}
	}), context.Background()
	reg := provider.BuildRegistry(e.cfg.Auth)
	reg.Register(&fakeProvider{name: "fake", claims: &provider.Claims{ExternalID: "9"}})
	e.svc.SetProviders(reg)
	begin, err := e.auth.BeginOAuthFlow(ctx, connect.NewRequest(&tripmapv1.BeginOAuthFlowRequest{Provider: "logto"}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.auth.CompleteOAuthFlow(ctx, connect.NewRequest(&tripmapv1.CompleteOAuthFlowRequest{Provider: "fake", Code: "good-code", State: begin.Msg.State}))
	wantCode(t, err, connect.CodePermissionDenied)
}
