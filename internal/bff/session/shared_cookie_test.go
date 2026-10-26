package session_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/kongken/mono/internal/bff/session"
)

func TestSharedCookieDomainAcrossHosts(t *testing.T) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := url.Parse("https://id.example.com/")
	api, _ := url.Parse("https://api.example.com/")
	outside, _ := url.Parse("https://example.org/")
	cfg := session.CookieConfig{Domain: "example.com", Path: "/", Secure: true, SameSite: http.SameSiteLaxMode}
	expires := time.Now().Add(time.Hour)
	apply := func(host *url.URL, h http.Header) {
		t.Helper()
		response := &http.Response{Header: h}
		jar.SetCookies(host, response.Cookies())
	}
	cookie := func(host *url.URL, name string) string {
		t.Helper()
		for _, c := range jar.Cookies(host) {
			if c.Name == name {
				return c.Value
			}
		}
		return ""
	}
	// Existing host-only identity on the current host is expired, not promoted.
	old := http.Header{}
	session.SetSessionCookie(old, session.CookieConfig{Path: "/", Secure: true}, "old", expires)
	apply(id, old)
	h := http.Header{}
	session.SetSessionCookie(h, cfg, "new", expires)
	session.SetCSRFCookie(h, cfg, "csrf", expires)
	session.SetRecoveryCookie(h, cfg, "bf_recovery", expires)
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.MaxAge < 0 {
			if c.Domain != "" {
				t.Fatal("migration expiration must target the current host-only cookie")
			}
			continue
		}
		if c.Domain != "example.com" || !c.Secure || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
			t.Fatalf("incorrect shared cookie attributes: %s", c.Name)
		}
		if c.HttpOnly != (c.Name != session.CSRFCookieName) {
			t.Fatalf("incorrect HttpOnly policy: %s", c.Name)
		}
	}
	apply(id, h)
	for _, host := range []*url.URL{id, api} {
		if cookie(host, session.CookieName) != "new" || cookie(host, session.CSRFCookieName) != "csrf" || cookie(host, session.RecoveryCookieName("bf_recovery")) != "bf_recovery" {
			t.Fatalf("shared credentials missing on %s", host.Host)
		}
	}
	if len(jar.Cookies(outside)) != 0 {
		t.Fatal("shared cookies escaped their domain")
	}
	h = http.Header{}
	session.SetRecoveryCookie(h, cfg, "bf_recovery", expires.Add(time.Hour))
	apply(api, h)
	if cookie(id, session.RecoveryCookieName("bf_recovery")) != "bf_recovery" {
		t.Fatal("recovery renewal changed its immutable value")
	}
	h = http.Header{}
	session.ClearSessionCookie(h, cfg)
	apply(api, h)
	if cookie(id, session.CookieName) != "" || cookie(api, session.CookieName) != "" {
		t.Fatal("logout did not remove the domain session across hosts")
	}
	if cookie(id, session.RecoveryCookieName("bf_recovery")) != "bf_recovery" {
		t.Fatal("logout removed deletion recovery")
	}
	h = http.Header{}
	session.SetRecoveryCookie(h, cfg, "bf_later", expires)
	apply(id, h)
	h = http.Header{}
	session.ClearRecoveryCookie(h, cfg, "bf_recovery")
	apply(id, h)
	if cookie(api, session.RecoveryCookieName("bf_recovery")) != "" {
		t.Fatal("completion did not remove the shared recovery generation")
	}
	if cookie(api, session.RecoveryCookieName("bf_later")) != "bf_later" {
		t.Fatal("a late completion removed another shared recovery generation")
	}
	for _, value := range []string{session.RecoveryCookie(cfg, "bf_later", expires), session.ClearRecoveryCookieValue(cfg, "bf_later"), session.ClearSessionCookieValue(cfg)} {
		response := &http.Response{Header: http.Header{"Set-Cookie": {value}}}
		if cookies := response.Cookies(); len(cookies) != 1 || cookies[0].Domain != cfg.Domain {
			t.Fatal("raw response/error Cookie must use the configured domain too")
		}
	}
}

func TestConflictingCookieScopesFailClosed(t *testing.T) {
	for _, split := range []bool{false, true} {
		h := http.Header{}
		if split {
			h.Add("Cookie", "kongken_sid=old; kongken_csrf=old")
			h.Add("Cookie", "kongken_sid=new; kongken_csrf=new")
		} else {
			h.Set("Cookie", "kongken_sid=old; kongken_sid=new; kongken_csrf=old; kongken_csrf=new")
		}
		h.Set(session.CSRFHeader, "old")
		if session.TokenFromRequest(h) != "" || session.CSRFValid(h) {
			t.Fatal("conflicting scopes selected an arbitrary identity or CSRF token")
		}
	}
	h := http.Header{"Cookie": {"kongken_sid=same; kongken_sid=same"}}
	if session.TokenFromRequest(h) != "same" {
		t.Fatal("identical duplicate session cookies should remain usable")
	}
}

func TestSharedDomainDoesNotAuthorizeSiblingOrigin(t *testing.T) {
	h := http.Header{
		"Cookie": {"kongken_csrf=token"},
		"Origin": {"https://unregistered.example.com"}, "Sec-Fetch-Site": {"same-site"},
	}
	h.Set(session.CSRFHeader, "token")
	if !session.CSRFValid(h) {
		t.Fatal("test needs a valid double-submit token to isolate Origin rejection")
	}
	if session.CheckWrite(h, []string{"https://id.example.com"}, "api.example.com") == nil {
		t.Fatal("a valid shared CSRF cookie must not authorize an unregistered origin")
	}
}
