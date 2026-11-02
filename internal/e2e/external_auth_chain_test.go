package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	bffconfig "github.com/kongken/mono/internal/bff/config"
	bffent "github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/ent/oauthbrowserstate"
	bffsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/navigation"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/externalprovider"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type browserGrant struct {
	request  externalprovider.AuthorizationRequest
	identity externalprovider.ProviderIdentity
}

// browserProvider is the local provider boundary: it requires the original
// nonce/PKCE and accepts a code only once. Adapter cryptographic contracts are
// separately tested against local OIDC/JWKS and GitHub HTTP servers.
type browserProvider struct {
	mu               sync.Mutex
	name             externalprovider.ProviderName
	subject, email   string
	grants           map[string]browserGrant
	exchanges        int
	entered, release chan struct{}
}

func (p *browserProvider) Name() externalprovider.ProviderName { return p.name }
func (p *browserProvider) ValidateConfiguration() error        { return nil }
func (p *browserProvider) AuthorizationURL(_ context.Context, r externalprovider.AuthorizationRequest) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	issuer := externalprovider.GoogleIssuer
	trust := externalprovider.GoogleAuthoritative
	endpoint := "https://accounts.google.com/o/oauth2/v2/auth"
	if p.name == externalprovider.GitHubName {
		issuer = externalprovider.GitHubIssuer
		trust = externalprovider.ProviderVerified
		endpoint = "https://github.com/login/oauth/authorize"
	}
	p.grants[r.State] = browserGrant{r, externalprovider.ProviderIdentity{Provider: p.name, Issuer: issuer, Subject: p.subject, Email: p.email, EmailVerified: true, EmailTrust: trust, DisplayName: "Provider snapshot", AuthenticatedAt: time.Now()}}
	q := url.Values{"state": {r.State}, "code_challenge": {r.CodeChallenge}, "code_challenge_method": {"S256"}}
	if r.Nonce != "" {
		q.Set("nonce", r.Nonce)
	}
	return endpoint + "?" + q.Encode(), nil
}
func (p *browserProvider) ExchangeCode(ctx context.Context, r externalprovider.CodeExchangeRequest) (externalprovider.ProviderIdentity, error) {
	p.mu.Lock()
	g, ok := p.grants[r.Code]
	delete(p.grants, r.Code)
	p.exchanges++
	entered, release := p.entered, p.release
	p.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return externalprovider.ProviderIdentity{}, externalprovider.ErrUnavailable
		}
	}
	sum := sha256.Sum256([]byte(r.Verifier))
	if !ok || r.Provider != p.name || r.ConfigVersion != 1 || r.Nonce != g.request.Nonce || r.CodeChallenge != g.request.CodeChallenge || base64.RawURLEncoding.EncodeToString(sum[:]) != g.request.CodeChallenge {
		return externalprovider.ProviderIdentity{}, externalprovider.ErrRejected
	}
	return g.identity, nil
}
func (p *browserProvider) next(subject, email string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subject, p.email = subject, email
}
func (p *browserProvider) count() int { p.mu.Lock(); defer p.mu.Unlock(); return p.exchanges }

type externalStack struct {
	h              *ChainHarness
	google, github *browserProvider
	settings       account.ExternalSettings
}

