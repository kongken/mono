package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"time"
)

// Provider names accepted by mail.provider.
const (
	ProviderMailgun  = "mailgun"
	ProviderSendGrid = "sendgrid"
	ProviderSMTP     = "smtp"
	ProviderSMTP2GO  = "smtp2go"
)

// SMTP TLS modes. "plain" exists only for a local development server on a
// loopback address and needs Config.AllowInsecureLoopback.
const (
	TLSImplicit = "implicit_tls"
	TLSStartTLS = "starttls"
	TLSPlain    = "plain"
)

// DefaultTimeout is the per-send deadline.
const DefaultTimeout = 10 * time.Second

// Config is the mail section of the usersrv configuration. Exactly one provider
// is active; the section of any other provider must stay empty so a stale
// credential of a former vendor cannot linger unnoticed.
type Config struct {
	// Provider is mailgun, sendgrid, smtp or smtp2go.
	Provider string `yaml:"provider"`
	// Generation is the configuration generation (default 1). A queued job is
	// pinned to the provider and generation it was issued under; bump it when
	// switching vendors so jobs of the old setup are cancelled instead of being
	// sent through another vendor.
	Generation int `yaml:"generation"`
	// FromEmail, FromName and ReplyTo are fixed server-side; no request can
	// override them.
	FromEmail string `yaml:"from_email"`
	FromName  string `yaml:"from_name"`
	ReplyTo   string `yaml:"reply_to"`
	// Timeout bounds one send (default 10s, at most 60s), such as "10s".
	Timeout string `yaml:"timeout"`
	// AllowInsecureLoopback permits smtp.tls_mode "plain" towards a loopback host,
	// for a local development mail catcher only.
	AllowInsecureLoopback bool `yaml:"allow_insecure_loopback"`

	Mailgun  MailgunConfig  `yaml:"mailgun"`
	SendGrid SendGridConfig `yaml:"sendgrid"`
	SMTP     SMTPConfig     `yaml:"smtp"`
	SMTP2GO  SMTP2GOConfig  `yaml:"smtp2go"`
}

// MailgunConfig is the Mailgun HTTP API setup.
type MailgunConfig struct {
	APIKey string `yaml:"api_key"`
	// Domain is the sending domain registered at Mailgun.
	Domain string `yaml:"domain"`
	// Region is "us" (api.mailgun.net) or "eu" (api.eu.mailgun.net). The other
	// region is never tried.
	Region string `yaml:"region"`
}

// SendGridConfig is the SendGrid v3 HTTP API setup (endpoint api.sendgrid.com).
type SendGridConfig struct {
	// APIKey should be a key restricted to "Mail Send".
	APIKey string `yaml:"api_key"`
}

// SMTP2GOConfig is the SMTP2GO HTTP API setup (not its SMTP relay).
type SMTP2GOConfig struct {
	APIKey string `yaml:"api_key"`
}

// SMTPConfig is a standard SMTP submission server.
type SMTPConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// TLSMode is implicit_tls (TLS from the first byte, usually port 465) or
	// starttls (usually 587; a missing or failed upgrade aborts the send).
	TLSMode string `yaml:"tls_mode"`
	// Username and Password are set together or not at all.
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

