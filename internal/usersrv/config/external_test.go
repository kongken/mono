package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func enabledExternal() ExternalConfig {
	return ExternalConfig{Enabled: true, ProofKeyID: "k1", Providers: map[string]ExternalProviderConfig{
		"google": {Enabled: true, ClientID: "google-client", ClientSecret: "google-secret", RedirectURI: "https://api.example.com/api/identity/callback/google", ConfigVersion: 1},
		"github": {Enabled: true, ClientID: "github-client", ClientSecret: "github-secret", RedirectURI: "https://api.example.com/api/identity/callback/github", ConfigVersion: 1},
	}}
}

func TestExternalGatesAreIndependentOfRegistration(t *testing.T) {
	c := valid()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Auth.External.Enabled || c.Auth.External.ProviderEnabled("google") || c.Auth.External.ProviderEnabled("github") || !*c.Auth.External.RequireVerifiedEmail {
		t.Fatal("external authentication must default off with verified-email policy on")
	}
	for _, registration := range []bool{true, false} {
		for _, global := range []bool{true, false} {
			for _, google := range []bool{true, false} {
				c := valid()
				c.Auth.RegistrationEnabled = &registration
				c.Auth.External = enabledExternal()
				c.Auth.External.Enabled = global
				p := c.Auth.External.Providers["google"]
				p.Enabled = google
				c.Auth.External.Providers["google"] = p
				if err := c.Validate(); err != nil {
					t.Fatal(err)
				}
				if c.Auth.RegistrationOpen() != registration || c.Auth.External.ProviderEnabled("google") != (global && google) || c.Auth.External.ProviderEnabled("github") != global || c.Auth.External.ProviderEnabled("other") {
					t.Fatal("registration/global/provider gates were coupled")
				}
			}
		}
	}
	// Disabled providers need neither credentials nor mounted secrets, even
	// when the other provider is enabled. Validation never contacts endpoints.
	c.Auth.External = enabledExternal()
	c.Auth.External.Providers["github"] = ExternalProviderConfig{ClientSecretFile: "/absent/mounted-secret"}
	if err := c.Validate(); err != nil || c.Auth.External.ProviderEnabled("github") {
		t.Fatalf("disabled provider: %v", err)
	}
}

func TestExternalStartupValidationAndSecretRedaction(t *testing.T) {
	for name, change := range map[string]func(*ExternalConfig){
		"unknown provider": func(e *ExternalConfig) { e.Providers["other"] = ExternalProviderConfig{} },
		"no proof key":     func(e *ExternalConfig) { e.ProofKeyID = "absent" },
		"relaxed email":    func(e *ExternalConfig) { off := false; e.RequireVerifiedEmail = &off },
		"empty client":     func(e *ExternalConfig) { p := e.Providers["google"]; p.ClientID = ""; e.Providers["google"] = p },
		"no secret":        func(e *ExternalConfig) { p := e.Providers["google"]; p.ClientSecret = ""; e.Providers["google"] = p },
		"two secrets": func(e *ExternalConfig) {
			p := e.Providers["google"]
			p.ClientSecretFile = "/secret/path"
			e.Providers["google"] = p
		},
		"bad generation": func(e *ExternalConfig) { p := e.Providers["google"]; p.ConfigVersion = 0; e.Providers["google"] = p },
	} {
		t.Run(name, func(t *testing.T) {
			c := valid()
			c.Auth.External = enabledExternal()
			change(&c.Auth.External)
			if err := c.Validate(); err == nil || strings.Contains(err.Error(), "google-secret") || strings.Contains(err.Error(), "/secret/path") {
				t.Fatalf("unsafe or accepted configuration: %v", err)
			}
		})
	}
	for _, raw := range []string{
		"http://api.example.com/api/identity/callback/google",
		"https://user:password@api.example.com/api/identity/callback/google",
		"https://api.example.com/api/identity/callback/github",
		"https://api.example.com/api/identity/callback/google?secret=value",
		"https://api.example.com/api/identity/callback/google#value",
		"https://api.example.com/api/identity/callback/%67oogle",
		"/api/identity/callback/google", "https://%zz",
	} {
		c := valid()
		c.Auth.External = enabledExternal()
		p := c.Auth.External.Providers["google"]
		p.RedirectURI = raw
		c.Auth.External.Providers["google"] = p
		if err := c.Validate(); err == nil || strings.Contains(err.Error(), raw) {
			t.Fatalf("redirect accepted or echoed: %v", err)
		}
	}
	c := valid()
	c.Auth.External = enabledExternal()
	var log bytes.Buffer
	slog.New(slog.NewTextHandler(&log, nil)).Info("config", "external", c.Auth.External, "provider", c.Auth.External.Providers["google"])
	encoded, err := json.Marshal(c.Auth.External)
	if err != nil {
		t.Fatal(err)
	}
	formatted := fmt.Sprintf("%v %+v %#v %v %+v %#v", c.Auth.External, c.Auth.External, c.Auth.External, c.Auth.External.Providers, c.Auth.External.Providers, c.Auth.External.Providers)
	for _, output := range []string{log.String(), string(encoded), formatted} {
		if strings.Contains(output, "google-secret") || strings.Contains(output, "github-secret") {
			t.Fatal("external secret leaked through config formatting")
		}
	}
}

func TestExternalMountedSecretAndLoopbackOptIn(t *testing.T) {
	file := filepath.Join(t.TempDir(), "client-secret")
	if err := os.WriteFile(file, []byte("mounted-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := valid()
	c.Auth.AllowLoopbackHTTP = true
	c.Auth.External = enabledExternal()
	p := c.Auth.External.Providers["google"]
	p.ClientSecret, p.ClientSecretFile = "", file
	p.RedirectURI = "http://127.0.0.1:18081/api/identity/callback/google"
	c.Auth.External.Providers["google"] = p
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if secret, err := p.ClientSecretValue(); err != nil || secret != "mounted-secret" {
		t.Fatal("mounted secret failed")
	}
	c.Auth.AllowLoopbackHTTP = false
	if err := c.Validate(); err == nil {
		t.Fatal("loopback HTTP without opt-in")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ClientSecretValue(); err == nil || strings.Contains(err.Error(), file) {
		t.Fatal("missing file accepted or path leaked")
	}
}
