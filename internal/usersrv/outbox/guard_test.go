package outbox

import (
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

var guardKey = []byte(strings.Repeat("g", 32))

func rule(name string, limit int, window time.Duration) ratelimit.Rule {
	return ratelimit.Rule{Name: name, Limit: limit, Window: window}
}

// defaultRules are the spec's section 10 defaults.
func defaultRules() ratelimit.MailRules {
	return ratelimit.MailRules{
		RegistrationPerIP:         rule("registration_mail_per_ip", 20, time.Hour),
		ResetPerIP:                rule("reset_mail_per_ip", 20, time.Hour),
		RegistrationEmailCooldown: rule("registration_mail_cooldown", 1, time.Minute),
		RegistrationPerEmail:      rule("registration_mail_per_email", 5, time.Hour),
		ResetEmailCooldown:        rule("reset_mail_cooldown", 1, time.Minute),
		ResetPerEmail:             rule("reset_mail_per_email", 5, time.Hour),
		MailPerEmail:              rule("mail_per_email", 8, time.Hour),
		MailGlobal:                rule("mail_global", 200, time.Hour),
	}
}

func newGuard(t *testing.T, rules ratelimit.MailRules) (*Guard, redis.UniversalClient, string) {
	t.Helper()
	rdb, prefix := testutil.Redis(t)
	l, err := ratelimit.New(rdb, prefix, guardKey)
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewGuard(l, rules)
	if err != nil {
		t.Fatal(err)
	}
	return g, rdb, prefix
}

func TestCooldownAndHourlyCapsSuppressWithoutLimiting(t *testing.T) {
	g, _, _ := newGuard(t, defaultRules())
	// First request goes through; an immediate repeat is inside the 60 second cooldown.
	if d, err := g.Check(ctx, PurposeRegistration, "alice@example.com", "198.51.100.1"); err != nil || d != Allow {
		t.Fatalf("%v %v", d, err)
	}
	if d, _ := g.Check(ctx, PurposeRegistration, "alice@example.com", "198.51.100.1"); d != Suppress {
		t.Fatalf("cooldown: %v", d)
	}
	// Another mailbox from the same address is unaffected.
	if d, _ := g.Check(ctx, PurposeRegistration, "bob@example.com", "198.51.100.1"); d != Allow {
		t.Fatalf("other mailbox: %v", d)
	}
	// The registration and reset cooldowns are independent: the same mailbox may request a reset.
	if d, _ := g.Check(ctx, PurposePasswordReset, "alice@example.com", "198.51.100.1"); d != Allow {
		t.Fatalf("reset after registration: %v", d)
	}
}

func TestPerMailboxHourlyCapsAndTotal(t *testing.T) {
	rules := defaultRules()
	rules.RegistrationEmailCooldown = rule("registration_mail_cooldown", 1000, time.Hour) // isolate the caps
	rules.ResetEmailCooldown = rule("reset_mail_cooldown", 1000, time.Hour)
	g, _, _ := newGuard(t, rules)
	allowed := func(purpose string) int {
		n := 0
		for i := 0; i < 20; i++ {
			d, err := g.Check(ctx, purpose, "alice@example.com", "")
			if err != nil {
				t.Fatal(err)
			}
			if d == Allow {
				n++
			} else if d != Suppress {
				t.Fatalf("a mailbox limit must suppress, got %v", d)
			}
		}
		return n
	}
	// 5 per hour per purpose...
	if n := allowed(PurposeRegistration); n != 5 {
		t.Fatalf("registration allowed %d, want 5", n)
	}
	// ...and 8 per hour over all mail: 3 more resets fit, although the reset cap is 5.
	if n := allowed(PurposePasswordReset); n != 3 {
		t.Fatalf("reset allowed %d, want 3 (8 total)", n)
	}
}

func TestPublicLimitsRefuseAndAreCountedForUnknownMailboxesToo(t *testing.T) {
	rules := defaultRules()
	rules.RegistrationPerIP = rule("registration_mail_per_ip", 3, time.Hour)
	g, _, _ := newGuard(t, rules)
	// Each attempt uses a different (possibly non-existing) mailbox: the client address still runs out.
	for i := 0; i < 3; i++ {
		if d, err := g.Check(ctx, PurposeRegistration, string(rune('a'+i))+"@example.com", "203.0.113.9"); err != nil || d != Allow {
			t.Fatalf("%d: %v %v", i, d, err)
		}
	}
	if d, _ := g.Check(ctx, PurposeRegistration, "z@example.com", "203.0.113.9"); d != Limited {
		t.Fatalf("per-IP limit: %v", d)
	}
	// Another client address is unaffected; the reset limit of the first address is separate.
	if d, _ := g.Check(ctx, PurposeRegistration, "y@example.com", "203.0.113.10"); d != Allow {
		t.Fatalf("other client: %v", d)
	}
	if d, _ := g.Check(ctx, PurposePasswordReset, "x@example.com", "203.0.113.9"); d != Allow {
		t.Fatalf("reset limit is separate: %v", d)
	}
}

func TestGlobalLimit(t *testing.T) {
	rules := defaultRules()
	rules.MailGlobal = rule("mail_global", 4, time.Hour)
	g, _, _ := newGuard(t, rules)
	for i := 0; i < 4; i++ {
		if d, _ := g.Check(ctx, PurposePasswordReset, string(rune('a'+i))+"@example.com", string(rune('0'+i))+".1.1.1"); d != Allow {
			t.Fatalf("%d: %v", i, d)
		}
	}
	if d, _ := g.Check(ctx, PurposePasswordReset, "q@example.com", "9.9.9.9"); d != Limited {
		t.Fatalf("global cap: %v", d)
	}
}

func TestConcurrentRequestsCannotSlipUnderALimit(t *testing.T) {
	rules := defaultRules()
	rules.RegistrationEmailCooldown = rule("registration_mail_cooldown", 1, time.Hour)
	g, _, _ := newGuard(t, rules)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d, err := g.Check(ctx, PurposeRegistration, "race@example.com", ""); err == nil && d == Allow {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("%d concurrent requests passed a cooldown of one", allowed.Load())
	}

	rules = defaultRules()
	rules.RegistrationPerIP = rule("registration_mail_per_ip", 7, time.Hour)
	g2, _, _ := newGuard(t, rules)
	allowed.Store(0)
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			email := strings.Repeat(string(rune('a'+i%26)), 3) + string(rune('a'+i/26)) + "@example.com"
			if d, err := g2.Check(ctx, PurposeRegistration, email, "192.0.2.1"); err == nil && d == Allow {
				allowed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if allowed.Load() != 7 {
		t.Fatalf("%d concurrent requests passed a per-IP limit of seven", allowed.Load())
	}
}

func TestRedisKeysHoldNoAddressOrIP(t *testing.T) {
	g, rdb, prefix := newGuard(t, defaultRules())
	if _, err := g.Check(ctx, PurposeRegistration, "alice@example.com", "198.51.100.77"); err != nil {
		t.Fatal(err)
	}
	keys, err := rdb.Keys(ctx, prefix+"*").Result()
	if err != nil || len(keys) != 5 {
		t.Fatalf("%v %v", keys, err)
	}
	for _, k := range keys {
		for _, leak := range []string{"alice", "example", "198.51", "@"} {
			if strings.Contains(k, leak) {
				t.Fatalf("key %q leaks %q", k, leak)
			}
		}
	}
	// The digest depends on the server-side key (an HMAC): a guessed address cannot be looked up.
	other, _ := ratelimit.New(rdb, prefix, []byte(strings.Repeat("z", 32)))
	g2, _ := NewGuard(other, defaultRules())
	if d, _ := g2.Check(ctx, PurposeRegistration, "alice@example.com", "198.51.100.77"); d != Allow {
		t.Fatalf("a different HMAC key must not share counters: %v", d)
	}
}

func TestRedisOutageNeverFallsBackToSending(t *testing.T) {
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = dead.Close() })
	l, err := ratelimit.New(dead, "usersrv_test:", guardKey)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := NewGuard(l, defaultRules())
	for _, purpose := range []string{PurposeRegistration, PurposePasswordReset} {
		d, err := g.Check(ctx, purpose, "alice@example.com", "198.51.100.1")
		if !errors.Is(err, ratelimit.ErrUnavailable) || d == Allow {
			t.Fatalf("%s: %v %v", purpose, d, err)
		}
	}
	if _, err = g.Check(ctx, "unknown", "a@example.com", ""); err == nil {
		t.Fatal("unknown purpose accepted")
	}
}
