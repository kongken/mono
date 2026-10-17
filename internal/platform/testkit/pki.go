// Package testkit creates isolated TLS material and signing keys for tests, so
// they never touch real Secrets.
package testkit

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kongken/mono/internal/platform/servicetoken"
)

// CA is a throwaway certificate authority.
type CA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CAFile  string
	dir     string
	counter int64
}

// NewCA writes the CA certificate into a temporary directory.
func NewCA(t testing.TB) *CA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "tripmap test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	dir := t.TempDir()
	ca := &CA{Cert: cert, Key: key, dir: dir, CAFile: filepath.Join(dir, "ca.pem"), counter: 1}
	writePEM(t, ca.CAFile, "CERTIFICATE", der)
	return ca
}

// Server is a TLS identity issued by a CA.
type Server struct{ CertFile, KeyFile string }

// Issue creates a server certificate valid for 127.0.0.1 and the given DNS names.
func (ca *CA) Issue(t testing.TB, dnsNames ...string) Server {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca.counter++
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(ca.counter), Subject: pkix.Name{CommonName: "tripmap test service"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: dnsNames, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	s := Server{CertFile: filepath.Join(ca.dir, "server-"+big.NewInt(ca.counter).String()+".pem"), KeyFile: filepath.Join(ca.dir, "server-"+big.NewInt(ca.counter).String()+".key")}
	writePEM(t, s.CertFile, "CERTIFICATE", der)
	writePEM(t, s.KeyFile, "EC PRIVATE KEY", keyDER)
	return s
}

// Keys is an Ed25519 signing key pair on disk.
type Keys struct {
	Private                 ed25519.PrivateKey
	PrivateFile, PublicFile string
}

// NewKeys generates a key pair.
func NewKeys(t testing.TB) Keys {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privPEM, pubPEM, err := servicetoken.MarshalKeyPair(priv)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	dir := t.TempDir()
	k := Keys{Private: priv, PrivateFile: filepath.Join(dir, "bff.key"), PublicFile: filepath.Join(dir, "bff.pub")}
	if err := os.WriteFile(k.PrivateFile, privPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(k.PublicFile, pubPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	return k
}

func writePEM(t testing.TB, file, kind string, der []byte) {
	t.Helper()
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}
