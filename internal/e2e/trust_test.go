package e2e_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/platform/testkit"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	statusMethod    = "/tripmap.v1.SystemService/GetStatus"
	protectedMethod = "/tripmap.v1.FootprintService/GetFootprints"
)

// protectedProbe stands in for a protected business method so the trust boundary
// can be tested before the real ones migrate. It records who the handler saw.
type protectedProbe struct {
	tripmapv1.UnimplementedFootprintServiceServer
	mu   sync.Mutex
	seen []rpcauth.Call
}

func (p *protectedProbe) GetFootprints(ctx context.Context, _ *tripmapv1.GetFootprintsRequest) (*tripmapv1.GetFootprintsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, rpcauth.CallFrom(ctx))
	return &tripmapv1.GetFootprintsResponse{}, nil
}

func (p *protectedProbe) calls() []rpcauth.Call {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]rpcauth.Call(nil), p.seen...)
}

// direct dials tripmap's TLS gRPC port without the BFF.
func (s *stack) direct(caFile, serverName string) *grpc.ClientConn {
	s.t.Helper()
	pem, err := os.ReadFile(caFile)
	if err != nil {
		s.t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	conn, err := grpc.NewClient(s.tripmap.GRPCAddr(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: pool, ServerName: serverName, MinVersion: tls.VersionTLS13})))
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// invoke calls a method with a raw authorization value and extra metadata.
func invoke(conn *grpc.ClientConn, method, authorization string, extra ...string) error {
	md := metadata.Pairs(extra...)
	if authorization != "" {
		md.Set("authorization", authorization)
	}
	ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), md), 5*time.Second)
	defer cancel()
	switch method {
	case statusMethod:
		return conn.Invoke(ctx, method, &tripmapv1.GetStatusRequest{}, &tripmapv1.GetStatusResponse{})
	default:
		return conn.Invoke(ctx, method, &tripmapv1.GetFootprintsRequest{}, &tripmapv1.GetFootprintsResponse{})
	}
}

func codeIs(t *testing.T, name string, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("%s: code %v (%v), want %v", name, got, err, want)
	}
}

func withProbe(p *protectedProbe) func(*options, *stack) {
	return func(o *options, _ *stack) {
		o.extra = append(o.extra, func(r grpc.ServiceRegistrar) { tripmapv1.RegisterFootprintServiceServer(r, p) })
	}
}

func TestTripmapRejectsEveryCallWithoutAValidBFFCredential(t *testing.T) {
	probe := &protectedProbe{}
	s := newStack(t, withProbe(probe))
	conn := s.direct(s.ca.CAFile, "tripmap.internal")
	now := time.Now()
	signer := func(mutate func(*servicetoken.Signer)) *servicetoken.Signer {
		sg := &servicetoken.Signer{Issuer: issuer, KID: "k1", Key: s.keys.Private, Now: func() time.Time { return now }}
		if mutate != nil {
			mutate(sg)
		}
		return sg
	}
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	sign := func(sg *servicetoken.Signer, aud, method, sub string) string {
		tok, err := sg.Sign(aud, method, sub)
		if err != nil {
			t.Fatal(err)
		}
		return "Bearer " + tok
	}

	if err := invoke(conn, statusMethod, sign(signer(nil), audience, statusMethod, "")); err != nil {
		t.Fatalf("valid anonymous credential refused: %v", err)
	}
	for name, authorization := range map[string]string{
		"no credential":            "",
		"not a bearer value":       "Basic abc",
		"garbage":                  "Bearer not-a-token",
		"bad signature":            sign(signer(func(sg *servicetoken.Signer) { sg.Key = otherKey }), audience, statusMethod, ""),
		"unknown kid":              sign(signer(func(sg *servicetoken.Signer) { sg.KID = "k-unknown" }), audience, statusMethod, ""),
		"wrong issuer":             sign(signer(func(sg *servicetoken.Signer) { sg.Issuer = "someone-else" }), audience, statusMethod, ""),
		"wrong audience":           sign(signer(nil), "other-service", statusMethod, ""),
		"token for another method": sign(signer(nil), audience, protectedMethod, "user-1"),
		"expired":                  sign(signer(func(sg *servicetoken.Signer) { sg.Now = func() time.Time { return now.Add(-3 * time.Minute) } }), audience, statusMethod, ""),
		"not yet valid":            sign(signer(func(sg *servicetoken.Signer) { sg.Now = func() time.Time { return now.Add(3 * time.Minute) } }), audience, statusMethod, ""),
	} {
		codeIs(t, name, invoke(conn, statusMethod, authorization), codes.Unauthenticated)
	}

	// Anonymous methods need a credential too, and a credential with no user
	// cannot open a protected method. A user credential reaches the handler.
	codeIs(t, "anonymous credential on a protected method", invoke(conn, protectedMethod, sign(signer(nil), audience, protectedMethod, "")), codes.Unauthenticated)
	codeIs(t, "no credential on a protected method", invoke(conn, protectedMethod, ""), codes.Unauthenticated)
	if len(probe.calls()) != 0 {
		t.Fatalf("a protected handler ran for a rejected call: %v", probe.calls())
	}
	if err := invoke(conn, protectedMethod, sign(signer(nil), audience, protectedMethod, "user-1")); err != nil {
		t.Fatalf("user credential refused: %v", err)
	}
	if got := probe.calls(); len(got) != 1 || got[0].UserID != "user-1" {
		t.Fatalf("handler saw %v", got)
	}
}

