package mailer_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/kongken/mono/internal/usersrv/mailer"
)

const goodToken = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO12" // 43 URL-safe characters

func content(t *testing.T) mailer.Content {
	t.Helper()
	link, err := testLinks(t).Build(mailer.KindVerifyEmail, "trip-map", goodToken)
	if err != nil {
		t.Fatal(err)
	}
	c, err := mailer.Render(mailer.KindVerifyEmail, mailer.TemplateData{AppName: "山海足迹", Link: link, ExpiresIn: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testLinks(t *testing.T) *mailer.Links {
	t.Helper()
	l, err := mailer.NewLinks(map[string]string{"ozid": "https://id.example.com", "trip-map": "https://map.example.com", "metro": "https://metro.example.com"}, false)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func message(t *testing.T, m *mailer.Mailer) mailer.Message {
	t.Helper()
	return m.Compose("0b8f0f4e-0000-4000-8000-000000000001", "alice+tag@example.com", content(t))
}

func baseConfig() mailer.Config {
	return mailer.Config{Provider: "sendgrid", FromEmail: "no-reply@example.com", FromName: "山海足迹",
		ReplyTo: "help@example.com", Timeout: "2s", SendGrid: mailer.SendGridConfig{APIKey: "SG.supersecretkey123"}}
}

// ---- links and templates ------------------------------------------------------

func TestLinksUseTheExactOriginFixedPathAndFragment(t *testing.T) {
	l := testLinks(t)
	got, err := l.Build(mailer.KindVerifyEmail, "trip-map", goodToken)
	if err != nil || got != "https://map.example.com/account/verify-email#token="+goodToken {
		t.Fatalf("%q %v", got, err)
	}
	got, err = l.Build(mailer.KindResetPassword, "metro", goodToken)
	if err != nil || got != "https://metro.example.com/account/reset-password#token="+goodToken {
		t.Fatalf("%q %v", got, err)
	}
	if strings.Contains(got, "?") {
		t.Fatal("the token must not be in a query string")
	}
	if _, err = l.Build(mailer.KindVerifyEmail, "evil", goodToken); !errors.Is(err, mailer.ErrUnknownApp) {
		t.Fatalf("unknown app: %v", err)
	}
	for _, tok := range []string{"", "short", goodToken + "x", strings.Repeat("a", 42) + "/", strings.Repeat("a", 42) + "#"} {
		if _, err = l.Build(mailer.KindVerifyEmail, "metro", tok); err == nil {
			t.Errorf("token %q accepted", tok)
		}
	}
	if _, err = l.Build("other", "metro", goodToken); err == nil {
		t.Fatal("unknown template accepted")
	}
}

func TestLinkOriginsAreValidated(t *testing.T) {
	for _, o := range []string{"", "http://map.example.com", "https://map.example.com/", "https://map.example.com/x", "https://u@map.example.com", "ftp://x.example.com", "https://map.example.com?x=1", "map.example.com"} {
		if _, err := mailer.NewLinks(map[string]string{"a": o}, false); err == nil {
			t.Errorf("origin %q accepted", o)
		}
	}
	if _, err := mailer.NewLinks(map[string]string{"a": "http://localhost:5173"}, false); err == nil {
		t.Fatal("loopback http accepted without the switch")
	}
	if _, err := mailer.NewLinks(map[string]string{"a": "http://localhost:5173"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := mailer.NewLinks(map[string]string{"a": "http://map.example.com"}, true); err == nil {
		t.Fatal("public http accepted")
	}
}

func TestTemplatesAreFixedChineseAndEscapeParameters(t *testing.T) {
	l := testLinks(t)
	for _, kind := range []mailer.Kind{mailer.KindVerifyEmail, mailer.KindResetPassword} {
		link, _ := l.Build(kind, "metro", goodToken)
		c, err := mailer.Render(kind, mailer.TemplateData{AppName: mailer.AppName("metro"), Link: link, ExpiresIn: 15 * time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(c.Subject, "地铁足迹") || !strings.Contains(c.Text, link) || !strings.Contains(c.HTML, link) ||
			!strings.Contains(c.Text, "15 分钟") || !strings.Contains(c.HTML, `lang="zh-CN"`) {
			t.Fatalf("%s: %+v", kind, c)
		}
		if strings.Contains(c.HTML, "<script") || strings.Contains(c.HTML, "http-equiv") || strings.Count(strings.ToLower(c.HTML), "https://") != 2 {
			t.Fatalf("unexpected active or external content: %s", c.HTML)
		}
	}
	bad := map[string]mailer.TemplateData{
		"crlf in link":     {AppName: "x", Link: "https://a.example.com/\r\nBcc: evil@example.com"},
		"quote in link":    {AppName: "x", Link: `https://a.example.com/"onmouseover="x`},
		"script scheme":    {AppName: "x", Link: "javascript:alert(1)"},
		"markup in name":   {AppName: "<script>", Link: "https://a.example.com"},
		"newline in name":  {AppName: "a\nb", Link: "https://a.example.com"},
		"space in link":    {AppName: "x", Link: "https://a.example.com/a b"},
		"angle in link":    {AppName: "x", Link: "https://a.example.com/<b>"},
		"empty app name":   {AppName: "", Link: "https://a.example.com"},
		"backslash":        {AppName: "x", Link: `https://a.example.com\evil`},
		"backtick in link": {AppName: "x", Link: "https://a.example.com/`"},
	}
	for name, d := range bad {
		if _, err := mailer.Render(mailer.KindVerifyEmail, d); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := mailer.Render("nope", mailer.TemplateData{AppName: "x", Link: "https://a.example.com"}); err == nil {
		t.Fatal("unknown template accepted")
	}
}

// ---- message validation (header injection) ------------------------------------

func TestMessageRejectsHeaderInjectionAndExtraRecipients(t *testing.T) {
	m, err := mailer.NewWithSender(baseConfig(), nopSender{})
	if err != nil {
		t.Fatal(err)
	}
	good := message(t, m)
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*mailer.Message){
		"crlf in subject":       func(x *mailer.Message) { x.Subject = "hi\r\nBcc: evil@example.com" },
		"lf in subject":         func(x *mailer.Message) { x.Subject = "hi\nBcc: evil@example.com" },
		"crlf in to":            func(x *mailer.Message) { x.To = "a@example.com\r\nBcc: evil@example.com" },
		"two recipients":        func(x *mailer.Message) { x.To = "a@example.com,b@example.com" },
		"recipient with name":   func(x *mailer.Message) { x.To = "Alice <a@example.com>" },
		"uppercase recipient":   func(x *mailer.Message) { x.To = "A@Example.com" },
		"crlf in from name":     func(x *mailer.Message) { x.From.Name = "x\r\nBcc: evil@example.com" },
		"crlf in from address":  func(x *mailer.Message) { x.From.Email = "a@example.com\r\nX: y" },
		"crlf in reply-to":      func(x *mailer.Message) { x.ReplyTo.Email = "a@example.com\nX: y" },
		"from with display":     func(x *mailer.Message) { x.From.Email = "Bob <bob@example.com>" },
		"control char subject":  func(x *mailer.Message) { x.Subject = "a\x00b" },
		"empty subject":         func(x *mailer.Message) { x.Subject = "" },
		"empty html":            func(x *mailer.Message) { x.HTML = "" },
		"message id injection":  func(x *mailer.Message) { x.ID = "a>\r\nX: y" },
		"non ascii recipient":   func(x *mailer.Message) { x.To = "用户@example.com" },
		"bare local recipient":  func(x *mailer.Message) { x.To = "root" },
		"subject too long":      func(x *mailer.Message) { x.Subject = strings.Repeat("a", 500) },
		"reply-to name newline": func(x *mailer.Message) { x.ReplyTo.Name = "a\nb" },
	}
	for name, mutate := range cases {
		msg := good
		mutate(&msg)
		if err := msg.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

type nopSender struct{}

func (nopSender) Send(context.Context, mailer.Message) (mailer.Result, error) {
	return mailer.Result{}, nil
}

// A caller cannot choose the sender: the Mailer stamps the configured identity.
func TestMailerOverridesFromAndReplyTo(t *testing.T) {
	var seen mailer.Message
	m, err := mailer.NewWithSender(baseConfig(), senderFunc(func(_ context.Context, msg mailer.Message) (mailer.Result, error) {
		seen = msg
		return mailer.Result{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	msg := message(t, m)
	msg.From = mailer.Address{Email: "ceo@evil.example", Name: "CEO"}
	msg.ReplyTo = mailer.Address{Email: "phish@evil.example"}
	if _, err = m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if seen.From.Email != "no-reply@example.com" || seen.ReplyTo.Email != "help@example.com" || seen.From.Name != "山海足迹" {
		t.Fatalf("identity not fixed: %+v", seen)
	}
	// An invalid message is permanent and never reaches the vendor.
	msg.Subject = "x\r\ny"
	called := false
	m2, _ := mailer.NewWithSender(baseConfig(), senderFunc(func(context.Context, mailer.Message) (mailer.Result, error) {
		called = true
		return mailer.Result{}, nil
	}))
	_, err = m2.Send(context.Background(), msg)
	if called || mailer.ClassOf(err) != mailer.ClassPermanent {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

type senderFunc func(context.Context, mailer.Message) (mailer.Result, error)

func (f senderFunc) Send(ctx context.Context, m mailer.Message) (mailer.Result, error) {
	return f(ctx, m)
}

// ---- configuration -------------------------------------------------------------

func TestConfigValidation(t *testing.T) {
	ok := map[string]mailer.Config{
		"mailgun us": {Provider: "mailgun", FromEmail: "a@example.com", Mailgun: mailer.MailgunConfig{APIKey: "k", Domain: "mg.example.com", Region: "us"}},
		"mailgun eu": {Provider: "mailgun", FromEmail: "a@example.com", Mailgun: mailer.MailgunConfig{APIKey: "k", Domain: "mg.example.com", Region: "eu"}},
		"sendgrid":   {Provider: "sendgrid", FromEmail: "a@example.com", SendGrid: mailer.SendGridConfig{APIKey: "k"}},
		"smtp2go":    {Provider: "smtp2go", FromEmail: "a@example.com", SMTP2GO: mailer.SMTP2GOConfig{APIKey: "k"}},
		"smtp tls":   {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Host: "smtp.example.com", Port: 465, TLSMode: "implicit_tls", Username: "u", Password: "p"}},
		"smtp start": {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Host: "smtp.example.com", Port: 587, TLSMode: "starttls"}},
		"smtp dev":   {Provider: "smtp", FromEmail: "a@example.com", AllowInsecureLoopback: true, SMTP: mailer.SMTPConfig{Host: "127.0.0.1", Port: 1025, TLSMode: "plain"}},
	}
	for name, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	mg := mailer.MailgunConfig{APIKey: "k", Domain: "mg.example.com", Region: "us"}
	bad := map[string]mailer.Config{
		"no provider":             {FromEmail: "a@example.com"},
		"unknown provider":        {Provider: "postmark", FromEmail: "a@example.com"},
		"provider without params": {Provider: "sendgrid", FromEmail: "a@example.com"},
		"mailgun without key":     {Provider: "mailgun", FromEmail: "a@example.com", Mailgun: mailer.MailgunConfig{Domain: "mg.example.com", Region: "us"}},
		"mailgun without domain":  {Provider: "mailgun", FromEmail: "a@example.com", Mailgun: mailer.MailgunConfig{APIKey: "k", Region: "us"}},
		"mailgun path domain":     {Provider: "mailgun", FromEmail: "a@example.com", Mailgun: mailer.MailgunConfig{APIKey: "k", Domain: "x.com/../y", Region: "us"}},
		"mailgun without region":  {Provider: "mailgun", FromEmail: "a@example.com", Mailgun: mailer.MailgunConfig{APIKey: "k", Domain: "mg.example.com"}},
		"mailgun region asia":     {Provider: "mailgun", FromEmail: "a@example.com", Mailgun: mailer.MailgunConfig{APIKey: "k", Domain: "mg.example.com", Region: "asia"}},
		"mailgun region upper":    {Provider: "mailgun", FromEmail: "a@example.com", Mailgun: mailer.MailgunConfig{APIKey: "k", Domain: "mg.example.com", Region: "EU"}},
		"mixed mailgun+sendgrid":  {Provider: "mailgun", FromEmail: "a@example.com", Mailgun: mg, SendGrid: mailer.SendGridConfig{APIKey: "old"}},
		"mixed sendgrid+smtp2go":  {Provider: "sendgrid", FromEmail: "a@example.com", SendGrid: mailer.SendGridConfig{APIKey: "k"}, SMTP2GO: mailer.SMTP2GOConfig{APIKey: "old"}},
		"mixed smtp+mailgun":      {Provider: "smtp", FromEmail: "a@example.com", Mailgun: mg, SMTP: mailer.SMTPConfig{Host: "smtp.example.com", Port: 587, TLSMode: "starttls"}},
		"smtp without host":       {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Port: 587, TLSMode: "starttls"}},
		"smtp bad host":           {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Host: "smtp.example.com:25", Port: 587, TLSMode: "starttls"}},
		"smtp without port":       {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Host: "smtp.example.com", TLSMode: "starttls"}},
		"smtp port too large":     {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Host: "smtp.example.com", Port: 70000, TLSMode: "starttls"}},
		"smtp without tls mode":   {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Host: "smtp.example.com", Port: 587}},
		"smtp none tls":           {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Host: "smtp.example.com", Port: 25, TLSMode: "none"}},
		"smtp plain public":       {Provider: "smtp", FromEmail: "a@example.com", AllowInsecureLoopback: true, SMTP: mailer.SMTPConfig{Host: "smtp.example.com", Port: 25, TLSMode: "plain"}},
		"smtp plain no switch":    {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Host: "127.0.0.1", Port: 25, TLSMode: "plain"}},
		"smtp user without pass":  {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Host: "smtp.example.com", Port: 587, TLSMode: "starttls", Username: "u"}},
		"smtp pass without user":  {Provider: "smtp", FromEmail: "a@example.com", SMTP: mailer.SMTPConfig{Host: "smtp.example.com", Port: 587, TLSMode: "starttls", Password: "p"}},
		"api key with space":      {Provider: "sendgrid", FromEmail: "a@example.com", SendGrid: mailer.SendGridConfig{APIKey: "a b"}},
		"api key with newline":    {Provider: "smtp2go", FromEmail: "a@example.com", SMTP2GO: mailer.SMTP2GOConfig{APIKey: "a\r\nX: y"}},
		"no from":                 {Provider: "sendgrid", SendGrid: mailer.SendGridConfig{APIKey: "k"}},
		"from with display name":  {Provider: "sendgrid", FromEmail: "Bob <b@example.com>", SendGrid: mailer.SendGridConfig{APIKey: "k"}},
		"from crlf":               {Provider: "sendgrid", FromEmail: "a@example.com\r\nBcc: x@example.com", SendGrid: mailer.SendGridConfig{APIKey: "k"}},
		"from name crlf":          {Provider: "sendgrid", FromEmail: "a@example.com", FromName: "x\r\ny", SendGrid: mailer.SendGridConfig{APIKey: "k"}},
		"reply-to crlf":           {Provider: "sendgrid", FromEmail: "a@example.com", ReplyTo: "r@example.com\nBcc: x@example.com", SendGrid: mailer.SendGridConfig{APIKey: "k"}},
		"timeout too long":        {Provider: "sendgrid", FromEmail: "a@example.com", Timeout: "5m", SendGrid: mailer.SendGridConfig{APIKey: "k"}},
		"timeout garbage":         {Provider: "sendgrid", FromEmail: "a@example.com", Timeout: "soon", SendGrid: mailer.SendGridConfig{APIKey: "k"}},
		"negative generation":     {Provider: "sendgrid", FromEmail: "a@example.com", Generation: -1, SendGrid: mailer.SendGridConfig{APIKey: "k"}},
	}
	for name, c := range bad {
		err := c.Validate()
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		// An error text names the setting; it never repeats a secret.
		for _, secret := range []string{"supersecret", "old"} {
			if strings.Contains(err.Error(), "\""+secret) {
				t.Errorf("%s: error repeats a secret: %v", name, err)
			}
		}
	}
	c := mailer.Config{Provider: "sendgrid", FromEmail: "a@example.com", SendGrid: mailer.SendGridConfig{APIKey: "k"}}
	if err := c.Validate(); err != nil || c.Generation != 1 || c.Timeout != "10s" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	if _, err := mailer.New(baseConfig(), mailer.WithTLSConfig(&tls.Config{InsecureSkipVerify: true})); err == nil {
		t.Fatal("certificate verification can be switched off")
	}
}

// ---- HTTP adapters ---------------------------------------------------------------

type httpCase struct {
	name     string
	cfg      mailer.Config
	host     string
	path     string
	okStatus int
	okBody   string
}

func httpCases() []httpCase {
	from := func(c mailer.Config) mailer.Config {
		c.FromEmail, c.FromName, c.ReplyTo, c.Timeout = "no-reply@example.com", "山海足迹", "help@example.com", "2s"
		return c
	}
	return []httpCase{
		{"mailgun us", from(mailer.Config{Provider: "mailgun", Mailgun: mailer.MailgunConfig{APIKey: "key-supersecret999", Domain: "mg.example.com", Region: "us"}}),
			"api.mailgun.net", "/v3/mg.example.com/messages", 200, `{"id":"<20260101.1@mg.example.com>","message":"Queued. Thank you."}`},
		{"mailgun eu", from(mailer.Config{Provider: "mailgun", Mailgun: mailer.MailgunConfig{APIKey: "key-supersecret999", Domain: "mg.example.com", Region: "eu"}}),
			"api.eu.mailgun.net", "/v3/mg.example.com/messages", 200, `{"id":"<20260101.2@mg.example.com>","message":"Queued. Thank you."}`},
		{"sendgrid", from(mailer.Config{Provider: "sendgrid", SendGrid: mailer.SendGridConfig{APIKey: "SG.supersecretkey123"}}),
			"api.sendgrid.com", "/v3/mail/send", 202, ``},
		{"smtp2go", from(mailer.Config{Provider: "smtp2go", SMTP2GO: mailer.SMTP2GOConfig{APIKey: "api-supersecret555"}}),
			"api.smtp2go.com", "/v3/email/send", 200, `{"request_id":"r-1","data":{"succeeded":1,"failed":0,"failures":[],"email_id":"1abc-XyZ"}}`},
	}
}

func TestHTTPAdaptersTalkToTheFixedEndpointWithTheRightRequest(t *testing.T) {
	for _, tc := range httpCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newHTTPFake(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.okStatus)
				_, _ = io.WriteString(w, tc.okBody)
			})
			m, err := mailer.New(tc.cfg, mailer.WithTransport(f.transport()))
			if err != nil {
				t.Fatal(err)
			}
			msg := message(t, m)
			res, err := m.Send(context.Background(), msg)
			if err != nil {
				t.Fatal(err)
			}
			reqs := f.requests()
			if len(reqs) != 1 {
				t.Fatalf("%d requests", len(reqs))
			}
			r := reqs[0]
			if r.Host != tc.host || r.Path != tc.path || r.Method != http.MethodPost {
				t.Fatalf("addressed %s %s %s", r.Method, r.Host, r.Path)
			}
			checkRequest(t, tc, r, msg)
			if tc.name != "sendgrid" && res.ProviderID == "" {
				t.Fatal("no provider id")
			}
		})
	}
}

func checkRequest(t *testing.T, tc httpCase, r recorded, msg mailer.Message) {
	t.Helper()
	body := string(r.Body)
	// The key is in an auth header, never in the body or the URL.
	for _, secret := range []string{"supersecret"} {
		if strings.Contains(body, secret) || strings.Contains(r.Path, secret) {
			t.Fatal("secret in body or URL")
		}
	}
	switch tc.cfg.Provider {
	case "mailgun":
		user, pass, ok := basicAuth(r.Header)
		if !ok || user != "api" || pass != tc.cfg.Mailgun.APIKey {
			t.Fatalf("auth %q %q", user, pass)
		}
		if got := formValue(t, r.Body, "to"); got != msg.To {
			t.Fatalf("to %q", got)
		}
		if got := formValue(t, r.Body, "from"); got != `"山海足迹" <no-reply@example.com>` {
			t.Fatalf("from %q", got)
		}
		if got := formValue(t, r.Body, "h:Reply-To"); got != "help@example.com" {
			t.Fatalf("reply-to %q", got)
		}
		if formValue(t, r.Body, "subject") != msg.Subject || formValue(t, r.Body, "text") != msg.Text || formValue(t, r.Body, "html") != msg.HTML {
			t.Fatal("content differs")
		}
		for _, k := range []string{"o:tracking", "o:tracking-clicks", "o:tracking-opens"} {
			if formValue(t, r.Body, k) != "no" {
				t.Fatalf("%s not disabled", k)
			}
		}
	case "sendgrid":
		if r.Header.Get("Authorization") != "Bearer "+tc.cfg.SendGrid.APIKey {
			t.Fatalf("auth %q", r.Header.Get("Authorization"))
		}
		var got struct {
			Personalizations []struct {
				To []struct{ Email string } `json:"to"`
			} `json:"personalizations"`
			From    struct{ Email, Name string } `json:"from"`
			ReplyTo struct{ Email string }       `json:"reply_to"`
			Subject string                       `json:"subject"`
			Content []struct{ Type, Value string }
			Track   struct {
				Click struct{ Enable bool } `json:"click_tracking"`
				Open  struct{ Enable bool } `json:"open_tracking"`
			} `json:"tracking_settings"`
		}
		if err := json.Unmarshal(r.Body, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Personalizations) != 1 || len(got.Personalizations[0].To) != 1 || got.Personalizations[0].To[0].Email != msg.To {
			t.Fatalf("recipients %+v", got.Personalizations)
		}
		if got.From.Email != "no-reply@example.com" || got.From.Name != "山海足迹" || got.ReplyTo.Email != "help@example.com" || got.Subject != msg.Subject {
			t.Fatalf("envelope %+v", got)
		}
		if len(got.Content) != 2 || got.Content[0].Type != "text/plain" || got.Content[0].Value != msg.Text || got.Content[1].Type != "text/html" || got.Content[1].Value != msg.HTML {
			t.Fatal("content parts")
		}
		if got.Track.Click.Enable || got.Track.Open.Enable {
			t.Fatal("tracking is on")
		}
	case "smtp2go":
		if r.Header.Get("X-Smtp2go-Api-Key") != tc.cfg.SMTP2GO.APIKey {
			t.Fatal("api key header")
		}
		var got struct {
			Sender  string                           `json:"sender"`
			To      []string                         `json:"to"`
			Subject string                           `json:"subject"`
			Text    string                           `json:"text_body"`
			HTML    string                           `json:"html_body"`
			Headers []struct{ Header, Value string } `json:"custom_headers"`
		}
		if err := json.Unmarshal(r.Body, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.To) != 1 || got.To[0] != msg.To || got.Sender != `"山海足迹" <no-reply@example.com>` || got.Subject != msg.Subject ||
			got.Text != msg.Text || got.HTML != msg.HTML || len(got.Headers) != 1 || got.Headers[0].Header != "Reply-To" || got.Headers[0].Value != "help@example.com" {
			t.Fatalf("payload %+v", got)
		}
	}
	if r.Header.Get("Content-Type") == "" {
		t.Fatal("no content type")
	}
}

func basicAuth(h http.Header) (string, string, bool) {
	req := &http.Request{Header: h}
	return req.BasicAuth()
}

func TestHTTPStatusClassification(t *testing.T) {
	cases := []struct {
		status int
		class  mailer.Class
		auth   bool
	}{
		{400, mailer.ClassPermanent, false}, {401, mailer.ClassPermanent, true}, {403, mailer.ClassPermanent, true},
		{404, mailer.ClassPermanent, false}, {413, mailer.ClassPermanent, false}, {422, mailer.ClassPermanent, false},
		{408, mailer.ClassTemporary, false}, {429, mailer.ClassTemporary, false}, {503, mailer.ClassTemporary, false},
		{500, mailer.ClassUncertain, false}, {502, mailer.ClassUncertain, false}, {504, mailer.ClassUncertain, false},
	}
	for _, tc := range httpCases() {
		for _, c := range cases {
			f := newHTTPFake(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(c.status) })
			m, err := mailer.New(tc.cfg, mailer.WithTransport(f.transport()))
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.Send(context.Background(), message(t, m))
			var me *mailer.Error
			if !errors.As(err, &me) || me.Class != c.class || me.Auth != c.auth || me.Provider != tc.cfg.Provider {
				t.Errorf("%s HTTP %d: %+v", tc.name, c.status, err)
			}
		}
	}
}

