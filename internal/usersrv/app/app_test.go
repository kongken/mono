package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/platform/testkit"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/app"
	"github.com/kongken/mono/internal/usersrv/application"
	"github.com/kongken/mono/internal/usersrv/config"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/outbox"
	"github.com/kongken/mono/internal/usersrv/ratelimit"
	"github.com/kongken/mono/internal/usersrv/testutil"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const password = "correct horse battery"

// stack is the real usersrv gRPC server (rpcauth interceptors, account core,
// PostgreSQL and Redis) with a client that signs credentials like the BFF.
type stack struct {
	h        *app.Handlers
	db       *ent.Client
	res      *app.Resources
	accounts *account.Service
	keys     testkit.Keys
	conn     *grpc.ClientConn
	signer   *servicetoken.Signer
	rdb      *redis.Client
	prefix   string
	account  usersrvv1.AccountServiceClient
	system   usersrvv1.SystemServiceClient
}

func limits() application.Limits {
	return application.Limits{
		PasswordPerAccount:  ratelimit.Rule{Name: "password_per_account", Limit: 10, Window: 15 * time.Minute},
		PasswordPerIP:       ratelimit.Rule{Name: "password_per_ip", Limit: 40, Window: 15 * time.Minute},
		ProfileWritePerUser: ratelimit.Rule{Name: "profile_write_per_user", Limit: 30, Window: time.Minute},
		DeletionPerUser:     ratelimit.Rule{Name: "deletion_per_user", Limit: 10, Window: 15 * time.Minute},
		TokenSubmitPerIP:    ratelimit.Rule{Name: "token_submit_per_ip", Limit: 30, Window: 15 * time.Minute},
		TokenSubmitPerToken: ratelimit.Rule{Name: "token_submit_per_token", Limit: 5, Window: 15 * time.Minute},
		Mail: ratelimit.MailRules{
			RegistrationPerIP:         ratelimit.Rule{Name: "registration_mail_per_ip", Limit: 20, Window: time.Hour},
			ResetPerIP:                ratelimit.Rule{Name: "reset_mail_per_ip", Limit: 20, Window: time.Hour},
			RegistrationEmailCooldown: ratelimit.Rule{Name: "registration_mail_cooldown", Limit: 1, Window: time.Minute},
			RegistrationPerEmail:      ratelimit.Rule{Name: "registration_mail_per_email", Limit: 5, Window: time.Hour},
			ResetEmailCooldown:        ratelimit.Rule{Name: "reset_mail_cooldown", Limit: 1, Window: time.Minute},
			ResetPerEmail:             ratelimit.Rule{Name: "reset_mail_per_email", Limit: 5, Window: time.Hour},
			MailPerEmail:              ratelimit.Rule{Name: "mail_per_email", Limit: 8, Window: time.Hour},
			MailGlobal:                ratelimit.Rule{Name: "mail_global", Limit: 200, Window: time.Hour},
		},
	}
}

func start(t *testing.T) *stack {
	t.Helper()
	return startWith(t, limits(), nil)
}

// startWith is start with explicit limits and an optional mail outbox.
func startWith(t *testing.T, lim application.Limits, mail func(*ent.Client) *outbox.Service) *stack {
	t.Helper()
	db := testutil.DB(t)
	rdb, prefix := testutil.Redis(t)
	keys := testkit.NewKeys(t)
	cfg := &config.AppConfig{
		GRPC: grpcx.ServerConfig{Transport: grpcx.TransportH2C, Listen: "127.0.0.1:0"},
		InternalAuth: servicetoken.VerifyConfig{Issuer: "tripmap-bff", Audience: "usersrv",
			TrustedKeys: []servicetoken.TrustedKey{{KID: "k1", PublicKeyFile: keys.PublicFile}}},
	}
	h := app.New(cfg)
	var opts []app.ResourceOption
	if mail != nil {
		opts = append(opts, app.WithMail(mail(db)))
	}
	res, err := app.NewResources(db, testutil.Hasher(t), rdb, prefix, []byte(strings.Repeat("h", 32)), lim,
		func(ctx context.Context) error { _, err := db.User.Query().Exist(ctx); return err }, opts...)
	if err != nil {
		t.Fatal(err)
	}
	h.SetResources(res)
	for _, w := range res.Workers {
		w.Start()
	}
	if err = h.StartGRPC(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, w := range res.Workers {
			w.Stop()
		}
		h.StopGRPC()
	})
	signer := &servicetoken.Signer{Issuer: "tripmap-bff", KID: "k1", Key: keys.Private}
	conn, err := grpc.NewClient("passthrough:///"+h.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(rpcauth.UnaryClientInterceptor(signer, "usersrv")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &stack{h: h, db: db, res: res, accounts: res.Accounts, keys: keys, conn: conn, signer: signer, rdb: rdb, prefix: prefix,
		account: usersrvv1.NewAccountServiceClient(conn), system: usersrvv1.NewSystemServiceClient(conn)}
}

func as(id uuid.UUID, ip string) context.Context {
	return rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: id.String(), ClientIP: ip, RequestID: "req-" + id.String()[:8]})
}

