// Package session implements browser session tokens, cookies and CSRF for the BFF.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	CookieName     = "shanhai_sid"
	CSRFCookieName = "shanhai_csrf"
	CSRFHeader     = "X-CSRF-Token"
	tokenPrefix    = "bf_"
)

type browserKey struct{}

// BrowserSession is the authenticated browser caller on a protected RPC.
type BrowserSession struct {
	UserID          string
	SessionID       string
	DeletionPending bool
}

func WithBrowser(ctx context.Context, s BrowserSession) context.Context {
	return context.WithValue(ctx, browserKey{}, s)
}

func FromBrowser(ctx context.Context) (BrowserSession, bool) {
	s, ok := ctx.Value(browserKey{}).(BrowserSession)
	return s, ok && s.UserID != ""
}

func NewToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	token = tokenPrefix + hex.EncodeToString(buf)
	return token, HashToken(token), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func NewCSRF() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type CookieConfig struct {
	Secure   bool
	SameSite http.SameSite
	Path     string
}

func (c CookieConfig) session(expires time.Time) *http.Cookie {
	return &http.Cookie{Name: CookieName, Path: c.Path, HttpOnly: true, Secure: c.Secure, SameSite: c.SameSite, Expires: expires, MaxAge: int(time.Until(expires).Seconds())}
}

func (c CookieConfig) csrf(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{Name: CSRFCookieName, Value: token, Path: c.Path, HttpOnly: false, Secure: c.Secure, SameSite: c.SameSite, Expires: expires, MaxAge: int(time.Until(expires).Seconds())}
}

func SetSessionCookie(h http.Header, cfg CookieConfig, token string, expires time.Time) {
	c := cfg.session(expires)
	c.Value = token
	h.Add("Set-Cookie", c.String())
}

func ClearSessionCookie(h http.Header, cfg CookieConfig) {
	c := cfg.session(time.Unix(0, 0))
	c.MaxAge = -1
	c.Value = ""
	h.Add("Set-Cookie", c.String())
}

func SetCSRFCookie(h http.Header, cfg CookieConfig, token string, expires time.Time) {
	h.Add("Set-Cookie", cfg.csrf(token, expires).String())
}

func cookieValue(h http.Header, name string) string {
	for _, part := range strings.Split(h.Get("Cookie"), ";") {
		part = strings.TrimSpace(part)
		k, v, ok := strings.Cut(part, "=")
		if ok && k == name {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func TokenFromRequest(h http.Header) string {
	return cookieValue(h, CookieName)
}

func CSRFValid(h http.Header) bool {
	header := strings.TrimSpace(h.Get(CSRFHeader))
	cookie := cookieValue(h, CSRFCookieName)
	return header != "" && header == cookie
}

var ErrCSRF = errors.New("invalid or missing csrf token")

// CheckWrite validates Origin (when present) and CSRF for state-changing calls.
func CheckWrite(h http.Header, allowedOrigins []string, host string) error {
	if !CSRFValid(h) {
		return ErrCSRF
	}
	origin := strings.TrimSpace(h.Get("Origin"))
	if origin == "" {
		return nil
	}
	for _, o := range allowedOrigins {
		if o == origin {
			return nil
		}
	}
	// Same-origin Connect calls from the dev proxy often omit Origin; allow when Host matches.
	if host != "" && (strings.HasPrefix(origin, "http://"+host) || strings.HasPrefix(origin, "https://"+host)) {
		return nil
	}
	return errors.New("origin not allowed")
}
