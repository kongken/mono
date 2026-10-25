package session_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kongken/mono/internal/bff/session"
)

func csrfPair(token string) http.Header {
	h := http.Header{}
	h.Set(session.CSRFHeader, token)
	h.Set("Cookie", session.CSRFCookieName+"="+token)
	return h
}

func TestCheckWriteOriginAbsentRejected(t *testing.T) {
	h := csrfPair("tok")
	if err := session.CheckWrite(h, []string{"http://localhost"}, "localhost"); err == nil {
		t.Fatal("missing Origin and Sec-Fetch-Site should fail")
	}
}

func TestCheckWriteSameOriginViaSecFetchSite(t *testing.T) {
	h := csrfPair("tok")
	h.Set("Sec-Fetch-Site", "same-origin")
	if err := session.CheckWrite(h, nil, "localhost"); err != nil {
		t.Fatalf("same-origin fetch: %v", err)
	}
}

func TestCheckWriteAllowedOrigin(t *testing.T) {
	h := csrfPair("tok")
	h.Set("Origin", "https://app.example.com")
	if err := session.CheckWrite(h, []string{"https://app.example.com"}, ""); err != nil {
		t.Fatalf("allowed origin: %v", err)
	}
}

func TestCheckWriteForeignOriginRejected(t *testing.T) {
	h := csrfPair("tok")
	h.Set("Origin", "https://evil.example.com")
	if err := session.CheckWrite(h, []string{"https://app.example.com"}, ""); err == nil {
		t.Fatal("foreign origin should fail")
	}
}

// A sibling subdomain of the same site is not an allowed origin: a Cookie reaching the
// API because the sites match does not make the request pass the Origin check.
func TestCheckWriteSameSiteSiblingRejected(t *testing.T) {
	for _, origin := range []string{
		"https://evil.example.com",         // sibling subdomain
		"https://id.example.com.evil.test", // suffix trick
		"http://id.example.com",            // same host, other scheme
		"https://id.example.com:8443",      // same host, other port
		"https://ID.example.com",           // not an exact byte match
		"null",
	} {
		h := csrfPair("tok")
		h.Set("Origin", origin)
		if err := session.CheckWrite(h, []string{"https://id.example.com"}, "api.example.com"); err == nil {
			t.Errorf("origin %q passed", origin)
		}
	}
}

// The session Cookie is host-only on the API host: no Domain (so it never reaches a
// sibling host), HttpOnly, SameSite=Lax and, in production, Secure. The token is only
// ever in this header, never in a body or URL.
func TestSessionCookieIsHostOnly(t *testing.T) {
	h := http.Header{}
	session.SetSessionCookie(h, session.CookieConfig{Path: "/", SameSite: http.SameSiteLaxMode, Secure: true}, "bf_token", time.Now().Add(time.Hour))
	line := h.Get("Set-Cookie")
	for _, want := range []string{"kongken_sid=bf_token", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q missing from %s", want, line)
		}
	}
	if strings.Contains(strings.ToLower(line), "domain") {
		t.Errorf("session cookie names a Domain: %s", line)
	}
}