func TestHTTPRedirectsAreRefusedAndNeverFollowed(t *testing.T) {
	for _, tc := range httpCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newHTTPFake(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://evil.example.com/steal")
				w.WriteHeader(http.StatusTemporaryRedirect)
			})
			m, _ := mailer.New(tc.cfg, mailer.WithTransport(f.transport()))
			_, err := m.Send(context.Background(), message(t, m))
			var me *mailer.Error
			if !errors.As(err, &me) || me.Class != mailer.ClassPermanent || me.Code != "redirect_refused" {
				t.Fatalf("%v", err)
			}
			if n := len(f.requests()); n != 1 {
				t.Fatalf("followed the redirect: %d requests", n)
			}
			// Every request, however many, only ever addressed the official host.
			for _, r := range f.requests() {
				if r.Host != tc.host {
					t.Fatalf("addressed %q", r.Host)
				}
			}
		})
	}
}

func TestHTTPResponseBodiesAreRedactedAndBounded(t *testing.T) {
	for _, tc := range httpCases() {
		t.Run(tc.name, func(t *testing.T) {
			secret := firstNonEmpty(tc.cfg.Mailgun.APIKey, tc.cfg.SendGrid.APIKey, tc.cfg.SMTP2GO.APIKey)
			body := "bad request for alice+tag@example.com key=" + secret + " see https://vendor.example.com/docs?token=abc and " +
				strings.Repeat("Z", 5000) + "\r\nInjected: header\x00"
			f := newHTTPFake(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, body)
			})
			m, _ := mailer.New(tc.cfg, mailer.WithTransport(f.transport()))
			_, err := m.Send(context.Background(), message(t, m))
			if err == nil {
				t.Fatal("no error")
			}
			text := err.Error()
			for _, leak := range []string{secret, "alice", "example.com", "vendor", "token=abc", "\r", "\n", "\x00", strings.Repeat("Z", 30)} {
				if strings.Contains(text, leak) {
					t.Fatalf("error text leaks %q: %s", leak, text)
				}
			}
			if len(text) > 400 {
				t.Fatalf("error text is not bounded: %d bytes", len(text))
			}
		})
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func TestHTTPTransportFailuresAreClassifiedByWhetherTheRequestWasWritten(t *testing.T) {
	for _, tc := range httpCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Nothing listens: the request never left, so a retry is safe.
			dead := rewriteTo("127.0.0.1:1")
			m, _ := mailer.New(tc.cfg, mailer.WithTransport(dead))
			_, err := m.Send(context.Background(), message(t, m))
			if mailer.ClassOf(err) != mailer.ClassTemporary {
				t.Fatalf("connection refused: %v", err)
			}

			// The server reads the request and drops the connection: unknown outcome.
			f := newHTTPFake(t, func(w http.ResponseWriter, r *http.Request) {
				conn, _, _ := w.(http.Hijacker).Hijack()
				_ = conn.Close()
			})
			m, _ = mailer.New(tc.cfg, mailer.WithTransport(f.transport()))
			_, err = m.Send(context.Background(), message(t, m))
			if mailer.ClassOf(err) != mailer.ClassUncertain {
				t.Fatalf("reset after the request: %v", err)
			}

			// The server accepts the request and stalls past the deadline.
			release := make(chan struct{})
			slow := newHTTPFake(t, func(w http.ResponseWriter, r *http.Request) { <-release })
			t.Cleanup(func() { close(release) })
			cfg := tc.cfg
			cfg.Timeout = "1s"
			m, _ = mailer.New(cfg, mailer.WithTransport(slow.transport()))
			start := time.Now()
			_, err = m.Send(context.Background(), message(t, m))
			var me *mailer.Error
			if !errors.As(err, &me) || me.Class != mailer.ClassUncertain || me.Code != "timeout" {
				t.Fatalf("timeout: %v", err)
			}
			if time.Since(start) > 4*time.Second {
				t.Fatalf("the deadline was not enforced: %v", time.Since(start))
			}

			// Cancelling the context stops the send and is not mistaken for success.
			ctx, cancel := context.WithCancel(context.Background())
			go func() { time.Sleep(100 * time.Millisecond); cancel() }()
			_, err = m.Send(ctx, message(t, m))
			if err == nil || mailer.ClassOf(err) == mailer.ClassPermanent {
				t.Fatalf("cancel: %v", err)
			}
		})
	}
}