func externalChain(t *testing.T) *externalStack {
	t.Helper()
	keyFile := filepath.Join(t.TempDir(), "state.key")
	if err := os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))), 0600); err != nil {
		t.Fatal(err)
	}
	h := NewChainHarness(t, ChainHTTPOptions{Configure: func(c *bffconfig.Config, base string) {
		c.Auth.External = bffconfig.ExternalConfig{Enabled: true, Providers: map[string]bool{"google": true, "github": true}, StateEncryptionKeyID: "state-1", StateEncryptionKeyFiles: map[string]string{"state-1": keyFile}}
		product := strings.Replace(base, "localhost:", "trip.localhost:", 1)
		admin := strings.Replace(base, "localhost:", "admin.localhost:", 1)
		c.Navigation = navigation.Config{APIOrigin: base, Apps: map[string]navigation.App{"ozid": {Origin: base}, "trip-map": {Origin: product, Paths: []string{"/city/*"}}, "admin": {Origin: admin}}}
		c.CORSAllowedOrigins = []string{base, product, admin}
		c.Admin.AllowedCIDRs = []string{"127.0.0.1/32", "::1/128"}
	}})
	e := &externalStack{h: h, google: &browserProvider{name: externalprovider.GoogleName, subject: "google-stable", email: "candidate@gmail.com", grants: map[string]browserGrant{}}, github: &browserProvider{name: externalprovider.GitHubName, subject: "424242", email: "candidate@example.com", grants: map[string]browserGrant{}}}
	codec, err := h.UsersrvRes.Mail.ProofCipher("k1")
	if err != nil {
		t.Fatal(err)
	}
	e.settings = account.ExternalSettings{Providers: map[externalprovider.ProviderName]externalprovider.ExternalProvider{externalprovider.GoogleName: e.google, externalprovider.GitHubName: e.github}, Policies: map[externalprovider.ProviderName]account.ProviderPolicy{externalprovider.GoogleName: {Enabled: true, ConfigVersion: 1, RedirectURI: h.BFFURL + "/api/identity/callback/google"}, externalprovider.GitHubName: {Enabled: true, ConfigVersion: 1, RedirectURI: h.BFFURL + "/api/identity/callback/github"}}, Codec: codec}
	h.Accounts.ConfigureExternal(e.settings)
	return e
}
func extClient(b *Browser) bffv1connect.ExternalAuthServiceClient {
	return bffv1connect.NewExternalAuthServiceClient(b.HTTP, b.APIURL)
}
func startExternal(t *testing.T, b *Browser, provider, intent string) *bffv1.StartExternalLoginResponse {
	t.Helper()
	req := connect.NewRequest(&bffv1.StartExternalLoginRequest{Provider: provider, Intent: intent, AppId: "trip-map", ReturnPath: "/city/shanghai"})
	b.Write(t.Context(), req)
	res, err := extClient(b).StartExternalLogin(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg
}
func callbackExternal(t *testing.T, b *Browser, start *bffv1.StartExternalLoginResponse, provider, query string) *http.Response {
	t.Helper()
	u, err := url.Parse(start.GetAuthorizationUrl())
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	target := b.h.BFFURL + "/api/identity/callback/" + provider + "?state=" + url.QueryEscape(state)
	if query == "" {
		target += "&code=" + url.QueryEscape(state)
	} else {
		target += "&" + query
	}
	client := *b.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("unsafe callback: %d %v", res.StatusCode, res.Header)
	}
	if strings.Contains(res.Header.Get("Location"), state) {
		t.Fatal("state leaked to landing URL")
	}
	return res
}
func getExternal(t *testing.T, b *Browser, id string) (*bffv1.GetExternalFlowResponse, error) {
	t.Helper()
	req := connect.NewRequest(&bffv1.GetExternalFlowRequest{FlowId: id})
	req.Header().Set("Origin", b.Origin)
	res, err := extClient(b).GetExternalFlow(t.Context(), req)
	if err != nil {
		return nil, err
	}
	return res.Msg, err
}
func finishExternal(t *testing.T, b *Browser, id string) (*connect.Response[bffv1.CompleteExternalRegistrationResponse], error) {
	t.Helper()
	req := connect.NewRequest(&bffv1.CompleteExternalRegistrationRequest{FlowId: id, MutationId: "create-once", DisplayName: "Owner profile"})
	b.Write(t.Context(), req)
	return extClient(b).CompleteExternalRegistration(t.Context(), req)
}

func identityClient(b *Browser) bffv1connect.IdentityServiceClient {
	return bffv1connect.NewIdentityServiceClient(b.HTTP, b.APIURL)
}

