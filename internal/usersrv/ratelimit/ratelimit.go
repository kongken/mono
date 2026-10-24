// Package ratelimit counts attempts in Redis. A counter is incremented before
// the guarded work starts and every attempt counts, whether it succeeds or not.
// The increment and the expiry are one atomic script, so concurrent requests
// cannot slip under a limit. When Redis cannot answer, the guarded work is not
// allowed: the limiter fails closed.
package ratelimit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrUnavailable is returned when the counter store failed. Callers answer
// UNAVAILABLE and do not proceed.
var ErrUnavailable = errors.New("rate limit store unavailable")

// Rule is "Limit attempts per Window" for one named kind of action.
type Rule struct {
	Name   string
	Limit  int
	Window time.Duration
}

// Limiter keeps counters under a key prefix. Subjects (an account ID, an IP, an
// email) never appear in a key: each is replaced by an HMAC-SHA256 digest under
// a server-side key, so keys neither leak nor can be enumerated from a guess.
type Limiter struct {
	rdb    redis.Scripter
	prefix string
	key    []byte
}

// New returns a limiter. key must be a secret of at least 32 bytes.
func New(rdb redis.Scripter, prefix string, key []byte) (*Limiter, error) {
	if rdb == nil || prefix == "" || len(key) < 32 {
		return nil, errors.New("ratelimit: client, prefix and a 32-byte key are required")
	}
	return &Limiter{rdb: rdb, prefix: prefix, key: append([]byte(nil), key...)}, nil
}

// hit increments the counter and (re)arms the expiry when none is set, so a
// counter can never outlive its window even after a crash between the commands.
var hit = redis.NewScript(`
local c = redis.call('INCR', KEYS[1])
if c == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return c
`)

// Allow counts one attempt by subject (made of one or more parts) against rule
// and reports whether it is within the limit. A non-nil error means the store
// could not answer; the attempt must then be refused as unavailable.
func (l *Limiter) Allow(ctx context.Context, rule Rule, subject ...string) (bool, error) {
	if rule.Limit < 1 || rule.Window < time.Millisecond || len(subject) == 0 {
		return false, fmt.Errorf("ratelimit: invalid rule %q", rule.Name)
	}
	n, err := hit.Run(ctx, l.rdb, []string{l.redisKey(rule, subject)}, rule.Window.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return n <= int64(rule.Limit), nil
}

func (l *Limiter) redisKey(rule Rule, subject []string) string {
	mac := hmac.New(sha256.New, l.key)
	mac.Write([]byte(rule.Name))
	mac.Write([]byte{0})
	mac.Write([]byte(strings.Join(subject, "\x00")))
	return l.prefix + "rl:" + rule.Name + ":" + hex.EncodeToString(mac.Sum(nil))
}
