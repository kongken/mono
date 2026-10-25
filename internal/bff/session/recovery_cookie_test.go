package session

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The deletion recovery Cookie is host-only, HttpOnly, Secure as configured and SameSite=Lax, and the
// two credentials never stand in for each other.
func TestRecoveryCookieAttributes(t *testing.T) {
	for _, secure := range []bool{true, false} {
		cfg := CookieConfig{Secure: secure, SameSite: http.SameSiteLaxMode, Path: "/"}
		set := RecoveryCookie(cfg, "bf_abc", time.Now().Add(time.Hour))
		for _, want := range []string{RecoveryCookieName + "=bf_abc", "HttpOnly", "SameSite=Lax", "Path=/", "Max-Age="} {
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
		cleared := ClearRecoveryCookieValue(cfg)
		if !strings.Contains(cleared, RecoveryCookieName+"=;") && !strings.Contains(cleared, RecoveryCookieName+"=") || !strings.Contains(cleared, "Max-Age=0") {
			t.Errorf("clearing: %q", cleared)
		}
		if strings.Contains(cleared, "Domain=") {
			t.Errorf("clearing must name the same host-only Cookie: %q", cleared)
		}
	}
}

func TestSessionAndRecoveryCookiesAreNotInterchangeable(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", RecoveryCookieName+"=rec; "+CookieName+"=sess")
	if TokenFromRequest(h) != "sess" || RecoveryTokenFromRequest(h) != "rec" {
		t.Fatalf("session=%q recovery=%q", TokenFromRequest(h), RecoveryTokenFromRequest(h))
	}
	only := http.Header{}
	only.Set("Cookie", RecoveryCookieName+"=rec")
	if TokenFromRequest(only) != "" {
		t.Fatal("a recovery Cookie must never be read as a session")
	}
	only.Set("Cookie", CookieName+"=sess")
	if RecoveryTokenFromRequest(only) != "" {
		t.Fatal("a session Cookie must never be read as a recovery credential")
	}
}
