package session_test

import (
	"net/http"
	"testing"

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
