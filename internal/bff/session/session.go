// Package session implements browser session tokens, cookies and CSRF for the BFF.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	CookieName = "kongken_sid"
	// RecoveryCookiePrefix starts the name of a deletion recovery Cookie. One Cookie exists per
	// recovery generation: its name is the prefix plus the first RecoveryGenerationLength hex
	// characters of the digest of its value, and its value is the random token of the session that
	// began the deletion, which is what the workflow is bound to (by digest). A generation's Cookie
	// is therefore immutable (the same name always carries the same value), and a late response about
	// one generation can neither set, renew nor clear another one. It authenticates nothing.
	RecoveryCookiePrefix = "kongken_del_"
	// RecoveryGenerationLength is the number of hex characters of the digest in a generation's name.
	RecoveryGenerationLength = 16
	// MaxRecoveryCandidates bounds how many recovery Cookies a request may offer; the rest are ignored.
	MaxRecoveryCandidates = 8
	// maxCookiePairs bounds how many Cookie pairs are looked at while collecting candidates.
	maxCookiePairs = 64
	// LegacyRecoveryCookieName is the fixed name an earlier build of this change used for one
	// mutable recovery Cookie. It is still accepted as a candidate (and moved to the generation Cookie
	// by the next response about its deletion) and is never set again.
	LegacyRecoveryCookieName = "kongken_del"
	CSRFCookieName           = "kongken_csrf"
	CSRFHeader               = "X-CSRF-Token"
	tokenPrefix              = "bf_"
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
	// Domain is an explicitly trusted shared DNS domain; empty remains host-only.
	Domain string
}

func (c CookieConfig) session(expires time.Time) *http.Cookie {
	return &http.Cookie{Name: CookieName, Domain: c.Domain, Path: c.Path, HttpOnly: true, Secure: c.Secure, SameSite: c.SameSite, Expires: expires, MaxAge: int(time.Until(expires).Seconds())}
}

// recovery has the session Cookie's scope, HttpOnly, Secure and SameSite rules.
// Its generation-bound name/value remains immutable even when a shared domain is enabled.
func (c CookieConfig) recovery(name string, expires time.Time) *http.Cookie {
	return &http.Cookie{Name: name, Domain: c.Domain, Path: c.Path, HttpOnly: true, Secure: c.Secure, SameSite: c.SameSite, Expires: expires, MaxAge: int(time.Until(expires).Seconds())}
}

func (c CookieConfig) csrf(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{Name: CSRFCookieName, Domain: c.Domain, Value: token, Path: c.Path, HttpOnly: false, Secure: c.Secure, SameSite: c.SameSite, Expires: expires, MaxAge: int(time.Until(expires).Seconds())}
}

func SetSessionCookie(h http.Header, cfg CookieConfig, token string, expires time.Time) {
	c := cfg.session(expires)
	c.Value = token
	addScopedCookie(h, c)
}

// addScopedCookie also expires a same-named host-only Cookie on this response's host.
// Other hosts must be cleared during the documented cutover; a Domain cannot clear them.
func addScopedCookie(h http.Header, c *http.Cookie) {
	if c.Domain != "" {
		hostOnly := *c
		hostOnly.Domain = ""
		hostOnly.Value = ""
		hostOnly.Expires = time.Unix(0, 0)
		hostOnly.MaxAge = -1
		h.Add("Set-Cookie", hostOnly.String())
	}
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
	c := cfg.session(time.Unix(0, 0))
	c.MaxAge = -1
	addScopedCookie(h, c)
}

// RecoveryCookieName is the name of the recovery Cookie of the generation token belongs to.
func RecoveryCookieName(token string) string {
	return RecoveryCookiePrefix + HashToken(token)[:RecoveryGenerationLength]
}

// RecoveryCookie returns the Set-Cookie value that stores token as the recovery credential of its
// generation until expires. The name and value never change for a generation.
func RecoveryCookie(cfg CookieConfig, token string, expires time.Time) string {
	c := cfg.recovery(RecoveryCookieName(token), expires)
	c.Value = token
	return c.String()
}

// SetRecoveryCookie stores token as the recovery credential of its generation until expires.
func SetRecoveryCookie(h http.Header, cfg CookieConfig, token string, expires time.Time) {
	c := cfg.recovery(RecoveryCookieName(token), expires)
	c.Value = token
	addScopedCookie(h, c)
}

func clearedRecovery(cfg CookieConfig, name string) string {
	c := cfg.recovery(name, time.Unix(0, 0))
	c.MaxAge = -1
	c.Value = ""
	return c.String()
}

// ClearRecoveryCookieValue is the Set-Cookie value that removes the recovery Cookie of token's
// generation, and no other.
func ClearRecoveryCookieValue(cfg CookieConfig, token string) string {
	return clearedRecovery(cfg, RecoveryCookieName(token))
}

// ClearRecoveryCookie removes the recovery Cookie of token's generation, and no other.
func ClearRecoveryCookie(h http.Header, cfg CookieConfig, token string) {
	c := cfg.recovery(RecoveryCookieName(token), time.Unix(0, 0))
	c.MaxAge = -1
	addScopedCookie(h, c)
}

// ClearLegacyRecoveryCookie removes the fixed-name recovery Cookie of the earlier build.
func ClearLegacyRecoveryCookie(h http.Header, cfg CookieConfig) {
	c := cfg.recovery(LegacyRecoveryCookieName, time.Unix(0, 0))
	c.MaxAge = -1
	addScopedCookie(h, c)
}

func SetCSRFCookie(h http.Header, cfg CookieConfig, token string, expires time.Time) {
	addScopedCookie(h, cfg.csrf(token, expires))
}

func cookieValue(h http.Header, name string) string {
	var value string
	found := false
	for _, line := range h.Values("Cookie") {
		for part := range strings.SplitSeq(line, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok || k != name {
				continue
			}
			v = strings.TrimSpace(v)
			if found && v != value {
				// Cookie headers omit Domain: never guess between scopes or identities.
				return ""
			}
			value, found = v, true
		}
	}
	return value
}

func TokenFromRequest(h http.Header) string {
	return cookieValue(h, CookieName)
}

// RecoveryTokens are the recovery credentials the browser sent: Cookies named exactly the prefix
// plus RecoveryGenerationLength lowercase hex characters whose value hashes to that very suffix (a
// name that does not belong to its value is ignored, so no Cookie can stand for another generation),
// without duplicates, at most MaxRecoveryCandidates of them, in the order the browser sent them.
// Nothing authenticates with them.
func RecoveryTokens(h http.Header) []string {
	var tokens []string
	seen := 0
	for _, line := range h.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			if seen++; seen > maxCookiePairs || len(tokens) >= MaxRecoveryCandidates {
				return tokens
			}
			name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			value = strings.TrimSpace(value)
			if !ok || value == "" || !validRecoveryName(name) || RecoveryCookieName(value) != name || slices.Contains(tokens, value) {
				continue
			}
			tokens = append(tokens, value)
		}
	}
	return tokens
}

func validRecoveryName(name string) bool {
	suffix, ok := strings.CutPrefix(name, RecoveryCookiePrefix)
	if !ok || len(suffix) != RecoveryGenerationLength {
		return false
	}
	for _, r := range suffix {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// LegacyRecoveryToken is the value of the fixed-name Cookie of the earlier build, or "".
func LegacyRecoveryToken(h http.Header) string {
	return cookieValue(h, LegacyRecoveryCookieName)
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
