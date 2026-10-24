package mailer

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// smtp2goEndpoint is the fixed SMTP2GO API v3 "send email" endpoint.
const smtp2goEndpoint = "https://api.smtp2go.com/v3/email/send"

type smtp2go struct {
	*httpSender
	endpoint string
	apiKey   string
}

func newSMTP2GO(c SMTP2GOConfig, timeout time.Duration, rt http.RoundTripper, secrets []string) Sender {
	return &smtp2go{httpSender: newHTTPSender(ProviderSMTP2GO, rt, timeout, secrets), endpoint: smtp2goEndpoint, apiKey: c.APIKey}
}

type s2gHeader struct {
	Header string `json:"header"`
	Value  string `json:"value"`
}

type s2gBody struct {
	Sender  string      `json:"sender"`
	To      []string    `json:"to"`
	Subject string      `json:"subject"`
	Text    string      `json:"text_body"`
	HTML    string      `json:"html_body"`
	Headers []s2gHeader `json:"custom_headers,omitempty"`
}

// Send posts to the native JSON API; the key travels in the X-Smtp2go-Api-Key
// header, not in the body. HTTP 200 is only acceptance when the body reports
// one succeeded recipient and no failure.
func (s *smtp2go) Send(ctx context.Context, msg Message) (Result, error) {
	if err := msg.Validate(); err != nil {
		return Result{}, invalidMessage(s.provider, err)
	}
	body := s2gBody{Sender: formatAddress(msg.From), To: []string{msg.To}, Subject: msg.Subject, Text: msg.Text, HTML: msg.HTML}
	if !msg.ReplyTo.isZero() {
		body.Headers = append(body.Headers, s2gHeader{Header: "Reply-To", Value: formatAddress(msg.ReplyTo)})
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Result{}, &Error{Class: ClassPermanent, Provider: s.provider, Code: "request"}
	}
	req, err := newPost(ctx, s.endpoint, "application/json", raw)
	if err != nil {
		return Result{}, &Error{Class: ClassPermanent, Provider: s.provider, Code: "request"}
	}
	req.Header.Set("X-Smtp2go-Api-Key", s.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, derr := s.do(ctx, req)
	if derr != nil {
		return Result{}, derr
	}
	if resp.status != http.StatusOK {
		return Result{}, s.classifyStatus(resp)
	}
	var out struct {
		Data struct {
			Succeeded int    `json:"succeeded"`
			Failed    int    `json:"failed"`
			EmailID   string `json:"email_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.body, &out); err != nil {
		// 200 but no readable verdict: the message may have been queued.
		return Result{}, &Error{Class: ClassUncertain, Provider: s.provider, Code: "unreadable_response"}
	}
	switch {
	case out.Data.Failed > 0:
		return Result{}, &Error{Class: ClassPermanent, Provider: s.provider, Code: "recipient_rejected"}
	case out.Data.Succeeded < 1:
		return Result{}, &Error{Class: ClassUncertain, Provider: s.provider, Code: "no_verdict"}
	}
	return Result{ProviderID: sanitize(out.Data.EmailID, s.secrets)}, nil
}
