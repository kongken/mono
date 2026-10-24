package mailer

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// sendGridEndpoint is the fixed v3 Mail Send endpoint.
const sendGridEndpoint = "https://api.sendgrid.com/v3/mail/send"

type sendGrid struct {
	*httpSender
	endpoint string
	apiKey   string
}

func newSendGrid(c SendGridConfig, timeout time.Duration, rt http.RoundTripper, secrets []string) Sender {
	return &sendGrid{httpSender: newHTTPSender(ProviderSendGrid, rt, timeout, secrets), endpoint: sendGridEndpoint, apiKey: c.APIKey}
}

type sgAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type sgBody struct {
	Personalizations []struct {
		To []sgAddress `json:"to"`
	} `json:"personalizations"`
	From     sgAddress  `json:"from"`
	ReplyTo  *sgAddress `json:"reply_to,omitempty"`
	Subject  string     `json:"subject"`
	Content  []sgPart   `json:"content"`
	Tracking sgTracking `json:"tracking_settings"`
}

type sgPart struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type sgToggle struct {
	Enable     bool `json:"enable"`
	EnableText bool `json:"enable_text,omitempty"`
}

type sgTracking struct {
	Click        sgToggle `json:"click_tracking"`
	Open         sgToggle `json:"open_tracking"`
	Subscription sgToggle `json:"subscription_tracking"`
}

// Send posts to POST /v3/mail/send with a Bearer key; 2xx (202) is acceptance.
// Tracking is off for the same reason as at Mailgun.
func (s *sendGrid) Send(ctx context.Context, msg Message) (Result, error) {
	if err := msg.Validate(); err != nil {
		return Result{}, invalidMessage(s.provider, err)
	}
	var body sgBody
	body.Personalizations = []struct {
		To []sgAddress `json:"to"`
	}{{To: []sgAddress{{Email: msg.To}}}}
	body.From = sgAddress{Email: msg.From.Email, Name: msg.From.Name}
	if !msg.ReplyTo.isZero() {
		body.ReplyTo = &sgAddress{Email: msg.ReplyTo.Email, Name: msg.ReplyTo.Name}
	}
	body.Subject = msg.Subject
	// SendGrid requires text/plain before text/html.
	body.Content = []sgPart{{Type: "text/plain", Value: msg.Text}, {Type: "text/html", Value: msg.HTML}}
	raw, err := json.Marshal(body)
	if err != nil {
		return Result{}, &Error{Class: ClassPermanent, Provider: s.provider, Code: "request"}
	}
	req, err := newPost(ctx, s.endpoint, "application/json", raw)
	if err != nil {
		return Result{}, &Error{Class: ClassPermanent, Provider: s.provider, Code: "request"}
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, derr := s.do(ctx, req)
	if derr != nil {
		return Result{}, derr
	}
	if resp.status >= 200 && resp.status < 300 {
		return Result{}, nil
	}
	return Result{}, s.classifyStatus(resp)
}
