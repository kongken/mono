package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// maxResponseBytes bounds how much of a vendor response is read.
const maxResponseBytes = 16 * 1024

// maxDetailBytes bounds the sanitized snippet kept in an Error.
const maxDetailBytes = 200

// newHTTPTransport is the production transport: TLS 1.2+ with the system roots,
// a fresh connection per mail (no pooled connection whose silent death could
// blur "was it sent"), and no hidden retries.
func newHTTPTransport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableKeepAlives = true
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return t
}

// httpSender is the part the three HTTP vendors share: one POST per message to a
// fixed official endpoint, never following redirects (a redirect could carry the
// API key to another host), with a bounded and redacted response.
type httpSender struct {
	provider string
	client   *http.Client
	timeout  time.Duration
	secrets  []string
}

func newHTTPSender(provider string, rt http.RoundTripper, timeout time.Duration, secrets []string) *httpSender {
	if rt == nil {
		rt = newHTTPTransport()
	}
	return &httpSender{
		provider: provider,
		timeout:  timeout,
		secrets:  secrets,
		client: &http.Client{
			Transport: rt,
			Timeout:   timeout,
			// Return the 3xx response itself: it is classified, not followed.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// response is what a vendor answered, bounded.
type response struct {
	status int
	body   []byte
}

// do performs the request and reports either the bounded response or a
// classified transport failure. A failure before the request was completely
// written is temporary (the vendor cannot have acted on it); after that point
// nothing is known and the failure is uncertain.
func (h *httpSender) do(ctx context.Context, req *http.Request) (*response, *Error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	var written atomic.Bool
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			written.Store(true)
		}
	}}
	req = req.WithContext(httptrace.WithClientTrace(ctx, trace))
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, h.transportError(err, written.Load())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	return &response{status: resp.StatusCode, body: body}, nil
}

func (h *httpSender) transportError(err error, written bool) *Error {
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		// Not a transient condition: nothing was sent, and retrying cannot help.
		return &Error{Class: ClassPermanent, Provider: h.provider, Code: "tls_verification"}
	}
	code := "network"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code = "timeout"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	default:
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			code = "timeout"
		}
	}
	if written {
		return &Error{Class: ClassUncertain, Provider: h.provider, Code: code}
	}
	return &Error{Class: ClassTemporary, Provider: h.provider, Code: code}
}

// classifyStatus maps a non-success HTTP status to a class. The three vendors
// document the same meaning for these codes:
//
//	3xx          permanent: the endpoints are fixed, a redirect is never followed
//	401, 403     permanent, credentials refused (operators are alerted)
//	408, 429     temporary: the request was not processed (timeout, rate limit)
//	503          temporary: the service says it is unavailable and did nothing
//	other 4xx    permanent: the request itself is refused
//	other 5xx    uncertain: the vendor may have queued the message before failing
func (h *httpSender) classifyStatus(r *response) *Error {
	e := &Error{Provider: h.provider, Code: "http_" + strconv.Itoa(r.status), Detail: sanitize(string(r.body), h.secrets)}
	switch {
	case r.status >= 300 && r.status < 400:
		e.Class, e.Code, e.Detail = ClassPermanent, "redirect_refused", ""
	case r.status == http.StatusUnauthorized || r.status == http.StatusForbidden:
		e.Class, e.Auth = ClassPermanent, true
	case r.status == http.StatusRequestTimeout || r.status == http.StatusTooManyRequests || r.status == http.StatusServiceUnavailable:
		e.Class = ClassTemporary
	case r.status >= 400 && r.status < 500:
		e.Class = ClassPermanent
	default:
		e.Class = ClassUncertain
	}
	return e
}

var (
	emailLike = regexp.MustCompile(`[A-Za-z0-9._%+'!#$&*/=?^{|}~-]+@[A-Za-z0-9.-]+`)
	urlLike   = regexp.MustCompile(`(?i)https?://\S+`)
	// Long unbroken runs of token-like characters: API keys, tokens, signatures.
	tokenLike = regexp.MustCompile(`[A-Za-z0-9_\-+/=.:]{24,}`)
)

// sanitize reduces a vendor text to a short, printable, redacted snippet:
// configured secrets, addresses, URLs and token-like runs are replaced, control
// characters dropped, the result truncated.
func sanitize(s string, secrets []string) string {
	if s == "" {
		return ""
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	for _, secret := range secrets {
		if len(secret) >= 4 {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	s = urlLike.ReplaceAllString(s, "[url]")
	s = emailLike.ReplaceAllString(s, "[email]")
	s = tokenLike.ReplaceAllString(s, "[token]")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxDetailBytes {
		s = strings.ToValidUTF8(s[:maxDetailBytes], "") + "..."
	}
	return s
}

// newPost builds a POST whose body can be sent exactly once: no GetBody, so the
// transport has nothing to replay.
func newPost(ctx context.Context, url, contentType string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = nil
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "kongken-usersrv-mailer/1")
	return req, nil
}