var (
	domainPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`)
	hostPattern   = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)
)

// credential accepts a printable ASCII secret without whitespace.
func credential(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] >= 0x7f {
			return false
		}
	}
	return true
}

// Validate checks the configuration and fills the defaults. It never contacts a
// vendor. Error texts name the setting, never its value.
func (c *Config) Validate() error {
	if c.Generation == 0 {
		c.Generation = 1
	}
	if c.Generation < 1 || c.Generation > 1_000_000 {
		return errors.New("mail.generation must be a positive integer")
	}
	if c.Timeout == "" {
		c.Timeout = DefaultTimeout.String()
	}
	if d, err := time.ParseDuration(c.Timeout); err != nil || d < time.Second || d > time.Minute {
		return errors.New("mail.timeout must be a duration between 1s and 1m")
	}
	if err := validateAddress("mail.from_email", c.FromEmail); err != nil {
		return errors.New("mail.from_email must be one plain ASCII address")
	}
	if err := validateName("from", c.FromName); err != nil {
		return errors.New("mail.from_name must be at most 100 characters without control characters")
	}
	if c.ReplyTo != "" {
		if err := validateAddress("mail.reply_to", c.ReplyTo); err != nil {
			return errors.New("mail.reply_to must be one plain ASCII address")
		}
	}
	// Only the selected provider may carry settings.
	sections := map[string]bool{
		ProviderMailgun:  c.Mailgun != (MailgunConfig{}),
		ProviderSendGrid: c.SendGrid != (SendGridConfig{}),
		ProviderSMTP:     c.SMTP != (SMTPConfig{}),
		ProviderSMTP2GO:  c.SMTP2GO != (SMTP2GOConfig{}),
	}
	if c.Provider == "" {
		return errors.New("mail.provider is required: mailgun, sendgrid, smtp or smtp2go")
	}
	if _, known := sections[c.Provider]; !known {
		return errors.New("mail.provider must be one of mailgun, sendgrid, smtp, smtp2go")
	}
	for name, used := range sections {
		if used && name != c.Provider {
			return fmt.Errorf("mail.%s has settings but mail.provider is %s: remove the unused vendor's settings", name, c.Provider)
		}
	}
	switch c.Provider {
	case ProviderMailgun:
		return c.Mailgun.validate()
	case ProviderSendGrid:
		if !credential(c.SendGrid.APIKey, 512) {
			return errors.New("mail.sendgrid.api_key is required")
		}
	case ProviderSMTP2GO:
		if !credential(c.SMTP2GO.APIKey, 512) {
			return errors.New("mail.smtp2go.api_key is required")
		}
	case ProviderSMTP:
		return c.SMTP.validate(c.AllowInsecureLoopback)
	}
	return nil
}

func (m MailgunConfig) validate() error {
	if !credential(m.APIKey, 512) {
		return errors.New("mail.mailgun.api_key is required")
	}
	if !domainPattern.MatchString(m.Domain) {
		return errors.New("mail.mailgun.domain must be the sending domain, such as mg.example.com")
	}
	if m.Region != "us" && m.Region != "eu" {
		return errors.New(`mail.mailgun.region must be "us" or "eu"`)
	}
	return nil
}

func (s SMTPConfig) validate(allowInsecureLoopback bool) error {
	if s.Host == "" || !(hostPattern.MatchString(s.Host) || net.ParseIP(s.Host) != nil) {
		return errors.New("mail.smtp.host must be a host name or IP address")
	}
	if s.Port < 1 || s.Port > 65535 {
		return errors.New("mail.smtp.port must be between 1 and 65535")
	}
	switch s.TLSMode {
	case TLSImplicit, TLSStartTLS:
	case TLSPlain:
		if !allowInsecureLoopback || !IsLoopbackHost(s.Host) {
			return errors.New(`mail.smtp.tls_mode "plain" is only allowed for a loopback host with mail.allow_insecure_loopback`)
		}
	default:
		return errors.New(`mail.smtp.tls_mode must be "implicit_tls" or "starttls"`)
	}
	if (s.Username == "") != (s.Password == "") {
		return errors.New("mail.smtp.username and mail.smtp.password must be set together")
	}
	if s.Username != "" && (!credential(s.Username, 256) || !credential(s.Password, 512)) {
		return errors.New("mail.smtp.username and mail.smtp.password must be printable ASCII without spaces")
	}
	return nil
}

// secrets lists the configured credentials, for redaction of vendor texts.
func (c Config) secrets() []string {
	var out []string
	for _, s := range []string{c.Mailgun.APIKey, c.SendGrid.APIKey, c.SMTP2GO.APIKey, c.SMTP.Password} {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Mailer is the configured sender plus the fixed envelope data. It implements
// Sender and stamps the configured From and Reply-To on every message, so the
// sender identity cannot be chosen by a caller.
type Mailer struct {
	cfg    Config
	sender Sender
}

// Option adjusts how a Mailer is built. Options exist for tests: production code
// passes none.
type Option func(*options)

type options struct {
	transport http.RoundTripper
	tlsConfig *tls.Config
}

// WithTransport replaces the HTTP transport of the HTTP vendors with a
// controlled one (tests only). Redirects stay disabled whatever the transport.
func WithTransport(rt http.RoundTripper) Option { return func(o *options) { o.transport = rt } }

// WithTLSConfig supplies the TLS settings of the SMTP adapter, such as the root
// CAs of a test server (tests only). Certificate and host name verification
// cannot be switched off: InsecureSkipVerify is rejected.
func WithTLSConfig(c *tls.Config) Option { return func(o *options) { o.tlsConfig = c } }

// New validates cfg and builds the adapter of its provider.
func New(cfg Config, opts ...Option) (*Mailer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if o.tlsConfig != nil && o.tlsConfig.InsecureSkipVerify {
		return nil, errors.New("mailer: certificate verification cannot be disabled")
	}
	timeout, _ := time.ParseDuration(cfg.Timeout)
	var s Sender
	switch cfg.Provider {
	case ProviderMailgun:
		s = newMailgun(cfg.Mailgun, timeout, o.transport, cfg.secrets())
	case ProviderSendGrid:
		s = newSendGrid(cfg.SendGrid, timeout, o.transport, cfg.secrets())
	case ProviderSMTP2GO:
		s = newSMTP2GO(cfg.SMTP2GO, timeout, o.transport, cfg.secrets())
	case ProviderSMTP:
		s = newSMTP(cfg.SMTP, timeout, o.tlsConfig, cfg.secrets())
	}
	return &Mailer{cfg: cfg, sender: s}, nil
}

// NewWithSender wraps a custom Sender (tests only) under a validated config.
func NewWithSender(cfg Config, s Sender) (*Mailer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Mailer{cfg: cfg, sender: s}, nil
}

// Provider returns the active provider name.
func (m *Mailer) Provider() string { return m.cfg.Provider }

// Timeout returns the per-send deadline.
func (m *Mailer) Timeout() time.Duration {
	d, err := time.ParseDuration(m.cfg.Timeout)
	if err != nil {
		return DefaultTimeout
	}
	return d
}

// Generation returns the configuration generation.
func (m *Mailer) Generation() int { return m.cfg.Generation }

// Compose builds the message for one recipient from rendered content, using the
// configured sender identity. id is the outbox job ID.
func (m *Mailer) Compose(id, to string, c Content) Message {
	msg := Message{
		ID:      id,
		From:    Address{Email: m.cfg.FromEmail, Name: m.cfg.FromName},
		To:      to,
		Subject: c.Subject,
		Text:    c.Text,
		HTML:    c.HTML,
	}
	if m.cfg.ReplyTo != "" {
		msg.ReplyTo = Address{Email: m.cfg.ReplyTo}
	}
	return msg
}

// Send validates and delivers msg through the configured provider. The From and
// Reply-To of the message are overwritten with the configured ones.
func (m *Mailer) Send(ctx context.Context, msg Message) (Result, error) {
	msg.From = Address{Email: m.cfg.FromEmail, Name: m.cfg.FromName}
	msg.ReplyTo = Address{}
	if m.cfg.ReplyTo != "" {
		msg.ReplyTo = Address{Email: m.cfg.ReplyTo}
	}
	if err := msg.Validate(); err != nil {
		return Result{}, invalidMessage(m.cfg.Provider, err)
	}
	return m.sender.Send(ctx, msg)
}

var _ Sender = (*Mailer)(nil)
