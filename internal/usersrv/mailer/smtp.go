package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

type smtpSender struct {
	host, port string
	tlsMode    string
	username   string
	password   string
	timeout    time.Duration
	tlsConfig  *tls.Config
	secrets    []string
	now        func() time.Time
}

func newSMTP(c SMTPConfig, timeout time.Duration, tlsCfg *tls.Config, secrets []string) Sender {
	base := &tls.Config{}
	if tlsCfg != nil {
		base = tlsCfg.Clone()
	}
	base.ServerName = c.Host
	if base.MinVersion == 0 {
		base.MinVersion = tls.VersionTLS12
	}
	return &smtpSender{
		host: c.Host, port: strconv.Itoa(c.Port), tlsMode: c.TLSMode,
		username: c.Username, password: c.Password,
		timeout: timeout, tlsConfig: base, secrets: secrets, now: time.Now,
	}
}

func (s *smtpSender) fail(class Class, code, detail string, auth bool) *Error {
	return &Error{Class: class, Provider: ProviderSMTP, Code: code, Detail: sanitize(detail, s.secrets), Auth: auth}
}

// protocolError maps a failed SMTP command to a class: the reply code decides
// (4xx temporary, 5xx permanent). Anything that is not a protocol reply (the
// connection broke, a deadline passed) happened before the message was handed
// over, so it is temporary too. Reply text from the server is never kept.
func (s *smtpSender) protocolError(ctx context.Context, stage string, err error) *Error {
	var te *textproto.Error
	if errors.As(err, &te) {
		code := fmt.Sprintf("smtp_%d", te.Code)
		switch {
		case te.Code >= 400 && te.Code < 500:
			return s.fail(ClassTemporary, code, "", false)
		default:
			return s.fail(ClassPermanent, code, "", stage == "auth")
		}
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return s.fail(ClassPermanent, "tls_verification", "", false)
	}
	return s.fail(ClassTemporary, stage+"_"+transportCode(ctx, err), "", false)
}

// transportCode names a network failure. When the send's context is over, that is
// the cause: the deadline or a cancellation closes the connection under the
// blocked call, which would otherwise look like a generic network error.
func transportCode(ctx context.Context, err error) string {
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	return "network"
}

// Send delivers over one SMTP session: connect (implicit TLS, or STARTTLS which
// must succeed), authenticate when configured, MAIL/RCPT/DATA. A failure before
// the DATA terminator is acknowledged or refused never leaves the message
// accepted; only a lost connection while waiting for the final reply is
// uncertain.
func (s *smtpSender) Send(ctx context.Context, msg Message) (Result, error) {
	if err := msg.Validate(); err != nil {
		return Result{}, invalidMessage(ProviderSMTP, err)
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	body, err := buildMIME(msg, s.now())
	if err != nil {
		return Result{}, invalidMessage(ProviderSMTP, err)
	}

	addr := net.JoinHostPort(s.host, s.port)
	var dialer net.Dialer
	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Result{}, s.fail(ClassTemporary, "connect_"+transportCode(ctx, err), "", false)
	}
	var conn net.Conn = raw
	// net/smtp has no context support: a deadline bounds every read and write, and
	// cancellation closes the connection under it.
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	defer raw.Close()

	if s.tlsMode == TLSImplicit {
		tc := tls.Client(raw, s.tlsConfig)
		if err = tc.HandshakeContext(ctx); err != nil {
			return Result{}, s.protocolError(ctx, "tls", err)
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return Result{}, s.protocolError(ctx, "greeting", err)
	}
	defer c.Close()
	if err = c.Hello(helloName(msg.From.Email)); err != nil {
		return Result{}, s.protocolError(ctx, "hello", err)
	}
	if s.tlsMode == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			// A server that does not offer STARTTLS (or a network device that
			// stripped it) is never answered in clear text.
			return Result{}, s.fail(ClassPermanent, "starttls_unavailable", "", false)
		}
		if err = c.StartTLS(s.tlsConfig); err != nil {
			return Result{}, s.protocolError(ctx, "starttls", err)
		}
	}
	if s.username != "" {
		if e := s.authenticate(ctx, c); e != nil {
			return Result{}, e
		}
	}
	if err = c.Mail(msg.From.Email); err != nil {
		return Result{}, s.protocolError(ctx, "mail", err)
	}
	if err = c.Rcpt(msg.To); err != nil {
		return Result{}, s.protocolError(ctx, "rcpt", err)
	}
	w, err := c.Data()
	if err != nil {
		return Result{}, s.protocolError(ctx, "data", err)
	}
	if _, err = w.Write(body); err != nil {
		// The terminator was never sent: the server discards the partial message.
		return Result{}, s.fail(ClassTemporary, "data_"+transportCode(ctx, err), "", false)
	}
	if err = w.Close(); err != nil {
		var te *textproto.Error
		if errors.As(err, &te) {
			return Result{}, s.protocolError(ctx, "data", err)
		}
		// The terminator went out but the final reply did not arrive: the server
		// may have queued the message.
		return Result{}, s.fail(ClassUncertain, "final_reply_"+transportCode(ctx, err), "", false)
	}
	_ = c.Quit()
	return Result{}, nil
}

