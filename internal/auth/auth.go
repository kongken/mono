// Package auth holds the pieces shared by the RPC services and the
// authentication interceptor: the request principal, session tokens, and the
// Redis-backed rate limiter and OAuth state store.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// Principal identifies the authenticated caller of a request.
type Principal struct {
	UserID    string
	SessionID string
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok && p.UserID != ""
}

const tokenPrefix = "tm_"

// NewToken returns a random bearer token and its storage hash.
func NewToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	token = tokenPrefix + hex.EncodeToString(buf)
	return token, HashToken(token), nil
}

// HashToken is the SHA-256 hex digest stored instead of the token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
