package notification

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// AllKinds is the kind field of an unsubscribe token that covers every kind.
const AllKinds = "*"

// Unsubscribe identifies what an email unsubscribe link switches off: the email
// channel of one kind, or of every kind.
type Unsubscribe struct {
	UserID string
	// Kind is a Kind value or AllKinds.
	Kind string
}

var errToken = errors.New("invalid unsubscribe token")

func mac(secret []byte, payload string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte("tripmap-unsubscribe-v1\x00"))
	h.Write([]byte(payload))
	return h.Sum(nil)
}

// SignUnsubscribe returns a URL-safe token that proves the server issued the
// link. The token is stateless: unsubscribing is idempotent, so replaying it is
// harmless, and changing the secret revokes every link.
func SignUnsubscribe(secret string, u Unsubscribe) string {
	payload := u.UserID + "|" + u.Kind
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString(mac([]byte(secret), payload))
}

// VerifyUnsubscribe validates a token from SignUnsubscribe.
func VerifyUnsubscribe(secret, token string) (Unsubscribe, error) {
	if secret == "" || len(token) > 512 {
		return Unsubscribe{}, errToken
	}
	head, tail, ok := strings.Cut(token, ".")
	if !ok {
		return Unsubscribe{}, errToken
	}
	enc := base64.RawURLEncoding
	raw, err := enc.DecodeString(head)
	if err != nil {
		return Unsubscribe{}, errToken
	}
	got, err := enc.DecodeString(tail)
	if err != nil || !hmac.Equal(got, mac([]byte(secret), string(raw))) {
		return Unsubscribe{}, errToken
	}
	userID, kind, ok := strings.Cut(string(raw), "|")
	if !ok || userID == "" || (kind != AllKinds && !Kind(kind).Valid()) {
		return Unsubscribe{}, errToken
	}
	return Unsubscribe{UserID: userID, Kind: kind}, nil
}