func TestExternalBrowserIdentityLinkBindsAfterOperationConfirmation(t *testing.T) {
	e := externalChain(t)
	b := e.h.FreshBrowser()
	start := startExternal(t, b, "google", "login")
	callbackExternal(t, b, start, "google", "")
	if _, err := finishExternal(t, b, start.GetFlowId()); err != nil {
		t.Fatal(err)
	}
	begin := connect.NewRequest(&bffv1.BeginIdentityOperationRequest{Kind: "identity_link", Provider: "github"})
	b.Write(t.Context(), begin)
	op, err := identityClient(b).BeginIdentityOperation(t.Context(), begin)
	if err != nil {
		t.Fatal(err)
	}
	e.h.DeliverMail()
	confirm := connect.NewRequest(&bffv1.ConfirmIdentityOperationRequest{ChallengeId: op.Msg.GetChallengeId(), Token: e.h.Inbox.TokenIn(t, "candidate@gmail.com")})
	b.Write(t.Context(), confirm)
	if _, err = identityClient(b).ConfirmIdentityOperation(t.Context(), confirm); err != nil {
		t.Fatal(err)
	}
	startReq := connect.NewRequest(&bffv1.StartExternalLoginRequest{Provider: "github", Intent: "link", AppId: "ozid", ReturnPath: "/identities"})
	b.Write(t.Context(), startReq)
	linkedStart, err := extClient(b).StartExternalLogin(t.Context(), startReq)
	if err != nil {
		t.Fatal(err)
	}
	callbackExternal(t, b, linkedStart.Msg, "github", "")
	link := connect.NewRequest(&bffv1.LinkExternalIdentityRequest{OperationChallengeId: op.Msg.GetChallengeId(), ProviderFlowId: linkedStart.Msg.GetFlowId(), MutationId: "identity-link-once"})
	b.Write(t.Context(), link)
	result, err := identityClient(b).LinkExternalIdentity(t.Context(), link)
	if err != nil || result.Msg.GetExternalIdentityId() == "" || result.Msg.GetReplayed() {
		t.Fatalf("link: %v %#v", err, result)
	}
	if got := e.h.UsersrvDB.ExternalIdentity.Query().CountX(t.Context()); got != 2 {
		t.Fatalf("external identities: got %d, want 2", got)
	}
}
func TestExternalBrowserRegistrationAndStableLogin(t *testing.T) {
	e := externalChain(t)
	h := e.h
	b := h.FreshBrowser()
	start := startExternal(t, b, "google", "login")
	row := h.BFFDB.OAuthBrowserState.GetX(t.Context(), start.GetFlowId())
	u, _ := url.Parse(start.GetAuthorizationUrl())
	if len(row.PkceVerifierCiphertext) == 0 || strings.Contains(string(row.PkceVerifierCiphertext), u.Query().Get("nonce")) {
		t.Fatal("unencrypted protocol payload")
	}
	res := callbackExternal(t, b, start, "google", "")
	if len(res.Cookies()) != 0 || b.SessionCookie() != "" || h.UsersrvDB.User.Query().CountX(t.Context()) != 0 {
		t.Fatal("callback created account/session before confirmation")
	}
	pending, err := getExternal(t, b, start.GetFlowId())
	if err != nil || pending.GetStatus() != "registration_pending" || !pending.GetEmailConfirmed() {
		t.Fatalf("pending: %v %v", pending, err)
	}
	other := h.FreshBrowser()
	if _, err = finishExternal(t, other, start.GetFlowId()); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("other browser submitted flow")
	}
	if _, err = finishExternal(t, b, start.GetFlowId()); err != nil {
		t.Fatal(err)
	}
	me, err := b.MeFull(t.Context())
	if err != nil || me.GetAuthenticatedAt() != nil || me.GetUser().GetDisplayName() != "Owner profile" || len(me.GetRoles()) != 0 {
		t.Fatalf("external session/password time: %v %v", me, err)
	}
	uid := me.GetUser().GetId()
	user := h.UsersrvDB.User.GetX(t.Context(), uuid.MustParse(uid))
	if user.PasswordHash != nil {
		t.Fatal("external account acquired password")
	}
	if h.UsersrvDB.ExternalIdentity.Query().CountX(t.Context()) != 1 {
		t.Fatal("missing first identity")
	}
	sessions := h.BFFDB.Session.Query().CountX(t.Context())
	if replay, replayErr := finishExternal(t, b, start.GetFlowId()); replayErr != nil || len(replay.Header().Values("Set-Cookie")) != 0 || h.BFFDB.Session.Query().CountX(t.Context()) != sessions {
		t.Fatalf("receipt replay minted session: %v", err)
	}
	before := b.SessionCookie()
	res = callbackExternal(t, b, start, "google", "")
	if !strings.Contains(res.Header.Get("Location"), "STATE_INVALID") || b.SessionCookie() != before {
		t.Fatal("callback replay overwrote session")
	}
	row = h.BFFDB.OAuthBrowserState.GetX(t.Context(), start.GetFlowId())
	if len(row.PkceVerifierCiphertext) != 0 || row.ConsumedAt == nil {
		t.Fatal("protocol proof not erased")
	}
	// Stable key determines UUID despite profile/email changes; registration may
	// close without disabling an existing external identity's login.
	h.Accounts.SetRegistrationOpen(false)
	e.google.next("google-stable", "new@gmail.com")
	next := h.FreshBrowser()
	s2 := startExternal(t, next, "google", "login")
	res = callbackExternal(t, next, s2, "google", "")
	me, err = next.MeFull(t.Context())
	if err != nil || me.GetUser().GetId() != uid || me.GetUser().GetDisplayName() != "Owner profile" || me.GetEmail() != "candidate@gmail.com" {
		t.Fatalf("stable identity: %v %v %s", me, err, res.Header.Get("Location"))
	}
	e.google.next("new-subject", "new-account@gmail.com")
	fresh := h.FreshBrowser()
	s3 := startExternal(t, fresh, "google", "login")
	res = callbackExternal(t, fresh, s3, "google", "")
	if !strings.Contains(res.Header.Get("Location"), "REGISTRATION_UNAVAILABLE") || fresh.SessionCookie() != "" || h.UsersrvDB.User.Query().CountX(t.Context()) != 1 {
		t.Fatal("closed registration created account")
	}
}

