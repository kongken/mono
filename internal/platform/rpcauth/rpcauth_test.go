package rpcauth_test

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/platform/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func newKeys(t *testing.T) (*servicetoken.Signer, *servicetoken.Verifier) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &servicetoken.Signer{Issuer: "bff", KID: "k1", Key: priv},
		&servicetoken.Verifier{Issuer: "bff", Audience: "svc", Keys: map[string]ed25519.PublicKey{"k1": pub}}
}

func TestRequiredRoleReadsTheProtoOption(t *testing.T) {
	testkit.RegisterRoleProbe()
	if got := rpcauth.RequiredRole(testkit.AdminProcedure); got != testkit.ProbeRole {
		t.Fatalf("admin method requires %q", got)
	}
	for _, m := range []string{testkit.OpenProcedure, "/testkit.v1.RoleProbe/Missing", "/nonsense", ""} {
		if got := rpcauth.RequiredRole(m); got != "" {
			t.Errorf("RequiredRole(%q) = %q", m, got)
		}
	}
	if rpcauth.Anonymous(testkit.AdminProcedure) {
		t.Fatal("a role requirement must not make a method anonymous")
	}
}

// serve runs the server interceptor for method with a credential signed by sign.
func serve(t *testing.T, v *servicetoken.Verifier, method, token string) (rpcauth.Call, error) {
	t.Helper()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(rpcauth.MetadataAuthorization, "Bearer "+token))
	var got rpcauth.Call
	_, err := rpcauth.UnaryServerInterceptor(v)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, _ any) (any, error) { got = rpcauth.CallFrom(ctx); return nil, nil })
	return got, err
}

func TestServerEnforcesRequiredRoleFromTheCredential(t *testing.T) {
	testkit.RegisterRoleProbe()
	signer, verifier := newKeys(t)
	sign := func(method string, roles ...string) string {
		tok, err := signer.SignRoles("svc", method, "user-1", roles)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	call, err := serve(t, verifier, testkit.AdminProcedure, sign(testkit.AdminProcedure, "other", testkit.ProbeRole))
	if err != nil || call.UserID != "user-1" || !rpcauth.HasRole(call.Roles, testkit.ProbeRole) {
		t.Fatalf("admin call: %+v %v", call, err)
	}
	// Only the subject, no rol claim: the service refuses on its own.
	if _, err = serve(t, verifier, testkit.AdminProcedure, sign(testkit.AdminProcedure)); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("missing rol: %v", err)
	}
	if _, err = serve(t, verifier, testkit.AdminProcedure, sign(testkit.AdminProcedure, "other")); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong role: %v", err)
	}
	// A method without the option never looks at roles.
	if _, err = serve(t, verifier, testkit.OpenProcedure, sign(testkit.OpenProcedure)); err != nil {
		t.Fatalf("open method: %v", err)
	}
	// An anonymous credential never reaches a protected method, role or not.
	anon, _ := signer.Sign("svc", testkit.AdminProcedure, "")
	if _, err = serve(t, verifier, testkit.AdminProcedure, anon); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous: %v", err)
	}
}

func TestClientSignsRolesOnlyForMethodsThatRequireOne(t *testing.T) {
	testkit.RegisterRoleProbe()
	signer, verifier := newKeys(t)
	ic := rpcauth.UnaryClientInterceptor(signer, "svc")
	ctx := rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: "user-1", Roles: []string{testkit.ProbeRole, "other"}})
	for method, wantRoles := range map[string]int{testkit.AdminProcedure: 2, testkit.OpenProcedure: 0} {
		var token string
		err := ic(ctx, method, nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			token = md.Get(rpcauth.MetadataAuthorization)[0][len("Bearer "):]
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		claims, err := verifier.Verify(token, method)
		if err != nil || len(claims.Roles) != wantRoles {
			t.Errorf("%s: roles %v, %v; want %d", method, claims.Roles, err, wantRoles)
		}
	}
}

func TestRolesRequireASubject(t *testing.T) {
	signer, _ := newKeys(t)
	signer.Now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	if _, err := signer.SignRoles("svc", "/m", "", []string{"x"}); err == nil {
		t.Fatal("roles without a subject were signed")
	}
}
