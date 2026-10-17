// Command dev-secrets writes the local certificate authority, tripmap's server
// certificate and the BFF's Ed25519 signing key pair that the example
// configurations refer to. The files are for development only and are ignored by
// Git; deployments mount their own Secrets.
package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/kongken/mono/internal/platform/servicetoken"
)

func main() {
	dir := flag.String("dir", "dev-secrets", "output directory")
	force := flag.Bool("force", false, "overwrite existing files")
	flag.Parse()
	if err := run(*dir, *force); err != nil {
		log.Fatal(err)
	}
}

func run(dir string, force bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	files := []string{"ca.pem", "tripmap.pem", "tripmap.key", "bff.key", "bff.pub"}
	if !force {
		for _, f := range files {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				return fmt.Errorf("%s already exists; pass -force to replace the development secrets", filepath.Join(dir, f))
			}
		}
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "trip-map development CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	ca, _ := x509.ParseCertificate(caDER)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "tripmap"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost", "tripmap"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privPEM, pubPEM, err := servicetoken.MarshalKeyPair(signing)
	if err != nil {
		return err
	}
	write := func(name string, data []byte, mode os.FileMode) error {
		return os.WriteFile(filepath.Join(dir, name), data, mode)
	}
	for _, w := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644},
		{"tripmap.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644},
		{"tripmap.key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600},
		{"bff.key", privPEM, 0o600},
		{"bff.pub", pubPEM, 0o644},
	} {
		if err := write(w.name, w.data, w.mode); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %v to %s (kid dev-1)\n", files, dir)
	return nil
}
