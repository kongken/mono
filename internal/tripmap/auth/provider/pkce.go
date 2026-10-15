package provider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// PKCEProvider is implemented by providers that require Proof Key for Code
// Exchange (RFC 7636, method S256). The AuthService detects it and, instead of
// AuthorizeURL / Exchange, uses the PKCE variants with a server-generated
// verifier that never leaves the backend.
type PKCEProvider interface {
	Provider
	// AuthorizePKCEURL is AuthorizeURL plus the S256 code challenge.
	AuthorizePKCEURL(state, codeChallenge string) string
	// ExchangePKCE is Exchange plus the code verifier whose challenge was sent
	// in AuthorizePKCEURL.
	ExchangePKCE(ctx context.Context, code, codeVerifier string) (*Claims, error)
}

// NewPKCE returns a fresh random code verifier and its S256 challenge.
func NewPKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32) // 43 unreserved characters once encoded
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	return verifier, PKCEChallenge(verifier), nil
}

// PKCEChallenge derives the S256 code challenge for a code verifier.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
