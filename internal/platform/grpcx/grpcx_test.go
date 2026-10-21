package grpcx_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestServerConfigValidate(t *testing.T) {
	ok := map[string]grpcx.ServerConfig{
		"default is tls":    {Listen: ":1", CertFile: "c", KeyFile: "k"},
		"explicit tls":      {Transport: "tls", Listen: ":1", CertFile: "c", KeyFile: "k"},
		"h2c without certs": {Transport: "h2c", Listen: ":1"},
	}
	for name, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]grpcx.ServerConfig{
		"no listener":                 {CertFile: "c", KeyFile: "k"},
		"tls without cert":            {Listen: ":1", KeyFile: "k"},
		"tls without key":             {Transport: "tls", Listen: ":1", CertFile: "c"},
		"unknown transport":           {Transport: "plaintext", Listen: ":1", CertFile: "c", KeyFile: "k"},
		"wrong case is unknown":       {Transport: "H2C", Listen: ":1"},
		"h2c without listener":        {Transport: "h2c"},
		"h2c with cert":               {Transport: "h2c", Listen: ":1", CertFile: "c"},
		"h2c with key":                {Transport: "h2c", Listen: ":1", KeyFile: "k"},
		"h2c with both cert and key":  {Transport: "h2c", Listen: ":1", CertFile: "c", KeyFile: "k"},
		"negative limit":              {Listen: ":1", CertFile: "c", KeyFile: "k", MaxMessageBytes: -1},
		"negative limit on h2c":       {Transport: "h2c", Listen: ":1", MaxMessageBytes: -1},
		"unknown transport and certs": {Transport: "none", Listen: ":1"},
	}
	for name, c := range bad {
		if c.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNormalizeTransport(t *testing.T) {
	for in, want := range map[string]string{"": grpcx.TransportTLS, "tls": grpcx.TransportTLS, "h2c": grpcx.TransportH2C} {
		got, err := grpcx.NormalizeTransport(in)
		if err != nil || got != want {
			t.Errorf("NormalizeTransport(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"insecure", "TLS", " h2c", "mtls"} {
		if _, err := grpcx.NormalizeTransport(in); err == nil {
			t.Errorf("NormalizeTransport(%q) accepted", in)
		}
	}
}

func startHealth(t *testing.T, cfg grpcx.ServerConfig) *grpcx.Server {
	t.Helper()
	srv, err := grpcx.Start(cfg, func(r grpc.ServiceRegistrar) {
		healthpb.RegisterHealthServer(r, health.NewServer())
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(time.Second) })
	return srv
}

func check(conn *grpc.ClientConn, service string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: service})
	return err
}

func dial(t *testing.T, addr string, creds credentials.TransportCredentials) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func tlsCreds(t *testing.T, caFile, serverName string) credentials.TransportCredentials {
	t.Helper()
	pem, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	return credentials.NewTLS(&tls.Config{RootCAs: pool, ServerName: serverName, MinVersion: tls.VersionTLS13})
}

func TestTLSIsTheDefaultAndNeverFallsBackToPlaintext(t *testing.T) {
	ca := testkit.NewCA(t)
	pki := ca.Issue(t, "svc.internal")
	srv := startHealth(t, grpcx.ServerConfig{Listen: "127.0.0.1:0", CertFile: pki.CertFile, KeyFile: pki.KeyFile})

	if err := check(dial(t, srv.Addr(), tlsCreds(t, ca.CAFile, "svc.internal")), ""); err != nil {
		t.Fatalf("trusted TLS client: %v", err)
	}
	for name, conn := range map[string]*grpc.ClientConn{
		"plaintext client":    dial(t, srv.Addr(), insecure.NewCredentials()),
		"untrusted authority": dial(t, srv.Addr(), tlsCreds(t, testkit.NewCA(t).CAFile, "svc.internal")),
		"wrong server name":   dial(t, srv.Addr(), tlsCreds(t, ca.CAFile, "other.internal")),
	} {
		if got := status.Code(check(conn, "")); got != codes.Unavailable {
			t.Errorf("%s: code %v, want Unavailable", name, got)
		}
	}
}

func TestH2CServesNativeGRPCWithoutTLSMaterial(t *testing.T) {
	srv := startHealth(t, grpcx.ServerConfig{Transport: "h2c", Listen: "127.0.0.1:0"})

	if err := check(dial(t, srv.Addr(), insecure.NewCredentials()), ""); err != nil {
		t.Fatalf("h2c client: %v", err)
	}
	// A TLS client against an h2c listener fails; it is not silently downgraded.
	ca := testkit.NewCA(t)
	if got := status.Code(check(dial(t, srv.Addr(), tlsCreds(t, ca.CAFile, "svc.internal")), "")); got != codes.Unavailable {
		t.Errorf("TLS client on h2c listener: code %v, want Unavailable", got)
	}
}

func TestH2CRefusesToStartWithTLSMaterial(t *testing.T) {
	pki := testkit.NewCA(t).Issue(t, "svc.internal")
	_, err := grpcx.Start(grpcx.ServerConfig{Transport: "h2c", Listen: "127.0.0.1:0", CertFile: pki.CertFile, KeyFile: pki.KeyFile}, func(grpc.ServiceRegistrar) {}, nil, nil)
	if err == nil {
		t.Fatal("h2c with a certificate started")
	}
}

func TestMessageLimitAndShutdownApplyToBothTransports(t *testing.T) {
	ca := testkit.NewCA(t)
	pki := ca.Issue(t, "svc.internal")
	for name, tc := range map[string]struct {
		cfg   grpcx.ServerConfig
		creds func(t *testing.T) credentials.TransportCredentials
	}{
		"tls": {
			cfg:   grpcx.ServerConfig{Listen: "127.0.0.1:0", CertFile: pki.CertFile, KeyFile: pki.KeyFile, MaxMessageBytes: 1024},
			creds: func(t *testing.T) credentials.TransportCredentials { return tlsCreds(t, ca.CAFile, "svc.internal") },
		},
		"h2c": {
			cfg:   grpcx.ServerConfig{Transport: "h2c", Listen: "127.0.0.1:0", MaxMessageBytes: 1024},
			creds: func(*testing.T) credentials.TransportCredentials { return insecure.NewCredentials() },
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv, err := grpcx.Start(tc.cfg, func(r grpc.ServiceRegistrar) { healthpb.RegisterHealthServer(r, health.NewServer()) }, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			conn := dial(t, srv.Addr(), tc.creds(t))
			if err := check(conn, ""); err != nil {
				t.Fatal(err)
			}
			if got := status.Code(check(conn, strings.Repeat("x", 2048))); got != codes.ResourceExhausted {
				t.Errorf("oversized request: code %v, want ResourceExhausted", got)
			}
			srv.Shutdown(time.Second)
			if err := check(conn, ""); err == nil {
				t.Error("call succeeded after shutdown")
			}
		})
	}
}
