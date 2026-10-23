package config

import (
	"testing"
)

func TestOnlyLogtoIdentityConfiguration(t *testing.T) {
	for _, name := range []string{"github", "google", "unknown"} {
		t.Run(name, func(t *testing.T) {
			cfg := AuthConfig{OAuthProviders: map[string]OAuthProvider{name: {}}}
			if err := cfg.validateOAuth(); err == nil {
				t.Fatal("retired provider accepted")
			}
		})
	}
	if err := (&AuthConfig{RegistrationDisabled: true}).validateOAuth(); err == nil {
		t.Fatal("obsolete registration policy accepted")
	}
	if err := (&AuthConfig{OAuthProviders: map[string]OAuthProvider{"logto": {ClientID: "only-id"}}}).validateOAuth(); err == nil {
		t.Fatal("partial credentials accepted")
	}
	if err := (&AuthConfig{}).validateOAuth(); err != nil {
		t.Fatalf("local-only deployments must remain valid: %v", err)
	}
}