func TestExternalBrowserMailAndFinalGate(t *testing.T) {
	e := externalChain(t)
	h := e.h
	b := h.FreshBrowser()
	s := startExternal(t, b, "github", "login")
	callbackExternal(t, b, s, "github", "")
	pending, err := getExternal(t, b, s.GetFlowId())
	if err != nil || pending.GetEmailConfirmed() {
		t.Fatal("GitHub waived OzID mailbox confirmation")
	}
	if _, err = finishExternal(t, b, s.GetFlowId()); err == nil {
		t.Fatal("unverified contact completed")
	}
	// Use another independent browser flow after a refused first final attempt.
	s = startExternal(t, b, "github", "login")
	callbackExternal(t, b, s, "github", "")
	row := h.BFFDB.OAuthBrowserState.GetX(t.Context(), s.GetFlowId())
	mail := connect.NewRequest(&bffv1.RequestExternalRegistrationEmailRequest{FlowId: s.GetFlowId(), Email: "edited@example.com"})
	b.Write(t.Context(), mail)
	if _, err = extClient(b).RequestExternalRegistrationEmail(t.Context(), mail); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	messages := h.Inbox.To("edited@example.com")
	if len(messages) != 1 {
		t.Fatal("mail missing")
	}
	text := messages[0].Text
	fragment := strings.Split(strings.Fields(strings.Split(text, "#")[1])[0], "\n")[0]
	q, err := url.ParseQuery(fragment)
	if err != nil {
		t.Fatal(err)
	}
	token := q.Get("token")
	if token == "" || q.Get("challenge_id") != row.UsersrvChallengeID {
		t.Fatal("no external email token")
	}
	// A GET of the flow has no confirming side effect, even after mail delivery.
	if p, err := getExternal(t, b, s.GetFlowId()); err != nil || p.GetEmailConfirmed() {
		t.Fatal("GET confirmed mail")
	}
	other := h.FreshBrowser()
	confirm := connect.NewRequest(&bffv1.ConfirmExternalRegistrationEmailRequest{ChallengeId: q.Get("challenge_id"), Token: token})
	other.Write(t.Context(), confirm)
	if _, err = extClient(other).ConfirmExternalRegistrationEmail(t.Context(), confirm); err != nil {
		t.Fatal(err)
	}
	if h.UsersrvDB.User.Query().CountX(t.Context()) != 0 || other.SessionCookie() != "" {
		t.Fatal("mail device acquired account authority")
	}
	if p, err := getExternal(t, b, s.GetFlowId()); err != nil || !p.GetEmailConfirmed() {
		t.Fatal("original browser cannot see confirmed mail")
	}
	h.Accounts.SetRegistrationOpen(false)
	if _, err = finishExternal(t, b, s.GetFlowId()); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("closed after mail: %v", err)
	}
	if h.UsersrvDB.User.Query().CountX(t.Context()) != 0 || h.UsersrvDB.ExternalIdentity.Query().CountX(t.Context()) != 0 || b.SessionCookie() != "" {
		t.Fatal("final gate partially created account")
	}
}

