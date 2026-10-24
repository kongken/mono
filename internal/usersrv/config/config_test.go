package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"github.com/kongken/mono/internal/usersrv/mailer"
	"github.com/kongken/mono/internal/usersrv/outbox"
	"gopkg.in/yaml.v3"
)

func valid() *AppConfig {
	return &AppConfig{
		DBStore: "main", RedisStore: "main", KeyPrefix: "usersrv:",
		GRPC: grpcx.ServerConfig{Transport: "h2c", Listen: "127.0.0.1:18087"},
		InternalAuth: servicetoken.VerifyConfig{Issuer: "tripmap-bff", Audience: "usersrv",
			TrustedKeys: []servicetoken.TrustedKey{{KID: "k", PublicKey: "x"}}},
		Auth: AuthConfig{
			Apps:    map[string]AppOrigin{"trip-map": {Origin: "https://map.example.com"}, "metro": {Origin: "https://metro.example.com"}},
			HMACKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
		},
		Mail: validMail(),
	}
}

func validMail() MailConfig {
	return MailConfig{
		Config: mailer.Config{
			Provider: "mailgun", FromEmail: "no-reply@example.com", FromName: "山海足迹",
			Mailgun: mailer.MailgunConfig{APIKey: "key-secret", Domain: "mg.example.com", Region: "eu"},
		},
		Encryption: outbox.EncryptionConfig{ActiveKeyID: "k1", Keys: []outbox.KeyConfig{
			{ID: "k1", Key: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("e", 32)))},
		}},
	}
}

func TestValidConfigGetsTheSpecifiedDefaults(t *testing.T) {
	c := valid()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if !c.Auth.RegistrationOpen() || c.Auth.RegistrationTTL != "30m" || c.Auth.ResetTTL != "15m" {
		t.Fatalf("auth defaults: %+v", c.Auth)
	}
	h, err := c.Auth.PasswordHash.Resolve()
	if err != nil || h.MemoryKiB != 64*1024 || h.Iterations != 3 || h.Parallelism != 1 || h.MaxConcurrent != 2 || h.SaltBytes != 16 || h.KeyBytes != 32 {
		t.Fatalf("hash defaults: %+v %v", h, err)
	}
	l, err := c.RateLimit.Limits()
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][2]any{
		"password_per_account":   {l.PasswordPerAccount.Limit, l.PasswordPerAccount.Window},
		"password_per_ip":        {l.PasswordPerIP.Limit, l.PasswordPerIP.Window},
		"profile_write_per_user": {l.ProfileWritePerUser.Limit, l.ProfileWritePerUser.Window},
		"deletion_per_user":      {l.DeletionPerUser.Limit, l.DeletionPerUser.Window},
		"token_submit_per_ip":    {l.TokenSubmitPerIP.Limit, l.TokenSubmitPerIP.Window},
		"token_submit_per_token": {l.TokenSubmitPerToken.Limit, l.TokenSubmitPerToken.Window},
	} {
		want := map[string][2]any{
			"password_per_account":   {10, 15 * time.Minute},
			"password_per_ip":        {40, 15 * time.Minute},
			"profile_write_per_user": {30, time.Minute},
			"deletion_per_user":      {10, 15 * time.Minute},
			"token_submit_per_ip":    {30, 15 * time.Minute},
			"token_submit_per_token": {5, 15 * time.Minute},
		}[name]
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
}

