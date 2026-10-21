package upstream_test

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/upstream"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/platform/testkit"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type statusServer struct {
	tripmapv1.UnimplementedSystemServiceServer
}

func (statusServer) GetStatus(context.Context, *tripmapv1.GetStatusRequest) (*tripmapv1.GetStatusResponse, error) {
	return &tripmapv1.GetStatusResponse{Ready: true}, nil
}

// startService runs a real grpcx server behind the real credential interceptor;
// audience is the service name the BFF signs for.
func startService(t *testing.T, keys testkit.Keys, audience string, cfg grpcx.ServerConfig) *grpcx.Server {
	t.Helper()
	verifier := &servicetoken.Verifier{Issuer: "bff", Audience: audience, Keys: map[string]ed25519.PublicKey{"k1": keys.Private.Public().(ed25519.PublicKey)}}
	srv, err := grpcx.Start(cfg, func(r grpc.ServiceRegistrar) { tripmapv1.RegisterSystemServiceServer(r, statusServer{}) },
		[]grpc.UnaryServerInterceptor{rpcauth.UnaryServerInterceptor(verifier)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(time.Second) })
	return srv
}

func getStatus(clients *upstream.Clients, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := tripmapv1.NewSystemServiceClient(clients.Conn(name)).GetStatus(ctx, &tripmapv1.GetStatusRequest{})
	return err
}

func bffConfig(keys testkit.Keys, caFile string, services map[string]config.GRPCService) *config.Config {
	return &config.Config{
		InternalAuth: config.InternalAuth{Issuer: "bff", KID: "k1", PrivateKeyFile: keys.PrivateFile},
		GRPC:         config.GRPCConfig{CAFile: caFile, Services: services},
	}
}

func dial(t *testing.T, cfg *config.Config, names ...string) *upstream.Clients {
	t.Helper()
	c, err := upstream.Dial(cfg, names)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestH2CUpstreamNeedsNoCertificateAuthority(t *testing.T) {
	keys := testkit.NewKeys(t)
	srv := startService(t, keys, "tripmap", grpcx.ServerConfig{Transport: "h2c", Listen: "127.0.0.1:0"})
	c := dial(t, bffConfig(keys, "", map[string]config.GRPCService{"tripmap": {Address: srv.Addr(), Transport: "h2c"}}), "tripmap")
	// The server's interceptor verifies the BFF's signature, so success proves the
	// credential still travels over h2c.
	if err := getStatus(c, "tripmap"); err != nil {
		t.Fatal(err)
	}
}

func TestH2CUpstreamNeverReadsTheCAFile(t *testing.T) {
	keys := testkit.NewKeys(t)
	cfg := bffConfig(keys, "/nonexistent/ca.pem", map[string]config.GRPCService{"tripmap": {Address: "127.0.0.1:1", Transport: "h2c"}})
	if _, err := upstream.Dial(cfg, []string{"tripmap"}); err != nil {
		t.Fatalf("all-h2c config touched the CA: %v", err)
	}
}

func TestMixedUpstreamsKeepVerifyingTheTLSOnes(t *testing.T) {
	keys, ca := testkit.NewKeys(t), testkit.NewCA(t)
	pki := ca.Issue(t, "secure.internal")
	secure := startService(t, keys, "secure", grpcx.ServerConfig{Listen: "127.0.0.1:0", CertFile: pki.CertFile, KeyFile: pki.KeyFile})
	plain := startService(t, keys, "plain", grpcx.ServerConfig{Transport: "h2c", Listen: "127.0.0.1:0"})

	c := dial(t, bffConfig(keys, ca.CAFile, map[string]config.GRPCService{
		"secure":     {Address: secure.Addr(), ServerName: "secure.internal"},
		"plain":      {Address: plain.Addr(), Transport: "h2c"},
		"wrong-name": {Address: secure.Addr(), ServerName: "other.internal"},
		"tls-to-h2c": {Address: plain.Addr(), ServerName: "secure.internal"},
		"h2c-to-tls": {Address: secure.Addr(), Transport: "h2c"},
		"explicit":   {Address: secure.Addr(), Transport: "tls", ServerName: "secure.internal"},
	}), "secure", "plain", "wrong-name", "tls-to-h2c", "h2c-to-tls", "explicit")

	for _, name := range []string{"secure", "plain"} {
		if err := getStatus(c, name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := getStatus(c, "explicit"); status.Code(err) != codes.Unauthenticated {
		// Audience is the service name, so this reaches the verifier (and is refused
		// there) only if the TLS handshake succeeded.
		t.Errorf("explicit tls: %v", err)
	}
	// Identity mismatch and transport mismatch in either direction fail; none of
	// them falls back to another transport.
	for _, name := range []string{"wrong-name", "tls-to-h2c", "h2c-to-tls"} {
		if got := status.Code(getStatus(c, name)); got != codes.Unavailable {
			t.Errorf("%s: code %v, want Unavailable", name, got)
		}
	}
}

func TestTLSUpstreamNeedsAValidCA(t *testing.T) {
	keys, ca := testkit.NewKeys(t), testkit.NewCA(t)
	svcs := map[string]config.GRPCService{"tripmap": {Address: "127.0.0.1:1"}}
	for name, caFile := range map[string]string{"missing file": "/nonexistent/ca.pem", "unset": ""} {
		if _, err := upstream.Dial(bffConfig(keys, caFile, svcs), []string{"tripmap"}); err == nil {
			t.Errorf("%s: tls upstream dialed without a CA", name)
		}
	}
	// An untrusted CA yields a failed call, not plaintext.
	pki := ca.Issue(t, "tripmap.internal")
	srv := startService(t, keys, "tripmap", grpcx.ServerConfig{Listen: "127.0.0.1:0", CertFile: pki.CertFile, KeyFile: pki.KeyFile})
	c := dial(t, bffConfig(keys, testkit.NewCA(t).CAFile, map[string]config.GRPCService{"tripmap": {Address: srv.Addr(), ServerName: "tripmap.internal"}}), "tripmap")
	if got := status.Code(getStatus(c, "tripmap")); got != codes.Unavailable {
		t.Errorf("untrusted CA: code %v, want Unavailable", got)
	}
}

func TestUnknownTransportIsRejectedAtDial(t *testing.T) {
	keys, ca := testkit.NewKeys(t), testkit.NewCA(t)
	cfg := bffConfig(keys, ca.CAFile, map[string]config.GRPCService{"tripmap": {Address: "127.0.0.1:1", Transport: "insecure"}})
	if _, err := upstream.Dial(cfg, []string{"tripmap"}); err == nil {
		t.Fatal("unknown transport dialed")
	}
}
