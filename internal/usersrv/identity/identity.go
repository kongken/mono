// Package identity holds the pure input rules of the account system: the one
// email normalization every entry point shares, and the nickname, avatar and
// password checks. Nothing here touches storage.
package identity

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidationError names the field and the rule that failed. Its text is safe to
// show a client: it never echoes the offending value.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Reason }

func invalid(field, reason string) error { return &ValidationError{Field: field, Reason: reason} }

// IsValidation reports whether err is a ValidationError.
func IsValidation(err error) (*ValidationError, bool) {
	var v *ValidationError
	if errors.As(err, &v) {
		return v, true
	}
	return nil, false
}

// Limits of the account rules.
const (
	MaxEmailBytes       = 254
	MaxEmailLocalBytes  = 64
	maxEmailDomainBytes = 253
	maxDNSLabelBytes    = 63

	DefaultDisplayName   = "旅行者"
	MaxDisplayNameRunes  = 40
	MaxAvatarURLBytes    = 500
	MinPasswordRunes     = 12
	MaxPasswordRunes     = 128
	MaxPasswordBytes     = 512
	MaxMutationIDLength  = 64
	maxEmailInputBytes   = 4 * 1024
	maxProfileInputBytes = 4 * 1024
)

// NormalizeEmail returns the canonical form of an address and checks it against
// the single accepted shape (spec 4.1). The same function runs for registration,
// login, reset and the admin tools, so one mailbox has one key.
//
// Rules: CR and LF anywhere are refused; spaces and tabs around the address are
// trimmed; the whole ASCII address is lowercased (a deliberate policy, not a
// claim about every mail system); exactly one ASCII address of at most 254 bytes
// with a dot-atom local part of at most 64 bytes and a DNS domain with at least
// two labels. Display names, several addresses, comments, quoted local parts,
// control characters, IP literals and internationalized addresses are refused.
// "+tag" is kept and nothing is guessed about aliases.
func NormalizeEmail(input string) (string, error) {
	const field = "email"
	if len(input) > maxEmailInputBytes {
		return "", invalid(field, "too long")
	}
	if strings.ContainsAny(input, "\r\n") {
		return "", invalid(field, "invalid format")
	}
	s := strings.Trim(input, " \t\v\f")
	if s == "" {
		return "", invalid(field, "required")
	}
	if len(s) > MaxEmailBytes {
		return "", invalid(field, "too long")
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x21 || c > 0x7e { // controls, space and non-ASCII
			return "", invalid(field, "invalid format")
		}
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || strings.Contains(domain, "@") {
		return "", invalid(field, "invalid format")
	}
	if len(local) == 0 || len(local) > MaxEmailLocalBytes || !validLocalPart(local) {
		return "", invalid(field, "invalid format")
	}
	domain = strings.ToLower(domain)
	if !validDomain(domain) {
		return "", invalid(field, "invalid format")
	}
	return strings.ToLower(local) + "@" + domain, nil
}

// validLocalPart accepts an RFC 5322 dot-atom: atext characters separated by
// single dots, no leading or trailing dot.
func validLocalPart(s string) bool {
	if s[0] == '.' || s[len(s)-1] == '.' || strings.Contains(s, "..") {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+/=?^_`{|}~-.", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// validDomain accepts a lowercase DNS name of at least two labels made of
// letters, digits and inner hyphens (ASCII punycode "xn--" labels included).
// A numeric top-level label is refused, which also rules out IP addresses.
func validDomain(d string) bool {
	if d == "" || len(d) > maxEmailDomainBytes {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > maxDNSLabelBytes || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	allDigits := true
	for i := 0; i < len(tld); i++ {
		if tld[i] < '0' || tld[i] > '9' {
			allDigits = false
		}
	}
	return !allDigits
}

// NormalizeDisplayName trims the nickname and checks it: valid UTF-8, 1-40
// Unicode code points, no control characters. An empty input returns the
// default "旅行者" when useDefault is set and is an error otherwise.
func NormalizeDisplayName(input string, useDefault bool) (string, error) {
	const field = "display_name"
	if len(input) > maxProfileInputBytes || !utf8.ValidString(input) {
		return "", invalid(field, "invalid")
	}
	s := strings.TrimSpace(input)
	if s == "" {
		if useDefault {
			return DefaultDisplayName, nil
		}
		return "", invalid(field, "required")
	}
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || r == utf8.RuneError {
			return "", invalid(field, "invalid characters")
		}
		n++
	}
	if n > MaxDisplayNameRunes {
		return "", invalid(field, "too long")
	}
	return s, nil
}

// NormalizeAvatarURL accepts an empty value (no avatar) or an absolute https
// URL without credentials: at most 500 bytes, ASCII only, with a host and no
// whitespace or control characters. The server never fetches it.
func NormalizeAvatarURL(input string) (string, error) {
	const field = "avatar_url"
	if input == "" {
		return "", nil
	}
	if len(input) > MaxAvatarURLBytes {
		return "", invalid(field, "too long")
	}
	for i := 0; i < len(input); i++ {
		if c := input[i]; c <= 0x20 || c >= 0x7f {
			return "", invalid(field, "invalid format")
		}
	}
	u, err := url.Parse(input)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.Hostname() == "" ||
		u.User != nil || u.Opaque != "" || u.Fragment != "" {
		return "", invalid(field, "must be an https URL without credentials")
	}
	return input, nil
}

// ValidatePassword checks the password policy (spec 4.2): 12-128 Unicode code
// points and at most 512 UTF-8 bytes. The password is never trimmed, truncated
// or normalized, and no character-class mix is required. It must run before
// any hashing so oversized input is refused cheaply.
func ValidatePassword(field, password string) error {
	if len(password) > MaxPasswordBytes {
		return invalid(field, "too long")
	}
	if !utf8.ValidString(password) {
		return invalid(field, "invalid")
	}
	n := utf8.RuneCountInString(password)
	switch {
	case n < MinPasswordRunes:
		return invalid(field, fmt.Sprintf("must be at least %d characters", MinPasswordRunes))
	case n > MaxPasswordRunes:
		return invalid(field, fmt.Sprintf("must be at most %d characters", MaxPasswordRunes))
	}
	return nil
}

// CheckPasswordInput is the cheap size gate for a password that is only being
// compared, not chosen (login, re-authentication, "current password"): it
// refuses oversized or malformed input before any hashing, without applying
// the minimum length, so an old shorter password is still reported as wrong
// rather than as a format error.
func CheckPasswordInput(field, password string) error {
	if password == "" {
		return invalid(field, "required")
	}
	if len(password) > MaxPasswordBytes {
		return invalid(field, "too long")
	}
	if !utf8.ValidString(password) {
		return invalid(field, "invalid")
	}
	if utf8.RuneCountInString(password) > MaxPasswordRunes {
		return invalid(field, "too long")
	}
	return nil
}

// NormalizeMutationID checks a deletion workflow idempotency key.
func NormalizeMutationID(id string) (string, error) {
	if id == "" || len(id) > MaxMutationIDLength {
		return "", invalid("mutation_id", "must be 1-64 characters")
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return "", invalid("mutation_id", "invalid characters")
		}
	}
	return id, nil
}

// tokenPattern is the URL-safe base64 of 32 random bytes without padding: the
// only shape of token usersrv issues.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// TokenDigest returns the SHA-256 digest under which a registration or reset
// token is stored, or false when the string cannot be a token we issued (wrong
// length or alphabet), so such input never reaches the database. Issuing (the
// outbox) and consuming (the account flows) share this one function.
func TokenDigest(token string) ([]byte, bool) {
	if !tokenPattern.MatchString(token) {
		return nil, false
	}
	d := sha256.Sum256([]byte(token))
	return d[:], true
}