func TestStartupRefusesIncompleteOrUnsafeConfig(t *testing.T) {
	off := false
	cases := map[string]func(*AppConfig){
		"no db_store":          func(c *AppConfig) { c.DBStore = "" },
		"no redis_store":       func(c *AppConfig) { c.RedisStore = "" },
		"no key prefix":        func(c *AppConfig) { c.KeyPrefix = "" },
		"bad key prefix":       func(c *AppConfig) { c.KeyPrefix = "a b*" },
		"no grpc listen":       func(c *AppConfig) { c.GRPC.Listen = "" },
		"tls without certs":    func(c *AppConfig) { c.GRPC.Transport = "tls" },
		"no internal auth":     func(c *AppConfig) { c.InternalAuth = servicetoken.VerifyConfig{} },
		"wrong audience":       func(c *AppConfig) { c.InternalAuth.Audience = "metro" },
		"missing trip-map":     func(c *AppConfig) { delete(c.Auth.Apps, "trip-map") },
		"missing metro":        func(c *AppConfig) { delete(c.Auth.Apps, "metro") },
		"http origin":          func(c *AppConfig) { c.Auth.Apps["metro"] = AppOrigin{Origin: "http://metro.example.com"} },
		"origin with path":     func(c *AppConfig) { c.Auth.Apps["metro"] = AppOrigin{Origin: "https://metro.example.com/app"} },
		"origin with slash":    func(c *AppConfig) { c.Auth.Apps["metro"] = AppOrigin{Origin: "https://metro.example.com/"} },
		"origin with query":    func(c *AppConfig) { c.Auth.Apps["metro"] = AppOrigin{Origin: "https://metro.example.com?x=1"} },
		"origin with userinfo": func(c *AppConfig) { c.Auth.Apps["metro"] = AppOrigin{Origin: "https://u:p@metro.example.com"} },
		"origin without host":  func(c *AppConfig) { c.Auth.Apps["metro"] = AppOrigin{Origin: "https://"} },
		"bare host":            func(c *AppConfig) { c.Auth.Apps["metro"] = AppOrigin{Origin: "metro.example.com"} },
		"javascript origin":    func(c *AppConfig) { c.Auth.Apps["metro"] = AppOrigin{Origin: "javascript:alert(1)"} },
		"empty origin":         func(c *AppConfig) { c.Auth.Apps["metro"] = AppOrigin{} },
		"bad app id":           func(c *AppConfig) { c.Auth.Apps["Bad App"] = AppOrigin{Origin: "https://x.example.com"} },
		"no hmac key":          func(c *AppConfig) { c.Auth.HMACKey = "" },
		"short hmac key":       func(c *AppConfig) { c.Auth.HMACKey = base64.StdEncoding.EncodeToString([]byte("short")) },
		"non-base64 hmac":      func(c *AppConfig) { c.Auth.HMACKey = "%%%" },
		"both hmac sources":    func(c *AppConfig) { c.Auth.HMACKeyFile = "/nonexistent" },
		"missing hmac file":    func(c *AppConfig) { c.Auth.HMACKey, c.Auth.HMACKeyFile = "", "/nonexistent/key" },
		"token ttl too short":  func(c *AppConfig) { c.Auth.ResetTTL = "10s" },
		"token ttl too long":   func(c *AppConfig) { c.Auth.RegistrationTTL = "48h" },
		"token ttl garbage":    func(c *AppConfig) { c.Auth.RegistrationTTL = "soon" },
		"weak memory":          func(c *AppConfig) { c.Auth.PasswordHash.MemoryKiB = 1024 },
		"weak iterations":      func(c *AppConfig) { c.Auth.PasswordHash.Iterations = 1 },
		"huge concurrency":     func(c *AppConfig) { c.Auth.PasswordHash.MaxConcurrent = 100 },
		"bad wait":             func(c *AppConfig) { c.Auth.PasswordHash.WaitTimeout = "forever" },
		"zero-sized window":    func(c *AppConfig) { c.RateLimit.PasswordPerIP = LimitConfig{Limit: 5, Window: "0s"} },
		"negative limit":       func(c *AppConfig) { c.RateLimit.ProfileWritePerUser = LimitConfig{Limit: -1, Window: "1m"} },
		"garbage window":       func(c *AppConfig) { c.RateLimit.DeletionPerUser = LimitConfig{Limit: 5, Window: "often"} },
		"garbage mail window":  func(c *AppConfig) { c.RateLimit.MailGlobal = LimitConfig{Limit: 5, Window: "often"} },
		"no mail section":      func(c *AppConfig) { c.Mail = MailConfig{} },
		"no mail provider":     func(c *AppConfig) { c.Mail.Provider = "" },
		"mail without keys":    func(c *AppConfig) { c.Mail.Encryption = outbox.EncryptionConfig{} },
		"mail key is the hmac": func(c *AppConfig) { c.Mail.Encryption.Keys[0].Key = c.Auth.HMACKey },
		"active key unknown":   func(c *AppConfig) { c.Mail.Encryption.ActiveKeyID = "other" },
	}
	for name, mutate := range cases {
		c := valid()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The mail limits get the specified defaults.
	d := valid()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	ml, err := d.RateLimit.Limits()
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][2]any{
		"registration ip":       {ml.Mail.RegistrationPerIP.Limit, ml.Mail.RegistrationPerIP.Window},
		"reset ip":              {ml.Mail.ResetPerIP.Limit, ml.Mail.ResetPerIP.Window},
		"registration cooldown": {ml.Mail.RegistrationEmailCooldown.Limit, ml.Mail.RegistrationEmailCooldown.Window},
		"registration email":    {ml.Mail.RegistrationPerEmail.Limit, ml.Mail.RegistrationPerEmail.Window},
		"reset cooldown":        {ml.Mail.ResetEmailCooldown.Limit, ml.Mail.ResetEmailCooldown.Window},
		"reset email":           {ml.Mail.ResetPerEmail.Limit, ml.Mail.ResetPerEmail.Window},
		"all mail per email":    {ml.Mail.MailPerEmail.Limit, ml.Mail.MailPerEmail.Window},
		"global":                {ml.Mail.MailGlobal.Limit, ml.Mail.MailGlobal.Window},
	} {
		want := map[string][2]any{
			"registration ip": {20, time.Hour}, "reset ip": {20, time.Hour},
			"registration cooldown": {1, time.Minute}, "registration email": {5, time.Hour},
			"reset cooldown": {1, time.Minute}, "reset email": {5, time.Hour},
			"all mail per email": {8, time.Hour}, "global": {200, time.Hour},
		}[name]
		if got != want {
			t.Errorf("mail limit %s = %v, want %v", name, got, want)
		}
	}
	// Disabling registration is an explicit choice and stays valid.
	c := valid()
	c.Auth.RegistrationEnabled = &off
	if err := c.Validate(); err != nil || c.Auth.RegistrationOpen() {
		t.Fatalf("registration off: %v open=%v", err, c.Auth.RegistrationOpen())
	}
}