func TestExternalBrowserStateIntentAndLateResults(t *testing.T) {
	e := externalChain(t)
	h := e.h
	b := h.FreshBrowser()
	s := startExternal(t, b, "google", "login")
	other := h.FreshBrowser()
	for _, tc := range []struct {
		browser  *Browser
		provider string
		query    string
	}{{other, "google", ""}, {b, "github", ""}, {b, "google", "state=duplicate&code=x"}} {
		res := callbackExternal(t, tc.browser, s, tc.provider, tc.query)
		if !strings.Contains(res.Header.Get("Location"), "STATE_INVALID") {
			t.Fatal("invalid state admitted")
		}
	}
	if e.google.count() != 0 || h.BFFDB.Session.Query().CountX(t.Context()) != 0 {
		t.Fatal("invalid callback reached usersrv")
	}
	// Independent tabs survive one another's cancellation, including recovery Cookies.
	s2 := startExternal(t, b, "google", "login")
	u, _ := url.Parse(h.BFFURL)
	b.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: session.RecoveryCookieName("unfinished-deletion"), Value: "unfinished-deletion", Path: "/"}})
	res := callbackExternal(t, b, s, "google", "error=access_denied&error_description=SECRET-PROVIDER-TEXT")
	if !strings.Contains(res.Header.Get("Location"), "PROVIDER_REJECTED") || strings.Contains(res.Header.Get("Location"), "SECRET") {
		t.Fatal("cancel exposed provider error")
	}
	if len(res.Cookies()) != 0 {
		t.Fatal("cancel mutated other cookies")
	}
	callbackExternal(t, b, s2, "google", "")
	if _, err := finishExternal(t, b, s2.GetFlowId()); err != nil {
		t.Fatal(err)
	}
	original := b.SessionCookie()
	staleLink := connect.NewRequest(&bffv1.StartExternalLoginRequest{Provider: "google", Intent: "link", AppId: "ozid"})
	b.Write(t.Context(), staleLink)
	staleLink.Header().Set("X-Kongken-User", uuid.NewString())
	if _, err := extClient(b).StartExternalLogin(t.Context(), staleLink); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("stale account view began a link for another account")
	}
	link := startExternal(t, b, "google", "link")
	res = callbackExternal(t, b, link, "google", "")
	p, err := getExternal(t, b, link.GetFlowId())
	if err != nil || p.GetStatus() != "link_pending" || b.SessionCookie() != original || len(res.Cookies()) != 0 {
		t.Fatal("link intent became a login")
	}
	if _, err = finishExternal(t, b, link.GetFlowId()); err == nil {
		t.Fatal("link proof used to create account")
	}
	// Hold the exchange; change the browser account before releasing it. No
	// callback Set-Cookie may replace the new password session.
	e.google.mu.Lock()
	e.google.entered = make(chan struct{})
	e.google.release = make(chan struct{})
	entered, release := e.google.entered, e.google.release
	e.google.mu.Unlock()
	late := startExternal(t, b, "google", "login")
	done := make(chan *http.Response, 1)
	go func() { done <- callbackExternal(t, b, late, "google", "") }()
	<-entered
	if _, err = b.Register(t.Context(), "switched", "", "Switched"); err != nil {
		t.Fatal(err)
	}
	newCookie := b.SessionCookie()
	close(release)
	res = <-done
	if !strings.Contains(res.Header.Get("Location"), "STATE_INVALID") || len(res.Cookies()) != 0 || b.SessionCookie() != newCookie {
		t.Fatal("late authorization replaced new session")
	}
	if me, err := b.Me(t.Context()); err != nil || me.GetDisplayName() != "Switched" {
		t.Fatal("new account lost")
	}
}

func TestExternalBrowserAtomicCallbackAndFailure(t *testing.T) {
	e := externalChain(t)
	h := e.h
	b := h.FreshBrowser()
	s := startExternal(t, b, "google", "login")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { callbackExternal(t, b, s, "google", "") })
	}
	wg.Wait()
	if e.google.count() != 1 {
		t.Fatal("callback exchanged more than once")
	}
	if h.BFFDB.Session.Query().CountX(t.Context()) != 0 || h.UsersrvDB.User.Query().CountX(t.Context()) != 0 {
		t.Fatal("pending callback created session")
	}
	// Config closes after start; usersrv's current policy rejects the callback.
	s2 := startExternal(t, b, "github", "login")
	e.settings.Policies[externalprovider.GitHubName] = account.ProviderPolicy{Enabled: false, ConfigVersion: 1, RedirectURI: h.BFFURL + "/api/identity/callback/github"}
	h.Accounts.ConfigureExternal(e.settings)
	res := callbackExternal(t, b, s2, "github", "")
	if !strings.Contains(res.Header.Get("Location"), "PROVIDER_DISABLED") {
		t.Fatal("closed provider admitted")
	}
	// Corrupting encrypted state fails closed and erases it, without provider IO.
	s3 := startExternal(t, b, "google", "login")
	h.BFFDB.OAuthBrowserState.UpdateOneID(s3.GetFlowId()).SetPkceVerifierCiphertext([]byte("tampered")).ExecX(t.Context())
	before := e.google.count()
	callbackExternal(t, b, s3, "google", "")
	if e.google.count() != before {
		t.Fatal("tampered proof reached provider")
	}
	if h.BFFDB.OAuthBrowserState.Query().Where(oauthbrowserstate.ID(s3.GetFlowId()), oauthbrowserstate.PkceVerifierCiphertextNotNil()).CountX(t.Context()) != 0 {
		t.Fatal("tampered proof survived terminal callback")
	}
	// A valid external session is invalid as soon as usersrv increments the version.
	h.Accounts.SetRegistrationOpen(true)
	if _, err := finishExternal(t, b, s.GetFlowId()); err != nil {
		t.Fatal(err)
	}
	me, err := b.Me(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(me.GetId())
	h.UsersrvDB.User.UpdateOneID(id).AddAuthVersion(1).ExecX(t.Context())
	if _, err = b.Me(t.Context()); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("auth_version not rechecked")
	}
	if h.BFFDB.Session.Query().Where(bffsession.Revoked(true)).CountX(t.Context()) != 1 {
		t.Fatal("stale session not revoked")
	}
}

