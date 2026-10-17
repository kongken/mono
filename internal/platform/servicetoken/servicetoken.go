// Package servicetoken signs and verifies the short-lived credential the BFF
// attaches to every internal gRPC call. Only the BFF holds a private key; a
// business service holds the matching public keys and trusts nothing about the
// caller's network position.
//
// A token is a compact JWS (RFC 7515) with alg EdDSA. The header carries the
// key ID (kid); the claims are:
//
//	iss  issuer, identifies the BFF
//	aud  target service name, so a token for one service is useless at another
//	sub  user ID; absent on anonymous calls
//	iat, nbf, exp  issue, not-before and expiry times (seconds); exp-iat is 60s
//	jti  random token ID, for logs only; it is not a replay barrier
//	mth  full gRPC method the token is valid for, such as /tripmap.v1.SystemService/GetStatus
//
// Replay is limited by TLS, the 60s lifetime, aud and mth together; jti does not
// make a token single-use.
package servicetoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// Lifetime is how long a token is valid.
	Lifetime = 60 * time.Second
	// DefaultSkew is the clock difference tolerated between the BFF and a service.
	DefaultSkew = 10 * time.Second
	algorithm   = "EdDSA"
)

// ErrInvalid is returned for every verification failure. Callers answer
// UNAUTHENTICATED without saying which check failed, so the response does not
// help someone probing the boundary.
var ErrInvalid = errors.New("invalid service credential")

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	KID string `json:"kid"`
}

// Claims are the verified contents of a token.
type Claims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub,omitempty"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf"`
	ExpiresAt int64  `json:"exp"`
	ID        string `json:"jti"`
	Method    string `json:"mth"`
}

// Signer issues tokens with the BFF's private key.
type Signer struct {
	Issuer string
	KID    string
	Key    ed25519.PrivateKey
	// Now defaults to time.Now; tests set it to control time.
	Now func() time.Time
}

func (s *Signer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Sign issues a token for one call of method on audience. An empty subject marks
// an anonymous call.
func (s *Signer) Sign(audience, method, subject string) (string, error) {
	if s.Issuer == "" || s.KID == "" || len(s.Key) != ed25519.PrivateKeySize || audience == "" || method == "" {
		return "", errors.New("servicetoken: signer, audience and method are required")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	now := s.now().Unix()
	head, _ := json.Marshal(header{Alg: algorithm, Typ: "JWT", KID: s.KID})
	body, _ := json.Marshal(Claims{Issuer: s.Issuer, Audience: audience, Subject: subject, IssuedAt: now, NotBefore: now, ExpiresAt: now + int64(Lifetime/time.Second), ID: hex.EncodeToString(id), Method: method})
	signing := b64(head) + "." + b64(body)
	return signing + "." + b64(ed25519.Sign(s.Key, []byte(signing))), nil
}

// Verifier checks tokens against the trusted public keys of one service.
type Verifier struct {
	Issuer   string
	Audience string
	// Keys maps kid to public key. Listing the old and the new key together is how
	// a rotation proceeds: publish the new key, switch the BFF's signing key, then
	// drop the old key after Lifetime plus Skew.
	Keys map[string]ed25519.PublicKey
	Skew time.Duration
	Now  func() time.Time
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// Verify checks signature, kid, issuer, audience, timing and the method binding.
func (v *Verifier) Verify(token, method string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalid
	}
	var head header
	if err := decode(parts[0], &head); err != nil || head.Alg != algorithm {
		return Claims{}, ErrInvalid
	}
	key, ok := v.Keys[head.KID]
	if !ok || len(key) != ed25519.PublicKeySize {
		return Claims{}, ErrInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), sig) {
		return Claims{}, ErrInvalid
	}
	var c Claims
	if err := decode(parts[1], &c); err != nil {
		return Claims{}, ErrInvalid
	}
	skew := v.Skew
	if skew <= 0 {
		skew = DefaultSkew
	}
	now := v.now()
	issued, notBefore, expires := time.Unix(c.IssuedAt, 0), time.Unix(c.NotBefore, 0), time.Unix(c.ExpiresAt, 0)
	switch {
	case c.Issuer == "" || c.Issuer != v.Issuer,
		c.Audience == "" || c.Audience != v.Audience,
		c.ID == "",
		c.Method == "" || c.Method != method,
		c.IssuedAt == 0 || c.NotBefore == 0 || c.ExpiresAt == 0,
		// A token may not claim more than the agreed lifetime.
		expires.Sub(issued) > Lifetime || expires.Before(notBefore),
		now.Add(skew).Before(notBefore) || now.Add(skew).Before(issued),
		!now.Add(-skew).Before(expires):
		return Claims{}, ErrInvalid
	}
	return c, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func decode(part string, into any) error {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}
