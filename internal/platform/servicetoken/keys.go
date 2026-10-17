package servicetoken

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"
)

// LoadPrivateKey reads an Ed25519 private key in PKCS#8 PEM form.
func LoadPrivateKey(file string) (ed25519.PrivateKey, error) {
	block, err := pemBlock(file, "PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an Ed25519 private key", file)
	}
	return key, nil
}

// LoadPublicKey reads an Ed25519 public key in PKIX PEM form.
func LoadPublicKey(file string) (ed25519.PublicKey, error) {
	block, err := pemBlock(file, "PUBLIC KEY")
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an Ed25519 public key", file)
	}
	return key, nil
}

// MarshalKeyPair encodes a key pair as PEM, for key generation tooling and tests.
func MarshalKeyPair(priv ed25519.PrivateKey) (privatePEM, publicPEM []byte, err error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	pub, err := x509.MarshalPKIXPublicKey(priv.Public())
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}), nil
}

func pemBlock(file, want string) (*pem.Block, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != want {
		return nil, errors.New(file + ": expected a " + want + " PEM block")
	}
	return block, nil
}

// TrustedKey is one entry of a service's trusted public key list.
type TrustedKey struct {
	KID           string `yaml:"kid"`
	PublicKeyFile string `yaml:"public_key_file"`
}

// VerifyConfig is the service side of the internal credential configuration.
type VerifyConfig struct {
	// Issuer must match the BFF's internal_auth.issuer.
	Issuer string `yaml:"issuer"`
	// Audience is this service's name as the BFF addresses it, for example tripmap.
	Audience    string       `yaml:"audience"`
	TrustedKeys []TrustedKey `yaml:"trusted_keys"`
	// ClockSkew defaults to 10s.
	ClockSkew string `yaml:"clock_skew"`
}

// NewVerifier loads the trusted keys and builds the Verifier.
func (c VerifyConfig) NewVerifier() (*Verifier, error) {
	if c.Issuer == "" || c.Audience == "" || len(c.TrustedKeys) == 0 {
		return nil, errors.New("internal_auth requires issuer, audience and at least one trusted key")
	}
	v := &Verifier{Issuer: c.Issuer, Audience: c.Audience, Keys: map[string]ed25519.PublicKey{}}
	for _, k := range c.TrustedKeys {
		if k.KID == "" || k.PublicKeyFile == "" {
			return nil, errors.New("internal_auth.trusted_keys entries need kid and public_key_file")
		}
		if _, dup := v.Keys[k.KID]; dup {
			return nil, fmt.Errorf("internal_auth.trusted_keys: kid %q is listed twice", k.KID)
		}
		key, err := LoadPublicKey(k.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("internal_auth.trusted_keys %s: %w", k.KID, err)
		}
		v.Keys[k.KID] = key
	}
	if c.ClockSkew != "" {
		d, err := parseSkew(c.ClockSkew)
		if err != nil {
			return nil, err
		}
		v.Skew = d
	}
	return v, nil
}

func parseSkew(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 || d > time.Minute {
		return 0, fmt.Errorf("internal_auth.clock_skew %q must be a duration between 0 and 1m", s)
	}
	return d, nil
}