func TestLoopbackHTTPOnlyWhenExplicitlyAllowed(t *testing.T) {
	c := valid()
	c.Auth.Apps["metro"] = AppOrigin{Origin: "http://127.0.0.1:5174"}
	if err := c.Validate(); err == nil {
		t.Fatal("loopback http accepted without the development switch")
	}
	c.Auth.AllowLoopbackHTTP = true
	if err := c.Validate(); err != nil {
		t.Fatalf("loopback http with the switch: %v", err)
	}
	for _, o := range []string{"http://localhost:5173", "http://[::1]:5173"} {
		c.Auth.Apps["metro"] = AppOrigin{Origin: o}
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", o, err)
		}
	}
	// The switch never opens plain http for a public host.
	c.Auth.Apps["metro"] = AppOrigin{Origin: "http://metro.example.com"}
	if err := c.Validate(); err == nil {
		t.Fatal("public http host accepted")
	}
}

func TestHMACKeyFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hmac")
	if err := os.WriteFile(file, []byte(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("z", 40)))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := valid()
	c.Auth.HMACKey, c.Auth.HMACKeyFile = "", file
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if k, err := c.Auth.HMACKeyBytes(); err != nil || len(k) != 40 {
		t.Fatalf("%d %v", len(k), err)
	}
}

// The checked-in example must load with the production validation, so it cannot
// drift from the code, and its sample HMAC key must be the documented local value.
func TestExampleConfigLoadsAndValidates(t *testing.T) {
	raw, err := os.ReadFile("../../../config.usersrv.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c := new(AppConfig)
	if err = yaml.Unmarshal(raw, c); err != nil {
		t.Fatal(err)
	}
	if err = c.Validate(); err != nil {
		t.Fatalf("example config: %v", err)
	}
	if c.GRPC.Listen != "127.0.0.1:18087" || c.InternalAuth.Audience != "usersrv" || c.KeyPrefix == "" || c.RedisStore == "" {
		t.Fatalf("example config drifted: %+v", c)
	}
	// The local sample secrets are public; make sure nobody mistakes them for strong defaults.
	if strings.Count(string(raw), "public local sample") < 2 {
		t.Fatal("a sample key lost its warning")
	}
	if _, err = c.Mail.Keyring(); err != nil || c.Mail.Provider != "smtp" || c.Mail.SMTP.TLSMode != "plain" {
		t.Fatalf("example mail section: %+v %v", c.Mail, err)
	}
}
