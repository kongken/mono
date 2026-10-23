package upstream_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/upstream"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/platform/servicetoken"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

func TestH2CWithInlineKeysNeedsNoCertificateOrKeyFiles(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	priv, pub, err := servicetoken.MarshalKeyPair(private)
	if err != nil {
		t.Fatal(err)
	}
	// Literal YAML PEM blocks, parsed into the real configuration types. No key
	// files, TLS certificates or CA are created anywhere in this test.
	var service struct {
		InternalAuth servicetoken.VerifyConfig `yaml:"internal_auth"`
	}
	raw := "internal_auth:\n  issuer: bff\n  audience: tripmap\n  trusted_keys:\n    - kid: k1\n      public_key: |\n        " + strings.ReplaceAll(strings.TrimSpace(string(pub)), "\n", "\n        ") + "\n"
	if err := yaml.Unmarshal([]byte(raw), &service); err != nil {
		t.Fatal(err)
	}
	verifier, err := service.InternalAuth.NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := grpcx.Start(grpcx.ServerConfig{Transport: "h2c", Listen: "127.0.0.1:0"},
		func(r grpc.ServiceRegistrar) { tripmapv1.RegisterSystemServiceServer(r, statusServer{}) },
		[]grpc.UnaryServerInterceptor{rpcauth.UnaryServerInterceptor(verifier)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(time.Second) })
	var cfg config.Config
	raw = fmt.Sprintf("grpc:\n  services:\n    tripmap:\n      address: %s\n      transport: h2c\ninternal_auth:\n  issuer: bff\n  kid: k1\n  private_key: |\n    %s\n", srv.Addr(), strings.ReplaceAll(strings.TrimSpace(string(priv)), "\n", "\n    "))
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateInternal([]string{"tripmap"}); err != nil {
		t.Fatal(err)
	}
	clients := dial(t, &cfg, "tripmap")
	if err := getStatus(clients, "tripmap"); err != nil {
		t.Fatal(err)
	}
	// Selecting h2c and inline sources must not bypass the signature check.
	unsigned, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer unsigned.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := tripmapv1.NewSystemServiceClient(unsigned).GetStatus(ctx, &tripmapv1.GetStatusRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unsigned call: code %v, want Unauthenticated", status.Code(err))
	}
	cfg.InternalAuth.PrivateKeyFile = "/nonexistent/bff.key"
	if _, err := upstream.Dial(&cfg, []string{"tripmap"}); err == nil {
		t.Fatal("conflicting sources accepted at dial")
	}
	cfg.InternalAuth.PrivateKeyFile = ""
	cfg.InternalAuth.PrivateKey = "secret-marker"
	if _, err := upstream.Dial(&cfg, []string{"tripmap"}); err == nil || strings.Contains(err.Error(), "secret-marker") {
		t.Fatal("invalid inline key accepted or content exposed at dial")
	}
}