func TestExternalBrowserPerimeterAndNavigation(t *testing.T) {
	e := externalChain(t)
	b := e.h.FreshBrowser()
	client := extClient(b)
	for _, tc := range []struct {
		csrf, origin, path string
		code               connect.Code
	}{{"", "", "/", connect.CodePermissionDenied}, {"yes", "https://evil.example.com", "/", connect.CodePermissionDenied}, {"yes", b.Origin, "//evil.example.com", connect.CodeInvalidArgument}, {"yes", b.Origin, "/city/%2e%2e", connect.CodeInvalidArgument}} {
		req := connect.NewRequest(&bffv1.StartExternalLoginRequest{Provider: "google", Intent: "login", AppId: "trip-map", ReturnPath: tc.path})
		csrf := ""
		if tc.csrf != "" {
			csrf = b.refreshCSRF(t.Context())
		}
		b.writeHeaders(req, csrf, tc.origin)
		if _, err := client.StartExternalLogin(t.Context(), req); connect.CodeOf(err) != tc.code {
			t.Fatalf("perimeter %s: %v", tc.path, err)
		}
	}
	req := connect.NewRequest(&bffv1.StartExternalLoginRequest{Provider: "google", Intent: "link", AppId: "ozid"})
	b.Write(t.Context(), req)
	if _, err := client.StartExternalLogin(t.Context(), req); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("anonymous link intent admitted")
	}
	for _, path := range []string{"/auth/login", "/auth/callback/google", "/api/identity/callback/other"} {
		res, err := b.HTTP.Get(e.h.BFFURL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatalf("legacy/unknown path %s = %d", path, res.StatusCode)
		}
	}
	if e.h.UsersrvDB.User.Query().CountX(t.Context()) != 0 {
		t.Fatal("perimeter created account")
	}
}

