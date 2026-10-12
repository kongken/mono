package auth

import (
	"context"
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
