// Command dev-secrets writes the BFF's development Ed25519 signing key pair.
// Optional -tls also writes a local CA and tripmap's server certificate.
// Files are ignored by Git; deployments supply their own configuration or Secrets.
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
	"maps"
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
	withTLS := flag.Bool("tls", false, "also generate CA and tripmap TLS certificate/key")
	flag.Parse()
	if err := run(*dir, *force, *withTLS); err != nil {
		log.Fatal(err)
	}
}

func run(dir string, force, withTLS bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	files := []string{"bff.key", "bff.pub"}
	if withTLS {
		files = append(files, "ca.pem", "tripmap.pem", "tripmap.key")
	}
	if !force {
		for _, f := range files {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				return fmt.Errorf("%s already exists; pass -force to replace the development secrets", filepath.Join(dir, f))
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privPEM, pubPEM, err := servicetoken.MarshalKeyPair(signing)
	if err != nil {
		return err
	}
	material := map[string][]byte{"bff.key": privPEM, "bff.pub": pubPEM}
	if withTLS {
		tlsFiles, err := tlsMaterial()
		if err != nil {
			return err
		}
		maps.Copy(material, tlsFiles)
	}
	for _, name := range files {
		mode := os.FileMode(0o644)
		if filepath.Ext(name) == ".key" {
			mode = 0o600
		}
		if err := os.WriteFile(filepath.Join(dir, name), material[name], mode); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %v to %s (kid dev-1)\n", files, dir)
	return nil
}

func tlsMaterial() (map[string][]byte, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "trip-map development CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "tripmap"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost", "tripmap"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		"ca.pem":      pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		"tripmap.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"tripmap.key": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}, nil
}
