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
	CookieName = "kongken_sid"
	// RecoveryCookieName holds the credential to see and continue an account deletion, and nothing
	// else: it never authenticates a request. Its value is the random token of the session that
	// began the deletion, which is what the deletion workflow is bound to (by digest).
	RecoveryCookieName = "kongken_del"
	CSRFCookieName     = "kongken_csrf"
	CSRFHeader         = "X-CSRF-Token"
	tokenPrefix        = "bf_"
)

type browserKey struct{}

// BrowserSession is the authenticated browser caller on a protected RPC. It is
// built from the session row and the account state usersrv reported for this very
// request, so Roles are the account's current roles, not a stored snapshot.
type BrowserSession struct {
	UserID    string
	SessionID string
	// AuthVersion is the account's auth_version, equal to the one the session was
	// opened with (a session at any other version is never returned).
	AuthVersion int64
	// ProfileVersion is the account's profile_version, for products that keep a
	// display projection.
	ProfileVersion int64
	// Roles are the account's current roles at this request.
	Roles []string
	// AuthenticatedAt is when the password was last really checked on this
	// session: sign-in or Reauthenticate. Cookie refreshes never move it.
	AuthenticatedAt time.Time
	// ExpiresAt is the session's fixed expiry.
	ExpiresAt time.Time
}

// HasRole reports whether the account holds role.
func (s BrowserSession) HasRole(role string) bool {
	for _, r := range s.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// SensitiveAuthMaxAge is how recently the password must have been checked for a
// sensitive action: deleting the account or creating a management token.
const SensitiveAuthMaxAge = 15 * time.Minute

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

// recovery is host-only (no Domain), HttpOnly, Secure as configured, with the session Cookie's
// SameSite and Path: a browser sends it to the API host and to nothing else.
func (c CookieConfig) recovery(expires time.Time) *http.Cookie {
	return &http.Cookie{Name: RecoveryCookieName, Path: c.Path, HttpOnly: true, Secure: c.Secure, SameSite: c.SameSite, Expires: expires, MaxAge: int(time.Until(expires).Seconds())}
}

func (c CookieConfig) csrf(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{Name: CSRFCookieName, Value: token, Path: c.Path, HttpOnly: false, Secure: c.Secure, SameSite: c.SameSite, Expires: expires, MaxAge: int(time.Until(expires).Seconds())}
}

func SetSessionCookie(h http.Header, cfg CookieConfig, token string, expires time.Time) {
	c := cfg.session(expires)
	c.Value = token
	h.Add("Set-Cookie", c.String())
}

// ClearSessionCookieValue is the Set-Cookie value that removes the session Cookie.
func ClearSessionCookieValue(cfg CookieConfig) string {
	c := cfg.session(time.Unix(0, 0))
	c.MaxAge = -1
	c.Value = ""
	return c.String()
}

func ClearSessionCookie(h http.Header, cfg CookieConfig) {
	h.Add("Set-Cookie", ClearSessionCookieValue(cfg))
}

// RecoveryCookie returns the Set-Cookie value that stores token as the deletion recovery
// credential until expires.
func RecoveryCookie(cfg CookieConfig, token string, expires time.Time) string {
	c := cfg.recovery(expires)
	c.Value = token
	return c.String()
}

// SetRecoveryCookie stores token as the deletion recovery credential until expires.
func SetRecoveryCookie(h http.Header, cfg CookieConfig, token string, expires time.Time) {
	h.Add("Set-Cookie", RecoveryCookie(cfg, token, expires))
}

// ClearRecoveryCookieValue is the Set-Cookie value that removes the recovery credential.
func ClearRecoveryCookieValue(cfg CookieConfig) string {
	c := cfg.recovery(time.Unix(0, 0))
	c.MaxAge = -1
	c.Value = ""
	return c.String()
}

// ClearRecoveryCookie removes the deletion recovery credential.
func ClearRecoveryCookie(h http.Header, cfg CookieConfig) {
	h.Add("Set-Cookie", ClearRecoveryCookieValue(cfg))
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

// RecoveryTokenFromRequest is the deletion recovery credential the browser sent, or "".
// Nothing authenticates with it.
func RecoveryTokenFromRequest(h http.Header) string {
	return cookieValue(h, RecoveryCookieName)
}

func CSRFValid(h http.Header) bool {
	header := strings.TrimSpace(h.Get(CSRFHeader))
	cookie := cookieValue(h, CSRFCookieName)
	return header != "" && header == cookie
}

var ErrCSRF = errors.New("invalid or missing csrf token")

// CheckWrite validates CSRF and that the call looks same-origin (Origin header,
// configured allowlist, Host match, or Sec-Fetch-Site: same-origin from browsers).
func CheckWrite(h http.Header, allowedOrigins []string, host string) error {
	if !CSRFValid(h) {
		return ErrCSRF
	}
	if originAllowed(h, allowedOrigins, host) {
		return nil
	}
	return errors.New("origin not allowed")
}

func originAllowed(h http.Header, allowedOrigins []string, host string) bool {
	origin := strings.TrimSpace(h.Get("Origin"))
	if origin != "" {
		for _, o := range allowedOrigins {
			if o == origin {
				return true
			}
		}
		if host != "" && (origin == "http://"+host || origin == "https://"+host) {
			return true
		}
		return false
	}
	return strings.EqualFold(strings.TrimSpace(h.Get("Sec-Fetch-Site")), "same-origin")
}
