package session

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A recovery Cookie is host-only, HttpOnly, Secure as configured and SameSite=Lax; its name belongs to
// its value (one Cookie per generation) and never changes for that value.
func TestRecoveryCookieAttributesAndGenerationName(t *testing.T) {
	for _, secure := range []bool{true, false} {
		cfg := CookieConfig{Secure: secure, SameSite: http.SameSiteLaxMode, Path: "/"}
		set := RecoveryCookie(cfg, "bf_abc", time.Now().Add(time.Hour))
		name := RecoveryCookieName("bf_abc")
		if len(name) != len(RecoveryCookiePrefix)+RecoveryGenerationLength || !strings.HasPrefix(name, RecoveryCookiePrefix) {
			t.Fatalf("name %q", name)
		}
		for _, want := range []string{name + "=bf_abc", "HttpOnly", "SameSite=Lax", "Path=/", "Max-Age="} {
			if !strings.Contains(set, want) {
				t.Errorf("secure=%v: %q lacks %q", secure, set, want)
			}
		}
		if strings.Contains(set, "Domain=") {
			t.Errorf("a recovery Cookie must be host-only: %q", set)
		}
		if strings.Contains(set, "Secure") != secure {
			t.Errorf("secure=%v: %q", secure, set)
		}
		// The same generation always produces the same name and value, whenever it is set.
		again := RecoveryCookie(cfg, "bf_abc", time.Now().Add(2*time.Hour))
		if strings.SplitN(again, ";", 2)[0] != strings.SplitN(set, ";", 2)[0] {
			t.Errorf("a generation's name and value changed: %q vs %q", again, set)
		}
		// Another generation has another name, so clearing or setting one cannot touch the other.
		if RecoveryCookieName("bf_abd") == name {
			t.Fatal("two generations share a name")
		}
		cleared := ClearRecoveryCookieValue(cfg, "bf_abc")
		if !strings.HasPrefix(cleared, name+"=;") || !strings.Contains(cleared, "Max-Age=0") || strings.Contains(cleared, "Domain=") {
			t.Errorf("clearing: %q", cleared)
		}
	}
}

func TestSessionAndRecoveryCookiesAreNotInterchangeable(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", RecoveryCookieName("rec")+"=rec; "+CookieName+"=sess")
	if TokenFromRequest(h) != "sess" || len(RecoveryTokens(h)) != 1 || RecoveryTokens(h)[0] != "rec" {
		t.Fatalf("session=%q recovery=%v", TokenFromRequest(h), RecoveryTokens(h))
	}
	only := http.Header{}
	only.Set("Cookie", RecoveryCookieName("rec")+"=rec")
	if TokenFromRequest(only) != "" {
		t.Fatal("a recovery Cookie must never be read as a session")
	}
	only.Set("Cookie", CookieName+"=sess")
	if len(RecoveryTokens(only)) != 0 {
		t.Fatal("a session Cookie must never be read as a recovery credential")
	}
}

// Only well-formed Cookies whose name belongs to their value are candidates, there are no duplicates,
// and no more than MaxRecoveryCandidates are taken.
func TestRecoveryTokensAreBoundedAndBoundToTheirNames(t *testing.T) {
	good := RecoveryCookieName("bf_one") + "=bf_one"
	h := http.Header{}
	h.Set("Cookie", strings.Join([]string{
		good,
		good,                                     // a duplicate
		RecoveryCookieName("bf_one") + "=bf_two", // the name of one generation on the value of another
		RecoveryCookiePrefix + "ZZZZZZZZZZZZZZZZ=bf_two", // not hex
		RecoveryCookiePrefix + "0123=bf_two",             // too short
		RecoveryCookieName("bf_two") + "x=bf_two",        // too long
		RecoveryCookiePrefix + strings.ToUpper(RecoveryCookieName("bf_two")[len(RecoveryCookiePrefix):]) + "=bf_two", // not lower case
		RecoveryCookieName("bf_three") + "=",    // empty
		LegacyRecoveryCookieName + "=bf_legacy", // the old fixed name is not a generation Cookie
		RecoveryCookieName("bf_four") + "=bf_four",
	}, "; "))
	got := RecoveryTokens(h)
	if len(got) != 2 || got[0] != "bf_one" || got[1] != "bf_four" {
		t.Fatalf("candidates %v", got)
	}
	if LegacyRecoveryToken(h) != "bf_legacy" {
		t.Fatalf("legacy %q", LegacyRecoveryToken(h))
	}

	many := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		tok := fmt.Sprintf("bf_%d", i)
		many = append(many, RecoveryCookieName(tok)+"="+tok)
	}
	h.Set("Cookie", strings.Join(many, "; "))
	if got := RecoveryTokens(h); len(got) != MaxRecoveryCandidates || got[0] != "bf_0" {
		t.Fatalf("%d candidates, first %q", len(got), got[0])
	}
	// A flood of unrelated pairs in front does not push the scan past its bound.
	noise := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		noise = append(noise, fmt.Sprintf("n%d=x", i))
	}
	h.Set("Cookie", strings.Join(noise, "; ")+"; "+good)
	if got := RecoveryTokens(h); len(got) != 0 {
		t.Fatalf("scanned beyond the bound: %v", got)
	}
}
