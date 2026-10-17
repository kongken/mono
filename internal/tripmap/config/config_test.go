package config

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/tripmap/tiles"
	"gopkg.in/yaml.v3"
)

// valid returns a configuration that passes Validate; tests mutate it.
func valid() *AppConfig {
	return &AppConfig{
		DBStore: "main", RedisStore: "main", Storage: StorageConfig{S3Store: "main"},
		GRPC:         grpcx.ServerConfig{Listen: ":18083", CertFile: "server.pem", KeyFile: "server.key"},
		InternalAuth: servicetoken.VerifyConfig{Issuer: "bff", Audience: "tripmap", TrustedKeys: []servicetoken.TrustedKey{{KID: "k1", PublicKeyFile: "bff.pub"}}},
	}
}

func TestInternalTransportIsRequired(t *testing.T) {
	for name, mutate := range map[string]func(*AppConfig){
		"no grpc listener": func(c *AppConfig) { c.GRPC = grpcx.ServerConfig{} },
		"no tls key":       func(c *AppConfig) { c.GRPC.KeyFile = "" },
		"no trusted keys":  func(c *AppConfig) { c.InternalAuth.TrustedKeys = nil },
		"no audience":      func(c *AppConfig) { c.InternalAuth.Audience = "" },
	} {
		cfg := valid()
		mutate(cfg)
		if cfg.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRequiredStores(t *testing.T) {
	if (&AppConfig{}).Validate() == nil {
		t.Fatal("empty configuration accepted")
	}
	cfg := valid()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthConfigValidation(t *testing.T) {
	base := func() *AppConfig { return valid() }
	cfg := base()
	cfg.Auth.CORSAllowedOrigins = []string{"https://trip.example.com", "http://localhost:5173"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*AppConfig){
		"origin with path":      func(c *AppConfig) { c.Auth.CORSAllowedOrigins = []string{"https://a.example.com/app"} },
		"origin trailing slash": func(c *AppConfig) { c.Auth.CORSAllowedOrigins = []string{"https://a.example.com/"} },
		"wildcard origin":       func(c *AppConfig) { c.Auth.CORSAllowedOrigins = []string{"*"} },
	} {
		cfg := base()
		mutate(cfg)
		if cfg.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestExampleConfigParses(t *testing.T) {
	raw, err := os.ReadFile("../../../config.example.yaml")
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
	if cfg.Auth.ClientIPHeader != "X-Real-IP" {
		t.Fatalf("unexpected auth defaults: %+v", cfg.Auth)
	}
}

func TestShareConfigValidation(t *testing.T) {
	base := func() *AppConfig {
		return valid()
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
		return valid()
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

const mapboxTestToken = "pk.eyJ1IjoidGVzdC11c2VyIiwiYSI6InRlc3QifQ.c2lnbmF0dXJlLXZhbHVl"

func TestMapboxTilesConfigValidation(t *testing.T) {
	cfg := valid()
	if cfg.MapboxTiles.Enabled {
		t.Fatal("the Mapbox proxy must be off by default")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled proxy: %v", err)
	}
	enabled := func() *AppConfig {
		c := valid()
		c.MapboxTiles = tiles.Config{Enabled: true, Token: mapboxTestToken, Username: "mapbox", StyleID: "light-v11"}
		return c
	}
	if err := enabled().Validate(); err != nil {
		t.Fatalf("enabled with defaults: %v", err)
	}
	for name, mutate := range map[string]func(*tiles.Config){
		"no token":            func(c *tiles.Config) { c.Token = "" },
		"no username":         func(c *tiles.Config) { c.Username = "" },
		"no style":            func(c *tiles.Config) { c.StyleID = "" },
		"not a mapbox token":  func(c *tiles.Config) { c.Token = "not-a-token-" + mapboxTestToken },
		"token with newline":  func(c *tiles.Config) { c.Token += "\nX-Evil: 1" },
		"token with query":    func(c *tiles.Config) { c.Token += "&x=1" },
		"username with slash": func(c *tiles.Config) { c.Username = "a/b" },
		"username with dots":  func(c *tiles.Config) { c.Username = ".." },
		"style with query":    func(c *tiles.Config) { c.StyleID = "x?y=1" },
		"style with space":    func(c *tiles.Config) { c.StyleID = "my style" },
		"zoom above mapbox":   func(c *tiles.Config) { c.MaxZoom = 23 },
		"negative zoom":       func(c *tiles.Config) { c.MaxZoom = -1 },
		"negative rate":       func(c *tiles.Config) { c.RatePerMinute = -1 },
		"absurd rate":         func(c *tiles.Config) { c.RatePerMinute = 10_000_000 },
		"negative timeout":    func(c *tiles.Config) { c.UpstreamTimeout = -time.Second },
		"unbounded timeout":   func(c *tiles.Config) { c.UpstreamTimeout = time.Hour },
		"tiny response cap":   func(c *tiles.Config) { c.MaxResponseBytes = 10 },
		"huge response cap":   func(c *tiles.Config) { c.MaxResponseBytes = 1 << 30 },
	} {
		cfg := enabled()
		mutate(&cfg.MapboxTiles)
		err := cfg.Validate()
		if err == nil {
			t.Errorf("%s accepted", name)
			continue
		}
		// The startup error is what operators paste into tickets: no credential in it.
		if strings.Contains(err.Error(), mapboxTestToken) || strings.Contains(err.Error(), "eyJ1") {
			t.Errorf("%s: error leaks the token: %v", name, err)
		}
	}
}

func TestMapboxTilesConfigDoesNotPrintTheToken(t *testing.T) {
	cfg := tiles.Config{Enabled: true, Token: mapboxTestToken, Username: "mapbox", StyleID: "light-v11"}
	for _, out := range []string{fmt.Sprint(cfg), fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg), fmt.Sprint(&cfg)} {
		if strings.Contains(out, "eyJ1") {
			t.Errorf("configuration prints the token: %s", out)
		}
	}
}

func TestMapboxTilesConfigReadsYAML(t *testing.T) {
	var cfg AppConfig
	raw := "mapbox_tiles:\n  enabled: true\n  token: " + mapboxTestToken + "\n  username: mapbox\n  style_id: light-v11\n  max_zoom: 16\n  rate_per_minute: 120\n  upstream_timeout: 3s\n  max_response_bytes: 262144\n"
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	want := tiles.Config{Enabled: true, Token: mapboxTestToken, Username: "mapbox", StyleID: "light-v11", MaxZoom: 16, RatePerMinute: 120, UpstreamTimeout: 3 * time.Second, MaxResponseBytes: 262144}
	if cfg.MapboxTiles != want {
		t.Fatalf("parsed %+v", cfg.MapboxTiles)
	}
}
