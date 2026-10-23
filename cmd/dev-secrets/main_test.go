package main

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kongken/mono/internal/platform/servicetoken"
)

func TestSigningOnlyAndOptionalTLS(t *testing.T) {
	for _, withTLS := range []bool{false, true} {
		name := "signing only"
		if withTLS {
			name = "with TLS"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := run(dir, false, withTLS); err != nil {
				t.Fatal(err)
			}
			priv, err := servicetoken.LoadPrivateKey(filepath.Join(dir, "bff.key"))
			if err != nil {
				t.Fatal(err)
			}
			pub, err := servicetoken.LoadPublicKey(filepath.Join(dir, "bff.pub"))
			if err != nil || !pub.Equal(priv.Public()) {
				t.Fatal("generated key pair does not match")
			}
			info, err := os.Stat(filepath.Join(dir, "bff.key"))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("private key permissions must be 0600")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			want := []string{"bff.key", "bff.pub"}
			if withTLS {
				want = append(want, "ca.pem", "tripmap.key", "tripmap.pem")
				cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "tripmap.pem"), filepath.Join(dir, "tripmap.key"))
				if err != nil {
					t.Fatal(err)
				}
				ca, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
				if err != nil {
					t.Fatal(err)
				}
				pool := x509.NewCertPool()
				if !pool.AppendCertsFromPEM(ca) {
					t.Fatal("invalid CA")
				}
				if _, err := cert.Leaf.Verify(x509.VerifyOptions{DNSName: "localhost", Roots: pool}); err != nil {
					t.Fatal(err)
				}
			}
			if !slices.Equal(names, want) {
				t.Fatalf("generated %v, want %v", names, want)
			}
			if err := run(dir, false, withTLS); err == nil {
				t.Fatal("existing keys overwritten without -force")
			}
			again, err := servicetoken.LoadPrivateKey(filepath.Join(dir, "bff.key"))
			if err != nil || !priv.Equal(again) {
				t.Fatal("refused overwrite changed key")
			}
			if err := run(dir, true, withTLS); err != nil {
				t.Fatal(err)
			}
			again, err = servicetoken.LoadPrivateKey(filepath.Join(dir, "bff.key"))
			if err != nil || priv.Equal(again) {
				t.Fatal("-force did not rotate key")
			}
		})
	}
}