func TestForgedMetadataChangesNothing(t *testing.T) {
	probe := &protectedProbe{}
	s := newStack(t, withProbe(probe))
	conn := s.direct(s.ca.CAFile, "tripmap.internal")
	sg := &servicetoken.Signer{Issuer: issuer, KID: "k1", Key: s.keys.Private}
	token := func(method, sub string) string {
		tok, err := sg.Sign(audience, method, sub)
		if err != nil {
			t.Fatal(err)
		}
		return "Bearer " + tok
	}
	forged := []string{"x-user-id", "admin", "x-bff-user-id", "admin", "x-forwarded-user", "admin", "x-bff-client-ip", "not-an-address", "x-request-id", "bad id!"}

	// Identity-looking metadata cannot turn an anonymous credential into a user.
	codeIs(t, "forged identity on an anonymous credential", invoke(conn, protectedMethod, token(protectedMethod, ""), forged...), codes.Unauthenticated)
	// Nor does it replace the user a valid credential names; bad address and
	// request ID values are dropped or replaced.
	if err := invoke(conn, protectedMethod, token(protectedMethod, "user-1"), forged...); err != nil {
		t.Fatal(err)
	}
	got := probe.calls()
	if len(got) != 1 || got[0].UserID != "user-1" || got[0].ClientIP != "" || len(got[0].RequestID) != 32 {
		t.Fatalf("handler saw %+v", got)
	}
	// Forged metadata without any credential is refused like anything else.
	codeIs(t, "forged metadata alone", invoke(conn, protectedMethod, "", forged...), codes.Unauthenticated)
}

func TestTripmapOnlyTalksTLSToATrustedIdentity(t *testing.T) {
	s := newStack(t)
	sg := &servicetoken.Signer{Issuer: issuer, KID: "k1", Key: s.keys.Private}
	tok, err := sg.Sign(audience, statusMethod, "")
	if err != nil {
		t.Fatal(err)
	}
	auth := "Bearer " + tok
	if err := invoke(s.direct(s.ca.CAFile, "tripmap.internal"), statusMethod, auth); err != nil {
		t.Fatalf("trusted identity: %v", err)
	}
	// A certificate authority the client does not trust, a wrong service name, and
	// no TLS at all all fail before any call is answered.
	codeIs(t, "untrusted authority", invoke(s.direct(testkit.NewCA(t).CAFile, "tripmap.internal"), statusMethod, auth), codes.Unavailable)
	codeIs(t, "wrong service identity", invoke(s.direct(s.ca.CAFile, "billing.internal"), statusMethod, auth), codes.Unavailable)
	plain, err := grpc.NewClient(s.tripmap.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	codeIs(t, "plaintext", invoke(plain, statusMethod, auth), codes.Unavailable)
}

func TestPublicKeyRotation(t *testing.T) {
	oldKeys, newKeys := testkit.NewKeys(t), testkit.NewKeys(t)
	trust := func(entries ...servicetoken.TrustedKey) func(*options, *stack) {
		return func(o *options, _ *stack) { o.trusted = entries }
	}
	oldKey := servicetoken.TrustedKey{KID: "2026-10", PublicKeyFile: oldKeys.PublicFile}
	newKey := servicetoken.TrustedKey{KID: "2026-11", PublicKeyFile: newKeys.PublicFile}
	call := func(s *stack, keys testkit.Keys, kid string) error {
		tok, err := (&servicetoken.Signer{Issuer: issuer, KID: kid, Key: keys.Private}).Sign(audience, statusMethod, "")
		if err != nil {
			t.Fatal(err)
		}
		return invoke(s.direct(s.ca.CAFile, "tripmap.internal"), statusMethod, "Bearer "+tok)
	}

	// 1. The new public key is distributed while the BFF still signs with the old key.
	both := newStack(t, trust(oldKey, newKey))
	if err := call(both, oldKeys, "2026-10"); err != nil {
		t.Fatalf("old key while both are trusted: %v", err)
	}
	if err := call(both, newKeys, "2026-11"); err != nil {
		t.Fatalf("new key while both are trusted: %v", err)
	}
	// 2. The BFF switches its signing key; the real chain keeps working.
	switched := newStack(t, trust(oldKey, newKey), func(o *options, _ *stack) { o.signing, o.signingKID = &newKeys, "2026-11" })
	if _, err := switched.status.GetStatus(context.Background(), connect.NewRequest(&bffv1.GetStatusRequest{})); err != nil {
		t.Fatalf("BFF signing with the new key: %v", err)
	}
	// 3. The old public key is withdrawn: its tokens stop working, the new ones do not.
	retired := newStack(t, trust(newKey))
	codeIs(t, "retired key", call(retired, oldKeys, "2026-10"), codes.Unauthenticated)
	if err := call(retired, newKeys, "2026-11"); err != nil {
		t.Fatalf("new key after retiring the old: %v", err)
	}
	// A BFF still signing with a retired key is refused, and the browser sees a server fault.
	stale := newStack(t, trust(newKey), func(o *options, _ *stack) { o.signing, o.signingKID = &oldKeys, "2026-10" })
	_, err := stale.status.GetStatus(context.Background(), connect.NewRequest(&bffv1.GetStatusRequest{}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("BFF with a retired key: %v", err)
	}
}
