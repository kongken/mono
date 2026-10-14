package auth

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter is a fixed-window counter in Redis.
type Limiter struct {
	rdb    redis.Cmdable
	prefix string
}

func NewLimiter(rdb redis.Cmdable, prefix string) *Limiter { return &Limiter{rdb: rdb, prefix: prefix} }

var incrWindow = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return count`)

// Allow counts one event for key and reports whether it is within limit for
// the current window. A Redis error is returned to the caller, who decides
// whether to fail open or closed.
func (l *Limiter) Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	count, err := incrWindow.Run(ctx, l.rdb, []string{l.prefix + "rl:" + key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return count <= limit, nil
}

var incrByWindow = redis.NewScript(`
local count = redis.call('INCRBY', KEYS[1], ARGV[2])
if count == tonumber(ARGV[2]) then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return count`)

// AllowN counts n events at once, so a batch is charged in a single round trip.
// It reports whether the whole batch fits in the current window; a batch larger
// than limit is never allowed.
func (l *Limiter) AllowN(ctx context.Context, key string, n, limit int64, window time.Duration) (bool, error) {
	count, err := incrByWindow.Run(ctx, l.rdb, []string{l.prefix + "rl:" + key}, window.Milliseconds(), n).Int64()
	if err != nil {
		return false, err
	}
	return count <= limit, nil
}

// Count returns how many events key has recorded in the current window without
// counting a new one.
func (l *Limiter) Count(ctx context.Context, key string) (int64, error) {
	n, err := l.rdb.Get(ctx, l.prefix+"rl:"+key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}
