package servicetoken_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/platform/testkit"
)

func TestKeySources(t *testing.T) {
	keys := testkit.NewKeys(t)
	privatePEM, publicPEM, err := servicetoken.MarshalKeyPair(keys.Private)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecPrivate, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	ecPublic, err := x509.MarshalPKIXPublicKey(&ec.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, kind, file string
		valid, other     []byte
		read             func(string, string) error
	}{
		{"private", "PRIVATE KEY", keys.PrivateFile, privatePEM, ecPrivate, func(inline, file string) error {
			_, err := servicetoken.ReadPrivateKey(inline, file)
			return err
		}},
		{"public", "PUBLIC KEY", keys.PublicFile, publicPEM, ecPublic, func(inline, file string) error {
			_, err := servicetoken.ReadPublicKey(inline, file)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, source := range map[string][2]string{
				"inline": {string(tc.valid), ""},
				"file":   {"", tc.file},
			} {
				if err := tc.read(source[0], source[1]); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
			}
			for name, source := range map[string][2]string{
				"missing":          {},
				"both":             {string(tc.valid), tc.file},
				"no file fallback": {"secret-marker", tc.file},
				"missing file":     {"", filepath.Join(t.TempDir(), "absent")},
				"malformed PEM":    {"secret-marker", ""},
				"wrong PEM type":   {string(pem.EncodeToMemory(&pem.Block{Type: "WRONG", Bytes: tc.valid})), ""},
				"malformed DER":    {string(pem.EncodeToMemory(&pem.Block{Type: tc.kind, Bytes: []byte("secret-marker")})), ""},
				"not Ed25519":      {string(pem.EncodeToMemory(&pem.Block{Type: tc.kind, Bytes: tc.other})), ""},
			} {
				t.Run(name, func(t *testing.T) {
					err := tc.read(source[0], source[1])
					if err == nil {
						t.Fatal("invalid source accepted")
					}
					if strings.Contains(err.Error(), "secret-marker") || strings.Contains(err.Error(), "-----BEGIN") {
						t.Fatal("error exposed PEM content")
					}
				})
			}
			badFile := filepath.Join(t.TempDir(), "bad.key")
			if err := os.WriteFile(badFile, []byte("secret-marker"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := tc.read("", badFile); err == nil || strings.Contains(err.Error(), "secret-marker") {
				t.Fatal("invalid file accepted or content exposed")
			}
		})
	}
}

func TestVerifierWithMixedKeySources(t *testing.T) {
	old, next := testkit.NewKeys(t), testkit.NewKeys(t)
	_, pub, err := servicetoken.MarshalKeyPair(next.Private)
	if err != nil {
		t.Fatal(err)
	}
	cfg := servicetoken.VerifyConfig{
		Issuer: "bff", Audience: "tripmap",
		TrustedKeys: []servicetoken.TrustedKey{
			{KID: "old", PublicKeyFile: old.PublicFile},
			{KID: "next", PublicKey: string(pub)},
		},
	}
	v, err := cfg.NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	for kid, key := range map[string]ed25519.PrivateKey{"old": old.Private, "next": next.Private} {
		s := servicetoken.Signer{Issuer: "bff", KID: kid, Key: key}
		token, err := s.Sign("tripmap", "/status", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Verify(token, "/status"); err != nil {
			t.Fatalf("%s: %v", kid, err)
		}
	}
	for name, entries := range map[string][]servicetoken.TrustedKey{
		"missing kid":    {{PublicKey: string(pub)}},
		"missing source": {{KID: "next"}},
		"both sources":   {{KID: "next", PublicKey: string(pub), PublicKeyFile: next.PublicFile}},
		"duplicate kid":  {{KID: "next", PublicKey: string(pub)}, {KID: "next", PublicKeyFile: next.PublicFile}},
		"invalid inline": {{KID: "next", PublicKey: "secret-marker"}},
	} {
		cfg.TrustedKeys = entries
		if _, err := cfg.NewVerifier(); err == nil || strings.Contains(err.Error(), "secret-marker") {
			t.Errorf("%s: invalid config accepted or content exposed", name)
		}
	}
}