func TestExternalChromium(t *testing.T) {
	if os.Getenv("EXTERNAL_BROWSER_E2E") != "1" {
		t.Skip("set EXTERNAL_BROWSER_E2E=1 for real Chromium browser flows")
	}
	e := externalChain(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	run(t, root, []string{"EXTERNAL_BFF_URL=" + e.h.BFFURL}, "node", "scripts/test-external-browser.mjs")
	if e.h.UsersrvDB.User.Query().CountX(t.Context()) != 1 || e.google.count() < 2 {
		t.Fatal("Chromium did not exercise creation and stable login")
	}
}

func TestExternalBrowserCommittedButUndelivered(t *testing.T) {
	for _, fault := range []string{"usersrv creation response lost", "BFF session write failed"} {
		t.Run(fault, func(t *testing.T) {
			e := externalChain(t)
			h := e.h
			b := h.FreshBrowser()
			if _, err := b.Register(t.Context(), "existing", "", "Existing"); err != nil {
				t.Fatal(err)
			}
			original := b.SessionCookie()
			s := startExternal(t, b, "google", "login")
			callbackExternal(t, b, s, "google", "")
			var fail atomic.Bool
			if fault == "usersrv creation response lost" {
				h.LoseUsersrvResponse("/usersrv.v1.ExternalIdentityService/CompleteExternalRegistration")
			} else {
				fail.Store(true)
				h.BFFDB.Session.Use(func(next bffent.Mutator) bffent.Mutator {
					return bffent.MutateFunc(func(ctx context.Context, m bffent.Mutation) (bffent.Value, error) {
						if fail.Load() && m.Op().Is(bffent.OpCreate) {
							return nil, errors.New("injected session storage outage")
						}
						return next.Mutate(ctx, m)
					})
				})
			}
			if _, err := finishExternal(t, b, s.GetFlowId()); err == nil {
				t.Fatal("injected delivery failure ignored")
			}
			fail.Store(false)
			if b.SessionCookie() != original || h.UsersrvDB.User.Query().CountX(t.Context()) != 2 {
				t.Fatal("committed creation lost or old Cookie overwritten")
			}
			if me, err := b.Me(t.Context()); err != nil || me.GetDisplayName() != "Existing" {
				t.Fatal("failed callback revoked existing session")
			}
			res, err := finishExternal(t, b, s.GetFlowId())
			if err != nil || !res.Msg.GetLoginRequired() || res.Header().Get("Set-Cookie") != "" {
				t.Fatalf("receipt replay acquired login authority: %v", err)
			}
			fresh := h.FreshBrowser()
			login := startExternal(t, fresh, "google", "login")
			callbackExternal(t, fresh, login, "google", "")
			if _, err = fresh.Me(t.Context()); err != nil {
				t.Fatal("committed account cannot sign in")
			}
			if h.UsersrvDB.User.Query().CountX(t.Context()) != 2 {
				t.Fatal("relogin duplicated committed account")
			}
		})
	}
}

func TestExternalBrowserOutageAndExpiryPreserveSession(t *testing.T) {
	e := externalChain(t)
	h := e.h
	b := h.FreshBrowser()
	if _, err := b.Register(t.Context(), "preserved", "", "Preserved"); err != nil {
		t.Fatal(err)
	}
	original := b.SessionCookie()
	s := startExternal(t, b, "google", "login")
	h.FailUsersrv(func(method string) error {
		if strings.Contains(method, "CompleteExternalLogin") {
			return status.Error(codes.Unavailable, "secret upstream diagnostics")
		}
		return nil
	})
	res := callbackExternal(t, b, s, "google", "")
	if !strings.Contains(res.Header.Get("Location"), "UNAVAILABLE") || len(res.Cookies()) != 0 || b.SessionCookie() != original {
		t.Fatal("provider/RPC outage changed session")
	}
	h.FailUsersrv(nil)
	callbackExternal(t, b, s, "google", "")
	if e.google.count() != 0 {
		t.Fatal("failed callback replayed code")
	}
	s = startExternal(t, b, "google", "login")
	h.BFFDB.OAuthBrowserState.Update().Where(oauthbrowserstate.ID(s.GetFlowId())).SetConsumedAt(time.Now().Add(-time.Minute)).ExecX(t.Context())
	callbackExternal(t, b, s, "google", "")
	if e.google.count() != 0 {
		t.Fatal("consumed state exchanged")
	}
	// Persisted expiry is immutable to application callers; SQL models a state
	// left across downtime. The normal runner must erase it without a callback.
	if _, err := h.BFFDB.ExecContext(t.Context(), `UPDATE oauth_browser_states SET expires_at = now() - interval '1 second' WHERE id = $1`, s.GetFlowId()); err != nil {
		t.Fatal(err)
	}
	callbackExternal(t, b, s, "google", "")
	if _, err := h.BFF.RunDeletionsOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.BFFDB.OAuthBrowserState.Query().Where(oauthbrowserstate.ID(s.GetFlowId())).CountX(t.Context()) != 0 {
		t.Fatal("expired encrypted state survived cleanup")
	}
	// A Redis outage at Start fails closed before creating a provider challenge.
	h.bffRes.Redis.Close()
	req := connect.NewRequest(&bffv1.StartExternalLoginRequest{Provider: "google", Intent: "login", AppId: "ozid"})
	b.Write(t.Context(), req)
	if _, err := extClient(b).StartExternalLogin(t.Context(), req); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("Redis outage admitted start")
	}
	if b.SessionCookie() != original {
		t.Fatal("failure cleared the existing Cookie")
	}
}

func TestExternalBrowserNoEmailMerge(t *testing.T) {
	e := externalChain(t)
	h := e.h
	b := h.FreshBrowser()
	uid, err := b.Register(t.Context(), "occupied", "", "Existing owner")
	if err != nil {
		t.Fatal(err)
	}
	original := b.SessionCookie()
	e.google.next("independent-google", "occupied@example.com")
	s := startExternal(t, b, "google", "login")
	res := callbackExternal(t, b, s, "google", "")
	if len(res.Cookies()) != 0 || b.SessionCookie() != original {
		t.Fatal("same-email callback replaced account")
	}
	if _, err = finishExternal(t, b, s.GetFlowId()); connect.CodeOf(err) != connect.CodeFailedPrecondition || strings.Contains(err.Error(), "occupied") {
		t.Fatal("collision not neutral")
	}
	if h.UsersrvDB.User.Query().CountX(t.Context()) != 1 || h.UsersrvDB.ExternalIdentity.Query().CountX(t.Context()) != 0 {
		t.Fatal("same-email identity merged or duplicated")
	}
	if me, err := b.Me(t.Context()); err != nil || me.GetId() != uid {
		t.Fatal("owner session changed")
	}
}

