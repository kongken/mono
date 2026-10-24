// Package mailer sends the account mails of usersrv (email verification and
// password reset). One Sender interface hides four vendors: Mailgun, SendGrid
// and SMTP2GO through their native HTTP APIs, and plain SMTP. Exactly one is
// configured and active; there is no failover between vendors.
//
// The package knows nothing about accounts or storage. It renders the fixed
// Chinese templates, builds the fixed verification links, and delivers one
// message per Send call, classifying every failure as temporary (the vendor
// certainly did not accept the message), permanent (it will never accept it) or
// uncertain (the message may have been accepted, so it must not be re-sent).
package mailer

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kongken/mono/internal/usersrv/identity"
)

// Class is how a failed Send is to be treated by the caller.
type Class string

const (
	// ClassTemporary: the message was certainly not accepted and a later attempt
	// may succeed (connection refused before anything was sent, 429, SMTP 4xx).
	ClassTemporary Class = "temporary"
	// ClassPermanent: the vendor refused for good (bad credentials, invalid
	// recipient, rejected request) or the setup cannot work (certificate error).
	ClassPermanent Class = "permanent"
	// ClassUncertain: the request may have been processed (timeout or reset after
	// the request was written, HTTP 5xx other than 503). Never retried
	// automatically: a retry could deliver the same token twice.
	ClassUncertain Class = "uncertain"
)

// Error is a classified delivery failure. Its text is safe to log: the vendor
// response is reduced to a short redacted snippet that holds no address, key or
// token.
type Error struct {
	Class    Class
	Provider string
	// Code is a short machine-friendly reason such as "http_429", "smtp_550",
	// "timeout", "redirect_refused".
	Code string
	// Detail is a sanitized, truncated vendor message (may be empty).
	Detail string
	// Auth is set when the vendor refused the credentials: operators must act.
	Auth bool
}

func (e *Error) Error() string {
	s := fmt.Sprintf("mail %s: %s (%s)", e.Provider, e.Class, e.Code)
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

// ClassOf returns the class of err. An error that is not an *Error is treated as
// uncertain: nothing is known about whether the message left, so it is not
// re-sent.
func ClassOf(err error) Class {
	var me *Error
	if errors.As(err, &me) {
		return me.Class
	}
	return ClassUncertain
}

// Result is a successful hand-over to the vendor. It means "accepted", never
// "delivered".
type Result struct {
	// ProviderID is the vendor's message identifier, when it gave one (sanitized,
	// at most 200 bytes).
	ProviderID string
}

// Sender delivers one message.
type Sender interface {
	Send(ctx context.Context, msg Message) (Result, error)
}

// Address is a mailbox with an optional display name.
type Address struct {
	Email string
	Name  string
}

func (a Address) isZero() bool { return a.Email == "" && a.Name == "" }

// Message is one fully rendered mail with a single recipient.
type Message struct {
	// ID is unique per message (the outbox job ID); SMTP uses it as the Message-ID.
	ID string
	// From and ReplyTo come from the server configuration, never from a request.
	From    Address
	ReplyTo Address
	// To is one canonical ASCII address.
	To      string
	Subject string
	Text    string
	HTML    string
}

const (
	maxSubjectBytes = 200
	maxNameRunes    = 100
	maxBodyBytes    = 256 * 1024
)

// hasLineBreak reports a CR or LF, the characters header injection needs.
func hasLineBreak(s string) bool { return strings.ContainsAny(s, "\r\n") }

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return true
		}
	}
	return false
}

// validateAddress accepts a bare ASCII address without display name, comment or
// control characters.
func validateAddress(field, email string) error {
	if email == "" || len(email) > identity.MaxEmailBytes || hasLineBreak(email) || hasControl(email) {
		return fmt.Errorf("mailer: invalid %s address", field)
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Name != "" || parsed.Address != email {
		return fmt.Errorf("mailer: invalid %s address", field)
	}
	for i := 0; i < len(email); i++ {
		if email[i] >= 0x80 {
			return fmt.Errorf("mailer: invalid %s address", field)
		}
	}
	return nil
}

func validateName(field, name string) error {
	if hasLineBreak(name) || hasControl(name) || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxNameRunes {
		return fmt.Errorf("mailer: invalid %s name", field)
	}
	return nil
}

// Validate rejects anything an attacker could use to inject headers or extra
// recipients: CR/LF and control characters in every header value, anything but
// one canonical address as the recipient.
func (m Message) Validate() error {
	if err := validateAddress("from", m.From.Email); err != nil {
		return err
	}
	if err := validateName("from", m.From.Name); err != nil {
		return err
	}
	if !m.ReplyTo.isZero() {
		if err := validateAddress("reply-to", m.ReplyTo.Email); err != nil {
			return err
		}
		if err := validateName("reply-to", m.ReplyTo.Name); err != nil {
			return err
		}
	}
	canonical, err := identity.NormalizeEmail(m.To)
	if err != nil || canonical != m.To {
		return errors.New("mailer: recipient must be one canonical address")
	}
	if err := validateAddress("to", m.To); err != nil {
		return err
	}
	if m.Subject == "" || len(m.Subject) > maxSubjectBytes || hasLineBreak(m.Subject) || hasControl(m.Subject) || !utf8.ValidString(m.Subject) {
		return errors.New("mailer: invalid subject")
	}
	if m.Text == "" || m.HTML == "" || len(m.Text) > maxBodyBytes || len(m.HTML) > maxBodyBytes ||
		!utf8.ValidString(m.Text) || !utf8.ValidString(m.HTML) {
		return errors.New("mailer: invalid body")
	}
	if m.ID != "" && (len(m.ID) > 100 || hasControl(m.ID) || strings.ContainsAny(m.ID, " <>@\"")) {
		return errors.New("mailer: invalid message id")
	}
	return nil
}

// invalidMessage is the failure for a message that cannot be sent as is. It is
// permanent: retrying the same content cannot help.
func invalidMessage(provider string, err error) *Error {
	return &Error{Class: ClassPermanent, Provider: provider, Code: "invalid_message", Detail: err.Error()}
}
