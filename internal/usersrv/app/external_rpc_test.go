package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/application"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/externalprovider"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

type externalTransport func(*http.Request) (*http.Response, error)

func (f externalTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExternalRegistrationOverSignedRPCWithLocalGitHubAndMail(t *testing.T) {
	s := startMail(t, generous())
	var mu sync.Mutex
	used := map[string]bool{}
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if r.Method != "POST" || r.ParseForm() != nil || r.PostForm.Get("client_id") != "client" || r.PostForm.Get("client_secret") != "secret" || r.PostForm.Get("redirect_uri") != "https://api.example.com/api/identity/callback/github" || r.PostForm.Get("code_verifier") != verifier {
				t.Error("unbound token exchange")
				w.WriteHeader(400)
				return
			}
			mu.Lock()
			already := used[r.PostForm.Get("code")]
			used[r.PostForm.Get("code")] = true
			mu.Unlock()
			if already {
				fmt.Fprint(w, `{"error":"bad_verification_code"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"local-private-bearer","token_type":"bearer","scope":"user:email"}`)
		case "/user":
			if r.Header.Get("Authorization") != "Bearer local-private-bearer" {
				t.Error("missing bearer")
			}
			fmt.Fprint(w, `{"id":4242,"type":"User","login":"untrusted-login","name":"Provider name","email":"untrusted-public@example.com"}`)
		case "/user/emails":
			fmt.Fprint(w, `[{"email":"private-secondary@example.com","verified":true,"primary":false},{"email":"candidate@example.com","verified":true,"primary":true}]`)
		default:
			t.Error("unregistered provider endpoint")
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	rt := externalTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "github.com" && r.URL.Host != "api.github.com" {
			t.Error("untrusted endpoint")
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		copy.URL = &u
		return server.Client().Transport.RoundTrip(copy)
	})
	gh, err := externalprovider.NewGitHub(externalprovider.GitHubConfig{Enabled: true, ClientID: "client", ClientSecret: "secret", RedirectURI: "https://api.example.com/api/identity/callback/github", ConfigVersion: 1}, rt)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := s.res.Mail.ProofCipher("k1")
	if err != nil {
		t.Fatal(err)
	}
	s.accounts.ConfigureExternal(account.ExternalSettings{Providers: map[externalprovider.ProviderName]externalprovider.ExternalProvider{externalprovider.GitHubName: gh}, Policies: map[externalprovider.ProviderName]account.ProviderPolicy{externalprovider.GitHubName: {Enabled: true, ConfigVersion: 1, RedirectURI: "https://api.example.com/api/identity/callback/github"}}, Codec: codec})
	client := usersrvv1.NewExternalIdentityServiceClient(s.conn)
	signed := anonCtx("203.0.113.41")
	bind := &usersrvv1.ExternalFlowBinding{FlowId: uuid.NewString(), ContextHash: sum[:]}
	begin, err := client.BeginExternalLogin(signed, &usersrvv1.BeginExternalLoginRequest{Provider: usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GITHUB, Intent: "login", Binding: bind, RedirectUri: "https://api.example.com/api/identity/callback/github", State: strings.Repeat("s", 43), CodeChallenge: challenge})
	if err != nil {
		t.Fatal(err)
	}
	ref := &usersrvv1.ExternalChallengeReference{ChallengeId: begin.GetChallengeId(), Binding: bind}
	out, err := client.CompleteExternalLogin(signed, &usersrvv1.CompleteExternalLoginRequest{Challenge: ref, Provider: usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GITHUB, ProviderConfigVersion: 1, Code: "first-code", Verifier: verifier, CodeChallenge: challenge})
	if err != nil {
		t.Fatal(err)
	}
	if out.GetAuthenticated() != nil || out.GetPending().GetCandidateEmail() != "candidate@example.com" || out.GetPending().GetEmailConfirmed() {
		t.Fatal("pending proof granted session/Google email trust")
	}
	if text := protojson.Format(out); strings.Contains(text, "private-secondary") || strings.Contains(text, "local-private-bearer") || strings.Contains(text, "4242") {
		t.Fatal("RPC leaked provider secret or internal subject")
	}
	if _, err = client.RequestExternalRegistrationEmail(signed, &usersrvv1.RequestExternalRegistrationEmailRequest{Challenge: ref, Email: "candidate@example.com"}); err != nil {
		t.Fatal(err)
	}
	s.deliver(t)
	token := tokenIn(t, s.inbox.to("candidate@example.com")[0], "/account/confirm-external-email")
	if _, err = client.ConfirmExternalRegistrationEmail(anonCtx("203.0.113.42"), &usersrvv1.ConfirmExternalRegistrationEmailRequest{ChallengeId: ref.GetChallengeId(), Token: token}); err != nil {
		t.Fatal(err)
	}
	if s.db.User.Query().CountX(context.Background()) != 0 {
		t.Fatal("other-device email confirmation created account")
	}
	if _, err = client.GetExternalRegistration(signed, &usersrvv1.GetExternalRegistrationRequest{Challenge: ref}); err != nil {
		t.Fatal(err)
	}
	wrong := &usersrvv1.ExternalChallengeReference{ChallengeId: ref.GetChallengeId(), Binding: &usersrvv1.ExternalFlowBinding{FlowId: bind.GetFlowId(), ContextHash: []byte("another-browser")}}
	if _, err = client.CompleteExternalRegistration(signed, &usersrvv1.CompleteExternalRegistrationRequest{Challenge: wrong, MutationId: "final"}); reason(t, err) != application.ReasonChallengeInvalid {
		t.Fatal("cross-browser final submission passed")
	}
	created, err := client.CompleteExternalRegistration(signed, &usersrvv1.CompleteExternalRegistrationRequest{Challenge: ref, MutationId: "final", DisplayName: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	uid := uuid.MustParse(created.GetCreatedUserId())
	list, err := client.GetExternalIdentities(as(uid, ""), &usersrvv1.GetExternalIdentitiesRequest{})
	if err != nil || len(list.GetIdentities()) != 1 {
		t.Fatal("owner identity list failed")
	}
	if _, err = client.GetExternalIdentities(signed, &usersrvv1.GetExternalIdentitiesRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatal("anonymous caller read identities")
	}
	if _, err = client.BeginIdentityOperation(signed, &usersrvv1.BeginIdentityOperationRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatal("anonymous caller began account operation")
	}
	if s.db.AuthChallenge.Query().Where(authchallenge.StatusEQ(authchallenge.StatusCompleted)).CountX(context.Background()) != 1 {
		t.Fatal("registration not consumed")
	}
	// Every new RPC still passes through the real method-bound credential gate.
	conn, err := grpc.NewClient("passthrough:///"+s.h.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	raw := usersrvv1.NewExternalIdentityServiceClient(conn)
	if _, err = raw.BeginExternalLogin(context.Background(), &usersrvv1.BeginExternalLoginRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatal("unsigned external RPC accepted")
	}
	tokenForOtherMethod, _ := s.signer.Sign("usersrv", "/usersrv.v1.AccountService/GetAuthState", uid.String())
	wrongMethod := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(rpcauth.MetadataAuthorization, "Bearer "+tokenForOtherMethod))
	if _, err = raw.GetExternalIdentities(wrongMethod, &usersrvv1.GetExternalIdentitiesRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatal("cross-method credential accepted")
	}
}

func TestExternalConfirmationRateLimitsAndRedisFailClosed(t *testing.T) {
	s := startMail(t, limits())
	client := usersrvv1.NewExternalIdentityServiceClient(s.conn)
	signed := anonCtx("203.0.113.91")
	request := &usersrvv1.ConfirmIdentityOperationRequest{ChallengeId: uuid.NewString(), Token: strings.Repeat("t", 43)}
	for n := 0; n < 5; n++ {
		_, err := client.ConfirmIdentityOperation(signed, request)
		if reason(t, err) != application.ReasonChallengeInvalid {
			t.Fatal(err)
		}
	}
	if _, err := client.ConfirmIdentityOperation(signed, request); reason(t, err) != application.ReasonRateLimited {
		t.Fatal("token guesses escaped limiter")
	}
	if err := s.rdb.Close(); err != nil {
		t.Fatal(err)
	}
	request.Token = strings.Repeat("x", 43)
	if _, err := client.ConfirmIdentityOperation(signed, request); status.Code(err) != codes.Unavailable {
		t.Fatal("Redis failure allowed operation confirmation")
	}
}
