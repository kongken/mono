package mailer

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Kind is the fixed template type of an account mail.
type Kind string

const (
	// KindVerifyEmail is the registration mail: it confirms ownership of the address.
	KindVerifyEmail Kind = "verify_email"
	// KindResetPassword is the password reset mail.
	KindResetPassword Kind = "reset_password"
)

// Valid reports whether k is a known template type.
func (k Kind) Valid() bool { return k == KindVerifyEmail || k == KindResetPassword }

// CentralApp is the app ID whose origin every new account mail links to: the OzID
// user center. The source app of a request never selects the link origin.
const CentralApp = "ozid"

// Fixed paths of the landing pages in each app. The token goes into the URL
// fragment, so it never reaches a server log, a Referer header or a proxy.
const (
	VerifyEmailPath   = "/account/verify-email"
	ResetPasswordPath = "/account/reset-password"
)

func (k Kind) path() string {
	if k == KindVerifyEmail {
		return VerifyEmailPath
	}
	return ResetPasswordPath
}

// tokenPattern is the URL-safe base64 of 32 random bytes, without padding.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// ValidateOrigin accepts an exact https origin, or an http loopback origin when
// development mode is explicitly allowed. No path, query, fragment or userinfo.
func ValidateOrigin(origin string, allowLoopbackHTTP bool) error {
	if origin == "" {
		return errors.New("is required")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		strings.ContainsAny(origin, " \t\r\n") || strings.HasSuffix(origin, "/") {
		return errors.New("must be an exact origin such as https://example.com")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if allowLoopbackHTTP && IsLoopbackHost(u.Hostname()) {
			return nil
		}
		return errors.New("http is only allowed for loopback hosts with auth.allow_loopback_http")
	}
	return errors.New("must use https")
}

// IsLoopbackHost reports "localhost" or a loopback IP literal.
func IsLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Links builds the verification links. A link is the exact configured origin of
// the app, one of two fixed paths and the token in the fragment. New mails are always
// built for CentralApp; other configured origins only serve the mails that were
// issued (and queued) before the central landing existed.
type Links struct {
	origins map[string]string
}

// NewLinks validates the app origins and returns the builder.
func NewLinks(origins map[string]string, allowLoopbackHTTP bool) (*Links, error) {
	if len(origins) == 0 {
		return nil, errors.New("mailer: no app origins configured")
	}
	l := &Links{origins: make(map[string]string, len(origins))}
	for id, origin := range origins {
		if err := ValidateOrigin(origin, allowLoopbackHTTP); err != nil {
			return nil, fmt.Errorf("mailer: app %q origin: %w", id, err)
		}
		l.origins[id] = origin
	}
	return l, nil
}

// Has reports whether appID is configured.
func (l *Links) Has(appID string) bool {
	_, ok := l.origins[appID]
	return ok
}

// ErrUnknownApp is returned for an app ID that is not in the allow list.
var ErrUnknownApp = errors.New("mailer: unknown app")

// Build returns origin + fixed path + "#token=" + token. An unknown app or a
// token that is not 43 URL-safe characters is refused.
func (l *Links) Build(kind Kind, appID, token string) (string, error) {
	origin, ok := l.origins[appID]
	if !ok {
		return "", ErrUnknownApp
	}
	if !kind.Valid() {
		return "", errors.New("mailer: unknown template")
	}
	if !tokenPattern.MatchString(token) {
		return "", errors.New("mailer: malformed token")
	}
	return origin + kind.path() + "#token=" + token, nil
}
