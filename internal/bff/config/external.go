package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kongken/mono/internal/bff/navigation"
)

// ExternalConfig contains only BFF state encryption, never OAuth client secrets.
type ExternalConfig struct {
	Enabled                 bool              `yaml:"enabled"`
	Providers               map[string]bool   `yaml:"providers"`
	ProviderConfigVersions  map[string]int64  `yaml:"provider_config_versions"`
	StateTTL                time.Duration     `yaml:"state_ttl"`
	ConfirmationTTL         time.Duration     `yaml:"confirmation_ttl"`
	StateEncryptionKeyID    string            `yaml:"state_encryption_key_id"`
	StateEncryptionKeyFiles map[string]string `yaml:"state_encryption_key_files" json:"-"`
}

func (e ExternalConfig) Lifetime() time.Duration {
	if e.StateTTL == 0 {
		return 15 * time.Minute
	}
	return e.StateTTL
}
func (e ExternalConfig) ConfirmationLifetime() time.Duration {
	if e.ConfirmationTTL == 0 {
		return 10 * time.Minute
	}
	return e.ConfirmationTTL
}
func (e ExternalConfig) Allows(p string) bool {
	return e.Enabled && (p == "google" || p == "github") && e.Providers[p]
}
func (e ExternalConfig) ProviderVersion(p string) int64 {
	if v, ok := e.ProviderConfigVersions[p]; ok {
		return v
	}
	return 1
}
func (e ExternalConfig) LoadKeys() (map[string][]byte, error) {
	keys := map[string][]byte{}
	for id, path := range e.StateEncryptionKeyFiles {
		if id == "" || path == "" {
			return nil, fmt.Errorf("auth.external state key ID and file must be non-empty")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("auth.external state key %q cannot be read", id)
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(b) != 32 {
			return nil, fmt.Errorf("auth.external state key %q must contain base64 of 32 bytes", id)
		}
		keys[id] = b
	}
	if e.Enabled && len(keys[e.StateEncryptionKeyID]) != 32 {
		return nil, fmt.Errorf("auth.external.state_encryption_key_id must name a configured key")
	}
	return keys, nil
}
func (e ExternalConfig) Validate(nav navigation.Config) error {
	if e.Lifetime() <= 0 || e.Lifetime() > 15*time.Minute || e.ConfirmationLifetime() <= 0 || e.ConfirmationLifetime() > 10*time.Minute {
		return fmt.Errorf("auth.external TTLs must be positive and at most 15m/10m")
	}
	for p := range e.Providers {
		if p != "google" && p != "github" {
			return fmt.Errorf("auth.external.providers: unknown provider")
		}
	}
	for p, v := range e.ProviderConfigVersions {
		if (p != "google" && p != "github") || v <= 0 {
			return fmt.Errorf("auth.external.provider_config_versions requires known providers and positive versions")
		}
	}
	if e.Enabled && (nav.APIOrigin == "" || nav.Apps[navigation.AppOzID].Origin == "") {
		return fmt.Errorf("auth.external requires API and OzID navigation origins")
	}
	_, err := e.LoadKeys()
	return err
}
