package passwordhash

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fast keeps unit tests quick; the defaults are checked separately.
func fast(t testing.TB, mutate ...func(*Config)) *Hasher {
	t.Helper()
	cfg := Config{MemoryKiB: 64, Iterations: 1, Parallelism: 1}
	for _, m := range mutate {
		m(&cfg)
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHashVerifyRoundTrip(t *testing.T) {
	h := fast(t)
	ctx := context.Background()
	a, err := h.Hash(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := h.Hash(ctx, "correct horse battery staple")
	if a == b {
		t.Fatal("two hashes of one password share a salt")
	}
	if !strings.HasPrefix(a, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("encoding does not describe its parameters: %s", a)
	}
	if strings.Contains(a, "correct horse") {
		t.Fatal("hash contains the password")
	}
	for _, enc := range []string{a, b} {
		if ok, err := h.Verify(ctx, enc, "correct horse battery staple"); !ok || err != nil {
			t.Fatalf("right password: %v %v", ok, err)
		}
		if ok, err := h.Verify(ctx, enc, "correct horse battery stapl"); ok || err != nil {
			t.Fatalf("wrong password: %v %v", ok, err)
		}
	}
	// Spaces, case and non-ASCII are kept verbatim: no trim, no normalization.
	for _, pw := range []string{" spaced password ", "ÄÖÜ-password-12", "密码密码密码密码密码密码"} {
		enc, err := h.Hash(ctx, pw)
		if err != nil {
			t.Fatal(err)
		}
		if ok, _ := h.Verify(ctx, enc, pw); !ok {
			t.Fatalf("%q does not verify", pw)
		}
		if ok, _ := h.Verify(ctx, enc, strings.TrimSpace(pw)+"x"); ok {
			t.Fatalf("%q verifies a different password", pw)
		}
	}
	if ok, _ := h.Verify(ctx, a, " correct horse battery staple"); ok {
		t.Fatal("password was trimmed")
	}
}

func TestVerifyUsesTheParametersOfTheStoredHash(t *testing.T) {
	old := fast(t, func(c *Config) { c.MemoryKiB = 32; c.Iterations = 2 })
	enc, err := old.Hash(context.Background(), "password-from-before")
	if err != nil {
		t.Fatal(err)
	}
	current := fast(t)
	if ok, err := current.Verify(context.Background(), enc, "password-from-before"); !ok || err != nil {
		t.Fatalf("hash made under other parameters: %v %v", ok, err)
	}
}

func TestMalformedAndHostileEncodingsAreRefused(t *testing.T) {
	h := fast(t)
	for _, enc := range []string{
		"", "plain", "$argon2i$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=18$m=64,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=4294967295,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=4000000,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=1,p=0$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=1,p=1$!!!$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$",
		"$argon2id$v=19$m=64,t=1,p=1$c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5",
	} {
		if ok, err := h.Verify(context.Background(), enc, "whatever-password"); ok || !errors.Is(err, ErrInvalidHash) {
			t.Errorf("%q: %v %v", enc, ok, err)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{MemoryKiB: 4, Iterations: 1, Parallelism: 1},
		{MemoryKiB: 64, Iterations: 0, Parallelism: 1},
		{MemoryKiB: 64, Iterations: 1, Parallelism: 0},
		{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8},
		{MemoryKiB: 64, Iterations: 1, Parallelism: 1, KeyBytes: 8},
		{MemoryKiB: 64, Iterations: 1, Parallelism: 1, MaxConcurrent: 1000},
		{MemoryKiB: 1 << 30, Iterations: 1, Parallelism: 1},
	}
	for i, c := range bad {
		if _, err := New(c); err == nil {
			t.Errorf("config %d accepted: %+v", i, c)
		}
	}
	if d := DefaultConfig(); d.MemoryKiB != 64*1024 || d.Iterations != 3 || d.Parallelism != 1 || d.SaltBytes != 16 || d.KeyBytes != 32 || d.MaxConcurrent != 2 {
		t.Fatalf("defaults drifted from the specification: %+v", d)
	}
}

func TestConcurrencyIsBoundedAndWaitIsBounded(t *testing.T) {
	h := fast(t, func(c *Config) { c.MaxConcurrent = 2; c.WaitTimeout = 50 * time.Millisecond })
	// Occupy both slots as running hashes would.
	h.slots <- struct{}{}
	h.slots <- struct{}{}
	start := time.Now()
	if _, err := h.Hash(context.Background(), "some-long-password"); !errors.Is(err, ErrBusy) {
		t.Fatalf("hash with no free slot: %v", err)
	}
	if d := time.Since(start); d < 40*time.Millisecond || d > time.Second {
		t.Fatalf("wait took %s, want about the 50ms limit", d)
	}
	enc, _ := fast(t).Hash(context.Background(), "some-long-password")
	if _, err := h.Verify(context.Background(), enc, "x"); !errors.Is(err, ErrBusy) {
		t.Fatalf("verify with no free slot: %v", err)
	}
	// The request deadline ends the wait before the timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	h2 := fast(t, func(c *Config) { c.MaxConcurrent = 1; c.WaitTimeout = 5 * time.Second })
	h2.slots <- struct{}{}
	start = time.Now()
	if err := h2.VerifyDummy(ctx, "x"); !errors.Is(err, ErrBusy) || time.Since(start) > time.Second {
		t.Fatalf("deadline ignored: %v after %s", err, time.Since(start))
	}
	<-h.slots
	<-h.slots
	if _, err := h.Hash(context.Background(), "some-long-password"); err != nil {
		t.Fatalf("free slots: %v", err)
	}
}

func TestNoMoreThanMaxConcurrentHashesRunAtOnce(t *testing.T) {
	// Real work with a measurable footprint: count how many hashes are in flight.
	h := fast(t, func(c *Config) {
		c.MemoryKiB = 4096
		c.Iterations = 2
		c.MaxConcurrent = 2
		c.WaitTimeout = 30 * time.Second
	})
	var inflight, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.acquire(context.Background()); err != nil {
				t.Error(err)
				return
			}
			n := inflight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			_, _ = h.compute("password-password")
			inflight.Add(-1)
			h.release()
		}()
	}
	wg.Wait()
	if peak.Load() > 2 {
		t.Fatalf("%d hashes ran at once, limit is 2", peak.Load())
	}
}

func TestDummyVerificationCostsAVerification(t *testing.T) {
	h := fast(t, func(c *Config) { c.MemoryKiB = 16 * 1024; c.Iterations = 2 })
	ctx := context.Background()
	enc, _ := h.Hash(ctx, "password-password")
	measure := func(f func()) time.Duration {
		start := time.Now()
		for i := 0; i < 5; i++ {
			f()
		}
		return time.Since(start)
	}
	real := measure(func() { _, _ = h.Verify(ctx, enc, "wrong-wrong-wrong") })
	dummy := measure(func() { _ = h.VerifyDummy(ctx, "wrong-wrong-wrong") })
	if dummy < real/3 {
		t.Fatalf("the dummy check is a fast path: %s against %s", dummy, real)
	}
}

// TestDefaultCostIsMeasured logs the real cost of the default parameters so the
// resource budget (64 MiB per hash, two at once) can be checked on the target
// host. It is a measurement, not a limit.
func TestDefaultCostIsMeasured(t *testing.T) {
	if testing.Short() {
		t.Skip("measurement")
	}
	h, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	enc, err := h.Hash(context.Background(), "a-twelve-char-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("default Argon2id (m=64MiB t=3 p=1): hash %s", time.Since(start))
	start = time.Now()
	if ok, _ := h.Verify(context.Background(), enc, "a-twelve-char-password"); !ok {
		t.Fatal("default hash does not verify")
	}
	t.Logf("verify %s", time.Since(start))
}
