package ratelimit

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter struct {
	rdb    redis.Cmdable
	prefix string
}

func New(rdb redis.Cmdable, prefix string) *Limiter { return &Limiter{rdb: rdb, prefix: prefix} }

var incrWindow = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return count`)

func (l *Limiter) Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	count, err := incrWindow.Run(ctx, l.rdb, []string{l.prefix + "rl:" + key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return count <= limit, nil
}

func (l *Limiter) Count(ctx context.Context, key string) (int64, error) {
	n, err := l.rdb.Get(ctx, l.prefix+"rl:"+key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}