func TestExternalBrowserClosedRegistrationStillAllowsLinkIntent(t *testing.T) {
	for _, provider := range []string{"google", "github"} {
		t.Run(provider, func(t *testing.T) {
			e := externalChain(t)
			h := e.h
			b := h.FreshBrowser()
			if _, err := b.Register(t.Context(), "link-owner", "", "Owner"); err != nil {
				t.Fatal(err)
			}
			old := b.SessionCookie()
			h.Accounts.SetRegistrationOpen(false)
			s := startExternal(t, b, provider, "link")
			res := callbackExternal(t, b, s, provider, "")
			p, err := getExternal(t, b, s.GetFlowId())
			if err != nil || p.GetStatus() != "link_pending" || b.SessionCookie() != old || len(res.Cookies()) != 0 {
				t.Fatalf("closed registration blocked link: %v", err)
			}
			anon := h.FreshBrowser()
			login := startExternal(t, anon, provider, "login")
			res = callbackExternal(t, anon, login, provider, "")
			if !strings.Contains(res.Header.Get("Location"), "REGISTRATION_UNAVAILABLE") {
				t.Fatal("closed registration admitted new identity")
			}
			if h.UsersrvDB.User.Query().CountX(t.Context()) != 1 || h.UsersrvDB.ExternalIdentity.Query().CountX(t.Context()) != 0 {
				t.Fatal("pending flow wrote identity/account")
			}
		})
	}
}

func TestExternalBrowserAdminStillNeedsRealPassword(t *testing.T) {
	e := externalChain(t)
	h := e.h
	b := h.FreshBrowser()
	s := startExternal(t, b, "google", "login")
	callbackExternal(t, b, s, "google", "")
	if _, err := finishExternal(t, b, s.GetFlowId()); err != nil {
		t.Fatal(err)
	}
	me, err := b.Me(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	uid := uuid.MustParse(me.GetId())
	for _, role := range []string{"ozid-admin", "metro-admin"} {
		h.UsersrvDB.UserRole.Create().SetUserID(uid).SetRole(role).SaveX(t.Context())
	}
	client := bffv1connect.NewUserAdminServiceClient(b.HTTP, b.APIURL)
	list := func() error {
		req := connect.NewRequest(&bffv1.ListUsersRequest{})
		req.Header().Set("Origin", h.bffCfg.Navigation.Apps["admin"].Origin)
		_, err := client.ListUsers(t.Context(), req)
		return err
	}
	if err = list(); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("external login satisfied admin password age: %v", err)
	}
	reauth := connect.NewRequest(&bffv1.ReauthenticateRequest{Password: E2EPassword})
	b.Write(t.Context(), reauth)
	if _, err = b.Auth.Reauthenticate(t.Context(), reauth); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("passwordless reauthentication bypassed")
	}
	if _, err = b.RequestPasswordReset(t.Context(), "candidate@gmail.com", ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	if err = b.ResetPassword(t.Context(), h.Inbox.TokenIn(t, "candidate@gmail.com"), E2EPassword); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Me(t.Context()); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("first password did not invalidate external session")
	}
	s = startExternal(t, b, "google", "login")
	callbackExternal(t, b, s, "google", "")
	if err = list(); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("provider login refreshed password time after first set")
	}
	reauth = connect.NewRequest(&bffv1.ReauthenticateRequest{Password: E2EPassword})
	b.Write(t.Context(), reauth)
	if _, err = b.Auth.Reauthenticate(t.Context(), reauth); err != nil {
		t.Fatal(err)
	}
	if err = list(); err != nil {
		t.Fatalf("real password cannot reach allowed admin perimeter: %v", err)
	}
}

func TestExternalBrowserAnonymousLogoutCancelsFlow(t *testing.T) {
	e := externalChain(t)
	b := e.h.FreshBrowser()
	if _, err := b.Register(t.Context(), "logout-browser", "", "Owner"); err != nil {
		t.Fatal(err)
	}
	if err := b.Logout(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := startExternal(t, b, "google", "login")
	if err := b.Logout(t.Context()); err != nil {
		t.Fatal(err)
	}
	res := callbackExternal(t, b, s, "google", "")
	if !strings.Contains(res.Header.Get("Location"), "STATE_INVALID") || e.google.count() != 0 || b.SessionCookie() != "" {
		t.Fatal("anonymous logout failed to cancel outstanding authorization")
	}
}
