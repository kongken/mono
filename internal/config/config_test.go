package config

import (
	"os"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestRequiredStores(t *testing.T) {
	if (&AppConfig{}).Validate() == nil {
		t.Fatal("empty configuration accepted")
	}
	cfg := &AppConfig{DBStore: "main", RedisStore: "main", Storage: StorageConfig{S3Store: "main"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthConfigValidation(t *testing.T) {
	base := func() *AppConfig {
		return &AppConfig{DBStore: "main", RedisStore: "main", Storage: StorageConfig{S3Store: "main"}}
	}
	if got := base().SessionLifetime(); got != DefaultSessionTTL {
		t.Fatalf("default session lifetime = %v", got)
	}
	cfg := base()
	cfg.Auth.CORSAllowedOrigins = []string{"https://trip.example.com", "http://localhost:5173"}
	cfg.Auth.OAuthProviders = map[string]OAuthProvider{
		"github": {ClientID: "id", ClientSecret: "secret", RedirectURL: "https://trip.example.com/auth/callback/github"},
		"google": {}, // unconfigured providers are ignored
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*AppConfig){
		"origin with path":      func(c *AppConfig) { c.Auth.CORSAllowedOrigins = []string{"https://a.example.com/app"} },
		"origin trailing slash": func(c *AppConfig) { c.Auth.CORSAllowedOrigins = []string{"https://a.example.com/"} },
		"wildcard origin":       func(c *AppConfig) { c.Auth.CORSAllowedOrigins = []string{"*"} },
		"oauth without redirect": func(c *AppConfig) {
			c.Auth.OAuthProviders = map[string]OAuthProvider{"github": {ClientID: "id", ClientSecret: "secret"}}
		},
		"negative ttl": func(c *AppConfig) { c.Auth.SessionTTL = -1 },
	} {
		cfg := base()
		mutate(cfg)
		if cfg.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestExampleConfigParses(t *testing.T) {
	raw, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Skip("config.example.yaml is not part of this build context")
	}
	var cfg AppConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.SessionLifetime() != 168*time.Hour || cfg.Auth.RegistrationDisabled {
		t.Fatalf("unexpected auth defaults: %+v", cfg.Auth)
	}
	if len(cfg.Auth.OAuthProviders) != 2 || cfg.Auth.OAuthProviders["github"].Enabled() {
		t.Fatalf("example OAuth providers should be listed but disabled: %+v", cfg.Auth.OAuthProviders)
	}
}

func TestShareConfigValidation(t *testing.T) {
	base := func() *AppConfig {
		return &AppConfig{DBStore: "main", RedisStore: "main", Storage: StorageConfig{S3Store: "main"}}
	}
	cfg := base()
	if cfg.Share.Enabled() {
		t.Fatal("sharing must be off by default")
	}
	cfg.Share = ShareConfig{PublicBaseURL: "https://trip.example.com/", AppIndexURL: "https://trip.example.com/index.html"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Share.BaseURL(); got != "https://trip.example.com" {
		t.Fatalf("base URL = %q", got)
	}
	for name, share := range map[string]ShareConfig{
		"index without base":    {AppIndexURL: "https://trip.example.com/"},
		"relative base":         {PublicBaseURL: "/s"},
		"ftp base":              {PublicBaseURL: "ftp://trip.example.com"},
		"credentials":           {PublicBaseURL: "https://user:pw@trip.example.com"},
		"query":                 {PublicBaseURL: "https://trip.example.com/?a=b"},
		"fragment":              {PublicBaseURL: "https://trip.example.com/#x"},
		"index into share":      {PublicBaseURL: "https://trip.example.com", AppIndexURL: "https://trip.example.com/s/abc"},
		"index is share root":   {PublicBaseURL: "https://trip.example.com", AppIndexURL: "https://trip.example.com/s"},
		"index into api":        {PublicBaseURL: "https://trip.example.com", AppIndexURL: "https://trip.example.com/api/x"},
		"index with credential": {PublicBaseURL: "https://trip.example.com", AppIndexURL: "https://u:p@trip.example.com/"},
		"negative limit":        {PublicBaseURL: "https://trip.example.com", PageRatePerMinute: -1},
	} {
		cfg := base()
		cfg.Share = share
		if cfg.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestNotificationConfigValidation(t *testing.T) {
	base := func() *AppConfig {
		return &AppConfig{DBStore: "main", RedisStore: "main", Storage: StorageConfig{S3Store: "main"}}
	}
	cfg := base()
	if cfg.Notification.EmailEnabled() || cfg.Notification.PushEnabled() {
		t.Fatal("channels must be off by default")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	secret := "0123456789abcdef0123456789abcdef"
	cfg.Notification = NotificationConfig{
		PublicBaseURL: "https://trip.example.com/", Secret: secret,
		SMTP:    SMTPConfig{Host: "smtp.example.com", Port: 587, Username: "u", Password: "p", From: "山海足迹 <noreply@example.com>"},
		WebPush: WebPushConfig{VAPIDPublicKey: "pub", VAPIDPrivateKey: "priv", Subject: "mailto:ops@example.com"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Notification.EmailEnabled() || !cfg.Notification.PushEnabled() || cfg.Notification.BaseURL() != "https://trip.example.com" || cfg.Notification.SMTPSecurity() != "starttls" {
		t.Fatalf("%+v", cfg.Notification)
	}
	for name, mutate := range map[string]func(*NotificationConfig){
		"short secret":         func(n *NotificationConfig) { n.Secret = "short" },
		"smtp without from":    func(n *NotificationConfig) { n.SMTP.From = "" },
		"bad from":             func(n *NotificationConfig) { n.SMTP.From = "not an address" },
		"bad tls":              func(n *NotificationConfig) { n.SMTP.TLS = "ssl3" },
		"bad port":             func(n *NotificationConfig) { n.SMTP.Port = 70000 },
		"user without pass":    func(n *NotificationConfig) { n.SMTP.Password = "" },
		"smtp without base":    func(n *NotificationConfig) { n.PublicBaseURL = "" },
		"base with query":      func(n *NotificationConfig) { n.PublicBaseURL = "https://trip.example.com/?x=1" },
		"push one key":         func(n *NotificationConfig) { n.WebPush.VAPIDPrivateKey = "" },
		"push bad subject":     func(n *NotificationConfig) { n.WebPush.Subject = "ops@example.com" },
		"push over plain http": func(n *NotificationConfig) { n.PublicBaseURL = "http://trip.example.com" },
	} {
		cfg := base()
		cfg.Notification = NotificationConfig{
			PublicBaseURL: "https://trip.example.com", Secret: secret,
			SMTP:    SMTPConfig{Host: "smtp.example.com", Username: "u", Password: "p", From: "noreply@example.com"},
			WebPush: WebPushConfig{VAPIDPublicKey: "pub", VAPIDPrivateKey: "priv", Subject: "mailto:ops@example.com"},
		}
		mutate(&cfg.Notification)
		if cfg.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Push alone works on a local http address for development.
	cfg = base()
	cfg.Notification = NotificationConfig{PublicBaseURL: "http://localhost:5173", WebPush: WebPushConfig{VAPIDPublicKey: "pub", VAPIDPrivateKey: "priv", Subject: "mailto:dev@example.com"}}
	if err := cfg.Validate(); err != nil || cfg.Notification.EmailEnabled() || !cfg.Notification.PushEnabled() {
		t.Fatalf("local push: %v", err)
	}
}
