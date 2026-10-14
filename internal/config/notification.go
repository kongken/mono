package config

import (
	"fmt"
	"net/mail"
	"net/url"
	"strings"
)

// NotificationConfig controls memory reminders and other notifications (F12).
// Each channel is optional and stays unavailable until fully configured, so the
// default deployment sends nothing.
type NotificationConfig struct {
	// PublicBaseURL is the address people use for the site, such as
	// https://trip.example.com. Email links (open the app, unsubscribe) and the
	// target of a tapped push notification are built from it, never from a request
	// header. /api/ must reach this backend.
	PublicBaseURL string `yaml:"public_base_url"`
	// Secret signs one-click unsubscribe links (at least 32 characters). Changing
	// it invalidates links already sent. Required for the email channel.
	Secret  string        `yaml:"secret"`
	SMTP    SMTPConfig    `yaml:"smtp"`
	WebPush WebPushConfig `yaml:"web_push"`
}

// SMTPConfig is the outgoing mail server for the email channel.
type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	// From is the sender, for example "山海足迹 <noreply@example.com>".
	From string `yaml:"from"`
	// TLS is "starttls" (default), "tls" (implicit TLS, usually port 465) or
	// "none" (only for a local test server).
	TLS string `yaml:"tls"`
}

// WebPushConfig holds the VAPID key pair for browser push (generate it with
// make vapid-keys).
type WebPushConfig struct {
	VAPIDPublicKey  string `yaml:"vapid_public_key"`
	VAPIDPrivateKey string `yaml:"vapid_private_key"`
	// Subject is a mailto: or https: contact the push services may use.
	Subject string `yaml:"subject"`
}

// EmailEnabled reports whether the email channel can send and be unsubscribed from.
func (n NotificationConfig) EmailEnabled() bool {
	return n.SMTP.Host != "" && n.SMTP.From != "" && n.Secret != "" && n.PublicBaseURL != ""
}

// PushEnabled reports whether browser push is configured.
func (n NotificationConfig) PushEnabled() bool {
	return n.WebPush.VAPIDPublicKey != "" && n.WebPush.VAPIDPrivateKey != ""
}

// BaseURL is PublicBaseURL without a trailing slash.
func (n NotificationConfig) BaseURL() string { return strings.TrimRight(n.PublicBaseURL, "/") }

// SMTPSecurity returns the normalised TLS mode.
func (n NotificationConfig) SMTPSecurity() string {
	if n.SMTP.TLS == "" {
		return "starttls"
	}
	return n.SMTP.TLS
}

func (n NotificationConfig) validate() error {
	smtp := n.SMTP
	anySMTP := smtp.Host != "" || smtp.From != "" || smtp.Username != "" || smtp.Password != "" || smtp.Port != 0
	push := n.WebPush
	anyPush := push.VAPIDPublicKey != "" || push.VAPIDPrivateKey != "" || push.Subject != ""
	if n.PublicBaseURL != "" {
		if err := shareURL("notification.public_base_url", n.PublicBaseURL, true); err != nil {
			return err
		}
	}
	if anySMTP {
		if smtp.Host == "" || smtp.From == "" {
			return fmt.Errorf("notification.smtp.host and notification.smtp.from must be set together")
		}
		if _, err := mail.ParseAddress(smtp.From); err != nil {
			return fmt.Errorf("notification.smtp.from: %w", err)
		}
		if smtp.Port < 0 || smtp.Port > 65535 {
			return fmt.Errorf("notification.smtp.port must be between 1 and 65535")
		}
		switch n.SMTPSecurity() {
		case "starttls", "tls", "none":
		default:
			return fmt.Errorf("notification.smtp.tls must be starttls, tls or none")
		}
		if (smtp.Username == "") != (smtp.Password == "") {
			return fmt.Errorf("notification.smtp.username and notification.smtp.password must be set together")
		}
		if len(n.Secret) < 32 {
			return fmt.Errorf("notification.secret must be at least 32 characters when the email channel is configured")
		}
		if n.PublicBaseURL == "" {
			return fmt.Errorf("notification.public_base_url is required when the email channel is configured")
		}
	} else if n.Secret != "" && len(n.Secret) < 32 {
		return fmt.Errorf("notification.secret must be at least 32 characters")
	}
	if anyPush {
		if push.VAPIDPublicKey == "" || push.VAPIDPrivateKey == "" {
			return fmt.Errorf("notification.web_push.vapid_public_key and vapid_private_key must be set together")
		}
		if !(strings.HasPrefix(push.Subject, "mailto:") && len(push.Subject) > len("mailto:")) && !strings.HasPrefix(push.Subject, "https://") {
			return fmt.Errorf("notification.web_push.subject must be a mailto: or https: contact")
		}
		if n.PublicBaseURL == "" {
			return fmt.Errorf("notification.public_base_url is required when web push is configured")
		}
		if u, _ := url.Parse(n.PublicBaseURL); u != nil && u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
			return fmt.Errorf("notification.public_base_url must be https for web push")
		}
	}
	return nil
}
