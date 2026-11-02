package config

import (
	"encoding/base64"
	"github.com/kongken/mono/internal/bff/navigation"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalDefaultsAndKeyValidation(t *testing.T) {
	var e ExternalConfig
	if e.Allows("google") || e.Allows("github") || e.Lifetime() != 15*time.Minute || e.ConfirmationLifetime() != 10*time.Minute {
		t.Fatal("unsafe external defaults")
	}
	if err := e.Validate(navigation.Config{}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte(base64.StdEncoding.EncodeToString(make([]byte, 32))), 0600); err != nil {
		t.Fatal(err)
	}
	nav := navigation.Config{APIOrigin: "https://api.example.com", Apps: map[string]navigation.App{"ozid": {Origin: "https://id.example.com"}}}
	valid := ExternalConfig{Enabled: true, Providers: map[string]bool{"google": true}, StateEncryptionKeyID: "k1", StateEncryptionKeyFiles: map[string]string{"k1": file}}
	if err := valid.Validate(nav); err != nil {
		t.Fatal(err)
	}
	if !valid.Allows("google") || valid.Allows("github") || valid.Allows("unknown") {
		t.Fatal("provider gates coupled")
	}
	for name, edit := range map[string]func(*ExternalConfig){"missing key": func(c *ExternalConfig) { c.StateEncryptionKeyID = "absent" }, "bad ttl": func(c *ExternalConfig) { c.StateTTL = 16 * time.Minute }, "bad confirmation ttl": func(c *ExternalConfig) { c.ConfirmationTTL = -time.Minute }, "unknown provider": func(c *ExternalConfig) { c.Providers = map[string]bool{"oidc": true} }, "bad file": func(c *ExternalConfig) { c.StateEncryptionKeyFiles = map[string]string{"k1": "missing-private-path"} }} {
		t.Run(name, func(t *testing.T) {
			c := valid
			edit(&c)
			if c.Validate(nav) == nil {
				t.Fatal("invalid external configuration accepted")
			}
		})
	}
}