func TestSMTP2GOVerdictInTheBody(t *testing.T) {
	cfg := httpCases()[3].cfg
	for _, c := range []struct {
		body  string
		class mailer.Class
		code  string
	}{
		{`{"data":{"succeeded":0,"failed":1,"failures":["x"]}}`, mailer.ClassPermanent, "recipient_rejected"},
		{`{"data":{"succeeded":0,"failed":0}}`, mailer.ClassUncertain, "no_verdict"},
		{`not json`, mailer.ClassUncertain, "unreadable_response"},
	} {
		f := newHTTPFake(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, c.body) })
		m, _ := mailer.New(cfg, mailer.WithTransport(f.transport()))
		_, err := m.Send(context.Background(), message(t, m))
		var me *mailer.Error
		if !errors.As(err, &me) || me.Class != c.class || me.Code != c.code {
			t.Errorf("%s: %v", c.body, err)
		}
	}
}

func TestHTTPAdaptersNeverSendTwice(t *testing.T) {
	// A failing request is attempted exactly once by the transport: no hidden retries.
	for _, tc := range httpCases() {
		f := newHTTPFake(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
		m, _ := mailer.New(tc.cfg, mailer.WithTransport(f.transport()))
		if _, err := m.Send(context.Background(), message(t, m)); err == nil {
			t.Fatal("no error")
		}
		if n := len(f.requests()); n != 1 {
			t.Fatalf("%s: %d requests", tc.name, n)
		}
	}
}

// ---- SMTP adapter ---------------------------------------------------------------

func smtpMailer(t *testing.T, f *fakeSMTP, mode string, user, pass string, tlsCfg *tls.Config) *mailer.Mailer {
	t.Helper()
	cfg := mailer.Config{
		Provider: "smtp", FromEmail: "no-reply@example.com", FromName: "山海足迹", ReplyTo: "help@example.com", Timeout: "3s",
		AllowInsecureLoopback: mode == "plain",
		SMTP:                  mailer.SMTPConfig{Host: "127.0.0.1", Port: f.port(), TLSMode: map[string]string{"plain": "plain", "implicit": "implicit_tls", "starttls": "starttls"}[mode], Username: user, Password: pass},
	}
	var opts []mailer.Option
	if tlsCfg != nil {
		opts = append(opts, mailer.WithTLSConfig(tlsCfg))
	}
	m, err := mailer.New(cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSMTPDeliversOverImplicitTLSAndStartTLSWithAuth(t *testing.T) {
	for _, tc := range []struct {
		name, mode, mechs string
	}{
		{"implicit tls plain auth", "implicit", "PLAIN LOGIN"},
		{"starttls plain auth", "starttls", "PLAIN"},
		{"starttls login auth", "starttls", "LOGIN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSMTPFake(t, smtpBehavior{mode: tc.mode, offerSTARTTLS: true, authMechs: tc.mechs})
			m := smtpMailer(t, f, tc.mode, "mailuser", "p@ss-w0rd", &tls.Config{RootCAs: f.pool})
			msg := message(t, m)
			if _, err := m.Send(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			s := f.last(t)
			if !s.TLS || s.User != "mailuser" || s.Pass != "p@ss-w0rd" || s.From != "no-reply@example.com" ||
				len(s.Rcpts) != 1 || s.Rcpts[0] != msg.To || s.EHLO != "example.com" {
				t.Fatalf("session %+v", s)
			}
			if strings.Contains(s.Clear, "AUTH") || strings.Contains(s.Clear, "MAIL FROM") || strings.Contains(s.Clear, "p@ss") {
				t.Fatalf("data was sent in clear text: %q", s.Clear)
			}
			if tc.mechs == "LOGIN" && s.AuthUsed != "LOGIN" {
				t.Fatalf("mechanism %s", s.AuthUsed)
			}
			checkMIME(t, s.Data, msg)
		})
	}
}

func TestSMTPPlainOnlyOnLoopbackDevelopment(t *testing.T) {
	f := newSMTPFake(t, smtpBehavior{mode: "plain", authMechs: "PLAIN"})
	m := smtpMailer(t, f, "plain", "u", "p", nil)
	if _, err := m.Send(context.Background(), message(t, m)); err != nil {
		t.Fatal(err)
	}
	if f.last(t).TLS {
		t.Fatal("expected a plain session")
	}
}

func checkMIME(t *testing.T, raw string, msg mailer.Message) {
	t.Helper()
	parsed, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subject != msg.Subject {
		t.Fatalf("subject %q %v", subject, err)
	}
	from, err := parsed.Header.AddressList("From")
	if err != nil || len(from) != 1 || from[0].Name != "山海足迹" || from[0].Address != "no-reply@example.com" {
		t.Fatalf("from %+v %v", from, err)
	}
	reply, err := parsed.Header.AddressList("Reply-To")
	if err != nil || len(reply) != 1 || reply[0].Address != "help@example.com" {
		t.Fatalf("reply-to %+v %v", reply, err)
	}
	to, err := parsed.Header.AddressList("To")
	if err != nil || len(to) != 1 || to[0].Address != msg.To {
		t.Fatalf("to %+v %v", to, err)
	}
	if len(parsed.Header["Subject"]) != 1 || len(parsed.Header["From"]) != 1 || parsed.Header.Get("Bcc") != "" || parsed.Header.Get("Cc") != "" {
		t.Fatal("unexpected or duplicated headers")
	}
	if id := parsed.Header.Get("Message-Id"); id != "<"+msg.ID+"@example.com>" {
		t.Fatalf("message-id %q", id)
	}
	mt, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("content type %q %v", mt, err)
	}
	mr := multipart.NewReader(parsed.Body, params["boundary"])
	var parts []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		// multipart.Reader decodes quoted-printable by itself (and drops the header).
		b, err := io.ReadAll(p)
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, strings.ReplaceAll(string(b), "\r\n", "\n")+"|"+p.Header.Get("Content-Type"))
	}
	if len(parts) != 2 {
		t.Fatalf("%d parts", len(parts))
	}
	if strings.Count(raw, "Content-Transfer-Encoding: quoted-printable") != 2 {
		t.Fatal("parts are not quoted-printable")
	}
	wantText := strings.ReplaceAll(msg.Text, "\r\n", "\n")
	wantHTML := strings.ReplaceAll(msg.HTML, "\r\n", "\n")
	if !strings.HasPrefix(parts[0], wantText) || !strings.Contains(parts[0], "text/plain") || !strings.HasPrefix(parts[1], wantHTML) || !strings.Contains(parts[1], "text/html") {
		t.Fatalf("parts differ:\n%q\n%q", parts[0], parts[1])
	}
}

