package e2e_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/platform/servicetoken"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
)

// A TLS client gets nowhere on an h2c listener, even with a CA that would have
// vouched for it: the server never presents a certificate.
func TestH2CListenerIsNotReachableOverTLS(t *testing.T) {
	s := newStack(t, withTransport("h2c"))
	pem, err := os.ReadFile(s.ca.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	conn, err := grpc.NewClient(s.tripmap.GRPCAddr(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: pool, ServerName: "tripmap.internal", MinVersion: tls.VersionTLS13})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tok, err := (&servicetoken.Signer{Issuer: issuer, KID: "k1", Key: s.keys.Private}).Sign(audience, statusMethod, "")
	if err != nil {
		t.Fatal(err)
	}
	codeIs(t, "tls client on h2c", invoke(conn, statusMethod, "Bearer "+tok), codes.Unavailable)
	// Without the signature h2c still refuses: no transport choice skips the credential.
	codeIs(t, "unsigned call on h2c", invoke(s.plain(), statusMethod, ""), codes.Unauthenticated)
}

// The two sides must agree. A mismatch surfaces as an unavailable service; it is
// never papered over by retrying with the other transport.
func TestMismatchedTransportsFailInsteadOfFallingBack(t *testing.T) {
	for name, tc := range map[string]struct{ server, bff string }{
		"tripmap h2c, BFF tls": {"h2c", "tls"},
		"tripmap tls, BFF h2c": {"tls", "h2c"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStack(t, withMismatchedTransports(tc.server, tc.bff))
			_, err := s.status.GetStatus(context.Background(), connect.NewRequest(&bfftripmapv1.GetStatusRequest{}))
			if connect.CodeOf(err) != connect.CodeUnavailable {
				t.Fatalf("mismatch: %v", err)
			}
		})
	}
}

// /readyz probes the business gRPC service with a real signed call over the
// transport that service uses, so reaching the HTTP port is not enough.
func TestBFFReadinessFollowsTheGRPCTransport(t *testing.T) {
	eachTransport(t, func(t *testing.T, transport string) {
		s := newStack(t, withTransport(transport))
		ready := func() (int, bool) {
			res, err := s.bff.Client().Get(s.bff.URL + "/readyz")
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var body struct {
				Dependencies []struct {
					Name  string `json:"name"`
					Ready bool   `json:"ready"`
				} `json:"dependencies"`
			}
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			for _, d := range body.Dependencies {
				if d.Name == "tripmap_grpc" {
					return res.StatusCode, d.Ready
				}
			}
			t.Fatalf("no tripmap_grpc probe in %+v", body)
			return 0, false
		}
		if code, ok := ready(); code != http.StatusOK || !ok {
			t.Fatalf("with tripmap up: %d, grpc ready %v", code, ok)
		}
		// The HTTP port stays up; only gRPC goes away.
		s.tripmap.StopGRPC()
		if code, ok := ready(); code != http.StatusServiceUnavailable || ok {
			t.Fatalf("with tripmap gRPC down: %d, grpc ready %v", code, ok)
		}
	})
}