// authenticate uses PLAIN or LOGIN, whichever the server offers, and only on an
// encrypted session or towards a loopback development server.
func (s *smtpSender) authenticate(ctx context.Context, c *smtp.Client) *Error {
	ok, params := c.Extension("AUTH")
	if !ok {
		return s.fail(ClassPermanent, "auth_unsupported", "", true)
	}
	_, isTLS := c.TLSConnectionState()
	if !isTLS && !IsLoopbackHost(s.host) {
		return s.fail(ClassPermanent, "auth_without_tls", "", false)
	}
	mechanisms := strings.Fields(strings.ToUpper(params))
	var auth smtp.Auth
	switch {
	case contains(mechanisms, "PLAIN"):
		auth = plainAuth{user: s.username, pass: s.password}
	case contains(mechanisms, "LOGIN"):
		auth = loginAuth{user: s.username, pass: s.password}
	default:
		return s.fail(ClassPermanent, "auth_unsupported", "", true)
	}
	if err := c.Auth(auth); err != nil {
		e := s.protocolError(ctx, "auth", err)
		if e.Class == ClassPermanent {
			e.Auth = true
		}
		return e
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// plainAuth is AUTH PLAIN. net/smtp's own PlainAuth refuses a non-TLS session to
// a non-"localhost" name; the caller has already enforced the policy.
type plainAuth struct{ user, pass string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

// loginAuth is AUTH LOGIN, still required by some providers.
type loginAuth struct{ user, pass string }

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }
func (a loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(challenge))) {
	case "username:", "user name:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.pass), nil
	}
	return nil, errors.New("unexpected server challenge")
}

// helloName is the EHLO identity: the domain of the sender address.
func helloName(from string) string {
	if i := strings.LastIndexByte(from, '@'); i >= 0 && i+1 < len(from) {
		return from[i+1:]
	}
	return "localhost"
}

// buildMIME renders the message: encoded headers, multipart/alternative with a
// plain and an HTML part, CRLF line ends, no attachments. Every header value was
// checked for CR/LF by Message.Validate; the encoders below additionally make
// non-ASCII text safe.
func buildMIME(msg Message, now time.Time) ([]byte, error) {
	var b bytes.Buffer
	header := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	header("From", smtpAddress(msg.From))
	header("To", msg.To)
	if !msg.ReplyTo.isZero() {
		header("Reply-To", smtpAddress(msg.ReplyTo))
	}
	header("Subject", mime.BEncoding.Encode("UTF-8", msg.Subject))
	header("Date", now.UTC().Format(time.RFC1123Z))
	if msg.ID != "" {
		header("Message-ID", "<"+msg.ID+"@"+helloName(msg.From.Email)+">")
	}
	header("MIME-Version", "1.0")
	header("Auto-Submitted", "auto-generated")
	mw := multipart.NewWriter(&b)
	header("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
	b.WriteString("\r\n")
	for _, part := range []struct{ typ, body string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.typ + "; charset=UTF-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		qp := quotedprintable.NewWriter(pw)
		if _, err = io.WriteString(qp, strings.ReplaceAll(part.body, "\r\n", "\n")); err != nil {
			return nil, err
		}
		if err = qp.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func smtpAddress(a Address) string {
	return (&mail.Address{Name: a.Name, Address: a.Email}).String()
}
