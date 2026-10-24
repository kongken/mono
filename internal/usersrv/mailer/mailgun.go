package mailer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// Official Mailgun API hosts. The region picks one; the other is never tried.
const (
	mailgunUSBase = "https://api.mailgun.net"
	mailgunEUBase = "https://api.eu.mailgun.net"
)

type mailgun struct {
	*httpSender
	baseURL string
	apiKey  string
	domain  string
}

func newMailgun(c MailgunConfig, timeout time.Duration, rt http.RoundTripper, secrets []string) Sender {
	base := mailgunUSBase
	if c.Region == "eu" {
		base = mailgunEUBase
	}
	return &mailgun{
		httpSender: newHTTPSender(ProviderMailgun, rt, timeout, secrets),
		baseURL:    base, apiKey: c.APIKey, domain: c.Domain,
	}
}

// Send posts to POST /v3/{domain}/messages with HTTP basic auth ("api" and the
// key). Click and open tracking are switched off: tracking rewrites links and
// would route the one-time token through the vendor.
func (m *mailgun) Send(ctx context.Context, msg Message) (Result, error) {
	if err := msg.Validate(); err != nil {
		return Result{}, invalidMessage(m.provider, err)
	}
	form := url.Values{}
	form.Set("from", formatAddress(msg.From))
	form.Set("to", msg.To)
	form.Set("subject", msg.Subject)
	form.Set("text", msg.Text)
	form.Set("html", msg.HTML)
	if !msg.ReplyTo.isZero() {
		form.Set("h:Reply-To", formatAddress(msg.ReplyTo))
	}
	form.Set("o:tracking", "no")
	form.Set("o:tracking-clicks", "no")
	form.Set("o:tracking-opens", "no")
	endpoint := m.baseURL + "/v3/" + url.PathEscape(m.domain) + "/messages"
	req, err := newPost(ctx, endpoint, "application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil {
		return Result{}, &Error{Class: ClassPermanent, Provider: m.provider, Code: "request"}
	}
	req.SetBasicAuth("api", m.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, derr := m.do(ctx, req)
	if derr != nil {
		return Result{}, derr
	}
	if resp.status >= 200 && resp.status < 300 {
		var ok struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(resp.body, &ok)
		return Result{ProviderID: sanitize(ok.ID, m.secrets)}, nil
	}
	return Result{}, m.classifyStatus(resp)
}

// formatAddress renders `"Name" <email>` with the name quoted, or the bare
// address. It is for JSON/form fields; SMTP headers use RFC 2047 instead.
func formatAddress(a Address) string {
	if a.Name == "" {
		return a.Email
	}
	// mail.Address.String would RFC 2047-encode a non-ASCII name; the HTTP APIs
	// take UTF-8 text, so quote it by hand.
	q := make([]rune, 0, len(a.Name)+2)
	q = append(q, '"')
	for _, r := range a.Name {
		if r == '"' || r == '\\' {
			q = append(q, '\\')
		}
		q = append(q, r)
	}
	q = append(q, '"')
	return string(q) + " <" + a.Email + ">"
}