func TestSMTPCertificateAndTLSFailuresAreRefused(t *testing.T) {
	// Implicit TLS and STARTTLS with an untrusted certificate: permanent, nothing sent.
	for _, mode := range []string{"implicit", "starttls"} {
		f := newSMTPFake(t, smtpBehavior{mode: mode, offerSTARTTLS: true, authMechs: "PLAIN"})
		m := smtpMailer(t, f, mode, "u", "secret-pass", nil) // system roots only: the test CA is unknown
		_, err := m.Send(context.Background(), message(t, m))
		var me *mailer.Error
		if !errors.As(err, &me) || me.Class != mailer.ClassPermanent || me.Code != "tls_verification" {
			t.Fatalf("%s: %v", mode, err)
		}
		if s := f.last(t); s.Data != "" || strings.Contains(s.Clear, "AUTH") || strings.Contains(s.Clear, "MAIL FROM") {
			t.Fatalf("%s: traffic after a failed verification: %+v", mode, s)
		}
	}
	// A server that never offers STARTTLS (or whose offer was stripped) is never used in clear text.
	f := newSMTPFake(t, smtpBehavior{mode: "starttls", offerSTARTTLS: false, authMechs: "PLAIN"})
	m := smtpMailer(t, f, "starttls", "u", "secret-pass", &tls.Config{RootCAs: f.pool})
	_, err := m.Send(context.Background(), message(t, m))
	var me *mailer.Error
	if !errors.As(err, &me) || me.Class != mailer.ClassPermanent || me.Code != "starttls_unavailable" {
		t.Fatalf("%v", err)
	}
	if s := f.last(t); strings.Contains(s.Clear, "AUTH") || strings.Contains(s.Clear, "MAIL FROM") || strings.Contains(s.Clear, "secret-pass") || s.Data != "" {
		t.Fatalf("clear-text fallback: %+v", s)
	}
	// A failing STARTTLS command terminates the send too.
	f = newSMTPFake(t, smtpBehavior{mode: "starttls", offerSTARTTLS: true, startTLSFails: true})
	m = smtpMailer(t, f, "starttls", "", "", &tls.Config{RootCAs: f.pool})
	_, err = m.Send(context.Background(), message(t, m))
	if err == nil || f.last(t).Data != "" || strings.Contains(f.last(t).Clear, "MAIL FROM") {
		t.Fatalf("starttls failure: %v %+v", err, f.last(t))
	}
	// A wrong host name is refused as well: the certificate only names 127.0.0.1.
	g := newSMTPFake(t, smtpBehavior{mode: "implicit"})
	cfg := mailer.Config{Provider: "smtp", FromEmail: "no-reply@example.com", Timeout: "3s",
		SMTP: mailer.SMTPConfig{Host: "localhost", Port: g.port(), TLSMode: "implicit_tls"}}
	hm, err := mailer.New(cfg, mailer.WithTLSConfig(&tls.Config{RootCAs: g.pool}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = hm.Send(context.Background(), message(t, hm)); mailer.ClassOf(err) != mailer.ClassPermanent {
		t.Fatalf("host name mismatch: %v", err)
	}
}

func TestSMTPErrorClassification(t *testing.T) {
	cases := []struct {
		name  string
		b     smtpBehavior
		class mailer.Class
		auth  bool
		code  string
	}{
		{"greeting 421", smtpBehavior{greeting: "421 busy"}, mailer.ClassTemporary, false, "smtp_421"},
		{"greeting 554", smtpBehavior{greeting: "554 go away"}, mailer.ClassPermanent, false, "smtp_554"},
		{"auth rejected", smtpBehavior{authMechs: "PLAIN", authFail: "535 5.7.8 bad credentials"}, mailer.ClassPermanent, true, "smtp_535"},
		{"auth temporary", smtpBehavior{authMechs: "PLAIN", authFail: "454 try later"}, mailer.ClassTemporary, false, "smtp_454"},
		{"auth not offered", smtpBehavior{}, mailer.ClassPermanent, true, "auth_unsupported"},
		{"sender rejected", smtpBehavior{mailReply: "553 sender not allowed"}, mailer.ClassPermanent, false, "smtp_553"},
		{"recipient permanent", smtpBehavior{rcptReply: "550 no such user"}, mailer.ClassPermanent, false, "smtp_550"},
		{"recipient temporary", smtpBehavior{rcptReply: "450 mailbox busy"}, mailer.ClassTemporary, false, "smtp_450"},
		{"data refused", smtpBehavior{dataReply: "554 no"}, mailer.ClassPermanent, false, "smtp_554"},
		{"final temporary", smtpBehavior{finalReply: "451 try later"}, mailer.ClassTemporary, false, "smtp_451"},
		{"final permanent", smtpBehavior{finalReply: "554 rejected as spam"}, mailer.ClassPermanent, false, "smtp_554"},
		{"dropped after terminator", smtpBehavior{dropAfterData: true}, mailer.ClassUncertain, false, "final_reply_network"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.b
			b.mode = "plain"
			if b.authMechs == "" && !strings.HasPrefix(tc.name, "auth not") {
				b.authMechs = "PLAIN"
			}
			f := newSMTPFake(t, b)
			user, pass := "", ""
			if strings.HasPrefix(tc.name, "auth") {
				user, pass = "u", "secretpassword"
			}
			m := smtpMailer(t, f, "plain", user, pass, nil)
			_, err := m.Send(context.Background(), message(t, m))
			var me *mailer.Error
			if !errors.As(err, &me) {
				t.Fatalf("%v", err)
			}
			if me.Class != tc.class || me.Auth != tc.auth || me.Code != tc.code {
				t.Fatalf("got %+v", me)
			}
			if strings.Contains(me.Error(), "secretpassword") || strings.Contains(me.Error(), "no such user") || strings.Contains(me.Error(), "spam") {
				t.Fatalf("vendor text or secret in %q", me.Error())
			}
		})
	}
}

func TestSMTPTimeoutWaitingForTheFinalReplyIsUncertain(t *testing.T) {
	f := newSMTPFake(t, smtpBehavior{mode: "plain", hangAfterData: true})
	cfg := mailer.Config{Provider: "smtp", FromEmail: "no-reply@example.com", Timeout: "1s", AllowInsecureLoopback: true,
		SMTP: mailer.SMTPConfig{Host: "127.0.0.1", Port: f.port(), TLSMode: "plain"}}
	m, err := mailer.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = m.Send(context.Background(), message(t, m))
	var me *mailer.Error
	if !errors.As(err, &me) || me.Class != mailer.ClassUncertain || me.Code != "final_reply_timeout" {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("deadline not enforced: %v", time.Since(start))
	}
}

func TestSMTPConnectionRefusedIsTemporaryAndCancelStops(t *testing.T) {
	cfg := mailer.Config{Provider: "smtp", FromEmail: "no-reply@example.com", Timeout: "2s", AllowInsecureLoopback: true,
		SMTP: mailer.SMTPConfig{Host: "127.0.0.1", Port: 1, TLSMode: "plain"}}
	m, _ := mailer.New(cfg)
	if _, err := m.Send(context.Background(), message(t, m)); mailer.ClassOf(err) != mailer.ClassTemporary {
		t.Fatalf("%v", err)
	}
	f := newSMTPFake(t, smtpBehavior{mode: "plain", hangAfterData: true})
	cfg.SMTP.Port = f.port()
	cfg.Timeout = "5s"
	m, _ = mailer.New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := m.Send(ctx, message(t, m))
	if err == nil || time.Since(start) > 3*time.Second || mailer.ClassOf(err) == mailer.ClassPermanent {
		t.Fatalf("cancel: %v after %v", err, time.Since(start))
	}
}

func TestErrorTextCarriesNoCredentialsEvenWhenTheVendorEchoesThem(t *testing.T) {
	cfg := httpCases()[0].cfg
	f := newHTTPFake(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = io.WriteString(w, "internal error for key "+cfg.Mailgun.APIKey)
	})
	m, _ := mailer.New(cfg, mailer.WithTransport(f.transport()))
	_, err := m.Send(context.Background(), message(t, m))
	if err == nil || strings.Contains(err.Error(), cfg.Mailgun.APIKey) || strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("%v", err)
	}
}