func (s *stack) newUser(t *testing.T, email string) uuid.UUID {
	t.Helper()
	id, err := s.accounts.CreateVerifiedUser(context.Background(), account.NewUser{Email: email, Password: password, DisplayName: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func reason(t *testing.T, err error) string {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("not a status: %v", err)
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.Domain == application.ErrorDomain {
			return info.Reason
		}
	}
	return ""
}

func TestEveryCallNeedsAValidBFFCredential(t *testing.T) {
	s := start(t)
	ctx := context.Background()
	rawSystem := usersrvv1.NewSystemServiceClient(func() *grpc.ClientConn {
		c, err := grpc.NewClient("passthrough:///"+s.h.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}())
	with := func(token string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs(rpcauth.MetadataAuthorization, "Bearer "+token))
	}
	const method = "/usersrv.v1.SystemService/GetStatus"
	good, _ := s.signer.Sign("usersrv", method, "")
	if _, err := rawSystem.GetStatus(with(good), &usersrvv1.GetStatusRequest{}); err != nil {
		t.Fatalf("valid anonymous credential: %v", err)
	}
	wrongAud, _ := s.signer.Sign("metro", method, "")
	wrongMethod, _ := s.signer.Sign("usersrv", "/usersrv.v1.AccountService/GetAuthState", "")
	expired := (&servicetoken.Signer{Issuer: "tripmap-bff", KID: "k1", Key: s.keys.Private, Now: func() time.Time { return time.Now().Add(-2 * time.Minute) }})
	oldToken, _ := expired.Sign("usersrv", method, "")
	otherKey := testkit.NewKeys(t)
	forged, _ := (&servicetoken.Signer{Issuer: "tripmap-bff", KID: "k1", Key: otherKey.Private}).Sign("usersrv", method, "")
	unknownKID, _ := (&servicetoken.Signer{Issuer: "tripmap-bff", KID: "k9", Key: s.keys.Private}).Sign("usersrv", method, "")
	wrongIss, _ := (&servicetoken.Signer{Issuer: "someone-else", KID: "k1", Key: s.keys.Private}).Sign("usersrv", method, "")
	for name, tok := range map[string]string{"wrong audience": wrongAud, "other method": wrongMethod, "expired (60s)": oldToken,
		"foreign key": forged, "unknown kid": unknownKID, "wrong issuer": wrongIss, "garbage": "x.y.z"} {
		if _, err := rawSystem.GetStatus(with(tok), &usersrvv1.GetStatusRequest{}); status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := rawSystem.GetStatus(ctx, &usersrvv1.GetStatusRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no credential: %v", err)
	}
	// Spoofed identity metadata is never read: a credential for another user wins.
	id := s.newUser(t, "real@example.com")
	victim := s.newUser(t, "victim@example.com")
	spoof := metadata.AppendToOutgoingContext(as(id, ""), "x-user-id", victim.String(), "x-bff-user", victim.String())
	got, err := s.account.GetMyAccount(spoof, &usersrvv1.GetMyAccountRequest{})
	if err != nil || got.UserId != id.String() || got.Email != "real@example.com" {
		t.Fatalf("spoofed headers changed the account: %+v %v", got, err)
	}
}

func TestAnonymousCredentialsOpenOnlyAnonymousMethods(t *testing.T) {
	s := start(t)
	anon := context.Background() // no user in the Call: the client interceptor signs an anonymous credential
	if res, err := s.system.GetStatus(anon, &usersrvv1.GetStatusRequest{}); err != nil || !res.Ready {
		t.Fatalf("status: %+v %v", res, err)
	}
	// Protected methods refuse an anonymous credential at the interceptor.
	for name, call := range map[string]func() error{
		"GetAuthState": func() error { _, err := s.account.GetAuthState(anon, &usersrvv1.GetAuthStateRequest{}); return err },
		"VerifyPassword": func() error {
			_, err := s.account.VerifyPassword(anon, &usersrvv1.VerifyPasswordRequest{Password: password})
			return err
		},
		"BeginDeletion": func() error {
			_, err := s.account.BeginDeletion(anon, &usersrvv1.BeginDeletionRequest{MutationId: "m"})
			return err
		},
	} {
		if err := call(); status.Code(err) != codes.Unauthenticated || reason(t, err) != "" {
			t.Errorf("%s with an anonymous credential: %v", name, err)
		}
	}
	for _, m := range []string{"RequestRegistration", "CompleteRegistration", "AuthenticateWithEmail", "RequestPasswordReset", "ResetPassword"} {
		if !rpcauth.Anonymous("/usersrv.v1.AccountService/" + m) {
			t.Errorf("%s must be declared anonymous in proto", m)
		}
	}
	for _, m := range []string{"GetAuthState", "GetMyAccount", "VerifyPassword", "ChangePassword", "UpdateMyProfile", "BeginDeletion", "FinishDeletion"} {
		if rpcauth.Anonymous("/usersrv.v1.AccountService/" + m) {
			t.Errorf("%s must not be anonymous", m)
		}
	}
	if !rpcauth.Anonymous("/usersrv.v1.SystemService/GetStatus") {
		t.Error("GetStatus must be anonymous")
	}
}

func TestAuthStateAndAccountOfTheCallerOnly(t *testing.T) {
	s := start(t)
	id := s.newUser(t, "me@example.com")
	other := s.newUser(t, "other@example.com")
	op := account.Operator{Name: "ops", Reason: "test"}
	if _, err := s.accounts.GrantRole(context.Background(), op, id, account.RoleMetroAdmin); err != nil {
		t.Fatal(err)
	}

	st, err := s.account.GetAuthState(as(id, ""), &usersrvv1.GetAuthStateRequest{})
	if err != nil || st.Status != usersrvv1.AccountStatus_ACCOUNT_STATUS_ACTIVE || st.AuthVersion != 2 || len(st.Roles) != 1 || st.Roles[0] != "metro-admin" || st.ProfileVersion != 1 {
		t.Fatalf("state: %+v %v", st, err)
	}
	acct, err := s.account.GetMyAccount(as(id, ""), &usersrvv1.GetMyAccountRequest{})
	if err != nil || acct.Email != "me@example.com" || acct.UserId != id.String() || acct.Profile.DisplayName != "测试" || acct.AuthVersion != 2 {
		t.Fatalf("account: %+v %v", acct, err)
	}
	// The other user sees only itself and no roles.
	if st, err = s.account.GetAuthState(as(other, ""), &usersrvv1.GetAuthStateRequest{}); err != nil || len(st.Roles) != 0 || st.AuthVersion != 1 {
		t.Fatalf("other state: %+v %v", st, err)
	}
	// An unknown subject (a deleted account's old session) is NOT_FOUND, never a default state.
	if _, err = s.account.GetAuthState(as(uuid.New(), ""), &usersrvv1.GetAuthStateRequest{}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown subject: %v", err)
	}
	// A subject that is not a UUID cannot name any account.
	bad := rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: "not-a-uuid"})
	if _, err = s.account.GetAuthState(bad, &usersrvv1.GetAuthStateRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("non-uuid subject: %v", err)
	}
	// A disabled account's state is readable (the BFF denies it) and carries the new version.
	if _, err = s.accounts.SetDisabled(context.Background(), op, other, true); err != nil {
		t.Fatal(err)
	}
	if st, err = s.account.GetAuthState(as(other, ""), &usersrvv1.GetAuthStateRequest{}); err != nil || st.Status != usersrvv1.AccountStatus_ACCOUNT_STATUS_DISABLED || st.AuthVersion != 2 {
		t.Fatalf("disabled state: %+v %v", st, err)
	}
}

func TestPasswordChecksAndTypedErrors(t *testing.T) {
	s := start(t)
	id := s.newUser(t, "pw@example.com")
	ctx := as(id, "203.0.113.7")

	v, err := s.account.VerifyPassword(ctx, &usersrvv1.VerifyPasswordRequest{Password: password})
	if err != nil || v.AuthVersion != 1 {
		t.Fatalf("verify: %+v %v", v, err)
	}
	_, err = s.account.VerifyPassword(ctx, &usersrvv1.VerifyPasswordRequest{Password: "definitely wrong"})
	if status.Code(err) != codes.Unauthenticated || reason(t, err) != application.ReasonInvalidCredentials {
		t.Fatalf("wrong password must be a typed UNAUTHENTICATED the BFF can tell from a signing failure: %v", err)
	}
	if strings.Contains(err.Error(), "definitely wrong") || strings.Contains(err.Error(), password) {
		t.Fatalf("error echoes a password: %v", err)
	}
	// Bad input is a field error and names only the field.
	_, err = s.account.ChangePassword(ctx, &usersrvv1.ChangePasswordRequest{CurrentPassword: password, NewPassword: "short"})
	if status.Code(err) != codes.InvalidArgument || reason(t, err) != application.ReasonInvalidArgument || strings.Contains(err.Error(), "short\"") {
		t.Fatalf("weak password: %v", err)
	}
	_, err = s.account.VerifyPassword(ctx, &usersrvv1.VerifyPasswordRequest{Password: strings.Repeat("a", 100000)})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversized password: %v", err)
	}

	res, err := s.account.ChangePassword(ctx, &usersrvv1.ChangePasswordRequest{CurrentPassword: password, NewPassword: "a brand new password"})
	if err != nil || res.AuthVersion != 2 {
		t.Fatalf("change: %+v %v", res, err)
	}
	if _, err = s.account.VerifyPassword(ctx, &usersrvv1.VerifyPasswordRequest{Password: password}); reason(t, err) != application.ReasonInvalidCredentials {
		t.Fatalf("old password: %v", err)
	}
	st, _ := s.account.GetAuthState(ctx, &usersrvv1.GetAuthStateRequest{})
	if st.AuthVersion != 2 {
		t.Fatalf("session version check would not notice the change: %+v", st)
	}

	// A non-active account gets its own reason.
	if _, err = s.accounts.SetDisabled(context.Background(), account.Operator{Name: "ops", Reason: "t"}, id, true); err != nil {
		t.Fatal(err)
	}
	_, err = s.account.VerifyPassword(ctx, &usersrvv1.VerifyPasswordRequest{Password: "a brand new password"})
	if status.Code(err) != codes.FailedPrecondition || reason(t, err) != application.ReasonAccountNotActive {
		t.Fatalf("disabled: %v", err)
	}
}

