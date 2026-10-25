package ratelimit_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kongken/mono/internal/usersrv/ratelimit"
	"github.com/kongken/mono/internal/usersrv/testutil"
	"github.com/redis/go-redis/v9"
)

var (
	ctx = context.Background()
	key = []byte(strings.Repeat("k", 32))
)

func TestConstructorRequiresSecretAndPrefix(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer rdb.Close()
	if _, err := ratelimit.New(rdb, "p:", []byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := ratelimit.New(rdb, "", key); err == nil {
		t.Fatal("empty prefix accepted")
	}
	if _, err := ratelimit.New(nil, "p:", key); err == nil {
		t.Fatal("nil client accepted")
	}
}

func TestAllowCountsEveryAttemptPerSubject(t *testing.T) {
	rdb, prefix := testutil.Redis(t)
	l, _ := ratelimit.New(rdb, prefix, key)
	rule := ratelimit.Rule{Name: "pw", Limit: 3, Window: time.Minute}
	for i := 1; i <= 5; i++ {
		ok, err := l.Allow(ctx, rule, "user-a")
		if err != nil || ok != (i <= 3) {
			t.Fatalf("attempt %d: ok=%v err=%v", i, ok, err)
		}
	}
	// Another subject and another rule have their own counters.
	if ok, _ := l.Allow(ctx, rule, "user-b"); !ok {
		t.Fatal("subjects share a counter")
	}
	if ok, _ := l.Allow(ctx, ratelimit.Rule{Name: "other", Limit: 3, Window: time.Minute}, "user-a"); !ok {
		t.Fatal("rules share a counter")
	}
	// Different part boundaries are different subjects.
	if ok, _ := l.Allow(ctx, ratelimit.Rule{Name: "join", Limit: 1, Window: time.Minute}, "ab", "c"); !ok {
		t.Fatal()
	}
	if ok, _ := l.Allow(ctx, ratelimit.Rule{Name: "join", Limit: 1, Window: time.Minute}, "a", "bc"); !ok {
		t.Fatal("subject parts are concatenated without a separator")
	}
}

func TestKeysHoldNoSubjectAndAreKeyed(t *testing.T) {
	rdb, prefix := testutil.Redis(t)
	l, _ := ratelimit.New(rdb, prefix, key)
	rule := ratelimit.Rule{Name: "mail", Limit: 5, Window: time.Minute}
	if _, err := l.Allow(ctx, rule, "Alice@Example.com"); err != nil {
		t.Fatal(err)
	}
	keys, err := rdb.Keys(ctx, prefix+"*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("%v %v", keys, err)
	}
	if strings.Contains(strings.ToLower(keys[0]), "alice") || strings.Contains(keys[0], "example") {
		t.Fatalf("subject visible in key %q", keys[0])
	}
	// A different server key gives a different key: it is a keyed digest, not a plain hash.
	other, _ := ratelimit.New(rdb, prefix, []byte(strings.Repeat("z", 32)))
	if _, err = other.Allow(ctx, rule, "Alice@Example.com"); err != nil {
		t.Fatal(err)
	}
	if keys, _ = rdb.Keys(ctx, prefix+"*").Result(); len(keys) != 2 {
		t.Fatalf("digest does not depend on the secret: %v", keys)
	}
}

func TestWindowExpires(t *testing.T) {
	rdb, prefix := testutil.Redis(t)
	l, _ := ratelimit.New(rdb, prefix, key)
	rule := ratelimit.Rule{Name: "short", Limit: 1, Window: 150 * time.Millisecond}
	if ok, _ := l.Allow(ctx, rule, "u"); !ok {
		t.Fatal()
	}
	if ok, _ := l.Allow(ctx, rule, "u"); ok {
		t.Fatal("second attempt allowed")
	}
	time.Sleep(250 * time.Millisecond)
	if ok, _ := l.Allow(ctx, rule, "u"); !ok {
		t.Fatal("the window never reset")
	}
}

func TestCounterWithoutExpiryHeals(t *testing.T) {
	rdb, prefix := testutil.Redis(t)
	l, _ := ratelimit.New(rdb, prefix, key)
	rule := ratelimit.Rule{Name: "heal", Limit: 5, Window: time.Minute}
	if _, err := l.Allow(ctx, rule, "u"); err != nil {
		t.Fatal(err)
	}
	keys, _ := rdb.Keys(ctx, prefix+"*").Result()
	rdb.Persist(ctx, keys[0]) // simulate a crash between INCR and PEXPIRE
	if _, err := l.Allow(ctx, rule, "u"); err != nil {
		t.Fatal(err)
	}
	if ttl := rdb.PTTL(ctx, keys[0]).Val(); ttl <= 0 {
		t.Fatalf("counter would never expire (ttl %v)", ttl)
	}
}

func TestConcurrentAttemptsCannotSlipUnderTheLimit(t *testing.T) {
	rdb, prefix := testutil.Redis(t)
	l, _ := ratelimit.New(rdb, prefix, key)
	rule := ratelimit.Rule{Name: "race", Limit: 10, Window: time.Minute}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := l.Allow(ctx, rule, "same")
			if err != nil {
				t.Error(err)
			}
			if ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("%d of 100 concurrent attempts allowed, limit 10", allowed.Load())
	}
}

func TestRedisFailureFailsClosed(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer rdb.Close()
	l, _ := ratelimit.New(rdb, "p:", key)
	ok, err := l.Allow(ctx, ratelimit.Rule{Name: "x", Limit: 1000, Window: time.Minute}, "u")
	if ok || !errors.Is(err, ratelimit.ErrUnavailable) {
		t.Fatalf("with Redis down: ok=%v err=%v", ok, err)
	}
	if ok, err = l.Allow(ctx, ratelimit.Rule{Name: "x", Limit: 0, Window: time.Minute}, "u"); ok || err == nil {
		t.Fatal("invalid rule accepted")
	}
}