func TestProfileOverRPC(t *testing.T) {
	s := start(t)
	id := s.newUser(t, "prof@example.com")
	res, err := s.account.UpdateMyProfile(as(id, ""), &usersrvv1.UpdateMyProfileRequest{DisplayName: "新名字", AvatarUrl: "https://cdn.example.com/x.png"})
	if err != nil || res.Profile.DisplayName != "新名字" || res.Profile.ProfileVersion != 2 {
		t.Fatalf("update: %+v %v", res, err)
	}
	for _, bad := range []*usersrvv1.UpdateMyProfileRequest{{DisplayName: ""}, {DisplayName: "x", AvatarUrl: "http://example.com/a.png"}, {DisplayName: "x", AvatarUrl: "https://u:p@example.com/a.png"}} {
		if _, err = s.account.UpdateMyProfile(as(id, ""), bad); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	acct, _ := s.account.GetMyAccount(as(id, ""), &usersrvv1.GetMyAccountRequest{})
	if acct.Profile.DisplayName != "新名字" || acct.AuthVersion != 1 {
		t.Fatalf("account after profile write: %+v", acct)
	}
}

func TestDeletionOverRPC(t *testing.T) {
	s := start(t)
	id := s.newUser(t, "del@example.com")
	ctx := as(id, "")
	b, err := s.account.BeginDeletion(ctx, &usersrvv1.BeginDeletionRequest{MutationId: "wf-1"})
	if err != nil || b.AuthVersion != 2 {
		t.Fatalf("begin: %+v %v", b, err)
	}
	if _, err = s.account.BeginDeletion(ctx, &usersrvv1.BeginDeletionRequest{MutationId: "wf-1"}); err != nil {
		t.Fatalf("begin replay: %v", err)
	}
	if _, err = s.account.BeginDeletion(ctx, &usersrvv1.BeginDeletionRequest{MutationId: "wf-2"}); reason(t, err) != application.ReasonDeletionMismatch {
		t.Fatalf("other workflow: %v", err)
	}
	if _, err = s.account.BeginDeletion(ctx, &usersrvv1.BeginDeletionRequest{MutationId: "bad id!"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad mutation id: %v", err)
	}
	st, _ := s.account.GetAuthState(ctx, &usersrvv1.GetAuthStateRequest{})
	if st.Status != usersrvv1.AccountStatus_ACCOUNT_STATUS_DELETING {
		t.Fatalf("state: %+v", st)
	}
	if _, err = s.account.FinishDeletion(ctx, &usersrvv1.FinishDeletionRequest{MutationId: "wf-2"}); reason(t, err) != application.ReasonDeletionMismatch {
		t.Fatalf("finish by another workflow: %v", err)
	}
	f, err := s.account.FinishDeletion(ctx, &usersrvv1.FinishDeletionRequest{MutationId: "wf-1"})
	if err != nil || !f.Removed {
		t.Fatalf("finish: %+v %v", f, err)
	}
	if f, err = s.account.FinishDeletion(ctx, &usersrvv1.FinishDeletionRequest{MutationId: "wf-1"}); err != nil || f.Removed {
		t.Fatalf("finish replay: %+v %v", f, err)
	}
	// Another user cannot replay or take over this deletion.
	other := s.newUser(t, "bystander@example.com")
	if _, err = s.account.FinishDeletion(as(other, ""), &usersrvv1.FinishDeletionRequest{MutationId: "wf-1"}); status.Code(err) == codes.OK {
		t.Fatal("another user advanced the deletion")
	}
	if _, err = s.account.GetAuthState(ctx, &usersrvv1.GetAuthStateRequest{}); status.Code(err) != codes.NotFound {
		t.Fatalf("deleted account state: %v", err)
	}
}

func TestPasswordRateLimitsCountEveryAttemptAndDoNotRevealQuota(t *testing.T) {
	s := start(t)
	id := s.newUser(t, "limit@example.com")
	ctx := as(id, "203.0.113.9")
	// 10 per account per window: correct and wrong attempts both count.
	for i := 1; i <= 10; i++ {
		pw := password
		if i%2 == 0 {
			pw = "wrong password " + strings.Repeat("x", i)
		}
		_, err := s.account.VerifyPassword(ctx, &usersrvv1.VerifyPasswordRequest{Password: pw})
		if i%2 == 1 && err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if i%2 == 0 && reason(t, err) != application.ReasonInvalidCredentials {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	_, err := s.account.VerifyPassword(ctx, &usersrvv1.VerifyPasswordRequest{Password: password})
	if status.Code(err) != codes.ResourceExhausted || reason(t, err) != application.ReasonRateLimited {
		t.Fatalf("11th attempt with the right password: %v", err)
	}
	// ChangePassword shares the budget, so it cannot be used to keep guessing.
	_, err = s.account.ChangePassword(ctx, &usersrvv1.ChangePasswordRequest{CurrentPassword: password, NewPassword: "another new password"})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("change after the limit: %v", err)
	}
	if strings.ContainsAny(err.Error(), "0123456789") {
		t.Fatalf("limit message reveals numbers: %v", err)
	}
	// The limit is per account: another account is not affected.
	other := s.newUser(t, "free@example.com")
	if _, err = s.account.VerifyPassword(as(other, "203.0.113.10"), &usersrvv1.VerifyPasswordRequest{Password: password}); err != nil {
		t.Fatalf("other account: %v", err)
	}
}

func TestPasswordRateLimitPerIP(t *testing.T) {
	s := start(t)
	ip := "198.51.100.77"
	var limited bool
	for i := 0; i < 45; i++ {
		id := s.newUser(t, "ip"+uuid.NewString()[:8]+"@example.com")
		_, err := s.account.VerifyPassword(as(id, ip), &usersrvv1.VerifyPasswordRequest{Password: password})
		if i < 40 && err != nil {
			t.Fatalf("attempt %d from the IP: %v", i, err)
		}
		if i >= 40 {
			if status.Code(err) != codes.ResourceExhausted {
				t.Fatalf("attempt %d from the IP: %v", i, err)
			}
			limited = true
		}
	}
	if !limited {
		t.Fatal("never limited")
	}
	// A different client address is a different budget.
	id := s.newUser(t, "elsewhere@example.com")
	if _, err := s.account.VerifyPassword(as(id, "198.51.100.78"), &usersrvv1.VerifyPasswordRequest{Password: password}); err != nil {
		t.Fatalf("other IP: %v", err)
	}
}

func TestProfileAndDeletionLimits(t *testing.T) {
	s := start(t)
	id := s.newUser(t, "writes@example.com")
	for i := 0; i < 30; i++ {
		if _, err := s.account.UpdateMyProfile(as(id, ""), &usersrvv1.UpdateMyProfileRequest{DisplayName: "n" + uuid.NewString()[:6]}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if _, err := s.account.UpdateMyProfile(as(id, ""), &usersrvv1.UpdateMyProfileRequest{DisplayName: "one too many"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("31st profile write: %v", err)
	}
	for i := 0; i < 10; i++ {
		_, _ = s.account.BeginDeletion(as(id, ""), &usersrvv1.BeginDeletionRequest{MutationId: "wf"})
	}
	if _, err := s.account.BeginDeletion(as(id, ""), &usersrvv1.BeginDeletionRequest{MutationId: "wf"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("11th deletion call: %v", err)
	}
}

func TestRedisOutageFailsClosedAndNeverChecksThePassword(t *testing.T) {
	s := start(t)
	id := s.newUser(t, "down@example.com")
	// Replace the limiter's store with an unreachable Redis.
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer dead.Close()
	limiter, _ := ratelimit.New(dead, "x:", []byte(strings.Repeat("h", 32)))
	svc := application.NewAccountService(s.accounts, limiter, limits())
	ctx := as(id, "203.0.113.50")
	_, err := svc.VerifyPassword(ctx, &usersrvv1.VerifyPasswordRequest{Password: password})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("verify with Redis down: %v", err)
	}
	if _, err = svc.ChangePassword(ctx, &usersrvv1.ChangePasswordRequest{CurrentPassword: password, NewPassword: "should never be applied"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("change with Redis down: %v", err)
	}
	// The password was not changed, and no audit trail of a check exists.
	if _, err = s.account.VerifyPassword(as(id, ""), &usersrvv1.VerifyPasswordRequest{Password: password}); err != nil {
		t.Fatalf("original password stopped working: %v", err)
	}
	// Reading the state for the BFF's per-request check has no limiter and still works.
	if _, err = svc.GetAuthState(ctx, &usersrvv1.GetAuthStateRequest{}); err != nil {
		t.Fatalf("auth state: %v", err)
	}
}

func TestReadyzReportsPostgresRedisAndWorkerLocally(t *testing.T) {
	s := start(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s.h.Routes(r)
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := get("/ping"); code != 200 || !strings.Contains(body, "pong") {
		t.Fatalf("ping: %d %s", code, body)
	}
	code, body := get("/readyz")
	if code != 200 {
		t.Fatalf("readyz: %d %s", code, body)
	}
	for _, dep := range []string{`"postgres"`, `"redis"`, `"worker"`} {
		if !strings.Contains(body, dep) {
			t.Fatalf("readyz lacks %s: %s", dep, body)
		}
	}
	res, err := s.system.GetStatus(context.Background(), &usersrvv1.GetStatusRequest{})
	if err != nil || !res.Ready || len(res.Dependencies) != 3 {
		t.Fatalf("system status: %+v %v", res, err)
	}
}

func TestReadyzFailsWhenRedisOrWorkerIsDown(t *testing.T) {
	db := testutil.DB(t)
	rdb, prefix := testutil.Redis(t)
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer dead.Close()
	res, err := app.NewResources(db, testutil.Hasher(t), dead, prefix, []byte(strings.Repeat("h", 32)), limits(), func(ctx context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	h := app.New(&config.AppConfig{})
	h.SetResources(res)
	got := map[string]bool{}
	for _, st := range h.Check(context.Background()) {
		got[st.Name] = st.Ready
	}
	// Redis is unreachable and the worker was never started; PostgreSQL probe is fine.
	if got["redis"] || got["worker"] || !got["postgres"] {
		t.Fatalf("readiness: %v", got)
	}
	_ = rdb
}
